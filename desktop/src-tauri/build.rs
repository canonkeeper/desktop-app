// SPDX-License-Identifier: AGPL-3.0-only

// The commands of the shell get permissions of their own (allow-<command>): with an app manifest every command is
// refused to a page whose capability does not name it — the page of the shell and the page of the backend alike
// (specs/2026-11-12-desk-shell.md SH3.2).
fn main() {
    tauri_build::try_build(tauri_build::Attributes::new().app_manifest(tauri_build::AppManifest::new().commands(&[
        "open_external",
        "reveal_data_dir",
        "launch_notes",
        "close_window",
    ])))
    .expect("the build of the shell failed");
}
