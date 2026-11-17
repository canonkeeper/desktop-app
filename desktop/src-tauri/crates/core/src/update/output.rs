// SPDX-License-Identifier: AGPL-3.0-only

//! The output of the installer of a package of Linux (specs/2026-11-17-desk-update-linux.md UL1.2, UL1.3): both pipes are read
//! to their end on threads of their own — a pipe left full would stop the installer, and the window cannot close while it
//! installs —, every piece goes on to the stderr of the shell as it comes, as the inherited output went to the journal of the
//! user, and only the signs of a line are kept: never its text. The signs also tell whether `apt-get` never took the lock it
//! waited for (UL2.2).

use std::io::{self, Read, Write};
use std::process::{Child, ExitStatus};
use std::sync::mpsc::{self, Sender};
use std::thread;
use std::time::{Duration, Instant};

use super::package::Step;

/// At most this much of a line of the output is looked at; the rest of a longer line is passed on and not kept (UL1.2).
pub(super) const LINE: usize = 4096;

/// How long the end of the output is waited for once the installer exited: a program it left behind may hold the pipes.
const DRAIN: Duration = Duration::from_secs(2);

/// What the readers of the output and the wait for the exit tell the thread of the installation.
enum Seen {
    /// A line of the output that is not empty: on stdout or stderr, whether it names a lock, and whether it is an error of
    /// apt (`E:`, which apt does not translate) that names one.
    Line { out: bool, lock: bool, error: bool },
    /// A pipe ended.
    Ended,
    /// The installer exited, with its status when it could be had.
    Exited(Option<ExitStatus>),
}

/// The steps the output of an installer shows (UL1.3) and whether it never took a lock (UL2.2): kept as signs, never as text.
#[derive(Debug)]
pub(super) struct Watch {
    shown: Step,
    /// An error of apt named a lock.
    error: bool,
    /// A line without a lock came after the last line that named one.
    after: bool,
}

impl Watch {
    fn new() -> Watch {
        Watch { shown: Step::Password, error: false, after: false }
    }

    /// The installer never took the lock it waited for (UL2.2): its error named a lock, and no line without a lock came after
    /// the last that named one — the system did not change. A wait after which the lock was taken and the work failed is
    /// not this.
    pub(super) fn never_locked(&self) -> bool {
        self.error && !self.after
    }

    /// A line of the output; the step the page says next, when it changed. A line that names a lock is the wait for it,
    /// once a wait; the first line of stdout — not the errors of `pkexec` on stderr — is the work after the password, and so
    /// is any line without a lock after a wait.
    fn line(&mut self, out: bool, lock: bool, error: bool) -> Option<Step> {
        self.error |= error;
        self.after = !lock;
        let next = match (lock, self.shown) {
            (true, _) => Step::LockWait,
            (false, Step::Password) if out => Step::Running,
            (false, Step::LockWait) => Step::Running,
            (false, shown) => shown,
        };
        (next != self.shown).then(|| {
            self.shown = next;
            next
        })
    }
}

/// Reads both pipes of `child` to their end and waits for its exit (UL1.1, UL1.2): every piece of the output goes on to
/// `output` as it comes, and only the signs of its lines are kept; `steps` hears the steps they show. A program the
/// installer left behind that still holds a pipe does not hold the installation: its end is waited for a little.
pub(super) fn follow<W: Write + Send + 'static>(
    mut child: Child,
    locks: &[Vec<u8>],
    steps: &mut impl FnMut(Step),
    output: fn() -> W,
) -> (Option<ExitStatus>, Watch) {
    let (seen, heard) = mpsc::channel::<Seen>();
    let mut open = 0;
    for (out, pipe) in [
        (true, child.stdout.take().map(|pipe| Box::new(pipe) as Box<dyn Read + Send>)),
        (false, child.stderr.take().map(|pipe| Box::new(pipe) as Box<dyn Read + Send>)),
    ] {
        if let Some(pipe) = pipe {
            open += 1;
            let (seen, locks) = (seen.clone(), locks.to_vec());
            thread::spawn(move || read(pipe, out, &locks, output(), &seen));
        }
    }
    let exited = seen.clone();
    thread::spawn(move || {
        let _ = exited.send(Seen::Exited(child.wait().ok()));
    });
    drop(seen);
    let mut watch = Watch::new();
    let mut status = None;
    let mut until: Option<Instant> = None;
    while open > 0 || status.is_none() {
        let next = match until {
            Some(until) => heard.recv_timeout(until.saturating_duration_since(Instant::now())).ok(),
            None => heard.recv().ok(),
        };
        match next {
            Some(Seen::Line { out, lock, error }) => {
                if let Some(step) = watch.line(out, lock, error) {
                    steps(step);
                }
            }
            Some(Seen::Ended) => open -= 1,
            Some(Seen::Exited(exit)) => {
                status = Some(exit);
                until = Some(Instant::now() + DRAIN);
            }
            None => break,
        }
    }
    (status.flatten(), watch)
}

/// Reads one pipe to its end: each piece goes on to `output` at once, each line — its first 4 KiB — becomes its signs.
fn read(mut pipe: Box<dyn Read + Send>, out: bool, locks: &[Vec<u8>], mut output: impl Write, seen: &Sender<Seen>) {
    let mut chunk = [0u8; 8192];
    let mut line = Vec::with_capacity(LINE);
    let said = |line: &mut Vec<u8>| {
        let text = line.trim_ascii();
        if !text.is_empty() {
            let lock = locks.iter().any(|lock| !lock.is_empty() && text.windows(lock.len()).any(|part| part == lock));
            let _ = seen.send(Seen::Line { out, lock, error: lock && text.starts_with(b"E:") });
        }
        line.clear();
    };
    loop {
        let read = match pipe.read(&mut chunk) {
            Ok(0) => break,
            Ok(read) => read,
            Err(error) if error.kind() == io::ErrorKind::Interrupted => continue,
            Err(_) => break,
        };
        let _ = output.write_all(&chunk[..read]).and_then(|()| output.flush());
        for &byte in &chunk[..read] {
            if byte == b'\n' {
                said(&mut line);
            } else if line.len() < LINE {
                line.push(byte);
            }
        }
    }
    said(&mut line);
    let _ = seen.send(Seen::Ended);
}

#[cfg(all(test, unix))]
mod tests {
    use super::*;
    use std::process::{Command, Stdio};
    use std::sync::Mutex;

    /// The lock the lines of the tests name.
    const LOCK: &[u8] = b"/var/lib/dpkg/lock-frontend";

    /// The output of `script` as an installer gives it, followed to its end: the steps it showed, its exit code, the signs.
    fn followed<W: Write + Send + 'static>(script: &str, output: fn() -> W) -> (Vec<Step>, Option<i32>, Watch) {
        let child = Command::new("/bin/sh")
            .arg("-c")
            .arg(script)
            .stdout(Stdio::piped())
            .stderr(Stdio::piped())
            .spawn()
            .unwrap();
        let mut steps = Vec::new();
        let (status, watch) = follow(child, &[LOCK.to_vec()], &mut |step| steps.push(step), output);
        (steps, status.and_then(|status| status.code()), watch)
    }

    fn steps(script: &str) -> Vec<Step> {
        followed(script, io::sink).0
    }

    // specs/2026-11-17-desk-update-linux.md UL1.3: after the password the first line of stdout is the work; a line that names a
    // lock is the wait, once a wait; a line without a lock after it is the work again. One stream keeps the order here.
    #[test]
    fn the_page_hears_the_steps_the_output_of_the_installer_shows() {
        use Step::*;
        let script = "echo 'Чтение списков пакетов…'\n\
             echo 'Ожидание блокировки кэша: Не удалось получить блокировку файла /var/lib/dpkg/lock-frontend… 1с'\n\
             echo 'Ожидание блокировки кэша: Не удалось получить блокировку файла /var/lib/dpkg/lock-frontend… 2с'\n\
             echo 'Настраивается пакет canonkeeper (1.3.0)…'\n\
             echo 'Ожидание блокировки кэша: … /var/lib/dpkg/lock-frontend …'\n\
             echo 'Обрабатываются триггеры…'";
        assert_eq!(steps(script), [Running, LockWait, Running, LockWait, Running]);
        // A wait on stderr is a wait too; a line of stderr before any wait — the errors of `pkexec` among them — is no work.
        assert_eq!(
            steps("echo 'W: что-то' >&2\necho 'Waiting for cache lock: /var/lib/dpkg/lock-frontend' >&2"),
            [LockWait]
        );
        let (said, code, _) =
            followed("echo 'Error executing command as another user: Request dismissed' >&2\nexit 126", io::sink);
        assert_eq!((said, code), (vec![], Some(126)));
        assert_eq!(
            [Password, Running, LockWait].map(Step::code),
            ["update_password_asked", "update_package_running", "update_lock_wait"]
        );
    }

    // UL1.2: only the first 4 KiB of a line are looked at; empty lines are no lines.
    #[test]
    fn a_line_is_looked_at_by_its_first_four_kib() {
        let long = "x".repeat(LINE);
        assert_eq!(
            steps(&format!("echo '{long} /var/lib/dpkg/lock-frontend'\necho\necho 'x /var/lib/dpkg/lock-frontend'")),
            [Step::Running, Step::LockWait]
        );
        assert_eq!(steps("echo '   '\necho '' >&2"), []);
    }

    // UL1.2: both pipes are read to their end — an installer that writes more than a pipe holds does not stand still.
    #[test]
    fn the_output_is_read_to_its_end() {
        let started = Instant::now();
        let (_, code, _) = followed(
            "yes 'Распаковывается пакет…' | head -c 300000\nyes 'W: предупреждение' | head -c 300000 >&2",
            io::sink,
        );
        assert_eq!(code, Some(0));
        assert!(started.elapsed() < Duration::from_secs(20));
    }

    // UL1.2: a program the installer left running with its pipes holds the end no longer than a moment after its exit.
    #[test]
    fn a_program_the_installer_left_holding_the_pipes_does_not_hold_the_installation() {
        let started = Instant::now();
        let (_, code, _) = followed("sleep 6 &\nexit 1", io::sink);
        assert_eq!(code, Some(1));
        assert!(started.elapsed() < Duration::from_secs(5), "{:?}", started.elapsed());
    }

    /// What the installer passed on to the stderr of the shell in the test of the passing: one test writes it.
    static PASSED: Mutex<Vec<u8>> = Mutex::new(Vec::new());

    struct Passed;

    impl Write for Passed {
        fn write(&mut self, bytes: &[u8]) -> io::Result<usize> {
            PASSED.lock().unwrap().extend_from_slice(bytes);
            Ok(bytes.len())
        }

        fn flush(&mut self) -> io::Result<()> {
            Ok(())
        }
    }

    // UL1.2: every line of both pipes goes on to the stderr of the shell, as the inherited output went to the journal of the user.
    #[test]
    fn the_output_of_both_pipes_goes_on_whole() {
        let (out, err) = ("Распаковывается пакет canonkeeper (1.3.0)…\n", "W: предупреждение установщика\n");
        let (_, code, _) = followed(
            &format!("yes '{}' | head -n 20000\nyes '{}' | head -n 10000 >&2", out.trim_end(), err.trim_end()),
            || Passed,
        );
        assert_eq!(code, Some(0));
        let passed = PASSED.lock().unwrap();
        assert_eq!(passed.len(), 20000 * out.len() + 10000 * err.len());
        assert_eq!(passed.iter().filter(|&&byte| byte == b'\n').count(), 30000);
    }

    /// The lines of `apt-get` that wait for the lock and the error that it could not get it, on stderr.
    const WAITED: &str = "echo 'Ожидание блокировки кэша: Не удалось получить блокировку файла /var/lib/dpkg/lock-frontend… 1с' >&2\n\
                          echo 'Ожидание блокировки кэша: Не удалось получить блокировку файла /var/lib/dpkg/lock-frontend… 2с' >&2";
    const NOT_GOT: &str =
        "echo 'E: Невозможно получить блокировку внешнего интерфейса dpkg (/var/lib/dpkg/lock-frontend)' >&2";

    // UL2.2: the installer never took the lock — its error `E:` named a lock and no line without one came after the lines of the
    // locks; a wait without the error, an error without a lock and a line after the error are not that.
    #[test]
    fn the_signs_tell_a_lock_that_was_never_taken() {
        let never = |script: &str| followed(script, io::sink).2.never_locked();
        assert!(never(&format!("{WAITED}\n{NOT_GOT}\nexit 100")));
        assert!(never(&format!("{NOT_GOT}\nexit 100")));
        assert!(!never(&format!("{WAITED}\nexit 100")));
        assert!(!never("echo 'E: Нарушены зависимости' >&2\nexit 100"));
        assert!(!never(&format!("{WAITED}\n{NOT_GOT}\necho 'E: Нарушены зависимости' >&2\nexit 100")));
        assert!(!never(&format!("echo 'E: {} /var/lib/dpkg/lock-frontend' >&2\nexit 100", "x".repeat(LINE))));
    }
}
