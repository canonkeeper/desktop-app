// SPDX-License-Identifier: AGPL-3.0-only

//! Where the app asks for a new version and whom it trusts (specs/2026-11-13-desk-updates.md UB1.3, UB1.6, UB3.4,
//! UB3.7): a constant list in the code. The addresses name the edition in their path — another build has its own
//! addresses and its own key. A request goes only over `https` and only to the hosts of the list, every step of a
//! redirect too; nothing of the environment of a run changes an address, a host or a key. The smoke build of CI is its
//! own feature of compilation with a source on the loopback and a key of one run given when it is built.

use std::time::Duration;

use url::{Host, Url};

use super::manifest::Keys;

/// `latest.json` of the open app on the site, then in the releases of its repository; the signature of each lies next to
/// it as `latest.json.sig`.
pub const SITE_MANIFEST: &str = "https://canonkeeper.ru/app/open/latest.json";
pub const RELEASES_MANIFEST: &str = "https://github.com/canonkeeper/desktop-app/releases/latest/download/latest.json";

/// Where the site serves the files of a release it mirrors (specs/2026-11-16-desk-app-mirror.md AM1.1): the file of a
/// version is `SITE_FILES` + version + `/` + its name, as the manifest of the site a release signs names it.
pub const SITE_FILES: &str = "https://canonkeeper.ru/app/open/";

/// The hosts a request of an update may reach (UB1.6): the site, GitHub, and the host GitHub serves the files of
/// releases from after its redirect. «Настройки» name them too: backend/internal/app/desktop/state.go owns the list of
/// the network (`networkUses`), backend/internal/app/desktop/updates.go its lines of these hosts.
pub const HOSTS: [&str; 3] = ["canonkeeper.ru", "github.com", "release-assets.githubusercontent.com"];

/// The keys of the app (UB3.4): the main key signs every release; the spare one signs the version after a loss or a leak
/// of the main one, which then names a new spare key. Both are the base64 of the text of a minisign public key; the main
/// one is also `plugins.updater.pubkey` of tauri.conf.json. These are the owner's keys (main AB76BE2033FDF3BD, spare
/// 7C58C3FAE72B4090), made by `canonkeeper-release keygen`; their secret halves are only the owner's.
pub const MAIN_KEY: &str = "dW50cnVzdGVkIGNvbW1lbnQ6IG1pbmlzaWduIHB1YmxpYyBrZXk6IEFCNzZCRTIwMzNGREYzQkQKUldTOTgvMHpJTDUycTlZZUxXOHpwb2VCU2dIcFBlM21tQXFhdkpZTFpIKzAxR3VLZGJjTklNeisK";
pub const SPARE_KEY: &str = "dW50cnVzdGVkIGNvbW1lbnQ6IG1pbmlzaWduIHB1YmxpYyBrZXk6IDdDNThDM0ZBRTcyQjQwOTAKUldTUVFDdm4rc05ZZkprUlpkNkxGMkNQUWRVRW9VSmVjN0xoN3pEeUF6czM3N2Qra0dMTmhkOUUK";

/// The keys of development: their pairs were made by `update::sign::generate` and the secret halves dropped at once, kept
/// nowhere, so no release can be signed for them; the
/// release check refuses a tree whose main key is one of them (UB3.3, UB3.4). The owner's keys replace `MAIN_KEY` and
/// `SPARE_KEY`; this list stays.
pub const DEVELOPMENT_KEYS: [&str; 2] = [
    "dW50cnVzdGVkIGNvbW1lbnQ6IG1pbmlzaWduIHB1YmxpYyBrZXk6IDM1NUMxREVCMUI0ODQzMkMKUldRc1EwZ2I2eDFjTllJb2FLTTRCT3UwZHNtMkFxODlJTTNzTDFQTXVQUlhTZi84ek16Z1lHWS8K",
    "dW50cnVzdGVkIGNvbW1lbnQ6IG1pbmlzaWduIHB1YmxpYyBrZXk6IDg3NEY4MERFRTFGMzhFOTIKUldTU2p2UGgzb0JQaC8waTlnZ3FKS0lGOWFHektXWC9mRVJPOVRDbzVhd1I1MFJqQ0RrK3RVSjUK",
];

/// The bounds of the network of an update (UB1.3, UB1.6).
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Limits {
    /// The largest answer of an address: `latest.json` or its signature.
    pub answer: u64,
    /// How long a connection may take, and an answer of an address in all.
    pub connect: Duration,
    pub wait: Duration,
    /// The largest file of a release, and how long its download may stand still; it has no limit in all.
    pub file: u64,
    pub idle: Duration,
}

impl Limits {
    pub const RELEASE: Limits = Limits {
        answer: 64 * 1024,
        connect: Duration::from_secs(10),
        wait: Duration::from_secs(30),
        file: 1 << 30,
        idle: Duration::from_secs(60),
    };
}

/// The addresses, the hosts and the keys of an update.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Policy {
    pub endpoints: Vec<String>,
    pub hosts: Vec<String>,
    /// Only a test or the smoke build: `http` to 127.0.0.1, and no proxy of the system.
    pub loopback: bool,
    pub keys: Keys,
    pub limits: Limits,
}

impl Policy {
    /// The policy of the app as it is released.
    #[cfg(not(feature = "smoke"))]
    pub fn release() -> Policy {
        Policy {
            endpoints: vec![SITE_MANIFEST.to_string(), RELEASES_MANIFEST.to_string()],
            hosts: HOSTS.iter().map(|host| host.to_string()).collect(),
            loopback: false,
            keys: Keys { main: MAIN_KEY.to_string(), spare: SPARE_KEY.to_string() },
            limits: Limits::RELEASE,
        }
    }

    /// The smoke build of CI (UB3.7): the source of versions on the loopback and the key of one run, both given when it
    /// is built.
    #[cfg(feature = "smoke")]
    pub fn release() -> Policy {
        // The program of the smoke build carries its mark, which the release check refuses (UB3.3).
        let _ = std::hint::black_box(SMOKE_MARK);
        let key = env!("CANONKEEPER_SMOKE_UPDATE_KEY").to_string();
        Policy::loopback(
            vec![env!("CANONKEEPER_SMOKE_UPDATE_SOURCE").to_string()],
            Keys { main: key.clone(), spare: key },
        )
    }

    /// A source on the loopback over `http`: the tests and the smoke build.
    pub fn loopback(endpoints: Vec<String>, keys: Keys) -> Policy {
        Policy { endpoints, hosts: vec!["127.0.0.1".to_string()], loopback: true, keys, limits: Limits::RELEASE }
    }

    /// Whether a request may go to `url` (UB1.6): `https` to a host of the list on its own port, without a user in the
    /// address; on the loopback policy `http` to 127.0.0.1.
    pub fn allowed(&self, url: &Url) -> bool {
        if !url.username().is_empty() || url.password().is_some() {
            return false;
        }
        match (url.scheme(), url.host()) {
            ("https", Some(Host::Domain(host))) => {
                !self.loopback && url.port().is_none() && self.hosts.iter().any(|allowed| allowed == host)
            }
            ("http", Some(Host::Ipv4(address))) => self.loopback && address.is_loopback(),
            _ => false,
        }
    }

    /// The signature of `latest.json` of an address: next to it.
    pub fn signature_of(endpoint: &str) -> String {
        format!("{endpoint}.sig")
    }
}

/// The mark of the smoke build in its program: the release check refuses a program that carries it (UB3.3). Only the
/// policy of the smoke build names it, so no other program of the app carries its bytes.
pub const SMOKE_MARK: &str = "CANONKEEPER_SMOKE_BUILD_OF_UPDATES";

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_request_goes_only_over_https_to_the_hosts_of_the_list() {
        let policy = Policy {
            endpoints: vec![],
            hosts: HOSTS.iter().map(|host| host.to_string()).collect(),
            loopback: false,
            keys: Keys { main: MAIN_KEY.into(), spare: SPARE_KEY.into() },
            limits: Limits::RELEASE,
        };
        for allowed in [
            SITE_MANIFEST,
            RELEASES_MANIFEST,
            "https://release-assets.githubusercontent.com/github-production-release-asset/1/2?sp=r",
        ] {
            assert!(policy.allowed(&Url::parse(allowed).unwrap()), "{allowed}");
        }
        for refused in [
            "http://canonkeeper.ru/app/open/latest.json",
            "https://canonkeeper.ru:8443/app/open/latest.json",
            "https://evil.example.test/latest.json",
            "https://canonkeeper.ru.example.test/latest.json",
            "https://objects.githubusercontent.com/x",
            "http://127.0.0.1:8000/latest.json",
            "ftp://github.com/x",
            "file:///etc/passwd",
        ] {
            assert!(!policy.allowed(&Url::parse(refused).unwrap()), "{refused}");
        }
        // A user in the address of a host of the list.
        let mut with_user = Url::parse(SITE_MANIFEST).unwrap();
        with_user.set_username("reader").unwrap();
        assert!(!policy.allowed(&with_user));
        let loopback = Policy::loopback(vec![], policy.keys.clone());
        assert!(loopback.allowed(&Url::parse("http://127.0.0.1:8000/latest.json").unwrap()));
        assert!(!loopback.allowed(&Url::parse("http://localhost:8000/latest.json").unwrap()));
        assert!(!loopback.allowed(&Url::parse("http://10.0.0.1:8000/latest.json").unwrap()));
        assert!(!loopback.allowed(&Url::parse(SITE_MANIFEST).unwrap()));
    }

    #[cfg(not(feature = "smoke"))]
    #[test]
    fn the_released_policy_is_the_site_then_the_releases_with_the_keys_of_the_app() {
        let policy = Policy::release();
        assert_eq!(policy.endpoints, [SITE_MANIFEST, RELEASES_MANIFEST]);
        assert!(policy
            .endpoints
            .iter()
            .all(|endpoint| endpoint.contains("/open/") || endpoint.contains("/desktop-app/")));
        assert!(!policy.loopback);
        assert_eq!((policy.keys.main.as_str(), policy.keys.spare.as_str()), (MAIN_KEY, SPARE_KEY));
        assert_eq!(Policy::signature_of(SITE_MANIFEST), "https://canonkeeper.ru/app/open/latest.json.sig");
    }
}
