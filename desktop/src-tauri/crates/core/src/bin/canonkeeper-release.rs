// SPDX-License-Identifier: AGPL-3.0-only

//! The tool of a release of the app (specs/2026-11-13-desk-updates.md UB3.2–UB3.4, UB3.7): the one signer and the one
//! check of `canonkeeper_desktop_core::update::release`, which the job of the signature and the smoke run of an update
//! call. It trusts the keys and the addresses of the policy it is built with — the tool built from a tag carries the main
//! key of that tag, the tool of the smoke build its key of one run.
//!
//!   canonkeeper-release keygen <new file for the secret half>
//!   canonkeeper-release sign --version X.Y.Z --base-url URL --notes FILE --key-env NAME <directory>
//!   canonkeeper-release check --tag vX.Y.Z --conf tauri.conf.json --cargo Cargo.toml [--kinds deb,rpm,…] <directory>
//!   canonkeeper-release marks --kind deb|rpm|appimage|nsis <directory of the unpacked package>
//!
//! A secret key comes only from the variable `--key-env` names and is never printed; `keygen` writes the secret half
//! into a new file of this user alone and prints the public half.

use std::collections::HashMap;
use std::fs::{self, OpenOptions};
use std::io::Write;
use std::path::Path;
use std::process::ExitCode;
use std::time::{SystemTime, UNIX_EPOCH};

use canonkeeper_desktop_core::update::policy::Policy;
use canonkeeper_desktop_core::update::release::{self, Expected};
use canonkeeper_desktop_core::update::sign::{generate, SecretKey};
use canonkeeper_desktop_core::update::Kind;
use semver::Version;
use url::Url;

const USAGE: &str =
    "canonkeeper-release keygen FILE | sign --version V --base-url URL --notes FILE --key-env NAME DIR | \
check --tag vV --conf tauri.conf.json --cargo Cargo.toml [--kinds K,…] DIR | marks --kind K UNPACKED";

/// The options `--name value` and the rest, in order.
fn options(args: &[String]) -> Result<(HashMap<String, String>, Vec<String>), String> {
    let mut named = HashMap::new();
    let mut rest = Vec::new();
    let mut args = args.iter();
    while let Some(arg) = args.next() {
        if let Some(name) = arg.strip_prefix("--") {
            let value = args.next().ok_or(format!("--{name} has no value"))?;
            named.insert(name.to_string(), value.clone());
        } else {
            rest.push(arg.clone());
        }
    }
    Ok((named, rest))
}

fn required<'a>(named: &'a HashMap<String, String>, name: &str) -> Result<&'a str, String> {
    named.get(name).map(String::as_str).ok_or(format!("--{name} is required"))
}

fn one(rest: &[String], what: &str) -> Result<String, String> {
    match rest {
        [only] => Ok(only.clone()),
        _ => Err(format!("one {what} is required")),
    }
}

fn kind(name: &str) -> Result<Kind, String> {
    Kind::RELEASED.into_iter().find(|kind| kind.name() == name).ok_or(format!("{name} is not a kind of installation"))
}

/// The version of the package of a Cargo.toml: `version` of its section `[package]`.
fn package_version(manifest: &str) -> Option<String> {
    let mut section = "";
    for line in manifest.lines().map(str::trim) {
        if line.starts_with('[') {
            section = line;
        } else if section == "[package]" {
            if let Some(value) = line.strip_prefix("version").map(str::trim).and_then(|rest| rest.strip_prefix('=')) {
                return Some(value.trim().trim_matches('"').to_string());
            }
        }
    }
    None
}

fn keygen(rest: &[String]) -> Result<(), String> {
    let path = one(rest, "file for the secret half")?;
    let pair = generate();
    let mut file = OpenOptions::new();
    file.write(true).create_new(true);
    #[cfg(unix)]
    std::os::unix::fs::OpenOptionsExt::mode(&mut file, 0o600);
    let mut file = file.open(&path).map_err(|error| format!("{path}: {error}"))?;
    file.write_all(pair.secret.as_bytes()).map_err(|error| format!("{path}: {error}"))?;
    println!("{}", pair.public);
    Ok(())
}

fn sign(args: &[String]) -> Result<(), String> {
    let (named, rest) = options(args)?;
    let dir = one(&rest, "directory of the installers")?;
    let version = required(&named, "version")?;
    let version = Version::parse(version.trim_start_matches('v')).map_err(|_| format!("{version} is not a version"))?;
    let base = Url::parse(required(&named, "base-url")?).map_err(|error| format!("--base-url: {error}"))?;
    let notes_file = required(&named, "notes")?;
    let notes = fs::read_to_string(notes_file).map_err(|error| format!("{notes_file}: {error}"))?;
    let variable = required(&named, "key-env")?;
    let secret = std::env::var(variable).map_err(|_| format!("the variable {variable} holds no key"))?;
    let key = SecretKey::read(&secret).map_err(|error| format!("the key of {variable} is not one: {error:?}"))?;
    drop(secret);
    let timestamp = SystemTime::now().duration_since(UNIX_EPOCH).map_err(|error| error.to_string())?.as_secs();
    let written =
        release::sign(Path::new(&dir), &version, &base, notes.trim_end(), &key, &Policy::release(), timestamp)?;
    for name in written {
        println!("signed {name}");
    }
    Ok(())
}

fn check(args: &[String]) -> Result<(), String> {
    let (named, rest) = options(args)?;
    let dir = one(&rest, "directory of the release")?;
    let tag = required(&named, "tag")?;
    let conf_path = required(&named, "conf")?;
    let conf: serde_json::Value =
        serde_json::from_slice(&fs::read(conf_path).map_err(|error| format!("{conf_path}: {error}"))?)
            .map_err(|error| format!("{conf_path}: {error}"))?;
    let configured = conf.pointer("/plugins/updater/pubkey").and_then(|key| key.as_str()).unwrap_or("").to_string();
    let cargo_path = required(&named, "cargo")?;
    let cargo = fs::read_to_string(cargo_path).map_err(|error| format!("{cargo_path}: {error}"))?;
    let shell_version = package_version(&cargo).ok_or(format!("{cargo_path} names no version of its package"))?;
    let kinds = match named.get("kinds") {
        Some(list) => list.split(',').map(kind).collect::<Result<Vec<_>, _>>()?,
        None => Kind::RELEASED.to_vec(),
    };
    let expected = Expected { tag, shell_version: &shell_version, configured_key: &configured, kinds: &kinds };
    let problems = release::check(Path::new(&dir), &expected, &Policy::release());
    if problems.is_empty() {
        println!("the release {tag} holds: {} installers", kinds.len());
        return Ok(());
    }
    Err(problems.join("\n"))
}

/// The marks of the programs of an unpacked package (UB3.3): every word names the package, a refusal the program too.
fn marks(args: &[String]) -> Result<(), String> {
    let (named, rest) = options(args)?;
    let kind = kind(required(&named, "kind")?)?;
    let unpacked = one(&rest, "directory of the unpacked package")?;
    let package = format!("the {} package", kind.name());
    let programs = release::programs(Path::new(&unpacked), kind)?;
    let read = |program: &Path| fs::read(program).map_err(|error| format!("{package}: {}: {error}", program.display()));
    let (shell, others) = programs.split_first().ok_or(format!("{package} has no programs"))?;
    let mut problems: Vec<String> = release::marks(&read(shell)?, kind)
        .into_iter()
        .map(|problem| format!("{package}: {}: {problem}", shell.display()))
        .collect();
    for other in others {
        if release::is_smoke(&read(other)?) {
            problems.push(format!("{package}: {}: the program is a smoke build of updates", other.display()));
        }
    }
    if problems.is_empty() {
        println!(
            "{package}: {} is marked as {}; no program of the package is a smoke build",
            shell.display(),
            kind.name()
        );
        return Ok(());
    }
    Err(problems.join("\n"))
}

fn main() -> ExitCode {
    let args: Vec<String> = std::env::args().skip(1).collect();
    let result = match args.split_first() {
        Some((command, rest)) if command == "keygen" => keygen(rest),
        Some((command, rest)) if command == "sign" => sign(rest),
        Some((command, rest)) if command == "check" => check(rest),
        Some((command, rest)) if command == "marks" => marks(rest),
        _ => Err(USAGE.to_string()),
    };
    match result {
        Ok(()) => ExitCode::SUCCESS,
        Err(problem) => {
            eprintln!("canonkeeper-release: {problem}");
            ExitCode::FAILURE
        }
    }
}
