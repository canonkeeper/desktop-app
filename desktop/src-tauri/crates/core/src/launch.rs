// SPDX-License-Identifier: AGPL-3.0-only

//! The page of the start (specs/2026-11-13-desk-upgrade.md UG3.2, UG4.1, UG4.4): what it shows and what its commands
//! may do. The page reads its words and buttons with a command of state from what the shell itself got from its backend
//! in this launch — never from its own address — and its commands take no name, no path and no address from it: each one
//! does its act only while the page shows a failure with a button for it. A page of the start the window reached by a
//! navigation shows the real state and can do nothing else.

use std::fs;
use std::path::PathBuf;

use serde::Serialize;

use crate::paths::System;
use crate::words::{self, date, size, Act, Button, Failure};

/// What the page of the start shows.
#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
#[serde(tag = "view", rename_all = "camelCase")]
pub enum Screen {
    /// «CanonKeeper запускается…», with the hint of the first start after a while.
    Starting,
    /// A long step of the start: its words instead, without the hint.
    Step { words: String },
    /// A failure: its words and its buttons.
    Failure { words: String, buttons: Vec<Button> },
    /// The page of the backend is in the window: the page of the start has nothing to say.
    Open,
}

/// The question before a return of the data from a copy (UG4.4).
#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
pub struct Question {
    pub title: String,
    pub text: String,
    /// The folders «до возврата» earlier returns left, with their date and size; they stay.
    pub earlier: Vec<String>,
    pub confirm: String,
    pub cancel: String,
}

/// The state of the page of the start: the screen and the failure behind it.
#[derive(Debug, Clone, Default)]
pub struct Launch {
    failure: Option<Failure>,
    step: Option<String>,
    open: bool,
    /// The question before a return was asked for the failure shown: a return starts only after it (UG4.1).
    asked: bool,
}

impl Launch {
    /// The backend starts.
    pub fn starting(&mut self) {
        *self = Launch::default();
    }

    /// A long step of the start goes on.
    pub fn step(&mut self, words: &str) {
        *self = Launch { step: Some(words.to_string()), ..Launch::default() };
    }

    /// The failure the window shows.
    pub fn failed(&mut self, failure: &Failure) {
        *self = Launch { failure: Some(failure.clone()), ..Launch::default() };
    }

    /// The page of the backend is open.
    pub fn opened(&mut self) {
        *self = Launch { open: true, ..Launch::default() };
    }

    pub fn screen(&self) -> Screen {
        match (&self.failure, &self.step, self.open) {
            (Some(failure), _, _) => {
                Screen::Failure { words: failure.page.words.clone(), buttons: failure.page.buttons.clone() }
            }
            (None, Some(words), _) => Screen::Step { words: words.clone() },
            (None, None, true) => Screen::Open,
            (None, None, false) => Screen::Starting,
        }
    }

    /// The failure shown, when its page has a button for `act`.
    fn showing(&self, act: Act) -> Option<&Failure> {
        self.failure.as_ref().filter(|failure| failure.page.has(act))
    }

    /// Whether «Написать нам» may open the contact page: a failure is shown.
    pub fn may_contact(&self) -> bool {
        self.showing(Act::Contact).is_some()
    }

    /// The page of the release «Скачать…» opens, while the failure shown has the button.
    pub fn download(&self, version: &str) -> Option<String> {
        words::download(&self.showing(Act::Download)?.fault, version)
    }

    /// The directory «Открыть папку с копией» opens: the copy of the reason shown, a plain name in `<data>/backups/`, and
    /// a directory there — not a link, not a file.
    pub fn copy_folder(&self, system: System, data_dir: &str) -> Option<PathBuf> {
        let name = words::copy_name(&self.showing(Act::Copy)?.fault)?;
        let path = PathBuf::from(system.join(data_dir, &["backups", name]));
        let metadata = fs::symlink_metadata(&path).ok()?;
        (metadata.is_dir() && !metadata.file_type().is_symlink()).then_some(path)
    }

    /// Whether a return of the data may start: the failure shown is `newer_schema` with a candidate, and the question
    /// before the return was asked (UG4.1).
    pub fn may_restore(&self) -> bool {
        self.asked && self.showing(Act::Restore).is_some_and(|failure| words::candidate(&failure.fault).is_some())
    }

    /// Asks the question before a return: the page shows it, and only then may the return start.
    pub fn ask_restore(&mut self) -> Option<Question> {
        let question = self.restore_question()?;
        self.asked = true;
        Some(question)
    }

    /// The question before a return (UG4.4): the date of the copy, the version it was made before, what goes and what
    /// stays, and the folders «до возврата» earlier returns left.
    pub fn restore_question(&self) -> Option<Question> {
        let failure = self.showing(Act::Restore)?;
        let copy = words::candidate(&failure.fault)?;
        let words::Fault::Fatal { said, .. } = &failure.fault else { return None };
        let title = match date(&copy.made_at) {
            Some(date) => format!("Вернуть данные из копии от {date}?"),
            None => "Вернуть данные из копии?".to_string(),
        };
        let made = match words::plain_version(copy.version.as_deref()) {
            Some(version) => format!("Копия сделана перед обновлением до версии {version}."),
            None => "Копия сделана перед обновлением CanonKeeper.".to_string(),
        };
        let earlier = said
            .before
            .iter()
            .map(|folder| match date(&folder.made_at) {
                Some(date) => format!("Папка «до возврата» от {date} — {}", size(folder.bytes)),
                None => format!("Папка «до возврата» — {}", size(folder.bytes)),
            })
            .collect::<Vec<_>>();
        let mut text = format!(
            "{made} Всё, что вы сделали после этого, пропадёт. Нынешние данные сохранятся рядом, в папке „до возврата“"
        );
        if !earlier.is_empty() {
            text.push_str(". Прежние папки „до возврата“ останутся как есть");
        }
        Some(Question {
            title, text, earlier, confirm: "Вернуть данные".to_string(), cancel: "Отмена".to_string()
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::control::{BackupCopy, BeforeRestore, Said};
    use crate::words::{failure, Fault};

    fn fault(reason: &str, backup: Option<&str>, said: Said) -> Fault {
        Fault::Fatal { reason: reason.into(), backup: backup.map(str::to_string), said: Box::new(said) }
    }

    fn candidate(name: &str) -> BackupCopy {
        BackupCopy { name: name.into(), made_at: "2026-11-13T14:05:00+03:00".into(), version: Some("1.3.0".into()) }
    }

    fn stand(name: &str) -> PathBuf {
        let root = std::env::temp_dir().join(format!("canonkeeper-launch-{name}-{}", std::process::id()));
        let _ = fs::remove_dir_all(&root);
        fs::create_dir_all(root.join("data").join("backups")).unwrap();
        root
    }

    #[test]
    fn the_page_reads_the_state_of_the_supervision_and_nothing_of_its_address() {
        // UG3.2: a page of the start the window reached by a navigation while the backend works shows the real state, and
        // the commands of a reason refuse.
        let mut launch = Launch::default();
        assert_eq!(launch.screen(), Screen::Starting);
        launch.step("Обновляю данные до версии 1.3.0");
        assert_eq!(launch.screen(), Screen::Step { words: "Обновляю данные до версии 1.3.0".into() });
        launch.opened();
        assert_eq!(launch.screen(), Screen::Open);
        for state in [Launch::default(), launch.clone()] {
            assert!(!state.may_contact() && !state.may_restore());
            assert_eq!(state.download("1.3.0"), None);
            assert_eq!(state.copy_folder(System::CURRENT, "/"), None);
            assert_eq!(state.restore_question(), None);
        }
    }

    #[test]
    fn a_copy_folder_is_a_plain_directory_of_the_copies_of_the_reason() {
        let root = stand("copy");
        let data = root.join("data").to_string_lossy().into_owned();
        let backups = root.join("data").join("backups");
        fs::create_dir_all(backups.join("pre-145-20261113T110500.000Z")).unwrap();
        fs::write(backups.join("pre-146-20261113T110500.000Z"), "a file").unwrap();
        let mut launch = Launch::default();
        let show = |launch: &mut Launch, backup: &str| {
            launch.failed(&failure(&fault("migrate", Some(backup), Said::default()), System::CURRENT, &data, "1.3.0"));
        };
        show(&mut launch, "pre-145-20261113T110500.000Z");
        assert_eq!(
            launch.copy_folder(System::CURRENT, &data),
            Some(PathBuf::from(System::CURRENT.join(&data, &["backups", "pre-145-20261113T110500.000Z"])))
        );
        // A file, a name that climbs, a path, a name that is not there: nothing opens.
        for name in ["pre-146-20261113T110500.000Z", "..", "../pg", "/etc", "pre-147-20261113T110500.000Z"] {
            show(&mut launch, name);
            assert_eq!(launch.copy_folder(System::CURRENT, &data), None, "{name}");
        }
        #[cfg(unix)]
        {
            std::os::unix::fs::symlink(root.join("data"), backups.join("pre-148-20261113T110500.000Z")).unwrap();
            show(&mut launch, "pre-148-20261113T110500.000Z");
            assert_eq!(launch.copy_folder(System::CURRENT, &data), None, "a link was opened");
        }
        // A reason without a copy opens none, though the backend named one.
        launch.failed(&failure(
            &fault("database", Some("pre-145-20261113T110500.000Z"), Said::default()),
            System::CURRENT,
            &data,
            "1.3.0",
        ));
        assert_eq!(launch.copy_folder(System::CURRENT, &data), None);
        fs::remove_dir_all(root).unwrap();
    }

    #[test]
    fn a_return_is_asked_only_from_a_newer_schema_with_a_candidate() {
        // UG4.1, UG4.4.
        let data = "/home/тест/.local/share/canonkeeper";
        let mut launch = Launch::default();
        let with_candidate = Said {
            opened: Some("1.3.0".into()),
            copy: Some(candidate("pre-145-20261113T110500.000Z")),
            ..Said::default()
        };
        launch.failed(&failure(&fault("newer_schema", None, with_candidate.clone()), System::Linux, data, "1.2.0"));
        // Not before the question.
        assert!(!launch.may_restore());
        let question = launch.ask_restore().unwrap();
        assert!(launch.may_restore());
        assert_eq!(question.title, "Вернуть данные из копии от 13 ноября, 14:05?");
        assert_eq!(
            question.text,
            "Копия сделана перед обновлением до версии 1.3.0. Всё, что вы сделали после этого, пропадёт. Нынешние данные сохранятся рядом, в папке „до возврата“"
        );
        assert!(question.earlier.is_empty());
        assert_eq!((question.confirm.as_str(), question.cancel.as_str()), ("Вернуть данные", "Отмена"));
        assert_eq!(
            launch.download("1.2.0").as_deref(),
            Some("https://github.com/canonkeeper/desktop-app/releases/latest")
        );

        // Earlier folders «до возврата» are named with their date and size: they stay.
        let before = vec![BeforeRestore { made_at: "2026-11-12T10:20:00+03:00".into(), bytes: 1_288_490_188 }];
        launch.failed(&failure(
            &fault("newer_schema", None, Said { before, ..with_candidate }),
            System::Linux,
            data,
            "1.2.0",
        ));
        let question = launch.restore_question().unwrap();
        assert_eq!(question.earlier, vec!["Папка «до возврата» от 12 ноября, 10:20 — 1,2 ГБ".to_string()]);
        assert!(question.text.ends_with("Прежние папки „до возврата“ останутся как есть"), "{}", question.text);

        // No candidate, a name that is no plain name, another reason: no return.
        for said in [Said::default(), Said { copy: Some(candidate("../../pg")), ..Said::default() }] {
            launch.failed(&failure(&fault("newer_schema", None, said), System::Linux, data, "1.2.0"));
            assert!(!launch.may_restore() && launch.restore_question().is_none());
        }
        let migrate = Said { copy: Some(candidate("pre-145-20261113T110500.000Z")), ..Said::default() };
        launch.failed(&failure(&fault("migrate", None, migrate), System::Linux, data, "1.3.0"));
        assert!(!launch.may_restore() && launch.restore_question().is_none());
        launch.failed(&failure(&Fault::Unerased { folder: data.into() }, System::Linux, data, "1.3.0"));
        assert!(!launch.may_contact() && launch.download("1.3.0").is_none());
    }
}
