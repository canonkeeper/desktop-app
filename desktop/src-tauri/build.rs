// SPDX-License-Identifier: AGPL-3.0-only

// The commands of the shell get permissions of their own (allow-<command>): with an app manifest every command is
// refused to a page whose capability does not name it — the page of the shell and the page of the backend alike
// (specs/2026-11-12-desk-shell.md SH3.2). The page of the backend has the first five — the fifth chooses the folder of
// an Obsidian vault (specs/2026-11-13-desk-obsidian.md OB1.2) —; the page of the start has its own
// (capabilities/start.json; specs/2026-11-13-desk-upgrade.md UG3.4).
fn main() {
    tauri_build::try_build(tauri_build::Attributes::new().app_manifest(tauri_build::AppManifest::new().commands(&[
        "open_external",
        "reveal_data_dir",
        "launch_notes",
        "close_window",
        "pick_folder",
        "launch_state",
        "write_to_us",
        "download_version",
        "open_copy",
        "restore_question",
        "restore_copy",
    ])))
    .expect("the build of the shell failed");
}
