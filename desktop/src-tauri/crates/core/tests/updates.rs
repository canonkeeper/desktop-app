// SPDX-License-Identifier: AGPL-3.0-only

//! The network of an update on a stand-in source (specs/2026-11-13-desk-updates.md UB1.3–UB1.6, UB2.2): the walk over the
//! addresses, the signature of `latest.json`, the hosts of a download and of its redirects, the signature of a file.

mod source;

use std::sync::atomic::AtomicBool;
use std::time::{Duration, Instant};

use canonkeeper_desktop_core::update::fetch::{Checked, Network, NotDownloaded, CLIENT};
use canonkeeper_desktop_core::update::policy::{Limits, Policy};
use canonkeeper_desktop_core::update::Kind;
use semver::Version;
use source::{keys, latest, publish, Reply, Signer, Source};

const DEB: &str = "linux-x86_64-deb";

fn policy(endpoints: Vec<String>, main: &Signer, spare: &Signer) -> Policy {
    let mut policy = Policy::loopback(endpoints, keys(main, spare));
    policy.limits = Limits {
        answer: 64 * 1024,
        connect: Duration::from_secs(2),
        wait: Duration::from_millis(400),
        file: 1 << 20,
        idle: Duration::from_secs(2),
    };
    policy
}

fn found(checked: Checked) -> Version {
    match checked {
        Checked::Found(release) => release.version,
        Checked::NoAnswer => panic!("no address answered"),
    }
}

#[test]
fn an_address_that_does_not_answer_with_a_signed_release_sends_the_walk_to_the_next() {
    let (main, spare, stranger) = (Signer::new(), Signer::new(), Signer::new());
    let source = Source::start();
    publish(&source, "/second", "1.3.0", DEB, b"the package", &main);
    let first = || source.at("/first/latest.json");
    let walk = || Network::new(policy(vec![first(), source.at("/second/latest.json")], &main, &spare)).check(Kind::Deb);

    // The site answers every path it does not know with its page and the code 200.
    source.route("/first/latest.json", Reply::Body(b"<!doctype html><title>CanonKeeper</title>".to_vec(), "text/html"));
    source.route("/first/latest.json.sig", Reply::Body(b"<!doctype html>".to_vec(), "text/html"));
    assert_eq!(found(walk()), Version::new(1, 3, 0));

    // Silent longer than the time of an answer.
    source.route("/first/latest.json", Reply::Silent(Duration::from_secs(3)));
    let began = Instant::now();
    assert_eq!(found(walk()), Version::new(1, 3, 0));
    assert!(began.elapsed() < Duration::from_secs(2), "the silent address held the walk for {:?}", began.elapsed());

    // 70 KiB of a JSON object, signed: longer than an answer may be.
    let mut long = latest("1.4.0", DEB, &source.at("/second/file-1.3.0"), "c2ln");
    long.truncate(long.len() - 1);
    long.extend_from_slice(format!(r#","padding":"{}"}}"#, "а".repeat(35 * 1024)).as_bytes());
    let signature = main.sign(&long, "latest.json", "1.4.0");
    source.route("/first/latest.json", Reply::Body(long, "application/json"));
    source.route("/first/latest.json.sig", Reply::Body(signature.into_bytes(), "text/plain"));
    assert_eq!(found(walk()), Version::new(1, 3, 0));

    // Without a signature, with a signature of another key.
    let body = latest("9.9.9", DEB, &source.at("/second/file-1.3.0"), &main.sign(b"x", "file", "9.9.9"));
    source.route("/first/latest.json", Reply::Body(body.clone(), "application/json"));
    source.route("/first/latest.json.sig", Reply::Status(404));
    assert_eq!(found(walk()), Version::new(1, 3, 0));
    let foreign = stranger.sign(&body, "latest.json", "9.9.9");
    source.route("/first/latest.json.sig", Reply::Body(foreign.into_bytes(), "text/plain"));
    assert_eq!(found(walk()), Version::new(1, 3, 0));

    // A release without the file of this kind of installation.
    publish(&source, "/first", "9.9.9", "linux-x86_64-rpm", b"x", &main);
    assert_eq!(found(walk()), Version::new(1, 3, 0));

    // Neither answers: «нет связи».
    source.route("/second/latest.json", Reply::Status(503));
    assert_eq!(walk(), Checked::NoAnswer);
    let nowhere = Network::new(policy(vec!["http://127.0.0.1:9/latest.json".into()], &main, &spare)).check(Kind::Deb);
    assert_eq!(nowhere, Checked::NoAnswer);

    // The spare key of the app is a key of the app.
    publish(&source, "/second", "1.3.1", DEB, b"the package", &spare);
    assert_eq!(found(walk()), Version::new(1, 3, 1));
}

#[test]
fn a_request_says_nothing_but_the_name_of_the_client_and_goes_nowhere_else() {
    let (main, spare) = (Signer::new(), Signer::new());
    let source = Source::start();
    publish(&source, "/site", "1.3.0", DEB, b"the package", &main);
    let network = Network::new(policy(vec![source.at("/site/latest.json")], &main, &spare));
    let Checked::Found(release) = network.check(Kind::Deb) else { panic!("no release") };
    let downloaded = network.download(&release, &AtomicBool::new(false), |_| {}).unwrap();
    assert_eq!(*downloaded.bytes, b"the package");
    let requests = source.requests();
    assert_eq!(
        requests.iter().map(|request| request.path.as_str()).collect::<Vec<_>>(),
        ["/site/latest.json", "/site/latest.json.sig", "/site/file-1.3.0"]
    );
    for request in &requests {
        // The address is the constant one, without a query; the headers are the host, the client and what it accepts.
        let mut names: Vec<&str> = request.headers.iter().map(|(name, _)| name.as_str()).collect();
        names.sort();
        assert_eq!(names, ["accept", "host", "user-agent"], "{}", request.path);
        let agent = request.headers.iter().find(|(name, _)| name == "user-agent").unwrap();
        assert_eq!(agent.1, CLIENT);
    }

    // The file of a release on another host, and a redirect to another host: no request goes there.
    let mut elsewhere = release.clone();
    elsewhere.url = url::Url::parse(&source.at("/site/file-1.3.0").replace("127.0.0.1", "localhost")).unwrap();
    let before = source.requests().len();
    assert_eq!(network.download(&elsewhere, &AtomicBool::new(false), |_| {}).unwrap_err(), NotDownloaded::Failed);
    assert_eq!(source.requests().len(), before, "the file of another host was asked for");
    let redirect = source.at("/elsewhere").replace("127.0.0.1", "localhost");
    source.route("/site/file-1.3.0", Reply::Redirect(redirect));
    assert_eq!(network.download(&release, &AtomicBool::new(false), |_| {}).unwrap_err(), NotDownloaded::Failed);
    assert!(!source.asked("/elsewhere"), "a redirect went to another host");
}

#[test]
fn a_file_its_signature_does_not_prove_for_its_version_and_by_a_key_of_the_app_is_refused() {
    let (main, spare, stranger) = (Signer::new(), Signer::new(), Signer::new());
    let source = Source::start();
    let network = Network::new(policy(vec![source.at("/site/latest.json")], &main, &spare));
    let check = || match network.check(Kind::Deb) {
        Checked::Found(release) => release,
        Checked::NoAnswer => panic!("no release"),
    };
    // The file's signature names another version.
    source.route("/site/file", Reply::Body(b"the package".to_vec(), "application/octet-stream"));
    let publish_file = |signature: String| {
        let body = latest("1.3.0", DEB, &source.at("/site/file"), &signature);
        let signed = main.sign(&body, "latest.json", "1.3.0");
        source.route("/site/latest.json", Reply::Body(body, "application/json"));
        source.route("/site/latest.json.sig", Reply::Body(signed.into_bytes(), "text/plain"));
    };
    publish_file(main.sign(b"the package", "file", "1.2.0"));
    assert_eq!(network.download(&check(), &AtomicBool::new(false), |_| {}).unwrap_err(), NotDownloaded::Refused);
    // A key the app does not trust: refused before anything is downloaded.
    publish_file(stranger.sign(b"the package", "file", "1.3.0"));
    let before = source.requests().len();
    assert_eq!(network.download(&check(), &AtomicBool::new(false), |_| {}).unwrap_err(), NotDownloaded::Refused);
    assert_eq!(source.requests().len(), before + 2, "the file of a stranger's key was downloaded");
    // Other bytes than were signed.
    publish_file(main.sign(b"another package", "file", "1.3.0"));
    assert_eq!(network.download(&check(), &AtomicBool::new(false), |_| {}).unwrap_err(), NotDownloaded::Refused);
    // A file larger than a file may be.
    source.route("/site/file", Reply::Body(vec![0u8; (1 << 20) + 1], "application/octet-stream"));
    publish_file(main.sign(&vec![0u8; (1 << 20) + 1], "file", "1.3.0"));
    assert_eq!(network.download(&check(), &AtomicBool::new(false), |_| {}).unwrap_err(), NotDownloaded::Failed);
    // The spare key signs a file too, and a cancel stops a download.
    source.route("/site/file", Reply::Body(b"the package".to_vec(), "application/octet-stream"));
    publish_file(spare.sign(b"the package", "file", "1.3.0"));
    assert!(network.download(&check(), &AtomicBool::new(false), |_| {}).is_ok());
    assert_eq!(network.download(&check(), &AtomicBool::new(true), |_| {}).unwrap_err(), NotDownloaded::Canceled);
}
