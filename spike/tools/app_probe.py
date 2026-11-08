# SPDX-License-Identifier: AGPL-3.0-only
"""DA1.9 measurement of the whole desktop app: launches the installed shell, which starts the backend sidecar, and
measures the first launch (the backend creates the PostgreSQL cluster) and the repeat launches.

  app_probe.py --exe <installed shell> --resources <installed resources> --out app.json [--runs 2] [--screenshot <prefix>]

For every launch: times from the spawn of the shell until the backend said `ready`, until the first page of the
backend finished loading in the window and until the window shows the app of the local author (the page after the
one-time code was exchanged for the session); then, after --idle seconds, the memory of the window (the shell and its
WebView processes), the backend, PostgreSQL and Codex; then the shell is killed — the stdin of the backend closes — and
the stop of the backend, Codex and PostgreSQL is timed. The data directory is the product default (DA1, «Data»): it is
removed before the first launch and measured after it; its logs are copied next to the result.
"""
import argparse
import json
import os
import shutil
import sys
import time
from urllib.parse import parse_qs, urlparse

import psutil

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from shell_probe import WEBKIT_XPC, read_report, screenshot, spawn  # noqa: E402


def default_data_dir():
    """The data directory of DA1 as backend/internal/app/desktop.DefaultDataDir computes it."""
    if sys.platform == "win32":
        return os.path.join(os.environ["LOCALAPPDATA"], "CanonKeeper")
    if sys.platform == "darwin":
        return os.path.expanduser("~/Library/Application Support/CanonKeeper")
    data = os.environ.get("XDG_DATA_HOME", "")
    return os.path.join(data if os.path.isabs(data) else os.path.expanduser("~/.local/share"), "canonkeeper")


def tree_bytes(path):
    total = 0
    for directory, _, names in os.walk(path):
        for name in names:
            full = os.path.join(directory, name)
            if not os.path.islink(full) and os.path.isfile(full):
                total += os.path.getsize(full)
    return total


def same_tree(path, root):
    try:
        path, root = os.path.normcase(os.path.realpath(path)), os.path.normcase(os.path.realpath(root))
        return os.path.commonpath([path, root]) == root
    except (ValueError, OSError):
        return False


def family(shell_pid, resources, spawned_at):
    """The processes of the app by part: the window (the shell, its WebView processes and, on macOS, the WebKit XPC
    processes launchd started for it), the backend, Codex (the backend's codex-app-server and its children) and the
    PostgreSQL of the bundle, which pg_ctl started apart from the backend."""
    parts = {"window": {}, "backend": {}, "codex": {}, "postgres": {}}
    try:
        shell = psutil.Process(shell_pid)
    except psutil.Error:
        return parts
    parts["window"][shell.pid] = shell

    def walk(process, part):
        try:
            children = process.children()
        except psutil.Error:
            return
        for child in children:
            try:
                name = child.name().lower()
            except psutil.Error:
                continue
            child_part = part
            if name.startswith("canonkeeper-backend"):
                child_part = "backend"
            elif name.startswith("codex-app-server"):
                child_part = "codex"
            elif name.startswith("postgres"):
                child_part = "postgres"
            parts[child_part][child.pid] = child
            walk(child, child_part)

    walk(shell, "window")
    postgres_bin = os.path.join(resources, "postgres", "bin")
    for process in psutil.process_iter(["name", "create_time"]):
        try:
            name = (process.info["name"] or "").lower()
            created = process.info["create_time"] or 0
            if sys.platform == "darwin" and any(marker.lower() in name for marker in WEBKIT_XPC) and created >= spawned_at - 1:
                parts["window"].setdefault(process.pid, process)
            elif name in ("postgres", "postgres.exe") and process.pid not in parts["postgres"]:
                try:
                    ours = same_tree(process.exe(), postgres_bin)
                except psutil.Error:
                    ours = created >= spawned_at - 1
                if ours:
                    parts["postgres"][process.pid] = process
        except psutil.Error:
            pass
    return parts


def memory(parts):
    result = {"parts": {}}
    totals = {}
    for part, members in parts.items():
        summary = {"processes": 0, "rss_bytes": 0, "names": {}}
        for process in members.values():
            try:
                try:
                    info = process.memory_full_info()
                except psutil.AccessDenied:
                    info = process.memory_info()
                name = process.name()
            except psutil.Error:
                continue
            summary["processes"] += 1
            summary["names"][name] = summary["names"].get(name, 0) + 1
            for field in ("rss", "uss", "pss"):
                value = getattr(info, field, None)
                if value is not None:
                    summary[field + "_bytes"] = summary.get(field + "_bytes", 0) + value
        result["parts"][part] = summary
        for field in ("rss_bytes", "uss_bytes", "pss_bytes"):
            if field in summary:
                totals[field] = totals.get(field, 0) + summary[field]
    result.update(totals)
    return result


def pids(parts, names=("backend", "codex", "postgres")):
    return {pid for part in names for pid in parts[part]}


def alive(pid):
    try:
        return psutil.Process(pid).status() != psutil.STATUS_ZOMBIE
    except psutil.Error:
        return False


def wait_until(report, process, timeout):
    """Waits for the window with the app of the author (a page whose address carries probe-session), a fatal of the
    backend or the end of the shell."""
    deadline = time.time() + timeout
    while time.time() < deadline:
        lines = read_report(report)
        for line in lines:
            if line.get("kind") == "page_load" and "probe-session=" in line["body"].get("url", ""):
                return lines, None
            if line.get("kind") == "backend" and line["body"].get("stage") in ("fatal", "spawn", "start", "resources", "stdout_closed"):
                return lines, f"backend: {json.dumps(line['body'], ensure_ascii=False)}"
        if process.poll() is not None:
            return lines, f"the shell exited with {process.poll()}"
        time.sleep(0.1)
    return read_report(report), f"no window with the app in {timeout} s"


def seconds(line, spawned_at):
    return None if line is None else round(line["at_ms"] / 1000 - spawned_at, 3)


def launch(args, index, resources):
    report = os.path.abspath(f"{args.out}.run{index}.jsonl")
    process, spawned_at = spawn(args.exe, report)
    lines, error = wait_until(report, process, args.timeout)
    first = lambda predicate: next((line for line in lines if predicate(line)), None)  # noqa: E731
    stage = lambda name: first(lambda line: line.get("kind") == "backend" and line["body"].get("stage") == name)  # noqa: E731
    pages = [line for line in lines if line.get("kind") == "page_load" and "url" in line["body"]]
    window = next((line for line in pages if "probe-session=" in line["body"]["url"]), None)
    run = {
        "run": index + 1,
        "kind": "first" if index == 0 else "repeat",
        "shell_setup_seconds": seconds(first(lambda line: line.get("kind") == "start"), spawned_at),
        "backend_spawned_seconds": seconds(stage("spawned"), spawned_at),
        "backend_ready_seconds": seconds(stage("ready"), spawned_at),
        "first_page_seconds": seconds(pages[0] if pages else None, spawned_at),
        "window_seconds": seconds(window, spawned_at),
    }
    if window:
        run["session_status"] = parse_qs(urlparse(window["body"]["url"]).query).get("probe-session", ["?"])[0]
    if error:
        run["error"] = error
        run["report_lines"] = lines[-8:]
    if process.poll() is None:
        time.sleep(args.idle)
        parts = family(process.pid, resources, spawned_at)
        # Memory counts only for an app that reached its window; a failed start shows the window alone.
        if window:
            run["idle_memory"] = memory(parts)
        if args.screenshot:
            run["screenshot"] = screenshot(f"{args.screenshot}-{run['kind']}.png")
        watched = pids(parts)
        stop_started = time.time()
        try:
            psutil.Process(process.pid).kill()
        except psutil.Error:
            pass
        try:
            process.wait(timeout=10)
        except Exception:
            pass
        deadline = stop_started + args.stop_timeout
        while time.time() < deadline and any(alive(pid) for pid in watched):
            time.sleep(0.1)
        left = []
        for pid in watched:
            if alive(pid):
                try:
                    leftover = psutil.Process(pid)
                    left.append(leftover.name())
                    leftover.kill()
                except psutil.Error:
                    pass
        run["stop_seconds"] = round(time.time() - stop_started, 3)
        run["left_after_stop"] = left
    return run


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--exe", required=True)
    parser.add_argument("--resources", required=True)
    parser.add_argument("--out", required=True)
    parser.add_argument("--runs", type=int, default=2)
    parser.add_argument("--screenshot")
    parser.add_argument("--timeout", type=float, default=180)
    parser.add_argument("--idle", type=float, default=10)
    parser.add_argument("--stop-timeout", type=float, default=90)
    args = parser.parse_args()
    data = default_data_dir()
    result = {"exe": args.exe, "data_dir_existed": os.path.exists(data), "runs": []}
    shutil.rmtree(data, ignore_errors=True)
    for index in range(args.runs):
        result["runs"].append(launch(args, index, args.resources))
        if index == 0:
            result["data_dir_after_first_bytes"] = tree_bytes(data) if os.path.isdir(data) else None
        time.sleep(2)
    logs = os.path.join(data, "logs")
    if os.path.isdir(logs):
        shutil.copytree(logs, os.path.join(os.path.dirname(os.path.abspath(args.out)), "app-logs"), dirs_exist_ok=True)
    with open(args.out, "w", encoding="utf-8") as stream:
        json.dump(result, stream, ensure_ascii=False, indent=2)
    print(json.dumps(result, ensure_ascii=False, indent=2)[:6000])
    if not all(run.get("window_seconds") for run in result["runs"]):
        sys.exit(1)


if __name__ == "__main__":
    main()
