// SPDX-License-Identifier: AGPL-3.0-only

//! The secrets of the app (specs/2026-11-13-desk-secrets.md SE1): the shell is their one owner. They live in the store
//! of the system — the Credential Manager of Windows, the Secret Service of Linux — under the service `CanonKeeper`,
//! one record per id; where the system has no store that works, in `secrets.json` of the data directory, readable by
//! the user alone. The store is chosen once at the start: Windows always has its own, elsewhere a probe writes, reads
//! and deletes a record of its own within [`PROBE_TIMEOUT`]. The set of records is closed: [`RECORDS`] here and
//! `secretRecords` in backend/internal/domain/desktop/secrets.go name the same ids, [`MAX_VALUE`] and `MaxSecretBytes`
//! the same longest value (scripts/desktop-app.test.mjs holds them equal). A value goes only into the store and the
//! control channel: never into a log, an error or a debug print.

use std::collections::BTreeMap;
use std::fmt;
use std::fs;
use std::io::{self, Write};
use std::path::{Path, PathBuf};
use std::sync::mpsc;
use std::sync::{Arc, Mutex};
use std::thread;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

/// The service the records of the app belong to in the store of the system.
pub const SERVICE: &str = "CanonKeeper";
/// The key of «Свой ИИ».
pub const OWN_AI: &str = "provider:own";
/// The pass of the link of the app with an account of the site (specs/2026-11-13-desk-account.md AC1.3).
pub const ACCOUNT_PASS: &str = "account:pass";
/// Every record the shell keeps: another id is neither kept nor read.
pub const RECORDS: [&str; 2] = [OWN_AI, ACCOUNT_PASS];
/// The longest value, in bytes of UTF-8, every store keeps whatever its characters: the Credential Manager of Windows
/// holds 2560 bytes of UTF-16, and no text takes more than twice its bytes of UTF-8 in UTF-16. A longer value is kept
/// by no store, so the file and the Secret Service, which would take it, refuse it as Windows does.
pub const MAX_VALUE: usize = 1280;
/// The file of the secrets in the data directory, when the system has no store that works.
pub const FILE: &str = "secrets.json";
/// How long the probe of the store of the system may take: a Secret Service that hangs instead of refusing means the
/// file.
pub const PROBE_TIMEOUT: Duration = Duration::from_secs(2);
/// The record of the probe; it is not one of [`RECORDS`] and is deleted by the probe itself.
const PROBE_RECORD: &str = "probe";

/// The value of a secret: it never shows in a debug print.
#[derive(Clone, PartialEq, Eq)]
pub struct Secret(String);

impl Secret {
    pub fn new(value: impl Into<String>) -> Secret {
        Secret(value.into())
    }

    pub fn as_str(&self) -> &str {
        &self.0
    }
}

impl fmt::Debug for Secret {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str("Secret(…)")
    }
}

/// Where the secrets live this run.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Store {
    System,
    File,
}

impl Store {
    /// The name the backend gets in `start.secretsStore`.
    pub fn name(self) -> &'static str {
        match self {
            Store::System => "system",
            Store::File => "file",
        }
    }
}

/// A store that did not do what it was asked; why is not told — its errors may carry what it holds.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Failure;

/// The store of the system as the owner sees it: one record by its id under [`SERVICE`].
pub trait Keyring: Send + Sync + 'static {
    fn set(&self, id: &str, value: &str) -> Result<(), Failure>;
    /// `None` — there is no such record.
    fn get(&self, id: &str) -> Result<Option<String>, Failure>;
    /// A record that is not there is deleted already.
    fn delete(&self, id: &str) -> Result<(), Failure>;
}

/// The store of the system through the crate `keyring`.
pub struct SystemKeyring;

impl SystemKeyring {
    fn entry(id: &str) -> Result<keyring::Entry, Failure> {
        keyring::Entry::new(SERVICE, id).map_err(|_| Failure)
    }
}

impl Keyring for SystemKeyring {
    fn set(&self, id: &str, value: &str) -> Result<(), Failure> {
        SystemKeyring::entry(id)?.set_password(value).map_err(|_| Failure)
    }

    fn get(&self, id: &str) -> Result<Option<String>, Failure> {
        match SystemKeyring::entry(id)?.get_password() {
            Ok(value) => Ok(Some(value)),
            Err(keyring::Error::NoEntry) => Ok(None),
            Err(_) => Err(Failure),
        }
    }

    fn delete(&self, id: &str) -> Result<(), Failure> {
        match SystemKeyring::entry(id)?.delete_credential() {
            Ok(()) | Err(keyring::Error::NoEntry) => Ok(()),
            Err(_) => Err(Failure),
        }
    }
}

/// Why a secret was not kept.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum SecretError {
    /// The id is not one of [`RECORDS`].
    Unknown,
    /// The value is longer than [`MAX_VALUE`].
    TooLong,
    /// The store refused.
    Store,
}

/// What the start of the owner found, for the log of the shell.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Opened {
    /// The store as chosen, nothing to carry over.
    Plain,
    /// The records of a file of an earlier run went into the store of the system, the file is gone.
    Moved,
    /// The store of the system works, but the records of the file did not go into it: this run keeps the file.
    NotMoved,
}

/// The one owner of the secrets of the app.
pub struct Secrets {
    store: Store,
    /// The store of the system works this run (the probe, or Windows), whichever store keeps the records.
    system_works: bool,
    keyring: Arc<dyn Keyring>,
    file: PathBuf,
    /// One writer of the file at a time.
    lock: Mutex<()>,
}

impl fmt::Debug for Secrets {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.debug_struct("Secrets").field("store", &self.store).finish_non_exhaustive()
    }
}

/// Whether an id is one of the records of the app.
pub fn known(id: &str) -> bool {
    RECORDS.contains(&id)
}

impl Secrets {
    /// Chooses the store (SE1.1) and carries the records of a file left by an earlier run into the store of the system
    /// when it works (SE1.3). `windows`: the system is Windows, whose store is always there; `file` is `secrets.json`
    /// of the data directory.
    pub fn open(file: PathBuf, keyring: Arc<dyn Keyring>, windows: bool, timeout: Duration) -> (Secrets, Opened) {
        let system = windows || probe(Arc::clone(&keyring), timeout);
        let mut secrets = Secrets {
            store: if system { Store::System } else { Store::File },
            system_works: system,
            keyring,
            file,
            lock: Mutex::new(()),
        };
        if !system || !secrets.file.exists() {
            return (secrets, Opened::Plain);
        }
        let left = read_file(&secrets.file);
        let moved = left.iter().all(|(id, value)| secrets.keyring.set(id, value.as_str()).is_ok());
        if moved && fs::remove_file(&secrets.file).is_ok() {
            return (secrets, Opened::Moved);
        }
        secrets.store = Store::File;
        (secrets, Opened::NotMoved)
    }

    pub fn store(&self) -> Store {
        self.store
    }

    /// Every record of the set that is kept: what `start.secrets` carries. A record the store cannot read is left out.
    pub fn read_all(&self) -> BTreeMap<String, Secret> {
        match self.store {
            Store::System => RECORDS
                .iter()
                .filter_map(|id| match self.keyring.get(id) {
                    Ok(Some(value)) => Some((id.to_string(), Secret(value))),
                    _ => None,
                })
                .collect(),
            Store::File => {
                let _guard = self.lock.lock().unwrap_or_else(|poisoned| poisoned.into_inner());
                read_file(&self.file)
            }
        }
    }

    /// Keeps a record of the set.
    pub fn save(&self, id: &str, value: &Secret) -> Result<(), SecretError> {
        self.change(id, Some(value))
    }

    /// Removes a record of the set; one that is not there is removed already.
    pub fn delete(&self, id: &str) -> Result<(), SecretError> {
        self.change(id, None)
    }

    /// Removes every record of the set wherever it may lie — «Стереть все данные» (specs/2026-11-13-desk-erase.md ER1.3):
    /// in the store of the system, whichever store keeps them this run, and the file. A store of the system that works
    /// must remove each one within `timeout` — a locked one asks the author first; a store that does not work keeps
    /// nothing of the app, and is not waited for longer than its probe.
    pub fn erase(&self, timeout: Duration) -> Result<(), SecretError> {
        let _guard = self.lock.lock().unwrap_or_else(|poisoned| poisoned.into_inner());
        let keyring = Arc::clone(&self.keyring);
        let (done, answer) = mpsc::channel();
        thread::spawn(move || {
            let _ = done.send(RECORDS.iter().all(|id| keyring.delete(id).is_ok()));
        });
        let wait = if self.system_works { timeout } else { timeout.min(PROBE_TIMEOUT) };
        let system = answer.recv_timeout(wait).unwrap_or(false) || !self.system_works;
        let file = match fs::remove_file(&self.file) {
            Err(error) => error.kind() == io::ErrorKind::NotFound,
            Ok(()) => true,
        };
        if system && file {
            Ok(())
        } else {
            Err(SecretError::Store)
        }
    }

    fn change(&self, id: &str, value: Option<&Secret>) -> Result<(), SecretError> {
        if !known(id) {
            return Err(SecretError::Unknown);
        }
        if value.is_some_and(|value| value.as_str().len() > MAX_VALUE) {
            return Err(SecretError::TooLong);
        }
        match self.store {
            Store::System => match value {
                Some(value) => self.keyring.set(id, value.as_str()),
                None => self.keyring.delete(id),
            }
            .map_err(|Failure| SecretError::Store),
            Store::File => {
                let _guard = self.lock.lock().unwrap_or_else(|poisoned| poisoned.into_inner());
                let mut records = read_file(&self.file);
                match value {
                    Some(value) => records.insert(id.to_string(), value.clone()),
                    None => records.remove(id),
                };
                write_file(&self.file, &records).map_err(|_| SecretError::Store)
            }
        }
    }
}

/// Writes, reads and deletes the record of the probe within `timeout`; a store that hangs is left to itself.
fn probe(keyring: Arc<dyn Keyring>, timeout: Duration) -> bool {
    let (done, answer) = mpsc::channel();
    thread::spawn(move || {
        let value = format!("{}-{}", std::process::id(), nanos());
        let works = keyring.set(PROBE_RECORD, &value).is_ok()
            && keyring.get(PROBE_RECORD) == Ok(Some(value))
            && keyring.delete(PROBE_RECORD).is_ok();
        let _ = done.send(works);
    });
    answer.recv_timeout(timeout).unwrap_or(false)
}

fn nanos() -> u128 {
    SystemTime::now().duration_since(UNIX_EPOCH).map(|since| since.as_nanos()).unwrap_or(0)
}

/// The records of the set in the file; a file that is not there or not an object of strings holds none.
fn read_file(file: &Path) -> BTreeMap<String, Secret> {
    let Ok(raw) = fs::read(file) else { return BTreeMap::new() };
    let Ok(values) = serde_json::from_slice::<BTreeMap<String, serde_json::Value>>(&raw) else {
        return BTreeMap::new();
    };
    values
        .into_iter()
        .filter(|(id, _)| known(id))
        .filter_map(|(id, value)| value.as_str().map(|value| (id, Secret(value.to_string()))))
        .collect()
}

/// Replaces the file whole: written beside it for the user alone (0600), then renamed over it — a stop in the middle
/// leaves the old file or the new one. Without records the file goes.
fn write_file(file: &Path, records: &BTreeMap<String, Secret>) -> io::Result<()> {
    if records.is_empty() {
        return match fs::remove_file(file) {
            Err(error) if error.kind() != io::ErrorKind::NotFound => Err(error),
            _ => Ok(()),
        };
    }
    let values: serde_json::Map<String, serde_json::Value> =
        records.iter().map(|(id, value)| (id.clone(), serde_json::Value::from(value.as_str()))).collect();
    let directory = file.parent().ok_or_else(|| io::Error::from(io::ErrorKind::NotFound))?;
    let temporary = directory.join(format!(".{FILE}.{}.{}", std::process::id(), nanos()));
    let mut options = fs::OpenOptions::new();
    options.write(true).create_new(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options.mode(0o600);
    }
    let written = options.open(&temporary).and_then(|mut opened| {
        opened.write_all(serde_json::Value::Object(values).to_string().as_bytes())?;
        opened.sync_all()
    });
    let renamed = written.and_then(|()| fs::rename(&temporary, file));
    if renamed.is_err() {
        let _ = fs::remove_file(&temporary);
    }
    renamed
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::collections::HashMap;

    /// A store of the system that works, in memory.
    #[derive(Default)]
    struct Memory(Mutex<HashMap<String, String>>);

    impl Keyring for Memory {
        fn set(&self, id: &str, value: &str) -> Result<(), Failure> {
            self.0.lock().unwrap().insert(id.to_string(), value.to_string());
            Ok(())
        }
        fn get(&self, id: &str) -> Result<Option<String>, Failure> {
            Ok(self.0.lock().unwrap().get(id).cloned())
        }
        fn delete(&self, id: &str) -> Result<(), Failure> {
            self.0.lock().unwrap().remove(id);
            Ok(())
        }
    }

    /// A system without a store: every call refuses.
    struct Refusing;

    impl Keyring for Refusing {
        fn set(&self, _: &str, _: &str) -> Result<(), Failure> {
            Err(Failure)
        }
        fn get(&self, _: &str) -> Result<Option<String>, Failure> {
            Err(Failure)
        }
        fn delete(&self, _: &str) -> Result<(), Failure> {
            Err(Failure)
        }
    }

    /// A Secret Service that never answers.
    struct Hanging;

    impl Keyring for Hanging {
        fn set(&self, _: &str, _: &str) -> Result<(), Failure> {
            thread::sleep(Duration::from_secs(30));
            Err(Failure)
        }
        fn get(&self, _: &str) -> Result<Option<String>, Failure> {
            Err(Failure)
        }
        fn delete(&self, _: &str) -> Result<(), Failure> {
            Err(Failure)
        }
    }

    /// A store that takes the probe and refuses every record of the set.
    struct OnlyTheProbe(Memory);

    impl Keyring for OnlyTheProbe {
        fn set(&self, id: &str, value: &str) -> Result<(), Failure> {
            if id == PROBE_RECORD {
                self.0.set(id, value)
            } else {
                Err(Failure)
            }
        }
        fn get(&self, id: &str) -> Result<Option<String>, Failure> {
            self.0.get(id)
        }
        fn delete(&self, id: &str) -> Result<(), Failure> {
            self.0.delete(id)
        }
    }

    fn directory(name: &str) -> PathBuf {
        let root = std::env::temp_dir().join(format!("canonkeeper-secrets-{name}-{}", std::process::id()));
        let _ = fs::remove_dir_all(&root);
        fs::create_dir_all(&root).unwrap();
        root
    }

    const KEY: &str = "key-test-of-the-north-pass-0001";

    #[test]
    fn without_a_store_of_the_system_the_file_keeps_reads_and_deletes_a_record() {
        let root = directory("file");
        let file = root.join(FILE);
        let (secrets, opened) = Secrets::open(file.clone(), Arc::new(Refusing), false, PROBE_TIMEOUT);
        assert_eq!((secrets.store(), opened), (Store::File, Opened::Plain));
        assert!(secrets.read_all().is_empty());
        secrets.save(OWN_AI, &Secret::new(KEY)).unwrap();
        assert_eq!(secrets.read_all(), BTreeMap::from([(OWN_AI.to_string(), Secret::new(KEY))]));
        let written: serde_json::Value = serde_json::from_slice(&fs::read(&file).unwrap()).unwrap();
        assert_eq!(written, serde_json::json!({ OWN_AI: KEY }));
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            assert_eq!(fs::metadata(&file).unwrap().permissions().mode() & 0o777, 0o600);
        }
        secrets.save(OWN_AI, &Secret::new("key-test-second")).unwrap();
        assert_eq!(secrets.read_all()[OWN_AI], Secret::new("key-test-second"));
        secrets.delete(OWN_AI).unwrap();
        assert!(secrets.read_all().is_empty());
        assert!(!file.exists(), "a file without records stays");
        secrets.delete(OWN_AI).unwrap();
        assert_eq!(fs::read_dir(&root).unwrap().count(), 0, "a temporary file is left");
        fs::remove_dir_all(root).unwrap();
    }

    #[test]
    fn the_file_is_replaced_whole_and_never_written_in_place() {
        let root = directory("whole");
        let file = root.join(FILE);
        let (secrets, _) = Secrets::open(file.clone(), Arc::new(Refusing), false, PROBE_TIMEOUT);
        secrets.save(OWN_AI, &Secret::new(KEY)).unwrap();
        // A link to the old file: a write in place would change what it holds, a rename over the name does not.
        let before = root.join("before");
        fs::hard_link(&file, &before).unwrap();
        secrets.save(OWN_AI, &Secret::new("key-test-replaced")).unwrap();
        assert!(String::from_utf8(fs::read(&before).unwrap()).unwrap().contains(KEY));
        assert!(String::from_utf8(fs::read(&file).unwrap()).unwrap().contains("key-test-replaced"));
        let names: Vec<String> = fs::read_dir(&root)
            .unwrap()
            .flatten()
            .map(|entry| entry.file_name().to_string_lossy().into_owned())
            .collect();
        assert_eq!(names.len(), 2, "{names:?}");
        fs::remove_dir_all(root).unwrap();
    }

    #[test]
    fn the_probe_chooses_the_store_and_a_hanging_one_means_the_file() {
        let root = directory("probe");
        let (working, _) = Secrets::open(root.join(FILE), Arc::new(Memory::default()), false, PROBE_TIMEOUT);
        assert_eq!(working.store(), Store::System);
        let (refusing, _) = Secrets::open(root.join(FILE), Arc::new(Refusing), false, PROBE_TIMEOUT);
        assert_eq!(refusing.store(), Store::File);
        let asked = std::time::Instant::now();
        let (hanging, _) = Secrets::open(root.join(FILE), Arc::new(Hanging), false, Duration::from_millis(200));
        assert_eq!(hanging.store(), Store::File);
        assert!(asked.elapsed() < Duration::from_secs(5), "{:?}", asked.elapsed());
        // Windows always has its store: no probe.
        let (windows, _) = Secrets::open(root.join(FILE), Arc::new(Refusing), true, PROBE_TIMEOUT);
        assert_eq!(windows.store(), Store::System);
        fs::remove_dir_all(root).unwrap();
    }

    #[test]
    fn the_probe_leaves_no_record_of_its_own() {
        let root = directory("clean");
        let memory = Arc::new(Memory::default());
        let _ = Secrets::open(root.join(FILE), Arc::clone(&memory) as Arc<dyn Keyring>, false, PROBE_TIMEOUT);
        assert!(memory.0.lock().unwrap().is_empty());
        fs::remove_dir_all(root).unwrap();
    }

    #[test]
    fn a_file_of_an_earlier_run_goes_into_the_store_of_the_system_that_works() {
        let root = directory("move");
        let file = root.join(FILE);
        fs::write(&file, serde_json::json!({ OWN_AI: KEY, "provider:other": "x" }).to_string()).unwrap();
        let memory = Arc::new(Memory::default());
        let (secrets, opened) =
            Secrets::open(file.clone(), Arc::clone(&memory) as Arc<dyn Keyring>, false, PROBE_TIMEOUT);
        assert_eq!((secrets.store(), opened), (Store::System, Opened::Moved));
        assert!(!file.exists());
        assert_eq!(secrets.read_all(), BTreeMap::from([(OWN_AI.to_string(), Secret::new(KEY))]));
        assert_eq!(memory.0.lock().unwrap().len(), 1, "a record outside the set went into the store");

        // The store takes the probe but not the records: this run keeps the file.
        fs::write(&file, serde_json::json!({ OWN_AI: KEY }).to_string()).unwrap();
        let (kept, opened) =
            Secrets::open(file.clone(), Arc::new(OnlyTheProbe(Memory::default())), false, PROBE_TIMEOUT);
        assert_eq!((kept.store(), opened), (Store::File, Opened::NotMoved));
        assert!(file.exists());
        assert_eq!(kept.read_all()[OWN_AI], Secret::new(KEY));
        fs::remove_dir_all(root).unwrap();
    }

    #[test]
    fn an_id_outside_the_set_is_neither_kept_nor_read() {
        let root = directory("unknown");
        let memory = Arc::new(Memory::default());
        let (system, _) = Secrets::open(root.join(FILE), Arc::clone(&memory) as Arc<dyn Keyring>, false, PROBE_TIMEOUT);
        assert_eq!(system.save("provider:other", &Secret::new(KEY)), Err(SecretError::Unknown));
        assert_eq!(system.delete("pass"), Err(SecretError::Unknown));
        memory.0.lock().unwrap().insert("provider:other".into(), KEY.into());
        assert!(system.read_all().is_empty());
        let (file, _) = Secrets::open(root.join(FILE), Arc::new(Refusing), false, PROBE_TIMEOUT);
        assert_eq!(file.save("", &Secret::new(KEY)), Err(SecretError::Unknown));
        assert!(!root.join(FILE).exists());
        fs::remove_dir_all(root).unwrap();
    }

    #[test]
    fn a_value_longer_than_every_store_keeps_is_kept_by_none() {
        // The Credential Manager holds 2560 bytes of UTF-16: the longest value of ASCII it keeps is 1280 bytes, and no
        // value of 1280 bytes takes more in UTF-16. The file and the Secret Service would take more; they do not.
        assert_eq!(MAX_VALUE, 1280);
        for value in ["k".repeat(MAX_VALUE), "ж".repeat(MAX_VALUE / 2), "𝔨".repeat(MAX_VALUE / 4)] {
            assert!(value.encode_utf16().count() * 2 <= 2560, "{} bytes", value.len());
        }
        let root = directory("long");
        let file = root.join(FILE);
        let memory = Arc::new(Memory::default());
        let (system, _) = Secrets::open(file.clone(), Arc::clone(&memory) as Arc<dyn Keyring>, false, PROBE_TIMEOUT);
        let (in_file, _) = Secrets::open(file.clone(), Arc::new(Refusing), false, PROBE_TIMEOUT);
        for secrets in [&system, &in_file] {
            let longest = Secret::new("k".repeat(MAX_VALUE));
            secrets.save(OWN_AI, &longest).unwrap();
            assert_eq!(secrets.read_all()[OWN_AI], longest);
            for longer in ["k".repeat(MAX_VALUE + 1), "ж".repeat(MAX_VALUE / 2 + 1)] {
                assert_eq!(secrets.save(OWN_AI, &Secret::new(longer)), Err(SecretError::TooLong));
            }
            assert_eq!(secrets.read_all()[OWN_AI], longest, "a refused value changed the record");
        }
        fs::remove_dir_all(root).unwrap();
    }

    #[test]
    fn erase_removes_the_records_from_the_store_of_the_system_and_the_file() {
        // specs/2026-11-13-desk-erase.md ER1.3: whichever store keeps them this run.
        let root = directory("erase");
        let file = root.join(FILE);
        let memory = Arc::new(Memory::default());
        let (system, _) = Secrets::open(file.clone(), Arc::clone(&memory) as Arc<dyn Keyring>, false, PROBE_TIMEOUT);
        system.save(OWN_AI, &Secret::new(KEY)).unwrap();
        fs::write(&file, serde_json::json!({ OWN_AI: KEY }).to_string()).unwrap();
        assert_eq!(system.erase(PROBE_TIMEOUT), Ok(()));
        assert!(memory.0.lock().unwrap().is_empty() && !file.exists());

        // The store of the system works but refuses this run's file: both still go.
        fs::write(&file, serde_json::json!({ OWN_AI: KEY }).to_string()).unwrap();
        let only = Arc::new(OnlyTheProbe(Memory::default()));
        only.0.set(OWN_AI, KEY).unwrap();
        let (kept, opened) = Secrets::open(file.clone(), Arc::clone(&only) as Arc<dyn Keyring>, false, PROBE_TIMEOUT);
        assert_eq!((kept.store(), opened), (Store::File, Opened::NotMoved));
        assert_eq!(kept.erase(PROBE_TIMEOUT), Ok(()));
        assert!(only.0 .0.lock().unwrap().is_empty() && !file.exists());

        // Without a store of the system the file alone holds them; a store that hangs is not waited for.
        fs::write(&file, serde_json::json!({ OWN_AI: KEY }).to_string()).unwrap();
        let (none, _) = Secrets::open(file.clone(), Arc::new(Refusing), false, PROBE_TIMEOUT);
        assert_eq!(none.erase(PROBE_TIMEOUT), Ok(()));
        assert!(!file.exists());
        let (hanging, _) = Secrets::open(file.clone(), Arc::new(Hanging), false, Duration::from_millis(100));
        let asked = std::time::Instant::now();
        assert_eq!(hanging.erase(Duration::from_millis(200)), Ok(()));
        assert!(asked.elapsed() < Duration::from_secs(5), "{:?}", asked.elapsed());

        // A store of the system that works and refuses to remove a record: the erasure did not end.
        let (windows, _) = Secrets::open(file.clone(), Arc::new(Refusing), true, PROBE_TIMEOUT);
        assert_eq!(windows.erase(PROBE_TIMEOUT), Err(SecretError::Store));
        fs::remove_dir_all(root).unwrap();
    }

    #[test]
    fn a_refusing_store_says_so_without_the_value() {
        let root = directory("refused");
        let (secrets, _) = Secrets::open(root.join(FILE), Arc::new(Refusing), true, PROBE_TIMEOUT);
        let refused = secrets.save(OWN_AI, &Secret::new(KEY));
        assert_eq!(refused, Err(SecretError::Store));
        assert!(!format!("{refused:?} {secrets:?} {:?}", Secret::new(KEY)).contains(KEY));
        fs::remove_dir_all(root).unwrap();
    }
}
