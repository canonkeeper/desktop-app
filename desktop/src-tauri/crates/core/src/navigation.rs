// SPDX-License-Identifier: AGPL-3.0-only

//! Where the window may go (specs/2026-11-12-desk-shell.md SH3.1; specs/2026-11-07-desktop-app.md DA3.7): the pages of
//! its own backend at `http://127.0.0.1:P/…` and the page the shell carries; `https:` and `mailto:` go to the browser
//! of the system; anything else is refused. And the addresses the shell itself opens: the page that exchanges the
//! one-time code for the session of the window (DA3.2, DA3.3), the starting page and the failure page.

use std::net::Ipv4Addr;
use std::path::{Path, PathBuf};

use url::{Host, Url};

use crate::control::Code;
use crate::paths::System;

/// What happens to a navigation of the window.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Navigation {
    /// The window goes there: a page of its backend or of the shell.
    Window,
    /// The browser of the system opens this address; the window stays.
    Browser(String),
    /// The page saves a file it made — «Скачать книгу…», the file of a chapter — through a `blob:` address of its own
    /// origin (src/ui/save-file.ts): the webview saves it into the folder of downloads and the window stays.
    Save,
    /// Nothing happens.
    Refuse,
}

/// The path of the page that exchanges the code; the web serves its index there.
pub const OPEN_PATH: &str = "/desktop/open";

/// Decides a navigation of the window to `target`; `port` is the port of the running backend, if any.
pub fn navigation(target: &str, port: Option<u16>, system: System) -> Navigation {
    let Ok(url) = Url::parse(target) else {
        return Navigation::Refuse;
    };
    if is_own(&url, port) || is_bundled(&url, system) {
        return Navigation::Window;
    }
    if is_own_blob(&url, port) {
        return Navigation::Save;
    }
    match external(target) {
        Some(address) => Navigation::Browser(address),
        None => Navigation::Refuse,
    }
}

/// The address the browser of the system may open (`open_external`, SH3.2): only `https:` to a named host and
/// `mailto:`; an address by IP — the loopback of the backend among them — is not a link of the app.
pub fn external(target: &str) -> Option<String> {
    let url = Url::parse(target).ok()?;
    match url.scheme() {
        "https" if matches!(url.host(), Some(Host::Domain(_))) => Some(url.into()),
        "mailto" if !url.path().is_empty() => Some(url.into()),
        _ => None,
    }
}

fn is_own(url: &Url, port: Option<u16>) -> bool {
    port.is_some()
        && url.scheme() == "http"
        && url.host() == Some(Host::Ipv4(Ipv4Addr::LOCALHOST))
        && url.port() == port
        && url.username().is_empty()
        && url.password().is_none()
}

/// A `blob:` address the page of the backend made: its origin is the origin of the page.
fn is_own_blob(url: &Url, port: Option<u16>) -> bool {
    url.scheme() == "blob" && Url::parse(url.path()).is_ok_and(|inner| is_own(&inner, port))
}

/// Whether the window saves a file from `target`: only what the page of its backend made or answered (DT0.2 — the
/// book and the chapter leave the app as files); a download from anywhere else is refused.
pub fn saves(target: &str, port: Option<u16>) -> bool {
    Url::parse(target).is_ok_and(|url| is_own(&url, port) || is_own_blob(&url, port))
}

/// Where a file the page saves lands on Linux: where the webview proposes when the system names a folder of downloads
/// that exists — the webview chose a free name in it — and otherwise the home folder under a free name: WebKitGTK alone
/// would save nothing then, and the window would say nothing.
pub fn save_path(proposed: &Path, downloads: Option<&Path>, home: Option<&Path>) -> PathBuf {
    if downloads.is_some_and(Path::is_dir) {
        return proposed.to_path_buf();
    }
    match (home.filter(|home| home.is_dir()), proposed.file_name().and_then(|name| name.to_str())) {
        (Some(home), Some(name)) => free(home, name),
        _ => proposed.to_path_buf(),
    }
}

/// `name` in `directory`, or `name (1)`, `name (2)`… before its extension, as the webviews number a taken name.
fn free(directory: &Path, name: &str) -> PathBuf {
    let (stem, extension) = match name.split_once('.') {
        Some((stem, extension)) if !stem.is_empty() => (stem, format!(".{extension}")),
        _ => (name, String::new()),
    };
    let mut path = directory.join(name);
    let mut number = 1;
    while path.exists() {
        path = directory.join(format!("{stem} ({number}){extension}"));
        number += 1;
    }
    path
}

/// The origin of the pages the shell carries (Tauri 2 without its https scheme).
pub fn bundled_origin(system: System) -> &'static str {
    match system {
        System::Windows => "http://tauri.localhost",
        System::Linux => "tauri://localhost",
    }
}

fn is_bundled(url: &Url, system: System) -> bool {
    let (scheme, host) = match system {
        System::Windows => ("http", "tauri.localhost"),
        System::Linux => ("tauri", "localhost"),
    };
    url.scheme() == scheme && url.host_str() == Some(host) && url.port().is_none()
}

/// The page that opens the window with a one-time code in its fragment, which never reaches a server or a log.
/// `attempt` makes every opening a new document: a change of the fragment alone would not load the page again.
pub fn open_url(port: u16, code: &Code, attempt: u8) -> String {
    format!("http://127.0.0.1:{port}{OPEN_PATH}?n={attempt}#c={}", code.as_str())
}

/// Whether `target` is a page of the running backend.
pub fn is_own_page(target: &str, port: Option<u16>) -> bool {
    Url::parse(target).is_ok_and(|url| is_own(&url, port))
}

/// Whether `target` is the page of the code exchange on the backend's own origin.
pub fn is_open_page(target: &str, port: Option<u16>) -> bool {
    Url::parse(target).is_ok_and(|url| is_own(&url, port) && url.path() == OPEN_PATH)
}

/// The status the session refused the code with: the exchange script reports it by going to
/// `/desktop/open?refused=<status>`, a navigation the shell cancels.
pub fn refused(target: &str, port: Option<u16>) -> Option<u16> {
    let url = Url::parse(target).ok()?;
    if !is_own(&url, port) || url.path() != OPEN_PATH {
        return None;
    }
    url.query_pairs().find(|(name, _)| name == "refused").and_then(|(_, status)| status.parse().ok())
}

/// The starting page the shell carries: «CanonKeeper запускается…».
pub fn starting_page(system: System) -> String {
    format!("{}/index.html", bundled_origin(system))
}

/// The same page with the words of a failure and its three buttons; «Написать нам» opens the contact page of the site.
pub fn failure_page(system: System, words: &str) -> String {
    let mut url = Url::parse(&starting_page(system)).expect("the page of the shell is an address");
    url.query_pairs_mut().append_pair("failure", words).append_pair("contact", crate::words::CONTACT_URL);
    url.into()
}

/// The script of the code exchange (SH1.4; DA3.2, DA3.3), in its own file so that its paths are tested as a script
/// (scripts/desktop-app.test.mjs). The window runs it at the start of every page, before the page's own scripts: on the
/// page of the exchange of its backend it takes the code out of the address — so the page never sees it — trades it for
/// the session of the local author, checks the session as the first page will and goes there, or reports a refused code
/// or a lost session by going to `/desktop/open?refused=<status>`. Anywhere else it does nothing.
pub const EXCHANGE_SCRIPT: &str = include_str!("exchange.js");

#[cfg(test)]
mod tests {
    use super::*;

    const PORT: Option<u16> = Some(24117);

    #[test]
    fn the_window_stays_on_its_own_backend_and_its_own_page() {
        for system in [System::Windows, System::Linux] {
            assert_eq!(navigation("http://127.0.0.1:24117/chat", PORT, system), Navigation::Window);
            assert_eq!(navigation("http://127.0.0.1:24117/desktop/open#c=abc", PORT, system), Navigation::Window);
            assert_eq!(navigation(&starting_page(system), PORT, system), Navigation::Window);
            assert_eq!(navigation(&failure_page(system, "слова"), None, system), Navigation::Window);
        }
    }

    #[test]
    fn https_and_mail_go_to_the_browser_of_the_system() {
        assert_eq!(
            navigation("https://canonkeeper.ru", PORT, System::Linux),
            Navigation::Browser("https://canonkeeper.ru/".into())
        );
        assert_eq!(
            navigation("mailto:support@canonkeeper.ru", PORT, System::Windows),
            Navigation::Browser("mailto:support@canonkeeper.ru".into())
        );
        assert_eq!(external(crate::words::CONTACT_URL), Some(crate::words::CONTACT_URL.to_string()));
    }

    #[test]
    fn everything_else_is_refused() {
        for target in [
            "javascript:alert(1)",
            "file:///etc/passwd",
            "file:///C:/Users/x/Documents/Книга.docx",
            "http://evil.example/",
            "http://localhost:24117/",
            "http://127.0.0.1:24118/",
            "http://user@127.0.0.1:24117/",
            "https://127.0.0.1:24117/",
            "https://192.168.1.1/",
            "http://[::1]:24117/",
            "http://0.0.0.0:24117/",
            "data:text/html,<p>x</p>",
            "about:blank",
            "ftp://example.org/",
            "tauri://localhost.evil/",
            "https://",
            "mailto:",
            "not an address",
        ] {
            assert_eq!(navigation(target, PORT, System::Linux), Navigation::Refuse, "{target}");
            assert_eq!(navigation(target, PORT, System::Windows), Navigation::Refuse, "{target}");
        }
        // Before the backend is ready no port is the window's.
        assert_eq!(navigation("http://127.0.0.1:24117/chat", None, System::Linux), Navigation::Refuse);
        // The page of the shell of one system is not the page of the other.
        assert_eq!(navigation("tauri://localhost/index.html", PORT, System::Windows), Navigation::Refuse);
        assert_eq!(navigation("http://tauri.localhost/index.html", PORT, System::Linux), Navigation::Refuse);
        assert_eq!(external("javascript:alert(1)"), None);
        assert_eq!(external("http://canonkeeper.ru"), None);
        assert_eq!(external("file:///etc/passwd"), None);
    }

    #[test]
    fn a_file_the_page_makes_is_saved_and_only_its_own() {
        // «Скачать книгу…» and the file of a chapter: the page makes the file and saves it through a `blob:` address of
        // its own origin (src/ui/save-file.ts).
        let own = "blob:http://127.0.0.1:24117/7c3f0a52-5d1e-4f43-9f6b-2a8c1e0b9d44";
        for system in [System::Windows, System::Linux] {
            assert_eq!(navigation(own, PORT, system), Navigation::Save, "{system:?}");
        }
        assert!(saves(own, PORT));
        // A file the backend answers with an attachment is a page of its own origin too.
        assert!(saves("http://127.0.0.1:24117/api/v1/books/1/export?format=docx", PORT));
        for target in [
            "blob:http://127.0.0.1:24118/7c3f0a52",
            "blob:http://localhost:24117/7c3f0a52",
            "blob:http://evil.example/7c3f0a52",
            "blob:https://canonkeeper.ru/7c3f0a52",
            "blob:null/7c3f0a52",
            "blob:tauri://localhost/7c3f0a52",
            "blob:",
            "data:text/plain,Книга",
        ] {
            assert_eq!(navigation(target, PORT, System::Linux), Navigation::Refuse, "{target}");
            assert!(!saves(target, PORT), "{target}");
        }
        // Before the backend is ready there is no page to save from.
        assert_eq!(navigation(own, None, System::Linux), Navigation::Refuse);
        assert!(!saves(own, None));
    }

    #[test]
    fn a_saved_file_lands_in_the_folder_of_downloads_or_else_at_home_under_a_free_name() {
        let root = std::env::temp_dir().join(format!("canonkeeper-save-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&root);
        let downloads = root.join("Загрузки");
        let home = root.join("дом");
        std::fs::create_dir_all(&downloads).unwrap();
        std::fs::create_dir_all(&home).unwrap();
        // The folder of downloads the system names: the webview already chose a free name in it.
        let proposed = downloads.join("Северный предел (1).docx");
        assert_eq!(save_path(&proposed, Some(&downloads), Some(&home)), proposed);
        // No folder of downloads, or it is gone: the home folder, with a free name, never the current directory.
        let lost = root.join("где-то").join("Северный предел.docx");
        assert_eq!(save_path(&lost, None, Some(&home)), home.join("Северный предел.docx"));
        assert_eq!(save_path(&lost, Some(&root.join("нет")), Some(&home)), home.join("Северный предел.docx"));
        std::fs::write(home.join("Северный предел.docx"), "").unwrap();
        std::fs::write(home.join("Северный предел (1).docx"), "").unwrap();
        assert_eq!(save_path(&lost, None, Some(&home)), home.join("Северный предел (2).docx"));
        std::fs::write(home.join("Рисунки саги.zip"), "").unwrap();
        assert_eq!(save_path(&root.join("Рисунки саги.zip"), None, Some(&home)), home.join("Рисунки саги (1).zip"));
        // Nowhere better to put it: as the webview proposed.
        assert_eq!(save_path(&lost, None, None), lost);
        let _ = std::fs::remove_dir_all(&root);
    }

    #[test]
    fn the_exchange_opens_with_the_code_in_the_fragment_and_reports_a_refusal() {
        let url = open_url(24117, &Code::new("Zm9vYmFyYmF6cXV4cXV1eA"), 0);
        assert_eq!(url, "http://127.0.0.1:24117/desktop/open?n=0#c=Zm9vYmFyYmF6cXV4cXV1eA");
        assert_ne!(
            Url::parse(&url).unwrap()[..url::Position::AfterQuery],
            Url::parse(&open_url(24117, &Code::new("Zm9vYmFyYmF6cXV4cXV1eA"), 1)).unwrap()[..url::Position::AfterQuery],
            "two openings must differ before the fragment"
        );
        assert!(is_open_page(&url, PORT));
        assert!(is_own_page(&url, PORT) && is_own_page("http://127.0.0.1:24117/chat", PORT));
        assert!(
            !is_own_page("http://127.0.0.1:24117/chat", None) && !is_own_page("tauri://localhost/index.html", PORT)
        );
        assert!(!is_open_page("http://127.0.0.1:24117/chat", PORT));
        assert!(!is_open_page(&url, Some(24118)));
        assert_eq!(refused("http://127.0.0.1:24117/desktop/open?refused=401", PORT), Some(401));
        assert_eq!(refused("http://127.0.0.1:24117/desktop/open?refused=0", PORT), Some(0));
        assert_eq!(refused("http://127.0.0.1:24117/desktop/open", PORT), None);
        assert_eq!(refused("http://127.0.0.1:24117/chat?refused=401", PORT), None);
        assert_eq!(refused("http://127.0.0.1:24118/desktop/open?refused=401", PORT), None);
        // The script takes the code from the address: the shell never writes the code into a script.
        assert!(EXCHANGE_SCRIPT.contains("location.hash") && EXCHANGE_SCRIPT.contains(OPEN_PATH));
        assert!(EXCHANGE_SCRIPT.contains("location.hostname !== '127.0.0.1'"));
    }

    #[test]
    fn the_failure_page_carries_its_words_in_its_address() {
        let page = failure_page(System::Linux, "Книги на месте: /home/тест/x & y");
        let url = Url::parse(&page).unwrap();
        assert_eq!(url.scheme(), "tauri");
        let query: Vec<_> = url.query_pairs().map(|(name, value)| (name.into_owned(), value.into_owned())).collect();
        assert_eq!(
            query,
            vec![
                ("failure".to_string(), "Книги на месте: /home/тест/x & y".to_string()),
                ("contact".to_string(), crate::words::CONTACT_URL.to_string()),
            ]
        );
        assert!(!page.starts_with("http://tauri.localhost/"));
        assert!(failure_page(System::Windows, "x").starts_with("http://tauri.localhost/index.html?failure="));
    }
}
