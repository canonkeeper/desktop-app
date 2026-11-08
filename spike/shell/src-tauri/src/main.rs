// SPDX-License-Identifier: AGPL-3.0-only

//! Shell of the S0 probe. It opens one window with the map probe page and appends what happens
//! to the JSON-lines file named by SPIKE_REPORT: the page's own report, the result of an update
//! from SPIKE_UPDATE_URL (DA14.7) and of a keychain write or read (SPIKE_KEYCHAIN, DA14.8).
//! The DA1.9 measurement build also carries the backend as a sidecar (`backend.rs`).
//! It holds no business logic.
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod backend;

use std::fs::OpenOptions;
use std::io::Write;
use std::time::{SystemTime, UNIX_EPOCH};

use serde_json::{json, Value};

fn now_ms() -> u128 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_millis())
        .unwrap_or(0)
}

fn append(kind: &str, version: &str, body: Value) {
    let Some(path) = std::env::var_os("SPIKE_REPORT") else {
        return;
    };
    let line = json!({
        "kind": kind,
        "version": version,
        "pid": std::process::id(),
        "at_ms": now_ms(),
        "body": body,
    });
    if let Ok(mut file) = OpenOptions::new().create(true).append(true).open(path) {
        let _ = writeln!(file, "{line}");
    }
}

struct Started(u128);

#[tauri::command]
fn report(app: tauri::AppHandle, started: tauri::State<'_, Started>, payload: Value) {
    let version = app.package_info().version.to_string();
    append(
        "page",
        &version,
        json!({"process_started_ms": started.0, "page": payload}),
    );
}

async fn update(app: tauri::AppHandle, url: String) -> Value {
    use tauri_plugin_updater::UpdaterExt;
    let endpoint = match tauri::Url::parse(&url) {
        Ok(endpoint) => endpoint,
        Err(error) => return json!({"stage": "url", "error": error.to_string()}),
    };
    let updater = match app
        .updater_builder()
        .endpoints(vec![endpoint])
        .and_then(|builder| builder.build())
    {
        Ok(updater) => updater,
        Err(error) => return json!({"stage": "build", "error": error.to_string()}),
    };
    let update = match updater.check().await {
        Ok(Some(update)) => update,
        Ok(None) => return json!({"stage": "check", "found": null}),
        Err(error) => return json!({"stage": "check", "error": error.to_string()}),
    };
    let version = app.package_info().version.to_string();
    let found = update.version.clone();
    let bytes = match update.download(|_, _| {}, || {}).await {
        Ok(bytes) => bytes,
        Err(error) => {
            return json!({"stage": "download", "found": found, "error": error.to_string()})
        }
    };
    append(
        "update",
        &version,
        json!({"stage": "downloaded", "found": found, "bytes": bytes.len()}),
    );
    // On Windows install() starts the installer and exits this process.
    match update.install(bytes) {
        Ok(()) => json!({"stage": "installed", "found": found}),
        Err(error) => json!({"stage": "install", "found": found, "error": error.to_string()}),
    }
}

#[cfg(target_os = "macos")]
fn keychain(mode: &str) -> Value {
    let entry = match keyring::Entry::new("CanonKeeperProbe", "provider:probe") {
        Ok(entry) => entry,
        Err(error) => return json!({"mode": mode, "error": error.to_string()}),
    };
    let started = now_ms();
    let outcome = match mode {
        "write" => entry
            .set_password("not-a-real-key")
            .map(|()| "written".to_string()),
        _ => entry
            .get_password()
            .map(|value| format!("read {} chars", value.chars().count())),
    };
    match outcome {
        Ok(result) => json!({"mode": mode, "result": result, "ms": now_ms() - started}),
        Err(error) => json!({"mode": mode, "error": error.to_string(), "ms": now_ms() - started}),
    }
}

#[cfg(not(target_os = "macos"))]
fn keychain(mode: &str) -> Value {
    json!({"mode": mode, "skipped": "only macOS is probed"})
}

fn main() {
    let started = now_ms();
    tauri::Builder::default()
        .plugin(tauri_plugin_updater::Builder::new().build())
        .manage(Started(started))
        .invoke_handler(tauri::generate_handler![report])
        .on_page_load(backend::page_load)
        .setup(move |app| {
            let version = app.package_info().version.to_string();
            append("start", &version, json!({"process_started_ms": started}));
            backend::start(app);
            if let Ok(mode) = std::env::var("SPIKE_KEYCHAIN") {
                let version = version.clone();
                std::thread::spawn(move || append("keychain", &version, keychain(&mode)));
            }
            if let Ok(url) = std::env::var("SPIKE_UPDATE_URL") {
                let handle = app.handle().clone();
                tauri::async_runtime::spawn(async move {
                    let outcome = update(handle.clone(), url).await;
                    let installed = outcome["stage"] == "installed";
                    append("update", &version, outcome);
                    if installed {
                        handle.exit(0);
                    }
                });
            }
            Ok(())
        })
        .run(tauri::generate_context!())
        .expect("the probe shell failed to run");
}
