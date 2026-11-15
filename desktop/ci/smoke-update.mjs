// SPDX-License-Identifier: AGPL-3.0-only
/* global process, console, setTimeout, fetch, FormData, Blob, URL */
// The smoke run of an update of the app (specs/2026-11-13-desk-updates.md UB3.7), in the job smoke-update of CI and by
// hand on a computer of a developer, with directories of its own:
//
//   node desktop/ci/smoke-update.mjs version                    prints the version of the shell (N)
//   node desktop/ci/smoke-update.mjs bump                       the version of the shell + 1 in its patch (N+1); prints it
//   node desktop/ci/smoke-update.mjs later-migration            one migration after the last of the tree (N+1 has it)
//   node desktop/ci/smoke-update.mjs config --port P --key K --out FILE
//                                                              the configuration of the smoke build over tauri.conf.json:
//                                                              the key of one run and the source on the loopback
//   node desktop/ci/smoke-update.mjs run --kind appimage|deb|rpm|nsis --from <installer of N> --release <directory of N+1>
//     --port P --from-version N --to-version N+1 --data <data directory> --work <directory>
//
// run serves the signed release N+1 on 127.0.0.1:P, where the smoke build looks for it; installs N; makes a saga with a
// book on the backend of N, playing its shell, and answers «Добро пожаловать» with the daily check on; starts N, which finds
// N+1 half a minute after its start and installs it — the backend of the smoke build presses «Обновить и перезапустить»
// itself — and starts N+1. Then N+1 runs alone, made the copy before its migration and keeps the saga and its book, and no
// process of the app is left after it stops. Linux runs it under xvfb with a session bus; a package of Linux is installed
// by the pkexec of the runner (desktop/ci/pkexec.sh). Only Node built-ins.
import { spawn, spawnSync } from 'node:child_process';
import { chmodSync, copyFileSync, createReadStream, existsSync, mkdirSync, readdirSync, readFileSync, readlinkSync, statSync, writeFileSync } from 'node:fs';
import { createServer } from 'node:http';
import path from 'node:path';
import { pathToFileURL } from 'node:url';
import { parseArgs } from 'node:util';

const repositoryRoot = path.resolve(import.meta.dirname, '..', '..');
const shellManifest = path.join(repositoryRoot, 'desktop', 'src-tauri', 'Cargo.toml');
const shellLock = path.join(repositoryRoot, 'desktop', 'src-tauri', 'Cargo.lock');
const migrations = path.join(repositoryRoot, 'backend', 'db', 'migrations');
const windows = process.platform === 'win32';
const programs = ['canonkeeper', 'canonkeeper-backend', 'postgres', 'codex-app-server'];
const book = '# Глава 1. Сигнальные огни\n\nВера поднялась на стену заставы, когда над перевалом погасли сигнальные огни.\n\n' +
  '# Глава 2. Перевал\n\nДозорные спустились к перевалу на рассвете и нашли следы чужого отряда.\n';

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

/** The version of the package of the shell in its Cargo.toml. */
export function versionOf(manifest) {
  const version = /\[package\]\s*\nname = "canonkeeper-desktop"\s*\nversion = "([^"]+)"/u.exec(manifest)?.[1];
  if (!version) throw new Error('the shell names no version of its package');
  return version;
}

/** The next version of N in its patch: N+1 of the smoke run. */
export function nextVersion(version) {
  const parts = /^(\d+)\.(\d+)\.(\d+)$/u.exec(version);
  if (!parts) throw new Error(`${version} is not a version of the shell`);
  return `${parts[1]}.${parts[2]}.${Number(parts[3]) + 1}`;
}

/** The manifest and the lock of the shell with another version of its package. */
export function withVersion(manifest, lock, version) {
  const shell = /(\[package\]\s*\nname = "canonkeeper-desktop"\s*\nversion = ")[^"]+(")/u;
  const locked = /(\[\[package\]\]\s*\nname = "canonkeeper-desktop"\s*\nversion = ")[^"]+(")/u;
  if (!shell.test(manifest) || !locked.test(lock)) throw new Error('the shell names no version of its package');
  return { manifest: manifest.replace(shell, `$1${version}$2`), lock: lock.replace(locked, `$1${version}$2`) };
}

/** The name of a migration after the last of the tree. */
export function laterMigration(names) {
  const numbers = names.map((name) => /^(\d{4})_.+\.sql$/u.exec(name)?.[1]).filter(Boolean).map(Number);
  if (numbers.length === 0) throw new Error('the tree has no migration');
  return `${String(Math.max(...numbers) + 1).padStart(4, '0')}_smoke_update_later.sql`;
}

/** The configuration of the smoke build over tauri.conf.json: the key of one run, the source on the loopback. */
export function smokeConfig(port, key) {
  return {
    plugins: {
      updater: {
        pubkey: key,
        endpoints: [`http://127.0.0.1:${port}/latest.json`],
        dangerousInsecureTransportProtocol: true,
      },
    },
  };
}

/** Where a kind of installation puts the shell and what the backend of N is run from to make the saga. */
export function installation(kind, { work, localAppData }) {
  switch (kind) {
    case 'appimage': {
      const file = path.join(work, 'CanonKeeper.AppImage');
      return { exe: file, file };
    }
    case 'deb':
    case 'rpm':
      return { exe: '/usr/bin/canonkeeper', backend: '/usr/bin/canonkeeper-backend', resources: '/usr/lib/CanonKeeper' };
    case 'nsis': {
      const directory = path.join(localAppData ?? '', 'CanonKeeper');
      return { exe: path.join(directory, 'canonkeeper.exe'), backend: path.join(directory, 'canonkeeper-backend.exe'), resources: directory };
    }
    default:
      throw new Error(`${kind} is not a kind of installation`);
  }
}

function run(command, args, options = {}) {
  console.log(`$ ${command} ${args.join(' ')}`);
  const result = spawnSync(command, args, { stdio: 'inherit', ...options });
  if (result.status !== 0) throw new Error(`${command} failed (${result.status ?? result.signal})`);
}

/** A command as root: at once in a container, by sudo on a runner. */
function asRoot(command) {
  const [program, ...args] = process.getuid?.() === 0 ? command : ['sudo', '-n', ...command];
  run(program, args);
}

function install(kind, from, place) {
  if (kind === 'appimage') {
    copyFileSync(from, place.file);
    chmodSync(place.file, 0o755);
  } else if (kind === 'deb') {
    asRoot(['/usr/bin/apt-get', 'install', '-y', '-qq', path.resolve(from)]);
  } else if (kind === 'rpm') {
    asRoot(['/usr/bin/dnf', 'install', '-y', '-q', path.resolve(from)]);
  } else {
    run(path.resolve(from), ['/S']);
    if (!existsSync(place.exe)) throw new Error(`the installer put no ${place.exe}`);
  }
}

/** The backend and its resources inside an installed kind; an AppImage is unpacked beside it. */
function backendOf(kind, place, work, label) {
  if (kind !== 'appimage') return place;
  const into = path.join(work, `unpacked-${label}`);
  mkdirSync(into, { recursive: true });
  run(place.file, ['--appimage-extract'], { cwd: into, stdio: 'ignore' });
  const unpacked = path.join(into, 'squashfs-root');
  return { backend: path.join(unpacked, 'usr', 'bin', 'canonkeeper-backend'), resources: path.join(unpacked, 'usr', 'lib', 'CanonKeeper') };
}

/** The backend run as its shell runs it, over its stdin and stdout, with a client of its window. */
async function asTheShell({ backend, resources }, data, version, kind, work) {
  const child = spawn(backend, [], { stdio: ['pipe', 'pipe', 'inherit'], cwd: work });
  const lines = [];
  let buffer = '';
  child.stdout.on('data', (chunk) => {
    buffer += chunk.toString('utf8');
    for (let end = buffer.indexOf('\n'); end >= 0; end = buffer.indexOf('\n')) {
      lines.push(JSON.parse(buffer.slice(0, end)));
      buffer = buffer.slice(end + 1);
    }
  });
  const exited = new Promise((resolve) => child.on('exit', resolve));
  const send = (message) => child.stdin.write(`${JSON.stringify(message)}\n`);
  send({ type: 'start', dataDir: data, resources, secretsStore: 'file', version, updateKind: kind });
  let ready;
  for (const until = Date.now() + 900_000; !ready && Date.now() < until;) {
    ready = lines.find((line) => line.type === 'ready' || line.type === 'fatal');
    await sleep(200);
  }
  if (ready?.type !== 'ready') throw new Error(`the backend of ${version} answered ${JSON.stringify(ready ?? 'nothing')}`);
  const origin = `http://127.0.0.1:${ready.port}`;
  let cookie = '';
  const call = async (method, where, body, headers = {}) => {
    const response = await fetch(origin + where, {
      method, body, headers: { Origin: origin, ...(cookie ? { Cookie: `ck_session=${cookie}` } : {}), ...headers },
    });
    for (const set of response.headers.getSetCookie()) {
      const value = /^ck_session=([^;]*)/u.exec(set)?.[1];
      if (value) cookie = value;
    }
    const text = await response.text();
    return { status: response.status, json: text ? JSON.parse(text) : null };
  };
  const token = async () => (await call('GET', '/api/v1/auth/csrf')).json.csrfToken;
  const write = async (method, where, body, extra = {}) => call(method, where, JSON.stringify(body), {
    'Content-Type': 'application/json', 'X-CSRF-Token': await token(), ...extra,
  });
  const opened = await write('POST', '/api/v1/desktop/session', { code: ready.code });
  if (opened.status !== 200) throw new Error(`the session of the window = ${opened.status}`);
  const stop = async () => {
    send({ type: 'shutdown' });
    child.stdin.end();
    await exited;
  };
  return { call, write, token, stop };
}

async function waitFor(what, predicate, ms) {
  for (const until = Date.now() + ms; Date.now() < until;) {
    if (await predicate()) return;
    await sleep(1000);
  }
  if (!(await predicate())) throw new Error(`no ${what} in ${ms / 1000} s`);
}

/** The saga of the run: made on N, read on N+1. */
async function makeSaga(window) {
  const created = await window.write('POST', '/api/v1/projects', { title: 'Северный предел' });
  if (created.status !== 201) throw new Error(`createProject = ${created.status}`);
  const form = new FormData();
  form.append('file', new Blob([book], { type: 'text/markdown' }), 'Застава у перевала.md');
  const imported = await window.call('POST', `/api/v1/projects/${created.json.id}/imports`, form, {
    'X-CSRF-Token': await window.token(), 'Idempotency-Key': 'smoke-update-book',
  });
  if (imported.status !== 202) throw new Error(`importBookFile = ${imported.status}`);
  await waitFor('book of two chapters', async () => {
    const books = await window.call('GET', `/api/v1/projects/${created.json.id}/books`);
    return books.json?.data?.some((entry) => entry.chapterCount === 2);
  }, 180_000);
  const welcomed = await window.write('POST', '/api/v1/desktop/welcome', { checkUpdates: true });
  if (welcomed.status !== 204) throw new Error(`dismissWelcome = ${welcomed.status}`);
}

async function readSaga(window, version) {
  const state = await window.call('GET', '/api/v1/desktop/state');
  if (state.json?.version !== version) throw new Error(`getDesktopState names ${state.json?.version}, not ${version}`);
  const sagas = await window.call('GET', '/api/v1/projects');
  const saga = sagas.json?.data?.find((entry) => entry.title === 'Северный предел');
  if (!saga) throw new Error('the saga is gone after the update');
  const books = await window.call('GET', `/api/v1/projects/${saga.id}/books`);
  if (!books.json?.data?.some((entry) => entry.chapterCount === 2)) throw new Error('the book of the saga is gone after the update');
}

function serve(directory, port) {
  const server = createServer((request, response) => {
    const name = decodeURIComponent(new URL(request.url, 'http://127.0.0.1').pathname.slice(1));
    const file = path.join(directory, name);
    if (name.includes('/') || name.includes('\\') || !existsSync(file) || !statSync(file).isFile()) {
      response.writeHead(404).end();
      return;
    }
    console.log(`source: ${request.method} /${name}`);
    response.writeHead(200, { 'Content-Length': statSync(file).size });
    createReadStream(file).pipe(response);
  });
  return new Promise((resolve) => server.listen(port, '127.0.0.1', () => resolve(server)));
}

function logs(data, prefix) {
  const directory = path.join(data, 'logs');
  if (!existsSync(directory)) return '';
  return readdirSync(directory).filter((name) => name.startsWith(prefix)).sort()
    .map((name) => readFileSync(path.join(directory, name), 'utf8')).join('');
}

/**
 * N+1 runs after the update: settings.json names it, the shell installed a kind and loaded the page twice. The line of the
 * shell log that says it did not install N+1 — update_install_refused, update_not_installed — ends the wait at once with
 * that line (specs/2026-11-15-desk-ci-shell.md CS6.2).
 */
export async function waitForTheUpdate(data, version, ms) {
  const settings = () => {
    try {
      return JSON.parse(readFileSync(path.join(data, 'settings.json'), 'utf8'));
    } catch {
      return {};
    }
  };
  await waitFor('N+1 running after the update', () => {
    const shell = logs(data, 'shell-');
    const refused = shell.split('\n').find((line) => /\s(?:update_install_refused|update_not_installed)\b/u.test(line));
    if (refused) throw new Error(`the shell did not install N+1: ${refused.trim()}`);
    return settings().version === version && /update_installing kind=/u.test(shell) && (shell.match(/\spage_loaded$/gmu) ?? []).length >= 2;
  }, ms);
}

/** What marks the shell of this run in its environment: a computer of a developer runs other copies of the app. */
const marker = `CANONKEEPER_SMOKE_RUN=${process.pid}-${Date.now()}`;

function readable(read) {
  try {
    return read();
  } catch {
    return null;
  }
}

/**
 * The processes of the app of this run, by name: on Windows by the list of tasks (a runner runs nothing else of the
 * app); on Linux the shell with the mark of the run, every process that names the data directory (PostgreSQL runs
 * apart from its backend) and every process under them.
 */
function running(data) {
  if (windows) {
    const listed = spawnSync('tasklist', ['/FO', 'CSV', '/NH'], { encoding: 'utf8' }).stdout ?? '';
    return listed.split('\n').map((line) => line.split('","')[0].replace(/^"/u, '').toLowerCase().replace(/\.exe$/u, ''))
      .filter((name) => programs.includes(name)).map((name) => ({ name }));
  }
  const all = [];
  for (const pid of readdirSync('/proc').filter((name) => /^\d+$/u.test(name))) {
    const name = readable(() => path.basename(readlinkSync(`/proc/${pid}/exe`)));
    const stat = readable(() => readFileSync(`/proc/${pid}/stat`, 'utf8'));
    if (!name || !stat) continue;
    const ppid = Number(stat.slice(stat.lastIndexOf(')') + 2).split(' ')[1]);
    const names = () => readable(() => readFileSync(`/proc/${pid}/cmdline`, 'utf8'))?.includes(data) ||
      readable(() => readFileSync(`/proc/${pid}/environ`))?.includes(marker) ||
      (readable(() => readdirSync(`/proc/${pid}/fd`)) ?? []).some((fd) => readable(() => readlinkSync(`/proc/${pid}/fd/${fd}`))?.startsWith(data));
    all.push({ pid: Number(pid), ppid, name, ours: Boolean(names()) });
  }
  const family = new Set(all.filter((entry) => entry.ours).map((entry) => entry.pid));
  for (let grown = true; grown;) {
    grown = false;
    for (const entry of all) {
      if (!family.has(entry.pid) && family.has(entry.ppid)) {
        family.add(entry.pid);
        grown = true;
      }
    }
  }
  return all.filter((entry) => family.has(entry.pid) && programs.includes(entry.name));
}

function stopTheApp(data) {
  if (windows) {
    spawnSync('taskkill', ['/IM', 'canonkeeper.exe'], { stdio: 'inherit' });
    return;
  }
  for (const shell of running(data).filter((entry) => entry.name === 'canonkeeper')) {
    try {
      process.kill(shell.pid, 'SIGTERM');
    } catch {
      // Gone.
    }
  }
}

function backups(data) {
  const directory = path.join(data, 'backups');
  if (!existsSync(directory)) return [];
  return readdirSync(directory).filter((name) => name.startsWith('pre-') && existsSync(path.join(directory, name, 'complete')));
}

async function runUpdate(values) {
  const { kind, port } = values;
  const work = path.resolve(values.work);
  const data = path.resolve(values.data);
  mkdirSync(work, { recursive: true });
  const place = installation(kind, { work, localAppData: process.env.LOCALAPPDATA });
  const source = await serve(path.resolve(values.release), Number(port));
  try {
    install(kind, values.from, place);
    const before = await asTheShell(backendOf(kind, place, work, 'from'), data, values['from-version'], kind, work);
    await makeSaga(before);
    await before.stop();
    console.log(`N ${values['from-version']}: the saga and its book are made, «Добро пожаловать» is answered`);

    const [mark, value] = marker.split('=');
    const shell = spawn(place.exe, [], { stdio: 'ignore', detached: !windows, env: { ...process.env, [mark]: value } });
    shell.unref();
    await waitForTheUpdate(data, values['to-version'], 900_000);
    console.log(`N+1 ${values['to-version']} runs; the shell log:\n${logs(data, 'shell-')}`);
    const copies = backups(data);
    if (copies.length === 0) throw new Error('no copy was made before the migration of N+1');
    const programsNow = running(data).map((entry) => entry.name);
    for (const name of ['canonkeeper', 'canonkeeper-backend']) {
      const count = programsNow.filter((program) => program === name).length;
      if (count !== 1) throw new Error(`${count} processes ${name} run after the update: ${programsNow.join(', ')}`);
    }
    stopTheApp(data);
    await waitFor('the app stopped with no process left', () => running(data).length === 0, 180_000);

    const after = await asTheShell(backendOf(kind, place, work, 'to'), data, values['to-version'], kind, work);
    await readSaga(after, values['to-version']);
    await after.stop();
    console.log(`ok: ${kind} ${values['from-version']} → ${values['to-version']}; the copy ${copies.join(', ')}; the saga is read`);
  } catch (error) {
    console.log(`--- shell log\n${logs(data, 'shell-')}\n--- backend log (tail)\n${logs(data, 'backend-').split('\n').slice(-80).join('\n')}`);
    stopTheApp(data);
    throw error;
  } finally {
    source.close();
  }
}

export async function main(args = process.argv.slice(2)) {
  const [command, ...rest] = args;
  const { values } = parseArgs({
    args: rest,
    options: Object.fromEntries(['kind', 'from', 'release', 'port', 'from-version', 'to-version', 'data', 'work', 'key', 'out']
      .map((name) => [name, { type: 'string' }])),
  });
  switch (command) {
    case 'version':
      console.log(versionOf(readFileSync(shellManifest, 'utf8')));
      return;
    case 'bump': {
      const manifest = readFileSync(shellManifest, 'utf8');
      const next = nextVersion(versionOf(manifest));
      const changed = withVersion(manifest, readFileSync(shellLock, 'utf8'), next);
      writeFileSync(shellManifest, changed.manifest);
      writeFileSync(shellLock, changed.lock);
      console.log(next);
      return;
    }
    case 'later-migration': {
      const name = laterMigration(readdirSync(migrations));
      writeFileSync(path.join(migrations, name), '-- The later migration of N+1 in the smoke run of an update.\nSELECT 1;\n');
      console.log(name);
      return;
    }
    case 'config': {
      if (!values.port || !values.key || !values.out) throw new Error('usage: config --port P --key K --out FILE');
      writeFileSync(values.out, `${JSON.stringify(smokeConfig(values.port, values.key), null, 2)}\n`);
      return;
    }
    case 'run': {
      for (const name of ['kind', 'from', 'release', 'port', 'from-version', 'to-version', 'data', 'work']) {
        if (!values[name]) throw new Error(`run needs --${name}`);
      }
      await runUpdate(values);
      return;
    }
    default:
      throw new Error('usage: smoke-update.mjs bump | later-migration | config … | run …');
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().then(() => process.exit(0), (error) => {
    console.error(`FAIL: ${error.message}`);
    process.exit(1);
  });
}
