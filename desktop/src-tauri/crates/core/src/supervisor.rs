// SPDX-License-Identifier: AGPL-3.0-only

//! The supervision of the backend as a state machine (specs/2026-11-12-desk-shell.md SH1.4, SH2.2, SH2.3): it hears
//! what happened — a line of the backend, its exit, a refused code, a request to close, the passing of time — and says
//! what to do. It does no input or output itself, so every path is tested without a process or a window. A stop of the
//! backend is one phase with its purpose (specs/2026-11-13-desk-updates.md UB2.9): the app ends, an update is installed,
//! or all the data are erased — the erasure comes before an installation, and closing the window does not cut an
//! installation short.

use std::time::{Duration, Instant};

use serde::Serialize;

#[cfg(test)]
use crate::control::Said;
use crate::control::{Code, FromBackend, Step};
use crate::link::{Dropped, Stage, Target};
use crate::navigation::{open_url, open_url_to};
use crate::words::{candidate, Fault};

/// How many falls in a row the shell answers with a restart, and within what time (SH2.2).
pub const RESTARTS: usize = 3;
pub const RESTART_WINDOW: Duration = Duration::from_secs(5 * 60);
/// How long the shell waits for the backend to stop: its own timeouts — turns 5 s, core 30 s, PostgreSQL 90 s — and a
/// little more (SH2.3).
pub const STOP_TIMEOUT: Duration = Duration::from_secs(130);
/// How many refused codes in one run of the backend the shell answers with a new code.
pub const OPEN_ATTEMPTS: u8 = 3;
/// How long the window may take to reach the page of the start before an installation: the page of the backend saves
/// what is not saved as it goes (specs/2026-11-13-desk-updates.md UB2.3).
pub const LEAVE_TIMEOUT: Duration = Duration::from_secs(10);

/// What the page learns once after it opens (`launch_notes`, SH1.4, SH2.2).
#[derive(Debug, Default, Clone, PartialEq, Eq, Serialize)]
pub struct Notes {
    /// The remembered port was taken: the address of the window is new, its place of reading and its view are reset.
    #[serde(rename = "portChanged")]
    pub port_changed: bool,
    /// The backend fell and the shell started it again.
    #[serde(rename = "restartedAfterCrash")]
    pub restarted_after_crash: bool,
    /// The version the data were opened with before this one: the app was updated (specs/2026-11-13-desk-upgrade.md
    /// UG2.5).
    #[serde(rename = "updatedFrom", skip_serializing_if = "Option::is_none")]
    pub updated_from: Option<String>,
    /// The notes of the release now running, when the app installed it (specs/2026-11-13-desk-updates.md UB2.8).
    #[serde(rename = "releaseNotes", skip_serializing_if = "Option::is_none")]
    pub release_notes: Option<String>,
    /// A package of Linux the system did not finish installing: its version and where it is kept (UB2.6).
    #[serde(rename = "updateUnfinished", skip_serializing_if = "Option::is_none")]
    pub update_unfinished: Option<Unfinished>,
    /// The installer of Windows ended the bridges of the programs of the author for the update to this version
    /// (specs/2026-11-15-desk-mcp.md MC1.8).
    #[serde(rename = "bridgesStopped", skip_serializing_if = "std::ops::Not::not")]
    pub bridges_stopped: bool,
}

/// An installation of a package that did not finish: the version, and the package kept for the author when it could be.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, serde::Deserialize)]
pub struct Unfinished {
    pub version: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub package: Option<String>,
}

impl Notes {
    /// Both notes, the ones not yet taken and the new ones.
    pub fn merge(self, other: Notes) -> Notes {
        Notes {
            port_changed: self.port_changed || other.port_changed,
            restarted_after_crash: self.restarted_after_crash || other.restarted_after_crash,
            updated_from: other.updated_from.or(self.updated_from),
            release_notes: other.release_notes.or(self.release_notes),
            update_unfinished: other.update_unfinished.or(self.update_unfinished),
            bridges_stopped: self.bridges_stopped || other.bridges_stopped,
        }
    }
}

/// What happened.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Event {
    /// The backend wrote a message.
    Message(FromBackend),
    /// The program of the backend could not start.
    SpawnFailed,
    /// The backend process ended; every line it wrote came before.
    Exited,
    /// The page of the code exchange reports the status the session refused the code with.
    Refused(u16),
    /// The window or the system asks the app to close.
    Close,
    /// The author asked, on the page of the start, to bring the data back from the copy the backend named
    /// (specs/2026-11-13-desk-upgrade.md UG4.1).
    Restore,
    /// The backend asked to install the update the shell downloaded and checked (specs/2026-11-13-desk-updates.md UB2.3).
    Install,
    /// The window reached the page of the start: the page of the backend is gone, what it held is saved.
    Left,
    /// The installation ended without leaving the app: `restart` — the program is replaced and the app starts again;
    /// otherwise nothing changed and the backend starts again on the same data (UB2.7).
    Installed { restart: bool },
    /// Time passes.
    Tick,
}

/// What to do.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Action {
    /// Start the backend and send it `start`.
    Spawn,
    /// Start the backend with a `start` that brings the data back from the copy it names (UG4.1).
    SpawnRestoring,
    /// Show the page of the shell: «CanonKeeper запускается…».
    ShowStarting,
    /// The page of the shell says the long step of the start that goes on (UG3.1).
    ShowStep(Step),
    /// Let the page of 127.0.0.1:port call the commands of the shell and open the window at `url`; `notes` are what
    /// the page learns once.
    Open { port: u16, url: OpenUrl, notes: Notes },
    /// Ask the backend for a new code (`open-code`).
    AskCode,
    /// Show the failure window with the words of the fault.
    ShowFailure(Fault),
    /// Write `shutdown`.
    SendShutdown,
    /// The author asked to erase all the data of the app (specs/2026-11-13-desk-erase.md ER1.3): mark the erasure, before
    /// the backend stops and before anything is removed.
    BeginErase,
    /// The backend is gone: close the window and remove what the app wrote, then take the mark away.
    Erase,
    /// Kill the backend: it did not stop in time.
    Kill,
    /// The window goes to the page of the start with the words of the installation (UB2.3).
    Leave,
    /// The backend is gone: check that it stopped by itself and that nothing of the installation runs, then install.
    /// `stopped` — the backend was not killed.
    Install { stopped: bool },
    /// The program is replaced: the app starts again (UB2.5).
    Restart,
    /// End the app.
    Exit,
}

/// Why the backend stops (UB2.9).
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Purpose {
    /// The author closed the app.
    Exit,
    /// An update is installed once the backend is gone.
    Install,
    /// All the data of the app are erased once the backend is gone (specs/2026-11-13-desk-erase.md ER1.3).
    Erase,
}

/// The address that opens the window: it carries the code, so it never shows in a debug print.
#[derive(Clone, PartialEq, Eq)]
pub struct OpenUrl(String);

impl OpenUrl {
    pub fn as_str(&self) -> &str {
        &self.0
    }

    /// The same opening, whose exchange goes on to the place of a link that waited for the window
    /// (specs/2026-11-16-desk-app-links.md AL1.4).
    pub fn to(&self, target: &Target) -> OpenUrl {
        OpenUrl(open_url_to(&self.0, target))
    }
}

impl std::fmt::Debug for OpenUrl {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        formatter.write_str("OpenUrl(…)")
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum Phase {
    /// The backend is starting; nothing to show but the starting page.
    Starting,
    /// The backend is ready on its port.
    Open { port: u16 },
    /// The failure window is shown; the backend may still be alive.
    Failed,
    /// The window goes to the page of the start before the backend stops for an installation.
    Leaving { since: Instant },
    /// `shutdown` is sent; the shell waits for the backend to exit, then does what the stop is for.
    Stopping { since: Instant, purpose: Purpose },
    /// The backend is gone and the shell installs an update.
    Installing,
    /// The app is ending.
    Stopped,
}

/// The state of the supervision.
#[derive(Debug)]
pub struct Supervisor {
    phase: Phase,
    alive: bool,
    falls: Vec<Instant>,
    fatal: Option<Fault>,
    /// The failure the window shows: a return starts only from it (UG4.1).
    shown: Option<Fault>,
    refusals: u8,
    restarted: bool,
    /// The backend was killed in this stop: an installation does not begin after it.
    killed: bool,
    stop_timeout: Duration,
}

impl Supervisor {
    pub fn new(stop_timeout: Duration) -> Supervisor {
        Supervisor {
            phase: Phase::Starting,
            alive: false,
            falls: Vec::new(),
            fatal: None,
            shown: None,
            refusals: 0,
            restarted: false,
            killed: false,
            stop_timeout,
        }
    }

    /// What the stop was for, once the backend is gone: the end of the app — the erasure first when the stop was one — or
    /// the installation.
    fn stopped(&mut self, purpose: Purpose) -> Vec<Action> {
        match purpose {
            Purpose::Exit => {
                self.phase = Phase::Stopped;
                vec![Action::Exit]
            }
            Purpose::Erase => {
                self.phase = Phase::Stopped;
                vec![Action::Erase, Action::Exit]
            }
            Purpose::Install => {
                self.phase = Phase::Installing;
                vec![Action::Install { stopped: !self.killed }]
            }
        }
    }

    /// Stops the backend for `purpose`.
    fn stop(&mut self, purpose: Purpose, now: Instant) -> Vec<Action> {
        self.killed = false;
        self.phase = Phase::Stopping { since: now, purpose };
        vec![Action::SendShutdown]
    }

    /// Whether an installation goes on: from the window leaving for the page of the start to the end of the installation.
    pub fn installing(&self) -> bool {
        matches!(
            self.phase,
            Phase::Leaving { .. } | Phase::Stopping { purpose: Purpose::Install, .. } | Phase::Installing
        )
    }

    /// Shows the failure: the window says it, and the supervision remembers what it shows.
    fn fail(&mut self, fault: Fault) -> Vec<Action> {
        self.phase = Phase::Failed;
        self.shown = Some(fault.clone());
        vec![Action::ShowFailure(fault)]
    }

    /// The first actions: start the backend.
    pub fn start(&mut self) -> Vec<Action> {
        self.alive = true;
        vec![Action::Spawn]
    }

    /// What a link that comes now meets (specs/2026-11-16-desk-app-links.md AL1.3): it waits while the backend starts —
    /// its long steps and a restart after a fall among them — and goes to the page of the backend once it is open; the
    /// failure window, a departure for an installation and every stop drop it.
    pub fn stage(&self) -> Stage {
        match self.phase {
            Phase::Starting => Stage::Starting,
            Phase::Open { .. } => Stage::Open,
            Phase::Failed => Stage::Gone(Dropped::Failure),
            Phase::Leaving { .. } => Stage::Gone(Dropped::Leaving),
            Phase::Stopping { purpose: Purpose::Exit, .. } | Phase::Stopped => Stage::Gone(Dropped::Closing),
            Phase::Stopping { purpose: Purpose::Install, .. } | Phase::Installing => Stage::Gone(Dropped::Installing),
            Phase::Stopping { purpose: Purpose::Erase, .. } => Stage::Gone(Dropped::Erasing),
        }
    }

    /// The port of the page the window shows, while the backend is ready.
    pub fn port(&self) -> Option<u16> {
        match self.phase {
            Phase::Open { port } => Some(port),
            _ => None,
        }
    }

    pub fn handle(&mut self, event: Event, now: Instant) -> Vec<Action> {
        match (event, self.phase) {
            // UB2.9: closing the window does not cut an installation short, and a stop goes on as it began.
            (Event::Close, Phase::Leaving { .. } | Phase::Stopping { .. } | Phase::Installing | Phase::Stopped) => {
                vec![]
            }
            (Event::Close, _) => {
                if self.alive {
                    self.stop(Purpose::Exit, now)
                } else {
                    self.phase = Phase::Stopped;
                    vec![Action::Exit]
                }
            }
            // UB2.9: the erasure comes before an installation that has not begun — while the window leaves, while the
            // backend stops for it.
            (Event::Message(FromBackend::Erase), Phase::Leaving { .. }) => {
                let mut actions = vec![Action::BeginErase];
                actions.extend(self.stop(Purpose::Erase, now));
                actions
            }
            (Event::Message(FromBackend::Erase), Phase::Stopping { since, purpose: Purpose::Install }) => {
                self.phase = Phase::Stopping { since, purpose: Purpose::Erase };
                vec![Action::BeginErase]
            }
            (Event::Exited, Phase::Stopping { purpose, .. }) => {
                self.alive = false;
                self.stopped(purpose)
            }
            (Event::Tick, Phase::Stopping { since, purpose })
                if !self.killed && now.duration_since(since) >= self.stop_timeout =>
            {
                self.killed = true;
                let mut actions = vec![Action::Kill];
                if purpose != Purpose::Install {
                    // The installation waits for the exit of the killed backend; the end of the app does not.
                    actions.extend(self.stopped(purpose));
                }
                actions
            }
            // UB2.3: the page of the backend is gone, or the window took too long: the backend stops for the installation.
            (Event::Left, Phase::Leaving { .. }) => self.stop(Purpose::Install, now),
            // A backend that fell meanwhile did not stop by itself: the installation does not begin, the backend starts again.
            (Event::Exited, Phase::Leaving { .. }) => {
                self.alive = false;
                self.killed = true;
                self.stopped(Purpose::Install)
            }
            (Event::Tick, Phase::Leaving { since }) if now.duration_since(since) >= LEAVE_TIMEOUT => {
                self.stop(Purpose::Install, now)
            }
            // UB2.5, UB2.7: replaced — the app starts again; nothing changed — the backend starts again on the same data,
            // and its first ready hears what became of the installation.
            (Event::Installed { restart: true }, Phase::Installing) => {
                self.phase = Phase::Stopped;
                vec![Action::Restart]
            }
            (Event::Installed { restart: false }, Phase::Installing) => {
                self.phase = Phase::Starting;
                self.alive = true;
                self.fatal = None;
                vec![Action::ShowStarting, Action::Spawn]
            }
            (_, Phase::Leaving { .. } | Phase::Stopping { .. } | Phase::Installing | Phase::Stopped) => vec![],
            // UB2.3: only a backend that is ready asks, and only once.
            (Event::Install, Phase::Open { .. }) => {
                self.phase = Phase::Leaving { since: now };
                vec![Action::Leave]
            }
            (Event::Install | Event::Left | Event::Installed { .. }, _) => vec![],
            // UG4.1: only from the failure of a newer schema with a copy to bring back, once the backend is gone.
            (Event::Restore, Phase::Failed)
                if !self.alive && self.shown.as_ref().is_some_and(|fault| candidate(fault).is_some()) =>
            {
                self.phase = Phase::Starting;
                self.alive = true;
                self.fatal = None;
                self.shown = None;
                vec![Action::ShowStarting, Action::SpawnRestoring]
            }
            (Event::Restore, _) => vec![],
            // The backend writes erase only for the word of the author on the page of its window (ER1.2, ER1.6).
            (Event::Message(FromBackend::Erase), _) => {
                if self.alive {
                    let mut actions = vec![Action::BeginErase];
                    actions.extend(self.stop(Purpose::Erase, now));
                    actions
                } else {
                    let mut actions = vec![Action::BeginErase];
                    actions.extend(self.stopped(Purpose::Erase));
                    actions
                }
            }

            (Event::SpawnFailed, _) => {
                self.alive = false;
                self.fail(Fault::Program)
            }
            (
                Event::Message(FromBackend::Ready { port, code, port_changed, updated_from, release_notes }),
                Phase::Starting,
            ) => {
                self.phase = Phase::Open { port };
                self.refusals = 0;
                let notes = Notes {
                    port_changed,
                    restarted_after_crash: std::mem::take(&mut self.restarted),
                    updated_from,
                    release_notes,
                    update_unfinished: None,
                    bridges_stopped: false,
                };
                vec![open(port, &code, 0, notes)]
            }
            (Event::Message(FromBackend::OpenCode { code }), Phase::Open { port }) => {
                vec![open(port, &code, self.refusals, Notes::default())]
            }
            (Event::Message(FromBackend::Step(step)), Phase::Starting) => vec![Action::ShowStep(step)],
            (Event::Message(FromBackend::Fatal { reason, backup, said }), _) => {
                self.fatal = Some(Fault::Fatal { reason, backup, said });
                vec![]
            }
            (Event::Message(_), _) => vec![],
            (Event::Refused(_), Phase::Open { .. }) => {
                self.refusals += 1;
                if self.refusals <= OPEN_ATTEMPTS {
                    vec![Action::AskCode]
                } else {
                    self.fail(Fault::Unknown)
                }
            }
            (Event::Refused(_), _) => vec![],
            (Event::Exited, Phase::Starting) => {
                // The backend ended before it was ready: its fatal says why, silence means the program vanished.
                self.alive = false;
                let fault = self.fatal.take().unwrap_or(Fault::Program);
                self.fail(fault)
            }
            (Event::Exited, Phase::Open { .. }) => {
                // A fall during work: restart, unless it is the fourth within the window.
                self.alive = false;
                self.falls.retain(|fall| now.duration_since(*fall) < RESTART_WINDOW);
                self.falls.push(now);
                if self.falls.len() <= RESTARTS {
                    self.fatal = None;
                    self.restarted = true;
                    self.phase = Phase::Starting;
                    self.alive = true;
                    vec![Action::ShowStarting, Action::Spawn]
                } else {
                    let fault = self.fatal.take().unwrap_or(Fault::Unknown);
                    self.fail(fault)
                }
            }
            (Event::Exited, Phase::Failed) => {
                self.alive = false;
                vec![]
            }
            (Event::Tick, _) => vec![],
        }
    }
}

fn open(port: u16, code: &Code, attempt: u8, notes: Notes) -> Action {
    Action::Open { port, url: OpenUrl(open_url(port, code, attempt)), notes }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn ready(port: u16, code: &str, port_changed: bool) -> Event {
        Event::Message(FromBackend::Ready {
            port,
            code: Code::new(code),
            port_changed,
            updated_from: None,
            release_notes: None,
        })
    }

    fn fatal(reason: &str, backup: Option<&str>) -> FromBackend {
        FromBackend::Fatal { reason: reason.into(), backup: backup.map(str::to_string), said: Box::default() }
    }

    fn fault(reason: &str, backup: Option<&str>) -> Fault {
        Fault::Fatal { reason: reason.into(), backup: backup.map(str::to_string), said: Box::default() }
    }

    fn opened_notes(actions: &[Action]) -> Notes {
        opened(actions).into_iter().next().map(|(_, _, notes)| notes).expect("the window opened")
    }

    fn opened(actions: &[Action]) -> Vec<(u16, String, Notes)> {
        actions
            .iter()
            .filter_map(|action| match action {
                Action::Open { port, url, notes } => Some((*port, url.as_str().to_string(), notes.clone())),
                _ => None,
            })
            .collect()
    }

    #[test]
    fn ready_opens_the_window_with_the_code_and_a_refused_code_asks_for_the_next() {
        let now = Instant::now();
        let mut supervisor = Supervisor::new(STOP_TIMEOUT);
        assert_eq!(supervisor.start(), vec![Action::Spawn]);
        let actions = supervisor.handle(ready(24117, "first", true), now);
        assert_eq!(
            opened(&actions),
            vec![(
                24117,
                "http://127.0.0.1:24117/desktop/open?n=0#c=first".into(),
                Notes { port_changed: true, restarted_after_crash: false, updated_from: None, ..Notes::default() }
            )]
        );
        assert_eq!(supervisor.port(), Some(24117));
        assert_eq!(supervisor.handle(Event::Refused(401), now), vec![Action::AskCode]);
        let next = supervisor.handle(Event::Message(FromBackend::OpenCode { code: Code::new("second") }), now);
        assert_eq!(
            opened(&next),
            vec![(24117, "http://127.0.0.1:24117/desktop/open?n=1#c=second".into(), Notes::default())]
        );
    }

    #[test]
    fn three_refused_codes_are_answered_and_the_fourth_shows_the_failure() {
        let now = Instant::now();
        let mut supervisor = Supervisor::new(STOP_TIMEOUT);
        supervisor.start();
        supervisor.handle(ready(24117, "c", false), now);
        for _ in 0..OPEN_ATTEMPTS {
            assert_eq!(supervisor.handle(Event::Refused(403), now), vec![Action::AskCode]);
        }
        assert_eq!(supervisor.handle(Event::Refused(403), now), vec![Action::ShowFailure(Fault::Unknown)]);
        // The backend still runs: closing stops it.
        assert_eq!(supervisor.handle(Event::Close, now), vec![Action::SendShutdown]);
    }

    #[test]
    fn a_backend_that_ends_before_ready_shows_its_reason_or_the_vanished_program() {
        let now = Instant::now();
        let mut supervisor = Supervisor::new(STOP_TIMEOUT);
        supervisor.start();
        assert_eq!(supervisor.handle(Event::Message(fatal("migrate", Some("pre-0142-x"))), now), vec![]);
        assert_eq!(
            supervisor.handle(Event::Exited, now),
            vec![Action::ShowFailure(fault("migrate", Some("pre-0142-x")))]
        );
        assert_eq!(supervisor.handle(Event::Close, now), vec![Action::Exit]);

        let mut silent = Supervisor::new(STOP_TIMEOUT);
        silent.start();
        assert_eq!(silent.handle(Event::Exited, now), vec![Action::ShowFailure(Fault::Program)]);
        let mut unstartable = Supervisor::new(STOP_TIMEOUT);
        unstartable.start();
        assert_eq!(unstartable.handle(Event::SpawnFailed, now), vec![Action::ShowFailure(Fault::Program)]);
    }

    #[test]
    fn the_page_learns_once_the_version_the_app_was_updated_from() {
        // specs/2026-11-13-desk-upgrade.md UG2.5: ready names it, the notes of the page carry it.
        let now = Instant::now();
        let mut supervisor = Supervisor::new(STOP_TIMEOUT);
        supervisor.start();
        let ready = FromBackend::Ready {
            port: 24117,
            code: Code::new("c"),
            port_changed: false,
            updated_from: Some("1.2.0".into()),
            release_notes: None,
        };
        let actions = supervisor.handle(Event::Message(ready), now);
        assert_eq!(opened(&actions)[0].2.updated_from.as_deref(), Some("1.2.0"));
        let kept = Notes::default().merge(opened(&actions)[0].2.clone());
        assert_eq!(serde_json::to_value(&kept).unwrap()["updatedFrom"], "1.2.0");
        assert!(serde_json::to_value(Notes::default()).unwrap().get("updatedFrom").is_none());
    }

    #[test]
    fn the_page_learns_once_that_the_backend_was_restarted_after_a_fall() {
        let now = Instant::now();
        let mut supervisor = Supervisor::new(STOP_TIMEOUT);
        supervisor.start();
        supervisor.handle(ready(24117, "c", false), now);
        assert_eq!(supervisor.handle(Event::Message(fatal("backend", None)), now), vec![]);
        assert_eq!(supervisor.handle(Event::Exited, now), vec![Action::ShowStarting, Action::Spawn]);
        assert_eq!(supervisor.port(), None);
        let actions = supervisor.handle(ready(24117, "again", false), now);
        assert_eq!(
            opened(&actions)[0].2,
            Notes { port_changed: false, restarted_after_crash: true, updated_from: None, ..Notes::default() }
        );
        assert_eq!(supervisor.handle(Event::Refused(401), now), vec![Action::AskCode]);
        let next = supervisor.handle(Event::Message(FromBackend::OpenCode { code: Code::new("n") }), now);
        assert_eq!(opened(&next)[0].2, Notes::default());
    }

    #[test]
    fn three_falls_in_five_minutes_restart_and_the_fourth_shows_the_failure() {
        let start = Instant::now();
        let mut supervisor = Supervisor::new(STOP_TIMEOUT);
        supervisor.start();
        for fall in 0..RESTARTS as u64 {
            let at = start + Duration::from_secs(60 * fall);
            supervisor.handle(ready(24117, "c", false), at);
            assert_eq!(supervisor.handle(Event::Exited, at), vec![Action::ShowStarting, Action::Spawn], "fall {fall}");
        }
        let fourth = start + Duration::from_secs(4 * 60);
        supervisor.handle(ready(24117, "c", false), fourth);
        assert_eq!(supervisor.handle(Event::Exited, fourth), vec![Action::ShowFailure(Fault::Unknown)]);
    }

    #[test]
    fn falls_older_than_five_minutes_do_not_count() {
        let start = Instant::now();
        let mut supervisor = Supervisor::new(STOP_TIMEOUT);
        supervisor.start();
        // At 6:00 the falls of 0:00 and 1:00 are out of the window; at 6:30 the window holds 2:00, 6:00 and 6:30.
        for second in [0u64, 60, 120, 360, 390] {
            let at = start + Duration::from_secs(second);
            supervisor.handle(ready(24117, "c", false), at);
            assert_eq!(
                supervisor.handle(Event::Exited, at),
                vec![Action::ShowStarting, Action::Spawn],
                "second {second}"
            );
        }
        let at = start + Duration::from_secs(400);
        supervisor.handle(ready(24117, "c", false), at);
        supervisor.handle(Event::Message(fatal("database", None)), at);
        assert_eq!(supervisor.handle(Event::Exited, at), vec![Action::ShowFailure(fault("database", None))]);
    }

    #[test]
    fn closing_sends_shutdown_and_waits_for_the_exit_or_the_deadline() {
        let now = Instant::now();
        let mut supervisor = Supervisor::new(STOP_TIMEOUT);
        supervisor.start();
        supervisor.handle(ready(24117, "c", false), now);
        assert_eq!(supervisor.handle(Event::Close, now), vec![Action::SendShutdown]);
        assert_eq!(supervisor.handle(Event::Close, now), vec![]);
        assert_eq!(supervisor.handle(Event::Tick, now + Duration::from_secs(129)), vec![]);
        // A ready of the stopping backend opens nothing.
        assert_eq!(supervisor.handle(ready(24117, "late", false), now), vec![]);
        assert_eq!(supervisor.handle(Event::Exited, now + Duration::from_secs(2)), vec![Action::Exit]);

        let mut stuck = Supervisor::new(STOP_TIMEOUT);
        stuck.start();
        stuck.handle(Event::Close, now);
        assert_eq!(stuck.handle(Event::Tick, now + STOP_TIMEOUT), vec![Action::Kill, Action::Exit]);
        assert_eq!(stuck.handle(Event::Exited, now + STOP_TIMEOUT), vec![]);
    }

    #[test]
    fn the_steps_of_the_start_show_until_ready_and_a_return_starts_only_from_its_failure() {
        // specs/2026-11-13-desk-upgrade.md UG3.1, UG4.1.
        let now = Instant::now();
        let mut supervisor = Supervisor::new(STOP_TIMEOUT);
        supervisor.start();
        let step = Step::Migrate { version: Some("1.3.0".into()) };
        assert_eq!(
            supervisor.handle(Event::Message(FromBackend::Step(step.clone())), now),
            vec![Action::ShowStep(step)]
        );
        // A failure that is not a newer schema with a copy: no return.
        supervisor.handle(Event::Message(fatal("migrate", Some("pre-145-x"))), now);
        supervisor.handle(Event::Exited, now);
        assert_eq!(supervisor.handle(Event::Restore, now), vec![]);

        let candidate = crate::control::BackupCopy {
            name: "pre-145-20261113T110500.000Z".into(),
            made_at: "2026-11-13T14:05:00+03:00".into(),
            version: Some("1.3.0".into()),
        };
        let newer = FromBackend::Fatal {
            reason: "newer_schema".into(),
            backup: None,
            said: Box::new(Said { copy: Some(candidate), ..Said::default() }),
        };
        let mut older = Supervisor::new(STOP_TIMEOUT);
        older.start();
        older.handle(Event::Message(newer.clone()), now);
        // The backend that said fatal has not exited yet: nothing starts beside it.
        assert_eq!(older.handle(Event::Restore, now), vec![]);
        older.handle(Event::Exited, now);
        assert_eq!(older.handle(Event::Restore, now), vec![Action::ShowStarting, Action::SpawnRestoring]);
        // Twice is once; a ready open window is no failure to return from.
        assert_eq!(older.handle(Event::Restore, now), vec![]);
        older.handle(ready(24117, "c", false), now);
        assert_eq!(older.handle(Event::Restore, now), vec![]);

        // Without a candidate the failure of a newer schema has no return either.
        let mut empty = Supervisor::new(STOP_TIMEOUT);
        empty.start();
        empty.handle(Event::Message(fatal("newer_schema", None)), now);
        empty.handle(Event::Exited, now);
        assert_eq!(empty.handle(Event::Restore, now), vec![]);
    }

    #[test]
    fn erase_marks_stops_the_backend_and_erases_once_it_exited_without_a_restart() {
        // specs/2026-11-13-desk-erase.md ER1.3: the mark first, then the shutdown; the exit of the backend — a stop or a
        // fall — is the erasure and the end, never a restart; closing meanwhile and a second erase change nothing.
        let now = Instant::now();
        let mut supervisor = Supervisor::new(STOP_TIMEOUT);
        supervisor.start();
        supervisor.handle(ready(24117, "c", false), now);
        assert_eq!(
            supervisor.handle(Event::Message(FromBackend::Erase), now),
            vec![Action::BeginErase, Action::SendShutdown]
        );
        assert_eq!(supervisor.port(), None);
        assert_eq!(supervisor.handle(Event::Message(FromBackend::Erase), now), vec![]);
        assert_eq!(supervisor.handle(Event::Close, now), vec![]);
        assert_eq!(supervisor.handle(Event::Message(fatal("backend", None)), now), vec![]);
        assert_eq!(supervisor.handle(Event::Exited, now), vec![Action::Erase, Action::Exit]);
        assert_eq!(supervisor.handle(Event::Exited, now), vec![]);

        // A backend that does not stop in time is killed, then the data go.
        let mut stuck = Supervisor::new(STOP_TIMEOUT);
        stuck.start();
        stuck.handle(ready(24117, "c", false), now);
        stuck.handle(Event::Message(FromBackend::Erase), now);
        assert_eq!(stuck.handle(Event::Tick, now + STOP_TIMEOUT), vec![Action::Kill, Action::Erase, Action::Exit]);

        // An erase after the author closed the app is not heard: the app only closes.
        let mut closing = Supervisor::new(STOP_TIMEOUT);
        closing.start();
        closing.handle(ready(24117, "c", false), now);
        closing.handle(Event::Close, now);
        assert_eq!(closing.handle(Event::Message(FromBackend::Erase), now), vec![]);
        assert_eq!(closing.handle(Event::Exited, now), vec![Action::Exit]);
    }

    #[test]
    fn an_installation_leaves_the_page_stops_the_backend_and_installs_once_it_is_gone() {
        // specs/2026-11-13-desk-updates.md UB2.3: the page of the start first, then shutdown, then the installation.
        let now = Instant::now();
        let mut supervisor = Supervisor::new(STOP_TIMEOUT);
        // Only a ready backend asks.
        supervisor.start();
        assert_eq!(supervisor.handle(Event::Install, now), vec![]);
        supervisor.handle(ready(24117, "c", false), now);
        assert_eq!(supervisor.handle(Event::Install, now), vec![Action::Leave]);
        assert!(supervisor.installing() && supervisor.port().is_none());
        assert_eq!(supervisor.handle(Event::Install, now), vec![]);
        // UB2.9: closing the window does not cut it short.
        assert_eq!(supervisor.handle(Event::Close, now), vec![]);
        assert_eq!(supervisor.handle(Event::Left, now), vec![Action::SendShutdown]);
        assert_eq!(supervisor.handle(Event::Close, now), vec![]);
        assert_eq!(supervisor.handle(Event::Exited, now), vec![Action::Install { stopped: true }]);
        // UB2.7: nothing changed — the backend starts again on the same data, without a note of a fall.
        assert_eq!(
            supervisor.handle(Event::Installed { restart: false }, now),
            vec![Action::ShowStarting, Action::Spawn]
        );
        let opened = supervisor.handle(ready(24117, "again", false), now);
        assert_eq!(opened_notes(&opened), Notes::default());
        assert!(!supervisor.installing());

        // UB2.5: replaced — the app starts again.
        supervisor.handle(Event::Install, now);
        supervisor.handle(Event::Left, now);
        supervisor.handle(Event::Exited, now);
        assert_eq!(supervisor.handle(Event::Installed { restart: true }, now), vec![Action::Restart]);
    }

    #[test]
    fn a_page_that_does_not_load_or_a_backend_killed_by_the_deadline_still_ends_in_the_backend_again() {
        let now = Instant::now();
        let mut supervisor = Supervisor::new(STOP_TIMEOUT);
        supervisor.start();
        supervisor.handle(ready(24117, "c", false), now);
        supervisor.handle(Event::Install, now);
        assert_eq!(supervisor.handle(Event::Tick, now + LEAVE_TIMEOUT - Duration::from_secs(1)), vec![]);
        assert_eq!(supervisor.handle(Event::Tick, now + LEAVE_TIMEOUT), vec![Action::SendShutdown]);
        let stopping = now + LEAVE_TIMEOUT;
        // The deadline kills the backend once; the installation waits for its exit and does not begin after a kill.
        assert_eq!(supervisor.handle(Event::Tick, stopping + STOP_TIMEOUT), vec![Action::Kill]);
        assert_eq!(supervisor.handle(Event::Tick, stopping + STOP_TIMEOUT + Duration::from_secs(1)), vec![]);
        assert_eq!(supervisor.handle(Event::Exited, stopping + STOP_TIMEOUT), vec![Action::Install { stopped: false }]);
        assert_eq!(
            supervisor.handle(Event::Installed { restart: false }, now),
            vec![Action::ShowStarting, Action::Spawn]
        );

        // A backend that falls while the window leaves.
        supervisor.handle(ready(24117, "c", false), now);
        supervisor.handle(Event::Install, now);
        assert_eq!(supervisor.handle(Event::Exited, now), vec![Action::Install { stopped: false }]);
    }

    // specs/2026-11-16-desk-app-links.md AL1.3: a link waits while the backend starts, its steps and a restart after a fall
    // among them; it goes to an open page; the failure, a departure for an installation and every stop drop it.
    #[test]
    fn a_link_meets_the_stage_of_the_phase() {
        let now = Instant::now();
        let mut supervisor = Supervisor::new(STOP_TIMEOUT);
        supervisor.start();
        assert_eq!(supervisor.stage(), Stage::Starting);
        supervisor.handle(Event::Message(FromBackend::Step(Step::Migrate { version: None })), now);
        assert_eq!(supervisor.stage(), Stage::Starting);
        supervisor.handle(ready(24117, "c", false), now);
        assert_eq!(supervisor.stage(), Stage::Open);
        supervisor.handle(Event::Exited, now);
        assert_eq!(supervisor.stage(), Stage::Starting, "a restart after a fall");
        supervisor.handle(ready(24117, "c", false), now);
        supervisor.handle(Event::Install, now);
        assert_eq!(supervisor.stage(), Stage::Gone(Dropped::Leaving));
        supervisor.handle(Event::Left, now);
        assert_eq!(supervisor.stage(), Stage::Gone(Dropped::Installing));
        supervisor.handle(Event::Exited, now);
        assert_eq!(supervisor.stage(), Stage::Gone(Dropped::Installing));
        supervisor.handle(Event::Installed { restart: true }, now);
        assert_eq!(supervisor.stage(), Stage::Gone(Dropped::Closing));

        let mut erasing = Supervisor::new(STOP_TIMEOUT);
        erasing.start();
        erasing.handle(ready(24117, "c", false), now);
        erasing.handle(Event::Message(FromBackend::Erase), now);
        assert_eq!(erasing.stage(), Stage::Gone(Dropped::Erasing));

        let mut closing = Supervisor::new(STOP_TIMEOUT);
        closing.start();
        closing.handle(ready(24117, "c", false), now);
        closing.handle(Event::Close, now);
        assert_eq!(closing.stage(), Stage::Gone(Dropped::Closing));
        closing.handle(Event::Exited, now);
        assert_eq!(closing.stage(), Stage::Gone(Dropped::Closing));

        let mut failed = Supervisor::new(STOP_TIMEOUT);
        failed.start();
        failed.handle(Event::SpawnFailed, now);
        assert_eq!(failed.stage(), Stage::Gone(Dropped::Failure));

        // The opening carries the place of a link before the fragment of its code.
        let opened = supervisor_open(&mut Supervisor::new(STOP_TIMEOUT));
        assert_eq!(
            opened.to(&Target::Refused).as_str(),
            "http://127.0.0.1:24117/desktop/open?n=0&to=%2Fmanuscript%2Fopen%3Frefused%3D1#c=c"
        );
    }

    fn supervisor_open(supervisor: &mut Supervisor) -> OpenUrl {
        supervisor.start();
        match supervisor.handle(ready(24117, "c", false), Instant::now()).remove(0) {
            Action::Open { url, .. } => url,
            other => panic!("{other:?}"),
        }
    }

    #[test]
    fn the_erasure_comes_before_an_installation_that_has_not_begun() {
        // UB2.9: an erase while the window leaves or while the backend stops for the installation is the erasure; an
        // installation is not asked during an erasure.
        let now = Instant::now();
        let mut leaving = Supervisor::new(STOP_TIMEOUT);
        leaving.start();
        leaving.handle(ready(24117, "c", false), now);
        leaving.handle(Event::Install, now);
        assert_eq!(
            leaving.handle(Event::Message(FromBackend::Erase), now),
            vec![Action::BeginErase, Action::SendShutdown]
        );
        assert_eq!(leaving.handle(Event::Left, now), vec![]);
        assert_eq!(leaving.handle(Event::Exited, now), vec![Action::Erase, Action::Exit]);

        let mut stopping = Supervisor::new(STOP_TIMEOUT);
        stopping.start();
        stopping.handle(ready(24117, "c", false), now);
        stopping.handle(Event::Install, now);
        stopping.handle(Event::Left, now);
        assert_eq!(stopping.handle(Event::Message(FromBackend::Erase), now), vec![Action::BeginErase]);
        assert_eq!(stopping.handle(Event::Exited, now), vec![Action::Erase, Action::Exit]);

        let mut erasing = Supervisor::new(STOP_TIMEOUT);
        erasing.start();
        erasing.handle(ready(24117, "c", false), now);
        erasing.handle(Event::Message(FromBackend::Erase), now);
        assert_eq!(erasing.handle(Event::Install, now), vec![]);
        assert_eq!(erasing.handle(Event::Exited, now), vec![Action::Erase, Action::Exit]);
    }
}
