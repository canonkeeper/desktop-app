# SPDX-License-Identifier: AGPL-3.0-only
"""Builds PostgreSQL from the pinned source archive with meson and packages the desktop set.

  python3 -I build.py <work dir> <out dir>

No ICU, OpenSSL, LLVM, compression libraries, readline or NLS: the server needs nothing beyond
the C library (and the C runtime on Windows). Needs meson, ninja, bison, flex, perl and a C
compiler (MSVC on Windows, from a developer command prompt environment). Writes
<out dir>/pg (the set) and <out dir>/build.json (timings and sizes).
"""
import os
import shutil
import subprocess
import sys
import tarfile
import time
import urllib.request

PG_VERSION = "18.6"
PG_SHA256 = "555610c24d53e4316da5b7d3fc25c279d96856d5e0e23ee308c328c5fa881d9f"
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
    work = os.path.abspath(sys.argv[1])
    out = os.path.abspath(sys.argv[2])
    os.makedirs(work, exist_ok=True)
    os.makedirs(out, exist_ok=True)
    archive = os.path.join(work, f"postgresql-{PG_VERSION}.tar.bz2")
    if not os.path.exists(archive):
        urllib.request.urlretrieve(f"https://ftp.postgresql.org/pub/source/v{PG_VERSION}/postgresql-{PG_VERSION}.tar.bz2", archive)
    actual = package.sha256(archive)
    if actual != PG_SHA256:
        raise SystemExit(f"sha256 mismatch for {archive}: {actual}")
    source = os.path.join(work, f"postgresql-{PG_VERSION}")
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
                "--version", PG_VERSION, "--configure-seconds", f"{configured - started:.1f}",
                "--build-seconds", f"{built - configured:.1f}"]
    package.main()


if __name__ == "__main__":
    main()
