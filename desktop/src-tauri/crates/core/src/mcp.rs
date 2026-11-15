// SPDX-License-Identifier: AGPL-3.0-only

//! The bridge for the programs of the author (specs/2026-11-15-desk-mcp.md MC1.1, MC3.2): the program `canonkeeper-mcp` lies
//! beside the program of the app; Claude Code, Claude Desktop or Codex of the author runs it with the data directory. The
//! shell names it in `start` for the commands the backend builds, and an AppImage, whose files change their place at every
//! run, gives its own file with the first argument `mcp`: the shell replaces itself with the program of the bridge before
//! the lock of one copy and before Tauri.

use std::ffi::{OsStr, OsString};
use std::io;
use std::path::{Path, PathBuf};

use serde::Serialize;

/// The program of the bridge beside the program of the app.
pub const PROGRAM: &str = if cfg!(windows) { "canonkeeper-mcp.exe" } else { "canonkeeper-mcp" };

/// The first argument of an AppImage that runs the bridge.
pub const ARGUMENT: &str = "mcp";

/// How the program of the bridge runs, as `start` names it: the program and the arguments before the data directory.
#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
pub struct Bridge {
    pub program: String,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub args: Vec<String>,
}

/// The bridge of this copy of the app: an AppImage gives its file and `mcp`; else the program beside the shell, when it is
/// there; none without it.
pub fn of(shell_dir: &Path, appimage: Option<&Path>, exists: impl Fn(&Path) -> bool) -> Option<Bridge> {
    let beside = shell_dir.join(PROGRAM);
    if !exists(&beside) {
        return None;
    }
    match appimage {
        Some(file) => Some(Bridge { program: file.to_str()?.to_string(), args: vec![ARGUMENT.to_string()] }),
        None => Some(Bridge { program: beside.to_str()?.to_string(), args: Vec::new() }),
    }
}

/// The program of the bridge and its arguments when the first argument of the app is `mcp`; none else — the app starts as
/// it always does.
pub fn command_of(arguments: &[OsString], shell_dir: &Path) -> Option<(PathBuf, Vec<OsString>)> {
    let (first, rest) = arguments.split_first()?;
    (first.as_os_str() == OsStr::new(ARGUMENT)).then(|| (shell_dir.join(PROGRAM), rest.to_vec()))
}

/// Runs the bridge in place of the app when the arguments ask for it: `exec` replaces the process and returns only its
/// failure — one line on stderr and the code 1, no window; so does a program of the app whose directory is not known
/// (`current_exe` did not answer). None — the app starts.
pub fn replace(
    arguments: &[OsString],
    shell_dir: Option<&Path>,
    exec: impl FnOnce(&Path, &[OsString]) -> io::Error,
) -> Option<i32> {
    let first = arguments.first()?;
    if first.as_os_str() != OsStr::new(ARGUMENT) {
        return None;
    }
    let Some(shell_dir) = shell_dir else {
        eprintln!(
            "canonkeeper: the program of the bridge did not start: the place of the program of the app is not known"
        );
        return Some(1);
    };
    let (program, rest) = command_of(arguments, shell_dir)?;
    let failure = exec(&program, &rest);
    eprintln!("canonkeeper: the program of the bridge did not start: {failure}");
    Some(1)
}

#[cfg(test)]
mod tests {
    use super::*;

    // MC1.1: the first argument mcp is the program of the bridge beside the app with the other arguments as they are;
    // anything else starts the app; a failed exec is the code 1 without the window.
    #[test]
    fn the_arguments_choose_the_program_of_the_bridge() {
        let directory = Path::new("/tmp/.mount_abc/usr/bin");
        let arguments = vec![OsString::from("mcp"), OsString::from("/home/author/данные саги")];
        assert_eq!(
            command_of(&arguments, directory),
            Some((directory.join(PROGRAM), vec![OsString::from("/home/author/данные саги")]))
        );
        assert_eq!(command_of(&[], directory), None);
        assert_eq!(command_of(&[OsString::from("--other")], directory), None);
        assert_eq!(command_of(&[OsString::from("mcp-other")], directory), None);
        let mut ran = None;
        let code = replace(&arguments, Some(directory), |program, rest| {
            ran = Some((program.to_path_buf(), rest.to_vec()));
            io::Error::from(io::ErrorKind::NotFound)
        });
        assert_eq!(code, Some(1));
        assert_eq!(ran, Some((directory.join(PROGRAM), vec![OsString::from("/home/author/данные саги")])));
        assert_eq!(replace(&[OsString::from("--x")], Some(directory), |_, _| unreachable!()), None);
        // current_exe did not answer: mcp is the code 1 without the window, and nothing runs; the app without mcp starts.
        assert_eq!(replace(&arguments, None, |_, _| unreachable!()), Some(1));
        assert_eq!(replace(&[OsString::from("--x")], None, |_, _| unreachable!()), None);
        assert_eq!(replace(&[], None, |_, _| unreachable!()), None);
    }

    // MC3.2: an AppImage names its file and mcp; a program beside the shell names itself; without it there is no bridge.
    #[test]
    fn the_bridge_of_a_copy_of_the_app() {
        let directory = Path::new("/usr/bin");
        // Beside the shell is its directory joined with the program, as the system joins them: `/usr/bin\…` on Windows.
        let beside = directory.join(PROGRAM).to_str().unwrap().to_string();
        assert_eq!(of(directory, None, |_| true), Some(Bridge { program: beside, args: Vec::new() }));
        assert_eq!(
            of(directory, Some(Path::new("/home/a/CanonKeeper.AppImage")), |_| true),
            Some(Bridge { program: "/home/a/CanonKeeper.AppImage".into(), args: vec!["mcp".into()] })
        );
        assert_eq!(of(directory, None, |_| false), None);
        let line =
            serde_json::to_value(Bridge { program: "/usr/bin/canonkeeper-mcp".into(), args: Vec::new() }).unwrap();
        assert_eq!(line, serde_json::json!({"program": "/usr/bin/canonkeeper-mcp"}));
    }
}
