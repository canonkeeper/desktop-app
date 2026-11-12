// SPDX-License-Identifier: AGPL-3.0-only

//! The supervision on a stand-in backend process (specs/2026-11-12-desk-shell.md SH1.4, SH2.2, SH2.3): the real
//! threads, pipes and exits, a window that records what it was told.

use std::fs;
use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

use canonkeeper_desktop_core::driver::{run, Config, Handle, Window};
use canonkeeper_desktop_core::journal::Journal;
use canonkeeper_desktop_core::paths::System;
use canonkeeper_desktop_core::supervisor::{Notes, STOP_TIMEOUT};

#[derive(Clone, Default)]
struct Recorder(Arc<Mutex<Vec<String>>>);

impl Recorder {
    fn seen(&self) -> Vec<String> {
        self.0.lock().unwrap().clone()
    }

    fn wait_for(&self, what: &str) {
        let deadline = Instant::now() + Duration::from_secs(20);
        while Instant::now() < deadline {
            if self.seen().iter().any(|seen| seen == what) {
                return;
            }
            std::thread::sleep(Duration::from_millis(20));
        }
        panic!("the window was never told {what:?}; it was told {:?}", self.seen());
    }
}

impl Window for Recorder {
    fn show_starting(&self) {
        self.0.lock().unwrap().push("starting".into());
    }

    fn open(&self, port: u16, url: &str, notes: Notes) {
        self.0.lock().unwrap().push(format!("open {port} {url} {notes:?}"));
    }

    fn show_failure(&self, words: &str) {
        self.0.lock().unwrap().push(format!("failure {words}"));
    }

    fn exit(&self) {
        self.0.lock().unwrap().push("exit".into());
    }
}

struct Stand {
    root: PathBuf,
    resources: PathBuf,
    data: String,
}

impl Stand {
    fn new(name: &str, plan: &str) -> Stand {
        let root = std::env::temp_dir().join(format!("canonkeeper-driver-{name}-{}", std::process::id()));
        let _ = fs::remove_dir_all(&root);
        let resources = root.join("ресурсы");
        fs::create_dir_all(&resources).unwrap();
        fs::write(resources.join("plan.txt"), plan).unwrap();
        let data = root.join("данные").to_string_lossy().into_owned();
        Stand { root, resources, data }
    }

    fn start(&self, program: &Path, stop_timeout: Duration) -> (Recorder, Handle, std::thread::JoinHandle<()>) {
        let recorder = Recorder::default();
        let config = Config {
            program: program.to_path_buf(),
            data_dir: self.data.clone(),
            resources: self.resources.to_string_lossy().into_owned(),
            system: System::Linux,
            stop_timeout,
        };
        let journal = Journal::open(&Path::new(&self.data).join("logs"), std::time::SystemTime::now());
        let (handle, thread) = run(config, recorder.clone(), journal);
        (recorder, handle, thread)
    }

    fn received(&self) -> Vec<String> {
        fs::read_to_string(self.resources.join("received.txt"))
            .unwrap_or_default()
            .lines()
            .map(str::to_string)
            .collect()
    }

    fn journal(&self) -> String {
        let logs = Path::new(&self.data).join("logs");
        fs::read_dir(logs).unwrap().flatten().map(|entry| fs::read_to_string(entry.path()).unwrap()).collect()
    }
}

impl Drop for Stand {
    fn drop(&mut self) {
        let _ = fs::remove_dir_all(&self.root);
    }
}

fn fake() -> &'static Path {
    Path::new(env!("CARGO_BIN_EXE_fake-backend"))
}

#[test]
fn ready_opens_the_window_with_the_code_and_a_refused_code_or_a_lost_session_brings_the_next() {
    let stand = Stand::new("open", "ready 24117 first-code-0123456789 portChanged\nserve\n");
    let (window, handle, thread) = stand.start(fake(), STOP_TIMEOUT);
    let changed = Notes { port_changed: true, restarted_after_crash: false };
    window.wait_for(&format!("open 24117 http://127.0.0.1:24117/desktop/open?n=0#c=first-code-0123456789 {changed:?}"));
    // The backend refuses a code that does not open the window with 403 (delivery/http/desktop/session.go).
    handle.refused(403);
    window.wait_for(&format!("open 24117 http://127.0.0.1:24117/desktop/open?n=1#c=next1 {:?}", Notes::default()));
    // The code opened the session, but the first page got 401: the window lost it (SH1.4, exchange.js).
    handle.refused(401);
    window.wait_for(&format!("open 24117 http://127.0.0.1:24117/desktop/open?n=2#c=next2 {:?}", Notes::default()));
    handle.close();
    thread.join().unwrap();
    assert_eq!(window.seen().last().map(String::as_str), Some("exit"));
    assert_eq!(
        stand.received(),
        vec![format!("start dataDir={}", stand.data), "open-code".into(), "open-code".into(), "shutdown".into()]
    );
    let journal = stand.journal();
    assert!(journal.contains("backend_ready port=24117 port_changed=true"), "{journal}");
    assert!(journal.contains("window_code_refused status=403"), "{journal}");
    assert!(journal.contains("window_code_refused status=401"), "{journal}");
    assert!(!journal.contains("first-code") && !journal.contains("next1"), "the log holds a code: {journal}");
    assert!(!journal.contains("данные") && !journal.contains("ресурсы"), "the log holds a path: {journal}");
}

#[test]
fn the_backend_gets_no_arguments_and_no_added_environment() {
    // The code of the window and any key go only through stdin (SH1.3, DA3.2): arguments and environment of a process
    // are seen by other users of the system.
    let stand = Stand::new("process", "ready 24117 code-0123456789abcdef\nserve\n");
    let (window, handle, thread) = stand.start(fake(), STOP_TIMEOUT);
    window.wait_for(&format!(
        "open 24117 http://127.0.0.1:24117/desktop/open?n=0#c=code-0123456789abcdef {:?}",
        Notes::default()
    ));
    handle.close();
    thread.join().unwrap();
    let process: serde_json::Value =
        serde_json::from_str(&fs::read_to_string(stand.resources.join("process.json")).unwrap()).unwrap();
    assert_eq!(process["args"], serde_json::json!([]), "the backend got arguments");
    let ours: serde_json::Map<String, serde_json::Value> = std::env::vars_os()
        .map(|(name, value)| (name.to_string_lossy().into_owned(), value.to_string_lossy().into_owned().into()))
        .collect();
    assert_eq!(
        process["env"],
        serde_json::Value::Object(ours),
        "the environment of the backend is not the shell's own"
    );
    let seen = process.to_string();
    assert!(!seen.contains("code-0123456789abcdef") && !seen.contains("данные"), "{seen}");
}

#[test]
fn three_falls_restart_the_backend_and_the_fourth_shows_the_failure() {
    let stand = Stand::new("falls", "ready 24117 code-of-a-short-life\nfatal backend\nexit 3\n");
    let (window, handle, thread) = stand.start(fake(), STOP_TIMEOUT);
    window.wait_for(&format!("failure CanonKeeper не смог запуститься. Книги на месте: {}", stand.data));
    let seen = window.seen();
    assert_eq!(seen.iter().filter(|seen| *seen == "starting").count(), 3, "{seen:?}");
    let restarted = Notes { port_changed: false, restarted_after_crash: true };
    assert_eq!(seen.iter().filter(|seen| seen.ends_with(&format!("{restarted:?}"))).count(), 3, "{seen:?}");
    assert_eq!(stand.received().iter().filter(|line| line.starts_with("start ")).count(), 4);
    handle.close();
    thread.join().unwrap();
    assert_eq!(window.seen().last().map(String::as_str), Some("exit"));
}

#[test]
fn a_fatal_before_ready_shows_its_words() {
    let stand = Stand::new("fatal", "fatal migrate pre-0142-20261112T101500Z\nexit 1\n");
    let (window, handle, thread) = stand.start(fake(), STOP_TIMEOUT);
    window.wait_for(&format!(
        "failure Обновление не применилось: данные целы, копия — {}/backups/pre-0142-20261112T101500Z",
        stand.data
    ));
    handle.close();
    thread.join().unwrap();
    assert_eq!(
        window.seen(),
        vec![
            format!(
                "failure Обновление не применилось: данные целы, копия — {}/backups/pre-0142-20261112T101500Z",
                stand.data
            ),
            "exit".to_string(),
        ]
    );
}

#[test]
fn a_program_that_is_not_there_shows_the_words_of_the_quarantine() {
    let stand = Stand::new("missing", "");
    let (window, handle, thread) = stand.start(&stand.root.join("canonkeeper-backend"), STOP_TIMEOUT);
    window.wait_for(&format!(
        "failure CanonKeeper не смог запуститься. Возможно, антивирус поместил часть программы в карантин — проверьте его и запустите снова. Ваши книги на месте: {}",
        stand.data
    ));
    handle.close();
    thread.join().unwrap();
}

#[test]
fn shutdown_waits_for_the_backend_to_exit() {
    let stand = Stand::new("linger", "ready 24117 code-0123456789abcdef\nserve 700\n");
    let (window, handle, thread) = stand.start(fake(), STOP_TIMEOUT);
    window.wait_for(&format!(
        "open 24117 http://127.0.0.1:24117/desktop/open?n=0#c=code-0123456789abcdef {:?}",
        Notes::default()
    ));
    let asked = Instant::now();
    handle.close();
    thread.join().unwrap();
    assert!(asked.elapsed() >= Duration::from_millis(600), "the shell did not wait: {:?}", asked.elapsed());
    assert_eq!(stand.received().last().map(String::as_str), Some("shutdown"));
}

#[test]
fn a_backend_that_does_not_stop_in_time_is_killed() {
    let stand = Stand::new("deaf", "ready 24117 code-0123456789abcdef\ndeaf\n");
    let (window, handle, thread) = stand.start(fake(), Duration::from_secs(2));
    window.wait_for(&format!(
        "open 24117 http://127.0.0.1:24117/desktop/open?n=0#c=code-0123456789abcdef {:?}",
        Notes::default()
    ));
    let asked = Instant::now();
    handle.close();
    thread.join().unwrap();
    assert!(asked.elapsed() >= Duration::from_secs(2), "{:?}", asked.elapsed());
    let received = stand.received();
    assert!(received.contains(&"shutdown".to_string()), "{received:?}");
    let pid = received.iter().find_map(|line| line.strip_prefix("pid ")).unwrap().to_string();
    let deadline = Instant::now() + Duration::from_secs(5);
    while Path::new(&format!("/proc/{pid}")).exists() && Instant::now() < deadline {
        // A killed child stays a zombie until reaped; the status file says which.
        let status = fs::read_to_string(format!("/proc/{pid}/status")).unwrap_or_default();
        if status.lines().any(|line| line.starts_with("State:") && line.contains('Z')) {
            break;
        }
        std::thread::sleep(Duration::from_millis(50));
    }
    let status = fs::read_to_string(format!("/proc/{pid}/status")).unwrap_or_default();
    assert!(
        status.is_empty() || status.lines().any(|line| line.starts_with("State:") && line.contains('Z')),
        "{status}"
    );
    assert!(stand.journal().contains("backend_killed"));
}
