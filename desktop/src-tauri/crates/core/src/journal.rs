// SPDX-License-Identifier: AGPL-3.0-only

//! The log of the shell (specs/2026-11-12-desk-shell.md SH3.3): `logs/shell-<date>.log` in the data directory, next to
//! the logs of the backend, kept a week. It holds events by fixed names and numbers only — never an address, a code,
//! a command's content, a path or a text.

use std::fs::{self, File, OpenOptions};
use std::io::Write;
use std::path::Path;
use std::sync::{Arc, Mutex};
use std::time::{Duration, SystemTime, UNIX_EPOCH};

/// How long a log stays, as the logs of the backend (logs.go, logDays).
const KEEP: Duration = Duration::from_secs(7 * 24 * 60 * 60);

/// The log; clones write to the same file. Without a file (the directory cannot be made) it writes nothing.
#[derive(Clone, Default)]
pub struct Journal {
    file: Arc<Mutex<Option<File>>>,
}

impl Journal {
    /// Opens today's log in `logs` and removes the logs of the shell older than a week.
    pub fn open(logs: &Path, now: SystemTime) -> Journal {
        let file = prepare(logs).ok().and_then(|()| {
            remove_old(logs, now);
            let mut options = OpenOptions::new();
            options.create(true).append(true);
            #[cfg(unix)]
            {
                use std::os::unix::fs::OpenOptionsExt;
                options.mode(0o600);
            }
            options.open(logs.join(format!("shell-{}.log", date(now)))).ok()
        });
        Journal { file: Arc::new(Mutex::new(file)) }
    }

    /// Writes one event: a fixed name and, at most, numbers and names the shell itself chose.
    pub fn note(&self, event: &str) {
        let Ok(mut file) = self.file.lock() else { return };
        if let Some(file) = file.as_mut() {
            let _ = writeln!(file, "{} {event}", timestamp(SystemTime::now()));
        }
    }
}

/// A word the backend chose — the reason of a fatal, the type of a message — as the log may hold it: a short name of
/// lower-case letters, digits, `_` and `-`, otherwise `other`.
pub fn name(word: &str) -> &str {
    let plain = !word.is_empty()
        && word.len() <= 32
        && word.bytes().all(|byte| byte.is_ascii_lowercase() || byte.is_ascii_digit() || byte == b'_' || byte == b'-');
    if plain {
        word
    } else {
        "other"
    }
}

fn prepare(logs: &Path) -> std::io::Result<()> {
    let mut builder = fs::DirBuilder::new();
    builder.recursive(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::DirBuilderExt;
        builder.mode(0o700);
    }
    builder.create(logs)
}

fn remove_old(logs: &Path, now: SystemTime) {
    let Ok(entries) = fs::read_dir(logs) else { return };
    for entry in entries.flatten() {
        let name = entry.file_name();
        let name = name.to_string_lossy();
        if !(name.starts_with("shell-") && name.ends_with(".log")) {
            continue;
        }
        let old = entry
            .metadata()
            .and_then(|metadata| metadata.modified())
            .is_ok_and(|modified| now.duration_since(modified).is_ok_and(|age| age > KEEP));
        if old {
            let _ = fs::remove_file(entry.path());
        }
    }
}

fn civil(time: SystemTime) -> (i64, u32, u32, u64) {
    let seconds = time.duration_since(UNIX_EPOCH).map(|since| since.as_secs()).unwrap_or(0);
    // Days to the civil date (H. Hinnant's algorithm), UTC.
    let days = (seconds / 86_400) as i64 + 719_468;
    let era = days.div_euclid(146_097);
    let day_of_era = days.rem_euclid(146_097);
    let year_of_era = (day_of_era - day_of_era / 1_460 + day_of_era / 36_524 - day_of_era / 146_096) / 365;
    let day_of_year = day_of_era - (365 * year_of_era + year_of_era / 4 - year_of_era / 100);
    let month_index = (5 * day_of_year + 2) / 153;
    let day = (day_of_year - (153 * month_index + 2) / 5 + 1) as u32;
    let month = if month_index < 10 { month_index + 3 } else { month_index - 9 } as u32;
    let year = year_of_era + era * 400 + i64::from(month <= 2);
    (year, month, day, seconds % 86_400)
}

/// `YYYY-MM-DD` in UTC, as the names of the backend's logs.
pub fn date(time: SystemTime) -> String {
    let (year, month, day, _) = civil(time);
    format!("{year:04}-{month:02}-{day:02}")
}

fn timestamp(time: SystemTime) -> String {
    let (year, month, day, seconds) = civil(time);
    format!("{year:04}-{month:02}-{day:02}T{:02}:{:02}:{:02}Z", seconds / 3_600, seconds / 60 % 60, seconds % 60)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn at(seconds: u64) -> SystemTime {
        UNIX_EPOCH + Duration::from_secs(seconds)
    }

    #[test]
    fn dates_are_utc_days() {
        assert_eq!(date(at(0)), "1970-01-01");
        assert_eq!(date(at(951_782_400)), "2000-02-29");
        assert_eq!(date(at(1_794_441_600)), "2026-11-12");
        assert_eq!(timestamp(at(1_794_441_600 + 3_723)), "2026-11-12T01:02:03Z");
    }

    #[test]
    fn only_plain_names_reach_the_log() {
        assert_eq!(name("newer_schema"), "newer_schema");
        assert_eq!(name("open-code"), "open-code");
        assert_eq!(name("database: /home/x/Роман.docx"), "other");
        assert_eq!(name(""), "other");
        assert_eq!(name(&"a".repeat(33)), "other");
    }

    #[test]
    fn the_log_is_written_in_its_directory_and_old_logs_go() {
        let root = std::env::temp_dir().join(format!("canonkeeper-journal-{}", std::process::id()));
        let logs = root.join("data").join("logs");
        fs::create_dir_all(&logs).unwrap();
        let old = logs.join("shell-2000-01-01.log");
        fs::write(&old, "old\n").unwrap();
        File::options().write(true).open(&old).unwrap().set_modified(at(946_684_800)).unwrap();
        let backend_log = logs.join("backend-2000-01-01.log");
        fs::write(&backend_log, "theirs\n").unwrap();
        File::options().write(true).open(&backend_log).unwrap().set_modified(at(946_684_800)).unwrap();

        let journal = Journal::open(&logs, SystemTime::now());
        journal.clone().note("backend_spawned");
        journal.note("backend_ready port=24117");
        assert!(!old.exists(), "a log of the shell older than a week stays");
        assert!(backend_log.exists(), "the logs of the backend are not the shell's to remove");
        let written = fs::read_to_string(logs.join(format!("shell-{}.log", date(SystemTime::now())))).unwrap();
        let lines: Vec<_> = written.lines().collect();
        assert_eq!(lines.len(), 2);
        assert!(lines[1].ends_with(" backend_ready port=24117"), "{written}");
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            let file = fs::metadata(logs.join(format!("shell-{}.log", date(SystemTime::now())))).unwrap();
            assert_eq!(file.permissions().mode() & 0o777, 0o600);
        }
        fs::remove_dir_all(root).unwrap();
    }
}
