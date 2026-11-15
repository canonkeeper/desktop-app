// SPDX-License-Identifier: AGPL-3.0-only

//! Whether everything that holds the files of an installation stopped (specs/2026-11-13-desk-updates.md UB2.3): the
//! cluster of PostgreSQL of the data directory — no `postmaster.pid` — and no program of the installation besides the
//! shell itself — the backend, PostgreSQL, `codex-app-server`: on Linux by the program of every process (`/proc/*/exe`),
//! on Windows by the path of the image of every process. A process of another user is not seen and holds nothing of
//! this installation.

use std::path::{Path, PathBuf};

/// What still holds the files of an installation.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Holder {
    /// The cluster of the data directory runs: its `postmaster.pid` is there.
    Cluster,
    /// A program of the installation runs.
    Program(PathBuf),
}

/// What of the installation still runs: the cluster of `data_dir`, and a process whose program is one of `programs` or
/// lies in one of the directories `programs` names — never this process, and never a program of `except`: the bridge for
/// the programs of the author, which the installer ends itself (specs/2026-11-15-desk-mcp.md MC1.8). The paths of `except`
/// are made plain as the programs of the processes are.
pub fn holders(data_dir: &Path, programs: &[PathBuf], except: &[PathBuf]) -> Vec<Holder> {
    let mut found = Vec::new();
    if data_dir.join("pg").join("postmaster.pid").exists() {
        found.push(Holder::Cluster);
    }
    let own = std::process::id();
    let programs = plain(programs);
    let except = plain(except);
    for (pid, image) in processes() {
        if pid != own
            && !except.contains(&image)
            && programs.iter().any(|program| image == *program || image.starts_with(program))
        {
            found.push(Holder::Program(image));
        }
    }
    found
}

/// Whether a process of one of `programs` runs — the bridge of the installation before an installer ends it (MC1.8).
pub fn runs(programs: &[PathBuf]) -> bool {
    let programs = plain(programs);
    !programs.is_empty() && processes().into_iter().any(|(_, image)| programs.contains(&image))
}

fn plain(paths: &[PathBuf]) -> Vec<PathBuf> {
    paths.iter().map(|path| path.canonicalize().unwrap_or_else(|_| path.clone())).collect()
}

/// The processes of this user with the program each runs.
#[cfg(target_os = "linux")]
fn processes() -> Vec<(u32, PathBuf)> {
    let Ok(entries) = std::fs::read_dir("/proc") else { return Vec::new() };
    entries
        .flatten()
        .filter_map(|entry| {
            let pid: u32 = entry.file_name().to_str()?.parse().ok()?;
            let image = std::fs::read_link(entry.path().join("exe")).ok()?;
            // A program replaced while it runs reads « (deleted)»: it still holds the place of the old one.
            let text = image.to_string_lossy();
            let image = match text.strip_suffix(" (deleted)") {
                Some(path) => PathBuf::from(path),
                None => image,
            };
            Some((pid, image))
        })
        .collect()
}

#[cfg(windows)]
fn processes() -> Vec<(u32, PathBuf)> {
    use std::ffi::OsString;
    use std::os::windows::ffi::OsStringExt;
    use windows_sys::Win32::Foundation::{CloseHandle, INVALID_HANDLE_VALUE};
    use windows_sys::Win32::System::Diagnostics::ToolHelp::{
        CreateToolhelp32Snapshot, Process32FirstW, Process32NextW, PROCESSENTRY32W, TH32CS_SNAPPROCESS,
    };
    use windows_sys::Win32::System::Threading::{
        OpenProcess, QueryFullProcessImageNameW, PROCESS_NAME_WIN32, PROCESS_QUERY_LIMITED_INFORMATION,
    };

    let mut found = Vec::new();
    // SAFETY: the snapshot and every process handle are closed below; the buffers outlive the calls that fill them.
    unsafe {
        let snapshot = CreateToolhelp32Snapshot(TH32CS_SNAPPROCESS, 0);
        if snapshot == INVALID_HANDLE_VALUE {
            return found;
        }
        let mut entry: PROCESSENTRY32W = std::mem::zeroed();
        entry.dwSize = std::mem::size_of::<PROCESSENTRY32W>() as u32;
        let mut more = Process32FirstW(snapshot, &mut entry) != 0;
        while more {
            let pid = entry.th32ProcessID;
            let process = OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, 0, pid);
            if !process.is_null() {
                let mut buffer = vec![0u16; 32 * 1024];
                let mut length = buffer.len() as u32;
                if QueryFullProcessImageNameW(process, PROCESS_NAME_WIN32, buffer.as_mut_ptr(), &mut length) != 0 {
                    let image = PathBuf::from(OsString::from_wide(&buffer[..length as usize]));
                    found.push((pid, image.canonicalize().unwrap_or(image)));
                }
                CloseHandle(process);
            }
            more = Process32NextW(snapshot, &mut entry) != 0;
        }
        CloseHandle(snapshot);
    }
    found
}

#[cfg(not(any(target_os = "linux", windows)))]
fn processes() -> Vec<(u32, PathBuf)> {
    Vec::new()
}

#[cfg(all(test, target_os = "linux"))]
mod tests {
    use super::*;
    use std::fs;

    #[test]
    fn a_cluster_or_a_program_of_the_installation_that_runs_holds_it() {
        let root = std::env::temp_dir().join(format!("canonkeeper-stopped-{}", std::process::id()));
        let _ = fs::remove_dir_all(&root);
        let (data, resources) = (root.join("data"), root.join("resources"));
        fs::create_dir_all(data.join("pg")).unwrap();
        let programs = [root.join("canonkeeper-backend"), resources.clone()];
        assert_eq!(holders(&data, &programs, &[]), vec![], "nothing runs");

        fs::write(data.join("pg").join("postmaster.pid"), "4242\n").unwrap();
        assert_eq!(holders(&data, &programs, &[]), vec![Holder::Cluster]);
        fs::remove_file(data.join("pg").join("postmaster.pid")).unwrap();

        // A program of the installation that still runs is in tests/driver.rs: a copy of the stand-in backend.
        // This very process is never a holder, though its program lies where it is asked about.
        let own = std::env::current_exe().unwrap();
        assert_eq!(holders(&data, &[own], &[]), vec![]);
        assert!(!runs(&[root.join("canonkeeper-mcp")]), "no bridge runs");
        assert!(!runs(&[]), "no program, nothing runs");
        fs::remove_dir_all(root).unwrap();
    }
}
