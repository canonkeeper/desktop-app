// SPDX-License-Identifier: AGPL-3.0-only

//! The shell of the CanonKeeper desktop app without its window (specs/2026-11-12-desk-shell.md SH1–SH3): the lines of
//! the control channel with the backend, the data directory and the plain paths of Windows, the words of a failure,
//! where the window may go, and the supervision of the backend process as a state machine. The window crate maps the
//! actions of this crate onto Tauri; it holds no business logic, and neither does this crate.

pub mod backend;
pub mod control;
pub mod driver;
pub mod journal;
pub mod navigation;
pub mod paths;
pub mod supervisor;
pub mod words;
