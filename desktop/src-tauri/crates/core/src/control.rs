// SPDX-License-Identifier: AGPL-3.0-only

//! The control channel (specs/2026-11-07-desktop-app.md DA3, «Types»; backend/internal/app/desktop/control.go): one
//! line of JSON per message on the stdin and stdout of the backend. The shell writes `start`, `open-code` and
//! `shutdown`; the backend writes `ready`, `open-code` and `fatal`. Fields this shell does not know are ignored, so a
//! newer backend can add them; a message of an unknown type is kept by its type only.

use std::fmt;

use serde::Deserialize;
use serde_json::json;

/// What the shell writes to the backend.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ToBackend<'a> {
    /// The first message: the data directory of the user and the directory of the bundle, both plain paths.
    Start { data_dir: &'a str, resources: &'a str },
    /// A new one-time code for the window (its session was refused).
    OpenCode,
    /// Stop: the backend cancels the chat turns, stops the core, Codex and PostgreSQL, and exits.
    Shutdown,
}

impl ToBackend<'_> {
    /// The message as one line, with its newline.
    pub fn line(&self) -> String {
        let value = match self {
            ToBackend::Start { data_dir, resources } => {
                json!({"type": "start", "dataDir": data_dir, "resources": resources})
            }
            ToBackend::OpenCode => json!({"type": "open-code"}),
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

/// What the backend writes to the shell.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum FromBackend {
    /// The backend listens on 127.0.0.1:port; the code opens the window once. `port_changed`: the remembered port was
    /// taken and the address of the window is new (specs/2026-11-12-desk-process.md PR1.2).
    Ready { port: u16, code: Code, port_changed: bool },
    /// The next code, the answer to `open-code`.
    OpenCode { code: Code },
    /// The backend stops; `reason` names the step without content, `backup` the directory of the copy made before the
    /// migrations (`migrate`, PR3.3).
    Fatal { reason: String, backup: Option<String> },
    /// A message of a type this shell does not know.
    Other { kind: String },
}

/// Why a line is not a message.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum LineError {
    NotJson,
    NoType,
    /// `ready` without a port in 1–65535 or a code, `open-code` without a code, `fatal` without a reason.
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
            Ok(FromBackend::Ready { port, code: code.ok_or(LineError::Incomplete)?, port_changed: wire.port_changed })
        }
        "open-code" => Ok(FromBackend::OpenCode { code: code.ok_or(LineError::Incomplete)? }),
        "fatal" => Ok(FromBackend::Fatal {
            reason: wire.reason.filter(|reason| !reason.is_empty()).ok_or(LineError::Incomplete)?,
            backup: wire.backup.filter(|backup| !backup.is_empty()),
        }),
        _ => Ok(FromBackend::Other { kind }),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn the_shell_writes_one_line_per_message_with_the_names_of_the_backend() {
        let start = ToBackend::Start {
            data_dir: r"C:\Users\Тест\AppData\Local\ru.canonkeeper.desktop\data",
            resources: r"C:\Users\Тест\AppData\Local\CanonKeeper",
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
            })
        );
        assert_eq!(ToBackend::OpenCode.line(), "{\"type\":\"open-code\"}\n");
        assert_eq!(ToBackend::Shutdown.line(), "{\"type\":\"shutdown\"}\n");
    }

    #[test]
    fn ready_carries_the_port_the_code_and_whether_the_address_changed() {
        assert_eq!(
            parse(r#"{"type":"ready","port":24117,"code":"abc_-1"}"#),
            Ok(FromBackend::Ready { port: 24117, code: Code::new("abc_-1"), port_changed: false })
        );
        assert_eq!(
            parse("{\"type\":\"ready\",\"port\":24117,\"code\":\"abc\",\"portChanged\":true}\n"),
            Ok(FromBackend::Ready { port: 24117, code: Code::new("abc"), port_changed: true })
        );
    }

    #[test]
    fn fields_the_shell_does_not_know_are_ignored() {
        assert_eq!(
            parse(r#"{"type":"ready","port":20001,"code":"c","gatewayPort":20002,"future":{"x":1}}"#),
            Ok(FromBackend::Ready { port: 20001, code: Code::new("c"), port_changed: false })
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
            Ok(FromBackend::Fatal { reason: "migrate".into(), backup: Some("pre-0142-20261112T101500Z".into()) })
        );
        assert_eq!(
            parse(r#"{"type":"fatal","reason":"backup"}"#),
            Ok(FromBackend::Fatal { reason: "backup".into(), backup: None })
        );
        assert_eq!(
            parse(r#"{"type":"fatal","reason":"newer_schema","backup":""}"#),
            Ok(FromBackend::Fatal { reason: "newer_schema".into(), backup: None })
        );
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
