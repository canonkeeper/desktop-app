// SPDX-License-Identifier: AGPL-3.0-only

//! A release of the app (specs/2026-11-13-desk-updates.md UB3.2–UB3.4, UB3.7): one signer and one check, which the job of
//! the signature and the smoke run of an update call through `canonkeeper-release`. The signer signs every installer with
//! its version in the trusted comment, writes `latest.json` with one file for each kind of installation and its own
//! signature, and `SHA256SUMS` with its signature — only by the main key the app trusts: another key is refused before
//! anything is signed. A released policy — not the loopback of the smoke build — also signs `latest-site.json`, the same
//! manifest with the files at the address the site mirrors them at (`policy::SITE_FILES`), which the site serves as its
//! `/app/open/latest.json` (specs/2026-11-16-desk-app-mirror.md AM1). The check proves a release before its draft: the tag
//! is the version of the shell, of both manifests and of every signature; every signature holds by the main key of the
//! tree — the one of the code and the one of `tauri.conf.json` — through the reading and the verification the app makes
//! (`manifest`); one file for each kind and no key of a manifest without its kind; every file of the manifest of the site is
//! exactly at its address of the site; a key of development refuses the release. The marks of the bundler in the program
//! of each package are checked where the package is unpacked (`marks`).

use std::collections::{BTreeMap, BTreeSet};
use std::fs::{self, File, OpenOptions};
use std::io::Write;
use std::path::{Path, PathBuf};

use semver::Version;
use serde_json::{json, Map, Value};
use sha2::{Digest, Sha256};
use url::Url;

use super::manifest::{self, key_id, plain_notes, Keys};
use super::policy::{Policy, DEVELOPMENT_KEYS, SITE_FILES, SMOKE_MARK};
use super::sign::{trusted, SecretKey};
use super::Kind;

/// The names of the files the signer writes besides the signatures of the installers: the manifest of GitHub, the
/// manifest of the site — a released policy alone writes it (AM1.1) —, the sums.
pub const MANIFEST: &str = "latest.json";
pub const SITE_LATEST: &str = "latest-site.json";
pub const SUMS: &str = "SHA256SUMS";

/// The address of the file `name` of `version` on the site (AM1.1): exactly this string, nothing around it.
pub fn site_file(version: &Version, name: &str) -> String {
    format!("{SITE_FILES}{version}/{name}")
}

/// The kind of an installer by the end of its name, as the bundler names its files.
pub fn kind_of(name: &str) -> Option<Kind> {
    if name.ends_with("-setup.exe") {
        Some(Kind::Nsis)
    } else if name.ends_with(".deb") {
        Some(Kind::Deb)
    } else if name.ends_with(".rpm") {
        Some(Kind::Rpm)
    } else if name.ends_with(".AppImage") {
        Some(Kind::AppImage)
    } else {
        None
    }
}

/// An installer of a release: its kind and the name of its file.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Installer {
    pub kind: Kind,
    pub name: String,
}

/// The installers of a directory, in the order of [`Kind::RELEASED`]; two of one kind are refused.
pub fn installers(dir: &Path) -> Result<Vec<Installer>, String> {
    let mut found: BTreeMap<usize, Installer> = BTreeMap::new();
    let entries = fs::read_dir(dir).map_err(|error| format!("{}: {error}", dir.display()))?;
    for entry in entries {
        let entry = entry.map_err(|error| error.to_string())?;
        let Some(name) = entry.file_name().to_str().map(str::to_string) else { continue };
        let Some(kind) = kind_of(&name) else { continue };
        if !entry.file_type().map_err(|error| error.to_string())?.is_file() {
            return Err(format!("{name} is not a file"));
        }
        let order = Kind::RELEASED.iter().position(|known| *known == kind).unwrap_or(Kind::RELEASED.len());
        if let Some(other) = found.insert(order, Installer { kind, name: name.clone() }) {
            return Err(format!("two installers of the kind {}: {} and {name}", kind.name(), other.name));
        }
    }
    Ok(found.into_values().collect())
}

fn hex(bytes: &[u8]) -> String {
    bytes.iter().map(|byte| format!("{byte:02x}")).collect()
}

fn sha256(bytes: &[u8]) -> String {
    hex(&Sha256::digest(bytes))
}

/// A day of the calendar from the days since 1970-01-01 (the civil calendar, proleptic Gregorian).
fn civil(days: i64) -> (i64, u32, u32) {
    let z = days + 719_468;
    let era = z.div_euclid(146_097);
    let day_of_era = z.rem_euclid(146_097);
    let year_of_era = (day_of_era - day_of_era / 1460 + day_of_era / 36_524 - day_of_era / 146_096) / 365;
    let day_of_year = day_of_era - (365 * year_of_era + year_of_era / 4 - year_of_era / 100);
    let shifted_month = (5 * day_of_year + 2) / 153;
    let day = (day_of_year - (153 * shifted_month + 2) / 5 + 1) as u32;
    let month = if shifted_month < 10 { shifted_month + 3 } else { shifted_month - 9 } as u32;
    let year = year_of_era + era * 400 + i64::from(month <= 2);
    (year, month, day)
}

/// The time of a release in RFC 3339, as `pub_date` of `latest.json` names it.
pub fn rfc3339(timestamp: u64) -> String {
    let seconds = timestamp as i64;
    let (year, month, day) = civil(seconds.div_euclid(86_400));
    let in_day = seconds.rem_euclid(86_400);
    format!("{year:04}-{month:02}-{day:02}T{:02}:{:02}:{:02}Z", in_day / 3600, in_day % 3600 / 60, in_day % 60)
}

/// Writes a new file: a file of an earlier run is never overwritten.
fn write_new(path: &Path, bytes: &[u8]) -> Result<(), String> {
    let mut file: File = OpenOptions::new()
        .write(true)
        .create_new(true)
        .open(path)
        .map_err(|error| format!("{}: {error}", path.display()))?;
    file.write_all(bytes).map_err(|error| format!("{}: {error}", path.display()))
}

/// Signs the installers of `dir` as the release `version` (UB3.2): `<installer>.sig` with the version, `latest.json` with
/// the file of each kind at `base` and its signature, on a released policy `latest-site.json` with the file of each kind at
/// its address of the site and its signature (AM1.1), `SHA256SUMS` of every file and its signature — and answers the names
/// it wrote. `key` must be the main key of `policy`; `base` must be an address the app would take a file from; the notes
/// must be what the app shows. Nothing is written when one of these does not hold.
pub fn sign(
    dir: &Path,
    version: &Version,
    base: &Url,
    notes: &str,
    key: &SecretKey,
    policy: &Policy,
    timestamp: u64,
) -> Result<Vec<String>, String> {
    if key.public() != policy.keys.main {
        return Err("the key is not the main key of the app: nothing is signed".into());
    }
    if !plain_notes(notes) {
        return Err(format!("the notes are not plain text of at most {} bytes", manifest::NOTES_LIMIT));
    }
    if !base.path().ends_with('/') {
        return Err("the address of the files ends with /".into());
    }
    let installers = installers(dir)?;
    if installers.is_empty() {
        return Err("no installer to sign".into());
    }
    let mut platforms = Map::new();
    // The manifest of the site: the same signature of each file at its address of the site (AM1.1).
    let mut site_platforms = Map::new();
    let mut prepared = Vec::new();
    for installer in &installers {
        let url = base.join(&installer.name).map_err(|error| error.to_string())?;
        let site_url = Url::parse(&site_file(version, &installer.name)).map_err(|error| error.to_string())?;
        for url in [Some(&url), (!policy.loopback).then_some(&site_url)].into_iter().flatten() {
            if !policy.allowed(url) {
                return Err(format!("the app takes no file from {url}"));
            }
        }
        let bytes = fs::read(dir.join(&installer.name)).map_err(|error| format!("{}: {error}", installer.name))?;
        let signature = key.sign(&bytes, &trusted(&installer.name, &version.to_string(), timestamp));
        let platform = installer.kind.platform().expect("an installer has a platform");
        platforms.insert(platform.to_string(), json!({"signature": signature, "url": url.as_str()}));
        site_platforms.insert(platform.to_string(), json!({"signature": signature, "url": site_url.as_str()}));
        prepared.push((format!("{}.sig", installer.name), signature.into_bytes()));
    }
    let mut manifests = vec![(MANIFEST, platforms)];
    if !policy.loopback {
        manifests.push((SITE_LATEST, site_platforms));
    }
    for (name, platforms) in manifests {
        let manifest = serde_json::to_vec_pretty(&json!({
            "version": version.to_string(),
            "notes": notes,
            "pub_date": rfc3339(timestamp),
            "platforms": Value::Object(platforms),
        }))
        .map_err(|error| error.to_string())?;
        let manifest_signature = key.sign(&manifest, &trusted(name, &version.to_string(), timestamp));
        prepared.push((name.to_string(), manifest));
        prepared.push((format!("{name}.sig"), manifest_signature.into_bytes()));
    }
    for (name, _) in &prepared {
        if dir.join(name).symlink_metadata().is_ok() {
            return Err(format!(
                "{name} is already there: a release is signed into a directory of its installers alone"
            ));
        }
    }
    let mut sums = BTreeMap::new();
    for installer in &installers {
        let bytes = fs::read(dir.join(&installer.name)).map_err(|error| error.to_string())?;
        sums.insert(installer.name.clone(), sha256(&bytes));
    }
    for (name, bytes) in &prepared {
        sums.insert(name.clone(), sha256(bytes));
    }
    let listed: String = sums.iter().map(|(name, sum)| format!("{sum}  {name}\n")).collect();
    let listed_signature = key.sign(listed.as_bytes(), &trusted(SUMS, &version.to_string(), timestamp));
    prepared.push((SUMS.to_string(), listed.into_bytes()));
    prepared.push((format!("{SUMS}.sig"), listed_signature.into_bytes()));
    let mut written = Vec::new();
    for (name, bytes) in &prepared {
        write_new(&dir.join(name), bytes)?;
        written.push(name.clone());
    }
    Ok(written)
}

/// What a release must be (UB3.3).
#[derive(Debug, Clone)]
pub struct Expected<'a> {
    /// The tag of the release, `vX.Y.Z`.
    pub tag: &'a str,
    /// The version of the shell in its Cargo.toml.
    pub shell_version: &'a str,
    /// `plugins.updater.pubkey` of tauri.conf.json.
    pub configured_key: &'a str,
    /// The kinds of installation the release carries: one file each.
    pub kinds: &'a [Kind],
}

fn read_text(dir: &Path, name: &str, problems: &mut Vec<String>) -> Option<String> {
    match fs::read_to_string(dir.join(name)) {
        Ok(text) => Some(text.trim().to_string()),
        Err(error) => {
            problems.push(format!("{name}: {error}"));
            None
        }
    }
}

/// Checks the release of `dir` (UB3.3) against the keys and the addresses of `policy`, which the program of the release
/// carries: answers every problem found; none is a release that installs.
pub fn check(dir: &Path, expected: &Expected, policy: &Policy) -> Vec<String> {
    let mut problems = Vec::new();
    let version = match Version::parse(expected.shell_version) {
        Ok(version) => version,
        Err(_) => return vec![format!("the version of the shell {:?} is not a version", expected.shell_version)],
    };
    if expected.tag != format!("v{version}") {
        problems.push(format!("the tag {} is not v{version}, the version of the shell", expected.tag));
    }
    let main = &policy.keys.main;
    if expected.configured_key != main {
        problems.push("the key of tauri.conf.json is not the main key of the code".into());
    }
    if DEVELOPMENT_KEYS.contains(&main.as_str()) {
        problems.push("the main key is a key of development: no release is signed for it".into());
    }
    // The keys of a released app: a spare key apart from the main one. The policy on the loopback — the smoke build of an
    // update — has the one key of its run in both places.
    match (key_id(main), key_id(&policy.keys.spare)) {
        (None, _) => problems.push("the main key is not a key".into()),
        _ if policy.loopback => {}
        (Some(main_id), Some(spare_id)) if main_id != spare_id => {}
        (_, None) => problems.push("the spare key is not a key".into()),
        _ => problems.push("the spare key is the main key".into()),
    }
    let only_main = Keys { main: main.clone(), spare: main.clone() };
    let installers = match installers(dir) {
        Ok(installers) => installers,
        Err(problem) => return [problems, vec![problem]].concat(),
    };
    let kinds: Vec<Kind> = installers.iter().map(|installer| installer.kind).collect();
    if kinds != expected.kinds {
        problems.push(format!(
            "the installers are of {:?}; the release carries one of each of {:?}",
            kinds.iter().map(|kind| kind.name()).collect::<Vec<_>>(),
            expected.kinds.iter().map(|kind| kind.name()).collect::<Vec<_>>()
        ));
    }
    let mut signatures = BTreeMap::new();
    for installer in &installers {
        let Some(signature) = read_text(dir, &format!("{}.sig", installer.name), &mut problems) else { continue };
        match fs::read(dir.join(&installer.name)) {
            Ok(bytes) => {
                if let Err(refused) = manifest::verify_file(&bytes, &signature, &only_main, &version) {
                    problems
                        .push(format!("{}: the signature does not hold for {version}: {refused:?}", installer.name));
                }
            }
            Err(error) => problems.push(format!("{}: {error}", installer.name)),
        }
        signatures.insert(installer.kind.name(), signature);
    }
    // The manifest of GitHub, and on a released policy the manifest of the site with every file exactly at its address of
    // the site (AM1.2); the smoke build of an update carries no manifest of the site.
    let mut manifests = vec![(MANIFEST, false)];
    if policy.loopback {
        if dir.join(SITE_LATEST).symlink_metadata().is_ok() {
            problems.push(format!("a release on the loopback carries no {SITE_LATEST}"));
        }
    } else {
        manifests.push((SITE_LATEST, true));
    }
    for (name, of_site) in manifests {
        if let (Ok(body), Some(signature)) =
            (fs::read(dir.join(name)), read_text(dir, &format!("{name}.sig"), &mut problems))
        {
            let manifest = Manifest { name, body: &body, signature: &signature, of_site };
            check_manifest(&manifest, &installers, &signatures, &only_main, &version, policy, &mut problems);
        } else if !dir.join(name).is_file() {
            problems.push(format!("no {name}"));
        }
    }
    check_sums(dir, &only_main, &version, &mut problems);
    problems
}

/// A manifest of a release as the check reads it: its name, its bytes and its signature, and whether it is the manifest
/// of the site, whose files are at their addresses of the site.
struct Manifest<'a> {
    name: &'a str,
    body: &'a [u8],
    signature: &'a str,
    of_site: bool,
}

fn check_manifest(
    manifest: &Manifest,
    installers: &[Installer],
    signatures: &BTreeMap<&str, String>,
    keys: &Keys,
    version: &Version,
    policy: &Policy,
    problems: &mut Vec<String>,
) {
    let name = manifest.name;
    let platforms: BTreeSet<String> = serde_json::from_slice::<Value>(manifest.body)
        .ok()
        .and_then(|raw| raw.get("platforms").and_then(Value::as_object).map(|map| map.keys().cloned().collect()))
        .unwrap_or_default();
    let carried: BTreeSet<String> =
        installers.iter().filter_map(|installer| installer.kind.platform()).map(str::to_string).collect();
    if platforms != carried {
        problems.push(format!("{name} names the files of {platforms:?}; the release carries {carried:?}"));
    }
    for installer in installers {
        match manifest::read(manifest.body, manifest.signature, keys, installer.kind) {
            Ok(release) => {
                if &release.version != version {
                    problems.push(format!("{name} names {}, not {version}", release.version));
                }
                if !policy.allowed(&release.url) {
                    problems.push(format!("{name}: the app takes no file from {}", release.url));
                }
                if release.url.path_segments().and_then(|mut segments| segments.next_back()) != Some(&installer.name) {
                    problems.push(format!("{name}: the file of {} is not {}", installer.kind.name(), installer.name));
                }
                let at = site_file(version, &installer.name);
                if manifest.of_site && release.url.as_str() != at {
                    problems.push(format!("{name}: the file of {} is not at {at}", installer.kind.name()));
                }
                if signatures.get(installer.kind.name()) != Some(&release.signature) {
                    problems.push(format!(
                        "{name}: the signature of {} is not {}.sig",
                        installer.kind.name(),
                        installer.name
                    ));
                }
            }
            Err(unread) => problems.push(format!("{name} for {}: {unread:?}", installer.kind.name())),
        }
    }
}

fn check_sums(dir: &Path, keys: &Keys, version: &Version, problems: &mut Vec<String>) {
    let (Ok(listed), Some(signature)) =
        (fs::read_to_string(dir.join(SUMS)), read_text(dir, &format!("{SUMS}.sig"), problems))
    else {
        if !dir.join(SUMS).is_file() {
            problems.push(format!("no {SUMS}"));
        }
        return;
    };
    match manifest::verify(listed.as_bytes(), &signature, keys) {
        Ok((_, signed)) if &signed == version => {}
        Ok((_, signed)) => problems.push(format!("{SUMS} is signed for {signed}, not {version}")),
        Err(refused) => problems.push(format!("{SUMS}: the signature does not hold: {refused:?}")),
    }
    let mut named = BTreeSet::new();
    for line in listed.lines() {
        let Some((sum, name)) = line.split_once("  ") else {
            problems.push(format!("{SUMS}: a line without a sum and a name: {line}"));
            continue;
        };
        named.insert(name.to_string());
        match fs::read(dir.join(name)) {
            Ok(bytes) if sha256(&bytes) == sum => {}
            Ok(_) => problems.push(format!("{SUMS}: {name} is not the file it names")),
            Err(error) => problems.push(format!("{SUMS}: {name}: {error}")),
        }
    }
    if let Ok(entries) = fs::read_dir(dir) {
        for entry in entries.flatten() {
            let name = entry.file_name().to_string_lossy().into_owned();
            if name != SUMS && name != format!("{SUMS}.sig") && !named.contains(&name) {
                problems.push(format!("{SUMS} does not name {name}"));
            }
        }
    }
}

/// The mark the bundler writes into the program of a package of `kind` (tauri-utils `bundle_type`).
fn mark_of(kind: Kind) -> Option<&'static [u8]> {
    match kind {
        Kind::Nsis => Some(b"NSS"),
        Kind::Deb => Some(b"DEB"),
        Kind::Rpm => Some(b"RPM"),
        Kind::AppImage => Some(b"APP"),
        Kind::None => None,
    }
}

fn occurrences(haystack: &[u8], needle: &[u8]) -> usize {
    if needle.is_empty() || haystack.len() < needle.len() {
        return 0;
    }
    haystack.windows(needle.len()).filter(|window| *window == needle).count()
}

/// Checks the program of a package of `kind` (UB3.3): the bundler marked it as its kind — the mark of the kind stands
/// more often than the mark of any other and the mark of an unknown kind not at all (the program names every mark it
/// can read; the bundler patches one more) — and it is not a smoke build.
pub fn marks(program: &[u8], kind: Kind) -> Vec<String> {
    const PREFIX: &[u8] = b"__TAURI_BUNDLE_TYPE_VAR_";
    let Some(own) = mark_of(kind) else { return vec!["a package of no kind".into()] };
    let count = |suffix: &[u8]| occurrences(program, &[PREFIX, suffix].concat());
    let mut problems = Vec::new();
    if count(b"UNK") > 0 {
        problems.push("the program carries the mark of an unknown kind: the bundler did not mark it".into());
    }
    let others = [b"NSS", b"DEB", b"RPM", b"APP", b"MSI"]
        .into_iter()
        .filter(|suffix| *suffix != own)
        .map(|suffix| count(suffix))
        .max()
        .unwrap_or(0);
    if count(own) <= others {
        problems.push(format!("the program is not marked as {}", kind.name()));
    }
    if is_smoke(program) {
        problems.push("the program is a smoke build of updates".into());
    }
    problems
}

/// The programs of a package of `kind` where it is unpacked (UB3.3; specs/2026-11-15-desk-mcp.md MC1.1): the shell, the
/// backend and the bridge for the programs of the author, in this order — in `usr/bin` of a package of Linux, anywhere in
/// the files of the installer of Windows, whose names Windows compares without case. Those that are not there are named
/// with their package.
pub fn programs(unpacked: &Path, kind: Kind) -> Result<Vec<PathBuf>, String> {
    let exe = if kind == Kind::Nsis { ".exe" } else { "" };
    let mut found = Vec::new();
    let mut missing = Vec::new();
    for name in ["canonkeeper", "canonkeeper-backend", "canonkeeper-mcp"].map(|name| format!("{name}{exe}")) {
        let program = match kind {
            Kind::Nsis => find_without_case(unpacked, &name),
            _ => Some(unpacked.join("usr").join("bin").join(&name)).filter(|path| path.is_file()),
        };
        match program {
            Some(program) => found.push(program),
            None => missing.push(name),
        }
    }
    if missing.is_empty() {
        Ok(found)
    } else {
        Err(format!("the {} package has no {}", kind.name(), missing.join(", ")))
    }
}

/// The first file of the tree named `name` in any case, the names of each directory in order.
fn find_without_case(directory: &Path, name: &str) -> Option<PathBuf> {
    let mut entries: Vec<PathBuf> = fs::read_dir(directory).ok()?.flatten().map(|entry| entry.path()).collect();
    entries.sort();
    for path in &entries {
        let named = path.file_name().is_some_and(|own| own.to_string_lossy().eq_ignore_ascii_case(name));
        if named && path.is_file() {
            return Some(path.clone());
        }
    }
    entries.iter().filter(|path| path.is_dir()).find_map(|path| find_without_case(path, name))
}

/// Whether a program of a package — the shell or the backend beside it — is of the smoke build of updates (UB3.3, UB3.7).
pub fn is_smoke(program: &[u8]) -> bool {
    occurrences(program, SMOKE_MARK.as_bytes()) > 0
}
