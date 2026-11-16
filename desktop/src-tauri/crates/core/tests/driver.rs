// SPDX-License-Identifier: AGPL-3.0-only

//! The supervision on a stand-in backend process (specs/2026-11-12-desk-shell.md SH1.4, SH2.2, SH2.3): the real
//! threads, pipes and exits, a window that records what it was told.

use std::collections::HashMap;
use std::fs;
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Condvar, Mutex};
use std::time::{Duration, Instant};

use canonkeeper_desktop_core::driver::{run, Config, Handle, Window};
use canonkeeper_desktop_core::erase::{self, Earlier, Targets, Unfinished};
use canonkeeper_desktop_core::folder;
use canonkeeper_desktop_core::journal::Journal;
use canonkeeper_desktop_core::link::{Argument, Target};
use canonkeeper_desktop_core::paths::{Places, System};
use canonkeeper_desktop_core::secrets::{Failure, Keyring, OWN_AI};
use canonkeeper_desktop_core::supervisor::{Notes, STOP_TIMEOUT};
use canonkeeper_desktop_core::update::fetch::Downloaded;
use canonkeeper_desktop_core::update::state::Cause;
use canonkeeper_desktop_core::update::{Kind, Setup};
use canonkeeper_desktop_core::words::{self, Act};

/// A system without a store of secrets: the shell keeps them in the file of the data directory.
struct NoStore;

impl Keyring for NoStore {
    fn set(&self, _: &str, _: &str) -> Result<(), Failure> {
        Err(Failure)
    }
    fn get(&self, _: &str) -> Result<Option<String>, Failure> {
        Err(Failure)
    }
    fn delete(&self, _: &str) -> Result<(), Failure> {
        Err(Failure)
    }
}

/// A store of the system that works, in memory; clones share it.
#[derive(Clone, Default)]
struct Memory(Arc<Mutex<HashMap<String, String>>>);

impl Keyring for Memory {
    fn set(&self, id: &str, value: &str) -> Result<(), Failure> {
        self.0.lock().unwrap().insert(id.to_string(), value.to_string());
        Ok(())
    }
    fn get(&self, id: &str) -> Result<Option<String>, Failure> {
        Ok(self.0.lock().unwrap().get(id).cloned())
    }
    fn delete(&self, id: &str) -> Result<(), Failure> {
        self.0.lock().unwrap().remove(id);
        Ok(())
    }
}

/// A store of the system that works, but a record of the set it gives only once let go: a locked keyring that asks the
/// author to unlock it before a read.
#[derive(Clone, Default)]
struct Locked {
    memory: Memory,
    asked: Arc<AtomicBool>,
    unlocked: Arc<(Mutex<bool>, Condvar)>,
}

impl Locked {
    fn unlock(&self) {
        let (unlocked, changed) = &*self.unlocked;
        *unlocked.lock().unwrap() = true;
        changed.notify_all();
    }
}

impl Keyring for Locked {
    fn set(&self, id: &str, value: &str) -> Result<(), Failure> {
        self.memory.set(id, value)
    }
    fn get(&self, id: &str) -> Result<Option<String>, Failure> {
        if id == OWN_AI {
            self.asked.store(true, Ordering::SeqCst);
            let (unlocked, changed) = &*self.unlocked;
            let _open = changed.wait_while(unlocked.lock().unwrap(), |unlocked| !*unlocked).unwrap();
        }
        self.memory.get(id)
    }
    fn delete(&self, id: &str) -> Result<(), Failure> {
        self.memory.delete(id)
    }
}

/// A store of the system that works and takes its time to keep a record.
#[derive(Clone, Default)]
struct Slow(Memory);

impl Keyring for Slow {
    fn set(&self, id: &str, value: &str) -> Result<(), Failure> {
        if id == OWN_AI {
            std::thread::sleep(Duration::from_millis(600));
        }
        self.0.set(id, value)
    }
    fn get(&self, id: &str) -> Result<Option<String>, Failure> {
        self.0.get(id)
    }
    fn delete(&self, id: &str) -> Result<(), Failure> {
        self.0.delete(id)
    }
}

/// A store of the system that works — it takes the probe — and, while it refuses, will not remove a record of the set:
/// a keyring locked again before the erasure, a refusal of the Credential Manager.
#[derive(Clone, Default)]
struct Keeping {
    memory: Memory,
    refuses: Arc<AtomicBool>,
}

impl Keyring for Keeping {
    fn set(&self, id: &str, value: &str) -> Result<(), Failure> {
        self.memory.set(id, value)
    }
    fn get(&self, id: &str) -> Result<Option<String>, Failure> {
        self.memory.get(id)
    }
    fn delete(&self, id: &str) -> Result<(), Failure> {
        if id == OWN_AI && self.refuses.load(Ordering::SeqCst) {
            return Err(Failure);
        }
        self.memory.delete(id)
    }
}

/// What the system window that chooses a folder tells once it closes.
type Chosen = Box<dyn FnOnce(Option<PathBuf>) + Send>;

/// A window that records what it was told; an installation through it ends as its third field says; the system window
/// that chooses a folder waits in its fourth for the test to close it.
#[derive(Clone)]
struct Recorder(
    Arc<Mutex<Vec<String>>>,
    Arc<Mutex<Option<words::Failure>>>,
    Arc<Mutex<Result<(), Cause>>>,
    Arc<Mutex<Option<Chosen>>>,
);

impl Default for Recorder {
    fn default() -> Recorder {
        Recorder(Arc::default(), Arc::default(), Arc::new(Mutex::new(Ok(()))), Arc::default())
    }
}

impl Recorder {
    fn seen(&self) -> Vec<String> {
        self.0.lock().unwrap().clone()
    }

    /// The failure the window shows last.
    fn failure(&self) -> Option<words::Failure> {
        self.1.lock().unwrap().clone()
    }

    /// The author closes the system window that chooses a folder: a folder, or nothing — he canceled.
    fn choose(&self, folder: Option<&Path>) {
        let chosen = self.3.lock().unwrap().take().expect("the system window is open");
        chosen(folder.map(Path::to_path_buf));
    }

    /// Waits for the window of a failure and gives its words.
    fn wait_failure(&self) -> String {
        let deadline = Instant::now() + Duration::from_secs(20);
        while Instant::now() < deadline {
            if let Some(failure) = self.seen().into_iter().find(|seen| seen.starts_with("failure ")) {
                return failure;
            }
            std::thread::sleep(Duration::from_millis(20));
        }
        panic!("the window showed no failure; it was told {:?}", self.seen());
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

    fn show_step(&self, words: &str) {
        self.0.lock().unwrap().push(format!("step {words}"));
    }

    fn open(&self, port: u16, url: &str, notes: Notes, _opening: u64) {
        self.0.lock().unwrap().push(format!("open {port} {url} {notes:?}"));
    }

    fn show_failure(&self, failure: &words::Failure) {
        let words = &failure.page.words;
        if failure.page.buttons.iter().map(|button| button.act).eq([Act::Close]) {
            self.0.lock().unwrap().push(format!("failure {words} [Закрыть]"));
        } else {
            self.0.lock().unwrap().push(format!("failure {words}"));
        }
        *self.1.lock().unwrap() = Some(failure.clone());
    }

    fn close(&self) {
        self.0.lock().unwrap().push("close".into());
    }

    fn leave(&self, words: &str) {
        self.0.lock().unwrap().push(format!("leave {words}"));
    }

    fn install(&self, file: &Downloaded, kind: Kind) -> Result<(), Cause> {
        self.0.lock().unwrap().push(format!("install {} {}", kind.name(), file.release.version));
        *self.2.lock().unwrap()
    }

    fn restart(&self) {
        self.0.lock().unwrap().push("restart".into());
    }

    fn focus(&self) {
        self.0.lock().unwrap().push("focus".into());
    }

    fn visit(&self, target: &Target) {
        self.0.lock().unwrap().push(format!("visit {}", target.path()));
    }

    fn folder(&self, saga: &str, chosen: Chosen) {
        self.0.lock().unwrap().push(format!("folder {saga}"));
        *self.3.lock().unwrap() = Some(chosen);
    }

    fn exit(&self) {
        self.0.lock().unwrap().push("exit".into());
    }
}

struct Stand {
    root: PathBuf,
    resources: PathBuf,
    data: String,
    /// What «Стереть все данные» removes on this stand: the places of the user are directories of the stand.
    targets: Targets,
    patience: Duration,
    earlier: Earlier,
    /// The log of the shell lies in the data directory, as in the app; a stand without it shows a data directory with
    /// nothing of the app.
    journal: bool,
    /// The updates of the app on this stand, none by default.
    updates: Option<Setup>,
    /// The link of the first start, none by default (specs/2026-11-16-desk-app-links.md AL1.2).
    link: Option<Argument>,
}

impl Stand {
    fn new(name: &str, plan: &str) -> Stand {
        let mut stand = Stand::erasing(name, plan);
        stand.data = stand.root.join("данные").to_string_lossy().into_owned();
        stand
    }

    /// A stand whose data directory is the one the places of the stand name, as the shell computes it.
    fn erasing(name: &str, plan: &str) -> Stand {
        let root = std::env::temp_dir().join(format!("canonkeeper-driver-{name}-{}", std::process::id()));
        let _ = fs::remove_dir_all(&root);
        let resources = root.join("ресурсы");
        fs::create_dir_all(&resources).unwrap();
        fs::write(resources.join("plan.txt"), plan).unwrap();
        let text = |path: PathBuf| path.to_string_lossy().into_owned();
        let (data_home, cache_home, home) =
            (text(root.join("xdg-data")), text(root.join("xdg-cache")), text(root.join("дом")));
        let places = Places {
            local_app_data: Some(&data_home),
            xdg_data_home: Some(&data_home),
            xdg_cache_home: Some(&cache_home),
            home: Some(&home),
        };
        let targets = erase::targets(System::CURRENT, places).unwrap();
        let data = targets.data_dir().to_string();
        Stand {
            root,
            resources,
            data,
            targets,
            patience: Duration::from_millis(400),
            earlier: Earlier::None,
            journal: true,
            updates: None,
            link: None,
        }
    }

    fn start(&self, program: &Path, stop_timeout: Duration) -> (Recorder, Handle, std::thread::JoinHandle<()>) {
        self.start_with(program, stop_timeout, Arc::new(NoStore))
    }

    fn start_with(
        &self,
        program: &Path,
        stop_timeout: Duration,
        keyring: Arc<dyn Keyring>,
    ) -> (Recorder, Handle, std::thread::JoinHandle<()>) {
        let recorder = Recorder::default();
        let config = Config {
            program: program.to_path_buf(),
            data_dir: self.data.clone(),
            resources: self.resources.to_string_lossy().into_owned(),
            version: "1.3.0".to_string(),
            system: System::Linux,
            stop_timeout,
            erase: self.targets.clone(),
            patience: self.patience,
            earlier: self.earlier.clone(),
            updates: self.updates.clone(),
            bridge: None,
            link: self.link.clone(),
        };
        let journal = if self.journal {
            Journal::open(&Path::new(&self.data).join("logs"), std::time::SystemTime::now())
        } else {
            Journal::default()
        };
        let (handle, thread) = run(config, recorder.clone(), journal, keyring);
        (recorder, handle, thread)
    }

    /// A new plan for the next start of the backend.
    fn plan(&self, plan: &str) {
        fs::write(self.resources.join("plan.txt"), plan).unwrap();
    }

    /// The `start` the backend got last.
    fn start_message(&self) -> serde_json::Value {
        serde_json::from_str(&fs::read_to_string(self.resources.join("start.json")).unwrap()).unwrap()
    }

    /// Waits until the backend heard `line`.
    fn wait_received(&self, line: &str) {
        let deadline = Instant::now() + Duration::from_secs(20);
        while Instant::now() < deadline {
            if self.received().iter().any(|received| received == line) {
                return;
            }
            std::thread::sleep(Duration::from_millis(20));
        }
        panic!("the backend never heard {line:?}; it heard {:?}", self.received());
    }

    fn received(&self) -> Vec<String> {
        fs::read_to_string(self.resources.join("received.txt"))
            .unwrap_or_default()
            .lines()
            .map(str::to_string)
            .collect()
    }

    /// Every `start` the backend got, in order.
    fn starts(&self) -> Vec<serde_json::Value> {
        fs::read_to_string(self.resources.join("starts.jsonl"))
            .unwrap_or_default()
            .lines()
            .map(|line| serde_json::from_str(line).unwrap())
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
    let changed = Notes { port_changed: true, restarted_after_crash: false, updated_from: None, ..Notes::default() };
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
fn the_backend_raises_the_window_after_a_link() {
    // specs/2026-11-13-desk-account.md AC1.3: the author confirmed the link in the browser; the window of the app comes up.
    let stand = Stand::new("focus", "ready 24118 code-of-focus-0123456789\nsay {\"type\":\"focus\"}\nserve\n");
    let (window, handle, thread) = stand.start(fake(), STOP_TIMEOUT);
    window.wait_for("focus");
    handle.close();
    thread.join().unwrap();
    assert!(stand.journal().contains("backend_focus"), "{}", stand.journal());
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
    // specs/2026-11-13-desk-upgrade.md UG2.1: start names the version of the shell; a start that brings nothing back
    // has no mark of a return.
    let start = stand.start_message();
    assert_eq!(start["version"], "1.3.0");
    assert!(start.get("restore").is_none(), "{start}");
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
    let restarted = Notes { port_changed: false, restarted_after_crash: true, updated_from: None, ..Notes::default() };
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
    // specs/2026-11-13-desk-upgrade.md UG3.3: without the version before the update the words name no version of it.
    let words = format!(
        "failure Обновление до версии 1.3.0 не применилось: данные целы. Установите прежнюю версию CanonKeeper — она откроет их как раньше. Копия данных: {}/backups/pre-0142-20261112T101500Z",
        stand.data
    );
    window.wait_for(&words);
    handle.close();
    thread.join().unwrap();
    assert_eq!(window.seen(), vec![words, "exit".to_string()]);
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
    let pid: u32 = received.iter().find_map(|line| line.strip_prefix("pid ")).unwrap().parse().unwrap();
    let deadline = Instant::now() + Duration::from_secs(5);
    while !gone(pid) && Instant::now() < deadline {
        std::thread::sleep(Duration::from_millis(50));
    }
    assert!(gone(pid), "the killed backend {pid} still runs");
    assert!(stand.journal().contains("backend_killed"));
}

/// Whether the process `pid` is gone, as the system tells it: on Linux there is no process of the number, or a zombie
/// that only waits to be reaped (`/proc/<pid>/status`).
#[cfg(not(windows))]
fn gone(pid: u32) -> bool {
    let status = fs::read_to_string(format!("/proc/{pid}/status")).unwrap_or_default();
    status.is_empty() || status.lines().any(|line| line.starts_with("State:") && line.contains('Z'))
}

/// Whether the process `pid` is gone, as the system tells it: on Windows there is no process of the number, or its object
/// is signaled — it ended, and lives on only while a handle holds it. Windows has no `/proc`.
#[cfg(windows)]
fn gone(pid: u32) -> bool {
    use windows_sys::Win32::Foundation::{CloseHandle, GetLastError, ERROR_INVALID_PARAMETER, WAIT_OBJECT_0};
    use windows_sys::Win32::System::Threading::{OpenProcess, WaitForSingleObject, PROCESS_SYNCHRONIZE};
    // SAFETY: the handle is closed before it goes out of scope; no buffer is lent.
    unsafe {
        let process = OpenProcess(PROCESS_SYNCHRONIZE, 0, pid);
        if process.is_null() {
            // No process of this number; a refusal of another kind says nothing of it.
            return GetLastError() == ERROR_INVALID_PARAMETER;
        }
        let ended = WaitForSingleObject(process, 0) == WAIT_OBJECT_0;
        CloseHandle(process);
        ended
    }
}

/// The key of the tests: it must not show in the log of the shell, its process or anywhere but the store.
const KEY: &str = "key-test-key-of-the-north-pass-7f3a";

#[test]
fn a_secret_the_backend_asks_for_is_kept_answered_and_comes_with_the_next_start() {
    // specs/2026-11-13-desk-secrets.md SE1.3, SE1.4, SE1.5: without a store of the system the file of the data
    // directory keeps the record; the backend hears secret-result; the next start carries the record and the store.
    let stand = Stand::new(
        "secret",
        &format!(
            "ready 24117 code-0123456789abcdef\nsecret-save provider:own {KEY} s1\nsecret-save provider:other {KEY} s2\nserve\n"
        ),
    );
    let (window, handle, thread) = stand.start(fake(), STOP_TIMEOUT);
    stand.wait_received("secret-result s1 true");
    stand.wait_received("secret-result s2 false");
    let first = stand.start_message();
    assert_eq!(first["secrets"], serde_json::json!({}));
    assert_eq!(first["secretsStore"], "file");
    handle.close();
    thread.join().unwrap();
    assert_eq!(window.seen().last().map(String::as_str), Some("exit"));

    let file = Path::new(&stand.data).join("secrets.json");
    let kept: serde_json::Value = serde_json::from_str(&fs::read_to_string(&file).unwrap()).unwrap();
    assert_eq!(kept, serde_json::json!({"provider:own": KEY}));
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        assert_eq!(fs::metadata(&file).unwrap().permissions().mode() & 0o777, 0o600);
    }

    stand.plan("ready 24117 code-0123456789abcdef\nsecret-delete provider:own s3\nserve\n");
    let (_window, handle, thread) = stand.start(fake(), STOP_TIMEOUT);
    stand.wait_received("secret-result s3 true");
    let second = stand.start_message();
    assert_eq!(second["secrets"], serde_json::json!({"provider:own": KEY}));
    assert_eq!(second["secretsStore"], "file");
    handle.close();
    thread.join().unwrap();
    assert!(!file.exists(), "the deleted record stays in the file");

    let journal = stand.journal();
    assert!(journal.contains("secrets_store store=file"), "{journal}");
    assert!(journal.contains("secret_save id=provider:own"), "{journal}");
    assert!(journal.contains("secret_save_refused id=other"), "{journal}");
    assert!(journal.contains("secret_delete id=provider:own"), "{journal}");
    assert!(!journal.contains(KEY), "the log of the shell holds the key: {journal}");
    let process = fs::read_to_string(stand.resources.join("process.json")).unwrap();
    assert!(!process.contains(KEY), "the arguments or the environment of the backend hold the key");
}

#[test]
fn a_store_of_the_system_that_works_takes_the_records_of_an_earlier_file() {
    // SE1.1, SE1.3: the store of the system works, the file of an earlier run goes into it and is removed; a record the
    // backend asks for goes into the store, not into a file.
    let stand = Stand::new("system", "ready 24117 code-0123456789abcdef\nserve\n");
    fs::create_dir_all(&stand.data).unwrap();
    let file = Path::new(&stand.data).join("secrets.json");
    fs::write(&file, serde_json::json!({"provider:own": KEY}).to_string()).unwrap();
    let store = Memory::default();
    let (_window, handle, thread) = stand.start_with(fake(), STOP_TIMEOUT, Arc::new(store.clone()));
    stand.wait_received(&format!("start dataDir={}", stand.data));
    let start = stand.start_message();
    assert_eq!(start["secrets"], serde_json::json!({"provider:own": KEY}));
    assert_eq!(start["secretsStore"], "system");
    assert!(!file.exists(), "the file of the earlier run stays");
    handle.close();
    thread.join().unwrap();

    stand.plan("ready 24117 code-0123456789abcdef\nsecret-save provider:own key-test-next s4\nserve\n");
    let (_window, handle, thread) = stand.start_with(fake(), STOP_TIMEOUT, Arc::new(store.clone()));
    stand.wait_received("secret-result s4 true");
    handle.close();
    thread.join().unwrap();
    assert_eq!(store.0.lock().unwrap().get("provider:own").map(String::as_str), Some("key-test-next"));
    assert!(!file.exists(), "a record of a working store went into a file");
    let journal = stand.journal();
    assert!(journal.contains("secrets_store store=system") && journal.contains("secrets_moved"), "{journal}");
    assert!(!journal.contains(KEY) && !journal.contains("key-test-next"), "{journal}");
}

#[test]
fn a_store_that_waits_to_be_unlocked_does_not_hold_up_the_supervision() {
    // specs/2026-11-13-desk-secrets.md, Decision log: the store of the system is the keeper's alone. A locked keyring asks
    // the author to unlock it before a read of the records for start; meanwhile the app still closes when asked.
    let stand = Stand::new("locked", "ready 24117 code-0123456789abcdef\nserve\n");
    let store = Locked::default();
    let (window, handle, thread) = stand.start_with(fake(), STOP_TIMEOUT, Arc::new(store.clone()));
    let deadline = Instant::now() + Duration::from_secs(20);
    while !store.asked.load(Ordering::SeqCst) && Instant::now() < deadline {
        std::thread::sleep(Duration::from_millis(20));
    }
    assert!(store.asked.load(Ordering::SeqCst), "the records for start were never read");
    handle.close();
    let asked = Instant::now();
    while !thread.is_finished() && asked.elapsed() < Duration::from_secs(5) {
        std::thread::sleep(Duration::from_millis(20));
    }
    let closed = thread.is_finished();
    store.unlock();
    thread.join().unwrap();
    assert!(closed, "the app did not close while the store waited to be unlocked");
    assert_eq!(window.seen(), vec!["exit".to_string()]);
    assert!(stand.received().is_empty(), "a backend started after the app was closed: {:?}", stand.received());
}

#[test]
fn a_record_the_backend_before_asked_for_comes_with_the_next_start() {
    // SE1.3, SE1.4: the backend asks to keep a record and falls while the store is keeping it; the supervision restarts
    // it, and the next start is read after the record is kept — the records of a start follow the order asked.
    let stand =
        Stand::new("order", &format!("ready 24117 code-0123456789abcdef\nsecret-save provider:own {KEY} s1\nexit 3\n"));
    let store = Slow::default();
    let (window, handle, thread) = stand.start_with(fake(), STOP_TIMEOUT, Arc::new(store.clone()));
    // Every run falls: after the third restart the failure is shown.
    let deadline = Instant::now() + Duration::from_secs(30);
    while !window.seen().iter().any(|seen| seen.starts_with("failure ")) && Instant::now() < deadline {
        std::thread::sleep(Duration::from_millis(20));
    }
    handle.close();
    thread.join().unwrap();
    let starts = stand.starts();
    assert_eq!(starts.len(), 4, "{starts:?}");
    assert_eq!(starts[0]["secrets"], serde_json::json!({}));
    for (launch, start) in starts.iter().enumerate().skip(1) {
        assert_eq!(
            start["secrets"],
            serde_json::json!({"provider:own": KEY}),
            "start {} came before the record",
            launch + 1
        );
    }
    let journal = stand.journal();
    assert!(!journal.contains(KEY), "{journal}");
}

/// What an app leaves on the computer of the stand: a saga in the data directory with its copy before an update, the
/// settings and the log, the data of the window and its cache — and what is not the app's: a file of a saga the author
/// saved at home and the directory of another program beside the data.
fn a_computer_with_a_saga(stand: &Stand) -> Vec<PathBuf> {
    let data = Path::new(&stand.data);
    for (file, text) in [
        ("pg/base/16384/1259", "строки саги"),
        ("backups/pre-0142-20261112T101500Z/pg/base/1", "копия"),
        ("settings.json", r#"{"port":24117}"#),
        ("codex/sqlite/state_5.sqlite", "нить чата"),
        ("logs/backend-2026-11-13.log", "{}"),
    ] {
        let path = data.join(file);
        fs::create_dir_all(path.parent().unwrap()).unwrap();
        fs::write(path, text).unwrap();
    }
    for directory in &stand.targets.directories()[1..] {
        let path = Path::new(directory).join("storage").join("localstorage.sqlite3");
        fs::create_dir_all(path.parent().unwrap()).unwrap();
        fs::write(path, "Фон текста").unwrap();
    }
    let saved = stand.root.join("дом").join("Документы").join("Северный предел.ck");
    let other = stand.root.join("xdg-data").join("other-app").join("notes.md");
    for path in [&saved, &other] {
        fs::create_dir_all(path.parent().unwrap()).unwrap();
        fs::write(path, "чужое").unwrap();
    }
    vec![saved, other]
}

fn nothing_of_the_app_is_left(stand: &Stand) {
    for directory in stand.targets.directories() {
        assert!(!Path::new(directory).exists(), "{directory} is left");
    }
    assert!(!Path::new(stand.targets.mark()).exists(), "the mark is left");
}

#[test]
fn erase_stops_the_backend_closes_the_window_and_leaves_nothing_of_the_app() {
    // specs/2026-11-13-desk-erase.md ER1.3, ER1.6: the store of the system works and holds the key.
    let stand = Stand::erasing("erase", "ready 24117 code-0123456789abcdef\nerase\nserve\n");
    let foreign = a_computer_with_a_saga(&stand);
    let store = Memory::default();
    store.set(OWN_AI, KEY).unwrap();
    let (window, _handle, thread) = stand.start_with(fake(), STOP_TIMEOUT, Arc::new(store.clone()));
    thread.join().unwrap();
    let seen = window.seen();
    assert_eq!(seen[seen.len() - 2..], ["close".to_string(), "exit".to_string()], "{seen:?}");
    assert_eq!(stand.received(), vec![format!("start dataDir={}", stand.data), "shutdown".into()]);
    nothing_of_the_app_is_left(&stand);
    assert!(store.0.lock().unwrap().is_empty(), "the store of the system holds {:?}", store.0.lock().unwrap().keys());
    for path in foreign {
        assert_eq!(fs::read_to_string(&path).unwrap(), "чужое", "{}", path.display());
    }

    // Without a store of the system the file of the data directory held the key: it goes with the rest.
    let stand = Stand::erasing("erase-file", "ready 24117 code-0123456789abcdef\nerase\nserve\n");
    a_computer_with_a_saga(&stand);
    fs::write(Path::new(&stand.data).join("secrets.json"), serde_json::json!({"provider:own": KEY}).to_string())
        .unwrap();
    let (window, _handle, thread) = stand.start(fake(), STOP_TIMEOUT);
    thread.join().unwrap();
    assert_eq!(window.seen().last().map(String::as_str), Some("exit"));
    nothing_of_the_app_is_left(&stand);
}

#[test]
fn a_backend_that_falls_after_erase_is_not_started_again() {
    // ER1.3: the backend falls instead of stopping; the shell erases and ends, it does not start the backend again.
    let stand = Stand::erasing("erase-fall", "ready 24117 code-0123456789abcdef\nerase\nexit 3\n");
    a_computer_with_a_saga(&stand);
    let (window, _handle, thread) = stand.start(fake(), STOP_TIMEOUT);
    thread.join().unwrap();
    assert_eq!(stand.starts().len(), 1, "the backend was started again");
    assert!(!window.seen().iter().any(|seen| seen == "starting"), "{:?}", window.seen());
    assert_eq!(window.seen().last().map(String::as_str), Some("exit"));
    nothing_of_the_app_is_left(&stand);
}

#[test]
fn a_backend_that_does_not_stop_for_erase_is_killed_and_the_erasure_goes_on() {
    // ER1.3: the backend does not stop within the time of a stop; it is killed, as at the exit, and the erasure goes on.
    let stand = Stand::erasing("erase-deaf", "ready 24117 code-0123456789abcdef\nerase\ndeaf\n");
    a_computer_with_a_saga(&stand);
    let asked = Instant::now();
    let (window, _handle, thread) = stand.start(fake(), Duration::from_secs(2));
    thread.join().unwrap();
    assert!(asked.elapsed() >= Duration::from_secs(2), "{:?}", asked.elapsed());
    let seen = window.seen();
    assert_eq!(seen[seen.len() - 2..], ["close".to_string(), "exit".to_string()], "{seen:?}");
    assert!(stand.received().contains(&"shutdown".to_string()), "{:?}", stand.received());
    nothing_of_the_app_is_left(&stand);
}

#[test]
fn closing_the_app_erases_nothing() {
    // ER1.6: erase comes only from the channel of the backend; closing the app is no erasure.
    let stand = Stand::erasing("erase-close", "ready 24117 code-0123456789abcdef\nserve\n");
    a_computer_with_a_saga(&stand);
    let (window, handle, thread) = stand.start(fake(), STOP_TIMEOUT);
    window.wait_for(&format!(
        "open 24117 http://127.0.0.1:24117/desktop/open?n=0#c=code-0123456789abcdef {:?}",
        Notes::default()
    ));
    handle.close();
    thread.join().unwrap();
    assert!(!window.seen().contains(&"close".to_string()), "{:?}", window.seen());
    assert!(Path::new(&stand.data).join("pg").exists() && !Path::new(stand.targets.mark()).exists());
}

/// A file of the data directory another program holds: on Linux its directory takes no change, on Windows the file is
/// open without sharing its deletion. Dropped, it lets go.
struct Held {
    #[cfg(unix)]
    directory: PathBuf,
    #[cfg(windows)]
    _file: fs::File,
}

impl Held {
    fn file(path: &Path) -> Held {
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            let directory = path.parent().unwrap().to_path_buf();
            fs::set_permissions(&directory, fs::Permissions::from_mode(0o500)).unwrap();
            Held { directory }
        }
        #[cfg(windows)]
        {
            use std::os::windows::fs::OpenOptionsExt;
            let file = fs::OpenOptions::new().read(true).share_mode(0).open(path).unwrap();
            Held { _file: file }
        }
    }
}

impl Drop for Held {
    fn drop(&mut self) {
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            let _ = fs::set_permissions(&self.directory, fs::Permissions::from_mode(0o700));
        }
    }
}

#[test]
fn an_erasure_a_held_file_stopped_ends_at_the_next_start_or_says_why_and_starts_nothing() {
    // ER1.5: the removal fails on one file: the mark stays; the next start ends the erasure before the backend and starts
    // on an empty data directory; a second failure is the window of the start with its words, and no backend.
    let mut stand = Stand::erasing("erase-held", "ready 24117 code-0123456789abcdef\nerase\nserve\n");
    a_computer_with_a_saga(&stand);
    let held = Held::file(&Path::new(&stand.data).join("pg").join("base").join("16384").join("1259"));
    let store = Memory::default();
    store.set(OWN_AI, KEY).unwrap();
    let (window, _handle, thread) = stand.start_with(fake(), STOP_TIMEOUT, Arc::new(store.clone()));
    thread.join().unwrap();
    assert_eq!(window.seen().last().map(String::as_str), Some("exit"));
    assert!(Path::new(stand.targets.mark()).exists(), "the mark of an unfinished erasure is gone");
    assert!(store.0.lock().unwrap().is_empty(), "the records of the set go first, before the directories");

    // The next start, the file still held.
    stand.earlier = erase::finish(&stand.targets, Arc::new(store.clone()), cfg!(windows), stand.patience);
    assert_eq!(stand.earlier, Earlier::Unended(Unfinished::Held { folder: stand.data.clone() }));
    let (window, handle, thread) = stand.start_with(fake(), STOP_TIMEOUT, Arc::new(store.clone()));
    window.wait_for(&format!(
        "failure Не удалось стереть данные: файлы в папке {} заняты другой программой. Закройте её и запустите CanonKeeper снова [Закрыть]",
        stand.data
    ));
    handle.close();
    thread.join().unwrap();
    assert_eq!(window.seen().last().map(String::as_str), Some("exit"));
    assert_eq!(stand.starts().len(), 1, "a backend started on data half erased");
    assert!(Path::new(stand.targets.mark()).exists());

    // The program let go of the file: the next start ends the erasure and starts as the first one.
    drop(held);
    stand.earlier = erase::finish(&stand.targets, Arc::new(store.clone()), cfg!(windows), stand.patience);
    assert_eq!(stand.earlier, Earlier::Ended);
    nothing_of_the_app_is_left(&stand);
    stand.plan("ready 24117 code-0123456789abcdef\nserve\n");
    let (window, handle, thread) = stand.start_with(fake(), STOP_TIMEOUT, Arc::new(store.clone()));
    window.wait_for(&format!(
        "open 24117 http://127.0.0.1:24117/desktop/open?n=0#c=code-0123456789abcdef {:?}",
        Notes::default()
    ));
    handle.close();
    thread.join().unwrap();
    assert_eq!(stand.starts().len(), 2);
    let left: Vec<String> = fs::read_dir(&stand.data)
        .unwrap()
        .flatten()
        .map(|entry| entry.file_name().to_string_lossy().into_owned())
        .collect();
    assert_eq!(left, vec!["logs".to_string()], "the data directory of the new start holds more than its log");
    let journal = stand.journal();
    assert!(journal.contains("erase_finished"), "{journal}");
    assert!(!journal.contains(&stand.data) && !journal.contains(KEY), "{journal}");
}

#[test]
fn a_record_the_store_of_the_system_keeps_leaves_the_erasure_to_the_next_start() {
    // ER1.3, ER1.5 and the Decision log of the spec: a store of the system that works must remove every record of the
    // set. One it kept leaves the erasure unended: the directories go, the mark stays; the next start asks the store
    // again and starts no backend while it keeps the record, and ends the erasure once it lets go.
    let mut stand = Stand::erasing("erase-kept", "ready 24117 code-0123456789abcdef\nerase\nserve\n");
    a_computer_with_a_saga(&stand);
    let store = Keeping::default();
    store.refuses.store(true, Ordering::SeqCst);
    store.set(OWN_AI, KEY).unwrap();
    let (window, _handle, thread) = stand.start_with(fake(), STOP_TIMEOUT, Arc::new(store.clone()));
    thread.join().unwrap();
    let seen = window.seen();
    assert_eq!(seen[seen.len() - 2..], ["close".to_string(), "exit".to_string()], "{seen:?}");
    for directory in stand.targets.directories() {
        assert!(!Path::new(directory).exists(), "{directory} is left");
    }
    assert!(Path::new(stand.targets.mark()).exists(), "an erasure the store of the system did not end lost its mark");
    assert_eq!(store.get(OWN_AI), Ok(Some(KEY.to_string())));

    // The next start, the store still keeps the record: the window says so, and no backend starts.
    stand.earlier = erase::finish(&stand.targets, Arc::new(store.clone()), cfg!(windows), stand.patience);
    assert_eq!(stand.earlier, Earlier::Unended(Unfinished::Kept));
    let (window, handle, thread) = stand.start_with(fake(), STOP_TIMEOUT, Arc::new(store.clone()));
    window.wait_for(
        "failure Данные стёрты, но записи CanonKeeper остались в хранилище паролей системы. Удалите их там или запустите CanonKeeper снова — он попробует ещё раз [Закрыть]",
    );
    handle.close();
    thread.join().unwrap();
    assert_eq!(stand.starts().len(), 1, "a backend started while the store kept a record");
    assert!(Path::new(stand.targets.mark()).exists());

    // The store lets go of the record: the next start ends the erasure.
    store.refuses.store(false, Ordering::SeqCst);
    stand.earlier = erase::finish(&stand.targets, Arc::new(store.clone()), cfg!(windows), stand.patience);
    assert_eq!(stand.earlier, Earlier::Ended);
    nothing_of_the_app_is_left(&stand);
    assert_eq!(store.get(OWN_AI), Ok(None));
}

#[cfg(unix)]
#[test]
fn an_erasure_that_cannot_be_marked_does_not_begin() {
    // Decision log of the spec: without its mark an erasure that stops halfway would leave half the data unmarked; it
    // does not begin, the backend is stopped, the window says so, and the app ends with the data in place.
    use std::os::unix::fs::PermissionsExt;
    let stand = Stand::erasing("erase-unmarked", "ready 24117 code-0123456789abcdef\nerase\nserve\n");
    a_computer_with_a_saga(&stand);
    let home = Path::new(stand.targets.mark()).parent().unwrap().to_path_buf();
    fs::set_permissions(&home, fs::Permissions::from_mode(0o500)).unwrap();
    let (window, handle, thread) = stand.start(fake(), STOP_TIMEOUT);
    // The author reads why before the app ends: the window stays, with the folder of the data.
    window.wait_for(&format!(
        "failure Стирание не началось. Книги на месте: {}. Запустите CanonKeeper и попробуйте ещё раз",
        stand.data
    ));
    // specs/2026-11-16-desk-app-links.md AL1.7: a link while this window waits for «Закрыть» is dropped by its reason.
    handle.link(link_to(1));
    handle.close();
    thread.join().unwrap();
    fs::set_permissions(&home, fs::Permissions::from_mode(0o700)).unwrap();
    assert!(stand.journal().contains("link_dropped failure"), "{}", stand.journal());
    let seen = window.seen();
    assert_eq!(seen.last().map(String::as_str), Some("exit"));
    assert!(!seen.contains(&"close".to_string()), "the window went before the author read why: {seen:?}");
    assert_eq!(stand.received().last().map(String::as_str), Some("shutdown"));
    assert!(Path::new(&stand.data).join("pg").join("base").join("16384").join("1259").exists());
    assert!(!Path::new(stand.targets.mark()).exists());
    let journal = stand.journal();
    assert!(journal.contains("erase_requested") && journal.contains("erase_not_marked"), "{journal}");
}

#[test]
fn an_erasure_whose_directories_the_removal_refuses_does_not_begin_and_says_so() {
    // ER1.4, ER1.5: a place of the user written with `..` names directories the removal does not take. Marked, the
    // erasure would never end and the app would not start again; it is not marked, the data stay, the window says so.
    let mut stand = Stand::erasing("erase-climb", "ready 24117 code-0123456789abcdef\nerase\nserve\n");
    let text = |path: PathBuf| path.to_string_lossy().into_owned();
    // The directory `..` climbs from is there: the system reads the place as the data home of the stand.
    fs::create_dir_all(stand.root.join("xdg-data").join("x")).unwrap();
    let climbing = text(stand.root.join("xdg-data").join("x").join(".."));
    let (cache_home, home) = (text(stand.root.join("xdg-cache")), text(stand.root.join("дом")));
    let places = Places {
        local_app_data: Some(&climbing),
        xdg_data_home: Some(&climbing),
        xdg_cache_home: Some(&cache_home),
        home: Some(&home),
    };
    stand.targets = erase::targets(System::CURRENT, places).unwrap();
    stand.data = stand.targets.data_dir().to_string();
    a_computer_with_a_saga(&stand);
    let (window, handle, thread) = stand.start(fake(), STOP_TIMEOUT);
    window.wait_for(&format!(
        "failure Стирание не началось. Книги на месте: {}. Запустите CanonKeeper и попробуйте ещё раз",
        stand.data
    ));
    handle.close();
    thread.join().unwrap();
    assert_eq!(window.seen().last().map(String::as_str), Some("exit"));
    assert!(Path::new(&stand.data).join("pg").join("base").join("16384").join("1259").exists());
    for directory in &stand.targets.directories()[1..] {
        assert!(Path::new(directory).exists(), "{directory} went though the erasure did not begin");
    }
    assert!(!Path::new(stand.targets.mark()).exists());
}

#[test]
fn the_steps_of_the_start_and_a_fatal_with_its_data_reach_the_page_of_the_start() {
    // specs/2026-11-13-desk-upgrade.md UG3.1, UG3.2: the phases from the backend change what the page of the start shows;
    // a fatal with its data is the failure with these data — the words and the buttons of its reason.
    let stand = Stand::new(
        "steps",
        concat!(
            "say {\"type\":\"step\",\"step\":\"backup\",\"version\":\"1.3.0\"}\n",
            "say {\"type\":\"step\",\"step\":\"migrate\",\"version\":\"1.3.0\"}\n",
            "say {\"type\":\"fatal\",\"reason\":\"migrate\",\"backup\":\"pre-145-20261113T110500.000Z\",\"opened\":\"1.2.0\"}\n",
            "exit 1\n",
        ),
    );
    let (window, handle, thread) = stand.start(fake(), STOP_TIMEOUT);
    let words = format!(
        "Обновление до версии 1.3.0 не применилось: данные целы. Установите прежнюю версию 1.2.0 — она откроет их как раньше. Копия данных: {}/backups/pre-145-20261113T110500.000Z",
        stand.data
    );
    window.wait_for(&format!("failure {words}"));
    handle.close();
    thread.join().unwrap();
    assert_eq!(
        window.seen(),
        vec![
            "step Сохраняю копию данных перед обновлением до версии 1.3.0 — это может занять несколько минут"
                .to_string(),
            "step Обновляю данные до версии 1.3.0".to_string(),
            format!("failure {words}"),
            "exit".to_string(),
        ]
    );
    let labels: Vec<String> =
        window.failure().unwrap().page.buttons.iter().map(|button| button.label.clone()).collect();
    assert_eq!(
        labels,
        ["Открыть папку с данными", "Написать нам", "Закрыть", "Открыть папку с копией", "Скачать версию 1.2.0"]
    );
    let journal = stand.journal();
    assert!(journal.contains("backend_step step=backup") && journal.contains("failure migrate"), "{journal}");
}

#[test]
fn a_return_starts_only_from_the_failure_of_a_newer_schema_with_a_copy() {
    // UG4.1: the return is a start of the backend with the mark of a return, without the name of a copy; it starts only
    // from the failure of a newer schema with a candidate, once its backend is gone.
    let newer = concat!(
        "say {\"type\":\"fatal\",\"reason\":\"newer_schema\",\"opened\":\"1.3.0\",",
        "\"copy\":{\"name\":\"pre-145-20261113T110500.000Z\",\"madeAt\":\"2026-11-13T14:05:00+03:00\",\"version\":\"1.3.0\"}}\n",
        "exit 1\n",
    );
    let stand = Stand::new("restore", newer);
    let (window, handle, thread) = stand.start(fake(), STOP_TIMEOUT);
    window.wait_for(
        "failure Эти данные уже открывала версия 1.3.0, а запущена 1.3.0. Установите версию 1.3.0 или новее — данные откроются как есть",
    );
    stand.plan(concat!(
        "say {\"type\":\"step\",\"step\":\"restore\",\"copy\":{\"name\":\"pre-145-20261113T110500.000Z\",\"madeAt\":\"2026-11-13T14:05:00+03:00\"}}\n",
        "ready 24117 code-0123456789abcdef\nserve\n",
    ));
    handle.restore();
    window.wait_for(&format!(
        "open 24117 http://127.0.0.1:24117/desktop/open?n=0#c=code-0123456789abcdef {:?}",
        Notes::default()
    ));
    // An open window is no failure to return from.
    handle.restore();
    std::thread::sleep(Duration::from_millis(300));
    handle.close();
    thread.join().unwrap();
    let starts = stand.starts();
    assert_eq!(starts.len(), 2, "{starts:?}");
    assert!(starts[0].get("restore").is_none());
    assert_eq!(starts[1]["restore"], true);
    assert!(!starts[1].to_string().contains("pre-145"), "the start of a return names a copy: {}", starts[1]);
    assert!(window.seen().contains(
        &"step Возвращаю данные из копии от 13 ноября, 14:05 — это может занять несколько минут".to_string()
    ));

    // A failure of another reason, or of a newer schema without a copy: the command starts nothing.
    for plan in [
        "fatal migrate pre-145-20261113T110500.000Z\nexit 1\n",
        "say {\"type\":\"fatal\",\"reason\":\"newer_schema\",\"opened\":\"1.3.0\"}\nexit 1\n",
    ] {
        let stand = Stand::new("no-restore", plan);
        let (window, handle, thread) = stand.start(fake(), STOP_TIMEOUT);
        let deadline = Instant::now() + Duration::from_secs(20);
        while window.failure().is_none() && Instant::now() < deadline {
            std::thread::sleep(Duration::from_millis(20));
        }
        handle.restore();
        std::thread::sleep(Duration::from_millis(300));
        handle.close();
        thread.join().unwrap();
        assert_eq!(stand.starts().len(), 1, "{plan}: a return started");
    }
}

/// A `pg_ctl` of the bundle of the stand: it writes how it was called and stops — or, `failing`, does not.
#[cfg(unix)]
fn stand_pg_ctl(stand: &mut Stand, failing: bool) -> PathBuf {
    use std::os::unix::fs::PermissionsExt;
    let bin = stand.resources.join("postgres").join("bin");
    fs::create_dir_all(&bin).unwrap();
    let pg_ctl = bin.join("pg_ctl");
    let called = stand.resources.join("pg_ctl.called");
    let script = if failing {
        format!("#!/bin/sh\necho \"$@\" >> '{}'\nexit 1\n", called.display())
    } else {
        format!("#!/bin/sh\necho \"$@\" >> '{}'\n", called.display())
    };
    fs::write(&pg_ctl, script).unwrap();
    fs::set_permissions(&pg_ctl, fs::Permissions::from_mode(0o755)).unwrap();
    stand.targets = stand.targets.clone().with_pg_ctl(pg_ctl);
    called
}

#[cfg(unix)]
#[test]
fn a_cluster_a_killed_backend_left_is_stopped_before_the_data_go() {
    // specs/2026-11-13-desk-upgrade.md UG6.1: the backend does not stop for the erasure and is killed; its PostgreSQL —
    // postmaster.pid in the cluster — would hold the files: the program of the bundle stops it first, and the erasure
    // ends. The author never reads that another program holds them.
    let mut stand = Stand::erasing("erase-cluster", "ready 24117 code-0123456789abcdef\nerase\ndeaf\n");
    a_computer_with_a_saga(&stand);
    fs::write(Path::new(&stand.data).join("pg").join("postmaster.pid"), "4242\n").unwrap();
    let called = stand_pg_ctl(&mut stand, false);
    let (window, _handle, thread) = stand.start(fake(), Duration::from_secs(2));
    thread.join().unwrap();
    assert_eq!(fs::read_to_string(&called).unwrap(), format!("stop -m immediate -w -t 10 -D {}/pg\n", stand.data));
    let seen = window.seen();
    assert_eq!(seen[seen.len() - 2..], ["close".to_string(), "exit".to_string()], "{seen:?}");
    nothing_of_the_app_is_left(&stand);
}

#[cfg(unix)]
#[test]
fn a_cluster_that_does_not_stop_leaves_the_files_held_as_before() {
    // UG6.1: the stop fails and a file stays held: the erasure keeps its mark, and the next start says the files are held.
    let mut stand = Stand::erasing("erase-cluster-held", "ready 24117 code-0123456789abcdef\nerase\nserve\n");
    a_computer_with_a_saga(&stand);
    fs::write(Path::new(&stand.data).join("pg").join("postmaster.pid"), "4242\n").unwrap();
    let called = stand_pg_ctl(&mut stand, true);
    let held = Held::file(&Path::new(&stand.data).join("pg").join("base").join("16384").join("1259"));
    let (_window, _handle, thread) = stand.start(fake(), STOP_TIMEOUT);
    thread.join().unwrap();
    assert!(fs::read_to_string(&called).unwrap().starts_with("stop -m immediate"));
    assert!(Path::new(stand.targets.mark()).exists(), "the mark of an unfinished erasure is gone");
    stand.earlier = erase::finish(&stand.targets, Arc::new(NoStore), false, stand.patience);
    assert_eq!(stand.earlier, Earlier::Unended(Unfinished::Held { folder: stand.data.clone() }));
    drop(held);
    assert_eq!(erase::finish(&stand.targets, Arc::new(NoStore), false, stand.patience), Earlier::Ended);
    nothing_of_the_app_is_left(&stand);
}

#[test]
fn a_data_directory_with_nothing_of_the_app_is_not_erased_and_an_empty_one_is() {
    // UG6.2: the data directory named after the app holds the files of another program and nothing of the app; the
    // erasure does not begin, the window says so, the files stay. An empty one goes.
    let mut stand = Stand::erasing("erase-foreign", "ready 24117 code-0123456789abcdef\nerase\nserve\n");
    stand.journal = false;
    let foreign = Path::new(&stand.data).join("Книги").join("Северный предел.fb2");
    fs::create_dir_all(foreign.parent().unwrap()).unwrap();
    fs::write(&foreign, "чужое").unwrap();
    let (window, handle, thread) = stand.start(fake(), STOP_TIMEOUT);
    window.wait_for(&format!(
        "failure Стирание не началось. Книги на месте: {}. Запустите CanonKeeper и попробуйте ещё раз",
        stand.data
    ));
    handle.close();
    thread.join().unwrap();
    assert_eq!(fs::read_to_string(&foreign).unwrap(), "чужое");
    assert!(!Path::new(stand.targets.mark()).exists());

    let mut empty = Stand::erasing("erase-empty", "ready 24117 code-0123456789abcdef\nerase\nserve\n");
    empty.journal = false;
    fs::create_dir_all(&empty.data).unwrap();
    let (window, _handle, thread) = empty.start(fake(), STOP_TIMEOUT);
    thread.join().unwrap();
    assert_eq!(window.seen().last().map(String::as_str), Some("exit"));
    nothing_of_the_app_is_left(&empty);
}

/// The number of a choice of the test: 32 bytes in base64url.
const CHOICE: &str = "Y2Fub25rZWVwZXItb2JzaWRpYW4tY2hvaWNlLTAwMDA";

/// The plan of a backend that declares a choice once it is ready and answers a `folder-picked` with `answer` — or never.
fn chooses(stand: &Stand, answer: Option<&str>) {
    let declare = format!("say {{\"type\":\"folder-choice\",\"request\":\"{CHOICE}\",\"saga\":\"Северный предел\"}}");
    let answer = answer.map(|answer| format!("folder-answer {answer}\n")).unwrap_or_default();
    stand.plan(&format!("ready 24131 code-of-choice-0123456789\n{answer}{declare}\nserve\n"));
}

/// Waits until the driver heard the declaration of a choice for the `count`th time on this stand: the page may ask for
/// its window now.
fn wait_declared(stand: &Stand, count: usize) {
    let deadline = Instant::now() + Duration::from_secs(20);
    while Instant::now() < deadline {
        if stand.journal().matches("folder_choice").count() >= count {
            return;
        }
        std::thread::sleep(Duration::from_millis(20));
    }
    panic!("the driver never heard the choice; the log:\n{}", stand.journal());
}

/// What the window was told once the backend of a choice was ready.
fn opened() -> String {
    format!("open 24131 http://127.0.0.1:24131/desktop/open?n=0#c=code-of-choice-0123456789 {:?}", Notes::default())
}

/// The folder as the backend hears it: resolved, and on Windows without the prefix of a long path (`\\?\C:\…`) the
/// resolution gives.
fn as_heard(folder: &Path) -> String {
    folder::check(folder, &[], None).unwrap()
}

fn outcome(heard: &std::sync::mpsc::Receiver<folder::Outcome>) -> folder::Outcome {
    heard.recv_timeout(Duration::from_secs(20)).expect("the page hears how the choice ended")
}

#[test]
fn a_folder_the_author_picks_goes_to_the_backend_and_the_page_hears_only_its_answer() {
    // specs/2026-11-13-desk-obsidian.md OB1.2, OB1.3: the window opens for the declared number, once; the folder goes to
    // the backend, and the page hears the answer of the backend, never the path; the log names neither.
    let stand = Stand::new("folder", "");
    chooses(&stand, Some("linked"));
    let vault = stand.root.join("дом").join("Хранилище");
    fs::create_dir_all(&vault).unwrap();
    let (window, handle, thread) = stand.start(fake(), STOP_TIMEOUT);
    window.wait_for(&opened());
    wait_declared(&stand, 1);
    assert_eq!(outcome(&handle.pick_folder("not-a-number")), folder::Outcome::Expired);
    assert!(!window.seen().iter().any(|seen| seen.starts_with("folder")), "a window for a number of another form");
    let heard = handle.pick_folder(CHOICE);
    window.wait_for("folder Северный предел");
    assert_eq!(outcome(&handle.pick_folder(CHOICE)), folder::Outcome::Expired, "a second window");
    window.choose(Some(&vault));
    assert_eq!(outcome(&heard), folder::Outcome::Linked);
    stand.wait_received(&format!("folder-picked {}", as_heard(&vault)));
    assert_eq!(folder::Outcome::Linked.json(), serde_json::json!({"outcome": "linked"}));
    handle.close();
    thread.join().unwrap();
    let journal = stand.journal();
    assert!(journal.contains("folder_choice") && journal.contains("folder_result outcome=linked"), "{journal}");
    assert!(!journal.contains(CHOICE) && !journal.contains("Хранилище") && !journal.contains("Северный"), "{journal}");
}

#[test]
fn a_cancel_and_a_folder_of_the_app_tell_the_backend_nothing() {
    // OB1.2, OB1.3: a cancel is «отмена»; a folder the erasure takes, or that holds it, is refused by the shell.
    let stand = Stand::erasing("folder-refused", "");
    chooses(&stand, Some("linked"));
    let (window, handle, thread) = stand.start(fake(), STOP_TIMEOUT);
    window.wait_for(&opened());
    wait_declared(&stand, 1);
    let heard = handle.pick_folder(CHOICE);
    window.wait_for("folder Северный предел");
    window.choose(None);
    assert_eq!(outcome(&heard), folder::Outcome::Canceled);
    handle.close();
    thread.join().unwrap();
    assert!(!stand.received().iter().any(|line| line.starts_with("folder-picked")), "{:?}", stand.received());

    for (start, inside) in [stand.data.clone(), format!("{}/logs", stand.data)].into_iter().enumerate() {
        let (window, handle, thread) = stand.start(fake(), STOP_TIMEOUT);
        window.wait_for(&opened());
        wait_declared(&stand, start + 2);
        let heard = handle.pick_folder(CHOICE);
        window.wait_for("folder Северный предел");
        fs::create_dir_all(&inside).unwrap();
        window.choose(Some(Path::new(&inside)));
        assert_eq!(outcome(&heard), folder::Outcome::Refused("data_dir".into()), "{inside}");
        handle.close();
        thread.join().unwrap();
    }
    assert!(!stand.received().iter().any(|line| line.starts_with("folder-picked")), "{:?}", stand.received());
}

#[test]
fn the_page_hears_a_refusal_of_the_backend_and_waits_for_it() {
    // OB1.2, OB1.3: the shell names the folder to the backend and answers the page only with the refusal the backend
    // sends — never «linked» of its own.
    let stand = Stand::new("folder-refused-by-backend", "");
    chooses(&stand, Some("not_folder"));
    let vault = stand.root.join("дом").join("Хранилище");
    fs::create_dir_all(&vault).unwrap();
    let (window, handle, thread) = stand.start(fake(), STOP_TIMEOUT);
    window.wait_for(&opened());
    wait_declared(&stand, 1);
    let heard = handle.pick_folder(CHOICE);
    window.wait_for("folder Северный предел");
    window.choose(Some(&vault));
    assert_eq!(outcome(&heard), folder::Outcome::Refused("not_folder".into()));
    stand.wait_received(&format!("folder-picked {}", as_heard(&vault)));
    handle.close();
    thread.join().unwrap();
}

#[test]
fn a_backend_that_never_answers_ends_the_choice_for_the_page_in_its_time() {
    // OB1.2: without folder-result the page hears nothing — then, after the time of an answer, that the choice went by.
    let stand = Stand::new("folder-silent", "");
    chooses(&stand, None);
    let vault = stand.root.join("дом").join("Хранилище");
    fs::create_dir_all(&vault).unwrap();
    let (window, handle, thread) = stand.start(fake(), STOP_TIMEOUT);
    window.wait_for(&opened());
    wait_declared(&stand, 1);
    let heard = handle.pick_folder(CHOICE);
    window.wait_for("folder Северный предел");
    let picked = Instant::now();
    window.choose(Some(&vault));
    stand.wait_received(&format!("folder-picked {}", as_heard(&vault)));
    assert!(heard.recv_timeout(Duration::from_secs(5)).is_err(), "the page heard before the backend answered");
    assert_eq!(outcome(&heard), folder::Outcome::Expired);
    assert!(picked.elapsed() >= folder::RESULT_TERM, "the choice went by early: {:?}", picked.elapsed());
    handle.close();
    thread.join().unwrap();
}

// specs/2026-11-16-desk-app-links.md: the links `canonkeeper://` of the first start and of a second one on the real threads of
// the supervision.
const SAGA: &str = "0b7c1a52-5d1e-4f43-9f6b-2a8c1e0b9d44";
const CHAPTER: &str = "7c3f0a52-5d1e-4f43-9f6b-2a8c1e0b9d45";

fn link_to(paragraph: u32) -> Argument {
    Argument::new(&format!("canonkeeper://open?saga={SAGA}&chapter={CHAPTER}&p={paragraph}"))
}

fn place_of(paragraph: u32) -> String {
    format!("/manuscript/open?saga={SAGA}&chapter={CHAPTER}&p={paragraph}")
}

/// The opening of the window whose exchange goes on to the place of the link.
fn opening_to(code: &str, paragraph: u32) -> String {
    format!(
        "open 24117 http://127.0.0.1:24117/desktop/open?n=0&to=%2Fmanuscript%2Fopen%3Fsaga%3D{SAGA}%26chapter%3D{CHAPTER}%26p%3D{paragraph}#c={code} {:?}",
        Notes::default()
    )
}

/// The log names the facts of the links and no part of one.
fn no_part_of_a_link(journal: &str) {
    assert!(!journal.contains(SAGA) && !journal.contains(CHAPTER) && !journal.contains("canonkeeper:"), "{journal}");
    assert!(!journal.contains("manuscript") && !journal.contains("p=") && !journal.contains("rev="), "{journal}");
}

#[test]
fn a_link_of_the_first_start_is_the_first_page_after_the_exchange() {
    // AL1.3, AL1.4: the backend starts — the link waits —, then the opening carries it: the strings of the launch are said by
    // that page, not burnt by a page that leaves at once.
    let mut stand = Stand::new("link-first", "ready 24117 code-of-a-link-0123456789\nserve\n");
    stand.link = Some(link_to(15));
    let (window, handle, thread) = stand.start(fake(), STOP_TIMEOUT);
    window.wait_for(&opening_to("code-of-a-link-0123456789", 15));
    handle.close();
    thread.join().unwrap();
    assert!(!window.seen().iter().any(|seen| seen.starts_with("visit")), "{:?}", window.seen());
    let journal = stand.journal();
    assert_eq!(journal.matches("link_opened").count(), 1, "{journal}");
    no_part_of_a_link(&journal);
}

#[test]
fn a_link_of_a_second_start_moves_the_loaded_page_and_a_refused_one_shows_its_page() {
    // AL1.3, AL1.4: after the page of the opening loaded, the window comes up and its page moves; a refused link leads to
    // the page that says so, once the interval since the last move went by.
    let stand = Stand::new("link-second", "ready 24117 code-of-a-link-0123456789\nserve\n");
    let (window, handle, thread) = stand.start(fake(), STOP_TIMEOUT);
    window.wait_for(&format!(
        "open 24117 http://127.0.0.1:24117/desktop/open?n=0#c=code-of-a-link-0123456789 {:?}",
        Notes::default()
    ));
    // A page of another opening does not count: the link waits.
    handle.loaded(0);
    handle.link(link_to(15));
    std::thread::sleep(Duration::from_millis(300));
    assert!(!window.seen().iter().any(|seen| seen.starts_with("visit")), "{:?}", window.seen());
    handle.loaded(1);
    window.wait_for(&format!("visit {}", place_of(15)));
    handle.link(Argument::new(&format!("canonkeeper://open?saga={SAGA}&chapter=%41")));
    window.wait_for("visit /manuscript/open?refused=1");
    handle.close();
    thread.join().unwrap();
    let journal = stand.journal();
    assert_eq!(journal.matches("link_opened").count(), 2, "{journal}");
    assert!(journal.contains("link_refused value"), "{journal}");
    no_part_of_a_link(&journal);
}

#[test]
fn a_link_of_a_second_start_while_the_backend_starts_waits_and_rides_the_exchange() {
    // AL1.3: the backend is slow to start; two links come meanwhile — the last one waits and the opening carries it.
    let stand = Stand::new("link-starting", "sleep 600\nready 24117 code-of-a-link-0123456789\nserve\n");
    let (window, handle, thread) = stand.start(fake(), STOP_TIMEOUT);
    handle.link(link_to(3));
    handle.link(link_to(4));
    window.wait_for(&opening_to("code-of-a-link-0123456789", 4));
    handle.loaded(1);
    std::thread::sleep(Duration::from_millis(300));
    handle.close();
    thread.join().unwrap();
    assert!(!window.seen().iter().any(|seen| seen.starts_with("visit")), "{:?}", window.seen());
    let journal = stand.journal();
    assert!(journal.contains("link_merged") && journal.matches("link_opened").count() == 1, "{journal}");
    no_part_of_a_link(&journal);
}

#[test]
fn a_link_while_the_data_are_erased_or_the_app_closes_is_dropped() {
    // AL1.3: the backend stops for an erasure, or for the end of the app: the link goes, nothing moves.
    for (name, plan, why) in [
        ("link-erase", "ready 24117 code-of-a-link-0123456789\nerase\nserve 1500\n", "erasing"),
        ("link-close", "ready 24117 code-of-a-link-0123456789\nserve 1500\n", "closing"),
    ] {
        let stand = Stand::new(name, plan);
        let (window, handle, thread) = stand.start(fake(), STOP_TIMEOUT);
        window.wait_for(&format!(
            "open 24117 http://127.0.0.1:24117/desktop/open?n=0#c=code-of-a-link-0123456789 {:?}",
            Notes::default()
        ));
        handle.loaded(1);
        if why == "closing" {
            handle.close();
        }
        stand.wait_received("shutdown");
        handle.link(link_to(7));
        thread.join().unwrap();
        assert!(!window.seen().iter().any(|seen| seen.starts_with("visit") || seen == "focus"), "{:?}", window.seen());
        let journal = stand.journal();
        assert!(journal.contains(&format!("link_dropped {why}")), "{journal}");
        assert!(!journal.contains("link_opened"), "{journal}");
    }
}

/// A store of the system that works and takes its time to remove a record: the erasure waits for it.
#[derive(Clone, Default)]
struct SlowRemoval(Memory);

impl Keyring for SlowRemoval {
    fn set(&self, id: &str, value: &str) -> Result<(), Failure> {
        self.0.set(id, value)
    }
    fn get(&self, id: &str) -> Result<Option<String>, Failure> {
        self.0.get(id)
    }
    fn delete(&self, id: &str) -> Result<(), Failure> {
        std::thread::sleep(Duration::from_millis(400));
        self.0.delete(id)
    }
}

#[test]
fn a_link_while_a_window_of_a_failure_waits_or_the_records_go_is_dropped_by_its_reason() {
    // AL1.7: the data were half erased — the window of the failure waits for «Закрыть», no supervision runs: the link of the
    // start and a link of a second start are dropped, each by its line.
    let mut stand = Stand::new("link-unended", "");
    stand.earlier = Earlier::Unended(Unfinished::Kept);
    stand.link = Some(link_to(3));
    let (window, handle, thread) = stand.start(fake(), STOP_TIMEOUT);
    let words = window.wait_failure();
    handle.link(link_to(4));
    handle.close();
    thread.join().unwrap();
    assert!(words.ends_with("[Закрыть]"), "{words}");
    let journal = stand.journal();
    assert_eq!(journal.matches("link_dropped failure").count(), 2, "{journal}");
    no_part_of_a_link(&journal);

    // The records of the set go after the backend stopped for an erasure: a link meanwhile is dropped as erasing.
    let stand = Stand::new("link-records", "ready 24117 code-of-a-link-0123456789\nerase\nserve\n");
    let store = SlowRemoval::default();
    store.set(OWN_AI, "key-test-records-of-a-link").unwrap();
    let (window, handle, thread) = stand.start_with(fake(), STOP_TIMEOUT, Arc::new(store));
    window.wait_for("close");
    handle.link(link_to(5));
    thread.join().unwrap();
    let journal = stand.journal();
    assert!(journal.contains("link_dropped erasing"), "{journal}");
    assert!(!journal.contains("link_opened"), "{journal}");
}
