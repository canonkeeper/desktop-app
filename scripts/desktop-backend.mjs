// SPDX-License-Identifier: AGPL-3.0-only
// Builds the backend of the desktop app with the desktop build of the web inside it (specs/2026-11-07-desktop-app.md
// DA1.2, DA3.6):
//
//   node scripts/desktop-backend.mjs <output binary>
//
// It builds the web edition desktop into dist-desktop, puts it into backend/internal/app/desktop/web — the directory
// the backend embeds, where git keeps only its README.md — and builds cmd/desktop with CGO_ENABLED=0 for GOOS and
// GOARCH of the environment and the version of the shell's Cargo.toml.
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

// backendBuildArgs are the arguments of go build for cmd/desktop: small, without local paths, with the version of the app.
export function backendBuildArgs(version, output) {
  return ['build', '-trimpath', `-ldflags=-s -w -X main.version=${version}`, '-o', output, './cmd/desktop'];
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
  if (args.length !== 1) throw new Error('usage: desktop-backend.mjs <output binary>');
  const output = path.resolve(args[0]);
  buildDesktopWeb();
  placeWeb(path.join(repositoryRoot, 'dist-desktop'), embeddedWeb);
  const version = shellVersion(readFileSync(path.join(repositoryRoot, 'desktop', 'src-tauri', 'Cargo.toml'), 'utf8'));
  run('go', backendBuildArgs(version, output), {
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
