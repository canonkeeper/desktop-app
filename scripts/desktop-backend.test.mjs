// SPDX-License-Identifier: AGPL-3.0-only
import assert from 'node:assert/strict';
import { mkdirSync, mkdtempSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { test } from 'node:test';
import { placeWeb } from './desktop-backend.mjs';

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
