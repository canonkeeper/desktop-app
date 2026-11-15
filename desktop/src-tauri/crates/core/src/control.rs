// SPDX-License-Identifier: AGPL-3.0-only

//! The control channel (specs/2026-11-07-desktop-app.md DA3, «Types»; backend/internal/app/desktop/control.go): one
//! line of JSON per message on the stdin and stdout of the backend. The shell writes `start` (with the secrets of the
//! app and where they live, its version, how it was installed and whether the start brings the data back from a copy —
//! specs/2026-11-13-desk-upgrade.md UG2.1, UG4.1; specs/2026-11-13-desk-updates.md UB1.1), `open-code`, `secret-result`,
//! `update-state` (what an update of the app came to, UB1.1) and `shutdown`; the backend writes `step` (a long step of the
//! start, UG3.1), `ready`, `open-code`, `secret-save`, `secret-delete` (specs/2026-11-13-desk-secrets.md SE1.4), `erase`
//! (specs/2026-11-13-desk-erase.md ER1.2), `focus` (specs/2026-11-13-desk-account.md AC1.3), `update-check`,
//! `update-download`, `update-install`, `update-cancel` (UB1.1), `folder-choice` and `folder-result`
//! (specs/2026-11-13-desk-obsidian.md OB1.1), and `fatal`; the shell answers a choice of a folder with `folder-picked`. Fields this shell does not know are ignored,
//! so a newer backend can add them; a message of an unknown type is kept by its type only.

use std::collections::BTreeMap;
use std::fmt;

use serde::Deserialize;
use serde_json::json;

use crate::mcp::Bridge;
use crate::secrets::{Secret, Store};
use crate::update::state::Line;

/// What the shell writes to the backend.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ToBackend<'a> {
    /// The first message: the data directory of the user and the directory of the bundle, both plain paths; the
    /// records of the secrets the shell keeps and where it keeps them (SE1.3); the version of the shell, which the
    /// backend must be (UG2.1); how this copy of the app was installed, which says whether it updates itself
    /// (specs/2026-11-13-desk-updates.md UB1.1, UB1.7); whether this start brings the data back from the copy the
    /// backend chooses (UG4.1); and how the program of the bridge for the programs of the author runs, when it is there
    /// (specs/2026-11-15-desk-mcp.md MC3.2).
    Start {
        data_dir: &'a str,
        resources: &'a str,
        secrets: &'a BTreeMap<String, Secret>,
        store: Store,
        version: &'a str,
        update_kind: &'a str,
        restore: bool,
        bridge: Option<&'a Bridge>,
    },
    /// A new one-time code for the window (its session was refused).
    OpenCode,
    /// The answer to `secret-save` and `secret-delete`: whether the record is kept as asked.
    SecretResult { request: &'a str, ok: bool },
    /// What an update of the app came to (UB1.1): after every change and after every `ready`.
    UpdateState(&'a Line),
    /// The folder the author picked in the system window for the choice `request` (specs/2026-11-13-desk-obsidian.md
    /// OB1.3): a folder the shell found to be none of the app's and not too wide.
    FolderPicked { request: &'a str, path: &'a str },
    /// Stop: the backend cancels the chat turns, stops the core, Codex and PostgreSQL, and exits.
    Shutdown,
}

impl ToBackend<'_> {
    /// The message as one line, with its newline.
    pub fn line(&self) -> String {
        let value = match self {
            ToBackend::Start { data_dir, resources, secrets, store, version, update_kind, restore, bridge } => {
                let secrets: serde_json::Map<String, serde_json::Value> =
                    secrets.iter().map(|(id, value)| (id.clone(), value.as_str().into())).collect();
                let mut start = json!({
                    "type": "start", "dataDir": data_dir, "resources": resources, "secrets": secrets,
                    "secretsStore": store.name(), "version": version, "updateKind": update_kind,
                });
                if *restore {
                    start["restore"] = true.into();
                }
                if let Some(bridge) = bridge {
                    start["bridge"] = serde_json::to_value(bridge).unwrap_or_default();
                }
                start
            }
            ToBackend::OpenCode => json!({"type": "open-code"}),
            ToBackend::SecretResult { request, ok } => json!({"type": "secret-result", "request": request, "ok": ok}),
            ToBackend::UpdateState(line) => {
                let mut state = serde_json::to_value(line).unwrap_or_else(|_| json!({}));
                state["type"] = "update-state".into();
                state
            }
            ToBackend::FolderPicked { request, path } => {
                json!({"type": "folder-picked", "request": request, "path": path})
            }
            ToBackend::Shutdown => json!({"type": "shutdown"}),
        };
        format!("{value}\n")
    }
}

/// The one-time code that opens the window: it goes only into the address of the window, never into a log or a
/// debug print.
#[derive(Clone, PartialEq, Eq)]
pub struct Code(String);

impl Code {
    pub fn new(code: impl Into<String>) -> Code {
        Code(code.into())
    }

    pub fn as_str(&self) -> &str {
        &self.0
    }
}

impl fmt::Debug for Code {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str("Code(…)")
    }
}

/// A copy made before the migrations, as the backend names it (specs/2026-11-13-desk-upgrade.md UG2.3): its directory in
/// `backups/`, when it was made — the local time of the computer as RFC 3339 — and the version of the app that made it.
#[derive(Debug, Clone, PartialEq, Eq, Deserialize)]
pub struct BackupCopy {
    pub name: String,
    #[serde(rename = "madeAt")]
    pub made_at: String,
    #[serde(default)]
    pub version: Option<String>,
}

/// A folder «до возврата» an earlier return left in the data directory (specs/2026-11-13-desk-upgrade.md UG4.4): when the
/// return made it — the local time of the computer as RFC 3339 — and its size.
#[derive(Debug, Clone, PartialEq, Eq, Deserialize)]
pub struct BeforeRestore {
    #[serde(rename = "madeAt")]
    pub made_at: String,
    pub bytes: u64,
}

/// What a `fatal` says besides its reason and the copy of a failed migration (UG2, UG4.6): the version that opened the
/// data before, the copy a return would bring back or failed with, the folders «до возврата» earlier returns left and the
/// bytes a return still needs on the disk; the version of the program of the backend a `version_mismatch` names — its words
/// name the later of it and of this shell (specs/2026-11-13-desk-updates.md UB0.2).
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Said {
    pub opened: Option<String>,
    pub program: Option<String>,
    pub copy: Option<BackupCopy>,
    pub before: Vec<BeforeRestore>,
    pub needed: Option<u64>,
}

/// A long step of the start before `ready` (UG3.1).
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Step {
    /// The copy of the data before the migrations of `version`.
    Backup { version: Option<String> },
    /// The migrations of `version`.
    Migrate { version: Option<String> },
    /// The return of the data from `copy`.
    Restore { copy: Option<BackupCopy> },
}

/// What the backend writes to the shell.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum FromBackend {
    /// The backend listens on 127.0.0.1:port; the code opens the window once. `port_changed`: the remembered port was
    /// taken and the address of the window is new (specs/2026-11-12-desk-process.md PR1.2). `updated_from`: the version
    /// the data were opened with before this one, once after an update (UG2.5), and the notes of the release now running
    /// when the app installed it (specs/2026-11-13-desk-updates.md UB2.8).
    Ready {
        port: u16,
        code: Code,
        port_changed: bool,
        updated_from: Option<String>,
        release_notes: Option<String>,
    },
    /// The next code, the answer to `open-code`.
    OpenCode {
        code: Code,
    },
    /// A long step of the start goes on.
    Step(Step),
    /// The backend stops; `reason` names the step without content, `backup` the directory of the copy made before the
    /// migrations (`migrate`, PR3.3), `said` what else the words of the reason need.
    Fatal {
        reason: String,
        backup: Option<String>,
        said: Box<Said>,
    },
    /// Keep a record of the secrets of the app (SE1.4); `request` names the answer.
    SecretSave {
        id: String,
        value: Secret,
        request: String,
    },
    /// Remove a record of the secrets of the app.
    SecretDelete {
        id: String,
        request: String,
    },
    /// The author asked to erase all the data of the app (specs/2026-11-13-desk-erase.md ER1.2): the shell stops the
    /// backend, closes the window and removes what the app wrote. Only the backend of the shell says it.
    Erase,
    /// Check for a new version (specs/2026-11-13-desk-updates.md UB1.1): no field — the addresses and the keys are the
    /// shell's.
    UpdateCheck,
    /// Download, install, or stop the download or the wait of, the version the shell itself reported in `update-state`;
    /// the shell does it only for the release it holds.
    UpdateDownload {
        version: String,
    },
    UpdateInstall {
        version: String,
    },
    UpdateCancel {
        version: String,
    },
    /// Raise the window of the app: the author confirmed the link with his account in the browser
    /// (specs/2026-11-13-desk-account.md AC1.3).
    Focus,
    /// A choice of the folder of an Obsidian vault for the saga titled `saga` (specs/2026-11-13-desk-obsidian.md OB1.1):
    /// the page may open the system window for `request`, and for it only.
    FolderChoice {
        request: String,
        saga: String,
    },
    /// How the backend ended the choice `request`: `linked`, the reason it refused the folder, or `stale`.
    FolderResult {
        request: String,
        outcome: String,
    },
    /// A message of a type this shell does not know.
    Other {
        kind: String,
    },
}

/// Why a line is not a message.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum LineError {
    NotJson,
    NoType,
    /// `ready` without a port in 1–65535 or a code, `open-code` without a code, `fatal` without a reason,
    /// `secret-save` without an id, a value or a request, `secret-delete` without an id or a request, `step` without a
    /// step this shell knows, `update-download`, `update-install` or `update-cancel` without a version, `folder-choice`
    /// without a request, `folder-result` without a request or an outcome.
    Incomplete,
}

#[derive(Deserialize)]
struct Wire {
    #[serde(rename = "type")]
    kind: Option<String>,
    port: Option<u64>,
    code: Option<String>,
    #[serde(rename = "portChanged", default)]
    port_changed: bool,
    reason: Option<String>,
    backup: Option<String>,
    id: Option<String>,
    value: Option<String>,
    request: Option<String>,
    #[serde(rename = "updatedFrom")]
    updated_from: Option<String>,
    #[serde(rename = "releaseNotes")]
    release_notes: Option<String>,
    step: Option<String>,
    version: Option<String>,
    opened: Option<String>,
    copy: Option<BackupCopy>,
    #[serde(default)]
    before: Vec<BeforeRestore>,
    needed: Option<u64>,
    saga: Option<String>,
    outcome: Option<String>,
}

fn given(text: Option<String>) -> Option<String> {
    text.filter(|text| !text.is_empty())
}

/// Reads one line the backend wrote.
pub fn parse(line: &str) -> Result<FromBackend, LineError> {
    let wire: Wire = serde_json::from_str(line.trim_end()).map_err(|_| LineError::NotJson)?;
    let kind = wire.kind.filter(|kind| !kind.is_empty()).ok_or(LineError::NoType)?;
    let code = wire.code.filter(|code| !code.is_empty()).map(Code);
    match kind.as_str() {
        "ready" => {
            let port = wire
                .port
                .and_then(|port| u16::try_from(port).ok())
                .filter(|port| *port != 0)
                .ok_or(LineError::Incomplete)?;
            Ok(FromBackend::Ready {
                port,
                code: code.ok_or(LineError::Incomplete)?,
                port_changed: wire.port_changed,
                updated_from: given(wire.updated_from),
                release_notes: given(wire.release_notes),
            })
        }
        "open-code" => Ok(FromBackend::OpenCode { code: code.ok_or(LineError::Incomplete)? }),
        "step" => Ok(FromBackend::Step(match wire.step.as_deref() {
            Some("backup") => Step::Backup { version: given(wire.version) },
            Some("migrate") => Step::Migrate { version: given(wire.version) },
            Some("restore") => Step::Restore { copy: wire.copy },
            _ => return Err(LineError::Incomplete),
        })),
        "fatal" => Ok(FromBackend::Fatal {
            reason: wire.reason.filter(|reason| !reason.is_empty()).ok_or(LineError::Incomplete)?,
            backup: wire.backup.filter(|backup| !backup.is_empty()),
            said: Box::new(Said {
                opened: given(wire.opened),
                program: given(wire.version),
                copy: wire.copy,
                before: wire.before,
                needed: wire.needed,
            }),
        }),
        "secret-save" => Ok(FromBackend::SecretSave {
            id: wire.id.filter(|id| !id.is_empty()).ok_or(LineError::Incomplete)?,
            value: wire.value.filter(|value| !value.is_empty()).map(Secret::new).ok_or(LineError::Incomplete)?,
            request: wire.request.filter(|request| !request.is_empty()).ok_or(LineError::Incomplete)?,
        }),
        "erase" => Ok(FromBackend::Erase),
        "update-check" => Ok(FromBackend::UpdateCheck),
        "update-download" | "update-install" | "update-cancel" => {
            let version = given(wire.version).ok_or(LineError::Incomplete)?;
            Ok(match kind.as_str() {
                "update-download" => FromBackend::UpdateDownload { version },
                "update-install" => FromBackend::UpdateInstall { version },
                _ => FromBackend::UpdateCancel { version },
            })
        }
        "focus" => Ok(FromBackend::Focus),
        "folder-choice" => Ok(FromBackend::FolderChoice {
            request: given(wire.request).ok_or(LineError::Incomplete)?,
            saga: wire.saga.unwrap_or_default(),
        }),
        "folder-result" => Ok(FromBackend::FolderResult {
            request: given(wire.request).ok_or(LineError::Incomplete)?,
            outcome: given(wire.outcome).ok_or(LineError::Incomplete)?,
        }),
        "secret-delete" => Ok(FromBackend::SecretDelete {
            id: wire.id.filter(|id| !id.is_empty()).ok_or(LineError::Incomplete)?,
            request: wire.request.filter(|request| !request.is_empty()).ok_or(LineError::Incomplete)?,
        }),
        _ => Ok(FromBackend::Other { kind }),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn the_shell_writes_one_line_per_message_with_the_names_of_the_backend() {
        let secrets = BTreeMap::from([("provider:own".to_string(), Secret::new("key-test-north"))]);
        let start = ToBackend::Start {
            data_dir: r"C:\Users\Тест\AppData\Local\ru.canonkeeper.desktop\data",
            resources: r"C:\Users\Тест\AppData\Local\CanonKeeper",
            secrets: &secrets,
            store: Store::System,
            version: "1.3.0",
            update_kind: "nsis",
            restore: false,
            bridge: None,
        }
        .line();
        assert!(start.ends_with('\n') && start.matches('\n').count() == 1);
        let value: serde_json::Value = serde_json::from_str(&start).unwrap();
        assert_eq!(
            value,
            json!({
                "type": "start",
                "dataDir": r"C:\Users\Тест\AppData\Local\ru.canonkeeper.desktop\data",
                "resources": r"C:\Users\Тест\AppData\Local\CanonKeeper",
                "secrets": {"provider:own": "key-test-north"},
                "secretsStore": "system",
                "version": "1.3.0",
                "updateKind": "nsis",
            })
        );
        let none = BTreeMap::new();
        let empty: serde_json::Value = serde_json::from_str(
            &ToBackend::Start {
                data_dir: "/d",
                resources: "/r",
                secrets: &none,
                store: Store::File,
                version: "",
                update_kind: "none",
                restore: true,
                bridge: Some(&Bridge { program: "/a/CanonKeeper.AppImage".into(), args: vec!["mcp".into()] }),
            }
            .line(),
        )
        .unwrap();
        assert_eq!(empty["secrets"], json!({}));
        assert_eq!(empty["secretsStore"], "file");
        // specs/2026-11-13-desk-upgrade.md UG4.1: a return is a mark of start, without the name of a copy.
        assert_eq!(empty["restore"], true);
        // specs/2026-11-15-desk-mcp.md MC3.2: the bridge of an AppImage is its file and mcp; a start without it names none.
        assert_eq!(empty["bridge"], json!({"program": "/a/CanonKeeper.AppImage", "args": ["mcp"]}));
        assert!(value.get("bridge").is_none());
        assert_eq!(empty["version"], "");
        assert_eq!(ToBackend::OpenCode.line(), "{\"type\":\"open-code\"}\n");
        assert_eq!(
            ToBackend::SecretResult { request: "s7", ok: true }.line(),
            "{\"ok\":true,\"request\":\"s7\",\"type\":\"secret-result\"}\n"
        );
        assert_eq!(ToBackend::Shutdown.line(), "{\"type\":\"shutdown\"}\n");
    }

    #[test]
    fn a_choice_of_a_folder_goes_by_its_number() {
        assert_eq!(
            parse(r#"{"type":"folder-choice","request":"n1","saga":"Северный предел"}"#),
            Ok(FromBackend::FolderChoice { request: "n1".into(), saga: "Северный предел".into() })
        );
        assert_eq!(
            parse(r#"{"type":"folder-result","request":"n1","outcome":"linked"}"#),
            Ok(FromBackend::FolderResult { request: "n1".into(), outcome: "linked".into() })
        );
        for line in [r#"{"type":"folder-choice"}"#, r#"{"type":"folder-result","request":"n1"}"#] {
            assert_eq!(parse(line), Err(LineError::Incomplete), "{line}");
        }
        let picked = ToBackend::FolderPicked { request: "n1", path: "/home/author/Хранилище" }.line();
        assert!(picked.ends_with('\n') && picked.matches('\n').count() == 1);
        assert_eq!(
            serde_json::from_str::<serde_json::Value>(&picked).unwrap(),
            json!({"type": "folder-picked", "request": "n1", "path": "/home/author/Хранилище"})
        );
    }

    #[test]
    fn the_backend_asks_to_raise_the_window() {
        // specs/2026-11-13-desk-account.md AC1.3: the link went through in the browser; the window comes up.
        assert_eq!(parse("{\"type\":\"focus\"}\n"), Ok(FromBackend::Focus));
    }

    #[test]
    fn the_backend_asks_to_keep_and_to_remove_a_secret() {
        assert_eq!(
            parse(r#"{"type":"secret-save","id":"provider:own","value":"key-test-north","request":"s1"}"#),
            Ok(FromBackend::SecretSave {
                id: "provider:own".into(),
                value: Secret::new("key-test-north"),
                request: "s1".into()
            })
        );
        assert_eq!(
            parse(r#"{"type":"secret-delete","id":"provider:own","request":"s2"}"#),
            Ok(FromBackend::SecretDelete { id: "provider:own".into(), request: "s2".into() })
        );
        assert_eq!(parse(r#"{"type":"secret-save","id":"provider:own","request":"s1"}"#), Err(LineError::Incomplete));
        assert_eq!(
            parse(r#"{"type":"secret-save","id":"provider:own","value":"","request":"s1"}"#),
            Err(LineError::Incomplete)
        );
        assert_eq!(parse(r#"{"type":"secret-save","value":"v","request":"s1"}"#), Err(LineError::Incomplete));
        assert_eq!(parse(r#"{"type":"secret-delete","id":"provider:own"}"#), Err(LineError::Incomplete));
        let save = parse(r#"{"type":"secret-save","id":"provider:own","value":"key-test-north","request":"s1"}"#);
        assert!(!format!("{save:?}").contains("key-test-north"), "a debug print holds the value");
    }

    #[test]
    fn the_shell_does_not_print_the_secrets_of_start() {
        let secrets = BTreeMap::from([("provider:own".to_string(), Secret::new("key-test-north"))]);
        let start = ToBackend::Start {
            data_dir: "/d",
            resources: "/r",
            secrets: &secrets,
            store: Store::File,
            version: "1.3.0",
            update_kind: "deb",
            restore: false,
            bridge: None,
        };
        assert!(!format!("{start:?}").contains("key-test-north"));
    }

    #[test]
    fn the_backend_asks_to_erase_all_the_data_of_the_app() {
        // specs/2026-11-13-desk-erase.md ER1.2, ER1.6: one line without fields; fields the shell does not know are ignored.
        assert_eq!(parse("{\"type\":\"erase\"}\n"), Ok(FromBackend::Erase));
        assert_eq!(parse(r#"{"type":"erase","dataDir":"/elsewhere"}"#), Ok(FromBackend::Erase));
    }

    #[test]
    fn the_backend_asks_about_an_update_only_by_the_version_the_shell_reported() {
        // specs/2026-11-13-desk-updates.md UB1.1: a check has no field; the rest name a version and nothing else is heard.
        assert_eq!(
            parse(r#"{"type":"update-check","url":"https://elsewhere.example.test/latest.json"}"#),
            Ok(FromBackend::UpdateCheck)
        );
        assert_eq!(
            parse(
                r#"{"type":"update-download","version":"1.3.0","url":"https://elsewhere.example.test/a.deb","key":"x"}"#
            ),
            Ok(FromBackend::UpdateDownload { version: "1.3.0".into() })
        );
        assert_eq!(
            parse(r#"{"type":"update-install","version":"1.3.0"}"#),
            Ok(FromBackend::UpdateInstall { version: "1.3.0".into() })
        );
        assert_eq!(
            parse(r#"{"type":"update-cancel","version":"1.3.0"}"#),
            Ok(FromBackend::UpdateCancel { version: "1.3.0".into() })
        );
        for kind in ["update-download", "update-install", "update-cancel"] {
            assert_eq!(parse(&format!(r#"{{"type":"{kind}"}}"#)), Err(LineError::Incomplete), "{kind}");
            assert_eq!(parse(&format!(r#"{{"type":"{kind}","version":""}}"#)), Err(LineError::Incomplete), "{kind}");
        }
    }

    #[test]
    fn the_shell_tells_the_state_of_an_update_in_one_line() {
        use crate::update::state::Updates;
        use crate::update::Kind;
        let updates = Updates::new(Kind::Deb, "1.2.0");
        let line = ToBackend::UpdateState(&updates.line()).line();
        assert!(line.ends_with('\n') && line.matches('\n').count() == 1);
        let value: serde_json::Value = serde_json::from_str(&line).unwrap();
        assert_eq!(value, json!({"type": "update-state", "phase": "idle", "found": "never"}));
    }

    #[test]
    fn ready_carries_the_port_the_code_and_whether_the_address_changed() {
        assert_eq!(
            parse(r#"{"type":"ready","port":24117,"code":"abc_-1"}"#),
            Ok(FromBackend::Ready {
                port: 24117,
                code: Code::new("abc_-1"),
                port_changed: false,
                updated_from: None,
                release_notes: None
            })
        );
        assert_eq!(
            parse("{\"type\":\"ready\",\"port\":24117,\"code\":\"abc\",\"portChanged\":true}\n"),
            Ok(FromBackend::Ready {
                port: 24117,
                code: Code::new("abc"),
                port_changed: true,
                updated_from: None,
                release_notes: None
            })
        );
        // specs/2026-11-13-desk-upgrade.md UG2.5: the version before an update, once.
        assert_eq!(
            parse(r#"{"type":"ready","port":24117,"code":"abc","updatedFrom":"1.2.0"}"#),
            Ok(FromBackend::Ready {
                port: 24117,
                code: Code::new("abc"),
                port_changed: false,
                updated_from: Some("1.2.0".into()),
                release_notes: None,
            })
        );
        // specs/2026-11-13-desk-updates.md UB2.8: and the notes of the release now running, once.
        assert_eq!(
            parse(r#"{"type":"ready","port":24117,"code":"abc","updatedFrom":"1.2.0","releaseNotes":"Что нового"}"#),
            Ok(FromBackend::Ready {
                port: 24117,
                code: Code::new("abc"),
                port_changed: false,
                updated_from: Some("1.2.0".into()),
                release_notes: Some("Что нового".into()),
            })
        );
    }

    #[test]
    fn fields_the_shell_does_not_know_are_ignored() {
        assert_eq!(
            parse(r#"{"type":"ready","port":20001,"code":"c","gatewayPort":20002,"future":{"x":1}}"#),
            Ok(FromBackend::Ready {
                port: 20001,
                code: Code::new("c"),
                port_changed: false,
                updated_from: None,
                release_notes: None
            })
        );
        assert_eq!(
            parse(r#"{"type":"backup-progress","percent":40}"#),
            Ok(FromBackend::Other { kind: "backup-progress".into() })
        );
    }

    #[test]
    fn fatal_carries_its_reason_and_the_name_of_the_copy() {
        assert_eq!(
            parse(r#"{"type":"fatal","reason":"migrate","backup":"pre-0142-20261112T101500Z"}"#),
            Ok(FromBackend::Fatal {
                reason: "migrate".into(),
                backup: Some("pre-0142-20261112T101500Z".into()),
                said: Box::default()
            })
        );
        assert_eq!(
            parse(r#"{"type":"fatal","reason":"backup"}"#),
            Ok(FromBackend::Fatal { reason: "backup".into(), backup: None, said: Box::default() })
        );
        assert_eq!(
            parse(r#"{"type":"fatal","reason":"newer_schema","backup":""}"#),
            Ok(FromBackend::Fatal { reason: "newer_schema".into(), backup: None, said: Box::default() })
        );
    }

    #[test]
    fn fatal_carries_what_the_words_of_an_update_need() {
        // specs/2026-11-13-desk-upgrade.md UG2.3, UG2.4, UG2.1, UG4.6.
        let copy = BackupCopy {
            name: "pre-145-20261113T110500.000Z".into(),
            made_at: "2026-11-13T14:05:00+03:00".into(),
            version: Some("1.3.0".into()),
        };
        assert_eq!(
            parse(
                r#"{"type":"fatal","reason":"newer_schema","opened":"1.3.0","copy":{"name":"pre-145-20261113T110500.000Z","madeAt":"2026-11-13T14:05:00+03:00","version":"1.3.0"}}"#
            ),
            Ok(FromBackend::Fatal {
                reason: "newer_schema".into(),
                backup: None,
                said: Box::new(Said { opened: Some("1.3.0".into()), copy: Some(copy), ..Said::default() })
            })
        );
        assert_eq!(
            parse(r#"{"type":"fatal","reason":"version_mismatch","version":"1.3.0","shell":"1.2.0"}"#),
            Ok(FromBackend::Fatal {
                reason: "version_mismatch".into(),
                backup: None,
                said: Box::new(Said { program: Some("1.3.0".into()), ..Said::default() })
            })
        );
        assert_eq!(
            parse(r#"{"type":"fatal","reason":"restore_space","needed":2254857830}"#),
            Ok(FromBackend::Fatal {
                reason: "restore_space".into(),
                backup: None,
                said: Box::new(Said { needed: Some(2_254_857_830), ..Said::default() })
            })
        );
    }

    #[test]
    fn a_step_of_the_start_names_its_version_or_its_copy() {
        // UG3.1: the copy, the migrations, the return.
        assert_eq!(
            parse(r#"{"type":"step","step":"backup","version":"1.3.0"}"#),
            Ok(FromBackend::Step(Step::Backup { version: Some("1.3.0".into()) }))
        );
        assert_eq!(
            parse(r#"{"type":"step","step":"migrate"}"#),
            Ok(FromBackend::Step(Step::Migrate { version: None }))
        );
        assert_eq!(
            parse(
                r#"{"type":"step","step":"restore","copy":{"name":"pre-1-20261113T110500.000Z","madeAt":"2026-11-13T14:05:00+03:00"}}"#
            ),
            Ok(FromBackend::Step(Step::Restore {
                copy: Some(BackupCopy {
                    name: "pre-1-20261113T110500.000Z".into(),
                    made_at: "2026-11-13T14:05:00+03:00".into(),
                    version: None
                })
            }))
        );
        assert_eq!(parse(r#"{"type":"step"}"#), Err(LineError::Incomplete));
        assert_eq!(parse(r#"{"type":"step","step":"format"}"#), Err(LineError::Incomplete));
    }

    #[test]
    fn a_line_that_is_not_a_whole_message_is_refused() {
        assert_eq!(parse("not json"), Err(LineError::NotJson));
        assert_eq!(parse("[1,2]"), Err(LineError::NotJson));
        assert_eq!(parse(r#"{"port":1}"#), Err(LineError::NoType));
        assert_eq!(parse(r#"{"type":""}"#), Err(LineError::NoType));
        assert_eq!(parse(r#"{"type":"ready","code":"c"}"#), Err(LineError::Incomplete));
        assert_eq!(parse(r#"{"type":"ready","port":0,"code":"c"}"#), Err(LineError::Incomplete));
        assert_eq!(parse(r#"{"type":"ready","port":70000,"code":"c"}"#), Err(LineError::Incomplete));
        assert_eq!(parse(r#"{"type":"ready","port":20001}"#), Err(LineError::Incomplete));
        assert_eq!(parse(r#"{"type":"open-code"}"#), Err(LineError::Incomplete));
        assert_eq!(parse(r#"{"type":"fatal"}"#), Err(LineError::Incomplete));
    }

    #[test]
    fn the_code_never_shows_in_a_debug_print() {
        let ready = parse(r#"{"type":"ready","port":20001,"code":"secret-code"}"#).unwrap();
        assert!(!format!("{ready:?}").contains("secret-code"));
    }
}
