// SPDX-License-Identifier: AGPL-3.0-only

//! The window and the commands of the shell on Tauri (specs/2026-11-12-desk-shell.md SH1–SH3). The decisions — where
//! the window may go, what the words of a failure are, when the backend restarts — are the core crate's; here they meet
//! the window: one instance per user, one window, the page of the backend allowed its four commands on its exact origin.

use std::collections::HashSet;
use std::error::Error;
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicBool, AtomicU16, Ordering};
use std::sync::{Arc, Mutex, OnceLock};
use std::time::SystemTime;

use canonkeeper_desktop_core::driver::{self, Config, Handle, Window};
use canonkeeper_desktop_core::journal::Journal;
use canonkeeper_desktop_core::navigation::{self, Navigation, EXCHANGE_SCRIPT};
use canonkeeper_desktop_core::paths::{self, Places, System};
use canonkeeper_desktop_core::supervisor::{Notes, STOP_TIMEOUT};
use canonkeeper_desktop_core::words::{words, Fault};
use tauri::ipc::CapabilityBuilder;
#[cfg(target_os = "linux")]
use tauri::webview::DownloadEvent;
use tauri::webview::{NewWindowResponse, PageLoadEvent};
use tauri::{AppHandle, Manager, RunEvent, State, Url, WebviewUrl, WebviewWindowBuilder, WindowEvent};

const MAIN: &str = "main";

/// The commands the page of the backend may call (SH3.2); the page of the shell has its own list in
/// capabilities/start.json.
const PAGE_COMMANDS: [&str; 4] =
    ["allow-open-external", "allow-reveal-data-dir", "allow-launch-notes", "allow-close-window"];

/// What the window, its commands and the supervision share.
struct Shell {
    data_dir: String,
    journal: Journal,
    /// The port of the page of the backend while it is ready; 0 — none.
    port: AtomicU16,
    /// The ports whose origin got the capability.
    granted: Mutex<HashSet<u16>>,
    notes: Mutex<Notes>,
    supervision: OnceLock<Handle>,
    closing: AtomicBool,
    exiting: AtomicBool,
}

impl Shell {
    fn port(&self) -> Option<u16> {
        match self.port.load(Ordering::SeqCst) {
            0 => None,
            port => Some(port),
        }
    }

    /// Closing the window, the command `close_window` and the end of the session of the system all come here: the
    /// window hides, the backend gets `shutdown` and the app ends once it exited (SH2.3).
    fn close(&self, app: &AppHandle) {
        if self.closing.swap(true, Ordering::SeqCst) {
            return;
        }
        if let Some(window) = app.get_webview_window(MAIN) {
            let _ = window.hide();
        }
        match self.supervision.get() {
            Some(supervision) => supervision.close(),
            None => {
                self.exiting.store(true, Ordering::SeqCst);
                app.exit(0);
            }
        }
    }
}

/// The window as the supervision drives it.
struct TauriWindow {
    app: AppHandle,
    shell: Arc<Shell>,
}

impl TauriWindow {
    fn go(&self, address: &str) {
        if let (Some(window), Ok(url)) = (self.app.get_webview_window(MAIN), Url::parse(address)) {
            if window.navigate(url).is_err() {
                self.shell.journal.note("navigate_failed");
            }
        }
    }
}

impl Window for TauriWindow {
    fn show_starting(&self) {
        self.shell.port.store(0, Ordering::SeqCst);
        self.go(&navigation::starting_page(System::CURRENT));
    }

    fn open(&self, port: u16, url: &str, notes: Notes) {
        let fresh = self.shell.granted.lock().map(|mut granted| granted.insert(port)).unwrap_or(false);
        if fresh {
            // Only the exact origin of this run of the backend may call the commands (DA3.8).
            let mut capability = CapabilityBuilder::new(format!("backend-{port}"))
                .remote(format!("http://127.0.0.1:{port}"))
                .local(false)
                .window(MAIN);
            for permission in PAGE_COMMANDS {
                capability = capability.permission(permission);
            }
            if self.app.add_capability(capability).is_err() {
                self.shell.journal.note("capability_refused");
            }
        }
        if let Ok(mut kept) = self.shell.notes.lock() {
            *kept = kept.merge(notes);
        }
        self.shell.port.store(port, Ordering::SeqCst);
        self.go(url);
    }

    fn show_failure(&self, words: &str) {
        self.shell.port.store(0, Ordering::SeqCst);
        self.go(&navigation::failure_page(System::CURRENT, words));
        if let Some(window) = self.app.get_webview_window(MAIN) {
            let _ = window.unminimize();
            let _ = window.show();
            let _ = window.set_focus();
        }
    }

    fn exit(&self) {
        self.shell.exiting.store(true, Ordering::SeqCst);
        self.app.exit(0);
    }
}

/// Opens an address in the browser of the system: only `https:` to a named host and `mailto:` (SH3.2).
#[tauri::command]
fn open_external(url: String, shell: State<'_, Arc<Shell>>) -> Result<(), String> {
    let Some(address) = navigation::external(&url) else {
        shell.journal.note("external_refused");
        return Err("refused".into());
    };
    open::that_detached(address).map_err(|_| "not opened".to_string())
}

/// «Открыть папку»: the data directory in the file manager of the system.
#[tauri::command]
fn reveal_data_dir(shell: State<'_, Arc<Shell>>) -> Result<(), String> {
    if shell.data_dir.is_empty() {
        return Err("no data directory".into());
    }
    open::that_detached(&shell.data_dir).map_err(|_| "not opened".to_string())
}

/// What the page learns once after it opens: the address changed, the backend was restarted after a fall (SH1.4,
/// SH2.2). A second call returns nothing new.
#[tauri::command]
fn launch_notes(shell: State<'_, Arc<Shell>>) -> Notes {
    shell.notes.lock().map(|mut notes| std::mem::take(&mut *notes)).unwrap_or_default()
}

/// Closes the app as the button of the window does; the page asks its own question first (SH2.3).
#[tauri::command]
fn close_window(app: AppHandle, shell: State<'_, Arc<Shell>>) {
    shell.close(&app);
}

/// Decides a navigation of the window (SH3.1).
fn navigate(shell: &Shell, url: &Url) -> bool {
    if let Some(status) = navigation::refused(url.as_str(), shell.port()) {
        if let Some(supervision) = shell.supervision.get() {
            supervision.refused(status);
        }
        return false;
    }
    match navigation::navigation(url.as_str(), shell.port(), System::CURRENT) {
        Navigation::Window => true,
        // The webview saves the file into the folder of downloads under a free name; the window stays on its page.
        Navigation::Save => {
            shell.journal.note("file_save");
            true
        }
        Navigation::Browser(address) => {
            shell.journal.note("browser_opened");
            let _ = open::that_detached(address);
            false
        }
        Navigation::Refuse => {
            shell.journal.note("navigation_refused");
            false
        }
    }
}

/// A file the page saves, on Linux: only from its own origin, into the folder of downloads or else the home folder
/// (`save_path`). Windows keeps the download panel of WebView2 — a handler here would hide it.
#[cfg(target_os = "linux")]
fn download(app: &AppHandle, shell: &Shell, event: DownloadEvent<'_>) -> bool {
    match event {
        DownloadEvent::Requested { url, destination } => {
            if !navigation::saves(url.as_str(), shell.port()) {
                shell.journal.note("file_save_refused");
                return false;
            }
            let downloads = app.path().download_dir().ok();
            let home = app.path().home_dir().ok();
            *destination = navigation::save_path(destination, downloads.as_deref(), home.as_deref());
            true
        }
        DownloadEvent::Finished { success, .. } => {
            shell.journal.note(if success { "file_saved" } else { "file_save_failed" });
            true
        }
        _ => true,
    }
}

fn text(path: PathBuf) -> Option<String> {
    path.into_os_string().into_string().ok()
}

/// The data directory: the environment first, as the backend reads it, then the folders the system names.
fn data_dir(app: &AppHandle) -> Result<String, paths::PathError> {
    let local_app_data = std::env::var("LOCALAPPDATA").ok().or_else(|| app.path().local_data_dir().ok().and_then(text));
    let xdg_data_home = std::env::var("XDG_DATA_HOME").ok();
    let home = std::env::var("HOME").ok().or_else(|| app.path().home_dir().ok().and_then(text));
    paths::data_dir(
        System::CURRENT,
        Places {
            local_app_data: local_app_data.as_deref(),
            xdg_data_home: xdg_data_home.as_deref(),
            home: home.as_deref(),
        },
    )
}

/// The backend next to the shell (SH1.3).
fn backend_program() -> Option<PathBuf> {
    let name = if cfg!(windows) { "canonkeeper-backend.exe" } else { "canonkeeper-backend" };
    Some(std::env::current_exe().ok()?.parent()?.join(name))
}

fn setup(app: &AppHandle) -> Result<(), Box<dyn Error>> {
    let system = System::CURRENT;
    let data_dir = data_dir(app);
    let journal = match &data_dir {
        Ok(data_dir) => Journal::open(Path::new(&system.join(data_dir, &["logs"])), SystemTime::now()),
        Err(_) => Journal::default(),
    };
    journal.note("shell_started");
    let shell = Arc::new(Shell {
        data_dir: data_dir.clone().unwrap_or_default(),
        journal,
        port: AtomicU16::new(0),
        granted: Mutex::new(HashSet::new()),
        notes: Mutex::new(Notes::default()),
        supervision: OnceLock::new(),
        closing: AtomicBool::new(false),
        exiting: AtomicBool::new(false),
    });
    app.manage(Arc::clone(&shell));

    let navigating = Arc::clone(&shell);
    let opening = Arc::clone(&shell);
    let loading = Arc::clone(&shell);
    let builder = WebviewWindowBuilder::new(app, MAIN, WebviewUrl::App("index.html".into()));
    #[cfg(target_os = "linux")]
    let builder = {
        let (saving, saver) = (Arc::clone(&shell), app.clone());
        builder.on_download(move |_webview, event| download(&saver, &saving, event))
    };
    builder
        .title("CanonKeeper")
        .inner_size(1280.0, 820.0)
        .min_inner_size(720.0, 520.0)
        .center()
        // HTML drag and drop (books onto the shelf) needs the page to keep it on Windows.
        .disable_drag_drop_handler()
        .on_navigation(move |url| navigate(&navigating, url))
        .on_new_window(move |url, _features| {
            match navigation::navigation(url.as_str(), opening.port(), System::CURRENT) {
                Navigation::Browser(address) => {
                    opening.journal.note("browser_opened");
                    let _ = open::that_detached(address);
                }
                _ => opening.journal.note("new_window_refused"),
            }
            NewWindowResponse::Deny
        })
        // The page of the exchange trades its code for the session of the window before any script of the page runs
        // (DA3.2, DA3.3); on every other page the script does nothing.
        .initialization_script(EXCHANGE_SCRIPT)
        .on_page_load(move |_window, payload| {
            let address = payload.url().as_str();
            if payload.event() == PageLoadEvent::Finished
                && navigation::is_own_page(address, loading.port())
                && !navigation::is_open_page(address, loading.port())
            {
                // A page of the backend after the exchange: the window shows the app (the smoke run of CI waits for it).
                loading.journal.note("page_loaded");
            }
        })
        .build()?;

    let window = TauriWindow { app: app.clone(), shell: Arc::clone(&shell) };
    let data_dir = match data_dir {
        Ok(data_dir) => data_dir,
        Err(_) => {
            shell.journal.note("data_directory_unknown");
            window.show_failure(&words(&Fault::Unknown, system, "—"));
            return Ok(());
        }
    };
    let resources = app
        .path()
        .resource_dir()
        .ok()
        .and_then(text)
        .ok_or(paths::PathError::Unknown)
        .and_then(|path| paths::plain(system, &path));
    let (Ok(resources), Some(program)) = (resources, backend_program()) else {
        shell.journal.note("resources_unknown");
        let fault = Fault::Fatal { reason: "resources".into(), backup: None };
        window.show_failure(&words(&fault, system, &data_dir));
        return Ok(());
    };
    let config = Config { program, data_dir, resources, system, stop_timeout: STOP_TIMEOUT };
    let (supervision, _thread) = driver::run(config, window, shell.journal.clone());
    let _ = shell.supervision.set(supervision);
    Ok(())
}

pub fn run() {
    tauri::Builder::default()
        // First: a second start of the app brings the window of the first one forward and ends (SH1.2).
        .plugin(tauri_plugin_single_instance::init(|app, _arguments, _directory| {
            let closing = app.try_state::<Arc<Shell>>().is_some_and(|shell| shell.closing.load(Ordering::SeqCst));
            if let (false, Some(window)) = (closing, app.get_webview_window(MAIN)) {
                let _ = window.unminimize();
                let _ = window.show();
                let _ = window.set_focus();
            }
        }))
        .invoke_handler(tauri::generate_handler![open_external, reveal_data_dir, launch_notes, close_window])
        .setup(|app| setup(app.handle()))
        .on_window_event(|window, event| {
            if let WindowEvent::CloseRequested { api, .. } = event {
                api.prevent_close();
                if let Some(shell) = window.app_handle().try_state::<Arc<Shell>>() {
                    shell.close(window.app_handle());
                }
            }
        })
        .build(tauri::generate_context!())
        .expect("the shell of CanonKeeper failed to start")
        .run(|app, event| {
            // The system ends the session, or the last window went: the backend stops first.
            if let RunEvent::ExitRequested { code: None, api, .. } = event {
                if let Some(shell) = app.try_state::<Arc<Shell>>() {
                    if !shell.exiting.load(Ordering::SeqCst) {
                        api.prevent_exit();
                        shell.close(app);
                    }
                }
            }
        });
}
