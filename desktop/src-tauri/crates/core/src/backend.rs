// SPDX-License-Identifier: AGPL-3.0-only

//! The backend process (specs/2026-11-12-desk-shell.md SH1.3): the program next to the shell, started with no
//! arguments and no added environment; `start` and every other message go only through its stdin, its answers come
//! through its stdout (DA3.2). Two threads hear it: one reads the lines, one notices the exit — and the exit is reported
//! only after the last line, so a `fatal` always comes before it.

use std::io::{self, BufRead, BufReader, Read, Write};
use std::path::Path;
use std::process::{Child, ChildStdin, Command, Stdio};
use std::sync::mpsc::{self, Sender};
use std::sync::{Arc, Mutex};
use std::thread;
use std::time::Duration;

use crate::control::{parse, LineError, ToBackend};
use crate::supervisor::Event;

/// One line is at most this long, as on the side of the backend (control.go, maxControlLine).
const MAX_LINE: u64 = 1 << 20;
/// How often the exit is checked.
const POLL: Duration = Duration::from_millis(100);
/// How long the exit waits for the reader to hear the end of stdout.
const DRAIN: Duration = Duration::from_secs(2);

/// What the threads of a backend tell their owner besides the events of the supervisor.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Heard {
    Event(Event),
    /// A line that is not a message: logged by its kind, never by its text.
    Unreadable(LineError),
    /// The process ended with this code (`None` — killed by a signal).
    ExitCode(Option<i32>),
}

/// A running backend.
pub struct Backend {
    child: Arc<Mutex<Child>>,
    stdin: Option<ChildStdin>,
}

impl Backend {
    /// Starts `program` and writes `start` to it; what it says goes to `heard`.
    pub fn spawn(program: &Path, start: &ToBackend<'_>, heard: Sender<Heard>) -> io::Result<Backend> {
        let mut command = Command::new(program);
        command.stdin(Stdio::piped()).stdout(Stdio::piped()).stderr(Stdio::inherit());
        #[cfg(windows)]
        {
            use std::os::windows::process::CommandExt;
            // CREATE_NO_WINDOW: the backend is a console program, the shell is not.
            command.creation_flags(0x0800_0000);
        }
        let mut child = command.spawn()?;
        let (Some(mut stdin), Some(stdout)) = (child.stdin.take(), child.stdout.take()) else {
            let _ = child.kill();
            let _ = child.wait();
            return Err(io::Error::other("the backend has no pipes"));
        };
        if let Err(error) = stdin.write_all(start.line().as_bytes()).and_then(|()| stdin.flush()) {
            let _ = child.kill();
            let _ = child.wait();
            return Err(error);
        }
        let child = Arc::new(Mutex::new(child));
        let (drained, drained_signal) = mpsc::channel::<()>();
        let lines = heard.clone();
        thread::spawn(move || {
            read_lines(stdout, &lines);
            let _ = drained.send(());
        });
        let watched = Arc::clone(&child);
        thread::spawn(move || {
            let status = loop {
                let polled = watched.lock().map(|mut child| child.try_wait());
                match polled {
                    Ok(Ok(Some(status))) => break status.code(),
                    Ok(Ok(None)) => thread::sleep(POLL),
                    _ => break None,
                }
            };
            let _ = drained_signal.recv_timeout(DRAIN);
            let _ = heard.send(Heard::ExitCode(status));
            let _ = heard.send(Heard::Event(Event::Exited));
        });
        Ok(Backend { child, stdin: Some(stdin) })
    }

    /// Writes a message; a backend that is gone does not hear it, and its exit is reported anyway.
    pub fn send(&mut self, message: &ToBackend<'_>) -> io::Result<()> {
        let stdin = self.stdin.as_mut().ok_or_else(|| io::Error::from(io::ErrorKind::BrokenPipe))?;
        stdin.write_all(message.line().as_bytes())?;
        stdin.flush()
    }

    /// Kills the process: it did not stop in time.
    pub fn kill(&mut self) {
        self.stdin = None;
        if let Ok(mut child) = self.child.lock() {
            let _ = child.kill();
        }
    }
}

impl Drop for Backend {
    /// Closing stdin is a stop for the backend too (DA1.6).
    fn drop(&mut self) {
        self.stdin = None;
    }
}

fn read_lines(stdout: impl Read, heard: &Sender<Heard>) {
    let mut reader = BufReader::new(stdout);
    let mut line = Vec::new();
    loop {
        line.clear();
        match (&mut reader).take(MAX_LINE).read_until(b'\n', &mut line) {
            Ok(0) | Err(_) => return,
            Ok(_) => {}
        }
        if line.last() != Some(&b'\n') && line.len() as u64 >= MAX_LINE {
            // A line longer than any message: skip the rest of it.
            let _ = heard.send(Heard::Unreadable(LineError::NotJson));
            let mut rest = Vec::new();
            if reader.read_until(b'\n', &mut rest).map(|read| read == 0).unwrap_or(true) {
                return;
            }
            continue;
        }
        let heard_line = match std::str::from_utf8(&line) {
            Ok(text) if text.trim().is_empty() => continue,
            Ok(text) => match parse(text) {
                Ok(message) => Heard::Event(Event::Message(message)),
                Err(error) => Heard::Unreadable(error),
            },
            Err(_) => Heard::Unreadable(LineError::NotJson),
        };
        if heard.send(heard_line).is_err() {
            return;
        }
    }
}
