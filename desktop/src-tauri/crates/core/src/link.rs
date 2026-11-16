// SPDX-License-Identifier: AGPL-3.0-only

//! The links `canonkeeper://open?…` (specs/2026-11-16-desk-app-links.md AL1): the system gives one to the shell as an
//! argument of its first start or of a second start the one instance hears, and the link only opens a place in a book —
//! it changes nothing and sends nothing. A link is an untrusted input of any process of the user. Here is all of it that
//! needs no window: the link out of the arguments (AL1.2), its check (AL1.1), the address of the page of the edition the
//! window goes to, built only of the checked values, and the script that gives the loaded page that address (AL1.4), what the
//! supervision does with a link by where the window is and how often the window moves (AL1.3, AL1.4), the mark that keeps
//! a restart from opening the link of the first start again (AL1.5), and the words of the log (AL1.7) — a fact and a
//! reason, never an id or a number of the link.

use std::ffi::OsStr;
use std::fs;
use std::io;
use std::path::Path;
use std::time::{Duration, Instant};

use sha2::{Digest, Sha256};

use crate::paths::System;

/// The scheme of the links, without `://`; the system gives it in any case.
pub const SCHEME: &str = "canonkeeper";
/// The longest link the shell reads: a right one is about 130 characters.
pub const LONGEST: usize = 300;
/// The page of the edition of the app that finds the chapter and the paragraph of a place (AL1.6).
pub const PAGE: &str = "/manuscript/open";
/// The window moves to a link at most once in this time; the links between are folded into the last (AL1.4).
pub const INTERVAL: Duration = Duration::from_secs(2);
/// The mark of the link of a start that is followed by a restart of the program (AL1.5), in the data directory.
pub const MARK: &str = "link-mark.json";

/// The script that gives the page of the backend the place of a link (AL1.4), with its one argument.
const VISIT: &str = include_str!("visit.js");
const VISIT_ARGUMENT: &str = "(CANONKEEPER_VISIT_TO)";

/// The link a start brought, as it came: one argument of the scheme, or more than one, which is refused (AL1.2). It never
/// shows in a debug print.
#[derive(Clone, PartialEq, Eq)]
pub struct Argument {
    text: String,
    many: bool,
}

impl Argument {
    /// One link, as a start brings it.
    pub fn new(text: &str) -> Argument {
        Argument { text: text.to_string(), many: false }
    }
}

impl std::fmt::Debug for Argument {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        formatter.write_str("Argument(…)")
    }
}

/// The link among the arguments of a start, after the program (AL1.2): exactly one argument of the scheme, in any case;
/// an argument that is not UTF-8 is not looked at — nor one the one instance of a second start gave with the replacement
/// character in place of its broken bytes —; the first argument `mcp` off Windows is the bridge for the programs of the
/// author (specs/2026-11-15-desk-mcp.md MC1.1), never a link.
pub fn of_arguments<S: AsRef<OsStr>>(arguments: &[S], system: System) -> Option<Argument> {
    let bridge = arguments.first().is_some_and(|first| first.as_ref() == OsStr::new(crate::mcp::ARGUMENT));
    if system != System::Windows && bridge {
        return None;
    }
    let links: Vec<&str> = arguments
        .iter()
        .filter_map(|argument| argument.as_ref().to_str())
        .filter(|text| of_scheme(text) && !text.contains(char::REPLACEMENT_CHARACTER))
        .collect();
    match links.as_slice() {
        [] => None,
        [one] => Some(Argument::new(one)),
        many => Some(Argument { text: many.join("\n"), many: true }),
    }
}

/// Whether a text begins with the scheme and its colon, in any case.
fn of_scheme(text: &str) -> bool {
    text.as_bytes().get(..SCHEME.len() + 1).is_some_and(|start| {
        start[..SCHEME.len()].eq_ignore_ascii_case(SCHEME.as_bytes()) && start[SCHEME.len()] == b':'
    })
}

/// Why a link is refused (AL1.7): its scheme, its action, a parameter that is unknown, repeated, missing or not
/// `name=value`, a value of the wrong form, its length, or more than one link in one start.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Refusal {
    Scheme,
    Action,
    Parameter,
    Value,
    Length,
    Many,
}

impl Refusal {
    /// The name in the log.
    pub fn name(self) -> &'static str {
        match self {
            Refusal::Scheme => "scheme",
            Refusal::Action => "action",
            Refusal::Parameter => "parameter",
            Refusal::Value => "value",
            Refusal::Length => "length",
            Refusal::Many => "many",
        }
    }
}

/// A place in a book a link names, checked: the saga and the chapter by their ids in the lower case, the number of the
/// paragraph from one and the revision of the published text, when the link names them.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Place {
    saga: String,
    chapter: String,
    paragraph: Option<u32>,
    revision: Option<u64>,
}

/// The greatest number of a paragraph a link names: the panel of the source of the chat takes five digits.
const LAST_PARAGRAPH: u32 = 99_999;
/// The greatest revision a link names: the page takes only a safe integer of JavaScript.
const LAST_REVISION: u64 = (1 << 53) - 1;

/// Checks a link (AL1.1): the raw text first — only `A–Z a–z 0–9 : / ? & = -`, up to 300 characters, so a coded, spaced or
/// quoted part is refused before anything reads it —, then the scheme in any case, the action `open` or `open/` as it is,
/// and the parameters `saga`, `chapter`, `p` and `rev`, each once.
pub fn parse(raw: &str) -> Result<Place, Refusal> {
    if raw.is_empty() || raw.len() > LONGEST {
        return Err(Refusal::Length);
    }
    if !of_scheme(raw) {
        return Err(Refusal::Scheme);
    }
    if !raw.bytes().all(|byte| byte.is_ascii_alphanumeric() || b":/?&=-".contains(&byte)) {
        return Err(Refusal::Value);
    }
    let rest = raw[SCHEME.len() + 1..].strip_prefix("//").ok_or(Refusal::Action)?;
    let (action, query) = match rest.split_once('?') {
        Some((action, query)) => (action, Some(query)),
        None => (rest, None),
    };
    if action != "open" && action != "open/" {
        return Err(Refusal::Action);
    }
    let (mut saga, mut chapter, mut paragraph, mut revision) = (None, None, None, None);
    for pair in query.ok_or(Refusal::Parameter)?.split('&') {
        let (name, value) = pair.split_once('=').ok_or(Refusal::Parameter)?;
        let slot = match name {
            "saga" => &mut saga,
            "chapter" => &mut chapter,
            "p" => &mut paragraph,
            "rev" => &mut revision,
            _ => return Err(Refusal::Parameter),
        };
        if slot.replace(value).is_some() {
            return Err(Refusal::Parameter);
        }
    }
    Ok(Place {
        saga: id(saga.ok_or(Refusal::Parameter)?)?,
        chapter: id(chapter.ok_or(Refusal::Parameter)?)?,
        paragraph: paragraph.map(|value| number(value, LAST_PARAGRAPH as u64)).transpose()?.map(|value| value as u32),
        revision: revision.map(|value| number(value, LAST_REVISION)).transpose()?,
    })
}

/// A canonical id: 36 characters, hex in groups of 8-4-4-4-12; the upper case goes to the lower.
fn id(value: &str) -> Result<String, Refusal> {
    let canonical = value.len() == 36
        && value.bytes().enumerate().all(|(at, byte)| match at {
            8 | 13 | 18 | 23 => byte == b'-',
            _ => byte.is_ascii_hexdigit(),
        });
    canonical.then(|| value.to_ascii_lowercase()).ok_or(Refusal::Value)
}

/// A number from one up to `last`, in decimal digits without a leading zero.
fn number(value: &str, last: u64) -> Result<u64, Refusal> {
    let plain = !value.is_empty() && !value.starts_with('0') && value.bytes().all(|byte| byte.is_ascii_digit());
    match value.parse::<u64>() {
        Ok(number) if plain && number <= last => Ok(number),
        _ => Err(Refusal::Value),
    }
}

/// Where a link leads the window: a checked place, or the page that says the link has a mistake — the author pressed it
/// and must see what happened.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Target {
    Place(Place),
    Refused,
}

impl Target {
    /// The address of the page of the edition (AL1.4), built only of the checked values.
    pub fn path(&self) -> String {
        match self {
            Target::Place(place) => {
                let mut path = format!("{PAGE}?saga={}&chapter={}", place.saga, place.chapter);
                if let Some(paragraph) = place.paragraph {
                    path.push_str(&format!("&p={paragraph}"));
                }
                if let Some(revision) = place.revision {
                    path.push_str(&format!("&rev={revision}"));
                }
                path
            }
            Target::Refused => format!("{PAGE}?refused=1"),
        }
    }
}

/// Where the link of a start leads, and why it was refused, if it was.
pub fn target(argument: &Argument) -> (Target, Option<Refusal>) {
    if argument.many {
        return (Target::Refused, Some(Refusal::Many));
    }
    match parse(&argument.text) {
        Ok(place) => (Target::Place(place), None),
        Err(refusal) => (Target::Refused, Some(refusal)),
    }
}

/// The script the window runs to give its loaded page the address of `target` (AL1.4): a constant script whose only
/// argument is the address as a string of JSON. It does not touch the history: it sends the page the event
/// `canonkeeper:link`, and the edition of the app follows the address by the navigation of its router, as a link pressed
/// inside the app — a page with unsaved words asks first. On any page but one of its backend it does nothing, and it
/// answers whether the page took the address.
pub fn visit_script(target: &Target) -> String {
    let to = serde_json::to_string(&target.path()).unwrap_or_default();
    VISIT.replacen(VISIT_ARGUMENT, &format!("({to})"), 1)
}

/// Why a link is dropped (AL1.7): the window closes, an update is installed, the data are erased, the failure window
/// stands, the window leaves for an installation, or the link of the first start came again with a restart (AL1.5).
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Dropped {
    Closing,
    Installing,
    Erasing,
    Failure,
    Leaving,
    Replayed,
}

impl Dropped {
    /// The name in the log.
    pub fn name(self) -> &'static str {
        match self {
            Dropped::Closing => "closing",
            Dropped::Installing => "installing",
            Dropped::Erasing => "erasing",
            Dropped::Failure => "failure",
            Dropped::Leaving => "leaving",
            Dropped::Replayed => "replayed",
        }
    }
}

/// Where the window is, as a link sees it (AL1.3): the backend starts, its page is open, or no link is taken now.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Stage {
    Starting,
    Open,
    Gone(Dropped),
}

/// What the supervision does for a link.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Done {
    /// Raise the window and give its page the target: `link_opened`.
    Visit(Target),
    /// The link the window moved to last, again within the interval: only raise the window.
    Raise,
    /// A newer link took the place of the one that waited: `link_merged`.
    Merged,
    /// The link is not taken: `link_dropped <reason>`.
    Dropped(Dropped),
}

/// The links of a run of the shell (AL1.3, AL1.4): one waits — for the page of an opening of the window, or for the
/// interval since the last move —, an opening carries it to its first page, and the page of the opening, once loaded,
/// lets the next ones move the window.
#[derive(Debug, Default)]
pub struct Links {
    /// The last link that waits.
    waiting: Option<Target>,
    /// The link the opening that runs carries to its first page.
    carried: Option<Target>,
    /// The link the window moved to last, and when.
    last: Option<(Target, Instant)>,
    /// The number of the opening that runs, and whether its page is loaded.
    opening: u64,
    loaded: bool,
}

impl Links {
    /// A link came (AL1.3): it waits while the backend starts or the page of the opening loads; it moves the window once
    /// the page is loaded; it is dropped when no link is taken.
    pub fn arrive(&mut self, target: Target, stage: Stage, now: Instant) -> Vec<Done> {
        match stage {
            Stage::Gone(why) => vec![Done::Dropped(why)],
            Stage::Open if self.loaded => self.move_to(target, now),
            _ => self.hold(target),
        }
    }

    fn hold(&mut self, target: Target) -> Vec<Done> {
        match self.waiting.replace(target) {
            Some(_) => vec![Done::Merged],
            None => vec![],
        }
    }

    /// The page is loaded: the window moves at once, or the link waits out the interval since the last move; the link of
    /// the last move again within it only raises the window.
    fn move_to(&mut self, target: Target, now: Instant) -> Vec<Done> {
        match &self.last {
            Some((last, at)) if now.duration_since(*at) < INTERVAL => {
                if *last != target {
                    return self.hold(target);
                }
                let mut done = Vec::new();
                if self.waiting.take().is_some() {
                    done.push(Done::Merged);
                }
                done.push(Done::Raise);
                done
            }
            _ => {
                self.last = Some((target.clone(), now));
                vec![Done::Visit(target)]
            }
        }
    }

    /// The window opens the page of the backend anew: the number of this opening, and the link that waits, which the
    /// opening carries to its first page (AL1.4) and which waits on until that page is loaded.
    pub fn opening(&mut self) -> (u64, Option<Target>) {
        self.opening += 1;
        self.loaded = false;
        self.carried = self.waiting.clone();
        (self.opening, self.carried.clone())
    }

    /// The page of an opening is loaded: only the page of the opening that runs counts (AL1.3).
    pub fn loaded(&mut self, opening: u64, stage: Stage, now: Instant) -> Vec<Done> {
        if opening != self.opening || stage != Stage::Open || self.loaded {
            return Vec::new();
        }
        self.loaded = true;
        if let Some(carried) = self.carried.take() {
            if self.waiting.as_ref() == Some(&carried) {
                self.waiting = None;
            }
            self.last = Some((carried, now));
        }
        match self.waiting.take() {
            Some(target) => self.move_to(target, now),
            None => Vec::new(),
        }
    }

    /// Time passes: the link that waited out the interval moves the window.
    pub fn tick(&mut self, stage: Stage, now: Instant) -> Vec<Done> {
        let waited = self.last.as_ref().is_none_or(|(_, at)| now.duration_since(*at) >= INTERVAL);
        if stage != Stage::Open || !self.loaded || !waited {
            return Vec::new();
        }
        match self.waiting.take() {
            Some(target) => self.move_to(target, now),
            None => Vec::new(),
        }
    }

    /// The window left the page of its opening: the page of the start, a failure, a departure for an installation, the
    /// end.
    pub fn left(&mut self) {
        self.loaded = false;
    }

    /// Where no link is taken, the link that waits goes too.
    pub fn settle(&mut self, stage: Stage) -> Vec<Done> {
        let Stage::Gone(why) = stage else { return Vec::new() };
        self.carried = None;
        match self.waiting.take() {
            Some(_) => vec![Done::Dropped(why)],
            None => Vec::new(),
        }
    }
}

#[derive(serde::Serialize, serde::Deserialize)]
struct Mark {
    /// SHA-256 of the link, in hex: the mark names no part of it.
    link: String,
}

fn fingerprint(argument: &Argument) -> String {
    Sha256::digest(argument.text.as_bytes()).iter().map(|byte| format!("{byte:02x}")).collect()
}

/// Marks the link of this start before the program starts again or the installer of Windows takes over (AL1.5): a restart
/// repeats the arguments of the first start.
pub fn mark(data_dir: &Path, argument: &Argument) -> io::Result<()> {
    let text = serde_json::to_vec(&Mark { link: fingerprint(argument) }).map_err(io::Error::other)?;
    let beside = data_dir.join(format!(".{MARK}.new"));
    fs::write(&beside, text)?;
    fs::rename(beside, data_dir.join(MARK))
}

/// Takes the mark away: an installation that did not begin leaves the app as it was.
pub fn unmark(data_dir: &Path) {
    let _ = fs::remove_file(data_dir.join(MARK));
}

/// Takes the mark away and says whether the link of this start is the one it marked: such a link is dropped once
/// (AL1.5); a start with another link or without one only takes the mark away.
pub fn replayed(data_dir: &Path, argument: Option<&Argument>) -> bool {
    let path = data_dir.join(MARK);
    let Ok(text) = fs::read(&path) else { return false };
    let _ = fs::remove_file(&path);
    let Some(argument) = argument else { return false };
    serde_json::from_slice::<Mark>(&text).is_ok_and(|mark| mark.link == fingerprint(argument))
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::ffi::OsString;

    const SAGA: &str = "0b7c1a52-5d1e-4f43-9f6b-2a8c1e0b9d44";
    const CHAPTER: &str = "7c3f0a52-5d1e-4f43-9f6b-2a8c1e0b9d45";

    fn place(paragraph: Option<u32>, revision: Option<u64>) -> Place {
        Place { saga: SAGA.into(), chapter: CHAPTER.into(), paragraph, revision }
    }

    // AL1.1: the right links — `open/`, the upper case of the scheme and of an id, the parameters in any order, without p and
    // rev — and the place they name.
    #[test]
    fn a_right_link_names_its_place() {
        let base = format!("canonkeeper://open?saga={SAGA}&chapter={CHAPTER}");
        for (link, want) in [
            (format!("{base}&p=15&rev=3"), place(Some(15), Some(3))),
            (base.clone(), place(None, None)),
            (format!("{base}&p=1"), place(Some(1), None)),
            (format!("{base}&rev=9007199254740991"), place(None, Some(9_007_199_254_740_991))),
            (format!("{base}&p=99999"), place(Some(99_999), None)),
            (format!("canonkeeper://open/?saga={SAGA}&chapter={CHAPTER}&p=4"), place(Some(4), None)),
            (format!("CanonKeeper://open?saga={SAGA}&chapter={CHAPTER}"), place(None, None)),
            (
                format!("CANONKEEPER://open?chapter={}&rev=2&saga={}", CHAPTER.to_uppercase(), SAGA.to_uppercase()),
                place(None, Some(2)),
            ),
        ] {
            assert_eq!(parse(&link), Ok(want), "{link}");
        }
    }

    // AL1.1: every kind of refusal, the raw text first.
    #[test]
    fn a_wrong_link_is_refused_by_its_reason() {
        let ok = format!("saga={SAGA}&chapter={CHAPTER}");
        for (link, refusal) in [
            (String::new(), Refusal::Length),
            (format!("canonkeeper://open?{ok}&p={}", "1".repeat(300)), Refusal::Length),
            (format!("https://open?{ok}"), Refusal::Scheme),
            (format!("canonkeepers://open?{ok}"), Refusal::Scheme),
            (format!("javascript://open?{ok}"), Refusal::Scheme),
            (format!("canonkeeper:open?{ok}"), Refusal::Action),
            (format!("canonkeeper://OPEN?{ok}"), Refusal::Action),
            (format!("canonkeeper://read?{ok}"), Refusal::Action),
            (format!("canonkeeper://open:8080?{ok}"), Refusal::Action),
            (format!("canonkeeper://open/chapter?{ok}"), Refusal::Action),
            (format!("canonkeeper://open//?{ok}"), Refusal::Action),
            ("canonkeeper://open".to_string(), Refusal::Parameter),
            ("canonkeeper://open?".to_string(), Refusal::Parameter),
            (format!("canonkeeper://open?{ok}&"), Refusal::Parameter),
            (format!("canonkeeper://open?{ok}&&p=1"), Refusal::Parameter),
            (format!("canonkeeper://open?{ok}&p"), Refusal::Parameter),
            (format!("canonkeeper://open?{ok}&x=1"), Refusal::Parameter),
            (format!("canonkeeper://open?{ok}&=1"), Refusal::Parameter),
            (format!("canonkeeper://open?{ok}&saga={SAGA}"), Refusal::Parameter),
            (format!("canonkeeper://open?{ok}&p=1&p=2"), Refusal::Parameter),
            (format!("canonkeeper://open?saga={SAGA}"), Refusal::Parameter),
            (format!("canonkeeper://open?chapter={CHAPTER}&p=1"), Refusal::Parameter),
            // The raw text is checked before anything reads it: a coded action, name or value, a fragment before the query,
            // would be refused later by another reason or not at all.
            (format!("canonkeeper://%6Fpen?{ok}"), Refusal::Value),
            (format!("canonkeeper://open?saga%3D{SAGA}&chapter={CHAPTER}"), Refusal::Value),
            (format!("canonkeeper://open?{ok}&%70=1"), Refusal::Value),
            (format!("canonkeeper://open#?{ok}"), Refusal::Value),
            (format!("canonkeeper://open?{ok}&p=1#"), Refusal::Value),
            // A user, a password, a fragment, a coded or spaced part, quotes, other signs: refused raw.
            (format!("canonkeeper://user@open?{ok}"), Refusal::Value),
            (format!("canonkeeper://user:pass@open?{ok}"), Refusal::Value),
            (format!("canonkeeper://open?{ok}#p=1"), Refusal::Value),
            (format!("canonkeeper://open?{ok}&p=%31"), Refusal::Value),
            (format!("canonkeeper://open?saga={SAGA}&chapter=%37c3f"), Refusal::Value),
            (format!("canonkeeper://open?{ok}&p=1+2"), Refusal::Value),
            (format!("canonkeeper://open?{ok}&p= 1"), Refusal::Value),
            (format!(" canonkeeper://open?{ok}"), Refusal::Scheme),
            (format!("canonkeeper://open?{ok}\n"), Refusal::Value),
            (format!("canonkeeper://open?{ok}\t"), Refusal::Value),
            (format!("canonkeeper://open?{ok}&p=\"1\""), Refusal::Value),
            (format!("canonkeeper://open?{ok}&p='1'"), Refusal::Value),
            (format!("canonkeeper://open?saga=javascript:alert(1)&chapter={CHAPTER}"), Refusal::Value),
            (format!("canonkeeper://open?{ok}&p=1."), Refusal::Value),
            (format!("canonkeeper://open?{ok}&p=глава"), Refusal::Value),
            // Values of the wrong form.
            (format!("canonkeeper://open?saga=&chapter={CHAPTER}"), Refusal::Value),
            (format!("canonkeeper://open?saga=javascript:alert&chapter={CHAPTER}"), Refusal::Value),
            (format!("canonkeeper://open?saga=data:text/html&chapter={CHAPTER}"), Refusal::Value),
            (format!("canonkeeper://open?saga={}&chapter={CHAPTER}", SAGA.replace('-', "")), Refusal::Value),
            (format!("canonkeeper://open?saga={SAGA}0&chapter={CHAPTER}"), Refusal::Value),
            (format!("canonkeeper://open?saga={}&chapter={CHAPTER}", SAGA.replacen('0', "g", 1)), Refusal::Value),
            (format!("canonkeeper://open?saga=chapter-16&chapter={CHAPTER}"), Refusal::Value),
            (format!("canonkeeper://open?saga={SAGA}&chapter={CHAPTER}/"), Refusal::Value),
            (format!("canonkeeper://open?{ok}&p=0"), Refusal::Value),
            (format!("canonkeeper://open?{ok}&p=015"), Refusal::Value),
            (format!("canonkeeper://open?{ok}&p=-1"), Refusal::Value),
            (format!("canonkeeper://open?{ok}&p=100000"), Refusal::Value),
            (format!("canonkeeper://open?{ok}&p="), Refusal::Value),
            (format!("canonkeeper://open?{ok}&p=1=2"), Refusal::Value),
            (format!("canonkeeper://open?{ok}&rev=0"), Refusal::Value),
            (format!("canonkeeper://open?{ok}&rev=9007199254740992"), Refusal::Value),
            (format!("canonkeeper://open?{ok}&rev=99999999999999999999999"), Refusal::Value),
        ] {
            assert_eq!(parse(&link), Err(refusal), "{link:?}");
        }
    }

    // AL1.4: the address of the page is built only of the checked values; a refused link leads to the page that says so.
    #[test]
    fn the_target_is_the_page_of_the_edition_built_of_the_checked_values() {
        let upper =
            format!("canonkeeper://open/?saga={}&chapter={}&rev=3&p=15", SAGA.to_uppercase(), CHAPTER.to_uppercase());
        let (target, refusal) = super::target(&Argument::new(&upper));
        assert_eq!(refusal, None);
        assert_eq!(target.path(), format!("/manuscript/open?saga={SAGA}&chapter={CHAPTER}&p=15&rev=3"));
        let (bare, _) = super::target(&Argument::new(&format!("canonkeeper://open?chapter={CHAPTER}&saga={SAGA}")));
        assert_eq!(bare.path(), format!("/manuscript/open?saga={SAGA}&chapter={CHAPTER}"));
        let (refused, refusal) = super::target(&Argument::new("canonkeeper://open?saga=x&chapter=y"));
        assert_eq!((refused.path().as_str(), refusal), ("/manuscript/open?refused=1", Some(Refusal::Value)));
        let many = of_arguments(&["canonkeeper://open?a", "canonkeeper://open?b"], System::Linux).unwrap();
        assert_eq!(super::target(&many), (Target::Refused, Some(Refusal::Many)));
        // The script that gives the page the place carries the address as a string of JSON and nothing of the raw link.
        let script = visit_script(&target);
        assert!(
            script.contains(&format!("(\"/manuscript/open?saga={SAGA}&chapter={CHAPTER}&p=15&rev=3\");")),
            "{script}"
        );
        assert!(!script.contains("CANONKEEPER_VISIT_TO)") && !script.contains("canonkeeper://"), "{script}");
        assert_eq!(VISIT.matches(VISIT_ARGUMENT).count(), 1);
        assert!(VISIT.contains("location.hostname !== '127.0.0.1'"));
        assert!(VISIT.contains("dispatchEvent(new CustomEvent('canonkeeper:link'") && !VISIT.contains("history."));
        assert_eq!(format!("{many:?}"), "Argument(…)");
    }

    // AL1.2: the link out of the arguments — none, one, two, not UTF-8, mcp — after the program.
    #[test]
    fn the_link_is_the_one_argument_of_the_scheme() {
        let link = format!("canonkeeper://open?saga={SAGA}&chapter={CHAPTER}");
        let os = |texts: &[&str]| texts.iter().map(OsString::from).collect::<Vec<_>>();
        for system in [System::Linux, System::Windows] {
            assert_eq!(of_arguments::<OsString>(&[], system), None);
            assert_eq!(of_arguments(&os(&["--flag", "C:\\Users\\a"]), system), None);
            assert_eq!(of_arguments(&os(&[&link]), system), Some(Argument::new(&link)));
            assert_eq!(of_arguments(&os(&["--flag", &link]), system), Some(Argument::new(&link)));
            let upper = link.replace("canonkeeper:", "CANONKEEPER:");
            assert_eq!(of_arguments(&os(&[&upper]), system), Some(Argument::new(&upper)));
            // A link with a quote cut by the command of Windows comes as two arguments: refused.
            let two = of_arguments(&os(&[&link, "canonkeeper://open?x"]), system).unwrap();
            assert!(two.many, "{system:?}");
            // A broken link is still the link: its check refuses it.
            assert_eq!(of_arguments(&os(&["canonkeeper:x y"]), system), Some(Argument::new("canonkeeper:x y")));
            assert_eq!(of_arguments(&os(&["canonkeeperx://open"]), system), None);
            // A second start gives its arguments with the replacement character in place of bytes that are not UTF-8.
            let replaced = format!("{link}\u{FFFD}");
            assert_eq!(of_arguments(&os(&[&replaced]), system), None);
            assert_eq!(of_arguments(&os(&[&replaced, &link]), system), Some(Argument::new(&link)));
            assert_eq!(of_arguments(&os(&["canonkeeper"]), system), None);
        }
        // `mcp` first is the bridge off Windows; on Windows there is no such rule.
        assert_eq!(of_arguments(&os(&["mcp", &link]), System::Linux), None);
        assert_eq!(of_arguments(&os(&["mcp", &link]), System::Windows), Some(Argument::new(&link)));
        assert_eq!(of_arguments(&os(&["--x", "mcp", &link]), System::Linux), Some(Argument::new(&link)));
        #[cfg(unix)]
        {
            use std::os::unix::ffi::OsStringExt;
            let broken = OsString::from_vec(b"canonkeeper://open?saga=\xff".to_vec());
            assert_eq!(of_arguments(std::slice::from_ref(&broken), System::Linux), None);
            assert_eq!(of_arguments(&[broken, OsString::from(&link)], System::Linux), Some(Argument::new(&link)));
        }
    }

    fn targets() -> (Target, Target) {
        (Target::Place(place(Some(1), None)), Target::Place(place(Some(2), None)))
    }

    // AL1.3: a link waits while the backend starts and while the page of the opening loads; the opening carries it to its
    // first page; where no link is taken it is dropped.
    #[test]
    fn a_link_waits_for_the_page_of_an_opening_and_rides_it() {
        let now = Instant::now();
        let (a, b) = targets();
        let mut links = Links::default();
        assert_eq!(links.arrive(a.clone(), Stage::Starting, now), vec![]);
        assert_eq!(links.arrive(b.clone(), Stage::Starting, now), vec![Done::Merged]);
        assert_eq!(links.opening(), (1, Some(b.clone())));
        // The page of the opening is not loaded yet: a newer link waits for it.
        assert_eq!(links.arrive(a.clone(), Stage::Open, now), vec![Done::Merged]);
        // A load of an older opening does not count.
        assert_eq!(links.loaded(0, Stage::Open, now), vec![]);
        // The page of the opening came with b; a, newer, waits out the interval since b, then moves the window.
        assert_eq!(links.loaded(1, Stage::Open, now), vec![]);
        assert_eq!(links.tick(Stage::Open, now + Duration::from_millis(1999)), vec![]);
        assert_eq!(links.tick(Stage::Open, now + INTERVAL), vec![Done::Visit(a.clone())]);
        assert_eq!(links.tick(Stage::Open, now + INTERVAL * 2), vec![]);

        // A link of the opening is not lost to a refused code: the next opening carries it again until a page is loaded.
        let mut refused = Links::default();
        refused.arrive(a.clone(), Stage::Starting, now);
        assert_eq!(refused.opening(), (1, Some(a.clone())));
        assert_eq!(refused.opening(), (2, Some(a.clone())));
        assert_eq!(refused.loaded(2, Stage::Open, now), vec![]);
        assert_eq!(refused.opening(), (3, None));

        // The window opens without a link; the page loads; a link moves it at once.
        let mut plain = Links::default();
        assert_eq!(plain.opening(), (1, None));
        assert_eq!(plain.arrive(a.clone(), Stage::Open, now), vec![]);
        assert_eq!(plain.loaded(1, Stage::Open, now), vec![Done::Visit(a.clone())]);
        // The window went to the page of the start (a fall): links wait again.
        plain.left();
        assert_eq!(plain.arrive(b.clone(), Stage::Starting, now + INTERVAL), vec![]);
        assert_eq!(plain.opening(), (2, Some(b)));
    }

    // AL1.3: every phase where no link is taken drops it, and a link that waits goes with the window.
    #[test]
    fn where_no_link_is_taken_it_is_dropped() {
        let now = Instant::now();
        let (a, _) = targets();
        for why in [Dropped::Closing, Dropped::Installing, Dropped::Erasing, Dropped::Failure, Dropped::Leaving] {
            let mut links = Links::default();
            assert_eq!(links.arrive(a.clone(), Stage::Gone(why), now), vec![Done::Dropped(why)]);
            links.arrive(a.clone(), Stage::Starting, now);
            assert_eq!(links.settle(Stage::Starting), vec![]);
            assert_eq!(links.settle(Stage::Gone(why)), vec![Done::Dropped(why)]);
            assert_eq!(links.settle(Stage::Gone(why)), vec![]);
            assert_eq!(links.opening(), (1, None));
        }
        // The page of an opening is not loaded once the backend is gone.
        let mut links = Links::default();
        links.opening();
        assert_eq!(links.loaded(1, Stage::Gone(Dropped::Closing), now), vec![]);
        assert_eq!(links.arrive(a, Stage::Open, now), vec![]);
    }

    // AL1.4: at most one move in two seconds; the links between are folded into the last; the link of the last move again
    // only raises the window, and after the interval moves it again.
    #[test]
    fn the_window_moves_at_most_once_in_the_interval() {
        let now = Instant::now();
        let (a, b) = targets();
        let c = Target::Refused;
        let mut links = Links::default();
        links.opening();
        links.loaded(1, Stage::Open, now);
        assert_eq!(links.arrive(a.clone(), Stage::Open, now), vec![Done::Visit(a.clone())]);
        assert_eq!(links.arrive(a.clone(), Stage::Open, now + Duration::from_millis(300)), vec![Done::Raise]);
        assert_eq!(links.arrive(b.clone(), Stage::Open, now + Duration::from_millis(500)), vec![]);
        assert_eq!(links.arrive(c.clone(), Stage::Open, now + Duration::from_millis(700)), vec![Done::Merged]);
        assert_eq!(links.tick(Stage::Open, now + Duration::from_millis(1500)), vec![]);
        assert_eq!(links.tick(Stage::Open, now + INTERVAL), vec![Done::Visit(c.clone())]);
        // The author pressed the link he moved to before: a raise; the one that waited goes.
        let later = now + INTERVAL;
        assert_eq!(links.arrive(b.clone(), Stage::Open, later + Duration::from_millis(100)), vec![]);
        assert_eq!(
            links.arrive(c.clone(), Stage::Open, later + Duration::from_millis(200)),
            vec![Done::Merged, Done::Raise]
        );
        assert_eq!(links.tick(Stage::Open, later + INTERVAL), vec![]);
        // Past the interval the same link moves the window again: the author went elsewhere and comes back to the place.
        assert_eq!(links.arrive(c.clone(), Stage::Open, later + INTERVAL * 3), vec![Done::Visit(c)]);
    }

    // AL1.5: the mark names the link of the start by its hash; the same link once dropped, another or none takes it away.
    #[test]
    fn the_mark_drops_the_link_of_the_first_start_once() {
        let root = std::env::temp_dir().join(format!("canonkeeper-link-mark-{}", std::process::id()));
        let _ = fs::remove_dir_all(&root);
        fs::create_dir_all(&root).unwrap();
        let link = Argument::new(&format!("canonkeeper://open?saga={SAGA}&chapter={CHAPTER}&p=15"));
        let other = Argument::new(&format!("canonkeeper://open?saga={SAGA}&chapter={CHAPTER}&p=16"));
        assert!(!replayed(&root, Some(&link)));
        mark(&root, &link).unwrap();
        let text = fs::read_to_string(root.join(MARK)).unwrap();
        assert!(!text.contains(SAGA) && !text.contains(CHAPTER), "{text}");
        assert!(replayed(&root, Some(&link)));
        assert!(!replayed(&root, Some(&link)), "dropped twice");
        mark(&root, &link).unwrap();
        assert!(!replayed(&root, Some(&other)));
        assert!(!root.join(MARK).exists());
        mark(&root, &link).unwrap();
        assert!(!replayed(&root, None));
        assert!(!root.join(MARK).exists());
        mark(&root, &link).unwrap();
        unmark(&root);
        assert!(!replayed(&root, Some(&link)));
        fs::remove_dir_all(&root).unwrap();
    }

    // AL1.7: the names of the log are words, never a part of a link.
    #[test]
    fn the_log_names_a_reason() {
        let refusals =
            [Refusal::Scheme, Refusal::Action, Refusal::Parameter, Refusal::Value, Refusal::Length, Refusal::Many];
        assert_eq!(refusals.map(Refusal::name), ["scheme", "action", "parameter", "value", "length", "many"]);
        let dropped = [
            Dropped::Closing,
            Dropped::Installing,
            Dropped::Erasing,
            Dropped::Failure,
            Dropped::Leaving,
            Dropped::Replayed,
        ];
        assert_eq!(dropped.map(Dropped::name), ["closing", "installing", "erasing", "failure", "leaving", "replayed"]);
        for name in refusals.map(Refusal::name).into_iter().chain(dropped.map(Dropped::name)) {
            assert_eq!(crate::journal::name(name), name);
        }
    }
}
