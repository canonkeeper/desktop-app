// SPDX-License-Identifier: AGPL-3.0-only

//! The network of an update (specs/2026-11-13-desk-updates.md UB1.3, UB1.6, UB2.2): the shell walks the addresses itself —
//! one request of `latest.json` and one of its signature an address, `https` to the hosts of the list only, with a limit
//! of time on the connection and the answer and 64 KiB at most; an answer that is not 2xx, not JSON, without the kind of
//! this installation or without a signature of a key of the app sends it to the next address, and «нет связи» is only
//! when no address answered. A file is downloaded once, checked by the key its signature names, at most 1 GiB, with a
//! limit on standing still and none in all. Every step of a redirect goes only where a first request may; a request
//! carries nothing of the author or of the computer — the name of the client and what it accepts.

use std::io::Read;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;
use std::time::Instant;

use reqwest::blocking::{Client, Response};
use reqwest::header::{HeaderValue, ACCEPT};
use reqwest::redirect;
use url::Url;

use super::manifest::{self, Release, Signer};
use super::policy::Policy;
use super::Kind;

/// The name of the client in a request of an update; nothing else is said.
pub const CLIENT: &str = "CanonKeeper";

/// What the walk over the addresses found.
#[derive(Debug, Clone, PartialEq)]
pub enum Checked {
    /// An address answered with a release that its signature proves.
    Found(Box<Release>),
    /// No address answered: «нет связи».
    NoAnswer,
}

/// A file downloaded and checked: its bytes and the key that signed it.
#[derive(Clone)]
pub struct Downloaded {
    pub release: Release,
    pub bytes: Arc<Vec<u8>>,
    pub signer: Signer,
}

impl PartialEq for Downloaded {
    fn eq(&self, other: &Downloaded) -> bool {
        self.release == other.release && self.bytes == other.bytes && self.signer == other.signer
    }
}

impl std::fmt::Debug for Downloaded {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        formatter
            .debug_struct("Downloaded")
            .field("version", &self.release.version.to_string())
            .field("bytes", &self.bytes.len())
            .field("signer", &self.signer)
            .finish()
    }
}

/// Why a download ended without a file.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum NotDownloaded {
    /// The network, an address or a redirect the policy refuses, a file too large: «Не удалось скачать».
    Failed,
    /// The bytes are not what a key of the app signed for this version: «Файл не тот».
    Refused,
    /// The author or an erasure stopped it.
    Canceled,
}

/// The network of an update under its policy.
pub struct Network {
    policy: Policy,
    client: Client,
}

impl Network {
    pub fn new(policy: Policy) -> Network {
        // The TLS of the updater plugin: rustls with ring, once for the process.
        let _ = rustls::crypto::ring::default_provider().install_default();
        let redirects = policy.clone();
        let mut builder = Client::builder()
            .user_agent(CLIENT)
            .connect_timeout(policy.limits.connect)
            .timeout(policy.limits.idle)
            .redirect(redirect::Policy::custom(move |attempt| {
                // UB1.6: a step of a redirect only where a first request may go; otherwise no request is made.
                if attempt.previous().len() >= 10 || !redirects.allowed(attempt.url()) {
                    attempt.stop()
                } else {
                    attempt.follow()
                }
            }));
        if policy.loopback {
            builder = builder.no_proxy();
        }
        let client = builder.build().expect("the client of updates");
        Network { policy, client }
    }

    /// The addresses in their order (UB1.3): the first that answers with a release of this kind its signature proves.
    pub fn check(&self, kind: Kind) -> Checked {
        for endpoint in &self.policy.endpoints {
            let Some(body) = self.small(endpoint, "application/json") else { continue };
            let Some(signature) = self.small(&Policy::signature_of(endpoint), "text/plain") else { continue };
            let Ok(signature) = String::from_utf8(signature) else { continue };
            if let Ok(release) = manifest::read(&body, &signature, &self.policy.keys, kind) {
                return Checked::Found(Box::new(release));
            }
        }
        Checked::NoAnswer
    }

    /// One answer of an address: 2xx, at most the limit, within the time of an answer.
    fn small(&self, address: &str, accept: &'static str) -> Option<Vec<u8>> {
        let url = Url::parse(address).ok().filter(|url| self.policy.allowed(url))?;
        let started = Instant::now();
        let limits = self.policy.limits;
        let response =
            self.client.get(url).header(ACCEPT, HeaderValue::from_static(accept)).timeout(limits.wait).send().ok()?;
        if !response.status().is_success() || response.content_length().is_some_and(|length| length > limits.answer) {
            return None;
        }
        let mut body = Vec::new();
        let mut reader = response.take(limits.answer + 1);
        let mut chunk = [0u8; 8192];
        loop {
            let read = reader.read(&mut chunk).ok()?;
            if read == 0 {
                break;
            }
            body.extend_from_slice(&chunk[..read]);
            if body.len() as u64 > limits.answer || started.elapsed() > limits.wait {
                return None;
            }
        }
        Some(body)
    }

    /// Downloads the file of `release` once and checks it by the key its signature names (UB2.2); `progress` hears the
    /// percent while the size is known, `cancel` stops it between two reads.
    pub fn download(
        &self,
        release: &Release,
        cancel: &AtomicBool,
        mut progress: impl FnMut(u8),
    ) -> Result<Downloaded, NotDownloaded> {
        // The key is chosen by the number its signature carries before anything is fetched: one download.
        manifest::signer(&release.signature, &self.policy.keys).map_err(|_| NotDownloaded::Refused)?;
        if !self.policy.allowed(&release.url) {
            return Err(NotDownloaded::Failed);
        }
        let limits = self.policy.limits;
        let response = self
            .client
            .get(release.url.clone())
            .header(ACCEPT, HeaderValue::from_static("application/octet-stream"))
            .send()
            .map_err(|_| NotDownloaded::Failed)?;
        if !response.status().is_success() {
            return Err(NotDownloaded::Failed);
        }
        let size = response.content_length();
        if size.is_some_and(|size| size > limits.file) {
            return Err(NotDownloaded::Failed);
        }
        let bytes = read_file(response, size, limits.file, cancel, &mut progress)?;
        let signer = manifest::verify_file(&bytes, &release.signature, &self.policy.keys, &release.version)
            .map_err(|_| NotDownloaded::Refused)?;
        Ok(Downloaded { release: release.clone(), bytes: Arc::new(bytes), signer })
    }
}

fn read_file(
    mut response: Response,
    size: Option<u64>,
    limit: u64,
    cancel: &AtomicBool,
    progress: &mut impl FnMut(u8),
) -> Result<Vec<u8>, NotDownloaded> {
    let mut bytes = Vec::with_capacity(size.unwrap_or(0).min(limit) as usize);
    let mut chunk = vec![0u8; 64 * 1024];
    let mut said = None;
    loop {
        if cancel.load(Ordering::SeqCst) {
            return Err(NotDownloaded::Canceled);
        }
        // Each read waits at most the limit of standing still (the timeout of the client).
        let read = response.read(&mut chunk).map_err(|_| NotDownloaded::Failed)?;
        if read == 0 {
            break;
        }
        bytes.extend_from_slice(&chunk[..read]);
        if bytes.len() as u64 > limit {
            return Err(NotDownloaded::Failed);
        }
        if let Some(size) = size.filter(|size| *size > 0) {
            let percent = ((bytes.len() as u64 * 100) / size).min(100) as u8;
            if said != Some(percent) {
                said = Some(percent);
                progress(percent);
            }
        }
    }
    if cancel.load(Ordering::SeqCst) {
        return Err(NotDownloaded::Canceled);
    }
    if size.is_some_and(|size| size != bytes.len() as u64) {
        return Err(NotDownloaded::Failed);
    }
    Ok(bytes)
}
