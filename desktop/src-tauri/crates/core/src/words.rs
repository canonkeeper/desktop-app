// SPDX-License-Identifier: AGPL-3.0-only

//! What the author reads on the page of the start (specs/2026-11-12-desk-shell.md SH2.1; specs/2026-11-13-desk-erase.md
//! ER1.5; specs/2026-11-13-desk-upgrade.md UG3): one table from the reason or the step to the words and the buttons of
//! the reason. Every failure has its way out — the folder of the data, «Написать нам» and «Закрыть» — and the action of
//! its reason; an erasure that did not end has «Закрыть» alone. A new reason is a new row, not a new page. The words name
//! only the directories of the app, the copy made before the migrations, versions, dates and sizes — no text of a book,
//! no path of another file, nothing of a log; what the backend sent is used only when it is a version, a plain name of
//! a copy, a date or a number.

use serde::Serialize;

use crate::control::{BackupCopy, Said, Step};
use crate::paths::System;
use crate::update::package;

/// «Написать нам» of the failure window: the contact form of the site in the browser of the system.
pub const CONTACT_URL: &str = "https://canonkeeper.ru/?contact=write";

/// The page of the releases of the open app: «Скачать…» opens it with the version of its button.
pub const RELEASES_URL: &str = "https://github.com/canonkeeper/desktop-app/releases";

/// Why the window shows a failure.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Fault {
    /// The program of the backend did not start, or ended before it said anything.
    Program,
    /// The backend said `fatal`; `said` is what else the words of its reason need (specs/2026-11-13-desk-upgrade.md UG2).
    Fatal { reason: String, backup: Option<String>, said: Box<Said> },
    /// Something the table does not name: the backend fell too often, the window could not open.
    Unknown,
    /// «Стереть все данные» did not end: a file of `folder` is held by another program (specs/2026-11-13-desk-erase.md
    /// ER1.5). The backend does not start on data half erased.
    Unerased { folder: String },
    /// «Стереть все данные» did not end: the store of the system kept the key of «Свой ИИ»; the data are gone.
    KeyKept,
    /// «Стереть все данные» did not begin: the mark could not be written, or a directory is one the removal refuses. The
    /// data are in place.
    NotErased,
}

/// What a button of the page of the start does.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "camelCase")]
pub enum Act {
    /// Opens the data directory in the file manager.
    Folder,
    /// Opens the contact page of the site in the browser.
    Contact,
    /// Opens the page of the release of the version the button names in the browser.
    Download,
    /// Opens the directory of the copy of the reason.
    Copy,
    /// Asks whether to bring the data back from the copy, then does it.
    Restore,
    /// Ends the app.
    Close,
}

/// Where a button of a failure stands (the board 44·40, specs/2026-11-13-desk-updates.md UB0.1): the way out every failure
/// has — the folder of the data and «Написать нам» — in a row under the words; «Закрыть» and the actions of the reason at
/// the bottom of the window.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "camelCase")]
pub enum Place {
    WayOut,
    Footer,
}

/// A button of the page of the start: what it does, its words, where it stands and whether it is the main one — the last
/// action of the reason; a failure without an action of its reason has no main button.
#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
pub struct Button {
    #[serde(rename = "id")]
    pub act: Act,
    pub label: String,
    pub place: Place,
    pub main: bool,
}

impl Button {
    fn new(act: Act, label: impl Into<String>) -> Button {
        Button { act, label: label.into(), place: Place::Footer, main: false }
    }
}

/// What a failure shows: its words and its buttons in the order the window shows them — the way out under the words, then
/// at the bottom «Закрыть» and the actions of the reason, the main one last.
#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
pub struct Page {
    pub words: String,
    pub buttons: Vec<Button>,
}

impl Page {
    /// Whether the page has a button that does `act`.
    pub fn has(&self, act: Act) -> bool {
        self.buttons.iter().any(|button| button.act == act)
    }
}

/// A failure the window shows: the fault and its page.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Failure {
    pub fault: Fault,
    pub page: Page,
}

/// The failure of a fault; `data_dir` is the data directory of the user on `system`, `version` the version of the app.
pub fn failure(fault: &Fault, system: System, data_dir: &str, version: &str) -> Failure {
    Failure { fault: fault.clone(), page: page(fault, system, data_dir, version) }
}

/// The words and the buttons of a fault: the table of the reasons (the table Data of UG3).
pub fn page(fault: &Fault, system: System, data_dir: &str, version: &str) -> Page {
    let shell = version;
    let version = plain_version(Some(version));
    let (words, actions): (String, Vec<Button>) = match fault {
        Fault::Program => (quarantine(data_dir), vec![]),
        Fault::Unknown => (unknown(data_dir), vec![]),
        Fault::Unerased { folder } => {
            let words = format!(
                "Не удалось стереть данные: файлы в папке {folder} заняты другой программой. Закройте её и запустите CanonKeeper снова"
            );
            return Page { words, buttons: footer(vec![]) };
        }
        Fault::KeyKept => {
            let words = "Данные стёрты, но записи CanonKeeper остались в хранилище паролей системы. Удалите их там или запустите CanonKeeper снова — он попробует ещё раз".to_string();
            return Page { words, buttons: footer(vec![]) };
        }
        Fault::NotErased => (
            format!("Стирание не началось. Книги на месте: {data_dir}. Запустите CanonKeeper и попробуйте ещё раз"),
            vec![],
        ),
        Fault::Fatal { reason, backup, said } => {
            let opened = plain_version(said.opened.as_deref());
            match reason.as_str() {
                // A program of the bundle is missing or did not start: the antivirus is the likely cause (DT13.8).
                "resources" | "postgres" => (quarantine(data_dir), vec![]),
                "database" => (
                    format!(
                        "Не удалось открыть данные CanonKeeper. Книги на месте: {data_dir}. Перезапустите программу; если не поможет — напишите нам"
                    ),
                    vec![],
                ),
                "listen" => (
                    "CanonKeeper не смог открыть окно: порты компьютера заняты. Перезапустите программу".to_string(),
                    vec![],
                ),
                "backup" => (
                    "Обновление не применено: не удалось сохранить копию данных. Данные целы. Проверьте место на диске и запустите снова".to_string(),
                    vec![download_earlier(opened)],
                ),
                "migrate" => {
                    let update = match version {
                        Some(version) => format!("Обновление до версии {version} не применилось: данные целы."),
                        None => "Обновление не применилось: данные целы.".to_string(),
                    };
                    let earlier = match opened {
                        Some(opened) => format!("Установите прежнюю версию {opened} — она откроет их как раньше."),
                        None => "Установите прежнюю версию CanonKeeper — она откроет их как раньше.".to_string(),
                    };
                    let copy = plain_copy(backup.as_deref());
                    let mut actions = vec![download_earlier(opened)];
                    if copy.is_some() {
                        actions.push(Button::new(Act::Copy, "Открыть папку с копией"));
                    }
                    (format!("{update} {earlier} Копия данных: {}", copy_path(system, data_dir, copy)), actions)
                }
                "migrate_space" => (
                    "Обновление не применилось: на диске не хватает места. Данные целы. Освободите место и запустите CanonKeeper снова".to_string(),
                    vec![],
                ),
                "newer_schema" => {
                    let words = match (opened, version) {
                        (Some(opened), Some(version)) => format!(
                            "Эти данные уже открывала версия {opened}, а запущена {version}. Установите версию {opened} или новее — данные откроются как есть"
                        ),
                        (Some(opened), None) => format!(
                            "Эти данные уже открывала версия {opened}. Установите версию {opened} или новее — данные откроются как есть"
                        ),
                        _ => "Эти данные открывала более новая версия CanonKeeper. Установите последнюю версию — данные откроются как есть".to_string(),
                    };
                    let mut actions = vec![Button::new(Act::Download, "Скачать новую версию")];
                    if candidate(fault).is_some() {
                        actions.push(Button::new(Act::Restore, "Вернуть данные из копии…"));
                    }
                    (words, actions)
                }
                // UB0.2: an installation cut short may have left either of the two programs earlier: the words name the
                // later one, the version to install again.
                "version_mismatch" => match bigger(Some(shell), said.program.as_deref()) {
                    Some(version) => (
                        format!("Установка CanonKeeper не закончена. Установите версию {version} ещё раз — данные целы"),
                        vec![Button::new(Act::Download, format!("Скачать версию {version}"))],
                    ),
                    None => (
                        "Установка CanonKeeper не закончена. Установите CanonKeeper ещё раз — данные целы".to_string(),
                        vec![Button::new(Act::Download, "Скачать CanonKeeper")],
                    ),
                },
                "restore_space" => (
                    match said.needed.filter(|needed| *needed > 0) {
                        Some(needed) => format!(
                            "Не хватает места на диске, чтобы вернуть данные из копии: нужно ещё {}. Данные не изменились",
                            size(needed)
                        ),
                        None => "Не хватает места на диске, чтобы вернуть данные из копии. Данные не изменились".to_string(),
                    },
                    vec![],
                ),
                "restore_copy" => (
                    match said.copy.as_ref().and_then(|copy| date(&copy.made_at)) {
                        Some(date) => {
                            format!("Копия от {date} повреждена, вернуть данные из неё нельзя. Данные не изменились")
                        }
                        None => "Копия данных повреждена, вернуть данные из неё нельзя. Данные не изменились".to_string(),
                    },
                    copy_button(fault),
                ),
                "restore" => {
                    ("Не удалось вернуть данные из копии. Данные не изменились".to_string(), copy_button(fault))
                }
                _ => (unknown(data_dir), vec![]),
            }
        }
    };
    let mut buttons = vec![way_out(Act::Folder, "Открыть папку с данными"), way_out(Act::Contact, "Написать нам")];
    buttons.extend(footer(actions));
    Page { words, buttons }
}

fn way_out(act: Act, label: &str) -> Button {
    Button { place: Place::WayOut, ..Button::new(act, label) }
}

/// The bottom of the window: «Закрыть», then the actions of the reason — named main first by the table — with the main one
/// last (the board 44·40).
fn footer(actions: Vec<Button>) -> Vec<Button> {
    let mut buttons = vec![close()];
    let main = actions.len();
    buttons.extend(
        actions.into_iter().rev().enumerate().map(|(index, button)| Button { main: index + 1 == main, ..button }),
    );
    buttons
}

/// The words of a long step of the start (UG3.1).
pub fn step_words(step: &Step) -> String {
    match step {
        Step::Backup { version } => match plain_version(version.as_deref()) {
            Some(version) => format!(
                "Сохраняю копию данных перед обновлением до версии {version} — это может занять несколько минут"
            ),
            None => "Сохраняю копию данных перед обновлением — это может занять несколько минут".to_string(),
        },
        Step::Migrate { version } => match plain_version(version.as_deref()) {
            Some(version) => format!("Обновляю данные до версии {version}"),
            None => "Обновляю данные".to_string(),
        },
        Step::Restore { copy } => match copy.as_ref().and_then(|copy| date(&copy.made_at)) {
            Some(date) => format!("Возвращаю данные из копии от {date} — это может занять несколько минут"),
            None => "Возвращаю данные из копии — это может занять несколько минут".to_string(),
        },
    }
}

/// The words of the page of the start while the backend stops for an installation (the board 44·39;
/// specs/2026-11-13-desk-updates.md UB2.3).
pub fn installing_words(version: &str) -> String {
    match plain_version(Some(version)) {
        Some(version) => format!(
            "Устанавливаю версию {version}: сохраняю данные и останавливаю помощника. Приложение закроется и откроется снова"
        ),
        None => "Устанавливаю новую версию: сохраняю данные и останавливаю помощника. Приложение закроется и откроется снова"
            .to_string(),
    }
}

/// The words of the page of the start while the backend stops before a package of Linux is installed
/// (specs/2026-11-17-desk-update-linux.md UL1.3, the board 44·39).
pub fn stopping_words(version: &str) -> String {
    format!("Сохраняю данные и останавливаю помощника, чтобы установить {}", accusative(version))
}

/// The words of a step of the installation of a package of Linux (UL1.3, the board 44·39); the code of the step in the log
/// of the shell is [`package::Step::code`].
pub fn package_words(step: package::Step, version: &str) -> String {
    match step {
        package::Step::Password => {
            format!("Система спрашивает пароль администратора, чтобы установить {}", accusative(version))
        }
        package::Step::Running => {
            format!("Устанавливаю {}. Окно откроется снова, когда установка закончится", accusative(version))
        }
        // The board 44·39: «2» and «минут» stay on one line.
        package::Step::LockWait => {
            "Жду, пока закончится другая установка программ (до 2\u{00a0}минут). Если она ждёт ответа в терминале, ответьте там"
                .to_string()
        }
    }
}

/// «версию 1.3.0», without a plain version «новую версию».
fn accusative(version: &str) -> String {
    match plain_version(Some(version)) {
        Some(version) => format!("версию {version}"),
        None => "новую версию".to_string(),
    }
}

fn close() -> Button {
    Button::new(Act::Close, "Закрыть")
}

/// «Скачать версию 1.2.0» of an update that did not apply: the version the data were opened with before.
fn download_earlier(opened: Option<&str>) -> Button {
    match opened {
        Some(opened) => Button::new(Act::Download, format!("Скачать версию {opened}")),
        None => Button::new(Act::Download, "Скачать прежнюю версию"),
    }
}

fn copy_button(fault: &Fault) -> Vec<Button> {
    match copy_name(fault) {
        Some(_) => vec![Button::new(Act::Copy, "Открыть папку с копией")],
        None => vec![],
    }
}

/// The copy a return would bring back: the candidate of `newer_schema`, when its name is a plain name of a copy.
pub fn candidate(fault: &Fault) -> Option<&BackupCopy> {
    match fault {
        Fault::Fatal { reason, said, .. } if reason == "newer_schema" => {
            said.copy.as_ref().filter(|copy| plain_copy(Some(&copy.name)).is_some())
        }
        _ => None,
    }
}

/// The name of the copy of the reason a failure shows: the copy of a failed migration, the copy a return failed with.
pub fn copy_name(fault: &Fault) -> Option<&str> {
    match fault {
        Fault::Fatal { reason, backup, .. } if reason == "migrate" => plain_copy(backup.as_deref()),
        Fault::Fatal { reason, said, .. } if reason == "restore_copy" || reason == "restore" => {
            plain_copy(said.copy.as_ref().map(|copy| copy.name.as_str()))
        }
        _ => None,
    }
}

/// The page of the release «Скачать…» of a failure opens: the release of the version of the button, the latest release
/// for a new version, the list of releases when the version is not known. None for a failure without the button.
pub fn download(fault: &Fault, version: &str) -> Option<String> {
    let Fault::Fatal { reason, said, .. } = fault else { return None };
    let release = |version: Option<&str>| match version {
        Some(version) => format!("{RELEASES_URL}/tag/v{}", version.trim_start_matches('v')),
        None => RELEASES_URL.to_string(),
    };
    match reason.as_str() {
        "newer_schema" => Some(format!("{RELEASES_URL}/latest")),
        "migrate" | "backup" => Some(release(plain_version(said.opened.as_deref()))),
        "version_mismatch" => Some(match bigger(Some(version), said.program.as_deref()) {
            Some(version) => release(Some(version)),
            None => format!("{RELEASES_URL}/latest"),
        }),
        _ => None,
    }
}

/// A version the words may say and an address may carry: digits, letters, dots, `-` and `+`, starting with a digit or
/// with `v` and a digit, not too long.
pub fn plain_version(version: Option<&str>) -> Option<&str> {
    let version = version?;
    let digits = version.strip_prefix('v').unwrap_or(version);
    (!version.is_empty()
        && version.len() <= 32
        && digits.starts_with(|letter: char| letter.is_ascii_digit())
        && version.chars().all(|letter| letter.is_ascii_alphanumeric() || matches!(letter, '.' | '-' | '+')))
    .then_some(version)
}

/// The later of two versions, each only when it is a plain version (UB0.2); a version that is no semantic version loses to
/// one that is, and of two such the first is taken.
pub fn bigger<'a>(first: Option<&'a str>, second: Option<&'a str>) -> Option<&'a str> {
    let parsed = |version: Option<&'a str>| {
        plain_version(version).map(|plain| (plain, semver::Version::parse(plain.trim_start_matches('v')).ok()))
    };
    match (parsed(first), parsed(second)) {
        (Some((one, Some(a))), Some((other, Some(b)))) => Some(if b > a { other } else { one }),
        (Some((one, Some(_))), _) | (Some((one, None)), None) => Some(one),
        (_, Some((other, _))) => Some(other),
        (None, None) => None,
    }
}

/// A plain name of a directory of `backups/`: no separator, no `..`, nothing but letters, digits, `-`, `_` and `.`.
pub fn plain_copy(name: Option<&str>) -> Option<&str> {
    name.filter(|name| {
        !name.is_empty()
            && name.len() <= 128
            && *name != "."
            && *name != ".."
            && name.chars().all(|letter| letter.is_ascii_alphanumeric() || matches!(letter, '-' | '_' | '.'))
    })
}

fn quarantine(data_dir: &str) -> String {
    format!(
        "CanonKeeper не смог запуститься. Возможно, антивирус поместил часть программы в карантин — проверьте его и запустите снова. Ваши книги на месте: {data_dir}"
    )
}

fn unknown(data_dir: &str) -> String {
    format!("CanonKeeper не смог запуститься. Книги на месте: {data_dir}")
}

/// The path of the copy: `<data>/backups/<name>` when the backend named a plain directory name, otherwise the
/// directory of the copies.
fn copy_path(system: System, data_dir: &str, name: Option<&str>) -> String {
    match plain_copy(name) {
        Some(name) => system.join(data_dir, &["backups", name]),
        None => system.join(data_dir, &["backups"]),
    }
}

const MONTHS: [&str; 12] = [
    "января",
    "февраля",
    "марта",
    "апреля",
    "мая",
    "июня",
    "июля",
    "августа",
    "сентября",
    "октября",
    "ноября",
    "декабря",
];

/// «13 ноября, 14:05» of a local time the backend gave as RFC 3339 (`2026-11-13T14:05:00+03:00`): the date and the clock
/// as the computer reads them. None for anything else.
pub fn date(made_at: &str) -> Option<String> {
    let (day, clock) = made_at.split_once('T')?;
    let mut parts = day.split('-');
    let (_year, month, day) = (parts.next()?, parts.next()?, parts.next()?);
    let month: usize = month.parse().ok().filter(|month| (1..=12).contains(month))?;
    let day: u8 = day.parse().ok().filter(|day| (1..=31).contains(day))?;
    let hours: u8 = clock.get(0..2)?.parse().ok().filter(|hours| *hours < 24)?;
    let minutes: u8 = clock.get(3..5)?.parse().ok().filter(|minutes| *minutes < 60)?;
    (clock.get(2..3)? == ":").then(|| format!("{day} {}, {hours:02}:{minutes:02}", MONTHS[month - 1]))
}

/// «2,1 ГБ», «640 МБ»: a size on the disk, never less than it is.
pub fn size(bytes: u64) -> String {
    const MEGABYTE: u64 = 1024 * 1024;
    const GIGABYTE: u64 = 1024 * MEGABYTE;
    if bytes >= GIGABYTE {
        let tenths = (u128::from(bytes) * 10).div_ceil(u128::from(GIGABYTE));
        format!("{},{} ГБ", tenths / 10, tenths % 10)
    } else {
        format!("{} МБ", bytes.div_ceil(MEGABYTE).max(1))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    const WINDOWS_DATA: &str = r"C:\Users\Тест\AppData\Local\ru.canonkeeper.desktop\data";
    const LINUX_DATA: &str = "/home/тест/.local/share/canonkeeper";

    fn fatal(reason: &str, backup: Option<&str>) -> Fault {
        Fault::Fatal { reason: reason.to_string(), backup: backup.map(str::to_string), said: Box::default() }
    }

    fn with(reason: &str, backup: Option<&str>, said: Said) -> Fault {
        Fault::Fatal { reason: reason.to_string(), backup: backup.map(str::to_string), said: Box::new(said) }
    }

    fn copy() -> BackupCopy {
        BackupCopy {
            name: "pre-145-20261113T110500.000Z".into(),
            made_at: "2026-11-13T14:05:00+03:00".into(),
            version: Some("1.3.0".into()),
        }
    }

    fn labels(page: &Page) -> Vec<&str> {
        page.buttons.iter().map(|button| button.label.as_str()).collect()
    }

    const WAY_OUT: [&str; 3] = ["Открыть папку с данными", "Написать нам", "Закрыть"];

    /// The buttons of a failure with the actions of its reason, given as the bottom of the window shows them: the main one
    /// last.
    fn with_way_out<'a>(actions: &[&'a str]) -> Vec<&'a str> {
        WAY_OUT.into_iter().chain(actions.iter().copied()).collect()
    }

    #[test]
    fn an_erasure_that_did_not_end_names_the_folder_and_has_only_close() {
        // specs/2026-11-13-desk-erase.md ER1.5: one phrase and «Закрыть»; specs/2026-11-13-desk-upgrade.md UG6.3: the
        // rows of the erasure stand in the one table unchanged.
        let fault = Fault::Unerased { folder: WINDOWS_DATA.to_string() };
        let held = page(&fault, System::Windows, WINDOWS_DATA, "1.3.0");
        assert_eq!(
            held.words,
            format!("Не удалось стереть данные: файлы в папке {WINDOWS_DATA} заняты другой программой. Закройте её и запустите CanonKeeper снова")
        );
        assert_eq!(labels(&held), vec!["Закрыть"]);
        assert_eq!(labels(&page(&Fault::Unknown, System::Windows, WINDOWS_DATA, "")), WAY_OUT.to_vec());
    }

    #[test]
    fn an_erasure_says_its_own_cause_a_kept_key_or_a_beginning_that_did_not_come() {
        let kept = page(&Fault::KeyKept, System::Windows, WINDOWS_DATA, "1.3.0");
        // specs/2026-11-13-desk-account.md AC2.5: the set holds the key of «Свой ИИ» and the pass of the account.
        assert_eq!(
            kept.words,
            "Данные стёрты, но записи CanonKeeper остались в хранилище паролей системы. Удалите их там или запустите CanonKeeper снова — он попробует ещё раз"
        );
        assert_eq!(labels(&kept), vec!["Закрыть"]);
        let not = page(&Fault::NotErased, System::Linux, LINUX_DATA, "1.3.0");
        assert_eq!(
            not.words,
            format!("Стирание не началось. Книги на месте: {LINUX_DATA}. Запустите CanonKeeper и попробуйте ещё раз")
        );
        assert_eq!(labels(&not), WAY_OUT.to_vec());
    }

    #[test]
    fn every_reason_of_the_table_has_its_own_words() {
        // The words of the table SH2.1, letter for letter.
        let quarantine = format!(
            "CanonKeeper не смог запуститься. Возможно, антивирус поместил часть программы в карантин — проверьте его и запустите снова. Ваши книги на месте: {WINDOWS_DATA}"
        );
        let table = [
            (Fault::Program, quarantine.clone()),
            (fatal("resources", None), quarantine.clone()),
            (fatal("postgres", None), quarantine),
            (
                fatal("database", None),
                format!(
                    "Не удалось открыть данные CanonKeeper. Книги на месте: {WINDOWS_DATA}. Перезапустите программу; если не поможет — напишите нам"
                ),
            ),
            (
                fatal("listen", None),
                "CanonKeeper не смог открыть окно: порты компьютера заняты. Перезапустите программу".to_string(),
            ),
        ];
        for (fault, expected) in table {
            let shown = page(&fault, System::Windows, WINDOWS_DATA, "1.3.0");
            assert_eq!(shown.words, expected, "{fault:?}");
            assert_eq!(labels(&shown), WAY_OUT.to_vec(), "{fault:?}");
        }
    }

    #[test]
    fn the_reasons_of_an_update_say_their_versions_and_have_their_actions() {
        // specs/2026-11-13-desk-upgrade.md UG3.3, the table Data: the words of the board 44·35 and of the spec.
        let opened = Said { opened: Some("1.2.0".into()), ..Said::default() };
        let migrate = page(
            &with("migrate", Some("pre-0142-20261113T140500Z"), opened.clone()),
            System::Windows,
            WINDOWS_DATA,
            "1.3.0",
        );
        assert_eq!(
            migrate.words,
            format!(
                r"Обновление до версии 1.3.0 не применилось: данные целы. Установите прежнюю версию 1.2.0 — она откроет их как раньше. Копия данных: {WINDOWS_DATA}\backups\pre-0142-20261113T140500Z"
            )
        );
        assert_eq!(labels(&migrate), with_way_out(&["Открыть папку с копией", "Скачать версию 1.2.0"]));

        let backup = page(&with("backup", None, opened), System::Linux, LINUX_DATA, "1.3.0");
        assert_eq!(
            backup.words,
            "Обновление не применено: не удалось сохранить копию данных. Данные целы. Проверьте место на диске и запустите снова"
        );
        assert_eq!(labels(&backup), with_way_out(&["Скачать версию 1.2.0"]));

        let space = page(&fatal("migrate_space", None), System::Linux, LINUX_DATA, "1.3.0");
        assert_eq!(
            space.words,
            "Обновление не применилось: на диске не хватает места. Данные целы. Освободите место и запустите CanonKeeper снова"
        );
        assert_eq!(labels(&space), WAY_OUT.to_vec());

        let newer =
            with("newer_schema", None, Said { opened: Some("1.3.0".into()), copy: Some(copy()), ..Said::default() });
        let newer_page = page(&newer, System::Linux, LINUX_DATA, "1.2.0");
        assert_eq!(
            newer_page.words,
            "Эти данные уже открывала версия 1.3.0, а запущена 1.2.0. Установите версию 1.3.0 или новее — данные откроются как есть"
        );
        assert_eq!(labels(&newer_page), with_way_out(&["Вернуть данные из копии…", "Скачать новую версию"]));

        let mismatch_page = page(&fatal("version_mismatch", None), System::Windows, WINDOWS_DATA, "1.3.0");
        assert_eq!(
            mismatch_page.words,
            "Установка CanonKeeper не закончена. Установите версию 1.3.0 ещё раз — данные целы"
        );
        assert_eq!(labels(&mismatch_page), with_way_out(&["Скачать версию 1.3.0"]));

        let room = page(
            &with("restore_space", None, Said { needed: Some(2_254_857_830), ..Said::default() }),
            System::Linux,
            LINUX_DATA,
            "1.2.0",
        );
        assert_eq!(
            room.words,
            "Не хватает места на диске, чтобы вернуть данные из копии: нужно ещё 2,1 ГБ. Данные не изменились"
        );
        assert_eq!(labels(&room), WAY_OUT.to_vec());

        let damaged = with("restore_copy", None, Said { copy: Some(copy()), ..Said::default() });
        let damaged_page = page(&damaged, System::Linux, LINUX_DATA, "1.2.0");
        assert_eq!(
            damaged_page.words,
            "Копия от 13 ноября, 14:05 повреждена, вернуть данные из неё нельзя. Данные не изменились"
        );
        assert_eq!(labels(&damaged_page), with_way_out(&["Открыть папку с копией"]));

        let failed = with("restore", None, Said { copy: Some(copy()), ..Said::default() });
        let failed_page = page(&failed, System::Linux, LINUX_DATA, "1.2.0");
        assert_eq!(failed_page.words, "Не удалось вернуть данные из копии. Данные не изменились");
        assert_eq!(labels(&failed_page), with_way_out(&["Открыть папку с копией"]));
    }

    #[test]
    fn the_reasons_without_a_known_version_have_their_own_words() {
        // The table Data: «без известной версии».
        let migrate = page(&fatal("migrate", Some("pre-0142-20261113T140500Z")), System::Linux, LINUX_DATA, "1.3.0");
        assert_eq!(
            migrate.words,
            format!("Обновление до версии 1.3.0 не применилось: данные целы. Установите прежнюю версию CanonKeeper — она откроет их как раньше. Копия данных: {LINUX_DATA}/backups/pre-0142-20261113T140500Z")
        );
        assert_eq!(labels(&migrate), with_way_out(&["Открыть папку с копией", "Скачать прежнюю версию"]));
        assert_eq!(
            labels(&page(&fatal("backup", None), System::Linux, LINUX_DATA, "")),
            with_way_out(&["Скачать прежнюю версию"])
        );
        let newer = page(&fatal("newer_schema", None), System::Linux, LINUX_DATA, "1.2.0");
        assert_eq!(
            newer.words,
            "Эти данные открывала более новая версия CanonKeeper. Установите последнюю версию — данные откроются как есть"
        );
        // Without a candidate there is nothing to bring back.
        assert_eq!(labels(&newer), with_way_out(&["Скачать новую версию"]));
        // A version that is no version is not said, and a candidate with a name that is no plain name is none.
        let odd = with(
            "newer_schema",
            None,
            Said {
                opened: Some("1.3.0 и всё".into()),
                copy: Some(BackupCopy { name: "../pg".into(), ..copy() }),
                ..Said::default()
            },
        );
        assert_eq!(page(&odd, System::Linux, LINUX_DATA, "1.2.0"), newer);
        assert_eq!(
            page(&fatal("version_mismatch", None), System::Linux, LINUX_DATA, "").words,
            "Установка CanonKeeper не закончена. Установите CanonKeeper ещё раз — данные целы"
        );
        assert_eq!(
            page(&fatal("restore_copy", None), System::Linux, LINUX_DATA, "1.2.0").words,
            "Копия данных повреждена, вернуть данные из неё нельзя. Данные не изменились"
        );
        assert_eq!(
            page(&fatal("restore_space", None), System::Linux, LINUX_DATA, "1.2.0").words,
            "Не хватает места на диске, чтобы вернуть данные из копии. Данные не изменились"
        );
    }

    #[test]
    fn the_buttons_of_every_failure_stand_as_the_board_draws_them() {
        // specs/2026-11-13-desk-updates.md UB0.1, the board 44·40: the way out — the folder of the data and «Написать нам» —
        // in a row under the words; «Закрыть» first at the bottom, then the actions of the reason, the main one last; a
        // failure without an action of its reason has no main button, an erasure that did not end only «Закрыть».
        let opened = Said { opened: Some("1.2.0".into()), ..Said::default() };
        let newer = Said { opened: Some("1.3.0".into()), copy: Some(copy()), ..Said::default() };
        let table: Vec<(Fault, Vec<Act>, Option<Act>)> = vec![
            (Fault::Program, vec![], None),
            (Fault::Unknown, vec![], None),
            (fatal("database", None), vec![], None),
            (fatal("migrate_space", None), vec![], None),
            (with("backup", None, opened.clone()), vec![Act::Download], Some(Act::Download)),
            (
                with("migrate", Some("pre-0142-20261113T140500Z"), opened),
                vec![Act::Copy, Act::Download],
                Some(Act::Download),
            ),
            (with("newer_schema", None, newer), vec![Act::Restore, Act::Download], Some(Act::Download)),
            (fatal("newer_schema", None), vec![Act::Download], Some(Act::Download)),
            (fatal("version_mismatch", None), vec![Act::Download], Some(Act::Download)),
            (fatal("restore_space", None), vec![], None),
            (
                with("restore_copy", None, Said { copy: Some(copy()), ..Said::default() }),
                vec![Act::Copy],
                Some(Act::Copy),
            ),
            (with("restore", None, Said { copy: Some(copy()), ..Said::default() }), vec![Act::Copy], Some(Act::Copy)),
            (Fault::NotErased, vec![], None),
        ];
        for (fault, reason, main) in table {
            let shown = page(&fault, System::Linux, LINUX_DATA, "1.3.0");
            let at = |place: Place| {
                shown.buttons.iter().filter(move |button| button.place == place).map(|button| button.act)
            };
            assert_eq!(at(Place::WayOut).collect::<Vec<_>>(), vec![Act::Folder, Act::Contact], "{fault:?}");
            let bottom: Vec<Act> = at(Place::Footer).collect();
            assert_eq!(bottom, [vec![Act::Close], reason].concat(), "{fault:?}");
            let mains: Vec<Act> = shown.buttons.iter().filter(|button| button.main).map(|button| button.act).collect();
            assert_eq!(mains, main.into_iter().collect::<Vec<_>>(), "{fault:?}");
            if main.is_some() {
                assert!(shown.buttons.last().unwrap().main, "{fault:?}: the main button is not the last");
            }
            // The window shows the way out first, then the bottom.
            let places: Vec<Place> = shown.buttons.iter().map(|button| button.place).collect();
            assert!(places.windows(2).all(|pair| pair[0] == pair[1] || pair[0] == Place::WayOut), "{fault:?}");
        }
        for fault in [Fault::Unerased { folder: LINUX_DATA.into() }, Fault::KeyKept] {
            let shown = page(&fault, System::Linux, LINUX_DATA, "1.3.0");
            assert_eq!(shown.buttons, vec![Button::new(Act::Close, "Закрыть")], "{fault:?}");
        }
    }

    #[test]
    fn an_installation_cut_short_names_the_later_of_the_two_versions() {
        // UB0.2: the shell 1.2.0 with the backend 1.3.0 and the shell 1.3.0 with the backend 1.2.0 both say 1.3.0.
        for (shell, backend) in [("1.2.0", "1.3.0"), ("1.3.0", "1.2.0"), ("1.3.0", "1.3.0")] {
            let fault = with("version_mismatch", None, Said { program: Some(backend.into()), ..Said::default() });
            let shown = page(&fault, System::Windows, WINDOWS_DATA, shell);
            assert_eq!(
                shown.words, "Установка CanonKeeper не закончена. Установите версию 1.3.0 ещё раз — данные целы",
                "{shell} {backend}"
            );
            assert_eq!(labels(&shown), with_way_out(&["Скачать версию 1.3.0"]), "{shell} {backend}");
            assert_eq!(
                download(&fault, shell).as_deref(),
                Some("https://github.com/canonkeeper/desktop-app/releases/tag/v1.3.0"),
                "{shell} {backend}"
            );
        }
        // 1.10.0 is later than 1.9.0, a version that is no version is not said, a backend that said none leaves the shell.
        assert_eq!(bigger(Some("1.9.0"), Some("1.10.0")), Some("1.10.0"));
        assert_eq!(bigger(Some("1.3.0"), Some("../2.0.0")), Some("1.3.0"));
        assert_eq!(bigger(Some("1.3.0"), None), Some("1.3.0"));
        assert_eq!(bigger(Some(""), Some("1.2.0")), Some("1.2.0"));
        assert_eq!(bigger(Some("1.3.0-rc.1"), Some("1.3.0")), Some("1.3.0"));
    }

    #[test]
    fn the_steps_of_a_start_say_what_goes_on() {
        // UG3.1.
        let version = Some("1.3.0".to_string());
        assert_eq!(
            step_words(&Step::Backup { version: version.clone() }),
            "Сохраняю копию данных перед обновлением до версии 1.3.0 — это может занять несколько минут"
        );
        assert_eq!(step_words(&Step::Migrate { version }), "Обновляю данные до версии 1.3.0");
        assert_eq!(
            step_words(&Step::Restore { copy: Some(copy()) }),
            "Возвращаю данные из копии от 13 ноября, 14:05 — это может занять несколько минут"
        );
        assert_eq!(
            step_words(&Step::Backup { version: None }),
            "Сохраняю копию данных перед обновлением — это может занять несколько минут"
        );
        assert_eq!(step_words(&Step::Migrate { version: Some("../x".into()) }), "Обновляю данные");
        assert_eq!(
            step_words(&Step::Restore { copy: None }),
            "Возвращаю данные из копии — это может занять несколько минут"
        );
    }

    // specs/2026-11-17-desk-update-linux.md UL1.3 (the board 44·39): the steps of an installation of a package.
    #[test]
    fn the_steps_of_an_installation_of_a_package_say_what_goes_on() {
        assert_eq!(stopping_words("1.3.0"), "Сохраняю данные и останавливаю помощника, чтобы установить версию 1.3.0");
        assert_eq!(
            package_words(package::Step::Password, "1.3.0"),
            "Система спрашивает пароль администратора, чтобы установить версию 1.3.0"
        );
        assert_eq!(
            package_words(package::Step::Running, "1.3.0"),
            "Устанавливаю версию 1.3.0. Окно откроется снова, когда установка закончится"
        );
        assert_eq!(
            package_words(package::Step::LockWait, "1.3.0"),
            "Жду, пока закончится другая установка программ (до 2\u{00a0}минут). Если она ждёт ответа в терминале, ответьте там"
        );
        assert_eq!(stopping_words(""), "Сохраняю данные и останавливаю помощника, чтобы установить новую версию");
        assert_eq!(
            package_words(package::Step::Running, "../x"),
            "Устанавливаю новую версию. Окно откроется снова, когда установка закончится"
        );
    }

    #[test]
    fn an_unknown_reason_takes_the_general_words_and_the_three_buttons() {
        for reason in ["local_author", "backend", "data_directory", "start_expected", "something_new"] {
            let shown = page(&fatal(reason, None), System::Linux, LINUX_DATA, "1.3.0");
            assert_eq!(shown.words, format!("CanonKeeper не смог запуститься. Книги на месте: {LINUX_DATA}"));
            assert_eq!(labels(&shown), WAY_OUT.to_vec(), "{reason}");
        }
        assert_eq!(
            page(&Fault::Unknown, System::Linux, LINUX_DATA, "").words,
            page(&fatal("x", None), System::Linux, LINUX_DATA, "").words
        );
    }

    #[test]
    fn the_words_name_the_data_directory_and_the_copy_only() {
        // A name that is not a plain directory name points to the directory of the copies, never elsewhere, and opens
        // no copy.
        for name in [r"..\..\Documents\Книга.docx", "/etc/passwd", "..", "a b", ""] {
            let shown = page(&fatal("migrate", Some(name)), System::Linux, LINUX_DATA, "1.3.0");
            assert!(shown.words.ends_with(&format!("Копия данных: {LINUX_DATA}/backups")), "{}", shown.words);
            assert!(!shown.has(Act::Copy), "{name}");
            assert_eq!(copy_name(&fatal("migrate", Some(name))), None);
        }
        // Whatever the backend puts into a reason, only the table speaks.
        let leaked = fatal("database: could not open /home/тест/Документы/Роман.docx", None);
        let said = page(&leaked, System::Linux, LINUX_DATA, "").words;
        assert!(!said.contains("Роман") && !said.contains("Документы"), "{said}");
        for fault in
            [Fault::Program, Fault::Unknown, fatal("listen", None), fatal("backup", None), fatal("newer_schema", None)]
        {
            let said = page(&fault, System::Linux, LINUX_DATA, "").words.replace(LINUX_DATA, "");
            assert!(!said.contains('/'), "{fault:?}: {said}");
        }
    }

    #[test]
    fn download_opens_the_release_of_the_version_of_its_button() {
        let earlier = with("migrate", None, Said { opened: Some("1.2.0".into()), ..Said::default() });
        assert_eq!(
            download(&earlier, "1.3.0").as_deref(),
            Some("https://github.com/canonkeeper/desktop-app/releases/tag/v1.2.0")
        );
        assert_eq!(download(&fatal("backup", None), "1.3.0").as_deref(), Some(RELEASES_URL));
        assert_eq!(
            download(&fatal("newer_schema", None), "1.2.0").as_deref(),
            Some("https://github.com/canonkeeper/desktop-app/releases/latest")
        );
        assert_eq!(
            download(&fatal("version_mismatch", None), "1.3.0").as_deref(),
            Some("https://github.com/canonkeeper/desktop-app/releases/tag/v1.3.0")
        );
        let forged = with("migrate", None, Said { opened: Some("1.2.0/../../evil".into()), ..Said::default() });
        assert_eq!(download(&forged, "1.3.0").as_deref(), Some(RELEASES_URL));
        for fault in [fatal("database", None), fatal("restore", None), Fault::Unknown, Fault::NotErased] {
            assert_eq!(download(&fault, "1.3.0"), None, "{fault:?}");
        }
    }

    #[test]
    fn dates_and_sizes_read_as_the_author_reads_them() {
        assert_eq!(date("2026-11-13T14:05:00+03:00").as_deref(), Some("13 ноября, 14:05"));
        assert_eq!(date("2026-01-02T09:07:59Z").as_deref(), Some("2 января, 09:07"));
        for wrong in ["", "2026-13-01T10:00:00Z", "2026-11-13", "вчера", "2026-11-13T25:00:00Z"] {
            assert_eq!(date(wrong), None, "{wrong}");
        }
        assert_eq!(size(2_254_857_830), "2,1 ГБ");
        assert_eq!(size(1024 * 1024 * 1024), "1,0 ГБ");
        assert_eq!(size(1024 * 1024 * 1024 + 1), "1,1 ГБ");
        assert_eq!(size(640 * 1024 * 1024), "640 МБ");
        assert_eq!(size(1), "1 МБ");
    }
}
