// SPDX-License-Identifier: AGPL-3.0-only

//! The system window that chooses the folder of an Obsidian vault (specs/2026-11-13-desk-obsidian.md OB1.2): the command
//! of the page asks for it by the number the backend declared and hears only how the choice ended; the window opens over
//! the window of the app without waiting, and its folder goes to the supervision, never to a page. On Linux it is the
//! chooser of GTK the window already runs on its main thread; elsewhere the dialog of the system through `rfd`. No plugin
//! of Tauri is here: their commands would give a page the paths.

use std::path::PathBuf;
use std::sync::{Arc, Mutex};

use canonkeeper_desktop_core::folder::{self as choice, Outcome};
use tauri::{AppHandle, Manager, State};

use super::{Shell, MAIN};

/// What the window tells once it closes: the folder, or nothing — the author canceled.
pub type Chosen = Box<dyn FnOnce(Option<PathBuf>) + Send>;

/// Opens the system window that chooses the folder of a vault for the number the backend declared, once, and answers the
/// page only how the choice ended: linked, refused with its reason, canceled or expired.
#[tauri::command]
pub async fn pick_folder(request: String, shell: State<'_, Arc<Shell>>) -> Result<serde_json::Value, String> {
    let Some(supervision) = shell.supervision.get() else { return Ok(Outcome::Expired.json()) };
    let heard = supervision.pick_folder(&request);
    let outcome = tauri::async_runtime::spawn_blocking(move || heard.recv().unwrap_or(Outcome::Expired))
        .await
        .unwrap_or(Outcome::Expired);
    Ok(outcome.json())
}

/// The window tells once: whichever of the main thread and the failure to reach it comes first.
fn once(chosen: Chosen) -> Arc<Mutex<Option<Chosen>>> {
    Arc::new(Mutex::new(Some(chosen)))
}

fn tell(chosen: &Mutex<Option<Chosen>>, folder: Option<PathBuf>) {
    if let Some(chosen) = chosen.lock().ok().and_then(|mut chosen| chosen.take()) {
        chosen(folder);
    }
}

/// Opens the system window over the window of the app on the main thread of GTK and returns at once (OB1.2).
#[cfg(target_os = "linux")]
pub fn open(app: &AppHandle, saga: &str, chosen: Chosen) {
    use gtk::prelude::*;
    use std::cell::RefCell;
    use std::rc::Rc;

    let chosen = once(chosen);
    let title = choice::window_title(saga);
    let (main, telling) = (app.clone(), Arc::clone(&chosen));
    let opened = app.run_on_main_thread(move || {
        let parent = main.get_webview_window(MAIN).and_then(|window| window.gtk_window().ok());
        let dialog = gtk::FileChooserNative::new(
            Some(&title),
            parent.as_ref(),
            gtk::FileChooserAction::SelectFolder,
            Some(choice::ACCEPT),
            Some(choice::CANCEL),
        );
        dialog.set_modal(true);
        // The chooser lives until it answers: the handler holds it, and lets it go once it told.
        let held = Rc::new(RefCell::new(Some(dialog.clone())));
        let holding = Rc::clone(&held);
        dialog.connect_response(move |dialog, response| {
            let folder = if response == gtk::ResponseType::Accept { dialog.filename() } else { None };
            tell(&telling, folder);
            holding.borrow_mut().take();
        });
        dialog.show();
    });
    if opened.is_err() {
        tell(&chosen, None);
    }
}

/// Opens the dialog of the system over the window of the app and waits for it on a thread of its own (OB1.2).
#[cfg(not(target_os = "linux"))]
pub fn open(app: &AppHandle, saga: &str, chosen: Chosen) {
    let chosen = once(chosen);
    let title = choice::window_title(saga);
    let (main, telling) = (app.clone(), Arc::clone(&chosen));
    let opened = app.run_on_main_thread(move || {
        let mut dialog = rfd::AsyncFileDialog::new().set_title(&title);
        if let Some(window) = main.get_webview_window(MAIN) {
            dialog = dialog.set_parent(&window);
        }
        let picking = dialog.pick_folder();
        std::thread::spawn(move || {
            let folder = tauri::async_runtime::block_on(picking).map(|handle| handle.path().to_path_buf());
            tell(&telling, folder);
        });
    });
    if opened.is_err() {
        tell(&chosen, None);
    }
}
