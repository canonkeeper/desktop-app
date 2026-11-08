# SPDX-License-Identifier: AGPL-3.0-only
"""Builds the DA1.9 table of the whole app from the result artifacts of a run of the `DA1.9 app measure` workflow.

  app_summary.py <results dir> <summary.md> <da19.json>

<results dir> holds results-app-<os>/ as downloaded by the workflow: app.json (app_probe.py), sizes.json (sizes.py),
codex-package.json, pg-build.json and build-time.json. A value that could not be measured is written as unavailable
with its reason.
"""
import json
import os
import sys

MB = 1_000_000
TARGETS = {"installer_mb": 130, "appimage_mb": 180, "disk_mb": 400, "idle_mb": 700, "first_s": 30, "repeat_s": 8}
LABELS = {
    "windows-2025": "Windows x64 (`windows-2025`)",
    "macos-15": "macOS arm64 (`macos-15`)",
    "macos-15-intel": "macOS x64 (`macos-15-intel`)",
    "ubuntu-24.04": "Linux x64 (`ubuntu-24.04`)",
}
INSTALLERS = ((".exe", "NSIS"), (".dmg", ".dmg"), (".deb", ".deb"), (".rpm", ".rpm"), (".AppImage", "AppImage"))


def load(path):
    try:
        with open(path, encoding="utf-8") as stream:
            return json.load(stream)
    except (OSError, ValueError):
        return None


def mb(value):
    return None if value is None else round(value / MB, 1)


def mark(value, limit):
    return f"{value} {'✅' if value <= limit else '❌'}"


def measure(directory):
    app = load(os.path.join(directory, "app.json")) or {}
    sizes = load(os.path.join(directory, "sizes.json")) or {}
    runs = app.get("runs", [])
    system = {"installers_mb": {}, "parts_mb": {}, "unavailable": {}}
    for name, size in sorted(sizes.get("installers", {}).items()):
        for suffix, label in INSTALLERS:
            if name.endswith(suffix) and not name.endswith(".sig"):
                system["installers_mb"][label] = mb(size)
    if not system["installers_mb"]:
        system["unavailable"]["installer"] = "no installer in the bundle (the build failed)"
    installed = sizes.get("installed", {})
    system["disk_mb"] = mb(installed.get("app", {}).get("bytes"))
    if system["disk_mb"] is None:
        system["unavailable"]["disk"] = installed.get("app", {}).get("error", "the app was not installed")
    for part in ("backend", "postgres", "codex", "codex-homes"):
        system["parts_mb"][part] = mb(installed.get(part, {}).get("bytes"))
    system["data_dir_after_first_mb"] = mb(app.get("data_dir_after_first_bytes"))
    system["runs"] = []
    for run in runs:
        memory = run.get("idle_memory") or {}
        system["runs"].append({
            "kind": run.get("kind"),
            "backend_ready_s": run.get("backend_ready_seconds"),
            "first_page_s": run.get("first_page_seconds"),
            "window_s": run.get("window_seconds"),
            "session_status": run.get("session_status"),
            "stop_s": run.get("stop_seconds"),
            "left_after_stop": run.get("left_after_stop"),
            "idle_rss_mb": mb(memory.get("rss_bytes")),
            "idle_uss_mb": mb(memory.get("uss_bytes")),
            "idle_pss_mb": mb(memory.get("pss_bytes")),
            "idle_parts_rss_mb": {part: mb(value.get("rss_bytes")) for part, value in memory.get("parts", {}).items()},
            "idle_parts_processes": {part: value.get("processes") for part, value in memory.get("parts", {}).items()},
            "error": run.get("error"),
        })
    first = system["runs"][0] if system["runs"] else {}
    repeat = system["runs"][-1] if len(system["runs"]) > 1 else {}
    system["first_s"] = first.get("window_s")
    system["repeat_s"] = repeat.get("window_s")
    if system["first_s"] is None:
        system["unavailable"]["first"] = first.get("error") or "no result"
    if system["repeat_s"] is None:
        system["unavailable"]["repeat"] = repeat.get("error") or "no result"
    # The memory of the table is the idle memory of the repeat launch, an everyday start; the first is kept beside it.
    idle = repeat if repeat.get("idle_rss_mb") else first
    system["idle_mb"] = idle.get("idle_rss_mb")
    system["idle_parts_mb"] = idle.get("idle_parts_rss_mb", {})
    system["idle_of"] = idle.get("kind")
    if system["idle_mb"] is None:
        system["unavailable"]["idle"] = idle.get("error") or "no result"
    system["build_seconds"] = (load(os.path.join(directory, "build-time.json")) or {}).get("tauri_build_seconds")
    system["codex"] = load(os.path.join(directory, "codex-package.json"))
    return system


def cells(system):
    unavailable = system["unavailable"]
    installers = []
    for label, size in system["installers_mb"].items():
        installers.append(f"{label} {mark(size, TARGETS['appimage_mb'] if label == 'AppImage' else TARGETS['installer_mb'])}")
    installer = " / ".join(installers) or f"недоступно: {unavailable.get('installer')}"
    disk = mark(system["disk_mb"], TARGETS["disk_mb"]) if system["disk_mb"] is not None else f"недоступно: {unavailable['disk']}"
    if system["idle_mb"] is not None:
        parts = system["idle_parts_mb"]
        idle = " + ".join(str(parts.get(part)) for part in ("window", "backend", "postgres", "codex"))
        idle = f"{idle} = {mark(system['idle_mb'], TARGETS['idle_mb'])}"
    else:
        idle = f"недоступно: {unavailable['idle']}"
    first = mark(system["first_s"], TARGETS["first_s"]) if system["first_s"] is not None else f"недоступно: {unavailable['first']}"
    repeat = mark(system["repeat_s"], TARGETS["repeat_s"]) if system["repeat_s"] is not None else f"недоступно: {unavailable['repeat']}"
    return installer, disk, idle, first, repeat


def main():
    root, summary_path, json_path = sys.argv[1:4]
    systems = {}
    for entry in sorted(os.listdir(root)) if os.path.isdir(root) else []:
        if entry.startswith("results-app-"):
            systems[entry[len("results-app-"):]] = measure(os.path.join(root, entry))
    server = os.environ.get("GITHUB_SERVER_URL", "https://github.com")
    run = {
        "id": os.environ.get("GITHUB_RUN_ID"),
        "url": f"{server}/{os.environ.get('GITHUB_REPOSITORY')}/actions/runs/{os.environ.get('GITHUB_RUN_ID')}",
        "sha": os.environ.get("GITHUB_SHA"),
    }
    with open(json_path, "w", encoding="utf-8") as stream:
        json.dump({"run": run, "targets": TARGETS, "systems": systems}, stream, ensure_ascii=False, indent=2)

    lines = [
        "## DA1.9 — приложение целиком",
        "",
        "Оболочка пробы с бэкендом `cmd/desktop` (sidecar), PostgreSQL 18, `codex-app-server` и профилем `research-chat` "
        "в установщике. Первый запуск — от запуска оболочки до окна с приложением локального автора (бэкенд создал "
        "кластер, применил миграции, окно обменяло одноразовый код на сессию); повторный — то же с готовым кластером. "
        "Память — сумма RSS процессов окна, бэкенда, PostgreSQL и Codex через 10 с покоя после окна, повторный запуск.",
        "",
        "| Система | Установщик, МБ (≤ 130; AppImage ≈ 180) | На диске, МБ (≤ 400) | Память в покое, МБ (≤ 700): окно + бэкенд + PostgreSQL + Codex | Первый запуск, с (≤ 30) | Повторный, с (≤ 8) |",
        "|---|---|---|---|---|---|",
    ]
    for name, system in systems.items():
        lines.append(f"| {LABELS.get(name, name)} | " + " | ".join(cells(system)) + " |")
    lines += [
        "",
        "| Система | Запуск | `ready` бэкенда, с | Первая страница, с | Окно с приложением, с | Сессия | RSS / USS / PSS, МБ | Процессы: окно, бэкенд, PostgreSQL, Codex | Остановка, с | Осталось |",
        "|---|---|---|---|---|---|---|---|---|---|",
    ]
    for name, system in systems.items():
        for item in system["runs"]:
            processes = item["idle_parts_processes"]
            counts = ", ".join(str(processes.get(part, "—")) for part in ("window", "backend", "postgres", "codex"))
            memory = " / ".join("—" if value is None else str(value) for value in (item["idle_rss_mb"], item["idle_uss_mb"], item["idle_pss_mb"]))
            lines.append(
                f"| {LABELS.get(name, name)} | {item['kind']} | {item['backend_ready_s']} | {item['first_page_s']} | "
                f"{item['window_s']} | {item['session_status']} | {memory} | {counts} | {item['stop_s']} | "
                f"{', '.join(item['left_after_stop'] or []) or '—'} |"
            )
            if item.get("error"):
                lines.append(f"| | | ошибка: {item['error']} | | | | | | | |")
    lines += [
        "",
        "| Система | Бэкенд, МБ | PostgreSQL, МБ | Codex, МБ | Профиль, МБ | Каталог данных после первого запуска, МБ | Сборка `tauri build`, с |",
        "|---|---|---|---|---|---|---|",
    ]
    for name, system in systems.items():
        parts = system["parts_mb"]
        lines.append(
            f"| {LABELS.get(name, name)} | {parts['backend']} | {parts['postgres']} | {parts['codex']} | "
            f"{parts['codex-homes']} | {system['data_dir_after_first_mb']} | {system['build_seconds']} |"
        )
    if not systems:
        lines.append("\nНет результатов: ни одна система не дошла до замера.")
    with open(summary_path, "w", encoding="utf-8") as stream:
        stream.write("\n".join(lines) + "\n")
    print("\n".join(lines))


if __name__ == "__main__":
    main()
