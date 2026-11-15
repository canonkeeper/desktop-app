// SPDX-License-Identifier: AGPL-3.0-only

//! The shell of the CanonKeeper desktop app without its window (specs/2026-11-12-desk-shell.md SH1–SH3): the lines of
//! the control channel with the backend, the data directory and the plain paths of Windows, the words of a failure,
//! where the window may go, the secrets of the app (specs/2026-11-13-desk-secrets.md SE1), «Стереть все данные»
//! (specs/2026-11-13-desk-erase.md), the page of the start and its commands (specs/2026-11-13-desk-upgrade.md UG3), the
//! updates of the app (specs/2026-11-13-desk-updates.md), the choice of the folder of an Obsidian vault
//! (specs/2026-11-13-desk-obsidian.md) and the supervision of the backend process as a state machine. The window crate maps the actions of this crate onto Tauri; it holds no business
//! logic, and neither does this crate.

pub mod backend;
pub mod control;
pub mod driver;
pub mod erase;
pub mod folder;
pub mod journal;
pub mod launch;
pub mod mcp;
pub mod navigation;
pub mod paths;
pub mod secrets;
pub mod supervisor;
pub mod update;
pub mod words;
