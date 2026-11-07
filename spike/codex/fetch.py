# SPDX-License-Identifier: AGPL-3.0-only
"""Downloads the codex-app-server of one target from the fork release pinned in release.json,
checks the archive sha256 and extracts only the binaries the app needs.

  python3 -I fetch.py <target triple> <out dir> [--with-bwrap]

Writes <out dir>/codex-app-server[.exe] (and bwrap on Linux with --with-bwrap) and
<out dir>/codex.json with the release, sizes and the compressed size of the binary.
"""
import hashlib
import io
import json
import lzma
import os
import sys
import tarfile
import urllib.request
import zipfile

here = os.path.dirname(os.path.abspath(__file__))


def main():
    target, out = sys.argv[1], sys.argv[2]
    with_bwrap = "--with-bwrap" in sys.argv[3:]
    with open(os.path.join(here, "release.json"), encoding="utf-8") as stream:
        release = json.load(stream)
    pin = release["targets"][target]
    url = f'{release["repository"]}/releases/download/{pin["tag"]}/{pin["asset"]}'
    os.makedirs(out, exist_ok=True)
    archive_path = os.path.join(out, pin["asset"])
    if not os.path.exists(archive_path):
        urllib.request.urlretrieve(url, archive_path)
    digest = hashlib.sha256()
    with open(archive_path, "rb") as stream:
        for block in iter(lambda: stream.read(1 << 20), b""):
            digest.update(block)
    if digest.hexdigest() != pin["sha256"]:
        raise SystemExit(f"sha256 mismatch for {pin['asset']}: {digest.hexdigest()}")
    windows = target.endswith("windows-msvc")
    wanted = {"codex-app-server" + (".exe" if windows else "")}
    if with_bwrap and "linux" in target:
        wanted.add("bwrap")
    extracted = {}
    if pin["asset"].endswith(".zip"):
        with zipfile.ZipFile(archive_path) as archive:
            for info in archive.infolist():
                name = info.filename.replace("\\", "/").rsplit("/", 1)[-1]
                if name in wanted:
                    target_path = os.path.join(out, name)
                    with archive.open(info) as source, open(target_path, "wb") as sink:
                        sink.write(source.read())
                    extracted[name] = target_path
    else:
        with tarfile.open(archive_path, "r:gz") as archive:
            for member in archive.getmembers():
                name = member.name.rsplit("/", 1)[-1]
                if member.isfile() and name in wanted:
                    target_path = os.path.join(out, name)
                    with archive.extractfile(member) as source, open(target_path, "wb") as sink:
                        sink.write(source.read())
                    os.chmod(target_path, 0o755)
                    extracted[name] = target_path
    missing = wanted - set(extracted)
    if missing:
        raise SystemExit(f"not in {pin['asset']}: {sorted(missing)}")
    os.remove(archive_path)
    binary = extracted[next(name for name in wanted if name.startswith("codex-app-server"))]
    with open(binary, "rb") as stream:
        data = stream.read()
    report = {
        "target": target,
        "tag": pin["tag"],
        "asset": pin["asset"],
        "archive_sha256": pin["sha256"],
        "binary_bytes": len(data),
        "binary_xz_bytes": len(lzma.compress(data, preset=6)),
        "files": sorted(extracted),
    }
    with open(os.path.join(out, "codex.json"), "w", encoding="utf-8") as stream:
        json.dump(report, stream, indent=2)
    print(json.dumps(report, indent=2))


if __name__ == "__main__":
    main()
