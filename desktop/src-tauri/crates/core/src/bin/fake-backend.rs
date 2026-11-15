// SPDX-License-Identifier: AGPL-3.0-only

//! A stand-in for the backend in the tests of the supervision (tests/driver.rs): it speaks the control channel and
//! follows the plan in `<resources>/plan.txt` of its `start`, writing what it heard to `<resources>/received.txt` and
//! the `start` itself to `<resources>/start.json` (and every `start` of the stand, one a line, to `starts.jsonl`). It
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
    fs::write(resources.join("start.json"), &first).expect("the record of start");
    let mut starts = OpenOptions::new().create(true).append(true).open(resources.join("starts.jsonl")).unwrap();
    writeln!(starts, "{first}").expect("the record of every start");
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
    // `on <phase> <line>`: when an `update-state` of the phase comes — `idle` names what was found — the backend says the
    // line once, or `exit <code>` ends it (specs/2026-11-13-desk-updates.md UB1.1).
    let mut reactions: Vec<(String, String, bool)> = Vec::new();
    // `folder-answer <outcome>`: a `folder-picked` gets the `folder-result` of this outcome (specs/2026-11-13-desk-obsidian.md
    // OB1.3); without it the backend never answers.
    let mut folder_answer: Option<String> = None;
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
            ["secret-save", id, value, request] => {
                say(json!({"type": "secret-save", "id": id, "value": value, "request": request}))
            }
            ["secret-delete", id, request] => say(json!({"type": "secret-delete", "id": id, "request": request})),
            // The word of the author on the page (specs/2026-11-13-desk-erase.md ER1.2).
            ["erase"] => say(json!({"type": "erase"})),
            ["folder-answer", outcome] => folder_answer = Some(outcome.to_string()),
            ["on", phase, ..] => {
                let line = step.trim_start().trim_start_matches("on").trim_start().trim_start_matches(phase).trim();
                reactions.push((phase.to_string(), line.to_string(), false));
            }
            // A line of the channel as it is: a step of the start, a fatal with its data (specs/2026-11-13-desk-upgrade.md).
            ["say", ..] => {
                say(serde_json::from_str(step.trim_start().trim_start_matches("say").trim()).expect("a line of JSON"))
            }
            // `serve`: hears until its end; `serve <ms>` lingers that long after `shutdown`, `serve stop <code>` ends a
            // `shutdown` with that code — a backend that could not stop its cluster (specs/2026-11-13-desk-updates.md UB2.3).
            ["serve", rest @ ..] => {
                let (linger, stop) = match rest {
                    ["stop", code] => (None, code.parse::<i32>().unwrap()),
                    _ => (rest.first().map(|ms| Duration::from_millis(ms.parse().unwrap())), 0),
                };
                for line in lines.by_ref() {
                    let Ok(line) = line else { break };
                    let message: Value = serde_json::from_str(&line).unwrap_or(Value::Null);
                    let kind = message["type"].as_str().unwrap_or("?").to_string();
                    if kind == "secret-result" {
                        let request = message["request"].as_str().unwrap_or("?");
                        record(&resources, &format!("secret-result {request} {}", message["ok"]));
                        continue;
                    }
                    if kind == "update-state" {
                        let phase = message["phase"].as_str().unwrap_or("?");
                        let phase = if phase == "idle" { message["found"].as_str().unwrap_or("?") } else { phase };
                        record(&resources, &format!("update-state {phase}"));
                        for (when, line, done) in reactions.iter_mut().filter(|(when, _, done)| when == phase && !*done)
                        {
                            let _ = when;
                            *done = true;
                            match line.split_whitespace().collect::<Vec<_>>().as_slice() {
                                ["exit", code] => std::process::exit(code.parse().unwrap()),
                                _ => say(serde_json::from_str(line).expect("a line of JSON")),
                            }
                        }
                        continue;
                    }
                    if kind == "folder-picked" {
                        let request = message["request"].as_str().unwrap_or("?");
                        record(&resources, &format!("folder-picked {}", message["path"].as_str().unwrap_or("?")));
                        if let Some(outcome) = &folder_answer {
                            say(json!({"type": "folder-result", "request": request, "outcome": outcome}));
                        }
                        continue;
                    }
                    record(&resources, &kind);
                    match kind.as_str() {
                        "open-code" => {
                            codes += 1;
                            say(json!({"type": "open-code", "code": format!("next{codes}")}));
                        }
                        "shutdown" => {
                            if let Some(linger) = linger {
                                thread::sleep(linger);
                            }
                            std::process::exit(stop);
                        }
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
