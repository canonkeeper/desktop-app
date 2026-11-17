// SPDX-License-Identifier: AGPL-3.0-only

//! The package of a version kept for the author (specs/2026-11-13-desk-updates.md UB2.6, specs/2026-11-17-desk-update-linux.md
//! UL2.3): when the system did not install it, it lies in «Загрузки» — or the home directory — under the name of its signed
//! version, beside the files there and never over one; a plain file of a name of the package with its very bytes is the
//! package kept before, and no second copy is written.

use std::fs::{self, File, OpenOptions};
use std::io::{self, Read, Write};
use std::path::{Path, PathBuf};

use sha2::{Digest, Sha256};

use super::fetch::Downloaded;
use super::package::Programs;
use super::Kind;

/// The name of the package of `version` for the author: built only from the signed version.
pub fn name(kind: Kind, version: &semver::Version) -> Option<String> {
    match kind {
        Kind::Deb => Some(format!("CanonKeeper_{version}_amd64.deb")),
        Kind::Rpm => Some(format!("CanonKeeper-{version}-1.x86_64.rpm")),
        _ => None,
    }
}

/// Keeps the checked package for the author in «Загрузки», else the home directory, under the name of its signed version:
/// a name that is taken gets « (2)», « (3)»…; nothing of the folder is overwritten. A plain file of a name of the package
/// with these very bytes is the package already kept (specs/2026-11-17-desk-update-linux.md UL2.3): no second copy.
pub fn keep(file: &Downloaded, kind: Kind, programs: &Programs) -> Option<PathBuf> {
    let name = name(kind, &file.release.version)?;
    let folder = programs.downloads.as_ref().filter(|folder| folder.is_dir()).or(programs.home.as_ref())?;
    let (stem, extension) = name.rsplit_once('.')?;
    for number in 1..100 {
        let candidate =
            if number == 1 { folder.join(&name) } else { folder.join(format!("{stem} ({number}).{extension}")) };
        if same(&candidate, &file.bytes) {
            return Some(candidate);
        }
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

/// Whether `path` is a plain file — not a link — of these bytes: its size first, then its SHA-256, read from the file that
/// was looked at.
fn same(path: &Path, bytes: &[u8]) -> bool {
    let Ok(seen) = fs::symlink_metadata(path) else { return false };
    if !seen.file_type().is_file() || seen.len() != bytes.len() as u64 {
        return false;
    }
    let Ok(mut opened) = File::open(path) else { return false };
    #[cfg(unix)]
    {
        use std::os::unix::fs::MetadataExt;
        // A link put in its place meanwhile is not read.
        if !opened.metadata().is_ok_and(|read| (read.dev(), read.ino()) == (seen.dev(), seen.ino())) {
            return false;
        }
    }
    let mut digest = Sha256::new();
    let mut chunk = vec![0u8; 64 * 1024];
    loop {
        match opened.read(&mut chunk) {
            Ok(0) => break,
            Ok(read) => digest.update(&chunk[..read]),
            Err(error) if error.kind() == io::ErrorKind::Interrupted => {}
            Err(_) => return false,
        }
    }
    digest.finalize() == Sha256::digest(bytes)
}

/// A new file only this user reads (0600); a name that is taken — by a file or by a link, even one that leads nowhere —
/// is not written.
pub(super) fn new_file(path: &Path) -> io::Result<File> {
    let mut options = OpenOptions::new();
    options.write(true).create_new(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options.mode(0o600);
    }
    options.open(path)
}

#[cfg(all(test, unix))]
mod tests {
    use super::*;
    use crate::update::manifest::{Release, Signer};
    use std::sync::Arc;

    struct Stand {
        root: PathBuf,
        programs: Programs,
        file: Downloaded,
    }

    impl Stand {
        fn new(name: &str) -> Stand {
            let root = std::env::temp_dir().join(format!("canonkeeper-kept-{name}-{}", std::process::id()));
            let _ = fs::remove_dir_all(&root);
            for folder in ["Загрузки", "дом"] {
                fs::create_dir_all(root.join(folder)).unwrap();
            }
            let programs = Programs::system(Some(root.join("Загрузки")), Some(root.join("дом")));
            let release = Release {
                version: semver::Version::new(1, 3, 0),
                notes: String::new(),
                url: url::Url::parse("https://github.com/canonkeeper/desktop-app/releases/download/v1.3.0/a.deb")
                    .unwrap(),
                signature: "c2ln".into(),
                raw: serde_json::json!({}),
            };
            let file = Downloaded { release, bytes: Arc::new(b"the package of 1.3.0".to_vec()), signer: Signer::Main };
            Stand { root, programs, file }
        }
    }

    impl Drop for Stand {
        fn drop(&mut self) {
            let _ = fs::remove_dir_all(&self.root);
        }
    }

    // specs/2026-11-17-desk-update-linux.md UL2.3: the package is not kept twice — a plain file of its name with the same bytes is
    // it; a file of the author of the same size and other bytes, and a link to the very package, are not.
    #[test]
    fn the_package_is_kept_once() {
        let stand = Stand::new("kept-once");
        let downloads = stand.root.join("Загрузки");
        let first = downloads.join("CanonKeeper_1.3.0_amd64.deb");
        assert_eq!(keep(&stand.file, Kind::Deb, &stand.programs), Some(first.clone()));
        assert_eq!(keep(&stand.file, Kind::Deb, &stand.programs), Some(first));
        assert_eq!(fs::read_dir(&downloads).unwrap().count(), 1);

        let stand = Stand::new("kept-beside");
        let downloads = stand.root.join("Загрузки");
        let mut other = (*stand.file.bytes).clone();
        other.reverse();
        fs::write(downloads.join("CanonKeeper_1.3.0_amd64.deb"), &other).unwrap();
        let elsewhere = stand.root.join("дом").join("package.deb");
        fs::write(&elsewhere, &*stand.file.bytes).unwrap();
        std::os::unix::fs::symlink(&elsewhere, downloads.join("CanonKeeper_1.3.0_amd64 (2).deb")).unwrap();
        let kept = downloads.join("CanonKeeper_1.3.0_amd64 (3).deb");
        assert_eq!(keep(&stand.file, Kind::Deb, &stand.programs), Some(kept.clone()));
        assert_eq!(keep(&stand.file, Kind::Deb, &stand.programs), Some(kept));
        assert_eq!(fs::read_dir(&downloads).unwrap().count(), 3);
    }

    // UB2.6: no «Загрузки» — the home directory; a kind that is no package of Linux has no name.
    #[test]
    fn without_downloads_the_package_goes_home() {
        let stand = Stand::new("home");
        fs::remove_dir(stand.root.join("Загрузки")).unwrap();
        let home = stand.root.join("дом").join("CanonKeeper_1.3.0_amd64.deb");
        assert_eq!(keep(&stand.file, Kind::Deb, &stand.programs), Some(home.clone()));
        assert_eq!(fs::read(home).unwrap(), *stand.file.bytes);
        assert_eq!(keep(&stand.file, Kind::AppImage, &stand.programs), None);
        assert_eq!(name(Kind::Rpm, &stand.file.release.version).as_deref(), Some("CanonKeeper-1.3.0-1.x86_64.rpm"));
    }
}
