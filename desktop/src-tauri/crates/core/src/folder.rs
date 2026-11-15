// SPDX-License-Identifier: AGPL-3.0-only

//! The choice of the folder of an Obsidian vault (specs/2026-11-13-desk-obsidian.md OB1.1–OB1.3). The backend declares
//! a choice with its number (`folder-choice`); the page asks for the system window with that number (`pick_folder`) and
//! no other, once; the shell refuses a folder of the app — one «Стереть все данные» erases, holds or lies in — and a
//! folder too wide — a root of a volume, the home folder —, names any other one to the backend (`folder-picked`) and
//! answers the page only how the backend ended the choice (`folder-result`), or that the choice went by. The page never
//! hears the path.

use std::fs;
use std::io;
use std::path::{Component, Path, PathBuf};
use std::sync::mpsc::Sender;
use std::time::{Duration, Instant};

use serde_json::json;

/// How long a declared choice waits for the page: the backend keeps its number as long.
pub const DECLARED_TERM: Duration = Duration::from_secs(10 * 60);
/// How long the page waits for the backend once it heard the folder.
pub const RESULT_TERM: Duration = Duration::from_secs(15);
/// The length of the number of a choice: 32 random bytes in base64url without padding.
const REQUEST_LEN: usize = 43;

/// How a choice ends for the page: the folder is the vault of the saga, it was refused by a reason, the author canceled
/// the window, or the choice went by — never declared, spent, out of time or without an answer of the backend.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Outcome {
    Linked,
    Refused(String),
    Canceled,
    Expired,
}

impl Outcome {
    /// What the command answers the page: the outcome and the reason of a refusal, nothing else.
    pub fn json(&self) -> serde_json::Value {
        match self {
            Outcome::Linked => json!({"outcome": "linked"}),
            Outcome::Refused(reason) => json!({"outcome": "refused", "reason": reason}),
            Outcome::Canceled => json!({"outcome": "canceled"}),
            Outcome::Expired => json!({"outcome": "expired"}),
        }
    }

    /// The outcome a `folder-result` of the backend says.
    fn of(outcome: &str) -> Outcome {
        match outcome {
            "linked" => Outcome::Linked,
            "not_found" | "not_folder" | "unreadable" => Outcome::Refused(outcome.to_string()),
            _ => Outcome::Expired,
        }
    }
}

/// Where the page waits for how its choice ends. Two replies are never the same: a reply is no value a message is
/// compared by.
#[derive(Debug, Clone)]
pub struct Reply(pub Sender<Outcome>);

impl PartialEq for Reply {
    fn eq(&self, _: &Reply) -> bool {
        false
    }
}

/// The words of the system window that chooses the folder of a vault (OB1.2): its title names the saga.
pub fn window_title(saga: &str) -> String {
    if saga.trim().is_empty() {
        return "Папка Obsidian для саги".to_string();
    }
    format!("Папка Obsidian для саги «{}»", saga.trim())
}
pub const ACCEPT: &str = "Выбрать";
pub const CANCEL: &str = "Отмена";

/// Whether a number has the form of a number of a choice.
pub fn well_formed(request: &str) -> bool {
    request.len() == REQUEST_LEN
        && request.bytes().all(|byte| byte.is_ascii_alphanumeric() || byte == b'-' || byte == b'_')
}

/// Why the shell refuses a folder itself (OB1.3), before the backend hears of it.
pub const DATA_DIR: &str = "data_dir";
pub const TOO_WIDE: &str = "too_wide";
const NOT_FOUND: &str = "not_found";
const UNREADABLE: &str = "unreadable";

/// Checks the folder the author picked against the directories «Стереть все данные» erases and against a folder too
/// wide; gives the path the backend is to hear. The comparison goes after the links are resolved, name by name — a
/// directory that is not there yet is compared by its nearest folder that is —, without regard to case on Windows. A
/// path that is not text is unreadable: it cannot go into the channel.
pub fn check(folder: &Path, directories: &[String], home: Option<&str>) -> Result<String, &'static str> {
    let resolved = fs::canonicalize(folder).map_err(|error| match error.kind() {
        io::ErrorKind::NotFound => NOT_FOUND,
        _ => UNREADABLE,
    })?;
    let path = resolved.to_str().ok_or(UNREADABLE)?.to_string();
    let chosen = names(&resolved);
    for directory in directories {
        let erased = names(&resolved_as_far_as_it_is(Path::new(directory)));
        if !erased.is_empty() && (within(&erased, &chosen) || within(&chosen, &erased)) {
            return Err(DATA_DIR);
        }
    }
    let root = resolved.parent().is_none() || mounted(&resolved);
    let home = home.filter(|home| !home.is_empty()).map(|home| names(&resolved_as_far_as_it_is(Path::new(home))));
    if root || home.is_some_and(|home| home == chosen) {
        return Err(TOO_WIDE);
    }
    Ok(plain(path))
}

/// Whether a folder is the root of a volume mounted into the tree of the system — a disk in `/media/<user>/…` or `/mnt/…`,
/// a share —: its device is not the device of its parent. On Windows the root of a volume has no parent.
#[cfg(unix)]
fn mounted(folder: &Path) -> bool {
    use std::os::unix::fs::MetadataExt;
    match (fs::metadata(folder), folder.parent().map(fs::metadata)) {
        (Ok(own), Some(Ok(parent))) => own.dev() != parent.dev(),
        _ => false,
    }
}

#[cfg(not(unix))]
fn mounted(_: &Path) -> bool {
    false
}

/// A path of Windows without the prefix of a long path that the resolution gives (`\\?\C:\…`).
fn plain(path: String) -> String {
    match path.strip_prefix(r"\\?\") {
        Some(rest) if !rest.starts_with("UNC\\") => rest.to_string(),
        _ => path,
    }
}

/// The path with every link of its part that is there resolved, and the rest of its names after it.
fn resolved_as_far_as_it_is(path: &Path) -> PathBuf {
    let mut rest = Vec::new();
    let mut current = path;
    loop {
        if let Ok(resolved) = fs::canonicalize(current) {
            return rest.iter().rev().fold(resolved, |path: PathBuf, name| path.join(name));
        }
        match (current.parent(), current.file_name()) {
            (Some(parent), Some(name)) => {
                rest.push(name.to_os_string());
                current = parent;
            }
            _ => return path.to_path_buf(),
        }
    }
}

/// The names of a path as the system compares them: the prefix, the root and every name, in lower case on Windows.
fn names(path: &Path) -> Vec<String> {
    path.components()
        .filter(|component| !matches!(component, Component::CurDir))
        .map(|component| {
            let name = component.as_os_str().to_string_lossy().into_owned();
            if cfg!(windows) {
                name.to_lowercase()
            } else {
                name
            }
        })
        .collect()
}

/// Whether `inner` is `outer` or lies in it.
fn within(outer: &[String], inner: &[String]) -> bool {
    inner.len() >= outer.len() && inner[..outer.len()] == *outer
}

/// The choices the shell holds (OB1.1, OB1.2): the one the backend declared and the page has not opened yet, the one whose
/// window is open, and the one whose folder the backend checks. A new declaration puts out the one before; each number
/// opens one window, once.
#[derive(Debug, Default)]
pub struct Choosing {
    declared: Option<Declared>,
    open: Option<Waiting>,
    picked: Option<(Waiting, Instant)>,
}

#[derive(Debug)]
struct Declared {
    request: String,
    saga: String,
    until: Instant,
}

/// A page that waits for how its choice ends.
#[derive(Debug)]
struct Waiting {
    request: String,
    reply: Sender<Outcome>,
}

impl Waiting {
    fn answer(self, outcome: Outcome) {
        let _ = self.reply.send(outcome);
    }
}

impl Choosing {
    /// The backend declared a choice for the saga titled `saga`.
    pub fn declare(&mut self, request: &str, saga: &str, now: Instant) {
        if well_formed(request) {
            self.declared =
                Some(Declared { request: request.to_string(), saga: saga.to_string(), until: now + DECLARED_TERM });
        }
    }

    /// The page asks for the window of a number: the title of its saga when the window opens — the declared number, in
    /// its time, and no window open or waiting for the backend —; otherwise the page hears at once that the choice went
    /// by, and no window opens.
    pub fn pick(&mut self, request: &str, reply: Sender<Outcome>, now: Instant) -> Option<String> {
        let declared = self.declared.take_if(|declared| declared.request == request && now < declared.until);
        match declared {
            Some(declared) if well_formed(request) && self.open.is_none() && self.picked.is_none() => {
                self.open = Some(Waiting { request: request.to_string(), reply });
                Some(declared.saga)
            }
            other => {
                self.declared = self.declared.take().or(other);
                let _ = reply.send(Outcome::Expired);
                None
            }
        }
    }

    /// The window of a number closed: canceled (`None`), a folder the shell refused, or a folder the backend is to hear —
    /// its path, given back for `folder-picked`; the page then waits for the backend.
    pub fn chosen(&mut self, request: &str, checked: Option<Result<String, String>>, now: Instant) -> Option<String> {
        let open = self.open.take_if(|open| open.request == request)?;
        match checked {
            None => open.answer(Outcome::Canceled),
            Some(Err(reason)) => open.answer(Outcome::Refused(reason)),
            Some(Ok(path)) => {
                self.picked = Some((open, now + RESULT_TERM));
                return Some(path);
            }
        }
        None
    }

    /// The backend ended the choice of a number (`folder-result`).
    pub fn result(&mut self, request: &str, outcome: &str) {
        if let Some((waiting, _)) = self.picked.take_if(|(waiting, _)| waiting.request == request) {
            waiting.answer(Outcome::of(outcome));
        }
    }

    /// Time passes: a page that waited too long for the backend hears the choice went by; a declaration out of its time
    /// is gone.
    pub fn tick(&mut self, now: Instant) {
        if let Some((waiting, _)) = self.picked.take_if(|(_, until)| now >= *until) {
            waiting.answer(Outcome::Expired);
        }
        self.declared.take_if(|declared| now >= declared.until);
    }

    /// The backend is gone: its choices with it.
    pub fn backend_gone(&mut self) {
        self.declared = None;
        if let Some((waiting, _)) = self.picked.take() {
            waiting.answer(Outcome::Expired);
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::mpsc;

    const NUMBER: &str = "Y2Fub25rZWVwZXItb2JzaWRpYW4tY2hvaWNlLTAwMDA";
    const OTHER: &str = "Y2Fub25rZWVwZXItb2JzaWRpYW4tY2hvaWNlLTExMTE";

    #[test]
    fn the_system_window_names_the_saga() {
        assert_eq!(window_title(" Северный предел "), "Папка Obsidian для саги «Северный предел»");
        assert_eq!(window_title(""), "Папка Obsidian для саги");
    }

    #[test]
    fn a_number_has_its_one_form() {
        assert!(well_formed(NUMBER));
        for bad in [
            "",
            "short",
            &format!("{NUMBER}A"),
            "Y2Fub25rZWVwZXItb2JzaWRpYW4tY2hvaWNlLTAwM/A=",
            "\"},{\"type\":\"x\"0000000000000000000000000",
        ] {
            assert!(!well_formed(bad), "{bad}");
        }
    }

    #[test]
    fn the_window_opens_for_the_declared_number_once() {
        let now = Instant::now();
        let mut choosing = Choosing::default();
        let (reply, heard) = mpsc::channel();
        assert_eq!(choosing.pick(NUMBER, reply.clone(), now), None, "nothing declared");
        assert_eq!(heard.try_recv(), Ok(Outcome::Expired));
        choosing.declare(NUMBER, "Северный предел", now);
        assert_eq!(choosing.pick(OTHER, reply.clone(), now), None, "another number");
        assert_eq!(heard.try_recv(), Ok(Outcome::Expired));
        assert_eq!(choosing.pick("not a number", reply.clone(), now), None);
        assert_eq!(heard.try_recv(), Ok(Outcome::Expired));
        assert_eq!(choosing.pick(NUMBER, reply.clone(), now).as_deref(), Some("Северный предел"));
        assert!(heard.try_recv().is_err(), "the page waits");
        choosing.declare(OTHER, "Северный предел", now);
        assert_eq!(choosing.pick(OTHER, reply.clone(), now), None, "a second window while one is open");
        assert_eq!(heard.try_recv(), Ok(Outcome::Expired));
        assert_eq!(choosing.pick(NUMBER, reply, now), None, "the number is spent");
        assert_eq!(heard.try_recv(), Ok(Outcome::Expired));
    }

    #[test]
    fn the_page_hears_how_the_backend_ended_the_choice_and_never_the_path() {
        let now = Instant::now();
        let mut choosing = Choosing::default();
        let (reply, heard) = mpsc::channel();
        choosing.declare(NUMBER, "Северный предел", now);
        choosing.pick(NUMBER, reply.clone(), now);
        assert_eq!(
            choosing.chosen(NUMBER, Some(Ok("/home/author/Хранилище".into())), now).as_deref(),
            Some("/home/author/Хранилище")
        );
        assert!(heard.try_recv().is_err(), "no answer before folder-result");
        choosing.result(OTHER, "linked");
        assert!(heard.try_recv().is_err());
        choosing.result(NUMBER, "linked");
        assert_eq!(heard.try_recv(), Ok(Outcome::Linked));
        assert_eq!(Outcome::Linked.json(), json!({"outcome": "linked"}));

        for (said, outcome) in [("not_folder", Outcome::Refused("not_folder".into())), ("stale", Outcome::Expired)] {
            choosing.declare(NUMBER, "Северный предел", now);
            choosing.pick(NUMBER, reply.clone(), now);
            choosing.chosen(NUMBER, Some(Ok("/x".into())), now);
            choosing.result(NUMBER, said);
            assert_eq!(heard.try_recv(), Ok(outcome));
        }
    }

    #[test]
    fn a_cancel_a_refusal_and_a_silent_backend_end_the_choice() {
        let now = Instant::now();
        let mut choosing = Choosing::default();
        let (reply, heard) = mpsc::channel();
        choosing.declare(NUMBER, "Сага", now);
        choosing.pick(NUMBER, reply.clone(), now);
        assert_eq!(choosing.chosen(NUMBER, None, now), None);
        assert_eq!(heard.try_recv(), Ok(Outcome::Canceled));
        choosing.declare(NUMBER, "Сага", now);
        choosing.pick(NUMBER, reply.clone(), now);
        assert_eq!(choosing.chosen(NUMBER, Some(Err(DATA_DIR.into())), now), None);
        assert_eq!(heard.try_recv(), Ok(Outcome::Refused(DATA_DIR.into())));
        choosing.declare(NUMBER, "Сага", now);
        choosing.pick(NUMBER, reply.clone(), now);
        choosing.chosen(NUMBER, Some(Ok("/x".into())), now);
        choosing.tick(now + RESULT_TERM - Duration::from_millis(1));
        assert!(heard.try_recv().is_err());
        choosing.tick(now + RESULT_TERM);
        assert_eq!(heard.try_recv(), Ok(Outcome::Expired));
        choosing.declare(NUMBER, "Сага", now);
        choosing.tick(now + DECLARED_TERM);
        assert_eq!(choosing.pick(NUMBER, reply.clone(), now + DECLARED_TERM), None, "a declaration out of its time");
        assert_eq!(heard.try_recv(), Ok(Outcome::Expired));
        choosing.declare(NUMBER, "Сага", now);
        choosing.pick(NUMBER, reply, now);
        choosing.chosen(NUMBER, Some(Ok("/x".into())), now);
        choosing.backend_gone();
        assert_eq!(heard.try_recv(), Ok(Outcome::Expired));
    }

    #[test]
    fn a_path_of_windows_loses_only_the_prefix_of_a_long_path() {
        // The resolution on Windows gives `\\?\C:\…`; the backend hears the path as the author knows it. A share keeps
        // its form.
        assert_eq!(plain(r"\\?\C:\Users\тест\Хранилище".into()), r"C:\Users\тест\Хранилище");
        assert_eq!(plain(r"\\?\UNC\server\share\Хранилище".into()), r"\\?\UNC\server\share\Хранилище");
        assert_eq!(plain("/home/author/Хранилище".into()), "/home/author/Хранилище");
    }

    fn folder(root: &Path, name: &str) -> PathBuf {
        let path = root.join(name);
        fs::create_dir_all(&path).unwrap();
        path
    }

    #[test]
    fn a_folder_of_the_app_and_a_folder_too_wide_are_refused() {
        let temp = std::env::temp_dir().join(format!("ck-folder-{}", std::process::id()));
        let _ = fs::remove_dir_all(&temp);
        let home = folder(&temp, "home");
        let data = folder(&home, ".local/share/canonkeeper");
        let window = folder(&home, ".local/share/ru.canonkeeper.desktop");
        let cache = home.join(".cache/ru.canonkeeper.desktop");
        let vault = folder(&home, "Хранилище/Северный предел");
        let directories = [
            data.to_str().unwrap().to_string(),
            window.to_str().unwrap().to_string(),
            cache.to_str().unwrap().to_string(),
        ];
        let home_text = home.to_str().unwrap().to_string();
        let check = |path: &Path| check(path, &directories, Some(&home_text));

        assert_eq!(check(&vault), Ok(plain(fs::canonicalize(&vault).unwrap().to_str().unwrap().to_string())));
        assert_eq!(check(&data), Err(DATA_DIR));
        assert_eq!(check(&folder(&data, "files")), Err(DATA_DIR));
        assert_eq!(check(&folder(&window, "WebKitCache")), Err(DATA_DIR));
        assert_eq!(
            check(&folder(&home, ".cache/ru.canonkeeper.desktop/x")),
            Err(DATA_DIR),
            "inside a cache made later"
        );
        assert_eq!(check(&home.join(".local")), Err(DATA_DIR), "a folder that holds the data");
        assert_eq!(check(&home), Err(DATA_DIR), "the home holds the data too");
        #[cfg(unix)]
        {
            let link = home.join("Ссылка на данные");
            std::os::unix::fs::symlink(&data, &link).unwrap();
            assert_eq!(check(&link), Err(DATA_DIR), "a link to the data");
            assert_eq!(check(Path::new("/")), Err(DATA_DIR), "the root holds the data");
            let lonely = folder(&temp, "lonely");
            assert_eq!(super::check(Path::new("/"), &[], None), Err(TOO_WIDE));
            assert_eq!(super::check(&lonely, &[], Some(lonely.to_str().unwrap())), Err(TOO_WIDE), "the home folder");
        }
        #[cfg(target_os = "linux")]
        {
            // A volume mounted into the tree is a volume too: /proc is one on every Linux.
            assert_eq!(super::check(Path::new("/proc"), &[], None), Err(TOO_WIDE), "the root of a mounted volume");
            assert_eq!(super::check(&vault, &[], None).map(|_| ()), Ok(()), "a folder inside a volume");
        }
        assert_eq!(check(&home.join("Нет")), Err(NOT_FOUND));
        let _ = fs::remove_dir_all(&temp);
    }
}
