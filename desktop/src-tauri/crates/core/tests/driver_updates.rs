// SPDX-License-Identifier: AGPL-3.0-only

//! The updates of the app on a stand-in backend and a stand-in source of versions (specs/2026-11-13-desk-updates.md UB1.1,
//! UB2.3–UB2.7, UB2.9): the real threads, pipes and exits of the supervision, a window that records what it was told and
//! installs as the test says, a `pkexec` of the test. Nothing is installed into the system and nothing reaches the site
//! or GitHub.

mod source;

use std::fs;
use std::path::{Path, PathBuf};
use std::process::{Child, Command, Stdio};
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

use canonkeeper_desktop_core::driver::{run, Config, Handle, Window};
use canonkeeper_desktop_core::erase::{self, Earlier};
use canonkeeper_desktop_core::journal::Journal;
use canonkeeper_desktop_core::paths::{Places, System};
use canonkeeper_desktop_core::secrets::{Failure, Keyring};
use canonkeeper_desktop_core::supervisor::Notes;
use canonkeeper_desktop_core::update::fetch::Downloaded;
use canonkeeper_desktop_core::update::package::Programs;
use canonkeeper_desktop_core::update::policy::{Limits, Policy};
use canonkeeper_desktop_core::update::state::Cause;
use canonkeeper_desktop_core::update::{self, Kind, Setup};
use canonkeeper_desktop_core::words;
use source::{keys, publish, Reply, Signer, Source};

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

/// A window that records what it was told; an installation through it ends as the test set.
#[derive(Clone)]
struct Recorder {
    seen: Arc<Mutex<Vec<String>>>,
    installs: Arc<Mutex<Result<(), Cause>>>,
    notes: Arc<Mutex<Vec<Notes>>>,
}

impl Recorder {
    fn new() -> Recorder {
        Recorder { seen: Arc::default(), installs: Arc::new(Mutex::new(Ok(()))), notes: Arc::default() }
    }

    fn seen(&self) -> Vec<String> {
        self.seen.lock().unwrap().clone()
    }

    fn push(&self, what: String) {
        self.seen.lock().unwrap().push(what);
    }

    fn wait_for(&self, what: &str) {
        let deadline = Instant::now() + Duration::from_secs(20);
        while Instant::now() < deadline {
            if self.seen().iter().any(|seen| seen.starts_with(what)) {
                return;
            }
            std::thread::sleep(Duration::from_millis(20));
        }
        panic!("the window was never told {what:?}; it was told {:?}", self.seen());
    }

    /// What the window was told, the openings without their codes.
    fn told(&self) -> Vec<String> {
        self.seen().into_iter().map(|seen| if seen.starts_with("open ") { "open".to_string() } else { seen }).collect()
    }
}

impl Window for Recorder {
    fn show_starting(&self) {
        self.push("starting".into());
    }
    fn show_step(&self, words: &str) {
        self.push(format!("step {words}"));
    }
    fn open(&self, port: u16, _url: &str, notes: Notes) {
        self.notes.lock().unwrap().push(notes);
        self.push(format!("open {port}"));
    }
    fn show_failure(&self, failure: &words::Failure) {
        self.push(format!("failure {}", failure.page.words));
    }
    fn close(&self) {
        self.push("close".into());
    }
    fn leave(&self, words: &str) {
        self.push(format!("leave {words}"));
    }
    fn install(&self, file: &Downloaded, kind: Kind) -> Result<(), Cause> {
        self.push(format!("install {} {}", kind.name(), file.release.version));
        *self.installs.lock().unwrap()
    }
    fn restart(&self) {
        self.push("restart".into());
    }
    fn focus(&self) {
        self.push("focus".into());
    }

    fn folder(&self, _: &str, chosen: Box<dyn FnOnce(Option<PathBuf>) + Send>) {
        chosen(None);
    }
    fn exit(&self) {
        self.push("exit".into());
    }
}

struct Stand {
    root: PathBuf,
    resources: PathBuf,
    data: String,
    source: Source,
    signer: Signer,
    setup: Setup,
    targets: erase::Targets,
    /// Programs the stand started that hold the installation; killed at the end.
    held: Vec<Child>,
}

impl Stand {
    fn new(name: &str, kind: Kind, plan: &str) -> Stand {
        let root = std::env::temp_dir().join(format!("canonkeeper-updates-{name}-{}", std::process::id()));
        let _ = fs::remove_dir_all(&root);
        let resources = root.join("ресурсы");
        for folder in [&resources, &root.join("bin"), &root.join("temp"), &root.join("Загрузки"), &root.join("дом")]
        {
            fs::create_dir_all(folder).unwrap();
        }
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
        let (source, signer, spare) = (Source::start(), Signer::new(), Signer::new());
        let mut policy = Policy::loopback(vec![source.at("/site/latest.json")], keys(&signer, &spare));
        policy.limits = Limits { wait: Duration::from_secs(2), idle: Duration::from_secs(5), ..Limits::RELEASE };
        let programs = Programs {
            pkexec: root.join("bin").join("pkexec"),
            apt_get: root.join("bin").join("apt-get"),
            dpkg: root.join("bin").join("dpkg"),
            dnf: root.join("bin").join("dnf"),
            rpm: root.join("bin").join("rpm"),
            downloads: Some(root.join("Загрузки")),
            home: Some(root.join("дом")),
            temp: root.join("temp"),
        };
        // The bridge for the programs of the author lies in the installation the stand stands for (MC1.8).
        let bridges = vec![resources.join(canonkeeper_desktop_core::mcp::PROGRAM)];
        let setup = Setup { kind, policy, programs, holders: vec![resources.clone()], bridges };
        Stand { root, resources, data, source, signer, setup, targets, held: Vec::new() }
    }

    /// A program of the test, by its name in `bin/`: a script of the shell of Unix — `pkexec` and the package managers
    /// exist only for the packages of Linux.
    #[cfg(unix)]
    fn program(&self, name: &str, body: &str) {
        use std::os::unix::fs::PermissionsExt;
        let path = self.root.join("bin").join(name);
        fs::write(&path, format!("#!/bin/sh\necho \"$@\" >> '{}'\n{body}\n", self.root.join("pkexec.txt").display()))
            .unwrap();
        fs::set_permissions(&path, fs::Permissions::from_mode(0o755)).unwrap();
    }

    #[cfg(unix)]
    fn pkexec_record(&self) -> String {
        fs::read_to_string(self.root.join("pkexec.txt")).unwrap_or_default()
    }

    /// A release of 1.3.0 for the kind of the stand on its source.
    fn publish(&self, file: &[u8]) {
        let platform = self.setup.kind.platform().unwrap();
        publish(&self.source, "/site", "1.3.0", platform, file, &self.signer);
    }

    fn start(&self, stop_timeout: Duration) -> (Recorder, Handle) {
        let recorder = Recorder::new();
        let config = Config {
            program: PathBuf::from(env!("CARGO_BIN_EXE_fake-backend")),
            data_dir: self.data.clone(),
            resources: self.resources.to_string_lossy().into_owned(),
            version: "1.2.0".to_string(),
            system: System::Linux,
            stop_timeout,
            erase: self.targets.clone(),
            patience: Duration::from_millis(400),
            earlier: Earlier::None,
            updates: Some(self.setup.clone()),
            bridge: None,
        };
        let journal = Journal::open(&Path::new(&self.data).join("logs"), std::time::SystemTime::now());
        let (handle, _thread) = run(config, recorder.clone(), journal, Arc::new(NoStore));
        (recorder, handle)
    }

    fn plan(&self, plan: &str) {
        fs::write(self.resources.join("plan.txt"), plan).unwrap();
    }

    fn received(&self) -> Vec<String> {
        fs::read_to_string(self.resources.join("received.txt"))
            .unwrap_or_default()
            .lines()
            .map(str::to_string)
            .collect()
    }

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

    /// The number of `start`s the backend got.
    fn starts(&self) -> usize {
        fs::read_to_string(self.resources.join("starts.jsonl")).unwrap_or_default().lines().count()
    }

    /// A «postgres» of the bundle that still runs: a copy of the stand-in backend that never stops.
    fn hold(&mut self) {
        let bin = self.resources.join("postgres").join("bin");
        fs::create_dir_all(&bin).unwrap();
        self.run_copy(&bin.join("postgres"), "postgres-plan");
    }

    /// A copy of the stand-in backend at `program` that runs and never stops.
    fn run_copy(&mut self, program: &Path, plan: &str) {
        let postgres = program.to_path_buf();
        fs::copy(env!("CARGO_BIN_EXE_fake-backend"), &postgres).unwrap();
        let place = self.root.join(plan);
        fs::create_dir_all(&place).unwrap();
        fs::write(place.join("plan.txt"), "deaf\n").unwrap();
        let mut child = Command::new(&postgres).stdin(Stdio::piped()).stdout(Stdio::null()).spawn().unwrap();
        use std::io::Write;
        let start = serde_json::json!({"type": "start", "resources": place.to_string_lossy()});
        writeln!(child.stdin.as_mut().unwrap(), "{start}").unwrap();
        let deadline = Instant::now() + Duration::from_secs(10);
        while !fs::read_to_string(place.join("received.txt")).unwrap_or_default().contains("pid") {
            assert!(Instant::now() < deadline, "the «postgres» of the test did not start");
            std::thread::sleep(Duration::from_millis(20));
        }
        self.held.push(child);
    }
}

impl Drop for Stand {
    fn drop(&mut self) {
        for child in &mut self.held {
            let _ = child.kill();
            let _ = child.wait();
        }
        let _ = fs::remove_dir_all(&self.root);
    }
}

/// The plan of a backend that checks, downloads and installs 1.3.0 as soon as the shell allows it.
const UPDATES: &str = r#"ready 24117 c
say {"type":"update-check"}
on available {"type":"update-download","version":"1.3.0"}
on downloaded {"type":"update-install","version":"1.3.0"}
serve
"#;

const AGAIN: &str = "ready 24117 again\nserve\n";

#[test]
fn an_appimage_is_installed_only_after_the_window_left_and_the_backend_stopped_by_itself() {
    // UB2.3, UB2.5: checked → the window on the page of the start → shutdown → the backend gone → the check → the
    // installation → the app starts again.
    let stand = Stand::new("appimage", Kind::AppImage, UPDATES);
    stand.publish(b"the new AppImage");
    let (window, handle) = stand.start(Duration::from_secs(20));
    window.wait_for("leave Устанавливаю версию 1.3.0: сохраняю данные и останавливаю помощника");
    std::thread::sleep(Duration::from_millis(300));
    assert!(!stand.received().contains(&"shutdown".to_string()), "the backend stopped before the window left");
    handle.left();
    window.wait_for("restart");
    assert_eq!(
        window.told(),
        [
            "open",
            "leave Устанавливаю версию 1.3.0: сохраняю данные и останавливаю помощника. Приложение закроется и откроется снова",
            "install appimage 1.3.0",
            "restart"
        ]
    );
    let received = stand.received();
    let shutdown = received.iter().position(|line| line == "shutdown").unwrap();
    assert!(received[..shutdown].contains(&"update-state downloaded".to_string()), "{received:?}");
    // The start told the backend how the app was installed.
    let start: serde_json::Value =
        serde_json::from_str(&fs::read_to_string(stand.resources.join("start.json")).unwrap()).unwrap();
    assert_eq!(start["updateKind"], "appimage");
}

#[test]
fn a_backend_killed_by_the_deadline_or_a_program_of_the_installation_that_runs_installs_nothing() {
    // UB2.3, UB2.7: the installation does not begin; the backend starts again and hears install_failed after ready.
    let stand = Stand::new("killed", Kind::AppImage, &UPDATES.replace("serve\n", "serve 5000\n"));
    stand.publish(b"the new AppImage");
    let (window, handle) = stand.start(Duration::from_millis(500));
    window.wait_for("leave");
    stand.plan(AGAIN);
    handle.left();
    stand.wait_received("update-state install_failed");
    assert!(!window.seen().iter().any(|seen| seen.starts_with("install")), "{:?}", window.seen());
    assert_eq!(stand.starts(), 2);

    let mut held = Stand::new("held", Kind::AppImage, UPDATES);
    held.publish(b"the new AppImage");
    held.hold();
    let (window, handle) = held.start(Duration::from_secs(20));
    window.wait_for("leave");
    held.plan(AGAIN);
    handle.left();
    held.wait_received("update-state install_failed");
    assert!(!window.seen().iter().any(|seen| seen.starts_with("install")), "{:?}", window.seen());
    assert_eq!(window.told().last().map(String::as_str), Some("open"), "the window is not back");
}

// specs/2026-11-15-desk-mcp.md MC1.8: on Windows the program of the bridge of the installation that runs holds nothing — the
// installer ends it first — and the launch of the new version says it once by the mark; any other program of the installation
// still refuses the installation.
#[test]
fn a_bridge_of_the_installation_that_runs_holds_nothing_and_leaves_its_mark() {
    let mut stand = Stand::new("bridge", Kind::Nsis, UPDATES);
    stand.publish(b"the new installer");
    let bridge = stand.setup.bridges[0].clone();
    stand.run_copy(&bridge, "bridge-plan");
    let (window, handle) = stand.start(Duration::from_secs(20));
    window.wait_for("leave");
    stand.plan(AGAIN);
    handle.left();
    window.wait_for("install nsis 1.3.0");
    assert!(update::take_bridge_note(Path::new(&stand.data), "1.3.0"), "no mark of the bridge");
    assert!(!update::take_bridge_note(Path::new(&stand.data), "1.3.0"), "the mark is said twice");

    let mut held = Stand::new("bridge-held", Kind::Nsis, UPDATES);
    held.publish(b"the new installer");
    let bridge = held.setup.bridges[0].clone();
    held.run_copy(&bridge, "bridge-plan");
    held.hold();
    let (window, handle) = held.start(Duration::from_secs(20));
    window.wait_for("leave");
    held.plan(AGAIN);
    handle.left();
    held.wait_received("update-state install_failed");
    assert!(!window.seen().iter().any(|seen| seen.starts_with("install")), "{:?}", window.seen());
}

#[test]
fn a_backend_that_stopped_with_an_error_installs_nothing() {
    // UB2.3, UB2.7: the backend went by itself, in time, but with an error — it could not stop its cluster: the
    // installation does not begin, the backend starts again on the same data and hears install_failed after ready.
    for kind in [Kind::AppImage, Kind::Nsis] {
        let stand =
            Stand::new(&format!("stop-error-{}", kind.name()), kind, &UPDATES.replace("serve\n", "serve stop 1\n"));
        stand.publish(b"the new installer");
        let (window, handle) = stand.start(Duration::from_secs(20));
        window.wait_for("leave");
        stand.plan(AGAIN);
        handle.left();
        stand.wait_received("update-state install_failed");
        assert!(stand.received().contains(&"shutdown".to_string()), "{:?}", stand.received());
        assert!(!window.seen().iter().any(|seen| seen.starts_with("install")), "{:?}", window.seen());
        assert!(!window.seen().contains(&"restart".to_string()), "{:?}", window.seen());
        assert_eq!(stand.starts(), 2);
        assert_eq!(window.told().last().map(String::as_str), Some("open"), "the window is not back");
    }
}

#[test]
fn an_installer_that_did_not_start_brings_the_window_and_the_backend_back() {
    // UB2.4, UB2.5, UB2.7: Windows — the installer did not start; Linux — the AppImage was not replaced.
    for (kind, cause) in [(Kind::Nsis, Cause::NotLaunched), (Kind::AppImage, Cause::NotReplaced)] {
        let stand = Stand::new(&format!("installer-{}", kind.name()), kind, UPDATES);
        stand.publish(b"the new installer");
        let (window, handle) = stand.start(Duration::from_secs(20));
        *window.installs.lock().unwrap() = Err(cause);
        window.wait_for("leave");
        stand.plan(AGAIN);
        handle.left();
        stand.wait_received("update-state install_failed");
        let told = window.told();
        assert_eq!(
            told[told.len() - 3..],
            [format!("install {} 1.3.0", kind.name()), "starting".into(), "open".into()]
        );
        assert!(!told.contains(&"restart".to_string()));
    }
}

// A package of Linux (UB2.6): `pkexec` and the package managers of the test are scripts of the shell of Unix.
#[cfg(unix)]
#[test]
fn a_package_the_system_refused_is_kept_and_the_backend_starts_again() {
    // UB2.6: 126 — the window of the password closed; 127 — the system cannot ask; the package beside a file of the same
    // name in «Загрузки».
    for (code, refusal) in [("exit 126", "declined"), ("exit 127", "unavailable")] {
        let stand = Stand::new(&format!("refused-{refusal}"), Kind::Deb, UPDATES);
        stand.program("pkexec", code);
        stand.program("apt-get", "exit 0");
        stand.publish(&debian());
        fs::write(stand.root.join("Загрузки").join("CanonKeeper_1.3.0_amd64.deb"), "a file of the author").unwrap();
        let (window, handle) = stand.start(Duration::from_secs(20));
        window.wait_for("leave");
        stand.plan(AGAIN);
        handle.left();
        stand.wait_received("update-state install_refused");
        assert!(stand.pkexec_record().contains("install -y -o DPkg::Lock::Timeout=120"), "{}", stand.pkexec_record());
        let kept = stand.root.join("Загрузки").join("CanonKeeper_1.3.0_amd64 (2).deb");
        assert_eq!(fs::read(kept).unwrap(), debian());
        assert_eq!(
            fs::read_to_string(stand.root.join("Загрузки").join("CanonKeeper_1.3.0_amd64.deb")).unwrap(),
            "a file of the author"
        );
        assert_eq!(window.told().last().map(String::as_str), Some("open"));
    }
}

// A package of Linux (UB2.6): `pkexec` and the package managers of the test are scripts of the shell of Unix.
#[cfg(unix)]
#[test]
fn a_package_that_failed_after_the_password_starts_the_app_again_with_its_note() {
    // UB2.6: the backend is not started again under this shell; the next start of the shell reads the note once.
    let stand = Stand::new("unfinished", Kind::Deb, UPDATES);
    stand.program("pkexec", "exit 1");
    stand.program("apt-get", "exit 0");
    stand.publish(&debian());
    let (window, handle) = stand.start(Duration::from_secs(20));
    window.wait_for("leave");
    handle.left();
    window.wait_for("restart");
    assert_eq!(stand.starts(), 1, "the backend started again under the old shell");
    let package = stand.root.join("Загрузки").join("CanonKeeper_1.3.0_amd64.deb").to_string_lossy().into_owned();
    assert_eq!(
        update::take_note(Path::new(&stand.data)),
        Some(canonkeeper_desktop_core::supervisor::Unfinished { version: "1.3.0".into(), package: Some(package) })
    );

    // A success starts the app again, without a note.
    let stand = Stand::new("installed", Kind::Deb, UPDATES);
    stand.program("pkexec", "exit 0");
    stand.program("apt-get", "exit 0");
    stand.publish(&debian());
    let (window, handle) = stand.start(Duration::from_secs(20));
    window.wait_for("leave");
    handle.left();
    window.wait_for("restart");
    assert_eq!(update::take_note(Path::new(&stand.data)), None);
    assert_eq!(fs::read_dir(stand.root.join("Загрузки")).unwrap().count(), 0);
}

#[test]
fn an_erasure_during_the_download_or_the_wait_is_the_erasure_and_installs_nothing() {
    // UB2.9: during the download.
    let stand = Stand::new(
        "erase-download",
        Kind::AppImage,
        r#"ready 24117 c
say {"type":"update-check"}
on available {"type":"update-download","version":"1.3.0"}
on downloading {"type":"erase"}
serve
"#,
    );
    stand.publish(b"the new AppImage");
    stand.source.route("/site/file-1.3.0", Reply::Silent(Duration::from_secs(4)));
    let (window, _handle) = stand.start(Duration::from_secs(20));
    window.wait_for("exit");
    assert_eq!(window.told(), ["open", "close", "exit"]);
    assert!(!Path::new(&stand.data).exists(), "the data were not erased");

    // After the download, while the backend waits for the turns: then an installation it asks for is refused.
    let stand = Stand::new(
        "erase-wait",
        Kind::AppImage,
        r#"ready 24117 c
say {"type":"update-check"}
on available {"type":"update-download","version":"1.3.0"}
on downloaded {"type":"erase"}
on downloaded {"type":"update-install","version":"1.3.0"}
serve
"#,
    );
    stand.publish(b"the new AppImage");
    let (window, _handle) = stand.start(Duration::from_secs(20));
    window.wait_for("exit");
    assert_eq!(window.told(), ["open", "close", "exit"]);
}

#[test]
fn a_backend_started_again_during_a_download_hears_the_phase_after_ready() {
    // UB1.1: the state lives in the shell; every ready hears it.
    let stand = Stand::new(
        "restarted",
        Kind::AppImage,
        r#"ready 24117 c
say {"type":"update-check"}
on available {"type":"update-download","version":"1.3.0"}
on downloading exit 1
serve
"#,
    );
    stand.publish(b"the new AppImage");
    stand.source.route("/site/file-1.3.0", Reply::Silent(Duration::from_secs(4)));
    let (window, _handle) = stand.start(Duration::from_secs(20));
    stand.wait_received("update-state downloading");
    stand.plan(AGAIN);
    window.wait_for("starting");
    let deadline = Instant::now() + Duration::from_secs(20);
    loop {
        let received = stand.received();
        let restarted = received.iter().rposition(|line| line.starts_with("start ")).filter(|_| stand.starts() == 2);
        if let Some(first) =
            restarted.and_then(|at| received[at..].iter().find(|line| line.starts_with("update-state")))
        {
            // The second backend hears where the download is, not «never».
            assert_eq!(first, "update-state downloading", "{received:?}");
            break;
        }
        assert!(Instant::now() < deadline, "{received:?}");
        std::thread::sleep(Duration::from_millis(20));
    }
    // The first ready heard that nothing was checked yet.
    assert_eq!(stand.received().iter().find(|line| line.starts_with("update-state")).unwrap(), "update-state never");
}

/// The first bytes of a Debian package, then anything.
#[cfg(unix)]
fn debian() -> Vec<u8> {
    let mut bytes = b"!<arch>\ndebian-binary   ".to_vec();
    bytes.extend_from_slice(&[b' '; 40]);
    bytes.extend_from_slice(b"the package of 1.3.0");
    bytes
}
