// SPDX-License-Identifier: AGPL-3.0-only

//! The data directory of the user and the plain form of the paths the shell hands to the backend
//! (specs/2026-11-12-desk-shell.md SH1.3; specs/2026-11-12-desk-process.md PR2). Paths are strings: they go into the
//! JSON of `start`, and the rules of both systems are checked on either of them.

/// The identifier of the app: the installer, the directory of the data on Windows and the single instance share it.
pub const APP_IDENTIFIER: &str = "ru.canonkeeper.desktop";

/// The system whose path rules apply.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum System {
    Windows,
    Linux,
}

impl System {
    /// The system this shell runs on.
    pub const CURRENT: System = if cfg!(windows) { System::Windows } else { System::Linux };

    fn separator(self) -> char {
        match self {
            System::Windows => '\\',
            System::Linux => '/',
        }
    }

    /// Joins names under a directory with the separator of the system.
    pub fn join(self, directory: &str, names: &[&str]) -> String {
        let separator = self.separator();
        let mut path = directory.trim_end_matches(separator).to_string();
        for name in names {
            path.push(separator);
            path.push_str(name);
        }
        path
    }
}

/// Why a path cannot be handed to the backend.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum PathError {
    /// The system named no place for the data of the user.
    Unknown,
    /// A network path (UNC): PostgreSQL and the files of the user do not live there.
    Network,
    /// Not an absolute path.
    Relative,
}

/// What the system says about the places of the user: `%LOCALAPPDATA%` on Windows, `$XDG_DATA_HOME` and the home on
/// Linux.
#[derive(Debug, Clone, Copy, Default)]
pub struct Places<'a> {
    pub local_app_data: Option<&'a str>,
    pub xdg_data_home: Option<&'a str>,
    pub home: Option<&'a str>,
}

/// The data directory of the user: Windows — `%LOCALAPPDATA%\ru.canonkeeper.desktop\data`, apart from the directory
/// of the installation; Linux — `$XDG_DATA_HOME/canonkeeper` when it is absolute, otherwise
/// `~/.local/share/canonkeeper`.
pub fn data_dir(system: System, places: Places<'_>) -> Result<String, PathError> {
    match system {
        System::Windows => {
            let base = places.local_app_data.filter(|base| !base.is_empty()).ok_or(PathError::Unknown)?;
            Ok(system.join(&plain(system, base)?, &[APP_IDENTIFIER, "data"]))
        }
        System::Linux => {
            if let Some(base) = places.xdg_data_home.filter(|base| base.starts_with('/')) {
                return Ok(system.join(base, &["canonkeeper"]));
            }
            let home = places.home.filter(|home| !home.is_empty()).ok_or(PathError::Unknown)?;
            Ok(system.join(&plain(system, home)?, &[".local", "share", "canonkeeper"]))
        }
    }
}

/// The plain form of an absolute path. On Windows the shell gets the directory of its resources as a verbatim path
/// (`\\?\C:\…`), through which initdb does not find postgres.exe (DA1.9, finding 2): the prefix is dropped. A network
/// path, verbatim (`\\?\UNC\…`) or not (`\\server\share`), is refused.
pub fn plain(system: System, path: &str) -> Result<String, PathError> {
    match system {
        System::Windows => {
            let rest = path.strip_prefix(r"\\?\").unwrap_or(path);
            if rest.starts_with(r"\\")
                || rest.starts_with("//")
                || rest.get(..4).is_some_and(|head| head.eq_ignore_ascii_case(r"UNC\"))
            {
                return Err(PathError::Network);
            }
            let bytes = rest.as_bytes();
            let drive = bytes.len() >= 3
                && bytes[0].is_ascii_alphabetic()
                && bytes[1] == b':'
                && (bytes[2] == b'\\' || bytes[2] == b'/');
            if !drive {
                return Err(PathError::Relative);
            }
            Ok(rest.to_string())
        }
        System::Linux => {
            if path.starts_with('/') {
                Ok(path.to_string())
            } else {
                Err(PathError::Relative)
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn the_data_of_windows_live_under_the_identifier_apart_from_the_installation() {
        let places = Places { local_app_data: Some(r"C:\Users\Тест\AppData\Local"), ..Places::default() };
        assert_eq!(
            data_dir(System::Windows, places),
            Ok(r"C:\Users\Тест\AppData\Local\ru.canonkeeper.desktop\data".to_string())
        );
        let trailing = Places { local_app_data: Some(r"D:\Profiles\x\Local\"), ..Places::default() };
        assert_eq!(
            data_dir(System::Windows, trailing),
            Ok(r"D:\Profiles\x\Local\ru.canonkeeper.desktop\data".to_string())
        );
        assert_eq!(data_dir(System::Windows, Places::default()), Err(PathError::Unknown));
        let network = Places { local_app_data: Some(r"\\server\profiles\x\Local"), ..Places::default() };
        assert_eq!(data_dir(System::Windows, network), Err(PathError::Network));
    }

    #[test]
    fn the_data_of_linux_follow_xdg_or_the_home() {
        let xdg =
            Places { xdg_data_home: Some("/home/тест/.data"), home: Some("/home/тест"), ..Places::default() };
        assert_eq!(data_dir(System::Linux, xdg), Ok("/home/тест/.data/canonkeeper".to_string()));
        let relative = Places { xdg_data_home: Some("relative/data"), home: Some("/home/a"), ..Places::default() };
        assert_eq!(data_dir(System::Linux, relative), Ok("/home/a/.local/share/canonkeeper".to_string()));
        let home = Places { home: Some("/home/a/"), ..Places::default() };
        assert_eq!(data_dir(System::Linux, home), Ok("/home/a/.local/share/canonkeeper".to_string()));
        assert_eq!(data_dir(System::Linux, Places::default()), Err(PathError::Unknown));
    }

    #[test]
    fn a_verbatim_windows_path_becomes_plain_and_a_network_one_is_refused() {
        assert_eq!(
            plain(System::Windows, r"\\?\C:\Users\Тест\AppData\Local\CanonKeeper"),
            Ok(r"C:\Users\Тест\AppData\Local\CanonKeeper".to_string())
        );
        assert_eq!(plain(System::Windows, r"C:\Program Files\x"), Ok(r"C:\Program Files\x".to_string()));
        assert_eq!(plain(System::Windows, r"\\?\UNC\server\share\x"), Err(PathError::Network));
        assert_eq!(plain(System::Windows, r"\\server\share\x"), Err(PathError::Network));
        assert_eq!(plain(System::Windows, r"\\?\Volume{0b1c}\x"), Err(PathError::Relative));
        assert_eq!(plain(System::Windows, r"relative\x"), Err(PathError::Relative));
        assert_eq!(plain(System::Windows, r"C:"), Err(PathError::Relative));
        assert_eq!(plain(System::Linux, "/usr/lib/CanonKeeper"), Ok("/usr/lib/CanonKeeper".to_string()));
        assert_eq!(plain(System::Linux, "usr/lib"), Err(PathError::Relative));
    }
}
