// SPDX-License-Identifier: AGPL-3.0-only

//! The examples of the signatures of a release that the app and the site read alike (specs/2026-11-16-desk-app-mirror.md
//! AM2.4): `tests/testdata/update-signatures.json` carries the public halves of the keys of a test — no secret half lies
//! anywhere — and for each example the bytes, the signature and the two outcomes it must have: `app`, which this test
//! proves by the reading and the verification of the app (`manifest`), and `site`, which the site proves by its own check.
//! The two differ only on a signature without the prehash of BLAKE2b («Ed»): the app takes it as minisign does, the site
//! refuses it — the signer of a release never writes one. The file is written again only with
//! `CANONKEEPER_UPDATE_SAMPLES_RECORD=1`, by the signer of a release on made-up installers and new keys of the test; the
//! example «Ed» is made here from `ed25519-dalek` and `blake2`.

use std::fs;
use std::path::PathBuf;

use base64::engine::general_purpose::STANDARD;
use base64::Engine;
use canonkeeper_desktop_core::update::manifest::{self, key_id, Keys, Refused};
use canonkeeper_desktop_core::update::policy::{Limits, Policy, HOSTS};
use canonkeeper_desktop_core::update::release::{sign, SITE_LATEST};
use canonkeeper_desktop_core::update::sign::{generate, trusted, KeyPair, SecretKey};
use canonkeeper_desktop_core::update::Kind;
use ed25519_dalek::{Signer, SigningKey};
use semver::Version;
use serde_json::{json, Value};
use url::Url;

const SAMPLES: &str = "tests/testdata/update-signatures.json";
const VERSION: &str = "1.3.0";
const INSTALLER: &str = "CanonKeeper_1.3.0_amd64.deb";
const TIMESTAMP: u64 = 1_789_000_000;

/// The examples by name, in the order of the file.
const NAMES: [&str; 10] = [
    "the manifest of the site",
    "an installer",
    "an installer by the spare key",
    "Ed without the prehash",
    "another key",
    "the number of the main key on the signature of another key",
    "another version in the trusted comment under the old global signature",
    "changed bytes",
    "signed for another version",
    "no global signature",
];

/// The word of an outcome of the app.
fn word(outcome: Result<manifest::Signer, Refused>) -> &'static str {
    match outcome {
        Ok(_) => "accept",
        Err(Refused::Encoding) => "encoding",
        Err(Refused::UnknownKey) => "unknown_key",
        Err(Refused::Signature) => "signature",
        Err(Refused::NoVersion) => "no_version",
        Err(Refused::OtherVersion) => "other_version",
    }
}

#[test]
fn the_examples_of_signatures_are_what_the_app_reads_them_as() {
    let path = PathBuf::from(env!("CARGO_MANIFEST_DIR")).join(SAMPLES);
    if std::env::var("CANONKEEPER_UPDATE_SAMPLES_RECORD").as_deref() == Ok("1") {
        let recorded = serde_json::to_string_pretty(&record()).unwrap();
        fs::write(&path, format!("{recorded}\n")).unwrap();
    }
    let samples: Value = serde_json::from_slice(&fs::read(&path).unwrap()).unwrap();
    let keys = Keys {
        main: samples["keys"]["main"].as_str().unwrap().to_string(),
        spare: samples["keys"]["spare"].as_str().unwrap().to_string(),
    };
    assert_ne!(key_id(&keys.main), key_id(&keys.spare));
    let version = Version::parse(samples["version"].as_str().unwrap()).unwrap();
    let examples = samples["samples"].as_array().unwrap();
    let names: Vec<&str> = examples.iter().map(|example| example["name"].as_str().unwrap()).collect();
    assert_eq!(names, NAMES);
    for example in examples {
        let name = example["name"].as_str().unwrap();
        let data = STANDARD.decode(example["data"].as_str().unwrap()).unwrap();
        let signature = example["signature"].as_str().unwrap();
        let outcome = word(manifest::verify_file(&data, signature, &keys, &version));
        assert_eq!(outcome, example["app"].as_str().unwrap(), "{name}");
        assert!(example["site"].is_string(), "{name}");
    }
    // The manifest of the site is what the app reads: a file of each kind at its address of the site.
    let site = &examples[0];
    assert_eq!(site["file"], SITE_LATEST);
    let body = STANDARD.decode(site["data"].as_str().unwrap()).unwrap();
    for kind in Kind::RELEASED {
        let read = manifest::read(&body, site["signature"].as_str().unwrap(), &keys, kind).unwrap();
        assert!(read.url.as_str().starts_with("https://canonkeeper.ru/app/open/1.3.0/"), "{}", read.url);
    }
}

/// The examples, by the signer of a release on made-up installers and new keys of the test.
fn record() -> Value {
    let (main, spare, stranger) = (generate(), generate(), generate());
    let keys = Keys { main: main.public.clone(), spare: spare.public.clone() };
    let policy = Policy {
        endpoints: vec![],
        hosts: HOSTS.iter().map(|host| host.to_string()).collect(),
        loopback: false,
        keys: keys.clone(),
        limits: Limits::RELEASE,
    };
    let dir = std::env::temp_dir().join(format!("canonkeeper-update-samples-{}", std::process::id()));
    let _ = fs::remove_dir_all(&dir);
    fs::create_dir_all(&dir).unwrap();
    for file in [
        "CanonKeeper_1.3.0_x64-setup.exe",
        INSTALLER,
        "CanonKeeper-1.3.0-1.x86_64.rpm",
        "CanonKeeper_1.3.0_amd64.AppImage",
    ] {
        fs::write(dir.join(file), format!("the installer {file}")).unwrap();
    }
    let base = Url::parse("https://github.com/canonkeeper/desktop-app/releases/download/v1.3.0/").unwrap();
    let version = Version::parse(VERSION).unwrap();
    let key = SecretKey::read(&main.secret).unwrap();
    sign(&dir, &version, &base, "Что нового:\n— быстрее", &key, &policy, TIMESTAMP).unwrap();
    let site = fs::read(dir.join(SITE_LATEST)).unwrap();
    let site_signature = fs::read_to_string(dir.join(format!("{SITE_LATEST}.sig"))).unwrap();
    let installer = fs::read(dir.join(INSTALLER)).unwrap();
    let signature = fs::read_to_string(dir.join(format!("{INSTALLER}.sig"))).unwrap();
    let _ = fs::remove_dir_all(&dir);

    let comment = trusted(INSTALLER, VERSION, TIMESTAMP);
    let spare_key = SecretKey::read(&spare.secret).unwrap();
    let stranger_key = SecretKey::read(&stranger.secret).unwrap();
    let by_stranger = stranger_key.sign(&installer, &comment);
    let mut changed = installer.clone();
    changed[0] ^= 1;
    let example = |name: &str, file: &str, data: &[u8], signature: &str, app: &str, site: &str| json!({"name": name, "file": file, "data": STANDARD.encode(data), "signature": signature, "app": app, "site": site});
    json!({
        "about": "The examples of the signatures of a release (specs/2026-11-16-desk-app-mirror.md AM2.4): the public halves of keys of a test, the bytes of each example, its signature, and what the app and the site answer. Written by tests/update_signatures.rs with CANONKEEPER_UPDATE_SAMPLES_RECORD=1.",
        "keys": {"main": keys.main, "spare": keys.spare},
        "version": VERSION,
        "samples": [
            example(NAMES[0], SITE_LATEST, &site, &site_signature, "accept", "accept"),
            example(NAMES[1], INSTALLER, &installer, &signature, "accept", "accept"),
            example(NAMES[2], INSTALLER, &installer, &spare_key.sign(&installer, &comment), "accept", "accept"),
            example(NAMES[3], INSTALLER, &installer, &without_prehash(&main, &installer, &comment), "accept", "legacy"),
            example(NAMES[4], INSTALLER, &installer, &by_stranger, "unknown_key", "unknown_key"),
            example(NAMES[5], INSTALLER, &installer, &with_key_id(&by_stranger, key_id(&main.public).unwrap()), "signature", "signature"),
            example(NAMES[6], INSTALLER, &installer, &with_comment(&signature, &trusted(INSTALLER, "1.4.0", TIMESTAMP)), "signature", "signature"),
            example(NAMES[7], INSTALLER, &changed, &signature, "signature", "signature"),
            example(NAMES[8], INSTALLER, &installer, &key.sign(&installer, &trusted(INSTALLER, "1.2.0", TIMESTAMP)), "other_version", "other_version"),
            example(NAMES[9], INSTALLER, &installer, &without_global(&signature), "encoding", "encoding"),
        ],
    })
}

/// The lines of the text of a signature, the base64 of it.
fn lines(signature: &str) -> Vec<String> {
    let text = String::from_utf8(STANDARD.decode(signature.trim()).unwrap()).unwrap();
    text.lines().map(str::to_string).collect()
}

fn signature_of(lines: &[String]) -> String {
    STANDARD.encode(format!("{}\n", lines.join("\n")))
}

/// A signature of `data` by the secret half of `pair` without the prehash: Ed25519 over the bytes themselves, as minisign
/// signed before its prehash («Ed»), with the global signature over the signature and the trusted comment.
fn without_prehash(pair: &KeyPair, data: &[u8], trusted: &str) -> String {
    let secret = lines(&pair.secret);
    let bytes = STANDARD.decode(&secret[1]).unwrap();
    let key_id: [u8; 8] = bytes[54..62].try_into().unwrap();
    let signing = SigningKey::from_bytes(&bytes[62..94].try_into().unwrap());
    let signature = signing.sign(data).to_bytes();
    let global = signing.sign(&[&signature[..], trusted.as_bytes()].concat()).to_bytes();
    let first = [&b"Ed"[..], &key_id, &signature].concat();
    signature_of(&[
        "untrusted comment: signature from CanonKeeper secret key".to_string(),
        STANDARD.encode(first),
        format!("trusted comment: {trusted}"),
        STANDARD.encode(global),
    ])
}

/// The signature with the number of another key in its first line, its bytes as they were.
fn with_key_id(signature: &str, key_id: [u8; 8]) -> String {
    let mut lines = lines(signature);
    let mut first = STANDARD.decode(&lines[1]).unwrap();
    first[2..10].copy_from_slice(&key_id);
    lines[1] = STANDARD.encode(first);
    signature_of(&lines)
}

/// The signature with another trusted comment under its old global signature.
fn with_comment(signature: &str, trusted: &str) -> String {
    let mut lines = lines(signature);
    lines[2] = format!("trusted comment: {trusted}");
    signature_of(&lines)
}

/// The signature without its global signature: three lines.
fn without_global(signature: &str) -> String {
    signature_of(&lines(signature)[..3])
}
