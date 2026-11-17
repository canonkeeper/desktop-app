// SPDX-License-Identifier: AGPL-3.0-only

//! A package of Linux, `.deb` or `.rpm` (specs/2026-11-13-desk-updates.md UB2.6): the shell installs it itself, not by the
//! fallback of the updater plugin, which asks for the password in an English window and pipes it through the process of
//! the app. The checked bytes are written into a new directory of this user alone, the kind of the file is checked, and
//! the signature again right before the installation; `pkexec` runs the package manager — `apt-get install`, without it
//! `dpkg -i`; `dnf install`, without it `rpm -U` — every program by its absolute path. Only the system asks for the
//! password. A window of the password closed, or a system that cannot ask for it — nothing changed: the package is kept in
//! «Загрузки» (or the home directory) under the name of the signed version, beside the files there; any other failure
//! after the password leaves the system half done, and the app starts again whole. The installation runs on a thread of
//! its own (specs/2026-11-17-desk-update-linux.md UL1): it reads the output of the installer to its end, passes it on to
//! the stderr of the shell and says the steps the page of the start shows — the password, the work, the wait for a lock of
//! another installation.

use std::fs::{self, File};
use std::io::{self, Read, Write};
use std::path::{Path, PathBuf};
use std::process::{Command, Stdio};

use super::fetch::Downloaded;
use super::kept::{keep, new_file};
use super::manifest::{self, Keys};
use super::output;
use super::state::Refusal;
use super::Kind;

/// The programs of an installation of a package and where a package is kept for the author.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Programs {
    pub pkexec: PathBuf,
    pub apt_get: PathBuf,
    pub dpkg: PathBuf,
    pub dnf: PathBuf,
    pub rpm: PathBuf,
    /// The locks of the installers of Debian (UL1.3, UL2.1): one another installation holds stops a `.deb` before it begins,
    /// and a line of the output of the installer that names one says it waits for another installation.
    pub locks: Vec<PathBuf>,
    /// The list of the locks the system holds (`/proc/locks`).
    pub proc_locks: PathBuf,
    /// The folder of downloads of the user, and the home directory when there is none.
    pub downloads: Option<PathBuf>,
    pub home: Option<PathBuf>,
    /// Where the package lies while it is installed: the temporary directory of the system.
    pub temp: PathBuf,
}

impl Programs {
    /// The programs of the system, by their absolute paths.
    pub fn system(downloads: Option<PathBuf>, home: Option<PathBuf>) -> Programs {
        Programs {
            pkexec: "/usr/bin/pkexec".into(),
            apt_get: "/usr/bin/apt-get".into(),
            dpkg: "/usr/bin/dpkg".into(),
            dnf: "/usr/bin/dnf".into(),
            rpm: "/usr/bin/rpm".into(),
            locks: ["/var/lib/dpkg/lock-frontend", "/var/lib/dpkg/lock", "/var/cache/apt/archives/lock"]
                .map(PathBuf::from)
                .to_vec(),
            proc_locks: "/proc/locks".into(),
            downloads,
            home,
            temp: std::env::temp_dir(),
        }
    }
}

/// What became of the installation of a package.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Outcome {
    /// The package manager installed it: the app starts again.
    Installed,
    /// The system did not install it, and nothing changed (`pkexec` 126, 127, no `pkexec`): the package is kept here; `apt-get`
    /// never took the lock another installation held (UL2.2): no package is kept.
    Refused(Refusal, Option<PathBuf>),
    /// Nothing was run: the file is not a package of the kind, or not what was checked; the package is kept here, when it
    /// could be.
    NotRun(Option<PathBuf>),
    /// The package manager failed after the password: what is on the disk the next start decides; the package is kept
    /// here, when it could be.
    Unfinished(Option<PathBuf>),
}

/// A step of an installation of a package the page of the start says (UL1.3), with its code in the log of the shell.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Step {
    /// `pkexec` runs: the system asks for the password of the administrator.
    Password,
    /// The package manager writes: the password was given, it installs.
    Running,
    /// The package manager waits for a lock another installation of programs holds.
    LockWait,
}

impl Step {
    pub fn code(self) -> &'static str {
        match self {
            Step::Password => "update_password_asked",
            Step::Running => "update_package_running",
            Step::LockWait => "update_lock_wait",
        }
    }
}

/// What the thread of an installation of a package tells the supervision (UL1.1): its steps as they come, then how it ended.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Progress {
    Step(Step),
    Ended(Outcome),
}

/// Installs the package `file` of `kind`, checked by `keys`: `steps` hears what the page of the start says, `output` gives
/// where the output of the installer goes on to — the stderr of the shell.
pub fn install<W: Write + Send + 'static>(
    file: &Downloaded,
    kind: Kind,
    keys: &Keys,
    programs: &Programs,
    steps: impl FnMut(Step),
    output: fn() -> W,
) -> Outcome {
    let staged = match Staged::write(&programs.temp, kind, &file.bytes) {
        Ok(staged) => staged,
        Err(_) => return Outcome::NotRun(keep(file, kind, programs)),
    };
    let outcome = run(&staged, file, kind, keys, programs, steps, output);
    drop(staged);
    outcome
}

fn run<W: Write + Send + 'static>(
    staged: &Staged,
    file: &Downloaded,
    kind: Kind,
    keys: &Keys,
    programs: &Programs,
    mut steps: impl FnMut(Step),
    output: fn() -> W,
) -> Outcome {
    // The bytes on the disk, read back right before the installation: the kind of the file and the signature again.
    let Ok(bytes) = staged.read() else { return Outcome::NotRun(keep(file, kind, programs)) };
    let right_kind = match kind {
        Kind::Deb => infer::archive::is_deb(&bytes),
        Kind::Rpm => infer::archive::is_rpm(&bytes),
        _ => false,
    };
    if !right_kind || manifest::verify_file(&bytes, &file.release.signature, keys, &file.release.version).is_err() {
        return Outcome::NotRun(keep(file, kind, programs));
    }
    let path = staged.file.as_os_str();
    let mut command = Command::new(&programs.pkexec);
    let apt = kind == Kind::Deb && programs.apt_get.exists();
    match kind {
        Kind::Deb if apt => {
            command.arg(&programs.apt_get).args(["install", "-y", "-o", "DPkg::Lock::Timeout=120"]).arg(path)
        }
        Kind::Deb => command.arg(&programs.dpkg).arg("-i").arg(path),
        Kind::Rpm if programs.dnf.exists() => command.arg(&programs.dnf).args(["install", "-y"]).arg(path),
        _ => command.arg(&programs.rpm).arg("-U").arg(path),
    };
    // UL1.2: the output is read, not inherited — the stdin stays the shell's, as the agent of polkit on a terminal needs.
    command.stdout(Stdio::piped()).stderr(Stdio::piped());
    steps(Step::Password);
    let child = match command.spawn() {
        Ok(child) => child,
        Err(error) if error.kind() == io::ErrorKind::NotFound => {
            return refused(Refusal::Unavailable, file, kind, programs)
        }
        Err(_) => return Outcome::Unfinished(keep(file, kind, programs)),
    };
    let locks: Vec<Vec<u8>> =
        programs.locks.iter().map(|lock| lock.to_string_lossy().into_owned().into_bytes()).collect();
    let (status, watch) = output::follow(child, &locks, &mut steps, output);
    match status {
        Some(status) if status.success() => Outcome::Installed,
        // pkexec: 126 — the author dismissed the window of the password; 127 — no authorization could be had: no agent
        // of polkit, no right.
        Some(status) if status.code() == Some(126) => refused(Refusal::Declined, file, kind, programs),
        Some(status) if status.code() == Some(127) => refused(Refusal::Unavailable, file, kind, programs),
        // UL2.2: only `apt-get` names the lock it could not take; `dpkg -i` and the systems of RPM keep their outcomes.
        _ if apt && watch.never_locked() => Outcome::Refused(Refusal::Busy, None),
        _ => Outcome::Unfinished(keep(file, kind, programs)),
    }
}

fn refused(refusal: Refusal, file: &Downloaded, kind: Kind, programs: &Programs) -> Outcome {
    match keep(file, kind, programs) {
        Some(kept) => Outcome::Refused(refusal, Some(kept)),
        None => Outcome::NotRun(None),
    }
}

/// The package in a new directory of this user alone, removed with it.
struct Staged {
    directory: PathBuf,
    file: PathBuf,
}

impl Staged {
    fn write(temp: &Path, kind: Kind, bytes: &[u8]) -> io::Result<Staged> {
        let directory = new_directory(temp)?;
        let file = directory.join(if kind == Kind::Rpm { "package.rpm" } else { "package.deb" });
        let staged = Staged { directory, file };
        let mut written = new_file(&staged.file)?;
        written.write_all(bytes)?;
        written.sync_all()?;
        Ok(staged)
    }

    fn read(&self) -> io::Result<Vec<u8>> {
        let mut bytes = Vec::new();
        File::open(&self.file)?.read_to_end(&mut bytes)?;
        Ok(bytes)
    }
}

impl Drop for Staged {
    fn drop(&mut self) {
        let _ = fs::remove_dir_all(&self.directory);
    }
}

/// A new directory of `parent` only this user enters (0700).
fn new_directory(parent: &Path) -> io::Result<PathBuf> {
    for _ in 0..16 {
        let mut random = [0u8; 8];
        getrandom::fill(&mut random).map_err(|_| io::Error::other("no randomness"))?;
        let name =
            format!("canonkeeper-update-{}", random.iter().map(|byte| format!("{byte:02x}")).collect::<String>());
        let directory = parent.join(name);
        // Only a package of Linux is staged here; the mode is Unix's, the code builds everywhere.
        #[cfg_attr(not(unix), allow(unused_mut))]
        let mut builder = fs::DirBuilder::new();
        #[cfg(unix)]
        {
            use std::os::unix::fs::DirBuilderExt;
            builder.mode(0o700);
        }
        match builder.create(&directory) {
            Ok(()) => return Ok(directory),
            Err(error) if error.kind() == io::ErrorKind::AlreadyExists => continue,
            Err(error) => return Err(error),
        }
    }
    Err(io::Error::other("no new directory"))
}

#[cfg(all(test, unix))]
mod tests {
    use super::*;
    use crate::update::manifest::{Release, Signer};
    use crate::update::output::LINE;
    use crate::update::sign::{generate, trusted, SecretKey};
    use std::os::unix::fs::PermissionsExt;
    use std::sync::Arc;

    /// The first bytes of a Debian package, as `infer` knows them, then anything.
    fn deb() -> Vec<u8> {
        let mut bytes = b"!<arch>\ndebian-binary   ".to_vec();
        bytes.extend_from_slice(&[b' '; 40]);
        bytes.extend_from_slice(b"the rest of a package");
        bytes
    }

    struct Stand {
        root: PathBuf,
        programs: Programs,
        keys: Keys,
        file: Downloaded,
    }

    /// The lock of the stand as its stand-in installer names it: `LOCK` in the body of its `pkexec`.
    const LOCK: &str = "LOCK";

    impl Stand {
        fn new(name: &str, pkexec: &str) -> Stand {
            let root = std::env::temp_dir().join(format!("canonkeeper-package-{name}-{}", std::process::id()));
            let _ = fs::remove_dir_all(&root);
            for folder in ["bin", "temp", "Загрузки", "дом"] {
                fs::create_dir_all(root.join(folder)).unwrap();
            }
            let script = |name: &str, body: &str| {
                let path = root.join("bin").join(name);
                fs::write(&path, format!("#!/bin/sh\n{body}\n")).unwrap();
                fs::set_permissions(&path, fs::Permissions::from_mode(0o755)).unwrap();
                path
            };
            let record = root.join("record.txt");
            let lock = root.join("lock-frontend");
            let pkexec = script(
                "pkexec",
                &format!(
                    "echo \"$@\" >> '{}'\n{}",
                    record.display(),
                    pkexec.replace(LOCK, &lock.display().to_string())
                ),
            );
            let apt_get = script("apt-get", "exit 0");
            let pair = generate();
            let key = SecretKey::read(&pair.secret).unwrap();
            let keys = Keys { main: pair.public.clone(), spare: pair.public };
            let bytes = deb();
            let release = Release {
                version: semver::Version::new(1, 3, 0),
                notes: String::new(),
                url: url::Url::parse("https://github.com/canonkeeper/desktop-app/releases/download/v1.3.0/a.deb")
                    .unwrap(),
                signature: key.sign(&bytes, &trusted("a.deb", "1.3.0", 1)),
                raw: serde_json::json!({}),
            };
            let programs = Programs {
                pkexec,
                apt_get,
                dpkg: root.join("bin").join("dpkg"),
                dnf: root.join("bin").join("dnf"),
                rpm: root.join("bin").join("rpm"),
                locks: vec![lock],
                proc_locks: root.join("locks"),
                downloads: Some(root.join("Загрузки")),
                home: Some(root.join("дом")),
                temp: root.join("temp"),
            };
            let file = Downloaded { release, bytes: Arc::new(bytes), signer: Signer::Main };
            Stand { root, programs, keys, file }
        }

        fn record(&self) -> String {
            fs::read_to_string(self.root.join("record.txt")).unwrap_or_default()
        }

        /// The installation of the package of the stand, of `kind`, and the steps it said.
        fn install_as(&self, kind: Kind) -> (Outcome, Vec<Step>) {
            let mut steps = Vec::new();
            let outcome = install(&self.file, kind, &self.keys, &self.programs, |step| steps.push(step), io::sink);
            (outcome, steps)
        }

        fn install(&self) -> Outcome {
            self.install_as(Kind::Deb).0
        }
    }

    impl Drop for Stand {
        fn drop(&mut self) {
            let _ = fs::remove_dir_all(&self.root);
        }
    }

    #[test]
    fn pkexec_runs_the_package_manager_by_its_path_and_a_success_leaves_nothing_behind() {
        let stand = Stand::new("installed", "exit 0");
        assert_eq!(stand.install(), Outcome::Installed);
        let record = stand.record();
        let apt = stand.programs.apt_get.display().to_string();
        assert!(record.starts_with(&format!("{apt} install -y -o DPkg::Lock::Timeout=120 ")), "{record}");
        assert!(record.trim_end().ends_with("/package.deb"), "{record}");
        assert_eq!(
            fs::read_dir(&stand.programs.temp).unwrap().count(),
            0,
            "the package stayed in the temporary folder"
        );
        assert_eq!(fs::read_dir(stand.root.join("Загрузки")).unwrap().count(), 0);
    }

    #[test]
    fn without_apt_get_it_is_dpkg() {
        let mut stand = Stand::new("dpkg", "exit 0");
        stand.programs.apt_get = stand.root.join("bin").join("no-apt-get");
        assert_eq!(stand.install(), Outcome::Installed);
        assert!(stand.record().starts_with(&format!("{} -i /", stand.programs.dpkg.display())), "{}", stand.record());
    }

    #[test]
    fn a_closed_window_of_the_password_or_none_keeps_the_package_beside_a_file_of_the_same_name() {
        for (code, refusal) in [("exit 126", Refusal::Declined), ("exit 127", Refusal::Unavailable)] {
            let stand = Stand::new("refused", code);
            let downloads = stand.root.join("Загрузки");
            fs::write(downloads.join("CanonKeeper_1.3.0_amd64.deb"), "a file of the author").unwrap();
            std::os::unix::fs::symlink(stand.root.join("nowhere"), downloads.join("CanonKeeper_1.3.0_amd64 (2).deb"))
                .unwrap();
            let kept = downloads.join("CanonKeeper_1.3.0_amd64 (3).deb");
            assert_eq!(stand.install(), Outcome::Refused(refusal, Some(kept.clone())));
            assert_eq!(fs::read(&kept).unwrap(), *stand.file.bytes);
            assert_eq!(
                fs::read_to_string(downloads.join("CanonKeeper_1.3.0_amd64.deb")).unwrap(),
                "a file of the author"
            );
            assert!(!stand.root.join("nowhere").exists(), "the package went through a link");
        }
        // No pkexec at all: the system cannot ask; without «Загрузки» the package goes home.
        let mut stand = Stand::new("no-pkexec", "exit 0");
        stand.programs.pkexec = stand.root.join("bin").join("no-pkexec");
        fs::remove_dir(stand.root.join("Загрузки")).unwrap();
        assert_eq!(
            stand.install(),
            Outcome::Refused(Refusal::Unavailable, Some(stand.root.join("дом").join("CanonKeeper_1.3.0_amd64.deb")))
        );
    }

    #[test]
    fn a_failure_after_the_password_is_unfinished_and_keeps_the_package() {
        let stand = Stand::new("failed", "exit 1");
        assert_eq!(
            stand.install(),
            Outcome::Unfinished(Some(stand.root.join("Загрузки").join("CanonKeeper_1.3.0_amd64.deb")))
        );
    }

    #[test]
    fn a_package_changed_after_the_check_or_of_another_kind_is_not_installed() {
        // The signature holds for other bytes than those of the file: nothing runs.
        let mut stand = Stand::new("changed", "exit 0");
        let mut other = (*stand.file.bytes).clone();
        other.extend_from_slice(b"changed");
        stand.file.bytes = Arc::new(other);
        assert!(matches!(stand.install(), Outcome::NotRun(_)));
        assert_eq!(stand.record(), "", "pkexec ran");
        // A file that is no package of its kind.
        let stand = Stand::new("kind", "exit 0");
        assert!(matches!(stand.install_as(Kind::Rpm).0, Outcome::NotRun(_)));
        assert_eq!(stand.record(), "", "pkexec ran");
    }

    #[test]
    fn the_package_lies_in_a_directory_of_this_user_alone() {
        let stand = Stand::new("staged", "exit 0");
        let staged = Staged::write(&stand.programs.temp, Kind::Deb, b"bytes").unwrap();
        let directory = fs::metadata(&staged.directory).unwrap().permissions().mode() & 0o777;
        let file = fs::metadata(&staged.file).unwrap().permissions().mode() & 0o777;
        assert_eq!((directory, file), (0o700, 0o600));
        assert!(new_file(&staged.file).is_err(), "a taken name was written");
        let kept = staged.directory.clone();
        drop(staged);
        assert!(!kept.exists());
    }

    const WAITED: &str = "echo 'Ожидание блокировки кэша: Не удалось получить блокировку файла LOCK. Она удерживается процессом 7 (apt)… 1с' >&2\n\
                          echo 'Ожидание блокировки кэша: Не удалось получить блокировку файла LOCK. Она удерживается процессом 7 (apt)… 2с' >&2";
    const NOT_GOT: &str = "echo 'E: Невозможно получить блокировку внешнего интерфейса dpkg (LOCK); она уже используется другим процессом?' >&2";

    // specs/2026-11-17-desk-update-linux.md UL2.2: `apt-get` waited and never took the lock — its error `E:` names the lock and
    // nothing came after the lines of the locks: `busy`, nothing changed, no package is kept.
    #[test]
    fn apt_get_that_never_took_the_lock_is_busy_and_keeps_no_package() {
        let stand = Stand::new("busy", &format!("{WAITED}\n{NOT_GOT}\nexit 100"));
        let (outcome, steps) = stand.install_as(Kind::Deb);
        assert_eq!(outcome, Outcome::Refused(Refusal::Busy, None));
        assert_eq!(steps, [Step::Password, Step::LockWait]);
        assert_eq!(fs::read_dir(stand.root.join("Загрузки")).unwrap().count(), 0, "a package is kept");
    }

    // UL2.2: a wait after which `apt-get` took the lock and the work failed — the system may be half done: `Unfinished` with the
    // package. An error without a lock, the lines of a wait without an error `E:`, a line without a lock after the error that
    // named one, an error of `dpkg -i` that names one, and a lock named past the first 4 KiB of the error are `Unfinished` too.
    #[test]
    fn a_failure_after_the_lock_was_taken_or_of_dpkg_is_unfinished() {
        let kept =
            |stand: &Stand| Outcome::Unfinished(Some(stand.root.join("Загрузки").join("CanonKeeper_1.3.0_amd64.deb")));
        let stand = Stand::new(
            "waited-then-failed",
            &format!("{WAITED}\necho 'Распаковывается canonkeeper (1.3.0)…'\necho 'E: Sub-process /usr/bin/dpkg returned an error code (1)' >&2\nexit 100"),
        );
        assert_eq!(stand.install(), kept(&stand));
        let stand = Stand::new("no-lock", "echo 'E: Нарушены зависимости' >&2\nexit 100");
        assert_eq!(stand.install(), kept(&stand));
        // Only the lines of the wait and a failure without an error of apt (apt-get killed while it waited): not the lock.
        let stand = Stand::new("waited-only", &format!("{WAITED}\nexit 100"));
        assert_eq!(stand.install(), kept(&stand));
        // A line without a lock after the error that named one: the lock was not what ended it.
        let stand = Stand::new("after-the-lock", &format!("{NOT_GOT}\necho 'E: Нарушены зависимости' >&2\nexit 100"));
        assert_eq!(stand.install(), kept(&stand));
        let mut stand = Stand::new("dpkg-locked", &format!("{NOT_GOT}\nexit 2"));
        stand.programs.apt_get = stand.root.join("bin").join("no-apt-get");
        assert_eq!(stand.install(), kept(&stand));
        let long = "x".repeat(LINE);
        let stand = Stand::new("long-error", &format!("echo 'E: {long} LOCK' >&2\nexit 100"));
        assert_eq!(stand.install(), kept(&stand));
    }
}
