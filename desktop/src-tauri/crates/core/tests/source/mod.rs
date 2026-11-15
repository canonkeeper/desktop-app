// SPDX-License-Identifier: AGPL-3.0-only

//! A stand-in source of versions on 127.0.0.1 (specs/2026-11-13-desk-updates.md UB1.3, UB1.6): every path answers as the
//! test says — a body, a status, a redirect, silence or a body too long — and every request it got is recorded with its
//! headers. The tests never reach the site or GitHub.

#![allow(dead_code)]

use std::collections::HashMap;
use std::io::{BufRead, BufReader, Write};
use std::net::{TcpListener, TcpStream};
use std::sync::{Arc, Mutex};
use std::thread;
use std::time::Duration;

use canonkeeper_desktop_core::update::manifest::Keys;
use canonkeeper_desktop_core::update::sign::{generate, trusted, SecretKey};
use serde_json::json;

/// How a path answers.
#[derive(Clone)]
pub enum Reply {
    Body(Vec<u8>, &'static str),
    Status(u16),
    /// A redirect to another address.
    Redirect(String),
    /// The connection is taken and nothing comes for this long.
    Silent(Duration),
}

/// A request the source got: the path and the headers, names in lower case.
#[derive(Debug, Clone)]
pub struct Request {
    pub path: String,
    pub headers: Vec<(String, String)>,
}

#[derive(Clone)]
pub struct Source {
    pub base: String,
    routes: Arc<Mutex<HashMap<String, Reply>>>,
    requests: Arc<Mutex<Vec<Request>>>,
}

impl Source {
    pub fn start() -> Source {
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let base = format!("http://{}", listener.local_addr().unwrap());
        let source = Source { base, routes: Arc::default(), requests: Arc::default() };
        let serving = source.clone();
        thread::spawn(move || {
            for stream in listener.incoming().flatten() {
                let serving = serving.clone();
                thread::spawn(move || serving.answer(stream));
            }
        });
        source
    }

    /// The address of `path` on this source.
    pub fn at(&self, path: &str) -> String {
        format!("{}{path}", self.base)
    }

    pub fn route(&self, path: &str, reply: Reply) {
        self.routes.lock().unwrap().insert(path.to_string(), reply);
    }

    pub fn requests(&self) -> Vec<Request> {
        self.requests.lock().unwrap().clone()
    }

    /// Whether a request of `path` came.
    pub fn asked(&self, path: &str) -> bool {
        self.requests().iter().any(|request| request.path == path)
    }

    fn answer(&self, mut stream: TcpStream) {
        let mut reader = BufReader::new(stream.try_clone().unwrap());
        let mut first = String::new();
        if reader.read_line(&mut first).is_err() {
            return;
        }
        let path = first.split_whitespace().nth(1).unwrap_or("").to_string();
        let mut headers = Vec::new();
        loop {
            let mut line = String::new();
            if reader.read_line(&mut line).unwrap_or(0) == 0 || line == "\r\n" {
                break;
            }
            if let Some((name, value)) = line.trim_end().split_once(':') {
                headers.push((name.trim().to_ascii_lowercase(), value.trim().to_string()));
            }
        }
        self.requests.lock().unwrap().push(Request { path: path.clone(), headers });
        let reply = self.routes.lock().unwrap().get(&path).cloned().unwrap_or(Reply::Status(404));
        let head = |status: u16, extra: &str, length: usize| {
            format!("HTTP/1.1 {status} X\r\nContent-Length: {length}\r\n{extra}Connection: close\r\n\r\n")
        };
        let _ = match reply {
            Reply::Body(body, kind) => stream
                .write_all(head(200, &format!("Content-Type: {kind}\r\n"), body.len()).as_bytes())
                .and_then(|()| stream.write_all(&body)),
            Reply::Status(status) => stream.write_all(head(status, "", 0).as_bytes()),
            Reply::Redirect(to) => stream.write_all(head(302, &format!("Location: {to}\r\n"), 0).as_bytes()),
            Reply::Silent(time) => {
                thread::sleep(time);
                Ok(())
            }
        };
    }
}

/// A key pair of one test, its secret half only in memory.
pub struct Signer {
    pub key: SecretKey,
    pub public: String,
}

impl Signer {
    pub fn new() -> Signer {
        let pair = generate();
        Signer { key: SecretKey::read(&pair.secret).unwrap(), public: pair.public }
    }

    pub fn sign(&self, data: &[u8], file: &str, version: &str) -> String {
        self.key.sign(data, &trusted(file, version, 1_763_000_000))
    }
}

/// The main and the spare key of the app of a test.
pub fn keys(main: &Signer, spare: &Signer) -> Keys {
    Keys { main: main.public.clone(), spare: spare.public.clone() }
}

/// `latest.json` of `version` whose file of the kind `platform` lies at `url`, signed by `signer`.
pub fn latest(version: &str, platform: &str, url: &str, signature: &str) -> Vec<u8> {
    serde_json::to_vec(&json!({
        "version": version,
        "notes": "Что нового в Северном пределе",
        "pub_date": "2026-11-13T10:00:00Z",
        "platforms": {platform: {"url": url, "signature": signature}},
    }))
    .unwrap()
}

/// Puts on `source` at `path` a release of `version` for `platform`: its file, the signature of the file and `latest.json`
/// with its signature, all by `signer`.
pub fn publish(source: &Source, path: &str, version: &str, platform: &str, file: &[u8], signer: &Signer) {
    let file_path = format!("{path}/file-{version}");
    source.route(&file_path, Reply::Body(file.to_vec(), "application/octet-stream"));
    let body = latest(version, platform, &source.at(&file_path), &signer.sign(file, "file", version));
    let signature = signer.sign(&body, "latest.json", version);
    source.route(&format!("{path}/latest.json"), Reply::Body(body, "application/json"));
    source.route(&format!("{path}/latest.json.sig"), Reply::Body(signature.into_bytes(), "text/plain"));
}
