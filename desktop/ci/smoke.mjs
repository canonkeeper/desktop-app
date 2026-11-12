// SPDX-License-Identifier: AGPL-3.0-only
/* global process, console, setTimeout */
// The smoke run of the installed app in CI (specs/2026-11-12-desk-shell.md SH4.3):
//
//   node desktop/ci/smoke.mjs --exe <installed shell> --data <data directory>
//
// starts the shell as a user would, waits until its log says that the window loaded a page of the backend after the
// code exchange, stops it the way the system does — WM_CLOSE on Windows (the close of the window), SIGTERM on Linux
// (the backend sees its stdin close) — and checks that no process of the app is left. Only Node built-ins.
import { spawn, spawnSync } from 'node:child_process';
import { existsSync, readdirSync, readFileSync, readlinkSync } from 'node:fs';
import path from 'node:path';
import { parseArgs } from 'node:util';

const { values } = parseArgs({ options: { exe: { type: 'string' }, data: { type: 'string' }, timeout: { type: 'string' } } });
if (!values.exe || !values.data) throw new Error('usage: smoke.mjs --exe <installed shell> --data <data directory>');
const deadline = Number(values.timeout ?? 180) * 1000;
const windows = process.platform === 'win32';
const programs = ['canonkeeper-backend', 'postgres', 'codex-app-server'];

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

function logs(prefix) {
  const directory = path.join(values.data, 'logs');
  if (!existsSync(directory)) return '';
  return readdirSync(directory).filter((name) => name.startsWith(prefix)).sort()
    .map((name) => readFileSync(path.join(directory, name), 'utf8')).join('');
}

function leftovers() {
  if (windows) {
    const listed = spawnSync('tasklist', ['/FO', 'CSV', '/NH'], { encoding: 'utf8' }).stdout ?? '';
    return listed.split('\n').map((line) => line.split('","')[0].replace(/^"/u, '').toLowerCase())
      .filter((name) => programs.some((program) => name === `${program}.exe`));
  }
  const left = [];
  for (const pid of readdirSync('/proc').filter((name) => /^\d+$/u.test(name))) {
    try {
      const name = path.basename(readlinkSync(`/proc/${pid}/exe`));
      if (programs.includes(name)) left.push(`${name} (${pid})`);
    } catch {
      // Not ours, or gone.
    }
  }
  return left;
}

function report(verdict) {
  console.log('--- shell log');
  console.log(logs('shell-'));
  console.log('--- backend log (tail)');
  console.log(logs('backend-').split('\n').slice(-60).join('\n'));
  console.log(verdict);
}

async function waitFor(predicate, ms) {
  const until = Date.now() + ms;
  while (Date.now() < until) {
    if (predicate()) return true;
    await sleep(500);
  }
  return predicate();
}

const started = Date.now();
const shell = spawn(values.exe, [], { stdio: 'ignore' });
let exited = false;
shell.on('exit', () => {
  exited = true;
});

const opened = await waitFor(() => exited || /\spage_loaded$/mu.test(logs('shell-')), deadline);
if (exited || !opened) {
  report(exited ? 'FAIL: the shell ended before its window showed a page of the backend' : 'FAIL: no page of the backend in the window');
  if (!exited) shell.kill('SIGKILL');
  process.exit(1);
}
console.log(`the window shows a page of the backend after ${((Date.now() - started) / 1000).toFixed(1)} s`);

if (windows) {
  spawnSync('taskkill', ['/PID', String(shell.pid)], { stdio: 'inherit' });
} else {
  shell.kill('SIGTERM');
}
const stopped = await waitFor(() => exited, 150_000);
const clean = stopped && (await waitFor(() => leftovers().length === 0, 30_000));
if (!clean) {
  report(stopped ? `FAIL: processes left: ${leftovers().join(', ')}` : 'FAIL: the shell did not stop');
  process.exit(1);
}
report(`ok: stopped with no process left after ${((Date.now() - started) / 1000).toFixed(1)} s`);
