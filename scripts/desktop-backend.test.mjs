// SPDX-License-Identifier: AGPL-3.0-only
import assert from 'node:assert/strict';
import { existsSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { test } from 'node:test';
import { placeWeb } from './desktop-backend.mjs';
import { desktopWebBuild } from './desktop-web.mjs';

// specs/2026-11-07-desktop-app.md DA3.6: the backend embeds the new build of the web and nothing an earlier build left.
test('the embedded web is the new build and the README of the directory', () => {
  const root = mkdtempSync(path.join(tmpdir(), 'desktop-backend-'));
  try {
    const built = path.join(root, 'dist-desktop');
    const embedded = path.join(root, 'web');
    mkdirSync(path.join(built, 'assets'), { recursive: true });
    mkdirSync(path.join(embedded, 'assets'), { recursive: true });
    writeFileSync(path.join(built, 'index.html'), '<!doctype html>');
    writeFileSync(path.join(built, 'assets', 'app-2.js'), '2');
    writeFileSync(path.join(embedded, 'README.md'), 'readme');
    writeFileSync(path.join(embedded, 'assets', 'app-1.js'), '1');
    placeWeb(built, embedded);
    assert.deepEqual(readdirSync(embedded).sort(), ['README.md', 'assets', 'index.html']);
    assert.deepEqual(readdirSync(path.join(embedded, 'assets')), ['app-2.js']);
  } finally {
    rmSync(root, { force: true, recursive: true });
  }
});

// The first Windows build of the installers (13.11.2026): the web of the app is built by Node itself, with the edition in
// the environment of the build — no npm script of the build sets a variable in the syntax of a POSIX shell.
test('the web of the app builds without a shell: Node runs vite with the edition in the environment', () => {
  const { command, args, options } = desktopWebBuild({ PATH: 'p' });
  assert.equal(command, process.execPath);
  assert.ok(existsSync(args[0]) && args[0].endsWith(path.join('vite', 'bin', 'vite.js')), args[0]);
  assert.deepEqual(args.slice(1), ['build', '--outDir', 'dist-desktop']);
  assert.deepEqual(options.env, { PATH: 'p', CK_EDITION: 'desktop' });
  assert.equal(options.shell, undefined);
  const scripts = JSON.parse(readFileSync(path.join(import.meta.dirname, '..', 'package.json'), 'utf8')).scripts;
  assert.equal(scripts['build:desktop'], 'node scripts/desktop-web.mjs');
  for (const name of ['build:desktop', 'build:desktop:backend']) {
    assert.doesNotMatch(scripts[name], /^[A-Z_][A-Z0-9_]*=/u, `${name} sets a variable in the syntax of a POSIX shell`);
  }
});
