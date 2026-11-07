# SPDX-License-Identifier: AGPL-3.0-only
"""Builds the S0 summary (DA1.9 table and facts) from the result artifacts of a run.

  summarize.py <results dir> <summary.md>

<results dir> holds results-probe-<os>/ and results-shell-<os>/ as downloaded by the workflow.
"""
import json
import os
import sys

MB = 1_000_000
TARGETS = {"installer_mb": 130, "disk_mb": 400, "idle_mb": 700, "first_s": 30, "repeat_s": 8}


def load(path):
    try:
        with open(path, encoding="utf-8") as stream:
            return json.load(stream)
    except (OSError, ValueError):
        return None


def text(path):
    try:
        with open(path, encoding="utf-8") as stream:
            return stream.read()
    except OSError:
        return ""


def mb(value):
    return None if value is None else round(value / MB, 1)


def fmt(value, unit=""):
    return "—" if value is None else f"{value}{unit}"


def mark(value, limit):
    if value is None:
        return "—"
    return f"{value} {'✅' if value <= limit else '❌'}"


def systems(root):
    names = set()
    for entry in os.listdir(root):
        for prefix in ("results-probe-", "results-shell-"):
            if entry.startswith(prefix):
                names.add(entry[len(prefix):])
    return sorted(names)


def pg_facts(pg):
    if not pg:
        return {}
    primary = pg["variants"][0]
    facts = {
        "initdb_s": primary.get("initdb_seconds"),
        "start_s": primary.get("first_start_seconds"),
        "restart_s": primary.get("second_start_seconds"),
        "idle_rss": (primary.get("idle_memory") or {}).get("rss_bytes"),
        "checks_ok": primary.get("checks_ok"),
        "error": primary.get("error"),
        "tsvector": {variant["variant"]: (variant.get("checks") or {}).get("to_tsvector") for variant in pg["variants"]},
        "locale": {variant["variant"]: (variant.get("checks") or {}).get("database_locale") for variant in pg["variants"]},
        "mismatches": {variant["variant"]: variant.get("check_mismatches") or variant.get("error") for variant in pg["variants"] if not variant.get("checks_ok")},
    }
    return facts


def codex_facts(codex):
    if not codex:
        return {}
    turns = codex.get("turns") or []
    return {
        "error": codex.get("error"),
        "init_s": codex.get("initialize_seconds"),
        "idle_rss": (codex.get("idle_memory_before_turn") or {}).get("rss_bytes"),
        "after_rss": (codex.get("idle_memory_after_turns") or {}).get("rss_bytes"),
        "turns": [turn.get("status") for turn in turns],
        "parallel": max([turn.get("max_tool_calls_in_flight", 0) for turn in turns] or [0]),
        "header": (codex.get("thread_header") or {}).get("in_every_request"),
        "outbound": codex.get("non_loopback_connections"),
        "samples": codex.get("connection_samples"),
        "warnings": sorted({warning for turn in turns for warning in (turn.get("warnings") or [])}),
    }


def main():
    root, out = sys.argv[1], sys.argv[2]
    rows = []
    facts = []
    for system in systems(root):
        probe = os.path.join(root, f"results-probe-{system}")
        shell = os.path.join(root, f"results-shell-{system}")
        pg = pg_facts(load(os.path.join(probe, "pg.json")))
        codex = codex_facts(load(os.path.join(probe, "codex.json")))
        sizes = load(os.path.join(shell, "sizes.json")) or {}
        launch = load(os.path.join(shell, "shell.json")) or {}
        runs = launch.get("runs") or []
        installers = sizes.get("installers") or {}
        main_installer = None
        for name, size in installers.items():
            if name.endswith((".dmg", "-setup.exe", ".AppImage")):
                main_installer = (name, size)
        installed = (sizes.get("installed") or {}).get("app") or {}
        shell_idle = ((runs[0] if runs else {}).get("idle_memory") or {}).get("rss_bytes")
        first_window = (runs[0] if runs else {}).get("spawn_to_page_dom_seconds")
        repeat_window = (runs[1] if len(runs) > 1 else {}).get("spawn_to_page_dom_seconds")
        idle_parts = [shell_idle, pg.get("idle_rss"), codex.get("idle_rss")]
        idle_total = mb(sum(part for part in idle_parts if part)) if all(idle_parts) else None
        first_parts = [first_window, pg.get("initdb_s"), pg.get("start_s"), codex.get("init_s")]
        first_total = round(sum(first_parts), 1) if all(part is not None for part in first_parts) else None
        repeat_parts = [repeat_window, pg.get("restart_s"), codex.get("init_s")]
        repeat_total = round(sum(repeat_parts), 1) if all(part is not None for part in repeat_parts) else None
        rows.append([
            system,
            mark(mb(main_installer[1]) if main_installer else None, TARGETS["installer_mb"]),
            mark(mb(installed.get("bytes")), TARGETS["disk_mb"]),
            f"{mark(idle_total, TARGETS['idle_mb'])} ({fmt(mb(shell_idle))} + {fmt(mb(pg.get('idle_rss')))} + {fmt(mb(codex.get('idle_rss')))})",
            f"{mark(first_total, TARGETS['first_s'])} ({fmt(first_window)} + {fmt(pg.get('initdb_s'))} + {fmt(pg.get('start_s'))} + {fmt(codex.get('init_s'))})",
            f"{mark(repeat_total, TARGETS['repeat_s'])} ({fmt(repeat_window)} + {fmt(pg.get('restart_s'))} + {fmt(codex.get('init_s'))})",
        ])
        page = (runs[0] if runs else {}).get("page") or {}
        system_facts = [f"### {system}", ""]
        system_facts.append(f"- installers: {json.dumps(installers, ensure_ascii=False)}")
        system_facts.append(f"- PostgreSQL checks ok: {pg.get('checks_ok')}; mismatches: {json.dumps(pg.get('mismatches'), ensure_ascii=False)}")
        system_facts.append(f"- to_tsvector by LC_CTYPE: {json.dumps(pg.get('tsvector'), ensure_ascii=False)}")
        cyrillic = load(os.path.join(probe, "pg-cyrillic-user.json"))
        if cyrillic or os.path.exists(os.path.join(probe, "pg-cyrillic-user-error.txt")):
            variant = (cyrillic or {}).get("variants", [{}])[0]
            system_facts.append(f"- PostgreSQL as user «Тестер»: root {json.dumps((cyrillic or {}).get('root'), ensure_ascii=False)}, checks ok {variant.get('checks_ok')}, error {variant.get('error') or text(os.path.join(probe, 'pg-cyrillic-user-error.txt')).strip() or None}")
        system_facts.append(f"- Codex: turns {codex.get('turns')}, parallel tool calls {codex.get('parallel')}, thread id carriers {codex.get('header')}, non-loopback sockets {codex.get('outbound')} in {codex.get('samples')} samples, error {codex.get('error')}")
        strace = load(os.path.join(probe, "codex-strace.json"))
        if strace:
            system_facts.append(f"- strace (loopback-only netns): outbound {strace.get('outbound')}, programs {strace.get('programs')}")
        nobwrap = codex_facts(load(os.path.join(probe, "codex-nobwrap.json")))
        if nobwrap:
            system_facts.append(f"- without bwrap (runner had: {text(os.path.join(probe, 'bwrap-on-runner.txt')).strip()}): turns {nobwrap.get('turns')}, error {nobwrap.get('error')}, warnings {nobwrap.get('warnings')}")
        for label in ("adhoc", "hardened"):
            variant = codex_facts(load(os.path.join(probe, f"codex-{label}.json")))
            if variant:
                system_facts.append(f"- codex-app-server {label} signature: turns {variant.get('turns')}, error {variant.get('error')}")
        system_facts.append(f"- WebGL page: stage {page.get('stage')}, webgl {json.dumps(page.get('webgl'), ensure_ascii=False)}, drawn pixels {page.get('drawn_pixels')}, errors {page.get('errors')}")
        bundle_pg = load(os.path.join(shell, "bundle-pg.json"))
        bundle_codex = load(os.path.join(shell, "bundle-codex.json"))
        if bundle_pg or bundle_codex:
            system_facts.append(f"- from the installed bundle: PostgreSQL checks ok {((bundle_pg or {}).get('variants') or [{}])[0].get('checks_ok')}, Codex turns {[t.get('status') for t in (bundle_codex or {}).get('turns') or []]}")
        for name in ("macos-signature.txt", "macos-after-update.txt"):
            content = text(os.path.join(shell, name))
            if content:
                verify = [line for line in content.splitlines() if line.startswith(("verify:", "version:", "quarantine", "## quarantine"))]
                system_facts.append(f"- {name}: {'; '.join(verify)}")
        for name in ("keychain-write.json", "keychain-read-0.1.0.json", "keychain-read-after-update.json"):
            content = load(os.path.join(shell, name))
            if content:
                system_facts.append(f"- {name}: {json.dumps(content, ensure_ascii=False)}")
        update = load(os.path.join(shell, "update.json"))
        if update:
            system_facts.append(f"- update: {json.dumps(update, ensure_ascii=False)}")
        windows = load(os.path.join(shell, "windows-after-update.json"))
        if windows:
            system_facts.append(f"- Windows after update: {json.dumps(windows, ensure_ascii=False)}")
        facts.append("\n".join(system_facts))

    lines = [
        "# S0 probe summary",
        "",
        "Targets of DA1.9: installer ≤ 130 MB, app on disk ≤ 400 MB, idle memory ≤ 700 MB, first launch ≤ 30 s, repeat ≤ 8 s.",
        "Measured parts: shell + PostgreSQL + codex-app-server (the backend is measured on Linux separately).",
        "",
        "| System | Installer, MB | App on disk, MB | Idle RSS, MB (shell + PostgreSQL + Codex) | First launch, s (window + initdb + start + Codex) | Repeat launch, s (window + start + Codex) |",
        "|---|---|---|---|---|---|",
    ]
    lines += ["| " + " | ".join(row) + " |" for row in rows]
    lines += ["", "## Facts by system", ""] + facts
    with open(out, "w", encoding="utf-8") as stream:
        stream.write("\n".join(lines) + "\n")
    print("\n".join(lines))


if __name__ == "__main__":
    main()
