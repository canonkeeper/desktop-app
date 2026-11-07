# SPDX-License-Identifier: AGPL-3.0-only
"""Launches the probe shell, waits for its page report and measures the window.

  shell_probe.py launch --exe <app binary> --out shell.json [--runs 2] [--screenshot shot.png]
  shell_probe.py keychain --exe <app binary> --mode write|read --out keychain.json
  shell_probe.py update --exe <app binary> --url <latest.json url> --out update.json

Times are wall clock from the spawn of the process: until the page's DOM is ready (the window
shows the page) and until the map has rendered. Memory is the resident memory of the process
tree after 5 s of idle; on macOS the WebKit XPC processes started for the window are added,
because launchd, not the app, is their parent. Needs psutil (and mss on Linux and Windows for
the screenshot).
"""
import argparse
import json
import os
import subprocess
import sys
import time

import psutil

WEBKIT_XPC = ("com.apple.WebKit", "WebKit.WebContent", "WebKit.Networking", "WebKit.GPU")


def read_report(path):
    lines = []
    try:
        with open(path, encoding="utf-8") as stream:
            for line in stream:
                line = line.strip()
                if line:
                    try:
                        lines.append(json.loads(line))
                    except json.JSONDecodeError:
                        pass
    except FileNotFoundError:
        pass
    return lines


def tree_memory(root_pid, spawned_at):
    members = {}
    try:
        root = psutil.Process(root_pid)
        members[root.pid] = root
        for child in root.children(recursive=True):
            members[child.pid] = child
    except psutil.Error:
        pass
    if sys.platform == "darwin":
        for process in psutil.process_iter(["name", "create_time"]):
            try:
                name = process.info["name"] or ""
                if any(marker in name for marker in WEBKIT_XPC) and process.info["create_time"] >= spawned_at - 1:
                    members[process.pid] = process
            except psutil.Error:
                pass
    rss = 0
    uss = 0
    names = {}
    for process in members.values():
        try:
            info = process.memory_full_info() if sys.platform != "darwin" else process.memory_info()
            rss += info.rss
            uss += getattr(info, "uss", 0)
            names[process.name()] = names.get(process.name(), 0) + 1
        except psutil.Error:
            pass
    result = {"processes": len(members), "rss_bytes": rss, "names": names}
    if uss:
        result["uss_bytes"] = uss
    return result


def screenshot(path):
    try:
        if sys.platform == "darwin":
            subprocess.run(["screencapture", "-x", path], check=True, timeout=30)
        else:
            import mss
            import mss.tools

            with mss.mss() as capture:
                shot = capture.grab(capture.monitors[0])
                mss.tools.to_png(shot.rgb, shot.size, output=path)
        return {"path": path, "bytes": os.path.getsize(path)}
    except Exception as error:  # the screenshot is evidence for a person, never a verdict
        return {"error": str(error)}


def kill_tree(process):
    try:
        parent = psutil.Process(process.pid)
        children = parent.children(recursive=True)
    except psutil.Error:
        children = []
    for child in children:
        try:
            child.kill()
        except psutil.Error:
            pass
    try:
        process.kill()
    except Exception:
        pass
    try:
        process.wait(timeout=10)
    except Exception:
        pass


def spawn(exe, report, extra_env=None):
    env = dict(os.environ)
    env["SPIKE_REPORT"] = report
    env.update(extra_env or {})
    if os.path.exists(report):
        os.remove(report)
    spawned_at = time.time()
    process = subprocess.Popen([exe], env=env)
    return process, spawned_at


def wait_for(report, predicate, timeout, process=None):
    deadline = time.time() + timeout
    while time.time() < deadline:
        lines = read_report(report)
        found = [line for line in lines if predicate(line)]
        if found:
            return found[0], lines
        if process is not None and process.poll() is not None:
            return None, lines
        time.sleep(0.2)
    return None, read_report(report)


def launch(args):
    runs = []
    for index in range(args.runs):
        report = os.path.abspath(f"{args.out}.run{index}.jsonl")
        process, spawned_at = spawn(args.exe, report)
        rendered, lines = wait_for(report, lambda line: line.get("kind") == "page" and line["body"]["page"].get("stage") in ("rendered", "failed"), args.timeout, process)
        run = {"run": index + 1}
        dom = next((line for line in lines if line.get("kind") == "page" and line["body"]["page"].get("stage") == "dom"), None)
        start = next((line for line in lines if line.get("kind") == "start"), None)
        if start:
            run["process_to_setup_seconds"] = round(start["at_ms"] / 1000 - spawned_at, 3)
        if dom:
            run["spawn_to_page_dom_seconds"] = round(dom["body"]["page"]["epoch_ms"] / 1000 - spawned_at, 3)
        if rendered:
            page = rendered["body"]["page"]
            run["spawn_to_map_rendered_seconds"] = round(page["epoch_ms"] / 1000 - spawned_at, 3)
            run["page"] = page
        else:
            run["error"] = "no rendered report" + (f" (exit code {process.poll()})" if process.poll() is not None else " in time")
            run["report_lines"] = lines[-5:]
        if process.poll() is None:
            time.sleep(5)
            run["idle_memory"] = tree_memory(process.pid, spawned_at)
            if index == 0 and args.screenshot:
                run["screenshot"] = screenshot(args.screenshot)
        kill_tree(process)
        runs.append(run)
        time.sleep(2)
    write(args.out, {"exe": args.exe, "runs": runs})


def keychain(args):
    report = os.path.abspath(args.out + ".jsonl")
    process, _ = spawn(args.exe, report, {"SPIKE_KEYCHAIN": args.mode})
    line, lines = wait_for(report, lambda line: line.get("kind") == "keychain", args.timeout, process)
    result = {"mode": args.mode}
    if line:
        result.update(line["body"])
        result["version"] = line["version"]
    else:
        result["outcome"] = f"no answer in {args.timeout} s: the keychain call blocked (a prompt waits for a person) or the app failed"
        result["report_lines"] = lines[-5:]
    kill_tree(process)
    write(args.out, result)


def update(args):
    report = os.path.abspath(args.out + ".jsonl")
    process, _ = spawn(args.exe, report, {"SPIKE_UPDATE_URL": args.url})
    line, lines = wait_for(report, lambda line: line.get("kind") == "update" and line["body"].get("stage") != "downloaded", args.timeout, process)
    result = {"lines": [line["body"] for line in lines if line.get("kind") == "update"]}
    try:
        result["exit_code"] = process.wait(timeout=30)
    except subprocess.TimeoutExpired:
        result["exit_code"] = None
        kill_tree(process)
    write(args.out, result)


def write(path, value):
    with open(path, "w", encoding="utf-8") as stream:
        json.dump(value, stream, ensure_ascii=False, indent=2)
    print(json.dumps(value, ensure_ascii=False, indent=2)[:4000])


def main():
    parser = argparse.ArgumentParser()
    commands = parser.add_subparsers(dest="command", required=True)
    launch_parser = commands.add_parser("launch")
    launch_parser.add_argument("--exe", required=True)
    launch_parser.add_argument("--out", required=True)
    launch_parser.add_argument("--runs", type=int, default=2)
    launch_parser.add_argument("--screenshot")
    launch_parser.add_argument("--timeout", type=float, default=90)
    keychain_parser = commands.add_parser("keychain")
    keychain_parser.add_argument("--exe", required=True)
    keychain_parser.add_argument("--mode", choices=["write", "read"], required=True)
    keychain_parser.add_argument("--out", required=True)
    keychain_parser.add_argument("--timeout", type=float, default=40)
    update_parser = commands.add_parser("update")
    update_parser.add_argument("--exe", required=True)
    update_parser.add_argument("--url", required=True)
    update_parser.add_argument("--out", required=True)
    update_parser.add_argument("--timeout", type=float, default=180)
    args = parser.parse_args()
    {"launch": launch, "keychain": keychain, "update": update}[args.command](args)


if __name__ == "__main__":
    main()
