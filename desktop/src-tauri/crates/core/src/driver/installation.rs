// SPDX-License-Identifier: AGPL-3.0-only

//! The update the supervision holds and its installation once the backend is gone (specs/2026-11-13-desk-updates.md
//! UB2.3–UB2.7): only a backend that stopped by itself and cleanly, with nothing of the installation running, lets an
//! installation begin; anything else leaves the program as it is, and the backend starts again on the same data. A package
//! of Linux installs on a thread of its own (specs/2026-11-17-desk-update-linux.md UL1.1): its steps and its end come back
//! to the supervision as messages, and only the supervision touches the window and the state.

use std::io;
use std::path::Path;
use std::sync::mpsc::Sender;
use std::thread;

use crate::backend::{Backend, Heard};
use crate::control::ToBackend;
use crate::journal::Journal;
use crate::supervisor::Unfinished as UnfinishedNote;
use crate::update::fetch::{Downloaded, Network};
use crate::update::package::{Outcome, Progress};
use crate::update::state::{Cause, Updates};
use crate::update::worker::Worker;
use crate::update::{self, kept, package, stopped, Kind, Setup};

use super::Window;

/// The update the shell holds and the thread of its network.
pub(super) struct Updating {
    pub(super) setup: Setup,
    pub(super) state: Updates,
    pub(super) worker: Worker,
}

impl Updating {
    pub(super) fn start(setup: Setup, version: &str, heard: Sender<Heard>) -> Updating {
        let state = Updates::new(setup.kind, version);
        let worker = Worker::start(Network::new(setup.policy.clone()), setup.kind, move |done| {
            let _ = heard.send(Heard::Update(done));
        });
        Updating { setup, state, worker }
    }
}

/// Tells the backend what the update came to (UB1.1).
pub(super) fn tell(backend: &mut Option<Backend>, updating: &Option<Updating>) {
    if let (Some(running), Some(updating)) = (backend.as_mut(), updating.as_ref()) {
        let _ = running.send(&ToBackend::UpdateState(&updating.state.line()));
    }
}

/// Installs the update the shell holds once the backend is gone (UB2.3–UB2.7) and says whether the app starts again:
/// only a backend that stopped by itself and cleanly, with the cluster of the data directory stopped and no program of the
/// installation running, lets an installation begin. Anything else is `install_failed` — or `install_refused` — with
/// nothing changed, and the backend starts again; a package of Linux is kept for the author. None — a package installs on
/// its thread, which tells `heard` its steps and its end ([`ended`]).
pub(super) fn install<W: Window>(
    updating: &mut Updating,
    data_dir: &str,
    stopped: bool,
    exit_code: Option<Option<i32>>,
    window: &W,
    journal: &Journal,
    heard: &Sender<Heard>,
) -> Option<bool> {
    let kind = updating.setup.kind;
    let Some(file) = updating.state.file().cloned() else {
        updating.state.install_failed(Cause::NotLaunched, None);
        return Some(false);
    };
    let failed = |updating: &mut Updating, cause: Cause| {
        let kept = kept::keep(&file, kind, &updating.setup.programs).map(|path| path.to_string_lossy().into_owned());
        updating.state.install_failed(cause, kept);
        Some(false)
    };
    if !stopped || exit_code != Some(Some(0)) {
        journal.note("update_not_installed cause=backend");
        return failed(updating, Cause::NotStopped);
    }
    let held = stopped::holders(Path::new(data_dir), &updating.setup.holders, &updating.setup.bridges);
    if !held.is_empty() {
        journal.note(&format!("update_not_installed cause=held count={}", held.len()));
        return failed(updating, Cause::NotStopped);
    }
    journal.note(&format!("update_installing kind={}", kind.name()));
    match kind {
        Kind::Deb | Kind::Rpm => {
            // UL1.1: the thread of the installation holds nothing of the supervision; what it says comes back as messages.
            let (keys, programs, heard) =
                (updating.setup.policy.keys.clone(), updating.setup.programs.clone(), heard.clone());
            thread::spawn(move || {
                let told = heard.clone();
                let steps = move |step| {
                    let _ = told.send(Heard::Package(Progress::Step(step)));
                };
                // UL1.2: the output goes on to the stderr of the shell — the journal of the user, as when it was inherited.
                let outcome = package::install(&file, kind, &keys, &programs, steps, io::stderr);
                let _ = heard.send(Heard::Package(Progress::Ended(outcome)));
            });
            None
        }
        Kind::Nsis | Kind::AppImage => {
            // MC1.8: the installer of Windows ends the bridges of the installation first; the launch of the new version says
            // how the programs of the author read again.
            if kind == Kind::Nsis
                && stopped::runs(&updating.setup.bridges)
                && update::write_bridge_note(Path::new(data_dir), &file.release.version.to_string()).is_err()
            {
                journal.note("update_bridge_note_not_written");
            }
            Some(install_file(updating, window, journal, &file, kind))
        }
        Kind::None => failed(updating, Cause::NotLaunched),
    }
}

/// The installation of a package ended (UB2.6, UL1.1): the supervision keeps what it came to and says whether the app starts
/// again.
pub(super) fn ended(updating: &mut Updating, data_dir: &str, outcome: Outcome, journal: &Journal) -> bool {
    match outcome {
        Outcome::Installed => true,
        // UL2.2: `busy` after the password is a refusal as the closed window is: the backend starts again, no note.
        Outcome::Refused(refusal, kept) => {
            journal.note(&format!("update_install_refused reason={}", refusal.name()));
            updating.state.install_refused(refusal, kept.map(|path| path.to_string_lossy().into_owned()));
            false
        }
        Outcome::NotRun(kept) => {
            journal.note("update_not_installed cause=package");
            updating.state.install_failed(Cause::NotLaunched, kept.map(|path| path.to_string_lossy().into_owned()));
            false
        }
        Outcome::Unfinished(kept) => {
            // UB2.6: the system may be half done: the app starts again whole, and the start of the version on the disk
            // decides; the note says what happened.
            journal.note("update_install_unfinished");
            let version = updating.state.file().map(|file| file.release.version.to_string()).unwrap_or_default();
            let note = UnfinishedNote { version, package: kept.map(|path| path.to_string_lossy().into_owned()) };
            if update::write_note(Path::new(data_dir), &note).is_err() {
                journal.note("update_note_not_written");
            }
            true
        }
    }
}

/// The installer of Windows or the AppImage replaces the program (UB2.4, UB2.5).
fn install_file<W: Window>(
    updating: &mut Updating,
    window: &W,
    journal: &Journal,
    file: &Downloaded,
    kind: Kind,
) -> bool {
    match window.install(file, kind) {
        Ok(()) => true,
        Err(cause) => {
            journal.note("update_not_installed cause=installer");
            updating.state.install_failed(cause, None);
            false
        }
    }
}
