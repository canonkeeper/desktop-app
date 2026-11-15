// SPDX-License-Identifier: AGPL-3.0-only

//! The shell of the CanonKeeper desktop app (specs/2026-11-12-desk-shell.md; specs/2026-11-12-desktop-target.md
//! DT13.1): one window on the pages of its own backend, the backend started and watched, plain words for the author
//! when it cannot start. It holds no business logic: the logic without the window lives in crates/core.
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod shell;

fn main() {
    // specs/2026-11-15-desk-mcp.md MC1.1: an AppImage run with the first argument mcp is the program of the bridge for the
    // programs of the author — before the lock of one copy and before Tauri; a failed exec, or a program of the app whose place
    // current_exe does not give, ends with the code 1 and a line on stderr, no window.
    #[cfg(unix)]
    {
        use std::os::unix::process::CommandExt;
        let arguments: Vec<std::ffi::OsString> = std::env::args_os().skip(1).collect();
        let directory =
            std::env::current_exe().ok().and_then(|program| program.parent().map(std::path::Path::to_path_buf));
        let code = canonkeeper_desktop_core::mcp::replace(&arguments, directory.as_deref(), |program, rest| {
            std::process::Command::new(program).args(rest).exec()
        });
        if let Some(code) = code {
            std::process::exit(code);
        }
    }
    shell::run();
}
