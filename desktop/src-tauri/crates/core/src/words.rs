// SPDX-License-Identifier: AGPL-3.0-only

//! What the author reads when the backend does not start or falls (specs/2026-11-12-desk-shell.md SH2.1): one table
//! from the reason to the words. The words name only the data directory and the copy made before the migrations —
//! no text of a book, no path of another file, nothing of a log.

use crate::paths::System;

/// «Написать нам» of the failure window: the contact form of the site in the browser of the system.
pub const CONTACT_URL: &str = "https://canonkeeper.ru/?contact=write";

/// Why the window shows a failure.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Fault {
    /// The program of the backend did not start, or ended before it said anything.
    Program,
    /// The backend said `fatal`.
    Fatal { reason: String, backup: Option<String> },
    /// Something the table does not name: the backend fell too often, the window could not open.
    Unknown,
}

/// The words of a fault; `data_dir` is the data directory of the user on `system`.
pub fn words(fault: &Fault, system: System, data_dir: &str) -> String {
    match fault {
        Fault::Program => quarantine(data_dir),
        Fault::Fatal { reason, backup } => match reason.as_str() {
            // A program of the bundle is missing or did not start: the antivirus is the likely cause (DT13.8).
            "resources" | "postgres" => quarantine(data_dir),
            "database" => format!(
                "Не удалось открыть данные CanonKeeper. Книги на месте: {data_dir}. Перезапустите программу; если не поможет — напишите нам"
            ),
            "listen" => "CanonKeeper не смог открыть окно: порты компьютера заняты. Перезапустите программу".to_string(),
            "backup" => "Обновление не применено: не удалось сохранить копию данных. Данные целы. Проверьте место на диске и запустите снова".to_string(),
            "migrate" => format!(
                "Обновление не применилось: данные целы, копия — {}",
                copy_of(system, data_dir, backup.as_deref())
            ),
            "newer_schema" => {
                "Эти данные созданы более новой версией CanonKeeper. Установите последнюю версию".to_string()
            }
            _ => unknown(data_dir),
        },
        Fault::Unknown => unknown(data_dir),
    }
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
fn copy_of(system: System, data_dir: &str, name: Option<&str>) -> String {
    let plain_name = name.filter(|name| {
        !name.is_empty()
            && name.len() <= 128
            && *name != "."
            && *name != ".."
            && name.chars().all(|letter| letter.is_ascii_alphanumeric() || matches!(letter, '-' | '_' | '.'))
    });
    match plain_name {
        Some(name) => system.join(data_dir, &["backups", name]),
        None => system.join(data_dir, &["backups"]),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    const WINDOWS_DATA: &str = r"C:\Users\Тест\AppData\Local\ru.canonkeeper.desktop\data";
    const LINUX_DATA: &str = "/home/тест/.local/share/canonkeeper";

    fn fatal(reason: &str, backup: Option<&str>) -> Fault {
        Fault::Fatal { reason: reason.to_string(), backup: backup.map(str::to_string) }
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
            (
                fatal("backup", None),
                "Обновление не применено: не удалось сохранить копию данных. Данные целы. Проверьте место на диске и запустите снова".to_string(),
            ),
            (
                fatal("migrate", Some("pre-0142-20261112T101500Z")),
                format!(r"Обновление не применилось: данные целы, копия — {WINDOWS_DATA}\backups\pre-0142-20261112T101500Z"),
            ),
            (
                fatal("newer_schema", None),
                "Эти данные созданы более новой версией CanonKeeper. Установите последнюю версию".to_string(),
            ),
        ];
        let mut seen = std::collections::BTreeSet::new();
        for (fault, expected) in table {
            let said = words(&fault, System::Windows, WINDOWS_DATA);
            assert_eq!(said, expected, "{fault:?}");
            seen.insert(said);
        }
        // resources and postgres share the words of the program; the other five are their own.
        assert_eq!(seen.len(), 6);
    }

    #[test]
    fn an_unknown_reason_takes_the_general_words() {
        for reason in ["local_author", "backend", "data_directory", "start_expected", "something_new"] {
            assert_eq!(
                words(&fatal(reason, None), System::Linux, LINUX_DATA),
                format!("CanonKeeper не смог запуститься. Книги на месте: {LINUX_DATA}")
            );
        }
        assert_eq!(
            words(&Fault::Unknown, System::Linux, LINUX_DATA),
            words(&fatal("x", None), System::Linux, LINUX_DATA)
        );
    }

    #[test]
    fn the_words_name_the_data_directory_and_the_copy_only() {
        assert_eq!(
            words(&fatal("migrate", Some("pre-0142-20261112T101500Z")), System::Windows, WINDOWS_DATA),
            format!(
                r"Обновление не применилось: данные целы, копия — {WINDOWS_DATA}\backups\pre-0142-20261112T101500Z"
            )
        );
        assert_eq!(
            words(&fatal("migrate", Some("pre-0142-20261112T101500Z")), System::Linux, LINUX_DATA),
            format!("Обновление не применилось: данные целы, копия — {LINUX_DATA}/backups/pre-0142-20261112T101500Z")
        );
        // A name that is not a plain directory name points to the directory of the copies, never elsewhere.
        for name in [r"..\..\Documents\Книга.docx", "/etc/passwd", "..", "a b", ""] {
            assert_eq!(
                words(&fatal("migrate", Some(name)), System::Linux, LINUX_DATA),
                format!("Обновление не применилось: данные целы, копия — {LINUX_DATA}/backups")
            );
        }
        // Whatever the backend puts into a reason, only the table speaks.
        let leaked = fatal("database: could not open /home/тест/Документы/Роман.docx", None);
        let said = words(&leaked, System::Linux, LINUX_DATA);
        assert!(!said.contains("Роман") && !said.contains("Документы"), "{said}");
        for fault in
            [Fault::Program, Fault::Unknown, fatal("listen", None), fatal("backup", None), fatal("newer_schema", None)]
        {
            let said = words(&fault, System::Linux, LINUX_DATA).replace(LINUX_DATA, "");
            assert!(!said.contains('/'), "{fault:?}: {said}");
        }
    }
}
