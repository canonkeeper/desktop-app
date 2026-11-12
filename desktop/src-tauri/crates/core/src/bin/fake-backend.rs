// SPDX-License-Identifier: AGPL-3.0-only

//! A stand-in for the backend in the tests of the supervision (tests/driver.rs): it speaks the control channel and
//! follows the plan in `<resources>/plan.txt` of its `start`, writing what it heard to `<resources>/received.txt`. It
//! takes nothing from its arguments or environment, as the real one. It is never bundled.

use std::fs::{self, OpenOptions};
use std::io::{self, BufRead, Write};
use std::path::{Path, PathBuf};
use std::thread;
use std::time::Duration;

use serde_json::{json, Value};

fn main() {
    let stdin = io::stdin();
    let mut lines = stdin.lock().lines();
    let Some(Ok(first)) = lines.next() else { return };
    let start: Value = serde_json::from_str(&first).expect("start is JSON");
    let resources = PathBuf::from(start["resources"].as_str().expect("start names the resources"));
    record(&resources, &format!("start dataDir={}", start["dataDir"].as_str().unwrap_or("")));
    // What the process was started with: the tests hold the shell to no arguments and no added environment (SH1.3).
    let process = json!({
        "args": std::env::args_os().skip(1).map(|arg| arg.to_string_lossy().into_owned()).collect::<Vec<_>>(),
        "env": std::env::vars_os()
            .map(|(name, value)| (name.to_string_lossy().into_owned(), Value::from(value.to_string_lossy().into_owned())))
            .collect::<serde_json::Map<_, _>>(),
    });
    fs::write(resources.join("process.json"), process.to_string()).expect("the record of the process");
    let plan = fs::read_to_string(resources.join("plan.txt")).expect("a plan");
    let mut codes = 0;
    for step in plan.lines() {
        let words: Vec<&str> = step.split_whitespace().collect();
        match words.as_slice() {
            ["ready", port, code, rest @ ..] => say(json!({
                "type": "ready", "port": port.parse::<u16>().unwrap(), "code": code,
                "portChanged": rest.contains(&"portChanged"), "gatewayPort": 1,
            })),
            ["fatal", reason, rest @ ..] => match rest.first() {
                Some(backup) => say(json!({"type": "fatal", "reason": reason, "backup": backup})),
                None => say(json!({"type": "fatal", "reason": reason})),
            },
            ["exit", code] => std::process::exit(code.parse().unwrap()),
            ["serve", linger @ ..] => {
                let linger = linger.first().map(|ms| Duration::from_millis(ms.parse().unwrap()));
                for line in lines.by_ref() {
                    let Ok(line) = line else { break };
                    let message: Value = serde_json::from_str(&line).unwrap_or(Value::Null);
                    let kind = message["type"].as_str().unwrap_or("?").to_string();
                    record(&resources, &kind);
                    match kind.as_str() {
                        "open-code" => {
                            codes += 1;
                            say(json!({"type": "open-code", "code": format!("next{codes}")}));
                        }
                        "shutdown" if linger.is_some() => {
                            thread::sleep(linger.unwrap());
                            std::process::exit(0);
                        }
                        "shutdown" => std::process::exit(0),
                        _ => {}
                    }
                }
                record(&resources, "eof");
                std::process::exit(0);
            }
            ["deaf"] => {
                // Hears and never stops: only a kill ends it.
                record(&resources, &format!("pid {}", std::process::id()));
                for line in lines.by_ref() {
                    let Ok(line) = line else { break };
                    let message: Value = serde_json::from_str(&line).unwrap_or(Value::Null);
                    record(&resources, message["type"].as_str().unwrap_or("?"));
                }
                loop {
                    thread::sleep(Duration::from_secs(60));
                }
            }
            _ => panic!("unknown step {step}"),
        }
    }
}

fn say(message: Value) {
    let mut stdout = io::stdout().lock();
    writeln!(stdout, "{message}").unwrap();
    stdout.flush().unwrap();
}

fn record(resources: &Path, what: &str) {
    let mut file = OpenOptions::new().create(true).append(true).open(resources.join("received.txt")).unwrap();
    writeln!(file, "{what}").unwrap();
}
