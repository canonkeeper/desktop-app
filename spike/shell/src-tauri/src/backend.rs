// SPDX-License-Identifier: AGPL-3.0-only

//! The backend of the desktop app as a sidecar of the probe shell, for the DA1.9 measurement only: the real shell is
//! slice S6. When the bundle carries `canonkeeper-backend` next to the shell (only the measurement build does), the
//! shell starts it, sends `start` with the bundle's resources on its stdin (DA1.6, DA3.2), opens the window on
//! `http://127.0.0.1:P/` when `ready` comes and exchanges the one-time code for the session of the window, as the page
//! of S6 will. What happens goes to SPIKE_REPORT; the code never does. When the shell ends, the stdin of the backend
//! closes and the backend stops Codex and PostgreSQL.

use std::io::{BufRead, BufReader, Write};
use std::path::PathBuf;
use std::process::{ChildStdin, ChildStdout, Command, Stdio};
use std::sync::Mutex;

use serde_json::{json, Value};
use tauri::webview::{PageLoadEvent, PageLoadPayload};
use tauri::{AppHandle, Manager, Webview};

/// The backend of this run: its stdin lives as long as the shell, the code waits for the first page of the backend.
struct Backend {
    _stdin: ChildStdin,
    code: Mutex<Option<String>>,
}

fn sidecar() -> Option<PathBuf> {
    let name = if cfg!(windows) {
        "canonkeeper-backend.exe"
    } else {
        "canonkeeper-backend"
    };
    let path = std::env::current_exe().ok()?.parent()?.join(name);
    path.is_file().then_some(path)
}

/// Starts the backend when the bundle has one.
pub fn start(app: &tauri::App) {
    let Some(path) = sidecar() else {
        return;
    };
    let version = app.package_info().version.to_string();
    let report = |body: Value| crate::append("backend", &version, body);
    let resources = match app.path().resource_dir() {
        Ok(resources) => resources,
        Err(error) => return report(json!({"stage": "resources", "error": error.to_string()})),
    };
    let mut command = Command::new(path);
    command
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::inherit());
    #[cfg(windows)]
    {
        use std::os::windows::process::CommandExt;
        // CREATE_NO_WINDOW: the backend is a console program and the shell is not.
        command.creation_flags(0x0800_0000);
    }
    let mut child = match command.spawn() {
        Ok(child) => child,
        Err(error) => return report(json!({"stage": "spawn", "error": error.to_string()})),
    };
    let (Some(mut stdin), Some(stdout)) = (child.stdin.take(), child.stdout.take()) else {
        return report(json!({"stage": "spawn", "error": "no pipes"}));
    };
    let start = json!({"type": "start", "resources": resources});
    if let Err(error) = writeln!(stdin, "{start}") {
        return report(json!({"stage": "start", "error": error.to_string()}));
    }
    report(json!({"stage": "spawned", "pid": child.id()}));
    app.manage(Backend {
        _stdin: stdin,
        code: Mutex::new(None),
    });
    let handle = app.handle().clone();
    std::thread::spawn(move || listen(handle, stdout, version));
}

/// Reads the control channel: `ready` opens the window on the backend, `fatal` is reported with its reason.
fn listen(app: AppHandle, stdout: ChildStdout, version: String) {
    let report = |body: Value| crate::append("backend", &version, body);
    for line in BufReader::new(stdout).lines() {
        let Ok(line) = line else { break };
        let Ok(message) = serde_json::from_str::<Value>(&line) else {
            continue;
        };
        match message["type"].as_str().unwrap_or_default() {
            "ready" => {
                let port = message["port"].as_u64().unwrap_or(0);
                report(json!({"stage": "ready", "port": port}));
                if let Some(code) = message["code"].as_str() {
                    *app.state::<Backend>().code.lock().unwrap() = Some(code.to_string());
                }
                let url = tauri::Url::parse(&format!("http://127.0.0.1:{port}/")).expect("a loopback URL");
                match app.get_webview_window("main") {
                    Some(window) => {
                        if let Err(error) = window.navigate(url) {
                            report(json!({"stage": "navigate", "error": error.to_string()}));
                        }
                    }
                    None => report(json!({"stage": "navigate", "error": "no main window"})),
                }
            }
            "fatal" => report(json!({"stage": "fatal", "reason": message["reason"]})),
            other => report(json!({"stage": "message", "type": other})),
        }
    }
    report(json!({"stage": "stdout_closed"}));
}

/// Hears every page of the backend in the window; the first one exchanges the code for the session and reloads the
/// page with the outcome in its address (`?probe-session=200`), so the report tells when the window shows the app of
/// the local author.
pub fn page_load(webview: &Webview, payload: &PageLoadPayload<'_>) {
    if payload.event() != PageLoadEvent::Finished || payload.url().host_str() != Some("127.0.0.1") {
        return;
    }
    let version = webview.app_handle().package_info().version.to_string();
    crate::append("page_load", &version, json!({"url": payload.url().as_str()}));
    let Some(backend) = webview.try_state::<Backend>() else {
        return;
    };
    let code = backend.code.lock().unwrap().take();
    if let Some(code) = code {
        let script = SESSION_SCRIPT.replace("CODE", &Value::String(code).to_string());
        if let Err(error) = webview.eval(script) {
            crate::append("page_load", &version, json!({"eval_error": error.to_string()}));
        }
    }
}

const SESSION_SCRIPT: &str = r#"(async () => {
  let outcome = 'error';
  try {
    const csrf = await (await fetch('/api/v1/auth/csrf', { credentials: 'same-origin' })).json();
    const response = await fetch('/api/v1/desktop/session', {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf.csrfToken },
      body: JSON.stringify({ code: CODE }),
    });
    outcome = String(response.status);
  } catch (error) {
    outcome = 'error';
  }
  location.replace('/?probe-session=' + outcome);
})();"#;
