// SPDX-License-Identifier: AGPL-3.0-only

//! «Стереть все данные» (specs/2026-11-13-desk-erase.md ER1.3–ER1.5): what the erasure removes on each system, the mark
//! that it goes on, and the removal itself. The removal takes only a directory of the app — its own name is a name of
//! the app on its system, it is neither the home nor above it, and no `..` climbs out of it — and follows no link out of
//! it: a link inside the data goes, what it points to stays. The mark lies beside the directory of the app in the local
//! data of the user, outside everything the erasure removes: an erasure is ended, or the next start ends it before it
//! reads anything (ER1.5). The mark names nothing; what to remove is computed from the places of the user every time. A
//! link in place of a directory of the app is followed once, when it names a directory of the app the author moved.
//! Before the data directory goes, the PostgreSQL of the app still running in it — a backend killed at a stop leaves its
//! server, which holds the files on Windows — stops by the program of the bundle (specs/2026-11-13-desk-upgrade.md UG6.1);
//! a data directory with no trace of the app and with files of something else is not erased at all (UG6.2).

use std::fs;
use std::io::{self, Write};
use std::path::{Path, PathBuf};
use std::process::{Command, Stdio};
use std::sync::Arc;
use std::thread;
use std::time::{Duration, Instant};

use crate::paths::{self, PathError, Places, System, APP_IDENTIFIER, DATA_NAME, WINDOWS_DATA_NAME};
use crate::secrets::{self, Keyring, Secrets, PROBE_TIMEOUT};

/// How long the removal keeps trying a file another program holds: the webview of the closed window lets go of its data
/// a moment later.
pub const PATIENCE: Duration = Duration::from_secs(10);
/// How long the records of the set may take to go: a store of the system that is locked asks the author first.
pub const RECORDS_TIMEOUT: Duration = Duration::from_secs(60);
/// The pause between two tries of the removal.
const RETRY: Duration = Duration::from_millis(100);
/// How long the stop of a PostgreSQL still running in the data directory may take: an immediate stop is a moment.
pub const CLUSTER_STOP: Duration = Duration::from_secs(15);
/// The clusters of the app in its data directory: the cluster of the author and the one a return restores into
/// (specs/2026-11-13-desk-upgrade.md UG4.2).
const CLUSTERS: [&str; 2] = ["pg", "pg.restoring"];

/// How the path of a directory of the app ends on each system — its own name, never the name of a directory it lies
/// in. Linux: the data directory and the directory of the identifier (the data of the window, the cache). Windows: the
/// directory of the identifier and the data directory in it; the installation of NSIS (`CanonKeeper`) is neither.
fn own_names(system: System) -> &'static [&'static [&'static str]] {
    match system {
        System::Linux => &[&[DATA_NAME], &[APP_IDENTIFIER]],
        System::Windows => &[&[APP_IDENTIFIER], &[APP_IDENTIFIER, WINDOWS_DATA_NAME]],
    }
}

/// What the erasure removes on this computer, and where its mark lies.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Targets {
    system: System,
    /// In the order they go: the data directory first.
    directories: Vec<String>,
    mark: String,
    home: Option<String>,
    /// `pg_ctl` of the bundle: it stops a PostgreSQL still running in the data directory before it goes (UG6.1).
    pg_ctl: Option<PathBuf>,
    /// The home of the user on Windows as the backend reads it (`USERPROFILE`, Go `os.UserHomeDir`): the directory of the
    /// sockets of the bridge `.canonkeeper` lies right in it (specs/2026-11-15-desk-mcp.md MC1.6, MC3.4).
    profile: Option<String>,
}

/// The directory of the sockets of the door of the bridge for the programs of the author on Windows, right in the home of
/// the user (specs/2026-11-15-desk-mcp.md MC1.6).
pub const SOCKETS_NAME: &str = ".canonkeeper";

/// Why an erasure did not end: the window of the start says this cause and no other.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Unfinished {
    /// A file of `folder` — a directory of the app, or the folder of the mark — is held by another program.
    Held { folder: String },
    /// The store of the system kept a record of the set: the directories are gone, the key is not.
    Kept,
}

/// What became of an erasure an earlier run did not end, found at the start before the window and the backend.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Earlier {
    /// No erasure was marked.
    None,
    /// The erasure ended now: the app starts as the first time.
    Ended,
    /// It did not end again: the window of the start says why, and no backend starts.
    Unended(Unfinished),
}

/// The directories of the app and its mark (specs/2026-11-13-desk-erase.md, the table of step 1).
///
/// - Windows: the directory of the identifier in `%LOCALAPPDATA%` — the data directory, the data of WebView2
///   (`EBWebView`) and the cache of the app are all in it; the data directory goes first.
/// - Linux: the data directory, the data of the window (WebKitGTK keeps its data and cache in the directory of the
///   identifier of the local data) and the cache of the app; the cache only when the system names a place for it.
pub fn targets(system: System, places: Places<'_>) -> Result<Targets, PathError> {
    let data_dir = paths::data_dir(system, places)?;
    let mark_name = format!("{APP_IDENTIFIER}.erasing");
    let (directories, mark) = match system {
        System::Windows => {
            let local = paths::plain(system, places.local_app_data.ok_or(PathError::Unknown)?)?;
            let root = system.join(&local, &[APP_IDENTIFIER]);
            (vec![data_dir, root], system.join(&local, &[&mark_name]))
        }
        System::Linux => {
            let data_home = paths::data_home(places)?;
            let mut directories = vec![data_dir, system.join(&data_home, &[APP_IDENTIFIER])];
            if let Ok(cache_home) = paths::cache_home(places) {
                directories.push(system.join(&cache_home, &[APP_IDENTIFIER]));
            }
            (directories, system.join(&data_home, &[&mark_name]))
        }
    };
    let home = places.home.filter(|home| !home.is_empty()).map(str::to_string);
    Ok(Targets { system, directories, mark, home, pg_ctl: None, profile: None })
}

impl Targets {
    /// The same targets with `pg_ctl` of the bundle, which stops a PostgreSQL still running in the data directory.
    pub fn with_pg_ctl(self, pg_ctl: PathBuf) -> Targets {
        Targets { pg_ctl: Some(pg_ctl), ..self }
    }

    /// The same targets on Windows with the directory of the sockets of the bridge in the home of `USERPROFILE` — the home
    /// of the backend, not the home of the erasure, which reads `HOME` first (specs/2026-11-15-desk-mcp.md MC3.4).
    pub fn with_profile(mut self, profile: Option<&str>) -> Targets {
        if self.system != System::Windows {
            return self;
        }
        let Some(profile) =
            profile.filter(|profile| !profile.is_empty()).and_then(|profile| paths::plain(self.system, profile).ok())
        else {
            return self;
        };
        self.directories.push(self.system.join(&profile, &[SOCKETS_NAME]));
        self.profile = Some(profile);
        self
    }

    /// Whether the erasure takes `directory`: a directory of the app, or the directory of the sockets right in the home of
    /// `USERPROFILE`.
    fn takes(&self, directory: &str) -> bool {
        inside_the_app(self.system, directory, self.home.as_deref())
            || sockets_of_the_app(self.system, directory, self.profile.as_deref())
    }

    /// Whether the data directory may go (UG6.2): it is not there or empty, or it holds a trace of the app — its
    /// cluster, `settings.json`, `secrets.json` or a log of the shell. A directory of the name of the app that holds
    /// something else and nothing of the app is not erased.
    pub fn data_is_the_apps(&self) -> bool {
        let data = Path::new(self.data_dir());
        let Ok(mut entries) = fs::read_dir(data) else { return !data.exists() };
        if entries.next().is_none() {
            return true;
        }
        let shell_log = fs::read_dir(data.join("logs")).is_ok_and(|logs| {
            logs.flatten().any(|entry| {
                let name = entry.file_name();
                let name = name.to_string_lossy();
                name.starts_with("shell-") && name.ends_with(".log")
            })
        });
        shell_log
            || data.join("pg").join("PG_VERSION").is_file()
            || data.join("settings.json").is_file()
            || data.join(secrets::FILE).is_file()
    }

    /// Stops a PostgreSQL still running in a cluster of the data directory (UG6.1): `postmaster.pid` names it, and the
    /// program of the bundle stops it at once, within `CLUSTER_STOP`. Whether it stopped or not, the removal goes on — a
    /// file it still holds keeps the erasure marked, as a file of another program does.
    pub fn stop_clusters(&self) -> Vec<bool> {
        let Some(pg_ctl) = &self.pg_ctl else { return Vec::new() };
        CLUSTERS
            .iter()
            .map(|cluster| PathBuf::from(self.system.join(self.data_dir(), &[cluster])))
            .filter(|cluster| cluster.join("postmaster.pid").exists())
            .map(|cluster| stop_cluster(pg_ctl, &cluster, CLUSTER_STOP))
            .collect()
    }
    pub fn directories(&self) -> &[String] {
        &self.directories
    }

    /// The data directory of the user, where `secrets.json` lies without a store of the system.
    pub fn data_dir(&self) -> &str {
        &self.directories[0]
    }

    pub fn mark(&self) -> &str {
        &self.mark
    }

    /// The home folder of the user, when the system names one: a folder of a vault is never it
    /// (specs/2026-11-13-desk-obsidian.md OB1.3).
    pub fn home(&self) -> Option<&str> {
        self.home.as_deref()
    }

    /// Whether an erasure is marked: an earlier run did not end it.
    pub fn marked(&self) -> bool {
        fs::symlink_metadata(&self.mark).is_ok()
    }

    /// Marks the erasure before anything is removed; it is written through, so a stop of the computer keeps it. An
    /// erasure with a directory the removal refuses (ER1.4) could never end and would keep the app from starting again:
    /// it is not marked, and does not begin.
    pub fn set_mark(&self) -> io::Result<()> {
        if !self.directories.iter().all(|directory| self.takes(directory)) {
            return Err(io::Error::new(io::ErrorKind::InvalidInput, "a directory the removal does not take"));
        }
        if !self.data_is_the_apps() {
            return Err(io::Error::new(io::ErrorKind::InvalidInput, "a data directory with nothing of the app"));
        }
        let mark = Path::new(&self.mark);
        if let Some(parent) = mark.parent() {
            fs::create_dir_all(parent)?;
        }
        let mut file = fs::OpenOptions::new().write(true).create(true).truncate(true).open(mark)?;
        file.write_all(b"erasing\n")?;
        file.sync_all()
    }

    /// Takes the mark away once nothing of the app is left.
    pub fn unmark(&self) -> io::Result<()> {
        match fs::remove_file(&self.mark) {
            Err(error) if error.kind() != io::ErrorKind::NotFound => Err(error),
            _ => Ok(()),
        }
    }

    /// Removes every directory of the app, trying a held file again for `patience`; the first directory that could not
    /// be removed whole is named. What could go goes either way. A PostgreSQL still running in the data directory stops
    /// first.
    pub fn remove(&self, patience: Duration) -> Result<(), Unfinished> {
        self.stop_clusters();
        let mut first = None;
        for directory in &self.directories {
            let removed = if sockets_of_the_app(self.system, directory, self.profile.as_deref()) {
                remove_tree(directory, Instant::now() + patience)
            } else {
                remove_directory(self.system, directory, self.home.as_deref(), patience)
            };
            if removed.is_err() && first.is_none() {
                first = Some(directory.clone());
            }
        }
        match first {
            Some(folder) => Err(Unfinished::Held { folder }),
            None => Ok(()),
        }
    }
}

/// Why a directory was not removed.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Refusal {
    /// The path is not a directory of the app: the removal does not touch it.
    Outside,
    /// Something in it stayed after `patience`: another program holds it.
    Held,
}

/// Whether `path` is a directory of the app (ER1.4): absolute, without `..` (it may climb out through a link), its own
/// name a name of the app on `system` (see [`own_names`]; case counts on Linux only), and neither `home` nor above it.
/// The root, the home, an empty path and a directory inside a directory of the app are not; a doubled separator or `.`
/// reads as the system reads it. A home written with `..` cannot be compared, and then nothing is.
pub fn inside_the_app(system: System, path: &str, home: Option<&str>) -> bool {
    let Some(names) = names_of(system, path) else { return false };
    if names.iter().any(|name| name == "..") {
        return false;
    }
    let same = |left: &str, right: &str| match system {
        System::Windows => left.to_lowercase() == right.to_lowercase(),
        System::Linux => left == right,
    };
    let own = own_names(system).iter().any(|tail| {
        names.len() >= tail.len()
            && names[names.len() - tail.len()..].iter().zip(*tail).all(|(name, own)| same(name, own))
    });
    if !own {
        return false;
    }
    let Some(home) = home.and_then(|home| names_of(system, home)) else { return true };
    if home.iter().any(|name| name == "..") {
        return false;
    }
    // The path is the home or one of the directories the home lies in.
    let above = names.len() <= home.len() && names.iter().zip(&home).all(|(name, part)| same(name, part));
    !above
}

/// Whether `path` is the directory of the sockets of the bridge (MC3.4): on Windows `.canonkeeper` right in `profile`, the
/// home of `USERPROFILE`; nowhere else, and on no other system.
pub fn sockets_of_the_app(system: System, path: &str, profile: Option<&str>) -> bool {
    if system != System::Windows {
        return false;
    }
    let (Some(names), Some(home)) = (names_of(system, path), profile.and_then(|profile| names_of(system, profile)))
    else {
        return false;
    };
    if names.iter().chain(&home).any(|name| name == "..") || names.len() != home.len() + 1 {
        return false;
    }
    let same = |left: &str, right: &str| left.to_lowercase() == right.to_lowercase();
    names.iter().zip(&home).all(|(name, part)| same(name, part)) && same(&names[home.len()], SOCKETS_NAME)
}

/// The names of an absolute plain path — on Windows its drive first — or `None` for a path that is neither. Empty names
/// and `.` are left out: the system reads `a//b` and `a/./b` as `a/b`.
fn names_of(system: System, path: &str) -> Option<Vec<String>> {
    let plain = paths::plain(system, path).ok()?;
    let separators: &[char] = match system {
        System::Windows => &['\\', '/'],
        System::Linux => &['/'],
    };
    let (first, rest) = match system {
        // `plain` has checked the drive: a letter, a colon and a separator.
        System::Windows => plain.split_at(2),
        System::Linux => ("", plain.as_str()),
    };
    let mut names: Vec<String> =
        Some(first).filter(|drive| !drive.is_empty()).map(str::to_string).into_iter().collect();
    names.extend(rest.split(separators).filter(|name| !name.is_empty() && *name != ".").map(str::to_string));
    Some(names)
}

/// Removes a directory of the app and everything in it, without following a link: a link goes, what it points to stays.
/// One link is followed — one in place of the directory itself, when what it names is a directory of the app as well:
/// the author moved the directory elsewhere (ER1.3). A path outside the directories of the app is refused before
/// anything is touched; a held file is tried again until `patience` passed.
pub fn remove_directory(system: System, path: &str, home: Option<&str>, patience: Duration) -> Result<(), Refusal> {
    if !inside_the_app(system, path, home) {
        return Err(Refusal::Outside);
    }
    let deadline = Instant::now() + patience;
    if let Some(moved) = moved(system, path, home) {
        remove_tree(&moved, deadline)?;
    }
    remove_tree(path, deadline)
}

/// What a link in place of a directory of the app names, when that is a directory of the app too: resolved, it carries
/// a name of the app as its own and is neither the home nor above it — the home as given and as the system resolves it.
/// A link to anything else is only a link.
fn moved(system: System, path: &str, home: Option<&str>) -> Option<String> {
    if !fs::symlink_metadata(path).ok()?.file_type().is_symlink() {
        return None;
    }
    let resolved = |path: &str| paths::plain(system, fs::canonicalize(path).ok()?.to_str()?).ok();
    let target = resolved(path)?;
    let resolved_home = home.and_then(resolved);
    let inside = inside_the_app(system, &target, home)
        && resolved_home.as_deref().is_none_or(|home| inside_the_app(system, &target, Some(home)));
    inside.then_some(target)
}

/// Removes a path whole without following a link, trying a held file again until `deadline`.
fn remove_tree(path: &str, deadline: Instant) -> Result<(), Refusal> {
    loop {
        let removed = match fs::symlink_metadata(path) {
            Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok(()),
            Ok(metadata) if metadata.is_file() => fs::remove_file(path),
            // A directory, or a link — `remove_dir_all` takes the link itself, never what it points to.
            _ => fs::remove_dir_all(path),
        };
        match removed {
            Ok(()) => return Ok(()),
            Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok(()),
            Err(_) if Instant::now() >= deadline => return Err(Refusal::Held),
            Err(_) => thread::sleep(RETRY),
        }
    }
}

/// Stops the PostgreSQL of `cluster` at once: `pg_ctl stop -m immediate`, killed when it takes longer than `limit`.
fn stop_cluster(pg_ctl: &Path, cluster: &Path, limit: Duration) -> bool {
    let started = Command::new(pg_ctl)
        .args(["stop", "-m", "immediate", "-w", "-t", "10", "-D"])
        .arg(cluster)
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .spawn();
    let Ok(mut child) = started else { return false };
    let deadline = Instant::now() + limit;
    loop {
        match child.try_wait() {
            Ok(Some(status)) => return status.success(),
            Ok(None) if Instant::now() < deadline => thread::sleep(RETRY),
            _ => {
                let _ = child.kill();
                let _ = child.wait();
                return false;
            }
        }
    }
}

/// Ends an erasure an earlier run marked and did not end (ER1.5), before the window and the backend: the records of the
/// set wherever they lie, the directories of the app, then the mark. `windows`: the system is Windows, whose store of
/// secrets is always there.
pub fn finish(targets: &Targets, keyring: Arc<dyn Keyring>, windows: bool, patience: Duration) -> Earlier {
    if !targets.marked() {
        return Earlier::None;
    }
    let file = PathBuf::from(targets.system.join(targets.data_dir(), &[secrets::FILE]));
    let (secrets, _) = Secrets::open(file, keyring, windows, PROBE_TIMEOUT);
    let records = secrets.erase(RECORDS_TIMEOUT);
    let removed = targets.remove(patience);
    match (records, removed) {
        (_, Err(unfinished)) => Earlier::Unended(unfinished),
        // The store of the system kept a record: the data are gone, the key is not.
        (Err(_), Ok(())) => Earlier::Unended(Unfinished::Kept),
        (Ok(()), Ok(())) => match targets.unmark() {
            Ok(()) => Earlier::Ended,
            // The mark is the file left: its folder is named.
            Err(_) => Earlier::Unended(Unfinished::Held { folder: parent(targets.system, &targets.mark) }),
        },
    }
}

fn parent(system: System, path: &str) -> String {
    let separator = match system {
        System::Windows => '\\',
        System::Linux => '/',
    };
    path.rsplit_once(separator).map(|(parent, _)| parent.to_string()).unwrap_or_default()
}

#[cfg(test)]
mod tests {
    use super::*;

    // specs/2026-11-15-desk-mcp.md MC3.4: the directory of the sockets of the bridge goes with the erasure on Windows — right in
    // the home of USERPROFILE, the home of the backend, even when HOME names another place; `.canonkeeper` anywhere else is
    // no directory of the app, and Linux has none.
    #[test]
    fn windows_erases_the_sockets_of_the_bridge_in_the_home_of_the_profile() {
        let places =
            Places {
                local_app_data: Some(r"C:\Users\Тест\AppData\Local"), home: Some(r"D:\h"), ..Places::default()
            };
        let mut windows = targets(System::Windows, places).unwrap().with_profile(Some(r"C:\Users\Тест"));
        assert_eq!(windows.directories().last().map(String::as_str), Some(r"C:\Users\Тест\.canonkeeper"));
        // The mark of the erasure is set: every directory, `.canonkeeper` of the profile among them, is one the removal takes.
        // The mark lies in a place of the test: the paths of Windows are only names here.
        let root = stand("profile");
        windows.mark = root.join("erasing").to_string_lossy().into_owned();
        windows.set_mark().expect("the mark of the erasure is not set");
        assert!(windows.marked());
        windows.unmark().unwrap();
        fs::remove_dir_all(root).unwrap();
        assert!(sockets_of_the_app(System::Windows, r"C:\Users\Тест\.CanonKeeper", Some(r"C:\Users\Тест")));
        for outside in [
            r"C:\Users\Тест\Documents\.canonkeeper",
            r"C:\.canonkeeper",
            r"C:\Users\Тест\.canonkeeper\.canonkeeper",
            r"C:\Users\Тест\.canonkeeper\x",
        ] {
            assert!(!sockets_of_the_app(System::Windows, outside, Some(r"C:\Users\Тест")), "{outside}");
            assert!(!inside_the_app(System::Windows, outside, Some(r"C:\Users\Тест")), "{outside}");
        }
        assert!(!sockets_of_the_app(System::Windows, r"C:\Users\Тест\.canonkeeper", None));
        assert!(!sockets_of_the_app(System::Linux, "/home/тест/.canonkeeper", Some("/home/тест")));
        let linux =
            Places {
                xdg_data_home: Some("/home/тест/.local/share"), home: Some("/home/тест"), ..Places::default()
            };
        let unchanged = targets(System::Linux, linux).unwrap();
        assert_eq!(unchanged.clone().with_profile(Some("/home/тест")), unchanged);
    }

    #[test]
    fn windows_erases_the_directory_of_the_identifier_with_the_data_first_and_marks_beside_it() {
        let places = Places { local_app_data: Some(r"C:\Users\Тест\AppData\Local"), ..Places::default() };
        let targets = targets(System::Windows, places).unwrap();
        assert_eq!(
            targets.directories(),
            [
                r"C:\Users\Тест\AppData\Local\ru.canonkeeper.desktop\data".to_string(),
                r"C:\Users\Тест\AppData\Local\ru.canonkeeper.desktop".to_string(),
            ]
        );
        assert_eq!(targets.data_dir(), paths::data_dir(System::Windows, places).unwrap());
        assert_eq!(targets.mark(), r"C:\Users\Тест\AppData\Local\ru.canonkeeper.desktop.erasing");
        // The installation of NSIS for the user lies apart, in %LOCALAPPDATA%\CanonKeeper: it is not erased.
        assert!(!targets.directories().iter().any(|directory| directory.ends_with(r"\CanonKeeper")));
    }

    #[test]
    fn linux_erases_the_data_the_window_and_the_cache_and_marks_beside_them() {
        let places = Places {
            xdg_data_home: Some("/home/тест/.data"),
            xdg_cache_home: Some("/home/тест/.kesh/"),
            home: Some("/home/тест"),
            ..Places::default()
        };
        let erased = targets(System::Linux, places).unwrap();
        assert_eq!(
            erased.directories(),
            [
                "/home/тест/.data/canonkeeper".to_string(),
                "/home/тест/.data/ru.canonkeeper.desktop".to_string(),
                "/home/тест/.kesh/ru.canonkeeper.desktop".to_string(),
            ]
        );
        assert_eq!(erased.data_dir(), paths::data_dir(System::Linux, places).unwrap());
        assert_eq!(erased.mark(), "/home/тест/.data/ru.canonkeeper.desktop.erasing");

        let plain = targets(System::Linux, Places { home: Some("/home/a"), ..Places::default() }).unwrap();
        assert_eq!(
            plain.directories(),
            [
                "/home/a/.local/share/canonkeeper".to_string(),
                "/home/a/.local/share/ru.canonkeeper.desktop".to_string(),
                "/home/a/.cache/ru.canonkeeper.desktop".to_string(),
            ]
        );
        // Without a home the cache has no place, and only the data and the window are erased.
        let no_home = targets(System::Linux, Places { xdg_data_home: Some("/d"), ..Places::default() }).unwrap();
        assert_eq!(no_home.directories().len(), 2);
        assert_eq!(targets(System::Linux, Places::default()), Err(PathError::Unknown));
    }

    #[test]
    fn only_a_directory_of_the_app_is_inside_the_app() {
        let home = Some("/home/тест");
        for inside in [
            "/home/тест/.local/share/canonkeeper",
            "/home/тест/.local/share/ru.canonkeeper.desktop",
            "/home/тест/.cache/ru.canonkeeper.desktop/",
            "/canonkeeper",
            // The same directories with a doubled separator or `.`: the system reads them as the plain ones.
            "/home/тест//.local/share/canonkeeper",
            "/home/тест/./.local/share/./ru.canonkeeper.desktop/.",
        ] {
            assert!(inside_the_app(System::Linux, inside, home), "{inside}");
        }
        for outside in [
            "",
            "/",
            "//",
            "/home/тест",
            "/home/тест/",
            "/home/тест/.",
            "/home/тест//",
            "/home",
            "relative/canonkeeper",
            "/home/тест/.local/share/canonkeeper/..",
            "/home/тест/.local/share/canonkeeper/../../..",
            // `..` may climb out through a link: a path with it is never the app's.
            "/home/тест/x/../canonkeeper",
            "/home/тест/Документы",
            "/home/тест/canonkeeper-old",
            // A name of the app above a directory does not make it the app's: only its own name does.
            "/home/тест/canonkeeper/Книги",
            "/home/тест/.local/share/canonkeeper/pg",
            "/home/тест/.local/share/ru.canonkeeper.desktop/storage",
            // `data` in the directory of the identifier is the data directory of Windows only; the case counts on Linux.
            "/home/тест/.local/share/ru.canonkeeper.desktop/data",
            "/usr/lib/CanonKeeper",
            "/home/тест/.local/share/CanonKeeper",
        ] {
            assert!(!inside_the_app(System::Linux, outside, home), "{outside:?}");
        }
        // A home inside a directory named after the app: the directory is above the home.
        assert!(!inside_the_app(System::Linux, "/srv/canonkeeper", Some("/srv/canonkeeper/users/тест")));
        // A home written with `..` cannot be compared: nothing is the app's.
        assert!(!inside_the_app(System::Linux, "/srv/canonkeeper", Some("/srv/x/../canonkeeper")));
        let windows_home = Some(r"C:\Users\Тест");
        for inside in [
            r"C:\Users\Тест\AppData\Local\ru.canonkeeper.desktop",
            r"C:\Users\Тест\AppData\Local\RU.CanonKeeper.Desktop\data",
            r"C:\Users\Тест\AppData\Local\\ru.canonkeeper.desktop\.\data\",
        ] {
            assert!(inside_the_app(System::Windows, inside, windows_home), "{inside}");
        }
        for outside in [
            r"C:\",
            r"C:\Users\Тест",
            r"c:\users\тест\",
            r"\\server\share\canonkeeper",
            r"C:\Users\Тест\AppData\Local",
            // The installation of NSIS and other directories that only sound like the app.
            r"C:\Users\Тест\AppData\Local\CanonKeeper",
            r"C:\Program Files\CanonKeeper",
            r"C:\CanonKeeper",
            r"C:\Users\Тест\AppData\Local\canonkeeper",
            r"C:\Users\Тест\AppData\Local\data",
            r"C:\Users\Тест\AppData\Local\ru.canonkeeper.desktop\EBWebView",
            r"C:\Users\Тест\AppData\Local\ru.canonkeeper.desktop\data\pg",
            r"C:\Users\Тест\AppData\Local\x\..\ru.canonkeeper.desktop",
        ] {
            assert!(!inside_the_app(System::Windows, outside, windows_home), "{outside}");
        }
    }

    #[test]
    fn an_erasure_the_removal_would_refuse_is_not_marked() {
        // ER1.4, ER1.5: a place of the user written with `..` names directories the removal does not take; marked, the
        // erasure would never end and the app would not start again. It does not begin. A doubled separator is the same
        // place: that erasure is marked and ends.
        let root = stand("climb");
        let text = |path: PathBuf| path.to_string_lossy().into_owned();
        let (cache_home, home) = (text(root.join("cache")), text(root.join("дом")));
        let climbing = text(root.join("data").join("x").join(".."));
        let places = Places {
            local_app_data: Some(&climbing),
            xdg_data_home: Some(&climbing),
            xdg_cache_home: Some(&cache_home),
            home: Some(&home),
        };
        let refused = targets(System::CURRENT, places).unwrap();
        assert!(refused.set_mark().is_err());
        assert!(!refused.marked() && !root.join("data").exists(), "the erasure was marked");

        let separator = if cfg!(windows) { '\\' } else { '/' };
        let doubled = format!("{}{separator}{separator}data", text(root.clone()));
        let places = Places { local_app_data: Some(&doubled), xdg_data_home: Some(&doubled), ..places };
        let marked = targets(System::CURRENT, places).unwrap();
        for directory in marked.directories() {
            fs::create_dir_all(Path::new(directory).join("x")).unwrap();
        }
        fs::write(Path::new(marked.data_dir()).join("settings.json"), "{}").unwrap();
        marked.set_mark().unwrap();
        assert_eq!(marked.remove(Duration::ZERO), Ok(()));
        assert!(!root.join("data").join(DATA_NAME).exists() && !root.join("data").join(APP_IDENTIFIER).exists());
        marked.unmark().unwrap();
        fs::remove_dir_all(root).unwrap();
    }

    fn stand(name: &str) -> PathBuf {
        let root = std::env::temp_dir().join(format!("canonkeeper-erase-{name}-{}", std::process::id()));
        let _ = fs::remove_dir_all(&root);
        fs::create_dir_all(&root).unwrap();
        root
    }

    #[test]
    fn a_data_directory_with_nothing_of_the_app_is_not_erased() {
        // specs/2026-11-13-desk-upgrade.md UG6.2: a directory of the name of the app that holds the files of another and
        // nothing of the app — no cluster, settings.json, secrets.json or log of the shell — is not erased; an empty one,
        // one that is not there and one with a trace of the app are.
        let root = stand("trace");
        let text = |path: PathBuf| path.to_string_lossy().into_owned();
        let (data_home, cache_home, home) = (text(root.join("data")), text(root.join("cache")), text(root.join("дом")));
        let places = Places {
            local_app_data: Some(&data_home),
            xdg_data_home: Some(&data_home),
            xdg_cache_home: Some(&cache_home),
            home: Some(&home),
        };
        let targets = targets(System::CURRENT, places).unwrap();
        let data = PathBuf::from(targets.data_dir());
        assert!(targets.data_is_the_apps(), "a data directory that is not there");
        fs::create_dir_all(&data).unwrap();
        assert!(targets.data_is_the_apps(), "an empty data directory");
        fs::create_dir_all(data.join("Книги")).unwrap();
        fs::write(data.join("Книги").join("Северный предел.fb2"), "чужое").unwrap();
        fs::create_dir_all(data.join("logs")).unwrap();
        fs::write(data.join("logs").join("backup.log"), "чужое").unwrap();
        assert!(!targets.data_is_the_apps());
        assert!(targets.set_mark().is_err() && !targets.marked());
        assert_eq!(fs::read_to_string(data.join("Книги").join("Северный предел.fb2")).unwrap(), "чужое");
        for trace in ["settings.json", secrets::FILE, "logs/shell-2026-11-13.log", "pg/PG_VERSION"] {
            let path = data.join(trace);
            fs::create_dir_all(path.parent().unwrap()).unwrap();
            fs::write(&path, "18").unwrap();
            assert!(targets.data_is_the_apps(), "{trace}");
            fs::remove_file(&path).unwrap();
        }
        fs::remove_dir_all(root).unwrap();
    }

    #[cfg(unix)]
    #[test]
    fn a_cluster_still_running_in_the_data_directory_is_stopped_before_it_goes() {
        // UG6.1: the program of the bundle stops a cluster whose postmaster.pid is there, at once; a cluster without one
        // is not asked; a program that does not answer in time is killed and the removal goes on.
        use std::os::unix::fs::PermissionsExt;
        let root = stand("cluster");
        let text = |path: PathBuf| path.to_string_lossy().into_owned();
        let (data_home, cache_home, home) = (text(root.join("data")), text(root.join("cache")), text(root.join("дом")));
        let places = Places {
            local_app_data: Some(&data_home),
            xdg_data_home: Some(&data_home),
            xdg_cache_home: Some(&cache_home),
            home: Some(&home),
        };
        let record = root.join("called");
        let pg_ctl = root.join("pg_ctl");
        fs::write(&pg_ctl, format!("#!/bin/sh\necho \"$@\" >> '{}'\n", record.display())).unwrap();
        fs::set_permissions(&pg_ctl, fs::Permissions::from_mode(0o755)).unwrap();
        let targets = targets(System::CURRENT, places).unwrap().with_pg_ctl(pg_ctl.clone());
        let data = PathBuf::from(targets.data_dir());
        fs::create_dir_all(data.join("pg")).unwrap();
        assert!(targets.stop_clusters().is_empty(), "a cluster without postmaster.pid was stopped");
        fs::write(data.join("pg").join("postmaster.pid"), "4242").unwrap();
        fs::create_dir_all(data.join("pg.restoring")).unwrap();
        fs::write(data.join("pg.restoring").join("postmaster.pid"), "4343").unwrap();
        assert_eq!(targets.stop_clusters(), vec![true, true]);
        let called = fs::read_to_string(&record).unwrap();
        assert_eq!(
            called.lines().collect::<Vec<_>>(),
            vec![
                format!("stop -m immediate -w -t 10 -D {}", data.join("pg").display()),
                format!("stop -m immediate -w -t 10 -D {}", data.join("pg.restoring").display()),
            ]
        );
        fs::write(&pg_ctl, "#!/bin/sh\nexit 1\n").unwrap();
        assert_eq!(targets.stop_clusters(), vec![false, false]);
        assert_eq!(targets.remove(Duration::ZERO), Ok(()));
        assert!(!data.exists());
        fs::remove_dir_all(root).unwrap();
    }

    #[test]
    fn a_path_outside_the_app_is_refused_and_a_link_out_of_the_data_is_not_followed() {
        // ER1.4: a directory of another program is refused whole; a link from the data directory to the files of the
        // author goes, the files stay.
        let root = stand("outside");
        let text = |path: &Path| path.to_string_lossy().into_owned();
        let foreign = root.join("Книги");
        fs::create_dir_all(&foreign).unwrap();
        fs::write(foreign.join("Северный предел.fb2"), "книга").unwrap();
        let home = text(&root.join("дом"));
        assert_eq!(
            remove_directory(System::CURRENT, &text(&foreign), Some(&home), Duration::ZERO),
            Err(Refusal::Outside)
        );
        assert_eq!(remove_directory(System::CURRENT, "", Some(&home), Duration::ZERO), Err(Refusal::Outside));
        assert_eq!(remove_directory(System::CURRENT, &home, Some(&home), Duration::ZERO), Err(Refusal::Outside));
        assert!(foreign.join("Северный предел.fb2").exists());

        // The data directory of the stand carries the own name of the app on this system: `canonkeeper` on Linux,
        // `ru.canonkeeper.desktop\data` on Windows.
        let data_home = text(&root.join("data"));
        let places = Places { local_app_data: Some(&data_home), xdg_data_home: Some(&data_home), ..Places::default() };
        let data = PathBuf::from(paths::data_dir(System::CURRENT, places).unwrap());
        fs::create_dir_all(data.join("files")).unwrap();
        fs::write(data.join("files").join("cover.png"), "обложка").unwrap();
        let linked = {
            #[cfg(unix)]
            {
                std::os::unix::fs::symlink(&foreign, data.join("files").join("книги")).is_ok()
            }
            #[cfg(windows)]
            {
                // A link of a directory needs the right to make one; without it the link is not part of the test.
                std::os::windows::fs::symlink_dir(&foreign, data.join("files").join("книги")).is_ok()
            }
        };
        assert_eq!(remove_directory(System::CURRENT, &text(&data), Some(&home), Duration::ZERO), Ok(()));
        assert!(!data.exists());
        assert!(foreign.join("Северный предел.fb2").exists(), "the link was followed (linked: {linked})");

        // The data directory itself a link to a directory that is not the app's: the link goes, the directory it names
        // stays. On Windows only where the right to make a link of a directory is, as above.
        let linked_data = {
            #[cfg(unix)]
            {
                std::os::unix::fs::symlink(&foreign, &data).unwrap();
                true
            }
            #[cfg(windows)]
            {
                std::os::windows::fs::symlink_dir(&foreign, &data).is_ok()
            }
        };
        if linked_data {
            assert_eq!(remove_directory(System::CURRENT, &text(&data), Some(&home), Duration::ZERO), Ok(()));
            assert!(fs::symlink_metadata(&data).is_err());
            assert!(foreign.join("Северный предел.fb2").exists());
        }
        fs::remove_dir_all(root).unwrap();
    }

    #[cfg(unix)]
    #[test]
    fn a_directory_of_the_app_moved_elsewhere_behind_a_link_goes_with_the_link() {
        // ER1.3 and ER1.4: the author moved the data directory to another disk and left a link in its place. What the link
        // names carries the name of the app — it is the data directory, moved — and goes, then the link; a link inside it
        // is still not followed. A link to the home or above it is only a link, though their name be the app's.
        use std::os::unix::fs::symlink;
        // The resolved paths of the stand are compared with the home as given: the stand itself is read resolved.
        let root = fs::canonicalize(stand("moved")).unwrap();
        let text = |path: &Path| path.to_string_lossy().into_owned();
        let foreign = root.join("Книги");
        fs::create_dir_all(&foreign).unwrap();
        fs::write(foreign.join("Северный предел.fb2"), "книга").unwrap();
        let home = text(&root.join("дом"));
        let data = root.join("share").join("canonkeeper");
        fs::create_dir_all(data.parent().unwrap()).unwrap();

        let moved = root.join("другой диск").join("canonkeeper");
        fs::create_dir_all(moved.join("pg")).unwrap();
        fs::write(moved.join("pg").join("1259"), "строки саги").unwrap();
        symlink(&foreign, moved.join("книги")).unwrap();
        symlink(&moved, &data).unwrap();
        assert_eq!(remove_directory(System::CURRENT, &text(&data), Some(&home), Duration::ZERO), Ok(()));
        assert!(fs::symlink_metadata(&data).is_err(), "the link stayed");
        assert!(!moved.exists(), "the moved data directory stayed");
        assert!(foreign.join("Северный предел.fb2").exists(), "a link inside the moved directory was followed");

        // A home inside a directory named after the app: a link to that directory takes only itself.
        let above = root.join("srv").join("canonkeeper");
        let inner_home = above.join("users").join("тест");
        fs::create_dir_all(&inner_home).unwrap();
        symlink(&above, &data).unwrap();
        assert_eq!(remove_directory(System::CURRENT, &text(&data), Some(&text(&inner_home)), Duration::ZERO), Ok(()));
        assert!(fs::symlink_metadata(&data).is_err() && inner_home.exists());
        // The home named after the app, read through a link of its own: resolved, it is the home, and stays.
        let named_home = root.join("homes").join("canonkeeper");
        fs::create_dir_all(&named_home).unwrap();
        symlink(root.join("homes"), root.join("home-link")).unwrap();
        symlink(&named_home, &data).unwrap();
        let home_as_given = text(&root.join("home-link").join("canonkeeper"));
        assert_eq!(remove_directory(System::CURRENT, &text(&data), Some(&home_as_given), Duration::ZERO), Ok(()));
        assert!(fs::symlink_metadata(&data).is_err() && named_home.exists());
        fs::remove_dir_all(root).unwrap();
    }

    #[test]
    fn a_marked_erasure_ends_at_the_start_and_takes_the_records_wherever_they_lie() {
        let root = stand("finish");
        let text = |path: PathBuf| path.to_string_lossy().into_owned();
        let (data_home, cache_home, home) = (text(root.join("data")), text(root.join("cache")), text(root.join("дом")));
        let places = Places {
            local_app_data: Some(&data_home),
            xdg_data_home: Some(&data_home),
            xdg_cache_home: Some(&cache_home),
            home: Some(&home),
        };
        let targets = targets(System::CURRENT, places).unwrap();
        let keyring: Arc<dyn Keyring> = Arc::new(NoStore);
        assert_eq!(finish(&targets, Arc::clone(&keyring), cfg!(windows), Duration::ZERO), Earlier::None);

        for directory in targets.directories() {
            fs::create_dir_all(Path::new(directory).join("x")).unwrap();
        }
        fs::write(Path::new(targets.data_dir()).join(secrets::FILE), r#"{"provider:own":"key-test-north"}"#).unwrap();
        targets.set_mark().unwrap();
        assert!(targets.marked());
        let earlier = finish(&targets, keyring, cfg!(windows), Duration::ZERO);
        if cfg!(windows) {
            // Windows always has its store; the store of the test refuses, so the erasure says the key stayed.
            assert_eq!(earlier, Earlier::Unended(Unfinished::Kept));
        } else {
            assert_eq!(earlier, Earlier::Ended);
            assert!(!targets.marked());
        }
        for directory in targets.directories() {
            assert!(!Path::new(directory).exists(), "{directory}");
        }
        fs::remove_dir_all(root).unwrap();
    }

    #[test]
    fn a_store_of_the_system_that_keeps_a_record_leaves_the_erasure_marked() {
        // Decision log of the spec: a store of the system that works must remove every record of the set, or the erasure
        // has not ended — the directories go, the mark stays, and the next start asks the store again.
        let root = stand("kept");
        let text = |path: PathBuf| path.to_string_lossy().into_owned();
        let (data_home, cache_home, home) = (text(root.join("data")), text(root.join("cache")), text(root.join("дом")));
        let places = Places {
            local_app_data: Some(&data_home),
            xdg_data_home: Some(&data_home),
            xdg_cache_home: Some(&cache_home),
            home: Some(&home),
        };
        let targets = targets(System::CURRENT, places).unwrap();
        for directory in targets.directories() {
            fs::create_dir_all(Path::new(directory).join("x")).unwrap();
        }
        fs::write(Path::new(targets.data_dir()).join("settings.json"), "{}").unwrap();
        targets.set_mark().unwrap();
        let store = Arc::new(Keeping::default());
        store.refuses.store(true, std::sync::atomic::Ordering::SeqCst);
        store.set(secrets::OWN_AI, "key-test-north").unwrap();

        let earlier = finish(&targets, Arc::clone(&store) as Arc<dyn Keyring>, cfg!(windows), Duration::ZERO);
        // The words say the key stayed, not that a folder that is gone is held.
        assert_eq!(earlier, Earlier::Unended(Unfinished::Kept));
        assert!(targets.marked(), "an erasure the store of the system did not end lost its mark");
        for directory in targets.directories() {
            assert!(!Path::new(directory).exists(), "{directory}");
        }
        assert_eq!(store.get(secrets::OWN_AI), Ok(Some("key-test-north".to_string())));

        // The store lets go of the record: the next start ends the erasure.
        store.refuses.store(false, std::sync::atomic::Ordering::SeqCst);
        assert_eq!(
            finish(&targets, Arc::clone(&store) as Arc<dyn Keyring>, cfg!(windows), Duration::ZERO),
            Earlier::Ended
        );
        assert!(!targets.marked());
        assert_eq!(store.get(secrets::OWN_AI), Ok(None));
        fs::remove_dir_all(root).unwrap();
    }

    /// A store of the system that works — it takes the probe — and, while it `refuses`, will not remove a record of the
    /// set: a keyring locked again before the erasure, a refusal of the Credential Manager.
    #[derive(Default)]
    struct Keeping {
        records: std::sync::Mutex<std::collections::HashMap<String, String>>,
        refuses: std::sync::atomic::AtomicBool,
    }

    impl Keyring for Keeping {
        fn set(&self, id: &str, value: &str) -> Result<(), secrets::Failure> {
            self.records.lock().unwrap().insert(id.to_string(), value.to_string());
            Ok(())
        }
        fn get(&self, id: &str) -> Result<Option<String>, secrets::Failure> {
            Ok(self.records.lock().unwrap().get(id).cloned())
        }
        fn delete(&self, id: &str) -> Result<(), secrets::Failure> {
            if secrets::known(id) && self.refuses.load(std::sync::atomic::Ordering::SeqCst) {
                return Err(secrets::Failure);
            }
            self.records.lock().unwrap().remove(id);
            Ok(())
        }
    }

    /// A system without a store of secrets.
    struct NoStore;

    impl Keyring for NoStore {
        fn set(&self, _: &str, _: &str) -> Result<(), secrets::Failure> {
            Err(secrets::Failure)
        }
        fn get(&self, _: &str) -> Result<Option<String>, secrets::Failure> {
            Err(secrets::Failure)
        }
        fn delete(&self, _: &str) -> Result<(), secrets::Failure> {
            Err(secrets::Failure)
        }
    }
}
