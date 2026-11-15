// SPDX-License-Identifier: AGPL-3.0-only

//! The updates of the app on Tauri (specs/2026-11-13-desk-updates.md UB1.7, UB2.4, UB2.5, UB2.8): how this copy was
//! installed, and the installer of Windows and the AppImage put in place by the updater plugin with the bytes the core
//! downloaded and checked. The network, the checks, the packages of Linux and the order of the stop are the core
//! crate's; the plugin makes no request here — `restore_update` builds the update from `latest.json` the core proved —
//! and its own step before the installer of Windows only writes the log: the window stays and the lock of one instance is
//! kept until the installer runs, so an installer that did not start leaves the app as it was.

use std::path::PathBuf;

use canonkeeper_desktop_core::journal::Journal;
use canonkeeper_desktop_core::mcp;
use canonkeeper_desktop_core::update::fetch::Downloaded;
use canonkeeper_desktop_core::update::package::Programs;
use canonkeeper_desktop_core::update::policy::Policy;
use canonkeeper_desktop_core::update::state::Cause;
use canonkeeper_desktop_core::update::{Kind, Setup};
use tauri::{AppHandle, Manager};
use tauri_plugin_updater::UpdaterExt;

/// How this copy of the app was installed (UB1.7): the mark the bundler wrote into the program, an AppImage only while
/// it runs from its file.
pub fn kind(app: &AppHandle) -> Kind {
    let bundle = tauri::utils::platform::bundle_type().map(|bundle| bundle.to_string());
    Kind::of(bundle.as_deref(), appimage(app).is_some())
}

/// The file of the AppImage this copy runs from (UB1.7; specs/2026-11-15-desk-mcp.md MC3.2): only Linux has one — Tauri
/// knows the variable only there, and on Windows the app is the installation of NSIS.
#[cfg(target_os = "linux")]
pub fn appimage(app: &AppHandle) -> Option<PathBuf> {
    app.env().appimage.map(PathBuf::from)
}

#[cfg(not(target_os = "linux"))]
pub fn appimage(_app: &AppHandle) -> Option<PathBuf> {
    None
}

/// What the updates need: the policy of the build, the programs of a package of Linux, the folders where a package is
/// kept, and what runs from the installation — the backend and the bundle, on Windows the directory of the installation.
pub fn setup(app: &AppHandle, backend: PathBuf, resources: PathBuf) -> Setup {
    let kind = kind(app);
    let (holders, bridges) = match (kind, backend.parent()) {
        // specs/2026-11-15-desk-mcp.md MC1.8: the bridge of the installation is no holder — the installer ends it first.
        (Kind::Nsis, Some(installation)) => (vec![installation.to_path_buf()], vec![installation.join(mcp::PROGRAM)]),
        _ => (vec![backend, resources], Vec::new()),
    };
    Setup {
        kind,
        policy: Policy::release(),
        programs: Programs::system(app.path().download_dir().ok(), app.path().home_dir().ok()),
        holders,
        bridges,
    }
}

/// Installs the installer of Windows or the AppImage (UB2.4, UB2.5): the plugin checks the bytes again by the key that
/// signed them and the signed version. On Windows it ends the app once the installer runs, which starts the app again;
/// an AppImage returns once its file is replaced.
pub fn install(app: &AppHandle, journal: &Journal, file: &Downloaded, kind: Kind) -> Result<(), Cause> {
    let failed = match kind {
        Kind::AppImage => Cause::NotReplaced,
        _ => Cause::NotLaunched,
    };
    if !matches!(kind, Kind::Nsis | Kind::AppImage) {
        return Err(failed);
    }
    let key = Policy::release().keys.of(file.signer).to_string();
    let before = journal.clone();
    let updater = app
        .updater_builder()
        .pubkey(key)
        // UB2.4: only the log — the window is not hidden and the lock of one instance is not let go before the installer.
        .on_before_exit(move || before.note("update_installer_starting"))
        .build()
        .map_err(|_| failed)?;
    let update = updater.restore_update(file.release.raw.clone()).map_err(|_| failed)?.ok_or(failed)?;
    if update.version != file.release.version.to_string() {
        return Err(failed);
    }
    update.install(file.bytes.as_slice()).map_err(|_| {
        journal.note(&format!("update_install_failed kind={}", kind.name()));
        failed
    })
}
