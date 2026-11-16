// SPDX-License-Identifier: AGPL-3.0-only

//! The thread that supervises the backend (specs/2026-11-12-desk-shell.md SH1, SH2): it feeds what the backend and the
//! window report into the supervisor and carries out its actions — the process here, the window through [`Window`],
//! which the window crate implements with Tauri. Words of a failure come from the one table of [`crate::words`]. The
//! secrets of the app (specs/2026-11-13-desk-secrets.md SE1) belong to a keeper on a thread of its own: it opens the
//! store, reads the records for every `start` and keeps or removes what the backend asks, in the order asked — a store
//! that waits for the author to unlock it never holds up the supervision. «Стереть все данные»
//! (specs/2026-11-13-desk-erase.md ER1.3–ER1.6) is carried out here too: the backend asks for it on its channel, the
//! supervision stops it, and the driver closes the window, has the keeper remove the records, removes the directories of
//! the app and takes the mark away — or, when the erasure could not be marked, shows why and leaves the data in place.
//! The updates of the app (specs/2026-11-13-desk-updates.md UB1, UB2) are carried out here as well: the backend asks to
//! check, download, install or cancel, the thread of the network does it, and every change — and every `ready` — goes back
//! to the backend as `update-state`; an installation begins only once the backend stopped by itself and nothing of the
//! installation runs, and any failure before the program changed starts the backend again on the same data. The choice
//! of the folder of an Obsidian vault (specs/2026-11-13-desk-obsidian.md OB1.1–OB1.3) goes through here as well: the
//! backend declares it, the page asks for the system window by its number, the shell checks the folder on the thread of
//! the window, and the page hears only how the backend ended it. A link `canonkeeper://` (specs/2026-11-16-desk-app-links.md
//! AL1) of the first start or of a second one is checked here and, by the phase of the supervision, waits for the window,
//! rides its opening to the first page, goes to its loaded page or is dropped; a link a restart repeats is dropped once.

use std::collections::VecDeque;
use std::path::{Path, PathBuf};
use std::sync::mpsc::{self, Receiver, RecvTimeoutError, Sender};
use std::sync::Arc;
use std::thread::{self, JoinHandle};
use std::time::{Duration, Instant};

use crate::backend::{Backend, Heard};
use crate::control::{FromBackend, Step, ToBackend};
use crate::erase::{self, Earlier, Targets, Unfinished};
use crate::folder::{self, Choosing, Outcome, Reply};
use crate::journal::{name, Journal};
use crate::link::{self, Argument, Links, Target};
use crate::paths::System;
use crate::secrets::{self, Keyring, Opened, Secret, SecretError, Secrets, PROBE_TIMEOUT};
use crate::supervisor::{Action, Event, Notes, Supervisor, Unfinished as UnfinishedNote};
use crate::update::fetch::{Downloaded, Network};
use crate::update::state::{Cause, Updates};
use crate::update::worker::{Done, Worker};
use crate::update::{self, package, stopped, Kind, Setup};
use crate::words::{failure, installing_words, step_words, Failure, Fault};

/// How often time passes for the supervisor while nothing happens.
const TICK: Duration = Duration::from_secs(1);

/// The window as the supervision sees it. Every call comes from the thread of the driver.
pub trait Window: Send + 'static {
    /// Shows the page of the shell: «CanonKeeper запускается…».
    fn show_starting(&self);
    /// The page of the shell says the long step of the start that goes on (specs/2026-11-13-desk-upgrade.md UG3.1).
    fn show_step(&self, words: &str);
    /// Lets the page of 127.0.0.1:port call the commands of the shell, keeps the notes for `launch_notes` and opens
    /// `url`, which carries the one-time code. `opening` numbers this opening: the window reports the page of the backend
    /// it loaded after the exchange by it ([`Handle::loaded`]).
    fn open(&self, port: u16, url: &str, notes: Notes, opening: u64);
    /// Shows the failure window: the words and the buttons of the failure, which its page reads by a command (UG3.2).
    fn show_failure(&self, failure: &Failure);
    /// Closes the window for good before an erasure: its webview lets go of the data of the window.
    fn close(&self);
    /// Goes to the page of the start with the words of an installation; once it is loaded the window says so
    /// ([`Handle::left`]) — the page of the backend is gone and saved what it held (UB2.3).
    fn leave(&self, words: &str);
    /// Installs the update of an installer of Windows or of an AppImage with the updater plugin (UB2.4, UB2.5): the
    /// bytes are checked again by the key that signed them. Windows ends the app once the installer runs; an AppImage
    /// returns once its file is replaced. An error says nothing changed.
    fn install(&self, file: &Downloaded, kind: Kind) -> Result<(), Cause>;
    /// The program is replaced: the app starts again by the way it ends — no second instance beside it (UB2.5).
    fn restart(&self);
    /// Raises the window: the link with the account went through in the browser (specs/2026-11-13-desk-account.md AC1.3).
    fn focus(&self);
    /// Raises the window and gives its loaded page the place of a link by the script of the core; the page follows it by the
    /// navigation of its router, without a reload (specs/2026-11-16-desk-app-links.md AL1.4).
    fn visit(&self, target: &Target);
    /// Opens the system window that chooses a folder over the window of the app, its title naming the saga
    /// (specs/2026-11-13-desk-obsidian.md OB1.2), without waiting for it; `chosen` hears the folder, or nothing when the
    /// author canceled, once and from any thread. No page hears the folder.
    fn folder(&self, saga: &str, chosen: Box<dyn FnOnce(Option<PathBuf>) + Send>);
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
    /// The version of the shell: its backend must be the same (specs/2026-11-13-desk-upgrade.md UG2.1).
    pub version: String,
    pub system: System,
    pub stop_timeout: Duration,
    /// What «Стереть все данные» removes, how long it tries a held file, and what became of an erasure an earlier run
    /// did not end — found before the window was made.
    pub erase: Targets,
    pub patience: Duration,
    pub earlier: Earlier,
    /// The updates of the app; none for a build that knows no updates (the tests of other paths).
    pub updates: Option<Setup>,
    /// How the program of the bridge for the programs of the author runs, when it is there (specs/2026-11-15-desk-mcp.md
    /// MC3.2).
    pub bridge: Option<crate::mcp::Bridge>,
    /// The link among the arguments of the first start (specs/2026-11-16-desk-app-links.md AL1.2).
    pub link: Option<Argument>,
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

    /// The author asked, on the page of the start, to bring the data back from the copy (UG4.1); the supervision does it
    /// only from the failure of a newer schema with a copy.
    pub fn restore(&self) {
        let _ = self.sender.send(Heard::Event(Event::Restore));
    }

    /// The window reached the page of the start before an installation (UB2.3).
    pub fn left(&self) {
        let _ = self.sender.send(Heard::Event(Event::Left));
    }

    /// A second start brought a link (specs/2026-11-16-desk-app-links.md AL1.2): the raw text, checked on the thread of the
    /// supervision.
    pub fn link(&self, argument: Argument) {
        let _ = self.sender.send(Heard::Link(argument));
    }

    /// The window loaded a page of its backend after the exchange of the opening `opening` (AL1.3).
    pub fn loaded(&self, opening: u64) {
        let _ = self.sender.send(Heard::Loaded(opening));
    }

    /// The page asks for the system window that chooses the folder of a vault for the number the backend declared
    /// (specs/2026-11-13-desk-obsidian.md OB1.2): the receiver hears how the choice ends — after the backend ended it,
    /// never with the path.
    pub fn pick_folder(&self, request: &str) -> Receiver<Outcome> {
        let (reply, heard) = mpsc::channel();
        if self.sender.send(Heard::Pick { request: request.to_string(), reply: Reply(reply.clone()) }).is_err() {
            let _ = reply.send(Outcome::Expired);
        }
        heard
    }
}

/// The update the shell holds and the thread of its network.
struct Updating {
    setup: Setup,
    state: Updates,
    worker: Worker,
}

impl Updating {
    fn start(setup: Setup, version: &str, heard: Sender<Heard>) -> Updating {
        let state = Updates::new(setup.kind, version);
        let worker = Worker::start(Network::new(setup.policy.clone()), setup.kind, move |done| {
            let _ = heard.send(Heard::Update(done));
        });
        Updating { setup, state, worker }
    }
}

/// Tells the backend what the update came to (UB1.1).
fn tell(backend: &mut Option<Backend>, updating: &Option<Updating>) {
    if let (Some(running), Some(updating)) = (backend.as_mut(), updating.as_ref()) {
        let _ = running.send(&ToBackend::UpdateState(&updating.state.line()));
    }
}

/// Starts the supervision on its own thread; it ends after [`Window::exit`]. `keyring` is the store of the system the
/// secrets of the app live in when it works.
pub fn run<W: Window>(
    config: Config,
    window: W,
    journal: Journal,
    keyring: Arc<dyn Keyring>,
) -> (Handle, JoinHandle<()>) {
    let (sender, receiver) = mpsc::channel::<Heard>();
    let handle = Handle { sender: sender.clone() };
    let thread = thread::spawn(move || {
        if let Earlier::Unended(unended) = &config.earlier {
            // ER1.5: the data are half erased — no store, no backend; the words of the cause and «Закрыть».
            let fault = match unended {
                Unfinished::Held { folder } => Fault::Unerased { folder: folder.clone() },
                Unfinished::Kept => Fault::KeyKept,
            };
            window.show_failure(&failure(&fault, config.system, &config.data_dir, &config.version));
            // specs/2026-11-16-desk-app-links.md AL1.7: the link of the start is not taken; the data are not touched.
            if config.link.is_some() {
                journal.note(&format!("link_dropped {}", link::Dropped::Failure.name()));
            }
            until_closed(&receiver, &journal);
            window.exit();
            return;
        }
        if config.earlier == Earlier::Ended {
            journal.note("erase_finished");
        }
        let file = PathBuf::from(config.system.join(&config.data_dir, &[secrets::FILE]));
        let keeper = keeper(file, keyring, config.system == System::Windows, journal.clone(), sender.clone());
        let mut supervisor = Supervisor::new(config.stop_timeout);
        let mut backend: Option<Backend> = None;
        // Every start of the backend is a launch; the one whose records the keeper is reading waits here, and whether it
        // brings the data back from a copy (UG4.1).
        let mut launches: u64 = 0;
        let mut awaited: Option<u64> = None;
        let mut restoring = false;
        // The erasure is marked: without the mark it does not begin.
        let mut marked = false;
        let mut updating = config.updates.clone().map(|setup| Updating::start(setup, &config.version, sender.clone()));
        // How the last backend ended, which an installation reads (UB2.3), and whether a ready came in this run.
        let mut exit_code: Option<Option<i32>> = None;
        let mut first_ready = true;
        // The choices of the folder of a vault the backend declared (OB1.1).
        let mut choosing = Choosing::default();
        // The links of this run (specs/2026-11-16-desk-app-links.md AL1.3–AL1.5): the link of the first start, unless a
        // restart repeated the one it marked.
        let mut links = Links::default();
        let data_dir = Path::new(&config.data_dir);
        if link::replayed(data_dir, config.link.as_ref()) {
            journal.note(&format!("link_dropped {}", link::Dropped::Replayed.name()));
        } else if let Some(argument) = &config.link {
            arrive(&mut links, argument, supervisor.stage(), &window, &journal);
        }
        let mut actions: VecDeque<Action> = supervisor.start().into();
        loop {
            while let Some(action) = actions.pop_front() {
                match action {
                    Action::Spawn | Action::SpawnRestoring => {
                        restoring = action == Action::SpawnRestoring;
                        if restoring {
                            journal.note("restore_requested");
                        }
                        launches += 1;
                        awaited = Some(launches);
                        if keeper.send(Ask::Records { launch: launches }).is_err() {
                            awaited = None;
                            journal.note("backend_spawn_failed");
                            actions.extend(supervisor.handle(Event::SpawnFailed, Instant::now()));
                        }
                    }
                    Action::ShowStarting => {
                        links.left();
                        window.show_starting();
                    }
                    Action::ShowStep(step) => window.show_step(&step_words(&step)),
                    Action::Open { port, url, notes } => {
                        journal.note(&format!("window_open port={port}"));
                        // AL1.4: the link that waited is the first page after the exchange.
                        let (opening, carried) = links.opening();
                        let url = match &carried {
                            Some(target) => {
                                journal.note("link_opened");
                                url.to(target)
                            }
                            None => url,
                        };
                        window.open(port, url.as_str(), notes, opening);
                    }
                    Action::AskCode => {
                        if let Some(running) = backend.as_mut() {
                            let _ = running.send(&ToBackend::OpenCode);
                        }
                    }
                    Action::ShowFailure(fault) => {
                        links.left();
                        journal.note(&format!("failure {}", fault_name(&fault)));
                        window.show_failure(&failure(&fault, config.system, &config.data_dir, &config.version));
                    }
                    Action::BeginErase => {
                        journal.note("erase_requested");
                        marked = config.erase.set_mark().is_ok();
                        if !marked {
                            journal.note("erase_not_marked");
                        }
                    }
                    Action::Erase if !marked => {
                        // The erasure did not begin: the data are in place, and the author reads why before the app
                        // ends — the window stays until it is closed.
                        journal.note("failure not_erased");
                        window.show_failure(&failure(
                            &Fault::NotErased,
                            config.system,
                            &config.data_dir,
                            &config.version,
                        ));
                        until_closed(&receiver, &journal);
                    }
                    Action::Erase => {
                        links.left();
                        window.close();
                        journal.note("erase_started");
                        let records = keeper.send(Ask::Erase).is_ok() && erased(&receiver, &journal);
                        // The log lies in the data directory: it goes with them.
                        journal.close();
                        if config.erase.remove(config.patience).is_ok() && records {
                            // An erasure that did not end keeps its mark: the next start ends it (ER1.5).
                            let _ = config.erase.unmark();
                        }
                    }
                    Action::SendShutdown => {
                        journal.note("shutdown_sent");
                        if let Some(running) = backend.as_mut() {
                            let _ = running.send(&ToBackend::Shutdown);
                        } else if awaited.take().is_some() {
                            // The backend has not started yet — its start waits for the records: nothing to stop.
                            actions.extend(supervisor.handle(Event::Exited, Instant::now()));
                        }
                    }
                    Action::Kill => {
                        journal.note("backend_killed");
                        if let Some(running) = backend.as_mut() {
                            running.kill();
                        }
                    }
                    Action::Leave => {
                        links.left();
                        let version = updating.as_ref().and_then(|updating| updating.state.line().version);
                        journal.note("update_leaving");
                        window.leave(&installing_words(version.as_deref().unwrap_or("")));
                    }
                    Action::Install { stopped } => {
                        // AL1.5: the installer of Windows starts the app again with the arguments of this start.
                        let nsis = updating.as_ref().is_some_and(|updating| updating.setup.kind == Kind::Nsis);
                        let link_marked = nsis && mark(&config, &journal);
                        let restart = match updating.as_mut() {
                            Some(updating) => {
                                install(updating, &config.data_dir, stopped, exit_code, &window, &journal)
                            }
                            None => false,
                        };
                        if link_marked && !restart {
                            link::unmark(data_dir);
                        }
                        actions.extend(supervisor.handle(Event::Installed { restart }, Instant::now()));
                    }
                    Action::Restart => {
                        journal.note("update_restart");
                        // AL1.5: the program starts again with the arguments of this start.
                        mark(&config, &journal);
                        links.left();
                        drop(backend.take());
                        window.restart();
                        return;
                    }
                    Action::Exit => {
                        journal.note("exit");
                        links.left();
                        drop(backend.take());
                        window.exit();
                        return;
                    }
                }
            }
            // AL1.3: where no link is taken now, the link that waits goes.
            carry(links.settle(supervisor.stage()), &window, &journal);
            let heard = match receiver.recv_timeout(TICK) {
                Ok(heard) => heard,
                Err(RecvTimeoutError::Timeout) => Heard::Event(Event::Tick),
                Err(RecvTimeoutError::Disconnected) => return,
            };
            match heard {
                Heard::Records { launch, store, records } if awaited == Some(launch) => {
                    awaited = None;
                    let kind = updating.as_ref().map_or(Kind::None, |updating| updating.setup.kind);
                    let start = ToBackend::Start {
                        data_dir: &config.data_dir,
                        resources: &config.resources,
                        secrets: &records,
                        store,
                        version: &config.version,
                        update_kind: kind.name(),
                        restore: restoring,
                        bridge: config.bridge.as_ref(),
                    };
                    exit_code = None;
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
                // The records of a launch the app no longer waits for.
                Heard::Records { .. } => {}
                Heard::Link(argument) => arrive(&mut links, &argument, supervisor.stage(), &window, &journal),
                Heard::Loaded(opening) => {
                    carry(links.loaded(opening, supervisor.stage(), Instant::now()), &window, &journal);
                }
                Heard::Event(Event::Message(FromBackend::SecretSave { id, value, request })) => {
                    let _ = keeper.send(Ask::Write { id, value: Some(value), request });
                }
                Heard::Event(Event::Message(FromBackend::SecretDelete { id, request })) => {
                    let _ = keeper.send(Ask::Write { id, value: None, request });
                }
                Heard::Event(Event::Message(FromBackend::Focus)) => {
                    journal.note("backend_focus");
                    window.focus();
                }
                // OB1.1–OB1.3: the log names no number and no path of a choice.
                Heard::Event(Event::Message(FromBackend::FolderChoice { request, saga })) => {
                    journal.note("folder_choice");
                    choosing.declare(&request, &saga, Instant::now());
                }
                Heard::Event(Event::Message(FromBackend::FolderResult { request, outcome })) => {
                    journal.note(&format!("folder_result outcome={}", name(&outcome)));
                    choosing.result(&request, &outcome);
                }
                Heard::Pick { request, reply } => {
                    if let Some(saga) = choosing.pick(&request, reply.0, Instant::now()) {
                        journal.note("folder_window");
                        let heard = sender.clone();
                        let directories = config.erase.directories().to_vec();
                        let home = config.erase.home().map(str::to_string);
                        window.folder(
                            &saga,
                            Box::new(move |chosen| {
                                let checked = chosen.map(|path| {
                                    folder::check(&path, &directories, home.as_deref()).map_err(str::to_string)
                                });
                                let _ = heard.send(Heard::Chosen { request, checked });
                            }),
                        );
                    } else {
                        journal.note("folder_window_refused");
                    }
                }
                Heard::Chosen { request, checked } => {
                    journal.note(match &checked {
                        None => "folder_canceled",
                        Some(Ok(_)) => "folder_picked",
                        Some(Err(_)) => "folder_refused",
                    });
                    if let Some(path) = choosing.chosen(&request, checked, Instant::now()) {
                        let sent = backend.as_mut().is_some_and(|running| {
                            running.send(&ToBackend::FolderPicked { request: &request, path: &path }).is_ok()
                        });
                        if !sent {
                            choosing.result(&request, "stale");
                        }
                    }
                }
                Heard::Kept { request, ok } => {
                    if let Some(running) = backend.as_mut() {
                        let _ = running.send(&ToBackend::SecretResult { request: &request, ok });
                    }
                }
                // UB1.1: the backend asks only by the version the shell reported; the shell does it only for the release
                // it holds, and answers with what the update came to.
                Heard::Event(Event::Message(FromBackend::UpdateCheck)) => {
                    if let Some(updating) = updating.as_mut() {
                        if updating.state.check() {
                            journal.note("update_check");
                            updating.worker.check();
                        }
                    }
                    tell(&mut backend, &updating);
                }
                Heard::Event(Event::Message(FromBackend::UpdateDownload { version })) => {
                    if let Some(updating) = updating.as_mut() {
                        if let Some(release) = updating.state.download(&version) {
                            journal.note("update_download");
                            updating.worker.download(release);
                        }
                    }
                    tell(&mut backend, &updating);
                }
                Heard::Event(Event::Message(FromBackend::UpdateCancel { version })) => {
                    if let Some(updating) = updating.as_mut() {
                        if updating.state.cancel(&version) {
                            journal.note("update_canceled");
                            updating.worker.cancel();
                        }
                    }
                    tell(&mut backend, &updating);
                }
                Heard::Event(Event::Message(FromBackend::UpdateInstall { version })) => {
                    // UB2.9: an erasure that is marked comes first; UB2.3: only the file of that version, checked.
                    if let Some(updating) =
                        updating.as_mut().filter(|updating| !marked && updating.state.may_install(&version))
                    {
                        let leaving = supervisor.handle(Event::Install, Instant::now());
                        if !leaving.is_empty() {
                            journal.note("update_install");
                            updating.state.install();
                            actions.extend(leaving);
                        }
                    }
                    tell(&mut backend, &updating);
                }
                Heard::Update(done) => {
                    if let Some(held) = updating.as_mut() {
                        let changed = match done {
                            Done::Checked(checked) => held.state.checked(checked),
                            Done::Progress { version, percent } => held.state.progress(&version, percent),
                            Done::Downloaded(result) => {
                                journal.note(match &result {
                                    Ok(_) => "update_downloaded",
                                    Err(_) => "update_not_downloaded",
                                });
                                held.state.downloaded(result)
                            }
                        };
                        if changed {
                            tell(&mut backend, &updating);
                        }
                    }
                }
                Heard::Event(event) => {
                    note(&journal, &event);
                    if event == Event::Exited {
                        backend = None;
                        choosing.backend_gone();
                    }
                    if event == Event::Tick {
                        choosing.tick(Instant::now());
                        carry(links.tick(supervisor.stage(), Instant::now()), &window, &journal);
                    }
                    if event == Event::Message(FromBackend::Erase) {
                        // UB2.9: the erasure comes before an update: the download, the wait and an installation that has
                        // not begun stop.
                        if let Some(updating) = updating.as_mut() {
                            updating.worker.cancel();
                            updating.state.forget();
                        }
                    }
                    let ready = matches!(event, Event::Message(FromBackend::Ready { .. }));
                    actions.extend(supervisor.handle(event, Instant::now()));
                    if ready && supervisor.port().is_some() {
                        // UB1.1: a backend started again loses nothing of the update.
                        tell(&mut backend, &updating);
                        if std::mem::take(&mut first_ready) {
                            if let Some(updating) = updating.as_ref() {
                                // UB2.8: what an update of an earlier version left behind.
                                let removed = update::remove_leftovers(&updating.setup.programs.temp);
                                if removed > 0 {
                                    journal.note(&format!("update_leftovers_removed count={removed}"));
                                }
                            }
                        }
                    }
                }
                // The answer of an erasure the driver no longer waits for.
                Heard::Erased { .. } => {}
                Heard::Unreadable(_) => journal.note("control_line_unreadable"),
                Heard::ExitCode(code) => {
                    exit_code = Some(code);
                    journal.note(&match code {
                        Some(code) => format!("backend_exit code={code}"),
                        None => "backend_exit signal".to_string(),
                    })
                }
            }
        }
    });
    (handle, thread)
}

/// A link came (specs/2026-11-16-desk-app-links.md AL1.1–AL1.3): checked — a refusal is noted by its reason and leads to the
/// page that says so —, then it waits, moves the window or is dropped.
fn arrive<W: Window>(links: &mut Links, argument: &Argument, stage: link::Stage, window: &W, journal: &Journal) {
    let (target, refusal) = link::target(argument);
    if let Some(refusal) = refusal {
        journal.note(&format!("link_refused {}", refusal.name()));
    }
    carry(links.arrive(target, stage, Instant::now()), window, journal);
}

/// Does what the links ask (AL1.4, AL1.7): the log names the fact and the reason, never a part of a link.
fn carry<W: Window>(done: Vec<link::Done>, window: &W, journal: &Journal) {
    for done in done {
        match done {
            link::Done::Visit(target) => {
                journal.note("link_opened");
                window.visit(&target);
            }
            link::Done::Raise => window.focus(),
            link::Done::Merged => journal.note("link_merged"),
            link::Done::Dropped(why) => journal.note(&format!("link_dropped {}", why.name())),
        }
    }
}

/// Marks the link of the first start before the program starts again (AL1.5); says whether it marked one.
fn mark(config: &Config, journal: &Journal) -> bool {
    let Some(argument) = &config.link else { return false };
    if link::mark(Path::new(&config.data_dir), argument).is_err() {
        journal.note("link_mark_not_written");
        return false;
    }
    true
}

/// Installs the update the shell holds once the backend is gone (UB2.3–UB2.7) and says whether the app starts again:
/// only a backend that stopped by itself and cleanly, with the cluster of the data directory stopped and no program of the
/// installation running, lets an installation begin. Anything else is `install_failed` — or `install_refused` — with
/// nothing changed, and the backend starts again; a package of Linux is kept for the author.
fn install<W: Window>(
    updating: &mut Updating,
    data_dir: &str,
    stopped: bool,
    exit_code: Option<Option<i32>>,
    window: &W,
    journal: &Journal,
) -> bool {
    let kind = updating.setup.kind;
    let Some(file) = updating.state.file().cloned() else {
        updating.state.install_failed(Cause::NotLaunched, None);
        return false;
    };
    let failed = |updating: &mut Updating, cause: Cause| {
        let kept = package::keep(&file, kind, &updating.setup.programs).map(|path| path.to_string_lossy().into_owned());
        updating.state.install_failed(cause, kept);
        false
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
            match package::install(&file, kind, &updating.setup.policy.keys, &updating.setup.programs) {
                package::Outcome::Installed => true,
                package::Outcome::Refused(refusal, kept) => {
                    journal.note("update_install_refused");
                    updating.state.install_refused(refusal, kept.to_string_lossy().into_owned());
                    false
                }
                package::Outcome::NotRun(kept) => {
                    journal.note("update_not_installed cause=package");
                    updating
                        .state
                        .install_failed(Cause::NotLaunched, kept.map(|path| path.to_string_lossy().into_owned()));
                    false
                }
                package::Outcome::Unfinished(kept) => {
                    // UB2.6: the system may be half done: the app starts again whole, and the start of the version on the disk
                    // decides; the note says what happened.
                    journal.note("update_install_unfinished");
                    let note = UnfinishedNote {
                        version: file.release.version.to_string(),
                        package: kept.map(|path| path.to_string_lossy().into_owned()),
                    };
                    if update::write_note(Path::new(data_dir), &note).is_err() {
                        journal.note("update_note_not_written");
                    }
                    true
                }
            }
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
            install_file(updating, window, journal, &file, kind)
        }
        Kind::None => failed(updating, Cause::NotLaunched),
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

/// What the keeper of the secrets is asked.
enum Ask {
    /// The records of the set for the `start` of a launch of the backend.
    Records { launch: u64 },
    /// A record the backend asks to keep (`value`) or to remove.
    Write { id: String, value: Option<Secret>, request: String },
    /// Every record of the set, wherever it lies: «Стереть все данные».
    Erase,
}

/// Waits for the author to close the window of a failure that ends the app; nothing else is heard meanwhile — a link is
/// dropped by its line (specs/2026-11-16-desk-app-links.md AL1.7).
fn until_closed(receiver: &mpsc::Receiver<Heard>, journal: &Journal) {
    while let Ok(heard) = receiver.recv() {
        match heard {
            Heard::Event(Event::Close) => return,
            Heard::Link(_) => journal.note(&format!("link_dropped {}", link::Dropped::Failure.name())),
            _ => {}
        }
    }
}

/// Waits for the keeper to remove the records of the set; the backend is gone, so whatever else comes is not heard — a link
/// is dropped by its line (AL1.7).
fn erased(receiver: &mpsc::Receiver<Heard>, journal: &Journal) -> bool {
    let deadline = Instant::now() + erase::RECORDS_TIMEOUT + TICK;
    loop {
        match receiver.recv_timeout(deadline.saturating_duration_since(Instant::now())) {
            Ok(Heard::Erased { ok }) => return ok,
            Ok(Heard::Link(_)) => journal.note(&format!("link_dropped {}", link::Dropped::Erasing.name())),
            Ok(_) => {}
            Err(_) => return false,
        }
    }
}

/// The thread that owns the secrets of the app: it chooses their store, then does what it is asked one thing after
/// another — so the records of a `start` hold every write asked before it, and a store of the system that waits (a
/// locked keyring asks the author to unlock it) never holds up the supervision. The log names a record only by an id of
/// the set.
fn keeper(
    file: PathBuf,
    keyring: Arc<dyn Keyring>,
    windows: bool,
    journal: Journal,
    heard: Sender<Heard>,
) -> Sender<Ask> {
    let (sender, asks) = mpsc::channel::<Ask>();
    thread::spawn(move || {
        let (secrets, opened) = Secrets::open(file, keyring, windows, PROBE_TIMEOUT);
        journal.note(&format!("secrets_store store={}", secrets.store().name()));
        match opened {
            Opened::Moved => journal.note("secrets_moved"),
            Opened::NotMoved => journal.note("secrets_not_moved"),
            Opened::Plain => {}
        }
        for ask in asks {
            let told = match ask {
                Ask::Records { launch } => {
                    Heard::Records { launch, store: secrets.store(), records: secrets.read_all() }
                }
                Ask::Write { id, value, request } => {
                    let kept = match &value {
                        Some(value) => secrets.save(&id, value),
                        None => secrets.delete(&id),
                    };
                    let id = if secrets::known(&id) { id.as_str() } else { "other" };
                    let what = if value.is_some() { "save" } else { "delete" };
                    journal.note(&match kept {
                        Ok(()) => format!("secret_{what} id={id}"),
                        Err(SecretError::Unknown | SecretError::TooLong) => format!("secret_{what}_refused id={id}"),
                        Err(SecretError::Store) => format!("secret_{what}_failed id={id}"),
                    });
                    Heard::Kept { request, ok: kept.is_ok() }
                }
                Ask::Erase => {
                    let ok = secrets.erase(erase::RECORDS_TIMEOUT).is_ok();
                    journal.note(if ok { "secrets_erased" } else { "secrets_not_erased" });
                    Heard::Erased { ok }
                }
            };
            if heard.send(told).is_err() {
                return;
            }
        }
    });
    sender
}

fn fault_name(fault: &Fault) -> &str {
    match fault {
        Fault::Program => "program",
        Fault::Fatal { reason, .. } => name(reason),
        Fault::Unknown => "unknown",
        Fault::Unerased { .. } => "unerased",
        Fault::KeyKept => "key_kept",
        Fault::NotErased => "not_erased",
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
        Event::Message(FromBackend::Step(step)) => journal.note(match step {
            Step::Backup { .. } => "backend_step step=backup",
            Step::Migrate { .. } => "backend_step step=migrate",
            Step::Restore { .. } => "backend_step step=restore",
        }),
        // The keeper notes what it did with them; the driver notes the erasure, the raised window and the updates.
        Event::Message(
            FromBackend::SecretSave { .. }
            | FromBackend::SecretDelete { .. }
            | FromBackend::Erase
            | FromBackend::Focus
            | FromBackend::FolderChoice { .. }
            | FromBackend::FolderResult { .. }
            | FromBackend::UpdateCheck
            | FromBackend::UpdateDownload { .. }
            | FromBackend::UpdateInstall { .. }
            | FromBackend::UpdateCancel { .. },
        ) => {}
        Event::Refused(status) => journal.note(&format!("window_code_refused status={status}")),
        Event::Close => journal.note("close_requested"),
        Event::Restore => journal.note("restore_heard"),
        Event::Left => journal.note("update_left"),
        Event::SpawnFailed | Event::Exited | Event::Tick | Event::Install | Event::Installed { .. } => {}
    }
}
