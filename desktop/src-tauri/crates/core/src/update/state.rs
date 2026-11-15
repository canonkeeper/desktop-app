// SPDX-License-Identifier: AGPL-3.0-only

//! The state of an update as the shell holds it (specs/2026-11-13-desk-updates.md UB1.1, UB1.7, UB2): what the last check
//! found, the release it proved and the file it downloaded and checked; the line `update-state` tells the backend, after
//! every change and after every `ready`, so a backend started again loses nothing. The shell installs only the release it
//! holds, of the version it reported itself, and only from the phase where the file is downloaded and checked.

use serde::Serialize;

use super::fetch::{Checked, Downloaded, NotDownloaded};
use super::manifest::{is_update, Release};
use super::Kind;

/// What the last check found.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum Found {
    /// No check yet in this run of the shell.
    Never,
    /// The running version is the latest.
    Latest,
    /// A later version: the release is held.
    Available,
    /// No address answered.
    Offline,
}

/// Why the system did not install a package (UB2.6).
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum Refusal {
    /// The window of the password was closed (`pkexec` 126).
    Declined,
    /// The system cannot ask for the password: no `pkexec`, no agent of polkit (127).
    Unavailable,
}

/// Why an installation did not happen after the backend stopped (UB2.7).
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum Cause {
    /// The backend did not stop by itself and cleanly, or a program of the installation still runs.
    NotStopped,
    /// The installer did not start (Windows), the package was not run (Linux).
    NotLaunched,
    /// The file of the AppImage was not replaced.
    NotReplaced,
}

/// The phase of an update in the shell.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Phase {
    Idle(Found),
    Checking,
    Downloading {
        percent: u8,
    },
    /// The file is downloaded and checked: the backend waits for the turns, then asks to install.
    Downloaded,
    Installing,
    DownloadFailed,
    VerifyFailed,
    InstallRefused {
        refusal: Refusal,
        package: String,
    },
    InstallFailed {
        cause: Cause,
        package: Option<String>,
    },
}

/// The line `update-state`: the phase and what goes with it.
#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
pub struct Line {
    pub phase: &'static str,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub found: Option<Found>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub version: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub notes: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub percent: Option<u8>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub refusal: Option<Refusal>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub package: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub cause: Option<Cause>,
}

/// The update the shell holds.
#[derive(Debug)]
pub struct Updates {
    kind: Kind,
    running: String,
    phase: Phase,
    release: Option<Release>,
    file: Option<Downloaded>,
}

impl Updates {
    pub fn new(kind: Kind, running: &str) -> Updates {
        Updates { kind, running: running.to_string(), phase: Phase::Idle(Found::Never), release: None, file: None }
    }

    fn version(&self) -> Option<String> {
        self.release.as_ref().map(|release| release.version.to_string())
    }

    fn holds(&self, version: &str) -> bool {
        self.version().as_deref() == Some(version.trim_start_matches('v'))
    }

    fn busy(&self) -> bool {
        matches!(self.phase, Phase::Checking | Phase::Downloading { .. } | Phase::Downloaded | Phase::Installing)
    }

    /// `update-check`: a check starts unless one goes on, a download or an installation; a build that updates by hand
    /// checks nothing.
    pub fn check(&mut self) -> bool {
        if self.kind == Kind::None || self.busy() {
            return false;
        }
        self.phase = Phase::Checking;
        true
    }

    /// What the check found; heard only while a check goes on.
    pub fn checked(&mut self, checked: Checked) -> bool {
        if self.phase != Phase::Checking {
            return false;
        }
        let (found, release) = match checked {
            Checked::Found(release) if is_update(&release, &self.running) => (Found::Available, Some(*release)),
            Checked::Found(_) => (Found::Latest, None),
            Checked::NoAnswer => (Found::Offline, None),
        };
        self.release = release;
        self.phase = Phase::Idle(found);
        true
    }

    /// `update-download`: the release of `version` the shell holds, downloaded anew — from an available version or after
    /// a download, a check or an installation that failed.
    pub fn download(&mut self, version: &str) -> Option<Release> {
        let may = matches!(
            self.phase,
            Phase::Idle(Found::Available)
                | Phase::DownloadFailed
                | Phase::VerifyFailed
                | Phase::InstallFailed { .. }
                | Phase::InstallRefused { .. }
        );
        if !may || !self.holds(version) {
            return None;
        }
        self.file = None;
        self.phase = Phase::Downloading { percent: 0 };
        self.release.clone()
    }

    /// A new percent of the download; true when it changed.
    pub fn progress(&mut self, version: &str, percent: u8) -> bool {
        match &mut self.phase {
            Phase::Downloading { percent: now }
                if self.release.as_ref().is_some_and(|r| r.version.to_string() == version) =>
            {
                let changed = *now != percent;
                *now = percent;
                changed
            }
            _ => false,
        }
    }

    /// The download ended; heard only while it goes on for the release held.
    pub fn downloaded(&mut self, result: Result<Downloaded, (String, NotDownloaded)>) -> bool {
        if !matches!(self.phase, Phase::Downloading { .. }) {
            return false;
        }
        match result {
            Ok(file) if self.holds(&file.release.version.to_string()) => {
                self.file = Some(file);
                self.phase = Phase::Downloaded;
            }
            Ok(_) => return false,
            Err((version, _)) if !self.holds(&version) => return false,
            Err((_, NotDownloaded::Failed)) => self.phase = Phase::DownloadFailed,
            Err((_, NotDownloaded::Refused)) => self.phase = Phase::VerifyFailed,
            Err((_, NotDownloaded::Canceled)) => self.phase = Phase::Idle(Found::Available),
        }
        true
    }

    /// `update-cancel`: the download or the wait for the turns of `version` stop; the version stays available.
    pub fn cancel(&mut self, version: &str) -> bool {
        if !matches!(self.phase, Phase::Downloading { .. } | Phase::Downloaded) || !self.holds(version) {
            return false;
        }
        self.file = None;
        self.phase = Phase::Idle(Found::Available);
        true
    }

    /// Whether `update-install` of `version` may go: the file of that version is downloaded and checked.
    pub fn may_install(&self, version: &str) -> bool {
        self.phase == Phase::Downloaded && self.file.is_some() && self.holds(version)
    }

    /// The installation begins with the file held.
    pub fn install(&mut self) -> Option<Downloaded> {
        if self.phase != Phase::Downloaded {
            return None;
        }
        self.phase = Phase::Installing;
        self.file.clone()
    }

    /// The file the installation took, while it goes on.
    pub fn file(&self) -> Option<&Downloaded> {
        self.file.as_ref()
    }

    /// The installation did not happen: nothing changed (UB2.7).
    pub fn install_failed(&mut self, cause: Cause, package: Option<String>) {
        self.file = None;
        self.phase = Phase::InstallFailed { cause, package };
    }

    /// The system did not install the package: nothing changed, the package is kept for the author (UB2.6).
    pub fn install_refused(&mut self, refusal: Refusal, package: String) {
        self.file = None;
        self.phase = Phase::InstallRefused { refusal, package };
    }

    /// «Стереть все данные» comes before an update (UB2.9): what goes on stops, nothing is installed.
    pub fn forget(&mut self) {
        self.file = None;
        if self.busy() {
            self.phase = if self.release.is_some() { Phase::Idle(Found::Available) } else { Phase::Idle(Found::Never) };
        }
    }

    /// The line `update-state` of the phase.
    pub fn line(&self) -> Line {
        let mut line = Line {
            phase: "idle",
            found: None,
            version: self.version(),
            notes: self.release.as_ref().map(|release| release.notes.clone()),
            percent: None,
            refusal: None,
            package: None,
            cause: None,
        };
        match &self.phase {
            Phase::Idle(found) => line.found = Some(*found),
            Phase::Checking => line.phase = "checking",
            Phase::Downloading { percent } => (line.phase, line.percent) = ("downloading", Some(*percent)),
            Phase::Downloaded => line.phase = "downloaded",
            Phase::Installing => line.phase = "installing",
            Phase::DownloadFailed => line.phase = "download_failed",
            Phase::VerifyFailed => line.phase = "verify_failed",
            Phase::InstallRefused { refusal, package } => {
                (line.phase, line.refusal, line.package) = ("install_refused", Some(*refusal), Some(package.clone()));
            }
            Phase::InstallFailed { cause, package } => {
                (line.phase, line.cause, line.package) = ("install_failed", Some(*cause), package.clone());
            }
        }
        line
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use semver::Version;
    use std::sync::Arc;

    fn release(version: &str) -> Release {
        Release {
            version: Version::parse(version).unwrap(),
            notes: "Что нового".into(),
            url: url::Url::parse("https://github.com/canonkeeper/desktop-app/releases/download/v1/a.deb").unwrap(),
            signature: "c2ln".into(),
            raw: serde_json::json!({}),
        }
    }

    fn file(version: &str) -> Downloaded {
        Downloaded {
            release: release(version),
            bytes: Arc::new(vec![1, 2]),
            signer: super::super::manifest::Signer::Main,
        }
    }

    #[test]
    fn a_check_finds_a_later_version_and_the_download_and_install_take_only_it() {
        let mut updates = Updates::new(Kind::Deb, "1.2.0");
        assert_eq!(updates.line().found, Some(Found::Never));
        assert!(updates.check() && !updates.check());
        assert!(updates.checked(Checked::Found(Box::new(release("1.3.0")))));
        let line = updates.line();
        assert_eq!((line.phase, line.found, line.version.as_deref()), ("idle", Some(Found::Available), Some("1.3.0")));
        assert_eq!(serde_json::to_value(&line).unwrap()["found"], "available");
        // Only the version the shell reported.
        assert_eq!(updates.download("9.9.9"), None);
        assert!(!updates.may_install("1.3.0"));
        assert!(updates.download("1.3.0").is_some());
        assert!(updates.progress("1.3.0", 40) && !updates.progress("1.3.0", 40));
        assert_eq!(updates.line().percent, Some(40));
        assert!(updates.downloaded(Ok(file("1.3.0"))));
        assert!(!updates.may_install("1.2.9") && updates.may_install("1.3.0") && updates.may_install("v1.3.0"));
        assert!(updates.install().is_some());
        assert_eq!(updates.line().phase, "installing");
        assert!(updates.download("1.3.0").is_none() && !updates.check());
        updates.install_refused(Refusal::Declined, "/home/тест/Загрузки/CanonKeeper_1.3.0_amd64.deb".into());
        let line = serde_json::to_value(updates.line()).unwrap();
        assert_eq!(line["phase"], "install_refused");
        assert_eq!(line["refusal"], "declined");
        // «Обновить и перезапустить» again downloads anew.
        assert!(updates.download("1.3.0").is_some());
    }

    #[test]
    fn a_version_not_later_is_no_update_and_silence_is_offline() {
        let mut updates = Updates::new(Kind::AppImage, "1.3.0");
        updates.check();
        updates.checked(Checked::Found(Box::new(release("1.3.0"))));
        assert_eq!((updates.line().found, updates.line().version), (Some(Found::Latest), None));
        updates.check();
        updates.checked(Checked::Found(Box::new(release("1.2.0"))));
        assert_eq!(updates.line().found, Some(Found::Latest));
        updates.check();
        updates.checked(Checked::NoAnswer);
        assert_eq!(updates.line().found, Some(Found::Offline));
        // A build that updates by hand checks nothing.
        assert!(!Updates::new(Kind::None, "1.3.0").check());
    }

    #[test]
    fn a_cancel_or_an_erasure_stops_the_download_and_late_answers_are_not_heard() {
        let mut updates = Updates::new(Kind::Nsis, "1.2.0");
        updates.check();
        updates.checked(Checked::Found(Box::new(release("1.3.0"))));
        updates.download("1.3.0");
        assert!(updates.cancel("1.3.0"));
        assert_eq!(updates.line().found, Some(Found::Available));
        // The file that came after the cancel is not held.
        assert!(!updates.downloaded(Ok(file("1.3.0"))));
        assert!(!updates.may_install("1.3.0"));
        updates.download("1.3.0");
        assert!(updates.downloaded(Err(("1.3.0".into(), NotDownloaded::Refused))));
        assert_eq!(updates.line().phase, "verify_failed");
        updates.download("1.3.0");
        updates.downloaded(Ok(file("1.3.0")));
        updates.forget();
        assert!(!updates.may_install("1.3.0") && updates.file().is_none());
        assert_eq!(updates.line().found, Some(Found::Available));
    }
}
