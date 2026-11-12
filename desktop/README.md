<!-- SPDX-License-Identifier: AGPL-3.0-only -->
# Оболочка настольного приложения CanonKeeper

Оболочка на Tauri 2: запускает бэкенд (`backend/cmd/desktop`) рядом с собой, открывает окно на его адресе
`http://127.0.0.1:P/`, говорит понятными словами, если бэкенд не стартовал, и собирается в установщики Windows (NSIS
для текущего пользователя) и Linux (`.deb`, `.rpm`, AppImage). Бизнес-логики в ней нет.

- `src-tauri/` — крейт окна; `src-tauri/crates/core/` — всё, что можно проверить без окна: канал управления, пути,
  слова при сбоях, куда окну можно ходить, надзор за бэкендом.
- `app/` — страница, которую оболочка несёт с собой: «CanonKeeper запускается…» и окно ошибки.
- `release.json` — что приходит извне с закреплённым sha256: пакет Codex (версии `backend/go.mod`) и исходники
  PostgreSQL; `postgres/` собирает из них набор PostgreSQL.
- `ci/desktop-build.yml` — workflow публичного репозитория.

## Что нужно для сборки на Linux (Ubuntu 24.04)

- Rust 1.96, Node.js версии `.node-version`, Go версии `backend/go.mod`, `gh` (скачать пакет Codex).
- Пакеты системы:
  `sudo apt install build-essential file libwebkit2gtk-4.1-dev librsvg2-dev patchelf rpm`.
- Для набора PostgreSQL: `sudo apt install bison flex python3-pip` и `pip install meson ninja`.
- Tauri CLI 2.12.4: `npm ci --prefix desktop` (из `desktop/package-lock.json`).

## Как собрать и запустить

1. Ядро оболочки — на любой машине, без системных библиотек окна:
   `cd desktop/src-tauri && cargo test -p canonkeeper-desktop-core`.
2. Набор PostgreSQL из закреплённых исходников:
   `python3 -I desktop/postgres/build.py /tmp/pg-work /tmp/pg-out && tar -cf /tmp/pg-set.tar -C /tmp/pg-out .`
3. Пакет Codex — тег и имя из `desktop/release.json`, например:
   `gh release download rust-v0.165.4 -R Dirard/codex -p codex-package-x86_64-unknown-linux-gnu.tar.gz -D /tmp/codex`.
4. Набор: `node scripts/desktop-app.mjs --target x86_64-unknown-linux-gnu --postgres /tmp/pg-set.tar --codex
   /tmp/codex/codex-package-x86_64-unknown-linux-gnu.tar.gz`. Сценарий сверяет sha256 с `release.json` (несовпадение —
   отказ), собирает бэкенд с вебом и раскладывает всё в `desktop/bundle`. Сам он ничего не скачивает.
5. Установщики: `cd desktop && npx tauri build --config src-tauri/tauri.bundle.json --bundles deb,rpm`; AppImage —
   `--bundles appimage`. Готовое — в `desktop/src-tauri/target/release/bundle/`.
6. Запуск: `sudo apt install ./desktop/src-tauri/target/release/bundle/deb/*.deb`, затем `canonkeeper`. Данные — в
   `~/.local/share/canonkeeper` (на Windows — `%LOCALAPPDATA%\ru.canonkeeper.desktop\data`, отдельно от программы),
   журнал оболочки — `logs/shell-<дата>.log` там же: в нём только события, без адресов, кодов и текстов. Файлы, которые
   сохраняет приложение («Скачать книгу…», файл главы), ложатся в папку загрузок системы под свободным именем; на Linux
   без папки загрузок — в домашнюю папку.

## Что проверяет CI

Задания `ubuntu-24.04` и `windows-2025`: формат и тесты ядра, clippy окна; набор PostgreSQL из исходников по sha256;
набор приложения по закреплённым sha256; `tauri build`; дымовой прогон установленного приложения — запуск, окно
получило страницу бэкенда после обмена кода, остановка без оставшихся процессов бэкенда, PostgreSQL и Codex; на
Windows — что удаление без флажка «Удалить данные приложения» оставляет данные. Установщики и журналы — артефакты
запуска. Секретов workflow не требует, сторонние действия закреплены по коммиту.

Пока у версии Codex из `backend/go.mod` нет пакета Windows, задание Windows останавливается на наборе с этой причиной.
