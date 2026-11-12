# SPDX-License-Identifier: AGPL-3.0-only
"""Refuses a push that would publish what must stay closed.

  python3 -I scripts/check-forbidden.py --ci
  python3 -I scripts/check-forbidden.py --strict --deny-list <file> --monorepo <dir>

It reads every blob reachable from any local ref (the whole history that a push publishes) and
every commit message and identity, and reports:

  - secrets: private keys, provider and GitHub tokens, Tauri/minisign secret keys, JWTs;
  - public IPv4 addresses (loopback, private and documentation ranges are allowed);
  - home directories of real users (/home/<name>, /Users/<name>, C:\\Users\\<name>), except the
    GitHub runner accounts;
  - e-mail addresses outside the allowlist, and commit identities outside it;
  - with --deny-list: every term of the private list (names and quotes of a private saga,
    internal hosts) as a case-insensitive substring, "ё" read as "е"; the list itself lives only
    in the closed repository and is never printed, only the line number of the matching term;
  - with --monorepo: blobs whose git object id equals a closed file of the closed repository (HEAD
    and its main branches), larger than 64 bytes; a file the closed repository exports — its
    scripts/open-source/export.mjs --list at HEAD — is open, and its blob is allowed;
  - binary blobs larger than 1 MiB.

--ci runs the generic rules only (the public CI has neither private input). --strict requires
both private inputs and is what the pre-push hook runs.

With --strict, what is reachable from refs/remotes/origin/* is already published: a blob, a commit
and its identities and message there cannot be unpublished by not pushing. Their findings are
printed in a separate block "already published (notice)", with the same line number of the deny
list term, and do not fail the check; everything new is checked as before and fails it. Without
remote refs (the first push) everything is new.
"""
import argparse
import ipaddress
import os
import re
import subprocess
import sys

ALLOWED_EMAILS = {
    "support@canonkeeper.ru",
    "noreply@anthropic.com",
    "noreply@github.com",
}
ALLOWED_EMAIL_DOMAINS = ("users.noreply.github.com", "example.com", "example.org", "example.test", "desktop.invalid")
# Names of home directories that are plainly made up: the placeholders of the docs and the fixtures of the tests of the
# app (a Latin and a Cyrillic author, one-letter names). A real name of a person or a machine is never added here.
ALLOWED_HOME_NAMES = {
    "runner", "runneradmin", "name", "user", "you", "username", "<name>", "<user>",
    "author", "a", "x", "тест", "Тест",
}
SECRET_PATTERNS = [
    ("private key", re.compile(r"-----BEGIN [A-Z ]*PRIVATE KEY-----")),
    ("openai-style key", re.compile(r"\bsk-(?:proj-|live-|ant-)?[A-Za-z0-9_-]{24,}")),
    ("github token", re.compile(r"\b(?:gh[pousr]_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{40,})")),
    ("aws key", re.compile(r"\bAKIA[0-9A-Z]{16}\b")),
    ("slack token", re.compile(r"\bxox[abprs]-[A-Za-z0-9-]{10,}")),
    ("google key", re.compile(r"\bAIza[0-9A-Za-z_-]{35}\b")),
    ("jwt", re.compile(r"\beyJ[A-Za-z0-9_-]{16,}\.eyJ[A-Za-z0-9_-]{16,}\.[A-Za-z0-9_-]{16,}")),
    # Built from parts so that this file does not match its own pattern.
    ("minisign/tauri secret key", re.compile("(?:untrusted comment: (?:rsign|minisign) encrypted " + "secret key|"
                                             + "dW50cnVzdGVkIGNvbW1lbnQ6IH" + "JzaWduIGVuY3J5cHRlZCBzZWNyZXQga2V5)")),
    ("assigned secret", re.compile(r"(?i)\b(?:api[_-]?key|secret[_-]?key|access[_-]?token|auth[_-]?token|password)\s*[:=]\s*[\"'][A-Za-z0-9+/_=-]{24,}[\"']")),
]
# Synthetic canaries the redaction harness of the backend keeps out of every operational output: it needs them
# literally, so they are allowed in their one file and for their kinds only.
CANARIES = "backend/internal/governance/testdata/redaction_forbidden_strings.json"
ALLOWED_FINDINGS = {(CANARIES, "private key"), (CANARIES, "openai-style key"), (CANARIES, "home directory")}
IPV4 = re.compile(r"(?<![\w.])(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})(?![\w.])")
EMAIL = re.compile(r"\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}\b")
HOME = re.compile(r"(?:/home/|/Users/|[Cc]:\\\\?Users\\\\?)([\w.-]+)")
FILE_SUFFIXES = (".png", ".jpg", ".jpeg", ".svg", ".ico", ".icns", ".webp", ".gif")
DOC_NETWORKS = [ipaddress.ip_network(net) for net in ("192.0.2.0/24", "198.51.100.0/24", "203.0.113.0/24")]


def git(repo, *args, binary=False):
    output = subprocess.run(["git", "-C", repo, *args], check=True, capture_output=True).stdout
    return output if binary else output.decode("utf-8", "replace")


def normalize(text):
    return text.casefold().replace("ё", "е")


def load_deny_list(path):
    terms = []
    with open(path, encoding="utf-8") as stream:
        for number, line in enumerate(stream, 1):
            line = line.strip()
            if line and not line.startswith("#"):
                terms.append((number, normalize(line)))
    return terms


def monorepo_blobs(path):
    refs = ["HEAD"]
    for ref in ("main", "codex/native-clean-architecture"):
        if subprocess.run(["git", "-C", path, "rev-parse", "--verify", "-q", ref], capture_output=True).returncode == 0:
            refs.append(ref)
    blobs = {}
    for ref in refs:
        for line in git(path, "ls-tree", "-r", "-l", ref).splitlines():
            meta, name = line.split("\t", 1)
            parts = meta.split()
            if parts[1] == "blob" and parts[3] != "-" and int(parts[3]) > 64:
                blobs.setdefault(parts[2], f"{ref}:{name}")
    return blobs


def open_blobs(path):
    """Blob ids of the files the closed repository exports at HEAD (scripts/open-source/export.mjs --list)."""
    script = os.path.join(path, "scripts", "open-source", "export.mjs")
    listed = subprocess.run(["node", script, "--list"], cwd=path, check=True, capture_output=True).stdout
    wanted = set(listed.decode("utf-8").splitlines())
    blobs = set()
    for entry in git(path, "ls-tree", "-r", "-z", "HEAD", binary=True).split(b"\0"):
        if not entry:
            continue
        meta, name = entry.split(b"\t", 1)
        if name.decode("utf-8") in wanted:
            blobs.add(meta.split()[2].decode("ascii"))
    return blobs


def published_objects(repo):
    """Objects and commits reachable from refs/remotes/origin/*: what a push cannot unpublish."""
    if not git(repo, "for-each-ref", "refs/remotes/origin/").strip():
        return set(), set()
    objects = {line.split(" ", 1)[0] for line in git(repo, "rev-list", "--objects", "--remotes=origin").splitlines()}
    commits = set(git(repo, "rev-list", "--remotes=origin").split())
    return objects, commits


def public_ip(octets):
    if any(int(octet) > 255 for octet in octets):
        return False
    address = ipaddress.ip_address(".".join(octets))
    if address.is_loopback or address.is_private or address.is_unspecified or address.is_reserved:
        return False
    if address.is_multicast or address.is_link_local or any(address in net for net in DOC_NETWORKS):
        return False
    if str(address) == "255.255.255.255":
        return False
    return True


def email_allowed(address):
    address = address.lower()
    return address in ALLOWED_EMAILS or address.endswith(ALLOWED_EMAIL_DOMAINS)


def scan_text(where, text, deny, findings):
    for label, pattern in SECRET_PATTERNS:
        if (where, label) in ALLOWED_FINDINGS:
            continue
        for match in pattern.finditer(text):
            findings.append(f"{where}: {label}: {match.group(0)[:12]}…")
    for match in IPV4.finditer(text):
        if public_ip(match.groups()):
            findings.append(f"{where}: public IPv4 {match.group(0)}")
    for match in EMAIL.finditer(text):
        if match.group(0).lower().endswith(FILE_SUFFIXES):
            continue
        if not email_allowed(match.group(0)):
            findings.append(f"{where}: e-mail {match.group(0)}")
    for match in HOME.finditer(text):
        name = match.group(1)
        if (where, "home directory") in ALLOWED_FINDINGS:
            continue
        if name not in ALLOWED_HOME_NAMES and not name.startswith(("$", "%", "<", "{")):
            findings.append(f"{where}: home directory {match.group(0)}")
    if deny:
        folded = normalize(text)
        for number, term in deny:
            if term in folded:
                findings.append(f"{where}: term from line {number} of the deny list")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--repo", default=".")
    parser.add_argument("--deny-list")
    parser.add_argument("--monorepo")
    parser.add_argument("--strict", action="store_true")
    parser.add_argument("--ci", action="store_true")
    args = parser.parse_args()
    if args.strict and not (args.deny_list and args.monorepo):
        raise SystemExit("--strict needs --deny-list and --monorepo")
    if not args.strict and not args.ci:
        raise SystemExit("choose --strict (before a push) or --ci (generic rules only)")
    repo = os.path.abspath(args.repo)
    deny = load_deny_list(args.deny_list) if args.deny_list else []
    closed = monorepo_blobs(args.monorepo) if args.monorepo else {}
    if closed:
        exported = open_blobs(args.monorepo)
        closed = {object_id: where for object_id, where in closed.items() if object_id not in exported}
    findings = []
    notices = []
    published, published_commits = published_objects(repo) if args.strict else (set(), set())

    objects = git(repo, "rev-list", "--objects", "--all").splitlines() if git(repo, "for-each-ref").strip() else []
    paths = {}
    for line in objects:
        object_id, _, path = line.partition(" ")
        if path:
            paths.setdefault(object_id, path)
    # The index and untracked files too: what the next commit would publish.
    for line in git(repo, "ls-files", "-s").splitlines():
        meta, path = line.split("\t", 1)
        paths.setdefault(meta.split()[1], path)
    for path in git(repo, "ls-files", "--others", "--exclude-standard", "-z").split("\0"):
        if path:
            object_id = git(repo, "hash-object", "-w", "--", path).strip()
            paths.setdefault(object_id, path)
    blob_ids = [object_id for object_id in paths if git(repo, "cat-file", "-t", object_id).strip() == "blob"]
    for object_id in blob_ids:
        where = paths[object_id]
        target = notices if object_id in published else findings
        if object_id in closed:
            target.append(f"{where}: same content as the closed repository file {closed[object_id]}")
        data = git(repo, "cat-file", "-p", object_id, binary=True)
        if b"\0" in data[:8192]:
            if len(data) > 1 << 20:
                target.append(f"{where}: binary blob of {len(data)} bytes")
            continue
        scan_text(where, data.decode("utf-8", "replace"), deny, target)

    history = git(repo, "log", "--all", "--format=%H%x1f%an <%ae>%x1f%cn <%ce>%x1f%B%x1e") if objects else ""
    for record in history.split("\x1e"):
        record = record.strip()
        if not record:
            continue
        commit, author, committer, message = (record.split("\x1f") + ["", "", ""])[:4]
        target = notices if commit in published_commits else findings
        for identity in (author, committer):
            address = identity[identity.rfind("<") + 1:-1]
            if not email_allowed(address):
                target.append(f"commit {commit[:10]}: identity {identity}")
        scan_text(f"commit {commit[:10]} message", message, deny, target)

    scope = "strict (deny list and closed repository)" if args.strict else "ci (generic rules)"
    if notices:
        print(f"check-forbidden: {len(set(notices))} finding(s) already published (notice), not failing:")
        for notice in sorted(set(notices)):
            print("  " + notice)
    if findings:
        print(f"check-forbidden: {len(findings)} finding(s), {scope}:")
        for finding in sorted(set(findings)):
            print("  " + finding)
        sys.exit(1)
    published_note = f", {len(set(notices))} already published (notice)" if notices else ""
    print(f"check-forbidden: clean, {len(blob_ids)} blobs and every commit checked, {scope}{published_note}")


if __name__ == "__main__":
    main()
