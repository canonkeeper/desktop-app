// SPDX-License-Identifier: AGPL-3.0-only
/* global process, console, setTimeout */
// The smoke run of the installed app in CI (specs/2026-11-12-desk-shell.md SH4.3):
//
//   node desktop/ci/smoke.mjs --exe <installed shell> --data <data directory> [--bridge <program>] [--appimage <file>]
//
// starts the shell as a user would, waits until its log says that the window loaded a page of the backend after the
// code exchange, stops it the way the system does — WM_CLOSE on Windows (the close of the window), SIGTERM on Linux
// (the backend sees its stdin close) — and checks that no process of the app is left. With --bridge it runs the program of
// the bridge for the programs of the author as Claude Code or Codex would (specs/2026-11-15-desk-mcp.md MC1.1): initialize
// and tools/list without the app, the reading turned on in settings.json before the start, tools/call sagas answered by the
// running app — and with --appimage the same through `<AppImage> mcp`, which is no second start of the app —, and no process
// of the bridge left once its stdin closed. Only Node built-ins.
import { spawn, spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, readdirSync, readFileSync, readlinkSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { parseArgs } from 'node:util';

const { values } = parseArgs({
  options: { exe: { type: 'string' }, data: { type: 'string' }, timeout: { type: 'string' }, bridge: { type: 'string' }, appimage: { type: 'string' } },
});
if (!values.exe || !values.data) throw new Error('usage: smoke.mjs --exe <installed shell> --data <data directory>');
const deadline = Number(values.timeout ?? 180) * 1000;
const windows = process.platform === 'win32';
const programs = ['canonkeeper-backend', 'postgres', 'codex-app-server', 'canonkeeper-mcp'];

/** The words of a closed door (backend/internal/delivery/mcp/programtools/words.go, WordsClosed). */
const closed = 'CanonKeeper закрыт или чтение саг выключено';

/**
 * Runs the bridge with the data directory, sends initialize and one request, and answers its answer; the stdin closes after
 * it, and the bridge ends by itself.
 */
async function askBridge(command, args, method, params) {
  const bridge = spawn(command, [...args, values.data], { stdio: ['pipe', 'pipe', 'inherit'] });
  let out = '';
  bridge.stdout.on('data', (chunk) => {
    out += chunk;
  });
  const ended = new Promise((resolve) => bridge.on('exit', (code) => resolve(code)));
  bridge.stdin.write(`${JSON.stringify({ jsonrpc: '2.0', id: 1, method: 'initialize', params: { protocolVersion: '2025-06-18', clientInfo: { name: 'smoke' } } })}\n`);
  bridge.stdin.write(`${JSON.stringify({ jsonrpc: '2.0', id: 2, method, params })}\n`);
  const answered = await waitFor(() => out.split('\n').some((line) => line.includes('"id":2')), 30_000);
  bridge.stdin.end();
  const code = await Promise.race([ended, sleep(10_000).then(() => 'running')]);
  if (!answered || code !== 0) throw new Error(`the bridge ${command} answered ${answered ? '' : 'nothing '}and ended with ${code}`);
  return JSON.parse(out.split('\n').find((line) => line.includes('"id":2')));
}

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

if (values.bridge) {
  const listed = await askBridge(values.bridge, [], 'tools/list', {});
  if (!(listed.result?.tools?.length > 0)) throw new Error('tools/list of the bridge without the app is empty');
  console.log(`the bridge lists ${listed.result.tools.length} tools without the app`);
  // The reading by the programs is turned on before the start, as the author leaves it (MC3.1).
  mkdirSync(values.data, { recursive: true });
  const settingsPath = path.join(values.data, 'settings.json');
  const settings = existsSync(settingsPath) ? JSON.parse(readFileSync(settingsPath, 'utf8')) : {};
  writeFileSync(settingsPath, JSON.stringify({ ...settings, programs: { enabled: true, ever: true } }));
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

if (values.bridge) {
  const runs = [[values.bridge, []], ...(values.appimage ? [[values.appimage, ['mcp']]] : [])];
  for (const [command, args] of runs) {
    const answer = await askBridge(command, args, 'tools/call', { name: 'sagas', arguments: {} });
    const text = answer.result?.content?.[0]?.text ?? '';
    if (!text || text.startsWith(closed)) {
      report(`FAIL: the running app did not answer sagas through ${command}: ${text}`);
      shell.kill('SIGKILL');
      process.exit(1);
    }
    console.log(`sagas through ${path.basename(command)}: answered`);
  }
  if (/\ssecond_instance$/mu.test(logs('shell-'))) {
    report('FAIL: the bridge of the AppImage started the app a second time');
    shell.kill('SIGKILL');
    process.exit(1);
  }
}

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
