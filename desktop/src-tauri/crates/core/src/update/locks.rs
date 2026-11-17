// SPDX-License-Identifier: AGPL-3.0-only

//! Whether another installation of programs holds a lock of the installers of Debian now (specs/2026-11-17-desk-update-linux.md
//! UL2.1): before the backend stops and before the password, a package of `.deb` is not begun while one of them is held. The
//! check needs no right: `stat` of a lock file gives its device and inode, and the list of the locks of the system
//! (`/proc/locks`, readable by all) names the device and the inode of every lock held — of any kind (`POSIX`, `OFDLCK`,
//! `FLOCK`) and mode; a line of a process that only waits for a lock (`->`) holds nothing. Who holds it is not read. A lock
//! file that is not there, or a list that cannot be read, says nothing: the installation goes as it did, and the output of
//! `apt-get` still tells a lock it never took.

use std::path::{Path, PathBuf};

/// Whether a line of `proc_locks` holds one of `locks`.
pub fn locked(locks: &[PathBuf], proc_locks: &Path) -> bool {
    imp::locked(locks, proc_locks)
}

/// The device and the inode a line of `/proc/locks` holds: `<n>: <kind> <mode> <access> <pid> <major>:<minor>:<inode> …`,
/// the numbers of the device in hex of any length, the inode in decimal; none for a line that waits (`<n>: -> …`).
#[cfg_attr(not(unix), allow(dead_code))]
fn held(line: &str) -> Option<(u64, u64, u64)> {
    let mut words = line.split_whitespace();
    words.next()?;
    let mut words = words.peekable();
    if words.peek() == Some(&"->") {
        return None;
    }
    words.find_map(|word| {
        let mut parts = word.split(':');
        let major = u64::from_str_radix(parts.next()?, 16).ok()?;
        let minor = u64::from_str_radix(parts.next()?, 16).ok()?;
        let inode = parts.next()?.parse().ok()?;
        parts.next().is_none().then_some((major, minor, inode))
    })
}

#[cfg(unix)]
mod imp {
    use std::fs::{self, File};
    use std::io::{BufRead, BufReader};
    use std::os::unix::fs::MetadataExt;
    use std::path::{Path, PathBuf};

    pub fn locked(locks: &[PathBuf], proc_locks: &Path) -> bool {
        let files: Vec<(u64, u64, u64)> = locks
            .iter()
            .filter_map(|lock| fs::metadata(lock).ok())
            .map(|lock| (major(lock.dev()), minor(lock.dev()), lock.ino()))
            .collect();
        if files.is_empty() {
            return false;
        }
        let Ok(list) = File::open(proc_locks) else { return false };
        BufReader::new(list)
            .lines()
            .map_while(Result::ok)
            .any(|line| super::held(&line).is_some_and(|lock| files.contains(&lock)))
    }

    /// The major number of a device as glibc reads it out of `st_dev`.
    fn major(device: u64) -> u64 {
        ((device >> 32) & 0xffff_f000) | ((device >> 8) & 0x0000_0fff)
    }

    /// The minor number of a device as glibc reads it out of `st_dev`.
    fn minor(device: u64) -> u64 {
        ((device >> 12) & 0xffff_ff00) | (device & 0x0000_00ff)
    }

    #[cfg(test)]
    #[test]
    fn the_numbers_of_a_device_are_read_as_glibc_reads_them() {
        assert_eq!((major(0x10302), minor(0x10302)), (0x103, 0x02));
        assert_eq!((major(0x0803), minor(0x0803)), (8, 3));
        // A minor over 255 and a major over 4095, as the 64 bits of glibc keep them.
        let device = (0x0000_1000u64 << 32) | (0x0012_3400 << 12) | (0x0abc << 8) | 0x56;
        assert_eq!((major(device), minor(device)), (0x1abc, 0x0012_3456));
    }
}

#[cfg(not(unix))]
mod imp {
    use std::path::{Path, PathBuf};

    /// Only a package of Linux has locks of Debian; the code builds everywhere.
    pub fn locked(_locks: &[PathBuf], _proc_locks: &Path) -> bool {
        false
    }
}

#[cfg(all(test, target_os = "linux"))]
mod tests {
    use super::*;
    use std::fs::{self, File, OpenOptions};
    use std::os::unix::io::AsRawFd;

    struct Stand {
        root: PathBuf,
    }

    impl Stand {
        fn new(name: &str) -> Stand {
            let root = std::env::temp_dir().join(format!("canonkeeper-locks-{name}-{}", std::process::id()));
            let _ = fs::remove_dir_all(&root);
            fs::create_dir_all(&root).unwrap();
            Stand { root }
        }

        fn lock(&self, name: &str) -> PathBuf {
            let path = self.root.join(name);
            fs::write(&path, "").unwrap();
            path
        }
    }

    impl Drop for Stand {
        fn drop(&mut self) {
            let _ = fs::remove_dir_all(&self.root);
        }
    }

    /// A lock of `command` (`F_SETLK` — POSIX, `F_OFD_SETLK` — OFD) on the whole of `file`: `F_WRLCK` takes it, `F_UNLCK` lets
    /// it go.
    fn fcntl_lock(file: &File, command: libc::c_int, kind: libc::c_int) {
        // SAFETY: a plain `flock` record for the whole file and a descriptor that lives as long as `file`.
        let mut lock: libc::flock = unsafe { std::mem::zeroed() };
        lock.l_type = kind as libc::c_short;
        lock.l_whence = libc::SEEK_SET as libc::c_short;
        let done = unsafe { libc::fcntl(file.as_raw_fd(), command, &lock) };
        assert_eq!(done, 0, "the lock of the test was not taken or let go");
    }

    // UL2.1: a lock of each kind the system shows holds the file — POSIX, OFD and flock — on the real list of the locks of the
    // system; once it is let go nothing holds it. The lock is let go by its own call, not by closing the file: an OFD or a
    // flock lock belongs to the open description of the file, and a child another test of this process forks holds a copy of
    // the descriptor until its `exec` closes it (the file is opened close-on-exec, as std opens every file) — closing ours
    // would leave the lock to that copy for a moment.
    #[test]
    fn a_lock_of_any_kind_on_a_lock_file_is_seen_and_its_end_too() {
        let stand = Stand::new("kinds");
        let proc_locks = Path::new("/proc/locks");
        let free = stand.lock("free");
        for (name, command) in [("posix", Some(libc::F_SETLK)), ("ofd", Some(libc::F_OFD_SETLK)), ("flock", None)] {
            let path = stand.lock(name);
            let file = OpenOptions::new().read(true).write(true).open(&path).unwrap();
            // SAFETY: a descriptor that lives as long as `file`.
            let flags = unsafe { libc::fcntl(file.as_raw_fd(), libc::F_GETFD) };
            assert!(flags >= 0 && flags & libc::FD_CLOEXEC != 0, "{name}: the file is not close-on-exec");
            assert!(!locked(std::slice::from_ref(&path), proc_locks), "{name}: held before the lock");
            match command {
                Some(command) => fcntl_lock(&file, command, libc::F_WRLCK),
                None => file.lock().unwrap(),
            }
            assert!(locked(&[free.clone(), path.clone()], proc_locks), "{name}: the lock is not seen");
            assert!(!locked(std::slice::from_ref(&free), proc_locks), "{name}: another file is held");
            match command {
                Some(command) => fcntl_lock(&file, command, libc::F_UNLCK),
                None => file.unlock().unwrap(),
            }
            assert!(!locked(&[path], proc_locks), "{name}: held after the lock is let go");
        }
    }

    // UL2.1: the lines of the list as they come — spaces of any length, a major of more than two hex digits, a line that only
    // waits; a list that cannot be read and a lock file that is not there say nothing.
    #[test]
    fn the_lines_of_the_list_of_the_locks_are_read_as_the_system_writes_them() {
        assert_eq!(held("159: POSIX  ADVISORY  WRITE 438658 103:02:28314803 0 EOF"), Some((0x103, 2, 28314803)));
        assert_eq!(held("159: OFDLCK ADVISORY  WRITE -1 08:03:1234 0 EOF"), Some((8, 3, 1234)));
        assert_eq!(held("2: FLOCK  ADVISORY  WRITE 1 fd:01:77 0 EOF"), Some((0xfd, 1, 77)));
        assert_eq!(held("311: -> POSIX  ADVISORY  WRITE 439543 103:02:28314803 0 EOF"), None);
        assert_eq!(held(""), None);
        assert_eq!(held("garbage"), None);

        let stand = Stand::new("lines");
        let lock = stand.lock("lock-frontend");
        let list = stand.root.join("locks");
        // A list of the test that holds the lock file only by a line that waits.
        let metadata = fs::metadata(&lock).unwrap();
        let device = {
            use std::os::unix::fs::MetadataExt;
            format!("{:x}:{:02x}:{}", libc::major(metadata.dev()), libc::minor(metadata.dev()), metadata.ino())
        };
        fs::write(&list, format!("1: -> POSIX  ADVISORY  WRITE 9 {device} 0 EOF\n")).unwrap();
        assert!(!locked(std::slice::from_ref(&lock), &list));
        fs::write(
            &list,
            format!("1: POSIX  ADVISORY  WRITE 9 1:2:3 0 EOF\n2:  FLOCK   ADVISORY  READ  9 {device} 0 EOF\n"),
        )
        .unwrap();
        assert!(locked(std::slice::from_ref(&lock), &list));
        assert!(!locked(&[stand.root.join("not-there")], &list));
        assert!(!locked(&[lock], &stand.root.join("no-list")));
    }
}
