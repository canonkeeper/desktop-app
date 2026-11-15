// SPDX-License-Identifier: AGPL-3.0-only

//! A package of Linux, `.deb` or `.rpm` (specs/2026-11-13-desk-updates.md UB2.6): the shell installs it itself, not by the
//! fallback of the updater plugin, which asks for the password in an English window and pipes it through the process of
//! the app. The checked bytes are written into a new directory of this user alone, the kind of the file is checked, and
//! the signature again right before the installation; `pkexec` runs the package manager — `apt-get install`, without it
//! `dpkg -i`; `dnf install`, without it `rpm -U` — every program by its absolute path. Only the system asks for the
//! password. A window of the password closed, or a system that cannot ask for it — nothing changed: the package is kept in
//! «Загрузки» (or the home directory) under the name of the signed version, beside the files there; any other failure
//! after the password leaves the system half done, and the app starts again whole.

use std::fs::{self, File, OpenOptions};
use std::io::{self, Read, Write};
use std::path::{Path, PathBuf};
use std::process::Command;

use super::fetch::Downloaded;
use super::manifest::{self, Keys};
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
    /// The system did not install it, and nothing changed (`pkexec` 126, 127, no `pkexec`): the package is kept here.
    Refused(Refusal, PathBuf),
    /// Nothing was run: the file is not a package of the kind, or not what was checked; the package is kept here, when it
    /// could be.
    NotRun(Option<PathBuf>),
    /// The package manager failed after the password: what is on the disk the next start decides; the package is kept
    /// here, when it could be.
    Unfinished(Option<PathBuf>),
}

/// The name of the package of `version` for the author: built only from the signed version.
pub fn name(kind: Kind, version: &semver::Version) -> Option<String> {
    match kind {
        Kind::Deb => Some(format!("CanonKeeper_{version}_amd64.deb")),
        Kind::Rpm => Some(format!("CanonKeeper-{version}-1.x86_64.rpm")),
        _ => None,
    }
}

/// Installs the package `file` of `kind`, checked by `keys`.
pub fn install(file: &Downloaded, kind: Kind, keys: &Keys, programs: &Programs) -> Outcome {
    let staged = match Staged::write(&programs.temp, kind, &file.bytes) {
        Ok(staged) => staged,
        Err(_) => return Outcome::NotRun(keep(file, kind, programs)),
    };
    let outcome = run(&staged, file, kind, keys, programs);
    drop(staged);
    outcome
}

fn run(staged: &Staged, file: &Downloaded, kind: Kind, keys: &Keys, programs: &Programs) -> Outcome {
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
    match kind {
        Kind::Deb if programs.apt_get.exists() => {
            command.arg(&programs.apt_get).args(["install", "-y", "-o", "DPkg::Lock::Timeout=120"]).arg(path)
        }
        Kind::Deb => command.arg(&programs.dpkg).arg("-i").arg(path),
        Kind::Rpm if programs.dnf.exists() => command.arg(&programs.dnf).args(["install", "-y"]).arg(path),
        _ => command.arg(&programs.rpm).arg("-U").arg(path),
    };
    match command.status() {
        Ok(status) if status.success() => Outcome::Installed,
        // pkexec: 126 — the author dismissed the window of the password; 127 — no authorization could be had: no agent
        // of polkit, no right.
        Ok(status) if status.code() == Some(126) => refused(Refusal::Declined, file, kind, programs),
        Ok(status) if status.code() == Some(127) => refused(Refusal::Unavailable, file, kind, programs),
        Err(error) if error.kind() == io::ErrorKind::NotFound => refused(Refusal::Unavailable, file, kind, programs),
        Ok(_) | Err(_) => Outcome::Unfinished(keep(file, kind, programs)),
    }
}

fn refused(refusal: Refusal, file: &Downloaded, kind: Kind, programs: &Programs) -> Outcome {
    match keep(file, kind, programs) {
        Some(kept) => Outcome::Refused(refusal, kept),
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

/// A new file only this user reads (0600); a name that is taken — by a file or by a link, even one that leads nowhere —
/// is not written.
fn new_file(path: &Path) -> io::Result<File> {
    let mut options = OpenOptions::new();
    options.write(true).create_new(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options.mode(0o600);
    }
    options.open(path)
}

/// Keeps the checked package for the author in «Загрузки», else the home directory, under the name of its signed version:
/// a name that is taken gets « (2)», « (3)»…; nothing of the folder is overwritten.
pub fn keep(file: &Downloaded, kind: Kind, programs: &Programs) -> Option<PathBuf> {
    let name = name(kind, &file.release.version)?;
    let folder = programs.downloads.as_ref().filter(|folder| folder.is_dir()).or(programs.home.as_ref())?;
    let (stem, extension) = name.rsplit_once('.')?;
    for number in 1..100 {
        let candidate =
            if number == 1 { folder.join(&name) } else { folder.join(format!("{stem} ({number}).{extension}")) };
        match new_file(&candidate) {
            Ok(mut written) => {
                if written.write_all(&file.bytes).and_then(|()| written.sync_all()).is_ok() {
                    return Some(candidate);
                }
                drop(written);
                let _ = fs::remove_file(&candidate);
                return None;
            }
            Err(error) if error.kind() == io::ErrorKind::AlreadyExists => continue,
            Err(_) => return None,
        }
    }
    None
}

#[cfg(all(test, unix))]
mod tests {
    use super::*;
    use crate::update::manifest::{Release, Signer};
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
            let pkexec = script("pkexec", &format!("echo \"$@\" >> '{}'\n{pkexec}", record.display()));
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
    }

    impl Drop for Stand {
        fn drop(&mut self) {
            let _ = fs::remove_dir_all(&self.root);
        }
    }

    #[test]
    fn pkexec_runs_the_package_manager_by_its_path_and_a_success_leaves_nothing_behind() {
        let stand = Stand::new("installed", "exit 0");
        assert_eq!(install(&stand.file, Kind::Deb, &stand.keys, &stand.programs), Outcome::Installed);
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
        assert_eq!(install(&stand.file, Kind::Deb, &stand.keys, &stand.programs), Outcome::Installed);
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
            assert_eq!(
                install(&stand.file, Kind::Deb, &stand.keys, &stand.programs),
                Outcome::Refused(refusal, kept.clone())
            );
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
            install(&stand.file, Kind::Deb, &stand.keys, &stand.programs),
            Outcome::Refused(Refusal::Unavailable, stand.root.join("дом").join("CanonKeeper_1.3.0_amd64.deb"))
        );
    }

    #[test]
    fn a_failure_after_the_password_is_unfinished_and_keeps_the_package() {
        let stand = Stand::new("failed", "exit 1");
        assert_eq!(
            install(&stand.file, Kind::Deb, &stand.keys, &stand.programs),
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
        assert!(matches!(install(&stand.file, Kind::Deb, &stand.keys, &stand.programs), Outcome::NotRun(_)));
        assert_eq!(stand.record(), "", "pkexec ran");
        // A file that is no package of its kind.
        let stand = Stand::new("kind", "exit 0");
        assert!(matches!(install(&stand.file, Kind::Rpm, &stand.keys, &stand.programs), Outcome::NotRun(_)));
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
}
