// SPDX-License-Identifier: AGPL-3.0-only

//! The signer of a release (specs/2026-11-13-desk-updates.md UB3.2, UB3.4): keys and signatures in the format of minisign,
//! as `tauri signer` writes them and the updater plugin reads them — a key or a signature is the base64 of its text.
//! A signature is Ed25519 over the BLAKE2b-512 of the file («ED»), and its trusted comment names the file and the version
//! (`timestamp:…\tfile:…\tversion:…`), which the app requires. A secret key is kept without a password: the secret of the
//! environment `release` is already sealed by GitHub. The tests make their pairs here, on the fly; no secret half of any
//! key lies in the repository.

use base64::engine::general_purpose::STANDARD;
use base64::Engine;
use blake2::{Blake2b512, Digest};
use ed25519_dalek::{Signer, SigningKey};

/// Why a secret key is not one.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum KeyError {
    /// Not the base64 of a text of a minisign secret key.
    Encoding,
    /// A key under a password: the signer takes only a key without one.
    Encrypted,
    /// The checksum of the key does not hold.
    Checksum,
}

/// A pair of keys: both halves as the base64 of their texts.
#[derive(Clone)]
pub struct KeyPair {
    pub public: String,
    pub secret: String,
}

impl std::fmt::Debug for KeyPair {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        formatter.debug_struct("KeyPair").field("public", &self.public).finish_non_exhaustive()
    }
}

/// A secret key that signs.
pub struct SecretKey {
    key_id: [u8; 8],
    signing: SigningKey,
}

impl std::fmt::Debug for SecretKey {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        formatter.write_str("SecretKey(…)")
    }
}

const ED: [u8; 2] = *b"Ed";
const PREHASHED: [u8; 2] = *b"ED";
const NO_KDF: [u8; 2] = [0, 0];
const CHECKSUM: [u8; 2] = *b"B2";

/// A new pair of keys from the randomness of the system.
pub fn generate() -> KeyPair {
    let mut seed = [0u8; 32];
    let mut key_id = [0u8; 8];
    getrandom::fill(&mut seed).expect("the randomness of the system");
    getrandom::fill(&mut key_id).expect("the randomness of the system");
    let key = SecretKey { key_id, signing: SigningKey::from_bytes(&seed) };
    KeyPair { public: key.public(), secret: key.secret() }
}

fn blake2b256(parts: &[&[u8]]) -> [u8; 32] {
    let mut hasher = blake2::Blake2b::<blake2::digest::consts::U32>::new();
    for part in parts {
        hasher.update(part);
    }
    hasher.finalize().into()
}

fn key_name(key_id: &[u8; 8]) -> String {
    format!("{:016X}", u64::from_le_bytes(*key_id))
}

impl SecretKey {
    /// Reads a secret key: the base64 of a text of a minisign secret key without a password.
    pub fn read(text: &str) -> Result<SecretKey, KeyError> {
        let decoded = STANDARD.decode(text.trim()).map_err(|_| KeyError::Encoding)?;
        let decoded = String::from_utf8(decoded).map_err(|_| KeyError::Encoding)?;
        let line = decoded.lines().nth(1).ok_or(KeyError::Encoding)?;
        let bytes = STANDARD.decode(line.trim()).map_err(|_| KeyError::Encoding)?;
        if bytes.len() != 158 || bytes[0..2] != ED || bytes[4..6] != CHECKSUM {
            return Err(KeyError::Encoding);
        }
        if bytes[2..4] != NO_KDF {
            return Err(KeyError::Encrypted);
        }
        let mut key_id = [0u8; 8];
        key_id.copy_from_slice(&bytes[54..62]);
        let secret = &bytes[62..126];
        if blake2b256(&[&ED, &key_id, secret]) != bytes[126..158] {
            return Err(KeyError::Checksum);
        }
        let mut seed = [0u8; 32];
        seed.copy_from_slice(&secret[..32]);
        let signing = SigningKey::from_bytes(&seed);
        if signing.verifying_key().to_bytes() != secret[32..64] {
            return Err(KeyError::Checksum);
        }
        Ok(SecretKey { key_id, signing })
    }

    /// The public half as the app keeps it: the base64 of the text of a minisign public key.
    pub fn public(&self) -> String {
        let mut bytes = ED.to_vec();
        bytes.extend_from_slice(&self.key_id);
        bytes.extend_from_slice(&self.signing.verifying_key().to_bytes());
        let text =
            format!("untrusted comment: minisign public key: {}\n{}\n", key_name(&self.key_id), STANDARD.encode(bytes));
        STANDARD.encode(text)
    }

    fn secret(&self) -> String {
        let mut keynum = self.key_id.to_vec();
        keynum.extend_from_slice(&self.signing.to_bytes());
        keynum.extend_from_slice(&self.signing.verifying_key().to_bytes());
        let checksum = blake2b256(&[&ED, &keynum]);
        let mut bytes = ED.to_vec();
        bytes.extend_from_slice(&NO_KDF);
        bytes.extend_from_slice(&CHECKSUM);
        bytes.extend_from_slice(&[0u8; 48]);
        bytes.extend_from_slice(&keynum);
        bytes.extend_from_slice(&checksum);
        let text =
            format!("untrusted comment: minisign secret key {}\n{}\n", key_name(&self.key_id), STANDARD.encode(bytes));
        STANDARD.encode(text)
    }

    /// Signs `data`: the base64 of the text of a minisign signature with `trusted` as its trusted comment.
    pub fn sign(&self, data: &[u8], trusted: &str) -> String {
        let signature = self.signing.sign(&Blake2b512::digest(data)).to_bytes();
        let mut global = signature.to_vec();
        global.extend_from_slice(trusted.as_bytes());
        let global = self.signing.sign(&global).to_bytes();
        let mut first = PREHASHED.to_vec();
        first.extend_from_slice(&self.key_id);
        first.extend_from_slice(&signature);
        let text = format!(
            "untrusted comment: signature from CanonKeeper secret key\n{}\ntrusted comment: {trusted}\n{}\n",
            STANDARD.encode(first),
            STANDARD.encode(global)
        );
        STANDARD.encode(text)
    }
}

/// The trusted comment of a signature of a release: when, which file and which version — the app requires the version
/// (UB1.5, `requireSignedVersion`).
pub fn trusted(file: &str, version: &str, timestamp: u64) -> String {
    format!("timestamp:{timestamp}\tfile:{file}\tversion:{version}")
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_new_pair_signs_and_its_secret_reads_back() {
        let pair = generate();
        let key = SecretKey::read(&pair.secret).unwrap();
        assert_eq!(key.public(), pair.public);
        // The public half is the text of a minisign public key the verifier of the plugin reads.
        let text = String::from_utf8(STANDARD.decode(&pair.public).unwrap()).unwrap();
        assert!(text.starts_with("untrusted comment: minisign public key: "), "{text}");
        let public = minisign_verify::PublicKey::decode(&text).unwrap();
        let signature = key.sign(b"payload", &trusted("payload.deb", "1.3.0", 1));
        let signature = String::from_utf8(STANDARD.decode(signature).unwrap()).unwrap();
        let decoded = minisign_verify::Signature::decode(&signature).unwrap();
        public.verify(b"payload", &decoded, false).unwrap();
        assert_eq!(decoded.trusted_comment(), "timestamp:1\tfile:payload.deb\tversion:1.3.0");
        assert!(public.verify(b"another payload", &decoded, false).is_err());
        assert!(!format!("{pair:?}{key:?}").contains(&pair.secret));
    }

    #[test]
    fn a_key_that_is_not_one_is_refused() {
        assert_eq!(SecretKey::read("not base64").unwrap_err(), KeyError::Encoding);
        let pair = generate();
        let text = String::from_utf8(STANDARD.decode(&pair.secret).unwrap()).unwrap();
        let mut bytes = STANDARD.decode(text.lines().nth(1).unwrap()).unwrap();
        bytes[2..4].copy_from_slice(b"Sc");
        let encrypted = STANDARD.encode(format!("untrusted comment: x\n{}\n", STANDARD.encode(&bytes)));
        assert_eq!(SecretKey::read(&encrypted).unwrap_err(), KeyError::Encrypted);
        bytes[2..4].copy_from_slice(&NO_KDF);
        bytes[100] ^= 1;
        let broken = STANDARD.encode(format!("untrusted comment: x\n{}\n", STANDARD.encode(&bytes)));
        assert_eq!(SecretKey::read(&broken).unwrap_err(), KeyError::Checksum);
    }
}
