// SPDX-License-Identifier: AGPL-3.0-only
// Lays out the set of the desktop installers (specs/2026-11-12-desk-shell.md SH4.2):
//
//   node scripts/desktop-app.mjs --target <triple> --postgres <set archive> --codex <package archive>
//
// into desktop/bundle, the directory desktop/src-tauri/tauri.bundle.json names: the backend with the desktop web
// inside (scripts/desktop-backend.mjs) as canonkeeper-backend-<triple>[.exe]; postgres/ (bin, lib, share) from the
// archive desktop/postgres/build.py made of the source pinned in desktop/release.json; codex/codex-app-server[.exe]
// from the package of the Codex fork pinned there, of the version backend/go.mod pins; codex-homes/research-chat, the
// one profile of the open app. Each archive is checked against its pin before anything is built, and a mismatch is a
// refusal. The script downloads nothing: the workflow fetches the archives.
import { createHash } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { chmodSync, copyFileSync, cpSync, existsSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, statSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { pathToFileURL } from 'node:url';
import { parseArgs } from 'node:util';

import { codexPackageVersion } from './codex-package.mjs';

const repositoryRoot = path.resolve(import.meta.dirname, '..');

/** The targets of the installers (specs/2026-11-12-desktop-target.md DT0.1): Windows x64 and Linux x64. */
export const targets = {
  'x86_64-unknown-linux-gnu': { goos: 'linux', goarch: 'amd64', exe: '' },
  'x86_64-pc-windows-msvc': { goos: 'windows', goarch: 'amd64', exe: '.exe' },
};

/** The programs of PostgreSQL the backend runs (backend/internal/app/desktop/postgres.go, DA1.3). */
export const postgresPrograms = ['postgres', 'initdb', 'pg_ctl', 'pg_dump', 'pg_restore'];

/** The profiles of Codex the open app carries: the chat only; the profiles of the workshops are closed. */
export const codexHomes = ['research-chat'];

export function sha256(file) {
  return createHash('sha256').update(readFileSync(file)).digest('hex');
}

/** The pin of the Codex package for a target: only the version backend/go.mod pins (DT13.9). */
export function codexPin(release, target, goMod) {
  const version = codexPackageVersion(goMod);
  if (!version) throw new Error('backend/go.mod pins no Codex SDK version');
  const pin = release.codex?.targets?.[target];
  if (!pin) throw new Error(`desktop/release.json pins no Codex package for ${target}; backend/go.mod needs ${version}`);
  if (pin.tag !== `rust-v${version}`) {
    throw new Error(`the Codex package for ${target} is pinned at ${pin.tag}; backend/go.mod needs rust-v${version}`);
  }
  return pin;
}

function checkSha256(file, expected, what) {
  const actual = sha256(file);
  if (actual !== expected) throw new Error(`sha256 of ${what} ${path.basename(file)} is ${actual}; the pin is ${expected}`);
}

// tar of the system: Windows has bsdtar, which reads zip too; the tar of Git Bash would not.
function tarProgram() {
  return process.platform === 'win32' ? path.join(process.env.SystemRoot ?? 'C:\\Windows', 'System32', 'tar.exe') : 'tar';
}

function extract(archive, into) {
  mkdirSync(into, { recursive: true });
  const result = spawnSync(tarProgram(), ['-xf', archive, '-C', into], { encoding: 'utf8' });
  if (result.status !== 0) throw new Error(`cannot unpack ${path.basename(archive)}: ${(result.stderr ?? '').trim()}`);
}

function find(directory, name) {
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const full = path.join(directory, entry.name);
    if (entry.isDirectory()) {
      const found = find(full, name);
      if (found) return found;
    } else if (entry.isFile() && entry.name === name) {
      return full;
    }
  }
  return null;
}

/** The set of PostgreSQL out of its archive: built from the pinned source, with every program the backend runs. */
function postgresSet(archive, pin, exe, work) {
  extract(archive, work);
  const reportFile = path.join(work, 'build.json');
  if (!existsSync(reportFile)) throw new Error('the PostgreSQL set has no build.json');
  const report = JSON.parse(readFileSync(reportFile, 'utf8'));
  if (report.source_sha256 !== pin.sha256 || report.version !== pin.version) {
    throw new Error(`the PostgreSQL set was built from ${report.version} ${report.source_sha256}; the pin is ${pin.version} ${pin.sha256}`);
  }
  const set = path.join(work, 'pg');
  const missing = postgresPrograms.filter((program) => !existsSync(path.join(set, 'bin', program + exe)));
  for (const directory of ['lib', 'share']) {
    if (!existsSync(path.join(set, directory)) || !statSync(path.join(set, directory)).isDirectory()) missing.push(`${directory}/`);
  }
  if (missing.length > 0) throw new Error(`the PostgreSQL set lacks ${missing.join(', ')}`);
  return set;
}

/** Builds the backend with the desktop web inside for the target (scripts/desktop-backend.mjs). */
function buildBackend(output, target) {
  const result = spawnSync(process.execPath, [path.join(repositoryRoot, 'scripts', 'desktop-backend.mjs'), output], {
    cwd: repositoryRoot, stdio: 'inherit', env: { ...process.env, GOOS: target.goos, GOARCH: target.goarch },
  });
  if (result.status !== 0) throw new Error('the backend did not build');
}

/**
 * Checks the archives against their pins and lays out the set in `out`; `build` makes the backend (the tests give
 * their own).
 */
export function assemble({ root = repositoryRoot, target, postgres, codex, out, build = buildBackend }) {
  const spec = targets[target];
  if (!spec) throw new Error(`no installer for ${target}; the targets are ${Object.keys(targets).join(', ')}`);
  const release = JSON.parse(readFileSync(path.join(root, 'desktop', 'release.json'), 'utf8'));
  const goMod = readFileSync(path.join(root, 'backend', 'go.mod'), 'utf8');
  const pin = codexPin(release, target, goMod);
  checkSha256(codex, pin.sha256, 'the Codex package');
  const work = mkdtempSync(path.join(tmpdir(), 'canonkeeper-desktop-set-'));
  try {
    const set = postgresSet(postgres, release.postgres, spec.exe, path.join(work, 'postgres'));
    extract(codex, path.join(work, 'codex'));
    const server = find(path.join(work, 'codex'), `codex-app-server${spec.exe}`);
    if (!server) throw new Error(`the Codex package has no codex-app-server${spec.exe}`);

    rmSync(out, { force: true, recursive: true });
    mkdirSync(path.join(out, 'codex'), { recursive: true });
    cpSync(set, path.join(out, 'postgres'), { recursive: true });
    const placed = path.join(out, 'codex', `codex-app-server${spec.exe}`);
    copyFileSync(server, placed);
    chmodSync(placed, 0o755);
    for (const home of codexHomes) {
      cpSync(path.join(root, 'codex-homes', home), path.join(out, 'codex-homes', home), { recursive: true });
    }
    build(path.join(out, `canonkeeper-backend-${target}${spec.exe}`), spec);
  } finally {
    rmSync(work, { force: true, recursive: true });
  }
  return out;
}

export function main(args = process.argv.slice(2)) {
  const { values } = parseArgs({
    args,
    options: { target: { type: 'string' }, postgres: { type: 'string' }, codex: { type: 'string' } },
  });
  if (!values.target || !values.postgres || !values.codex) {
    throw new Error('usage: desktop-app.mjs --target <triple> --postgres <set archive> --codex <package archive>');
  }
  const out = assemble({
    target: values.target,
    postgres: path.resolve(values.postgres),
    codex: path.resolve(values.codex),
    out: path.join(repositoryRoot, 'desktop', 'bundle'),
  });
  console.log(`desktop set: ${out}`);
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    main();
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}
