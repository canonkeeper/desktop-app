// SPDX-License-Identifier: AGPL-3.0-only
// Builds the backend of the desktop app with the desktop build of the web inside it (specs/2026-11-07-desktop-app.md
// DA1.2, DA3.6):
//
//   node scripts/desktop-backend.mjs [--smoke] <output binary>
//   node scripts/desktop-backend.mjs --bridge <output binary>
//
// It builds the web edition desktop into dist-desktop, puts it into backend/internal/app/desktop/web — the directory
// the backend embeds, where git keeps only its README.md — and builds cmd/desktop with CGO_ENABLED=0 for GOOS and
// GOARCH of the environment and the version of the shell's Cargo.toml. --smoke builds the backend of the smoke run of an
// update in CI (the build tag smokeupdate, specs/2026-11-13-desk-updates.md UB3.7): it presses «Обновить и перезапустить»
// itself, and the release check refuses its mark; only the job of that run asks for it. --bridge builds the program of the
// bridge for the programs of the author, cmd/desktop-mcp, with the same flags and version and without the web
// (specs/2026-11-15-desk-mcp.md MC1.1).
import { spawnSync } from 'node:child_process';
import { cpSync, readdirSync, readFileSync, rmSync } from 'node:fs';
import path from 'node:path';
import { pathToFileURL } from 'node:url';

import { buildDesktopWeb } from './desktop-web.mjs';

const repositoryRoot = path.resolve(import.meta.dirname, '..');
export const embeddedWeb = path.join(repositoryRoot, 'backend', 'internal', 'app', 'desktop', 'web');

// shellVersion is the version of the package of the shell in its Cargo.toml: the installer and the shell name it, and the
// backend is built with it, so the window, the backend and the installer name one (specs/2026-11-13-desk-settings.md ST4.2).
export function shellVersion(manifest) {
  const pkg = /^\[package\]\s*$([\s\S]*?)(?=^\[|(?![\s\S]))/mu.exec(manifest)?.[1] ?? '';
  const version = /^version\s*=\s*"([^"]+)"\s*$/mu.exec(pkg)?.[1];
  if (!version) throw new Error('the Cargo.toml of the shell names no version of its package');
  return version;
}

// backendBuildArgs are the arguments of go build for cmd/desktop: small, without local paths, with the version of the app;
// the smoke build of an update with its tag.
export function backendBuildArgs(version, output, { smoke = false } = {}) {
  const tags = smoke ? ['-tags=smokeupdate'] : [];
  return ['build', '-trimpath', ...tags, `-ldflags=-s -w -X main.version=${version}`, '-o', output, './cmd/desktop'];
}

// bridgeBuildArgs are the arguments of go build for cmd/desktop-mcp: the flags and the version of the backend.
export function bridgeBuildArgs(version, output) {
  return ['build', '-trimpath', `-ldflags=-s -w -X main.version=${version}`, '-o', output, './cmd/desktop-mcp'];
}

// placeWeb replaces what an earlier build left in the embedded directory with the files of the new build.
export function placeWeb(built, embedded) {
  for (const name of readdirSync(embedded)) {
    if (name !== 'README.md') rmSync(path.join(embedded, name), { force: true, recursive: true });
  }
  cpSync(built, embedded, { recursive: true });
}

function run(command, args, options) {
  const result = spawnSync(command, args, { stdio: 'inherit', ...options });
  if (result.status !== 0) throw new Error(`${command} ${args.join(' ')} failed`);
}

export function main(args = process.argv.slice(2)) {
  if (args[0] === '--bridge') {
    if (args.length !== 2) throw new Error('usage: desktop-backend.mjs --bridge <output binary>');
    const version = shellVersion(readFileSync(path.join(repositoryRoot, 'desktop', 'src-tauri', 'Cargo.toml'), 'utf8'));
    run('go', bridgeBuildArgs(version, path.resolve(args[1])), {
      cwd: path.join(repositoryRoot, 'backend'), env: { ...process.env, CGO_ENABLED: '0' },
    });
    return;
  }
  const smoke = args[0] === '--smoke';
  const rest = smoke ? args.slice(1) : args;
  if (rest.length !== 1) throw new Error('usage: desktop-backend.mjs [--smoke] <output binary>');
  const output = path.resolve(rest[0]);
  buildDesktopWeb();
  placeWeb(path.join(repositoryRoot, 'dist-desktop'), embeddedWeb);
  const version = shellVersion(readFileSync(path.join(repositoryRoot, 'desktop', 'src-tauri', 'Cargo.toml'), 'utf8'));
  run('go', backendBuildArgs(version, output, { smoke }), {
    cwd: path.join(repositoryRoot, 'backend'), env: { ...process.env, CGO_ENABLED: '0' },
  });
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    main();
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}
