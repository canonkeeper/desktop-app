// SPDX-License-Identifier: AGPL-3.0-only

//! The supervision of the backend as a state machine (specs/2026-11-12-desk-shell.md SH1.4, SH2.2, SH2.3): it hears
//! what happened — a line of the backend, its exit, a refused code, a request to close, the passing of time — and says
//! what to do. It does no input or output itself, so every path is tested without a process or a window.

use std::time::{Duration, Instant};

use serde::Serialize;

use crate::control::{Code, FromBackend};
use crate::navigation::open_url;
use crate::words::Fault;

/// How many falls in a row the shell answers with a restart, and within what time (SH2.2).
pub const RESTARTS: usize = 3;
pub const RESTART_WINDOW: Duration = Duration::from_secs(5 * 60);
/// How long the shell waits for the backend to stop: its own timeouts — turns 5 s, core 30 s, PostgreSQL 90 s — and a
/// little more (SH2.3).
pub const STOP_TIMEOUT: Duration = Duration::from_secs(130);
/// How many refused codes in one run of the backend the shell answers with a new code.
pub const OPEN_ATTEMPTS: u8 = 3;

/// What the page learns once after it opens (`launch_notes`, SH1.4, SH2.2).
#[derive(Debug, Default, Clone, Copy, PartialEq, Eq, Serialize)]
pub struct Notes {
    /// The remembered port was taken: the address of the window is new, its place of reading and its view are reset.
    #[serde(rename = "portChanged")]
    pub port_changed: bool,
    /// The backend fell and the shell started it again.
    #[serde(rename = "restartedAfterCrash")]
    pub restarted_after_crash: bool,
}

impl Notes {
    /// Both notes, the ones not yet taken and the new ones.
    pub fn merge(self, other: Notes) -> Notes {
        Notes {
            port_changed: self.port_changed || other.port_changed,
            restarted_after_crash: self.restarted_after_crash || other.restarted_after_crash,
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
    /// Time passes.
    Tick,
}

/// What to do.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Action {
    /// Start the backend and send it `start`.
    Spawn,
    /// Show the page of the shell: «CanonKeeper запускается…».
    ShowStarting,
    /// Let the page of 127.0.0.1:port call the commands of the shell and open the window at `url`; `notes` are what
    /// the page learns once.
    Open { port: u16, url: OpenUrl, notes: Notes },
    /// Ask the backend for a new code (`open-code`).
    AskCode,
    /// Show the failure window with the words of the fault.
    ShowFailure(Fault),
    /// Write `shutdown`.
    SendShutdown,
    /// Kill the backend: it did not stop in time.
    Kill,
    /// End the app.
    Exit,
}

/// The address that opens the window: it carries the code, so it never shows in a debug print.
#[derive(Clone, PartialEq, Eq)]
pub struct OpenUrl(String);

impl OpenUrl {
    pub fn as_str(&self) -> &str {
        &self.0
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
    /// `shutdown` is sent; the shell waits for the backend to exit.
    Stopping { since: Instant },
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
    refusals: u8,
    restarted: bool,
    stop_timeout: Duration,
}

impl Supervisor {
    pub fn new(stop_timeout: Duration) -> Supervisor {
        Supervisor {
            phase: Phase::Starting,
            alive: false,
            falls: Vec::new(),
            fatal: None,
            refusals: 0,
            restarted: false,
            stop_timeout,
        }
    }

    /// The first actions: start the backend.
    pub fn start(&mut self) -> Vec<Action> {
        self.alive = true;
        vec![Action::Spawn]
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
            (Event::Close, Phase::Stopping { .. } | Phase::Stopped) => vec![],
            (Event::Close, _) => {
                if self.alive {
                    self.phase = Phase::Stopping { since: now };
                    vec![Action::SendShutdown]
                } else {
                    self.phase = Phase::Stopped;
                    vec![Action::Exit]
                }
            }
            (Event::Exited, Phase::Stopping { .. }) => {
                self.alive = false;
                self.phase = Phase::Stopped;
                vec![Action::Exit]
            }
            (Event::Tick, Phase::Stopping { since }) if now.duration_since(since) >= self.stop_timeout => {
                self.phase = Phase::Stopped;
                vec![Action::Kill, Action::Exit]
            }
            (_, Phase::Stopping { .. } | Phase::Stopped) => vec![],

            (Event::SpawnFailed, _) => {
                self.alive = false;
                self.phase = Phase::Failed;
                vec![Action::ShowFailure(Fault::Program)]
            }
            (Event::Message(FromBackend::Ready { port, code, port_changed }), Phase::Starting) => {
                self.phase = Phase::Open { port };
                self.refusals = 0;
                let notes = Notes { port_changed, restarted_after_crash: std::mem::take(&mut self.restarted) };
                vec![open(port, &code, 0, notes)]
            }
            (Event::Message(FromBackend::OpenCode { code }), Phase::Open { port }) => {
                vec![open(port, &code, self.refusals, Notes::default())]
            }
            (Event::Message(FromBackend::Fatal { reason, backup }), _) => {
                self.fatal = Some(Fault::Fatal { reason, backup });
                vec![]
            }
            (Event::Message(_), _) => vec![],
            (Event::Refused(_), Phase::Open { .. }) => {
                self.refusals += 1;
                if self.refusals <= OPEN_ATTEMPTS {
                    vec![Action::AskCode]
                } else {
                    self.phase = Phase::Failed;
                    vec![Action::ShowFailure(Fault::Unknown)]
                }
            }
            (Event::Refused(_), _) => vec![],
            (Event::Exited, Phase::Starting) => {
                // The backend ended before it was ready: its fatal says why, silence means the program vanished.
                self.alive = false;
                self.phase = Phase::Failed;
                vec![Action::ShowFailure(self.fatal.take().unwrap_or(Fault::Program))]
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
                    self.phase = Phase::Failed;
                    vec![Action::ShowFailure(self.fatal.take().unwrap_or(Fault::Unknown))]
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
        Event::Message(FromBackend::Ready { port, code: Code::new(code), port_changed })
    }

    fn opened(actions: &[Action]) -> Vec<(u16, String, Notes)> {
        actions
            .iter()
            .filter_map(|action| match action {
                Action::Open { port, url, notes } => Some((*port, url.as_str().to_string(), *notes)),
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
                Notes { port_changed: true, restarted_after_crash: false }
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
        let fatal = FromBackend::Fatal { reason: "migrate".into(), backup: Some("pre-0142-x".into()) };
        assert_eq!(supervisor.handle(Event::Message(fatal), now), vec![]);
        assert_eq!(
            supervisor.handle(Event::Exited, now),
            vec![Action::ShowFailure(Fault::Fatal { reason: "migrate".into(), backup: Some("pre-0142-x".into()) })]
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
    fn the_page_learns_once_that_the_backend_was_restarted_after_a_fall() {
        let now = Instant::now();
        let mut supervisor = Supervisor::new(STOP_TIMEOUT);
        supervisor.start();
        supervisor.handle(ready(24117, "c", false), now);
        let fatal = FromBackend::Fatal { reason: "backend".into(), backup: None };
        assert_eq!(supervisor.handle(Event::Message(fatal), now), vec![]);
        assert_eq!(supervisor.handle(Event::Exited, now), vec![Action::ShowStarting, Action::Spawn]);
        assert_eq!(supervisor.port(), None);
        let actions = supervisor.handle(ready(24117, "again", false), now);
        assert_eq!(opened(&actions)[0].2, Notes { port_changed: false, restarted_after_crash: true });
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
        let fatal = FromBackend::Fatal { reason: "database".into(), backup: None };
        supervisor.handle(Event::Message(fatal), at);
        assert_eq!(
            supervisor.handle(Event::Exited, at),
            vec![Action::ShowFailure(Fault::Fatal { reason: "database".into(), backup: None })]
        );
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
}
