# SPDX-License-Identifier: AGPL-3.0-only
"""Builds PostgreSQL from the source archive pinned in desktop/release.json and packages the set of the app
(specs/2026-11-12-desk-shell.md SH4.2; the build of the S0 probe, specs/2026-11-07-desktop-app.md DA1.3).

  python3 -I desktop/postgres/build.py <work dir> <out dir>

No ICU, OpenSSL, LLVM, compression libraries, readline or NLS: the server needs nothing beyond the C library (and the
C runtime on Windows). Needs meson, ninja, bison, flex, perl and a C compiler (MSVC on Windows, from a developer
command prompt environment); patchelf on Linux. Writes <out dir>/pg (the set) and <out dir>/build.json (the source
sha256, timings and sizes). A source archive whose sha256 differs from the pin is refused.
"""
import json
import os
import shutil
import subprocess
import sys
import tarfile
import time
import urllib.request

MESON_OPTIONS = [
    "-Dicu=disabled", "-Dssl=none", "-Dllvm=disabled", "-Dzlib=disabled", "-Dlz4=disabled", "-Dzstd=disabled",
    "-Dreadline=disabled", "-Dnls=disabled", "-Duuid=none", "-Dlibxml=disabled", "-Dlibxslt=disabled",
    "-Dgssapi=disabled", "-Dldap=disabled", "-Dpam=disabled", "-Dbsd_auth=disabled", "-Dsystemd=disabled",
    "-Dbonjour=disabled", "-Dselinux=disabled", "-Dlibcurl=disabled", "-Dliburing=disabled", "-Dlibnuma=disabled",
    "-Dplperl=disabled", "-Dplpython=disabled", "-Dpltcl=disabled", "-Dtap_tests=disabled", "-Ddocs=disabled",
    "-Ddocs_pdf=disabled",
]

here = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, here)
import package  # noqa: E402  (the sibling module, loaded by path under -I)


def main():
    with open(os.path.join(here, "..", "release.json"), encoding="utf-8") as stream:
        pin = json.load(stream)["postgres"]
    version, source_url, source_sha256 = pin["version"], pin["source"], pin["sha256"]
    work = os.path.abspath(sys.argv[1])
    out = os.path.abspath(sys.argv[2])
    os.makedirs(work, exist_ok=True)
    os.makedirs(out, exist_ok=True)
    archive = os.path.join(work, f"postgresql-{version}.tar.bz2")
    if not os.path.exists(archive):
        urllib.request.urlretrieve(source_url, archive)
    actual = package.sha256(archive)
    if actual != source_sha256:
        raise SystemExit(f"sha256 mismatch for {archive}: {actual}")
    source = os.path.join(work, f"postgresql-{version}")
    build = os.path.join(work, "build")
    prefix = os.path.join(work, "prefix")
    for path in (source, build, prefix):
        shutil.rmtree(path, ignore_errors=True)
    with tarfile.open(archive, "r:bz2") as stream:
        stream.extractall(work, filter="data")

    started = time.time()
    subprocess.run(["meson", "setup", build, source, f"--prefix={prefix}", "--buildtype=release", *MESON_OPTIONS], check=True)
    configured = time.time()
    subprocess.run(["ninja", "-C", build], check=True)
    subprocess.run(["ninja", "-C", build, "install"], check=True, stdout=subprocess.DEVNULL)
    built = time.time()

    sys.argv = [sys.argv[0], "package", prefix, os.path.join(out, "pg"), os.path.join(out, "build.json"),
                "--version", version, "--source-sha256", source_sha256,
                "--configure-seconds", f"{configured - started:.1f}", "--build-seconds", f"{built - configured:.1f}"]
    package.main()


if __name__ == "__main__":
    main()
