// SPDX-License-Identifier: AGPL-3.0-only

//! The updates of the app (specs/2026-11-13-desk-updates.md UB1–UB3): the backend keeps the schedule, the setting and the
//! state, the shell the network and the installation. Here are the parts of the shell that need no window — the
//! constant list of addresses and keys (`policy`), what a release says and what proves it (`manifest`), the network
//! (`fetch`), the state the shell reports (`state`) and its thread (`worker`), the check that everything holding the
//! files stopped (`stopped`), the package of Linux (`package`) — and the signer and the check of a release (`sign`,
//! `release`), which `canonkeeper-release` runs in the jobs of a release.

use std::fs;
use std::path::{Path, PathBuf};

pub mod fetch;
pub mod manifest;
pub mod package;
pub mod policy;
pub mod release;
pub mod sign;
pub mod state;
pub mod stopped;
pub mod worker;

use crate::supervisor::Unfinished;

/// What the updates of the app need besides the version of the shell (UB1, UB2).
#[derive(Debug, Clone)]
pub struct Setup {
    pub kind: Kind,
    pub policy: policy::Policy,
    /// The programs of a package of Linux and where a package is kept for the author (UB2.6).
    pub programs: package::Programs,
    /// What runs from the installation: no process of them may run when an installation begins (UB2.3) — the program
    /// of the backend and the directory of the bundle, on Windows the directory of the installation.
    pub holders: Vec<PathBuf>,
    /// The program of the bridge for the programs of the author of the installation, on Windows: no holder — the installer
    /// ends it first — and the mark of the line «обновлён» when it ran (specs/2026-11-15-desk-mcp.md MC1.8).
    pub bridges: Vec<PathBuf>,
}

/// The mark of an installation of Windows before which the bridge for the programs of the author ran: the version it went
/// to, beside `update-note.json` until the launch of that version says it once (specs/2026-11-15-desk-mcp.md MC1.8).
pub const BRIDGE_NOTE: &str = "bridge-note.json";

#[derive(serde::Serialize, serde::Deserialize)]
struct BridgeNote {
    version: String,
}

/// Writes the mark of the bridge with the version the installation goes to.
pub fn write_bridge_note(data_dir: &Path, version: &str) -> std::io::Result<()> {
    let text = serde_json::to_vec(&BridgeNote { version: version.to_string() }).map_err(std::io::Error::other)?;
    let beside = data_dir.join(format!(".{BRIDGE_NOTE}.new"));
    fs::write(&beside, text)?;
    fs::rename(beside, data_dir.join(BRIDGE_NOTE))
}

/// Takes the mark away and says whether this launch runs the version it went to: once, and only that launch.
pub fn take_bridge_note(data_dir: &Path, version: &str) -> bool {
    let path = data_dir.join(BRIDGE_NOTE);
    let Ok(text) = fs::read(&path) else { return false };
    let _ = fs::remove_file(&path);
    serde_json::from_slice::<BridgeNote>(&text).is_ok_and(|note| !version.is_empty() && note.version == version)
}

/// The note of an installation of a package that did not finish, in the data directory until the next start of the
/// shell reads it (UB2.6): the shell has no settings.json; «Стереть все данные» takes the file with the data.
pub const NOTE: &str = "update-note.json";

/// Writes the note before the app starts again.
pub fn write_note(data_dir: &Path, note: &Unfinished) -> std::io::Result<()> {
    let text = serde_json::to_vec(note).map_err(std::io::Error::other)?;
    let path = data_dir.join(NOTE);
    let beside = data_dir.join(format!(".{NOTE}.new"));
    fs::write(&beside, text)?;
    fs::rename(beside, path)
}

/// Takes the note away and returns it: once.
pub fn take_note(data_dir: &Path) -> Option<Unfinished> {
    let path = data_dir.join(NOTE);
    let text = fs::read(&path).ok()?;
    let _ = fs::remove_file(&path);
    let note: Unfinished = serde_json::from_slice(&text).ok()?;
    let plain = crate::words::plain_version(Some(&note.version)).is_some()
        && note.package.as_ref().is_none_or(|package| package.len() <= 4096);
    plain.then_some(note)
}

/// Removes what an update of an earlier version left in the temporary directory (UB2.8): on Windows the updater plugin
/// keeps its installer in `CanonKeeper-<version>-updater-*` and the app ends before it could go.
pub fn remove_leftovers(temp: &Path) -> usize {
    let Ok(entries) = fs::read_dir(temp) else { return 0 };
    let mut removed = 0;
    for entry in entries.flatten() {
        let name = entry.file_name();
        let Some(name) = name.to_str() else { continue };
        let ours = name.strip_prefix("CanonKeeper-").is_some_and(|rest| rest.contains("-updater-"));
        let directory = entry.file_type().is_ok_and(|kind| kind.is_dir());
        if ours && directory && fs::remove_dir_all(entry.path()).is_ok() {
            removed += 1;
        }
    }
    removed
}

/// How this copy of the app was installed (UB1.7): the mark the bundler writes into the program, and for an AppImage the
/// file it runs from. A build without the mark — from the sources, or an AppImage unpacked — updates by hand.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Kind {
    Nsis,
    Deb,
    Rpm,
    AppImage,
    None,
}

impl Kind {
    /// The kind of a bundle type (`nsis`, `deb`, `rpm`, `appimage`, as the bundler names it); an AppImage only when it
    /// runs from its file (`$APPIMAGE`).
    pub fn of(bundle: Option<&str>, appimage: bool) -> Kind {
        match bundle.map(str::to_ascii_lowercase).as_deref() {
            Some("nsis") => Kind::Nsis,
            Some("deb") => Kind::Deb,
            Some("rpm") => Kind::Rpm,
            Some("appimage") if appimage => Kind::AppImage,
            _ => Kind::None,
        }
    }

    /// The name in the channel and the contract.
    pub fn name(self) -> &'static str {
        match self {
            Kind::Nsis => "nsis",
            Kind::Deb => "deb",
            Kind::Rpm => "rpm",
            Kind::AppImage => "appimage",
            Kind::None => "none",
        }
    }

    /// The key of its file in `latest.json` (UB3.2): the system, the processor and the kind, never a key without the kind.
    pub fn platform(self) -> Option<&'static str> {
        match self {
            Kind::Nsis => Some("windows-x86_64-nsis"),
            Kind::Deb => Some("linux-x86_64-deb"),
            Kind::Rpm => Some("linux-x86_64-rpm"),
            Kind::AppImage => Some("linux-x86_64-appimage"),
            Kind::None => None,
        }
    }

    /// Every kind a release carries a file of.
    pub const RELEASED: [Kind; 4] = [Kind::Nsis, Kind::Deb, Kind::Rpm, Kind::AppImage];
}

#[cfg(test)]
mod tests {
    use super::*;

    // specs/2026-11-15-desk-mcp.md MC1.8: the mark of the bridge is said once, and only by the launch of the version it
    // went to; another version takes it away silently.
    #[test]
    fn the_mark_of_the_bridge_is_said_once_by_its_version() {
        let root = std::env::temp_dir().join(format!("canonkeeper-bridge-note-{}", std::process::id()));
        let _ = fs::remove_dir_all(&root);
        fs::create_dir_all(&root).unwrap();
        assert!(!take_bridge_note(&root, "1.4.0"));
        write_bridge_note(&root, "1.4.0").unwrap();
        assert!(take_bridge_note(&root, "1.4.0"));
        assert!(!take_bridge_note(&root, "1.4.0"));
        write_bridge_note(&root, "1.4.0").unwrap();
        assert!(!take_bridge_note(&root, "1.3.9"));
        assert!(!root.join(BRIDGE_NOTE).exists());
        fs::remove_dir_all(&root).unwrap();
    }

    #[test]
    fn the_note_of_an_unfinished_installation_is_read_once() {
        let root = std::env::temp_dir().join(format!("canonkeeper-update-note-{}", std::process::id()));
        let _ = fs::remove_dir_all(&root);
        fs::create_dir_all(&root).unwrap();
        assert_eq!(take_note(&root), None);
        let note = Unfinished {
            version: "1.3.0".into(),
            package: Some("/home/тест/Загрузки/CanonKeeper_1.3.0_amd64.deb".into()),
        };
        write_note(&root, &note).unwrap();
        assert_eq!(take_note(&root), Some(note));
        assert_eq!(take_note(&root), None);
        fs::write(root.join(NOTE), r#"{"version":"<script>","package":"x"}"#).unwrap();
        assert_eq!(take_note(&root), None);
        fs::remove_dir_all(root).unwrap();
    }

    #[test]
    fn the_leftovers_of_an_earlier_update_go_and_nothing_else() {
        let root = std::env::temp_dir().join(format!("canonkeeper-update-leftovers-{}", std::process::id()));
        let _ = fs::remove_dir_all(&root);
        for name in
            ["CanonKeeper-1.2.0-updater-a1b2", "CanonKeeper-1.3.0-updater-x", "Other-1.0-updater-y", "CanonKeeper-data"]
        {
            fs::create_dir_all(root.join(name)).unwrap();
            fs::write(root.join(name).join("setup.exe"), "x").unwrap();
        }
        fs::write(root.join("CanonKeeper-1.1.0-updater-file"), "a file").unwrap();
        assert_eq!(remove_leftovers(&root), 2);
        let mut left: Vec<_> = fs::read_dir(&root).unwrap().flatten().map(|entry| entry.file_name()).collect();
        left.sort();
        assert_eq!(left, ["CanonKeeper-1.1.0-updater-file", "CanonKeeper-data", "Other-1.0-updater-y"]);
        fs::remove_dir_all(root).unwrap();
    }

    #[test]
    fn the_kind_is_the_mark_of_the_bundler_and_an_appimage_only_from_its_file() {
        assert_eq!(Kind::of(Some("nsis"), false), Kind::Nsis);
        assert_eq!(Kind::of(Some("deb"), true), Kind::Deb);
        assert_eq!(Kind::of(Some("rpm"), false), Kind::Rpm);
        assert_eq!(Kind::of(Some("AppImage"), true), Kind::AppImage);
        assert_eq!(Kind::of(Some("appimage"), false), Kind::None);
        assert_eq!(Kind::of(Some("msi"), false), Kind::None);
        assert_eq!(Kind::of(None, true), Kind::None);
        assert_eq!(Kind::None.platform(), None);
        let platforms: Vec<_> = Kind::RELEASED.iter().filter_map(|kind| kind.platform()).collect();
        assert_eq!(platforms, ["windows-x86_64-nsis", "linux-x86_64-deb", "linux-x86_64-rpm", "linux-x86_64-appimage"]);
    }
}
