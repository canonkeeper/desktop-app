// SPDX-License-Identifier: AGPL-3.0-only

//! The shell of the CanonKeeper desktop app (specs/2026-11-12-desk-shell.md; specs/2026-11-12-desktop-target.md
//! DT13.1): one window on the pages of its own backend, the backend started and watched, plain words for the author
//! when it cannot start. It holds no business logic: the logic without the window lives in crates/core.
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod shell;

fn main() {
    shell::run();
}
