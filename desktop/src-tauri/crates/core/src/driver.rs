// SPDX-License-Identifier: AGPL-3.0-only

//! The thread that supervises the backend (specs/2026-11-12-desk-shell.md SH1, SH2): it feeds what the backend and the
//! window report into the supervisor and carries out its actions — the process here, the window through [`Window`],
//! which the window crate implements with Tauri. Words of a failure come from the one table of [`crate::words`].

use std::collections::VecDeque;
use std::path::PathBuf;
use std::sync::mpsc::{self, RecvTimeoutError, Sender};
use std::thread::{self, JoinHandle};
use std::time::{Duration, Instant};

use crate::backend::{Backend, Heard};
use crate::control::{FromBackend, ToBackend};
use crate::journal::{name, Journal};
use crate::paths::System;
use crate::supervisor::{Action, Event, Notes, Supervisor};
use crate::words::{words, Fault};

/// How often time passes for the supervisor while nothing happens.
const TICK: Duration = Duration::from_secs(1);

/// The window as the supervision sees it. Every call comes from the thread of the driver.
pub trait Window: Send + 'static {
    /// Shows the page of the shell: «CanonKeeper запускается…».
    fn show_starting(&self);
    /// Lets the page of 127.0.0.1:port call the commands of the shell, keeps the notes for `launch_notes` and opens
    /// `url`, which carries the one-time code.
    fn open(&self, port: u16, url: &str, notes: Notes);
    /// Shows the failure window with these words.
    fn show_failure(&self, words: &str);
    /// Ends the app.
    fn exit(&self);
}

/// What the supervision needs to know.
#[derive(Debug, Clone)]
pub struct Config {
    /// The program of the backend, next to the shell.
    pub program: PathBuf,
    /// The data directory and the directory of the bundle, plain.
    pub data_dir: String,
    pub resources: String,
    pub system: System,
    pub stop_timeout: Duration,
}

/// What the window reports to the supervision.
#[derive(Clone)]
pub struct Handle {
    sender: Sender<Heard>,
}

impl Handle {
    /// The window or the system asks the app to close.
    pub fn close(&self) {
        let _ = self.sender.send(Heard::Event(Event::Close));
    }

    /// The page of the code exchange reports that the session refused the code.
    pub fn refused(&self, status: u16) {
        let _ = self.sender.send(Heard::Event(Event::Refused(status)));
    }
}

/// Starts the supervision on its own thread; it ends after [`Window::exit`].
pub fn run<W: Window>(config: Config, window: W, journal: Journal) -> (Handle, JoinHandle<()>) {
    let (sender, receiver) = mpsc::channel::<Heard>();
    let handle = Handle { sender: sender.clone() };
    let thread = thread::spawn(move || {
        let mut supervisor = Supervisor::new(config.stop_timeout);
        let mut backend: Option<Backend> = None;
        let mut actions: VecDeque<Action> = supervisor.start().into();
        loop {
            while let Some(action) = actions.pop_front() {
                match action {
                    Action::Spawn => {
                        let start = ToBackend::Start { data_dir: &config.data_dir, resources: &config.resources };
                        match Backend::spawn(&config.program, &start, sender.clone()) {
                            Ok(started) => {
                                journal.note("backend_spawned");
                                backend = Some(started);
                            }
                            Err(_) => {
                                journal.note("backend_spawn_failed");
                                actions.extend(supervisor.handle(Event::SpawnFailed, Instant::now()));
                            }
                        }
                    }
                    Action::ShowStarting => window.show_starting(),
                    Action::Open { port, url, notes } => {
                        journal.note(&format!("window_open port={port}"));
                        window.open(port, url.as_str(), notes);
                    }
                    Action::AskCode => {
                        if let Some(running) = backend.as_mut() {
                            let _ = running.send(&ToBackend::OpenCode);
                        }
                    }
                    Action::ShowFailure(fault) => {
                        journal.note(&format!("failure {}", fault_name(&fault)));
                        window.show_failure(&words(&fault, config.system, &config.data_dir));
                    }
                    Action::SendShutdown => {
                        journal.note("shutdown_sent");
                        if let Some(running) = backend.as_mut() {
                            let _ = running.send(&ToBackend::Shutdown);
                        }
                    }
                    Action::Kill => {
                        journal.note("backend_killed");
                        if let Some(running) = backend.as_mut() {
                            running.kill();
                        }
                    }
                    Action::Exit => {
                        journal.note("exit");
                        drop(backend.take());
                        window.exit();
                        return;
                    }
                }
            }
            let heard = match receiver.recv_timeout(TICK) {
                Ok(heard) => heard,
                Err(RecvTimeoutError::Timeout) => Heard::Event(Event::Tick),
                Err(RecvTimeoutError::Disconnected) => return,
            };
            match heard {
                Heard::Event(event) => {
                    note(&journal, &event);
                    if event == Event::Exited {
                        backend = None;
                    }
                    actions.extend(supervisor.handle(event, Instant::now()));
                }
                Heard::Unreadable(_) => journal.note("control_line_unreadable"),
                Heard::ExitCode(code) => journal.note(&match code {
                    Some(code) => format!("backend_exit code={code}"),
                    None => "backend_exit signal".to_string(),
                }),
            }
        }
    });
    (handle, thread)
}

fn fault_name(fault: &Fault) -> &str {
    match fault {
        Fault::Program => "program",
        Fault::Fatal { reason, .. } => name(reason),
        Fault::Unknown => "unknown",
    }
}

fn note(journal: &Journal, event: &Event) {
    match event {
        Event::Message(FromBackend::Ready { port, port_changed, .. }) => {
            journal.note(&format!("backend_ready port={port} port_changed={port_changed}"));
        }
        Event::Message(FromBackend::OpenCode { .. }) => journal.note("backend_open_code"),
        Event::Message(FromBackend::Fatal { reason, .. }) => {
            journal.note(&format!("backend_fatal reason={}", name(reason)))
        }
        Event::Message(FromBackend::Other { kind }) => journal.note(&format!("backend_message type={}", name(kind))),
        Event::Refused(status) => journal.note(&format!("window_code_refused status={status}")),
        Event::Close => journal.note("close_requested"),
        Event::SpawnFailed | Event::Exited | Event::Tick => {}
    }
}
