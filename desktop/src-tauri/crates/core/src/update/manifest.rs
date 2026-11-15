// SPDX-License-Identifier: AGPL-3.0-only

//! What a release says and what proves it (specs/2026-11-13-desk-updates.md UB1.4, UB1.5, UB2.2, UB3.3): a signature is
//! checked by the key whose number it carries — the main or the spare key of the app — with the version of its trusted
//! comment, by `minisign-verify`, the check the updater plugin makes. `latest.json` is taken only with its own signature:
//! its version, its notes and its addresses are shown or used only after it. A release check of CI reads a release with
//! the same functions.

use base64::engine::general_purpose::STANDARD;
use base64::Engine;
use minisign_verify::{PublicKey, Signature};
use semver::Version;
use serde_json::Value;
use url::Url;

use super::Kind;

/// The notes of a release are plain text no longer than this (UB1.4).
pub const NOTES_LIMIT: usize = 8 * 1024;

/// The keys the app trusts: the main one and the spare one, each the base64 of the text of a minisign public key.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Keys {
    pub main: String,
    pub spare: String,
}

/// Which of the two keys made a signature.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Signer {
    Main,
    Spare,
}

impl Keys {
    /// The key that `signer` names.
    pub fn of(&self, signer: Signer) -> &str {
        match signer {
            Signer::Main => &self.main,
            Signer::Spare => &self.spare,
        }
    }
}

/// Why a signature does not prove its data.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Refused {
    /// Not the base64 of a text of a minisign signature or key.
    Encoding,
    /// Made by a key the app does not trust.
    UnknownKey,
    /// The data are not what the key signed.
    Signature,
    /// The trusted comment names no version.
    NoVersion,
    /// The trusted comment names another version.
    OtherVersion,
}

fn text(base64: &str) -> Option<String> {
    String::from_utf8(STANDARD.decode(base64.trim()).ok()?).ok()
}

/// The number of a key, the base64 of the text of a minisign public key.
pub fn key_id(public: &str) -> Option<[u8; 8]> {
    let line = text(public)?.lines().nth(1)?.trim().to_string();
    let bytes = STANDARD.decode(line).ok()?;
    (bytes.len() == 42).then(|| bytes[2..10].try_into().unwrap())
}

/// The number of the key that made a signature, the base64 of its text.
pub fn signed_by(signature: &str) -> Option<[u8; 8]> {
    let line = text(signature)?.lines().nth(1)?.trim().to_string();
    let bytes = STANDARD.decode(line).ok()?;
    (bytes.len() == 74).then(|| bytes[2..10].try_into().unwrap())
}

/// The key of `keys` a signature was made by, chosen by its number before anything is fetched (UB2.2).
pub fn signer(signature: &str, keys: &Keys) -> Result<Signer, Refused> {
    let by = signed_by(signature).ok_or(Refused::Encoding)?;
    if key_id(&keys.main) == Some(by) {
        Ok(Signer::Main)
    } else if key_id(&keys.spare) == Some(by) {
        Ok(Signer::Spare)
    } else {
        Err(Refused::UnknownKey)
    }
}

/// The version a trusted comment names (`…\tversion:1.3.0`).
pub fn signed_version(trusted: &str) -> Option<&str> {
    trusted.split('\t').find_map(|field| field.strip_prefix("version:"))
}

fn version_of(text: &str) -> Option<Version> {
    Version::parse(text.trim().trim_start_matches('v')).ok()
}

/// Checks that `signature` proves `data` by one of `keys` and returns the signer and the version it names; the version is
/// required (UB1.5).
pub fn verify(data: &[u8], signature: &str, keys: &Keys) -> Result<(Signer, Version), Refused> {
    let signer = signer(signature, keys)?;
    let public = PublicKey::decode(&text(keys.of(signer)).ok_or(Refused::Encoding)?).map_err(|_| Refused::Encoding)?;
    let decoded = Signature::decode(&text(signature).ok_or(Refused::Encoding)?).map_err(|_| Refused::Encoding)?;
    public.verify(data, &decoded, true).map_err(|_| Refused::Signature)?;
    let version = signed_version(decoded.trusted_comment()).ok_or(Refused::NoVersion)?;
    Ok((signer, version_of(version).ok_or(Refused::NoVersion)?))
}

/// Checks a file of a release against its signature: by one of `keys`, for `version` (UB2.2).
pub fn verify_file(data: &[u8], signature: &str, keys: &Keys, version: &Version) -> Result<Signer, Refused> {
    let (signer, signed) = verify(data, signature, keys)?;
    if &signed != version {
        return Err(Refused::OtherVersion);
    }
    Ok(signer)
}

/// A release `latest.json` announces for the kind of this installation, once its signature held.
#[derive(Debug, Clone, PartialEq)]
pub struct Release {
    pub version: Version,
    /// The notes of the release: plain text, at most [`NOTES_LIMIT`] bytes.
    pub notes: String,
    /// The file of the kind and its signature.
    pub url: Url,
    pub signature: String,
    /// `latest.json` as it came: the plugin installs from it (`restore_update`).
    pub raw: Value,
}

/// Why an answer of an address is not a release.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Unread {
    /// Its signature does not prove it.
    Signature(Refused),
    /// Not a JSON object with a version that its signature names.
    Form,
    /// No file for the kind of this installation.
    NoKind,
    /// Notes that are not plain text or too long.
    Notes,
}

/// Reads `latest.json`: its signature first, by one of `keys`, naming the version the file names; then the file of
/// `kind` and the notes (UB1.3, UB1.4).
pub fn read(body: &[u8], signature: &str, keys: &Keys, kind: Kind) -> Result<Release, Unread> {
    let (_, signed) = verify(body, signature, keys).map_err(Unread::Signature)?;
    let raw: Value = serde_json::from_slice(body).map_err(|_| Unread::Form)?;
    let version = raw.get("version").and_then(Value::as_str).and_then(version_of).ok_or(Unread::Form)?;
    if version != signed {
        return Err(Unread::Signature(Refused::OtherVersion));
    }
    let notes = match raw.get("notes") {
        None | Some(Value::Null) => String::new(),
        Some(Value::String(notes)) => notes.clone(),
        Some(_) => return Err(Unread::Notes),
    };
    if !plain_notes(&notes) {
        return Err(Unread::Notes);
    }
    let platform = kind.platform().ok_or(Unread::NoKind)?;
    let entry = raw.get("platforms").and_then(|platforms| platforms.get(platform)).ok_or(Unread::NoKind)?;
    let url = entry.get("url").and_then(Value::as_str).and_then(|url| Url::parse(url).ok()).ok_or(Unread::NoKind)?;
    let signature = entry.get("signature").and_then(Value::as_str).ok_or(Unread::NoKind)?.to_string();
    Ok(Release { version, notes, url, signature, raw })
}

/// Whether the notes of a release are what the app shows (UB1.4): plain text — lines and tabs, no other control
/// character — of at most [`NOTES_LIMIT`] bytes. The signer of a release holds its notes to the same rule.
pub fn plain_notes(notes: &str) -> bool {
    notes.len() <= NOTES_LIMIT && !notes.chars().any(|letter| letter.is_control() && !matches!(letter, '\n' | '\t'))
}

/// A release is an update only when it is later than the running version: the app never goes down (UB1.5).
pub fn is_update(release: &Release, running: &str) -> bool {
    version_of(running).is_some_and(|running| release.version > running)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::update::sign::{generate, trusted, SecretKey};
    use serde_json::json;

    fn keys() -> (SecretKey, SecretKey, Keys) {
        let (main, spare) = (generate(), generate());
        let keys = Keys { main: main.public.clone(), spare: spare.public.clone() };
        (SecretKey::read(&main.secret).unwrap(), SecretKey::read(&spare.secret).unwrap(), keys)
    }

    fn latest(version: &str, notes: &str) -> Vec<u8> {
        serde_json::to_vec(&json!({
            "version": version,
            "notes": notes,
            "pub_date": "2026-11-13T10:00:00Z",
            "platforms": {
                "linux-x86_64-deb": {"url": "https://github.com/canonkeeper/desktop-app/releases/download/v1.3.0/CanonKeeper_1.3.0_amd64.deb", "signature": "c2ln"},
                "windows-x86_64-nsis": {"url": "https://github.com/canonkeeper/desktop-app/releases/download/v1.3.0/CanonKeeper_1.3.0_x64-setup.exe", "signature": "c2ln"},
            }
        }))
        .unwrap()
    }

    #[test]
    fn latest_json_is_read_only_with_its_signature_by_a_key_of_the_app() {
        let (main, spare, keys) = keys();
        let body = latest("1.3.0", "Что нового:\n— быстрее");
        let release = read(&body, &main.sign(&body, &trusted("latest.json", "1.3.0", 1)), &keys, Kind::Deb).unwrap();
        assert_eq!(release.version, Version::new(1, 3, 0));
        assert_eq!(release.notes, "Что нового:\n— быстрее");
        assert!(release.url.as_str().ends_with("_amd64.deb"));
        assert!(is_update(&release, "1.2.0") && !is_update(&release, "1.3.0") && !is_update(&release, "1.4.0"));
        // The spare key is a key of the app too.
        assert!(read(&body, &spare.sign(&body, &trusted("latest.json", "1.3.0", 1)), &keys, Kind::Nsis).is_ok());
        // A stranger's key, a signature without a version or of another one, a changed body.
        let (stranger, _, _) = keys_alone();
        assert_eq!(
            read(&body, &stranger.sign(&body, &trusted("latest.json", "1.3.0", 1)), &keys, Kind::Deb),
            Err(Unread::Signature(Refused::UnknownKey))
        );
        assert_eq!(
            read(&body, &main.sign(&body, "timestamp:1\tfile:latest.json"), &keys, Kind::Deb),
            Err(Unread::Signature(Refused::NoVersion))
        );
        assert_eq!(
            read(&body, &main.sign(&body, &trusted("latest.json", "1.2.0", 1)), &keys, Kind::Deb),
            Err(Unread::Signature(Refused::OtherVersion))
        );
        let changed = latest("9.9.9", "");
        assert_eq!(
            read(&changed, &main.sign(&body, &trusted("latest.json", "1.3.0", 1)), &keys, Kind::Deb),
            Err(Unread::Signature(Refused::Signature))
        );
        assert_eq!(read(&body, "", &keys, Kind::Deb), Err(Unread::Signature(Refused::Encoding)));
        // No file for this kind; none at all for a build that updates by hand.
        let signed = main.sign(&body, &trusted("latest.json", "1.3.0", 1));
        assert_eq!(read(&body, &signed, &keys, Kind::Rpm), Err(Unread::NoKind));
        assert_eq!(read(&body, &signed, &keys, Kind::None), Err(Unread::NoKind));
    }

    fn keys_alone() -> (SecretKey, SecretKey, Keys) {
        keys()
    }

    #[test]
    fn the_notes_are_plain_text_and_short() {
        let (main, _, keys) = keys();
        for notes in ["\u{1b}[31mкрасный", &"а".repeat(NOTES_LIMIT / 2 + 1)] {
            let body = latest("1.3.0", notes);
            let signed = main.sign(&body, &trusted("latest.json", "1.3.0", 1));
            assert_eq!(read(&body, &signed, &keys, Kind::Deb), Err(Unread::Notes));
        }
    }

    #[test]
    fn a_file_is_checked_by_the_key_its_signature_names_for_its_version() {
        let (main, spare, keys) = keys();
        let version = Version::new(1, 3, 0);
        let file = b"the installer";
        assert_eq!(
            verify_file(file, &main.sign(file, &trusted("a.deb", "1.3.0", 1)), &keys, &version),
            Ok(Signer::Main)
        );
        assert_eq!(
            verify_file(file, &spare.sign(file, &trusted("a.deb", "v1.3.0", 1)), &keys, &version),
            Ok(Signer::Spare)
        );
        assert_eq!(
            verify_file(file, &main.sign(file, &trusted("a.deb", "1.2.0", 1)), &keys, &version),
            Err(Refused::OtherVersion)
        );
        assert_eq!(
            verify_file(file, &main.sign(file, "timestamp:1\tfile:a.deb"), &keys, &version),
            Err(Refused::NoVersion)
        );
        assert_eq!(
            verify_file(b"another installer", &main.sign(file, &trusted("a.deb", "1.3.0", 1)), &keys, &version),
            Err(Refused::Signature)
        );
        assert_eq!(signer(&main.sign(file, "x"), &keys), Ok(Signer::Main));
        assert_eq!(signer(&spare.sign(file, "x"), &keys), Ok(Signer::Spare));
    }
}
