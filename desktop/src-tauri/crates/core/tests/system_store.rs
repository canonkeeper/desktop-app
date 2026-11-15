// SPDX-License-Identifier: AGPL-3.0-only

//! The store of secrets of the system itself (specs/2026-11-13-desk-secrets.md SE1.1). Off by default: the store of a
//! computer belongs to its user, so the tests run only where they are asked to. `CANONKEEPER_SYSTEM_STORE=works` — the
//! Windows job of desktop/ci/desktop-build.yml — writes, reads and deletes a record of a random id in the Credential
//! Manager, and a value of [`MAX_VALUE`] bytes, the longest the backend asks to keep, and sees a byte more refused;
//! `CANONKEEPER_SYSTEM_STORE=absent` checks a session without a store (Linux without Secret Service, for
//! instance under `dbus-run-session`, whose bus has none, or with no session bus at all): the probe chooses the file
//! within its time.

use std::path::PathBuf;
use std::sync::Arc;
use std::time::{Instant, SystemTime, UNIX_EPOCH};

use canonkeeper_desktop_core::secrets::{
    Failure, Keyring, Secrets, Store, SystemKeyring, FILE, MAX_VALUE, PROBE_TIMEOUT,
};

fn asked(mode: &str) -> bool {
    std::env::var("CANONKEEPER_SYSTEM_STORE").is_ok_and(|asked| asked == mode)
}

fn nanos() -> u128 {
    SystemTime::now().duration_since(UNIX_EPOCH).unwrap().as_nanos()
}

fn data_dir(name: &str) -> PathBuf {
    let root = std::env::temp_dir().join(format!("canonkeeper-system-store-{name}-{}", std::process::id()));
    std::fs::create_dir_all(&root).unwrap();
    root
}

#[test]
fn the_store_of_the_system_keeps_reads_and_deletes_a_record() {
    if !asked("works") {
        return;
    }
    let id = format!("test:{}-{}", std::process::id(), nanos());
    let value = format!("value-of-the-test-{}", nanos());
    let store = SystemKeyring;
    store.set(&id, &value).expect("the store of the system keeps a record");
    assert_eq!(store.get(&id), Ok(Some(value)));
    store.delete(&id).expect("the store of the system deletes the record");
    assert_eq!(store.get(&id), Ok(None));
    // The longest value the backend asks to keep (SE2.5): ASCII takes twice its bytes in UTF-16, the most of any text.
    let longest = "k".repeat(MAX_VALUE);
    store.set(&id, &longest).expect("the store of the system keeps the longest value");
    assert_eq!(store.get(&id), Ok(Some(longest)));
    if cfg!(windows) {
        // The bound is the Credential Manager's own: a byte more and it refuses (2560 bytes of UTF-16).
        assert_eq!(store.set(&id, &"k".repeat(MAX_VALUE + 1)), Err(Failure));
    }
    store.delete(&id).expect("the store of the system deletes the longest value");
    assert_eq!(store.get(&id), Ok(None));
    let root = data_dir("works");
    let (secrets, _) = Secrets::open(root.join(FILE), Arc::new(SystemKeyring), cfg!(windows), PROBE_TIMEOUT);
    assert_eq!(secrets.store(), Store::System);
    let _ = std::fs::remove_dir_all(root);
}

#[test]
fn without_a_store_of_the_system_the_probe_chooses_the_file_in_its_time() {
    if !asked("absent") {
        return;
    }
    let root = data_dir("absent");
    let started = Instant::now();
    let (secrets, _) = Secrets::open(root.join(FILE), Arc::new(SystemKeyring), false, PROBE_TIMEOUT);
    let took = started.elapsed();
    assert_eq!(secrets.store(), Store::File);
    assert!(took < PROBE_TIMEOUT + std::time::Duration::from_secs(1), "{took:?}");
    println!("the probe chose the file in {took:?}");
    let _ = std::fs::remove_dir_all(root);
}
