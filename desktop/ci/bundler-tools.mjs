// SPDX-License-Identifier: AGPL-3.0-only
/* global process, console, fetch, AbortSignal, Buffer, setTimeout */
// The files the bundler of Tauri takes from outside into an installer (specs/2026-11-13-desk-updates.md UB3.5), pinned by
// sha256 in desktop/release.json (bundler):
//
//   node desktop/ci/bundler-tools.mjs fetch --os linux|windows --tools <cache of the bundler>
//   node desktop/ci/bundler-tools.mjs verify --os linux|windows --tools <cache of the bundler>
//
// fetch downloads each from its release that does not move through the API of the release assets (gh; the download
// address of a release page answered 5xx on large files, DA1.9) — a file that is no asset of a release (the bootstrapper
// of WebView2) from the https address of that one file, where a redirect is a refusal —, refuses a sum that is not the
// pin before anything is laid, and lays each where the bundler takes it from its cache. verify, after tauri build, finds
// every file the bundler keeps as it was laid: one it took anew fails the job before its installers leave it. Only Node
// built-ins, gh and, for an archive, Python.
import { createHash } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { chmodSync, copyFileSync, existsSync, mkdirSync, mkdtempSync, readFileSync, renameSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { pathToFileURL } from 'node:url';
import { parseArgs } from 'node:util';

const release = () => JSON.parse(readFileSync(path.join(import.meta.dirname, '..', 'release.json'), 'utf8'));

export function sha256(file) {
  return createHash('sha256').update(readFileSync(file)).digest('hex');
}

/** The pins of a system, each checked: a release that does not move, a sum, a place inside the cache. */
export function pinsOf(pins, os) {
  const list = pins?.bundler?.[os];
  if (!Array.isArray(list) || list.length === 0) throw new Error(`desktop/release.json pins no file of the bundler for ${os}`);
  for (const pin of list) {
    if (pin.url !== undefined) {
      if (pin.repository !== undefined || pin.tag !== undefined) throw new Error(`${pin.asset}: an address and a release at once`);
      if (!pin.url.startsWith('https://')) throw new Error(`${pin.asset}: ${pin.url} is not https`);
      if (/fwlink|continuous|latest|nightly/iu.test(pin.url)) throw new Error(`${pin.asset}: ${pin.url} is an address that moves`);
    } else {
      if (!/^[\w.-]+\/[\w.-]+$/u.test(pin.repository ?? '')) throw new Error(`a pin names no repository: ${JSON.stringify(pin)}`);
      if (!pin.tag || /continuous|latest|nightly/u.test(pin.tag)) throw new Error(`${pin.asset}: ${pin.tag} is a release that moves`);
    }
    if (!/^[\w.-]+$/u.test(pin.asset ?? '')) throw new Error(`a pin names no file: ${JSON.stringify(pin)}`);
    if (!/^[0-9a-f]{64}$/u.test(pin.sha256 ?? '')) throw new Error(`${pin.asset}: no sha256`);
    if (!pin.place || path.isAbsolute(pin.place) || pin.place.split('/').includes('..')) throw new Error(`${pin.asset}: no place in the cache`);
  }
  return list;
}

async function fromAddress(pin, file) {
  try {
    const response = await fetch(pin.url, { redirect: 'error', signal: AbortSignal.timeout(300_000) });
    if (!response.ok) return false;
    writeFileSync(file, Buffer.from(await response.arrayBuffer()));
    return true;
  } catch (error) {
    console.log(`${pin.asset}: ${error.message}`);
    return false;
  }
}

async function download(pin, into) {
  const file = path.join(into, pin.asset);
  for (let attempt = 1; attempt <= 5; attempt += 1) {
    const done = pin.url
      ? await fromAddress(pin, file)
      : spawnSync('gh', ['release', 'download', pin.tag, '-R', pin.repository, '-p', pin.asset, '-O', file, '--clobber'], { stdio: 'inherit' }).status === 0;
    if (done) return file;
    console.log(`${pin.asset}: attempt ${attempt} failed`);
    await new Promise((resolve) => setTimeout(resolve, 20_000));
  }
  throw new Error(`${pin.asset} did not download`);
}

async function fetchAll(pins, tools) {
  const staging = mkdtempSync(path.join(tmpdir(), 'canonkeeper-bundler-'));
  try {
    const files = [];
    for (const pin of pins) {
      const file = await download(pin, staging);
      const sum = sha256(file);
      if (sum !== pin.sha256) throw new Error(`sha256 of ${pin.asset} is ${sum}; the pin is ${pin.sha256}`);
      files.push(file);
    }
    mkdirSync(tools, { recursive: true });
    pins.forEach((pin, at) => {
      const place = path.join(tools, ...pin.place.split('/'));
      rmSync(place, { force: true, recursive: true });
      if (pin.unpack) {
        const result = spawnSync('python', ['-I', '-m', 'zipfile', '-e', files[at], tools], { stdio: 'inherit' });
        if (result.status !== 0) throw new Error(`${pin.asset} did not unpack`);
        renameSync(path.join(tools, pin.unpack), place);
      } else {
        mkdirSync(path.dirname(place), { recursive: true });
        copyFileSync(files[at], place);
        chmodSync(place, 0o755);
      }
      console.log(`${pin.place}: ${pin.url ?? `${pin.repository}@${pin.tag}`} ${pin.sha256}`);
    });
  } finally {
    rmSync(staging, { force: true, recursive: true });
  }
}

/** The files the bundler keeps as they were laid and found otherwise after a build. */
export function changed(pins, tools) {
  return pins.filter((pin) => !pin.patched && !pin.unpack).filter((pin) => {
    const place = path.join(tools, ...pin.place.split('/'));
    return !existsSync(place) || sha256(place) !== pin.sha256;
  }).map((pin) => pin.place);
}

export async function main(args = process.argv.slice(2)) {
  const [command, ...rest] = args;
  const { values } = parseArgs({ args: rest, options: { os: { type: 'string' }, tools: { type: 'string' } } });
  if (!['fetch', 'verify'].includes(command) || !values.os || !values.tools) {
    throw new Error('usage: bundler-tools.mjs fetch|verify --os linux|windows --tools <cache of the bundler>');
  }
  const pins = pinsOf(release(), values.os);
  if (command === 'fetch') {
    await fetchAll(pins, values.tools);
    return;
  }
  const different = changed(pins, values.tools);
  if (different.length > 0) throw new Error(`the bundler took anew: ${different.join(', ')}`);
  console.log('the bundler built with the pinned files');
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch((error) => {
    console.error(error.message);
    process.exitCode = 1;
  });
}
