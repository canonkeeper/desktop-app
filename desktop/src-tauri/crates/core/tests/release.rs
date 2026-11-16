// SPDX-License-Identifier: AGPL-3.0-only

//! The signer and the check of a release (specs/2026-11-13-desk-updates.md UB3.2–UB3.4) on made-up installers and the
//! keys of a test, made here on the fly: the signatures carry the version, `latest.json` names a file for each kind and
//! passes the reading and the verification of the app — the same code —, and a signature without the version, versions
//! that differ, a key of development or a key out of tune fail the release. A released policy signs the manifest of the
//! site too, `latest-site.json`, with every file at its address of the site; the check requires it exactly so
//! (specs/2026-11-16-desk-app-mirror.md AM1.1, AM1.2).

use std::fs;

use base64::engine::general_purpose::STANDARD;
use base64::Engine;
use canonkeeper_desktop_core::update::manifest::{self, Keys};
use canonkeeper_desktop_core::update::policy::{Limits, Policy, HOSTS, MAIN_KEY, SITE_FILES, SMOKE_MARK, SPARE_KEY};
use canonkeeper_desktop_core::update::release::{
    check, installers, kind_of, marks, programs, rfc3339, sign, Expected, MANIFEST, SITE_LATEST, SUMS,
};
use canonkeeper_desktop_core::update::sign::{generate, trusted, SecretKey};
use canonkeeper_desktop_core::update::Kind;
use semver::Version;
use serde_json::Value;
use url::Url;

const KINDS: [Kind; 4] = Kind::RELEASED;

struct Stand {
    dir: std::path::PathBuf,
}

impl Drop for Stand {
    fn drop(&mut self) {
        let _ = fs::remove_dir_all(&self.dir);
    }
}

fn stand(name: &str, files: &[&str]) -> Stand {
    let dir = std::env::temp_dir().join(format!("canonkeeper-release-{name}-{}", std::process::id()));
    let _ = fs::remove_dir_all(&dir);
    fs::create_dir_all(&dir).unwrap();
    for file in files {
        fs::write(dir.join(file), format!("the installer {file}")).unwrap();
    }
    Stand { dir }
}

const ALL: [&str; 4] = [
    "CanonKeeper_1.3.0_x64-setup.exe",
    "CanonKeeper_1.3.0_amd64.deb",
    "CanonKeeper-1.3.0-1.x86_64.rpm",
    "CanonKeeper_1.3.0_amd64.AppImage",
];

/// The policy of a released app with the keys of a test.
fn released(main: &str, spare: &str) -> Policy {
    Policy {
        endpoints: vec![],
        hosts: HOSTS.iter().map(|host| host.to_string()).collect(),
        loopback: false,
        keys: Keys { main: main.into(), spare: spare.into() },
        limits: Limits::RELEASE,
    }
}

fn base() -> Url {
    Url::parse("https://github.com/canonkeeper/desktop-app/releases/download/v1.3.0/").unwrap()
}

fn expected<'a>(policy: &'a Policy) -> Expected<'a> {
    Expected { tag: "v1.3.0", shell_version: "1.3.0", configured_key: &policy.keys.main, kinds: &KINDS }
}

#[test]
fn a_release_signed_by_the_main_key_is_what_the_app_reads_and_installs() {
    let (main, spare) = (generate(), generate());
    let policy = released(&main.public, &spare.public);
    let key = SecretKey::read(&main.secret).unwrap();
    let release = stand("signed", &ALL);
    let version = Version::new(1, 3, 0);
    let written =
        sign(&release.dir, &version, &base(), "Что нового:\n— быстрее", &key, &policy, 1_789_000_000).unwrap();
    // Four signatures of the installers, the two manifests and the sums, each with its signature (AM1.1).
    assert_eq!(written.len(), 10, "{written:?}");
    assert!(written.contains(&SITE_LATEST.to_string()) && written.contains(&format!("{SITE_LATEST}.sig")));
    assert_eq!(check(&release.dir, &expected(&policy), &policy), Vec::<String>::new());
    // latest.json is what the app reads, with its signature, by the keys of the app — for each kind its file; the manifest
    // of the site the same with the file at its address of the site.
    let body = fs::read(release.dir.join(MANIFEST)).unwrap();
    let signature = fs::read_to_string(release.dir.join("latest.json.sig")).unwrap();
    let site_body = fs::read(release.dir.join(SITE_LATEST)).unwrap();
    let site_signature = fs::read_to_string(release.dir.join(format!("{SITE_LATEST}.sig"))).unwrap();
    for (kind, file) in KINDS.iter().zip(ALL) {
        let read = manifest::read(&body, &signature, &policy.keys, *kind).unwrap();
        assert_eq!(read.version, version);
        assert_eq!(read.notes, "Что нового:\n— быстрее");
        assert_eq!(read.url.as_str(), format!("{}{file}", base()));
        let bytes = fs::read(release.dir.join(file)).unwrap();
        assert_eq!(manifest::verify_file(&bytes, &read.signature, &policy.keys, &version), Ok(manifest::Signer::Main));
        let of_site = manifest::read(&site_body, &site_signature, &policy.keys, *kind).unwrap();
        assert_eq!(of_site.url.as_str(), format!("https://canonkeeper.ru/app/open/1.3.0/{file}"));
        assert_eq!(of_site.url.as_str(), format!("{SITE_FILES}1.3.0/{file}"));
        assert_eq!(
            (&of_site.version, &of_site.notes, &of_site.signature),
            (&read.version, &read.notes, &read.signature)
        );
    }
    let raw: Value = serde_json::from_slice(&body).unwrap();
    assert_eq!(raw["pub_date"], "2026-09-10T00:26:40Z");
    let site_raw: Value = serde_json::from_slice(&site_body).unwrap();
    assert_eq!(site_raw["pub_date"], raw["pub_date"]);
    // The manifest of the site is signed as its own file, and the sums name both.
    assert!(trusted_comment(&site_signature).contains(&format!("file:{SITE_LATEST}\tversion:1.3.0")));
    let sums = fs::read_to_string(release.dir.join(SUMS)).unwrap();
    for name in [SITE_LATEST.to_string(), format!("{SITE_LATEST}.sig")] {
        assert!(sums.lines().any(|line| line.ends_with(&format!("  {name}"))), "{sums}");
    }
    // A second signature into the same directory overwrites nothing.
    assert!(sign(&release.dir, &version, &base(), "", &key, &policy, 2).is_err());
}

#[test]
fn a_key_the_app_does_not_trust_signs_nothing() {
    let (main, spare, stranger) = (generate(), generate(), generate());
    let policy = released(&main.public, &spare.public);
    let release = stand("stranger", &ALL);
    let key = SecretKey::read(&stranger.secret).unwrap();
    assert!(sign(&release.dir, &Version::new(1, 3, 0), &base(), "", &key, &policy, 1).is_err());
    // Nor the spare key: a release is signed by the main key of its tree.
    let spare_key = SecretKey::read(&spare.secret).unwrap();
    assert!(sign(&release.dir, &Version::new(1, 3, 0), &base(), "", &spare_key, &policy, 1).is_err());
    assert_eq!(fs::read_dir(&release.dir).unwrap().count(), ALL.len());
    // An address the app would not take a file from, notes that are not plain text.
    let key = SecretKey::read(&main.secret).unwrap();
    let elsewhere = Url::parse("https://files.example.test/v1.3.0/").unwrap();
    assert!(sign(&release.dir, &Version::new(1, 3, 0), &elsewhere, "", &key, &policy, 1).is_err());
    assert!(sign(&release.dir, &Version::new(1, 3, 0), &base(), "\u{1b}[31m", &key, &policy, 1).is_err());
    assert_eq!(fs::read_dir(&release.dir).unwrap().count(), ALL.len());
}

#[test]
fn a_signature_without_the_version_or_of_another_one_fails_the_release() {
    let (main, spare) = (generate(), generate());
    let policy = released(&main.public, &spare.public);
    let key = SecretKey::read(&main.secret).unwrap();
    let release = stand("versions", &ALL);
    sign(&release.dir, &Version::new(1, 3, 0), &base(), "", &key, &policy, 1).unwrap();
    // The tag is not the version of the shell, nor of latest.json.
    let wrong_tag = Expected { tag: "v1.3.1", ..expected(&policy) };
    assert!(check(&release.dir, &wrong_tag, &policy).iter().any(|problem| problem.contains("the tag")));
    let later_shell = Expected { tag: "v1.4.0", shell_version: "1.4.0", ..expected(&policy) };
    let problems = check(&release.dir, &later_shell, &policy);
    assert!(problems.iter().any(|problem| problem.contains("does not hold for 1.4.0")), "{problems:?}");
    // A signature of an installer without the version, then of another version.
    let deb = release.dir.join("CanonKeeper_1.3.0_amd64.deb");
    let bytes = fs::read(&deb).unwrap();
    fs::write(release.dir.join("CanonKeeper_1.3.0_amd64.deb.sig"), key.sign(&bytes, "timestamp:1\tfile:x.deb"))
        .unwrap();
    let problems = check(&release.dir, &expected(&policy), &policy);
    assert!(problems.iter().any(|problem| problem.contains("NoVersion")), "{problems:?}");
    fs::write(release.dir.join("CanonKeeper_1.3.0_amd64.deb.sig"), key.sign(&bytes, &trusted("x.deb", "1.2.0", 1)))
        .unwrap();
    let problems = check(&release.dir, &expected(&policy), &policy);
    assert!(problems.iter().any(|problem| problem.contains("OtherVersion")), "{problems:?}");
}

#[test]
fn a_release_without_a_kind_or_with_a_file_for_no_kind_fails() {
    let (main, spare) = (generate(), generate());
    let policy = released(&main.public, &spare.public);
    let key = SecretKey::read(&main.secret).unwrap();
    let release = stand("kinds", &ALL[1..]);
    sign(&release.dir, &Version::new(1, 3, 0), &base(), "", &key, &policy, 1).unwrap();
    let problems = check(&release.dir, &expected(&policy), &policy);
    assert!(problems.iter().any(|problem| problem.contains("one of each")), "{problems:?}");
    // The smoke run carries the kinds of its system.
    let linux = Expected { kinds: &KINDS[1..], ..expected(&policy) };
    assert_eq!(check(&release.dir, &linux, &policy), Vec::<String>::new());
    // A file changed after the signature, and one SHA256SUMS does not name.
    fs::write(release.dir.join("CanonKeeper_1.3.0_amd64.deb"), "another").unwrap();
    fs::write(release.dir.join("notes.txt"), "x").unwrap();
    let problems = check(&release.dir, &linux, &policy);
    assert!(problems.iter().any(|problem| problem.contains("Signature")), "{problems:?}");
    assert!(problems.iter().any(|problem| problem.contains("is not the file it names")), "{problems:?}");
    assert!(problems.iter().any(|problem| problem.contains("does not name notes.txt")), "{problems:?}");
    // Two installers of one kind.
    let two = stand("two", &["a_amd64.deb", "b_amd64.deb"]);
    assert!(installers(&two.dir).is_err());
}

#[test]
fn the_keys_of_development_and_a_key_out_of_tune_fail_the_release() {
    let release = stand("development", &ALL);
    // The tree as it is: its main key is a key of development, whose secret half is nowhere.
    let tree = released(MAIN_KEY, SPARE_KEY);
    let problems = check(&release.dir, &expected(&tree), &tree);
    assert!(problems.iter().any(|problem| problem.contains("key of development")), "{problems:?}");
    // The key of tauri.conf.json is not the one of the code; the spare key is the main one or not a key.
    let (main, spare) = (generate(), generate());
    let policy = released(&main.public, &spare.public);
    let out_of_tune = Expected { configured_key: &spare.public, ..expected(&policy) };
    assert!(check(&release.dir, &out_of_tune, &policy).iter().any(|problem| problem.contains("tauri.conf.json")));
    let same = released(&main.public, &main.public);
    assert!(check(&release.dir, &expected(&same), &same)
        .iter()
        .any(|problem| problem.contains("spare key is the main")));
    let broken = released(&main.public, "not a key");
    assert!(check(&release.dir, &expected(&broken), &broken)
        .iter()
        .any(|problem| problem.contains("spare key is not")));
}

#[test]
fn the_program_of_a_package_carries_the_mark_of_its_kind_and_no_mark_of_smoke() {
    let names = |marked: &str| -> Vec<u8> {
        // A program names every mark it reads; the bundler patched the one of the variable.
        let mut program =
            b"\0__TAURI_BUNDLE_TYPE_VAR_DEB\0__TAURI_BUNDLE_TYPE_VAR_RPM\0__TAURI_BUNDLE_TYPE_VAR_APP\0".to_vec();
        program.extend_from_slice(b"__TAURI_BUNDLE_TYPE_VAR_MSI\0__TAURI_BUNDLE_TYPE_VAR_NSS\0");
        program.extend_from_slice(format!("__TAURI_BUNDLE_TYPE_VAR_{marked}\0").as_bytes());
        program
    };
    assert_eq!(marks(&names("DEB"), Kind::Deb), Vec::<String>::new());
    assert_eq!(marks(&names("NSS"), Kind::Nsis), Vec::<String>::new());
    assert!(!marks(&names("RPM"), Kind::Deb).is_empty());
    assert!(!marks(&names("UNK"), Kind::AppImage).is_empty());
    // The comparisons of a program that keeps no literal: the patched mark alone.
    assert_eq!(marks(b"..__TAURI_BUNDLE_TYPE_VAR_APP..", Kind::AppImage), Vec::<String>::new());
    let mut smoke = names("DEB");
    smoke.extend_from_slice(SMOKE_MARK.as_bytes());
    assert!(marks(&smoke, Kind::Deb).iter().any(|problem| problem.contains("smoke")));
}

#[test]
fn the_kind_of_an_installer_is_the_end_of_its_name() {
    for (kind, file) in KINDS.iter().zip(ALL) {
        assert_eq!(kind_of(file), Some(*kind), "{file}");
    }
    assert_eq!(kind_of("CanonKeeper_1.3.0_x64_en-US.msi"), None);
    assert_eq!(rfc3339(0), "1970-01-01T00:00:00Z");
    assert_eq!(rfc3339(951_782_400), "2000-02-29T00:00:00Z");
}

#[test]
fn the_smoke_build_signs_and_checks_its_release_on_the_loopback_with_the_key_of_its_run() {
    // UB3.7: the smoke run signs N+1 with the one signer, by the key of one run in both places of its policy, for the
    // installers of its system, at the source on the loopback.
    let run = generate();
    let policy = Policy::loopback(
        vec!["http://127.0.0.1:47613/latest.json".into()],
        Keys { main: run.public.clone(), spare: run.public.clone() },
    );
    let key = SecretKey::read(&run.secret).unwrap();
    let release = stand("smoke", &ALL[1..]);
    let base = Url::parse("http://127.0.0.1:47613/").unwrap();
    let written = sign(&release.dir, &Version::new(1, 3, 0), &base, "", &key, &policy, 1).unwrap();
    // The loopback has no site: no manifest of the site is written, none is required (AM1.1, AM1.2).
    assert!(!written.iter().any(|name| name.starts_with(SITE_LATEST)), "{written:?}");
    assert!(!release.dir.join(SITE_LATEST).exists());
    let linux = Expected { tag: "v1.3.0", shell_version: "1.3.0", configured_key: &run.public, kinds: &KINDS[1..] };
    assert_eq!(check(&release.dir, &linux, &policy), Vec::<String>::new());
    // A released app takes no file from the loopback, and its release carries the manifest of the site.
    let released = released(&run.public, &generate().public);
    let problems = check(&release.dir, &linux, &released);
    assert!(problems.iter().any(|problem| problem.contains("takes no file")), "{problems:?}");
    assert!(problems.contains(&format!("no {SITE_LATEST}")), "{problems:?}");
    // A manifest of the site in a release on the loopback is refused.
    fs::write(release.dir.join(SITE_LATEST), "{}").unwrap();
    let problems = check(&release.dir, &linux, &policy);
    assert!(problems.iter().any(|problem| problem.contains("carries no latest-site.json")), "{problems:?}");
}

/// The trusted comment of a signature, the base64 of its text.
fn trusted_comment(signature: &str) -> String {
    let text = String::from_utf8(STANDARD.decode(signature.trim()).unwrap()).unwrap();
    text.lines().find_map(|line| line.strip_prefix("trusted comment: ")).unwrap().to_string()
}

/// Rewrites the manifest of the site of `release` by `change` and signs it again by `key` for `signed` — a manifest the
/// main key signed, which only its addresses or its version can refuse.
fn resign_site(release: &Stand, key: &SecretKey, signed: &str, change: impl Fn(&mut Value)) {
    let mut raw: Value = serde_json::from_slice(&fs::read(release.dir.join(SITE_LATEST)).unwrap()).unwrap();
    change(&mut raw);
    let body = serde_json::to_vec_pretty(&raw).unwrap();
    fs::write(release.dir.join(format!("{SITE_LATEST}.sig")), key.sign(&body, &trusted(SITE_LATEST, signed, 1)))
        .unwrap();
    fs::write(release.dir.join(SITE_LATEST), body).unwrap();
}

#[test]
fn the_manifest_of_the_site_names_every_file_exactly_at_its_address_of_the_site() {
    // AM1.2: a manifest of the site signed by the main key is still refused when a file is not exactly at
    // SITE_FILES + version + "/" + its name — another path, another host, another version —, and a release without it.
    let (main, spare) = (generate(), generate());
    let policy = released(&main.public, &spare.public);
    let key = SecretKey::read(&main.secret).unwrap();
    let cases = [
        ("another path", "https://canonkeeper.ru/app/open/1.3.0/x/CanonKeeper_1.3.0_amd64.deb"),
        (
            "the address of GitHub",
            "https://github.com/canonkeeper/desktop-app/releases/download/v1.3.0/CanonKeeper_1.3.0_amd64.deb",
        ),
        ("another host", "https://mirror.example.test/app/open/1.3.0/CanonKeeper_1.3.0_amd64.deb"),
        ("a query", "https://canonkeeper.ru/app/open/1.3.0/CanonKeeper_1.3.0_amd64.deb?v=2"),
        ("another version", "https://canonkeeper.ru/app/open/1.2.0/CanonKeeper_1.3.0_amd64.deb"),
    ];
    for (what, url) in cases {
        let release = stand(&format!("site-{}", what.replace(' ', "-")), &ALL);
        sign(&release.dir, &Version::new(1, 3, 0), &base(), "", &key, &policy, 1).unwrap();
        resign_site(&release, &key, "1.3.0", |raw| raw["platforms"]["linux-x86_64-deb"]["url"] = url.into());
        let problems = check(&release.dir, &expected(&policy), &policy);
        let at = format!("{SITE_LATEST}: the file of deb is not at {SITE_FILES}1.3.0/CanonKeeper_1.3.0_amd64.deb");
        assert!(problems.contains(&at), "{what}: {problems:?}");
    }
    // The manifest of the site carries for deb the signature of another installer: the signatures of the kinds are those of
    // their .sig, as in latest.json.
    let release = stand("site-other-signature", &ALL);
    sign(&release.dir, &Version::new(1, 3, 0), &base(), "", &key, &policy, 1).unwrap();
    resign_site(&release, &key, "1.3.0", |raw| {
        raw["platforms"]["linux-x86_64-deb"]["signature"] = raw["platforms"]["linux-x86_64-rpm"]["signature"].clone()
    });
    let problems = check(&release.dir, &expected(&policy), &policy);
    let other = format!("{SITE_LATEST}: the signature of deb is not CanonKeeper_1.3.0_amd64.deb.sig");
    assert!(problems.contains(&other), "{problems:?}");
    // The manifest of the site names another version, signed for it.
    let release = stand("site-other-version", &ALL);
    sign(&release.dir, &Version::new(1, 3, 0), &base(), "", &key, &policy, 1).unwrap();
    resign_site(&release, &key, "1.2.0", |raw| raw["version"] = "1.2.0".into());
    let problems = check(&release.dir, &expected(&policy), &policy);
    assert!(problems.contains(&format!("{SITE_LATEST} names 1.2.0, not 1.3.0")), "{problems:?}");
    // The check names the manifest of the site in its every word, never latest.json.
    assert!(problems.iter().all(|problem| !problem.starts_with(MANIFEST)), "{problems:?}");
    // No manifest of the site: the release is refused.
    let release = stand("site-missing", &ALL);
    sign(&release.dir, &Version::new(1, 3, 0), &base(), "", &key, &policy, 1).unwrap();
    fs::remove_file(release.dir.join(SITE_LATEST)).unwrap();
    let problems = check(&release.dir, &expected(&policy), &policy);
    assert!(problems.contains(&format!("no {SITE_LATEST}")), "{problems:?}");
}

/// A package of `kind` as the step of the marks unpacks it (UB3.3; specs/2026-11-15-desk-ci-shell.md CS2): the shell, the
/// backend and the bridge, the shell marked as its kind — in `usr/bin` of a package of Linux, at the root of the files of
/// the installer of Windows, named as Windows lets them be.
fn unpacked(name: &str, kind: Kind) -> Stand {
    let package = stand(name, &[]);
    let (bin, names, mark) = match kind {
        Kind::Nsis => {
            (package.dir.clone(), ["CanonKeeper.exe", "canonkeeper-backend.exe", "canonkeeper-mcp.exe"], "NSS")
        }
        _ => (
            package.dir.join("usr").join("bin"),
            ["canonkeeper", "canonkeeper-backend", "canonkeeper-mcp"],
            match kind {
                Kind::Deb => "DEB",
                Kind::Rpm => "RPM",
                _ => "APP",
            },
        ),
    };
    fs::create_dir_all(&bin).unwrap();
    fs::write(bin.join(names[0]), format!("..__TAURI_BUNDLE_TYPE_VAR_{mark}..")).unwrap();
    for program in &names[1..] {
        fs::write(bin.join(program), "a program of the package").unwrap();
    }
    package
}

#[test]
fn the_three_programs_of_each_package_are_found_where_its_kind_puts_them_and_a_missing_one_is_named() {
    for kind in KINDS {
        let package = unpacked(&format!("programs-{}", kind.name()), kind);
        let found = programs(&package.dir, kind).unwrap();
        let names: Vec<String> =
            found.iter().map(|path| path.file_name().unwrap().to_string_lossy().to_lowercase()).collect();
        let exe = if kind == Kind::Nsis { ".exe" } else { "" };
        assert_eq!(
            names,
            [format!("canonkeeper{exe}"), format!("canonkeeper-backend{exe}"), format!("canonkeeper-mcp{exe}")]
        );
        let bridge = found[2].clone();
        fs::remove_file(&bridge).unwrap();
        let refused = programs(&package.dir, kind).unwrap_err();
        assert!(refused.contains(&format!("the {} package", kind.name())), "{refused}");
        assert!(refused.contains(&format!("canonkeeper-mcp{exe}")), "{refused}");
    }
}

#[test]
fn the_step_of_the_marks_passes_a_package_and_refuses_one_without_the_bridge_naming_both() {
    // The command the step of CI runs on each unpacked package: its word names the package and, in a refusal, the program.
    let tool = env!("CARGO_BIN_EXE_canonkeeper-release");
    let package = unpacked("step-rpm", Kind::Rpm);
    let passed = std::process::Command::new(tool).args(["marks", "--kind", "rpm"]).arg(&package.dir).output().unwrap();
    assert!(passed.status.success(), "{}", String::from_utf8_lossy(&passed.stderr));
    let said = String::from_utf8_lossy(&passed.stdout);
    assert!(said.contains("the rpm package") && said.contains("is marked as rpm"), "{said}");

    fs::remove_file(package.dir.join("usr").join("bin").join("canonkeeper-mcp")).unwrap();
    let refused = std::process::Command::new(tool).args(["marks", "--kind", "rpm"]).arg(&package.dir).output().unwrap();
    assert_eq!(refused.status.code(), Some(1));
    let said = String::from_utf8_lossy(&refused.stderr);
    assert!(said.contains("the rpm package has no canonkeeper-mcp"), "{said}");

    // A shell marked as another kind is named with its package too.
    let deb = unpacked("step-deb-as-rpm", Kind::Deb);
    let refused = std::process::Command::new(tool).args(["marks", "--kind", "rpm"]).arg(&deb.dir).output().unwrap();
    let said = String::from_utf8_lossy(&refused.stderr);
    assert!(
        !refused.status.success() && said.contains("the rpm package: ") && said.contains("not marked as rpm"),
        "{said}"
    );
}
