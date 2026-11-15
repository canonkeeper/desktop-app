// SPDX-License-Identifier: AGPL-3.0-only

//! The commands of the page of the start (specs/2026-11-13-desk-upgrade.md UG3.2, UG3.4; capabilities/start.json): what
//! the page shows — the start, its step or its failure with the buttons — and the acts of those buttons. What a failure
//! shows and allows is the core crate's (`launch`); here it meets the browser, the file manager and the supervision.

use std::sync::Arc;

use canonkeeper_desktop_core::launch::{Launch, Question, Screen};
use canonkeeper_desktop_core::paths::System;
use canonkeeper_desktop_core::words::CONTACT_URL;
use tauri::State;

use super::{Shell, VERSION};

/// The commands of the page of the start (specs/2026-11-13-desk-upgrade.md UG3.2): none takes a name, a path or an
/// address from the page; each does its act only while the page shows a failure with a button for it.
fn launched<T>(shell: &Shell, read: impl FnOnce(&Launch) -> Option<T>) -> Result<T, String> {
    let found = shell.launch.lock().ok().and_then(|launch| read(&launch));
    found.ok_or_else(|| {
        shell.journal.note("launch_command_refused");
        "refused".to_string()
    })
}

/// What the page of the start shows: the start, its step or its failure with the buttons.
#[tauri::command]
pub fn launch_state(shell: State<'_, Arc<Shell>>) -> Screen {
    shell.launch.lock().map(|launch| launch.screen()).unwrap_or(Screen::Starting)
}

/// «Написать нам» of a failure: the contact page of the site in the browser of the system.
#[tauri::command]
pub fn write_to_us(shell: State<'_, Arc<Shell>>) -> Result<(), String> {
    launched(&shell, |launch| launch.may_contact().then_some(()))?;
    open::that_detached(CONTACT_URL).map_err(|_| "not opened".to_string())
}

/// «Скачать…»: the page of the release of the version of the button.
#[tauri::command]
pub fn download_version(shell: State<'_, Arc<Shell>>) -> Result<(), String> {
    let address = launched(&shell, |launch| launch.download(VERSION))?;
    open::that_detached(address).map_err(|_| "not opened".to_string())
}

/// «Открыть папку с копией»: the directory of the copy of the reason in the file manager.
#[tauri::command]
pub fn open_copy(shell: State<'_, Arc<Shell>>) -> Result<(), String> {
    let folder = launched(&shell, |launch| launch.copy_folder(System::CURRENT, &shell.data_dir))?;
    open::that_detached(folder).map_err(|_| "not opened".to_string())
}

/// The question before a return of the data from the copy (UG4.4).
#[tauri::command]
pub fn restore_question(shell: State<'_, Arc<Shell>>) -> Result<Question, String> {
    let asked = shell.launch.lock().ok().and_then(|mut launch| launch.ask_restore());
    asked.ok_or_else(|| {
        shell.journal.note("launch_command_refused");
        "refused".to_string()
    })
}

/// «Вернуть данные»: the backend starts again and brings the data back from the copy it named (UG4.1).
#[tauri::command]
pub fn restore_copy(shell: State<'_, Arc<Shell>>) -> Result<(), String> {
    launched(&shell, |launch| launch.may_restore().then_some(()))?;
    let supervision = shell.supervision.get().ok_or_else(|| "refused".to_string())?;
    shell.journal.note("restore_asked");
    supervision.restore();
    Ok(())
}
