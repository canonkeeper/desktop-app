# SPDX-License-Identifier: AGPL-3.0-only
"""Sizes of the installers in a Tauri bundle directory and of an installed app tree.

  sizes.py --bundle <target/release/bundle> [--installed <dir>]... --out sizes.json

--installed takes NAME=PATH (for example app=/Applications/X.app); the tree size counts every
regular file once and also lists the largest files.
"""
import argparse
import json
import os

INSTALLER_SUFFIXES = (".exe", ".msi", ".dmg", ".deb", ".rpm", ".AppImage", ".app.tar.gz", ".sig", ".zip")


def tree(path):
    total = 0
    files = []
    if os.path.isfile(path):
        size = os.path.getsize(path)
        return {"bytes": size, "files": 1, "largest": [[os.path.basename(path), size]]}
    for directory, _, names in os.walk(path):
        for name in names:
            full = os.path.join(directory, name)
            if os.path.islink(full) or not os.path.isfile(full):
                continue
            size = os.path.getsize(full)
            total += size
            files.append((os.path.relpath(full, path), size))
    files.sort(key=lambda item: -item[1])
    return {"bytes": total, "files": len(files), "largest": [list(item) for item in files[:8]]}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--bundle", required=True)
    parser.add_argument("--installed", action="append", default=[])
    parser.add_argument("--out", required=True)
    args = parser.parse_args()
    installers = {}
    for directory, _, names in os.walk(args.bundle):
        for name in names:
            if name.endswith(INSTALLER_SUFFIXES):
                full = os.path.join(directory, name)
                installers[os.path.relpath(full, args.bundle)] = os.path.getsize(full)
    result = {"installers": installers, "installed": {}}
    for item in args.installed:
        name, path = item.split("=", 1)
        result["installed"][name] = tree(path) if os.path.exists(path) else {"error": f"missing: {path}"}
    with open(args.out, "w", encoding="utf-8") as stream:
        json.dump(result, stream, ensure_ascii=False, indent=2)
    print(json.dumps(result, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
