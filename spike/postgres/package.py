# SPDX-License-Identifier: AGPL-3.0-only
"""Packages the PostgreSQL set the desktop app needs out of a meson install prefix.

  package.py verify-sha256 <file> <sha256>
  package.py package <prefix> <out dir> <report.json> --version V --configure-seconds S --build-seconds S

The set keeps the install layout (bin, lib, share) so the server finds share and module
directories relative to its own binary, and makes the client tools load libpq from ../lib:
RUNPATH $ORIGIN/../lib on Linux, @executable_path/../lib on macOS (then an ad-hoc signature,
because changing load commands invalidates the linker signature on arm64), the DLL next to the
executable on Windows.
"""
import argparse
import hashlib
import io
import json
import os
import platform
import shutil
import subprocess
import sys
import tarfile

BINARIES = ["postgres", "initdb", "pg_ctl", "pg_dump", "pg_restore"]
MODULES = ["plpgsql", "dict_snowball", "pg_stat_statements"]
EXTENSIONS = ["plpgsql", "pg_stat_statements"]
SHARE_FILES_KEEP_ALL_DIRS = ["timezone", "timezonesets", "tsearch_data"]


def sha256(path):
    digest = hashlib.sha256()
    with open(path, "rb") as stream:
        for block in iter(lambda: stream.read(1 << 20), b""):
            digest.update(block)
    return digest.hexdigest()


def tree_size(root):
    total = 0
    files = 0
    for directory, _, names in os.walk(root):
        for name in names:
            path = os.path.join(directory, name)
            if not os.path.islink(path):
                total += os.path.getsize(path)
                files += 1
    return total, files


def compressed_sizes(root):
    sizes = {}
    for mode, label in (("w:gz", "tar_gz"), ("w:xz", "tar_xz")):
        buffer = io.BytesIO()
        with tarfile.open(fileobj=buffer, mode=mode) as archive:
            archive.add(root, arcname="pg")
        sizes[label] = buffer.tell()
    return sizes


def find_dir(prefix, *candidates):
    for candidate in candidates:
        path = os.path.join(prefix, candidate)
        if os.path.isdir(path):
            return candidate
    raise SystemExit(f"none of {candidates} under {prefix}")


def is_windows():
    return os.name == "nt" or sys.platform in ("msys", "cygwin")


def package(args):
    prefix = os.path.abspath(args.prefix)
    out = os.path.abspath(args.out)
    if os.path.exists(out):
        shutil.rmtree(out)
    full_size, full_files = tree_size(prefix)
    exe = ".exe" if is_windows() else ""

    os.makedirs(os.path.join(out, "bin"))
    for name in BINARIES:
        shutil.copy2(os.path.join(prefix, "bin", name + exe), os.path.join(out, "bin", name + exe))
    if is_windows():
        for name in os.listdir(os.path.join(prefix, "bin")):
            if name.lower().endswith(".dll"):
                shutil.copy2(os.path.join(prefix, "bin", name), os.path.join(out, "bin", name))

    # Module directory: lib/postgresql, or lib when the prefix names postgres; meson may put
    # libraries under lib/<triplet> on Debian-like systems.
    libdir = find_dir(prefix, "lib/x86_64-linux-gnu", "lib/aarch64-linux-gnu", "lib64", "lib")
    pkglib = libdir + "/postgresql" if os.path.isdir(os.path.join(prefix, libdir, "postgresql")) else libdir
    os.makedirs(os.path.join(out, pkglib), exist_ok=True)
    module_ext = {"darwin": ".dylib"}.get(sys.platform, ".dll" if is_windows() else ".so")
    for name in MODULES:
        shutil.copy2(os.path.join(prefix, pkglib, name + module_ext), os.path.join(out, pkglib, name + module_ext))
    if not is_windows():
        # Only the name the client tools load (the SONAME / install name), as a regular file:
        # installers and bundles copy resources without symbolic links.
        soname = "libpq.5.dylib" if sys.platform == "darwin" else "libpq.so.5"
        shutil.copy2(os.path.join(prefix, libdir, soname), os.path.join(out, libdir, soname), follow_symlinks=True)

    share = find_dir(prefix, "share/postgresql", "share")
    share_out = os.path.join(out, share)
    os.makedirs(share_out, exist_ok=True)
    for name in os.listdir(os.path.join(prefix, share)):
        source = os.path.join(prefix, share, name)
        if os.path.isdir(source):
            if name in SHARE_FILES_KEEP_ALL_DIRS:
                shutil.copytree(source, os.path.join(share_out, name))
            elif name == "extension":
                os.makedirs(os.path.join(share_out, name))
                for file_name in os.listdir(source):
                    if any(file_name == ext + ".control" or file_name.startswith(ext + "--") for ext in EXTENSIONS):
                        shutil.copy2(os.path.join(source, file_name), os.path.join(share_out, name, file_name))
        else:
            shutil.copy2(source, os.path.join(share_out, name))

    relocation = relocate(out, prefix, libdir)
    set_size, set_files = tree_size(out)
    report = {
        "version": args.version,
        "system": f"{platform.system()} {platform.machine()}",
        "configure_seconds": float(args.configure_seconds),
        "build_seconds": float(args.build_seconds),
        "full_install_bytes": full_size,
        "full_install_files": full_files,
        "set_bytes": set_size,
        "set_files": set_files,
        "layout": {"lib": libdir, "pkglib": pkglib, "share": share},
        "relocation": relocation,
    }
    report.update({"set_" + key + "_bytes": value for key, value in compressed_sizes(out).items()})
    with open(args.report, "w", encoding="utf-8") as stream:
        json.dump(report, stream, ensure_ascii=False, indent=2)
    print(json.dumps(report, ensure_ascii=False, indent=2))


def run(command):
    return subprocess.run(command, check=True, capture_output=True, text=True).stdout


def relocate(out, prefix, libdir):
    if sys.platform.startswith("linux"):
        changed = []
        for directory, rpath in (("bin", "$ORIGIN/../" + libdir), (libdir, "$ORIGIN")):
            for name in os.listdir(os.path.join(out, directory)):
                path = os.path.join(out, directory, name)
                if os.path.islink(path) or not os.path.isfile(path):
                    continue
                with open(path, "rb") as stream:
                    if stream.read(4) != b"\x7fELF":
                        continue
                run(["patchelf", "--set-rpath", rpath, path])
                changed.append(f"{directory}/{name}")
        leftovers = [name for name in changed if prefix in run(["readelf", "-d", os.path.join(out, name)])]
        return {"method": "patchelf RUNPATH", "files": changed, "absolute_left": leftovers}
    if sys.platform == "darwin":
        changed = []
        lib_path = os.path.join(out, libdir)
        dylibs = [name for name in os.listdir(lib_path) if name.endswith(".dylib") and not os.path.islink(os.path.join(lib_path, name))]
        for name in dylibs:
            run(["install_name_tool", "-id", "@rpath/" + name, os.path.join(lib_path, name)])
        machos = []
        for directory, _, names in os.walk(out):
            for name in names:
                path = os.path.join(directory, name)
                if os.path.islink(path):
                    continue
                with open(path, "rb") as stream:
                    magic = stream.read(4)
                if magic in (b"\xcf\xfa\xed\xfe", b"\xca\xfe\xba\xbe"):
                    machos.append(path)
        for path in machos:
            for line in run(["otool", "-L", path]).splitlines()[1:]:
                dependency = line.strip().split(" (")[0]
                if dependency.startswith(prefix):
                    base = os.path.basename(dependency)
                    run(["install_name_tool", "-change", dependency, "@loader_path/" + os.path.relpath(lib_path, os.path.dirname(path)) + "/" + base, path])
                    changed.append(os.path.relpath(path, out))
        for path in machos:
            run(["codesign", "--force", "--sign", "-", path])
        leftovers = [os.path.relpath(path, out) for path in machos if prefix in run(["otool", "-L", path])]
        return {"method": "install_name_tool + codesign -s -", "files": sorted(set(changed)), "signed": len(machos), "absolute_left": leftovers}
    return {"method": "none (DLLs next to executables)"}


def main():
    parser = argparse.ArgumentParser()
    commands = parser.add_subparsers(dest="command", required=True)
    verify = commands.add_parser("verify-sha256")
    verify.add_argument("file")
    verify.add_argument("sha256")
    pack = commands.add_parser("package")
    pack.add_argument("prefix")
    pack.add_argument("out")
    pack.add_argument("report")
    pack.add_argument("--version", required=True)
    pack.add_argument("--configure-seconds", required=True)
    pack.add_argument("--build-seconds", required=True)
    args = parser.parse_args()
    if args.command == "verify-sha256":
        actual = sha256(args.file)
        if actual != args.sha256:
            raise SystemExit(f"sha256 mismatch for {args.file}: {actual}")
        print(f"sha256 ok: {args.file}")
    else:
        package(args)


if __name__ == "__main__":
    main()
