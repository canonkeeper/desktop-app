// SPDX-License-Identifier: AGPL-3.0-only
import assert from 'node:assert/strict';
import { spawn, spawnSync } from 'node:child_process';
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import test from 'node:test';
import vm from 'node:vm';

import yaml from 'js-yaml';
import { JSDOM } from 'jsdom';

import { assemble, codexHomes, codexPin, postgresPrograms, sha256, targets } from './desktop-app.mjs';
import { importProblems, peImports, UCRT_DLLS, windowsDll } from './desktop-app-imports.mjs';
import { codexPackageVersion } from './codex-package.mjs';
import { changed, pinsOf } from '../desktop/ci/bundler-tools.mjs';
import { BACK, releaseBody } from '../desktop/ci/release-body.mjs';
import { laterMigration, nextVersion, smokeConfig, versionOf, waitForTheUpdate, withVersion } from '../desktop/ci/smoke-update.mjs';

const repositoryRoot = path.resolve(import.meta.dirname, '..');
const linux = 'x86_64-unknown-linux-gnu';
const source = 'a'.repeat(64);

function tar(args, cwd) {
  const result = spawnSync('tar', args, { cwd, encoding: 'utf8' });
  assert.equal(result.status, 0, result.stderr);
}

function writeFile(file, text, mode) {
  mkdirSync(path.dirname(file), { recursive: true });
  writeFileSync(file, text);
  if (mode) chmodSync(file, mode);
}

/**
 * A tree with the files the script reads — release.json, go.mod, codex-homes with a closed profile beside the open
 * one — and the two archives as the workflow brings them.
 */
function withStand(body, { tag = 'rust-v0.165.4', report = { version: '18.6', source_sha256: source }, programs = postgresPrograms } = {}) {
  const root = mkdtempSync(path.join(tmpdir(), 'canonkeeper-desktop-app-'));
  try {
    writeFile(path.join(root, 'backend', 'go.mod'), 'module x\n\nrequire (\n\tgithub.com/openai/codex/sdk/go v0.165.4\n)\n');
    writeFile(path.join(root, 'codex-homes', 'research-chat', 'AGENTS.md'), 'chat\n');
    writeFile(path.join(root, 'codex-homes', 'research-chat', 'skills', 'chat', 'SKILL.md'), 'skill\n');
    writeFile(path.join(root, 'codex-homes', 'writer-workshop', 'skills', 'writing', 'SKILL.md'), 'closed\n');

    const pg = path.join(root, 'work', 'pg-out');
    for (const program of programs) writeFile(path.join(pg, 'pg', 'bin', program), `#!/bin/sh\n# ${program}\n`, 0o755);
    writeFile(path.join(pg, 'pg', 'lib', 'libpq.so.5'), 'lib\n');
    writeFile(path.join(pg, 'pg', 'share', 'timezone', 'UTC'), 'tz\n');
    writeFile(path.join(pg, 'build.json'), JSON.stringify(report));
    const postgres = path.join(root, 'work', 'pg-set.tar');
    tar(['-cf', postgres, '-C', pg, '.']);

    const packageDir = path.join(root, 'work', 'codex-package');
    writeFile(path.join(packageDir, 'codex-package', 'bin', 'codex-app-server'), 'server\n', 0o755);
    writeFile(path.join(packageDir, 'codex-package', 'bin', 'bwrap'), 'bwrap\n', 0o755);
    const codex = path.join(root, 'work', `codex-package-${linux}.tar.gz`);
    tar(['-czf', codex, '-C', packageDir, '.']);

    const release = {
      codex: { repository: 'Dirard/codex', targets: { [linux]: { tag, asset: path.basename(codex), sha256: sha256(codex) } } },
      postgres: { version: '18.6', source: 'https://ftp.postgresql.org/pub/source/v18.6/postgresql-18.6.tar.bz2', sha256: source },
    };
    writeFile(path.join(root, 'desktop', 'release.json'), JSON.stringify(release));
    const out = path.join(root, 'desktop', 'bundle');
    const built = [];
    const build = (output, target) => {
      built.push([path.relative(out, output), target.goos]);
      writeFile(output, 'backend\n', 0o755);
    };
    body({ root, postgres, codex, out, build, built, release });
  } finally {
    rmSync(root, { force: true, recursive: true });
  }
}

test('the set lies out as the backend reads it and the bundle config names it', () => {
  withStand(({ root, postgres, codex, out, build, built }) => {
    assemble({ root, target: linux, postgres, codex, out, build });
    for (const program of postgresPrograms) assert.ok(existsSync(path.join(out, 'postgres', 'bin', program)), program);
    assert.equal(statSync(path.join(out, 'postgres', 'bin', 'postgres')).mode & 0o777, 0o755);
    assert.ok(existsSync(path.join(out, 'postgres', 'lib', 'libpq.so.5')));
    assert.ok(existsSync(path.join(out, 'postgres', 'share', 'timezone', 'UTC')));
    assert.equal(statSync(path.join(out, 'codex', 'codex-app-server')).mode & 0o777, 0o755);
    assert.ok(!existsSync(path.join(out, 'codex', 'bwrap')), 'only codex-app-server goes into the set');
    assert.ok(existsSync(path.join(out, 'codex-homes', 'research-chat', 'skills', 'chat', 'SKILL.md')));
    assert.ok(!existsSync(path.join(out, 'codex-homes', 'writer-workshop')), 'a closed profile went into the set');
    // specs/2026-11-15-desk-mcp.md MC1.1: the program of the bridge for the programs of the author lies beside the backend.
    assert.deepEqual(built, [[`canonkeeper-backend-${linux}`, 'linux'], [`canonkeeper-mcp-${linux}`, 'linux']]);
  });

  // What the backend reads under the directory of the set (backend/internal/app/desktop/build.go, Resources).
  const resources = readFileSync(path.join(repositoryRoot, 'backend', 'internal', 'app', 'desktop', 'build.go'), 'utf8');
  assert.match(resources, /filepath\.Join\(resources\.Root, "postgres", "bin"\)/u);
  assert.match(resources, /name := "codex-app-server"/u);
  assert.match(resources, /filepath\.Join\(resources\.Root, "codex", name\)/u);
  assert.match(resources, /filepath\.Join\(resources\.Root, "codex-homes"\)/u);
  assert.deepEqual(codexHomes, ['research-chat']);
  // The overlay of tauri build takes the set from desktop/bundle by the same names.
  const bundle = JSON.parse(readFileSync(path.join(repositoryRoot, 'desktop', 'src-tauri', 'tauri.bundle.json'), 'utf8')).bundle;
  assert.deepEqual(bundle.externalBin, ['../bundle/canonkeeper-backend', '../bundle/canonkeeper-mcp']);
  assert.deepEqual(bundle.resources, { '../bundle/postgres/': 'postgres/', '../bundle/codex/': 'codex/', '../bundle/codex-homes/': 'codex-homes/' });
});

test('a Codex package whose sha256 is not the pin is refused before anything is built', () => {
  withStand(({ root, postgres, codex, out, build, built }) => {
    const release = JSON.parse(readFileSync(path.join(root, 'desktop', 'release.json'), 'utf8'));
    release.codex.targets[linux].sha256 = 'b'.repeat(64);
    writeFileSync(path.join(root, 'desktop', 'release.json'), JSON.stringify(release));
    assert.throws(() => assemble({ root, target: linux, postgres, codex, out, build }), /sha256 of the Codex package .* the pin is b{64}/u);
    assert.deepEqual(built, []);
    assert.ok(!existsSync(out));
  });
});

test('a PostgreSQL set built from another source or without a program is refused', () => {
  withStand(({ root, postgres, codex, out, build, built }) => {
    assert.throws(() => assemble({ root, target: linux, postgres, codex, out, build }), /built from 18\.6 c{64}; the pin is 18\.6 a{64}/u);
    assert.deepEqual(built, []);
  }, { report: { version: '18.6', source_sha256: 'c'.repeat(64) } });
  withStand(({ root, postgres, codex, out, build }) => {
    assert.throws(() => assemble({ root, target: linux, postgres, codex, out, build }), /lacks pg_dump/u);
  }, { programs: postgresPrograms.filter((program) => program !== 'pg_dump') });
});

test('Codex is taken only at the version of backend/go.mod, and only for a pinned target', () => {
  withStand(({ root, postgres, codex, out, build }) => {
    assert.throws(() => assemble({ root, target: linux, postgres, codex, out, build }), /pinned at rust-v0\.165\.1; backend\/go\.mod needs rust-v0\.165\.4/u);
  }, { tag: 'rust-v0.165.1' });
  withStand(({ root, postgres, codex, out, build }) => {
    assert.throws(() => assemble({ root, target: 'x86_64-pc-windows-msvc', postgres, codex, out, build }), /pins no Codex package for x86_64-pc-windows-msvc/u);
    assert.throws(() => assemble({ root, target: 'aarch64-apple-darwin', postgres, codex, out, build }), /no installer for aarch64-apple-darwin/u);
  });
});

test('the pins of the repository are whole and Codex is the one of go.mod', () => {
  const release = JSON.parse(readFileSync(path.join(repositoryRoot, 'desktop', 'release.json'), 'utf8'));
  const goMod = readFileSync(path.join(repositoryRoot, 'backend', 'go.mod'), 'utf8');
  const version = codexPackageVersion(goMod);
  assert.match(release.postgres.sha256, /^[0-9a-f]{64}$/u);
  assert.match(release.postgres.source, new RegExp(`^https://ftp\\.postgresql\\.org/pub/source/v${release.postgres.version}/`, 'u'));
  for (const [target, pin] of Object.entries(release.codex.targets)) {
    assert.ok(targets[target], target);
    assert.match(pin.sha256, /^[0-9a-f]{64}$/u);
    assert.equal(pin.tag, `rust-v${version}`);
    assert.ok(pin.asset.includes(target), pin.asset);
    assert.deepEqual(codexPin(release, target, goMod), pin);
  }
});

test('the workflow builds both systems with pinned actions and a secret only where it signs', () => {
  const file = path.join(repositoryRoot, 'desktop', 'ci', 'desktop-build.yml');
  const text = readFileSync(file, 'utf8');
  const workflow = yaml.load(text);
  assert.deepEqual(workflow.permissions, { contents: 'read' });
  assert.deepEqual(workflow.jobs.app.strategy.matrix.include.map((entry) => [entry.os, entry.target]), [
    ['ubuntu-24.04', 'x86_64-unknown-linux-gnu'],
    ['windows-2025', 'x86_64-pc-windows-msvc'],
  ]);
  const steps = Object.values(workflow.jobs).flatMap((job) => job.steps);
  for (const step of steps.filter((entry) => entry.uses)) assert.match(step.uses, /^[\w.-]+\/[\w.-]+@[0-9a-f]{40}$/u, step.uses);
  assert.deepEqual(secretsOf(workflow), [['sign', 'Sign by the main key of the environment release']], 'a secret outside the step that signs');
  const commands = steps.map((step) => step.run ?? '').join('\n');
  // specs/2026-11-15-desk-ci-shell.md CS4.3: one run of the tests of the core names every failure of every test program.
  for (const needed of ['desktop/postgres/build.py', 'scripts/desktop-app.mjs', 'npx tauri build', '--bundles appimage', 'desktop/ci/smoke.mjs', 'cargo test --locked -p canonkeeper-desktop-core --no-fail-fast']) {
    assert.ok(commands.includes(needed), needed);
  }
  // The monorepo does not run it.
  assert.ok(!existsSync(path.join(repositoryRoot, '.github', 'workflows', 'desktop-build.yml')));
});

// specs/2026-11-13-desk-export.md EX2.2, EX2.3: the check of the content runs on a push to main or to a branch export/**,
// the build on a push to a branch export/** and on a tag v* — main only fast-forwards to a branch whose run built the same
// commit —, both by hand; a pull request runs nothing — the merge commit GitHub makes
// carries the identity of the account that opened it. The Windows job tests the copy of the Codex homes on a real
// Windows before it builds the installer. actionlint finds nothing.
test('the build runs on export/** and a tag, the check also on main, both by hand, never on a pull request', () => {
  const directory = path.join(repositoryRoot, 'desktop', 'ci');
  const workflows = readdirSync(directory).filter((name) => name.endsWith('.yml')).sort();
  assert.deepEqual(workflows, ['desktop-build.yml', 'forbidden.yml']);
  for (const name of workflows) {
    const text = readFileSync(path.join(directory, name), 'utf8');
    const workflow = yaml.load(text);
    const triggers = workflow.on;
    assert.deepEqual(Object.keys(triggers).sort(), ['push', 'workflow_dispatch'], name);
    assert.deepEqual(triggers.push.branches, name === 'desktop-build.yml' ? ['export/**'] : ['main', 'export/**'], name);
    assert.deepEqual(triggers.push.tags, name === 'desktop-build.yml' ? ['v*'] : undefined, name);
    assert.deepEqual(workflow.permissions, { contents: 'read' }, name);
    const steps = Object.values(workflow.jobs).flatMap((job) => job.steps);
    for (const step of steps.filter((entry) => entry.uses)) assert.match(step.uses, /^[\w.-]+\/[\w.-]+@[0-9a-f]{40}$/u, `${name}: ${step.uses}`);
    if (name === 'forbidden.yml') assert.ok(!/\bsecrets\./u.test(text), `${name} reads a secret`);
  }
  const forbidden = yaml.load(readFileSync(path.join(directory, 'forbidden.yml'), 'utf8'));
  assert.ok(forbidden.jobs.forbidden.steps.some((step) => step.run === 'python3 -I scripts/check-forbidden.py --ci'));

  const app = yaml.load(readFileSync(path.join(directory, 'desktop-build.yml'), 'utf8')).jobs.app.steps;
  const homes = app.findIndex((step) => /go test \.\/internal\/adapters\/codex -run 'Copy\|Hold' -count=1/u.test(step.run ?? ''));
  assert.ok(homes >= 0, 'no test of the copy of the Codex homes');
  assert.equal(app[homes].if, "runner.os == 'Windows'");
  assert.equal(app[homes]['working-directory'], 'backend');
  assert.ok(homes > app.findIndex((step) => step.uses?.startsWith('actions/setup-go@')));
  assert.ok(homes < app.findIndex((step) => /npx tauri build/u.test(step.run ?? '')));
});

test('actionlint finds nothing in the workflows', { skip: spawnSync('actionlint', ['-version']).status !== 0 && 'actionlint is missing' }, () => {
  const directory = path.join(repositoryRoot, 'desktop', 'ci');
  const files = readdirSync(directory).filter((name) => name.endsWith('.yml')).map((name) => path.join(directory, name));
  const result = spawnSync('actionlint', ['-no-color', ...files], { encoding: 'utf8' });
  assert.equal(result.status, 0, `${result.stdout}${result.stderr}`);
});

/**
 * Runs the code exchange of the window (desktop/src-tauri/crates/core/src/exchange.js) on `href` against answers by
 * path — a status, or `null` for a request that fails — and tells what it asked, where it went and when the code left
 * the address.
 */
async function exchange(href, answers = {}) {
  const script = readFileSync(path.join(repositoryRoot, 'desktop', 'src-tauri', 'crates', 'core', 'src', 'exchange.js'), 'utf8');
  const url = new URL(href);
  const asked = [];
  const went = [];
  const location = {
    protocol: url.protocol, hostname: url.hostname, pathname: url.pathname, search: url.search, hash: url.hash,
    replace: (to) => went.push(to),
  };
  const history = { replaceState: (_state, _title, to) => { location.hash = ''; asked.push(['address', to]); } };
  const fetch = async (where, init = {}) => {
    asked.push([init.method ?? 'GET', where, init.body ?? null, location.hash]);
    const status = answers[where] === undefined ? 404 : answers[where];
    if (status === null) throw new TypeError('Failed to fetch');
    return { status, json: async () => ({ csrfToken: 't' }) };
  };
  vm.runInNewContext(script, { location, history, fetch, JSON });
  for (let turn = 0; turn < 50 && went.length === 0; turn += 1) await new Promise((resolve) => setImmediate(resolve));
  return { asked, went };
}

const opening = 'http://127.0.0.1:24117/desktop/open?n=0#c=Zm9vYmFyYmF6cXV4cXV1eA';
const exchanged = { '/api/v1/auth/csrf': 200, '/api/v1/desktop/session': 200 };

test('the code exchange takes the code out of the address before it asks anything and opens the first page', async () => {
  const { asked, went } = await exchange(opening, { ...exchanged, '/api/v1/auth/session': 200 });
  assert.deepEqual(asked[0], ['address', '/desktop/open?n=0']);
  assert.ok(asked.slice(1).every((request) => request[3] === ''), 'a request was made while the code was in the address');
  assert.deepEqual(asked.slice(1).map(([method, where, body]) => [method, where, body]), [
    ['GET', '/api/v1/auth/csrf', null],
    ['POST', '/api/v1/desktop/session', JSON.stringify({ code: 'Zm9vYmFyYmF6cXV4cXV1eA' })],
    ['GET', '/api/v1/auth/session', null],
  ]);
  assert.deepEqual(went, ['/']);
});

test('a refused code and a lost session of the window are reported to the shell, which brings a new code', async () => {
  // The backend refuses a code that does not open the window with 403 (delivery/http/desktop/session.go).
  const refused = await exchange(opening, { ...exchanged, '/api/v1/desktop/session': 403 });
  assert.deepEqual(refused.went, ['/desktop/open?refused=403']);
  assert.ok(!refused.asked.some(([, where]) => where === '/api/v1/auth/session'));
  // The code opened the session, but the first question of the page gets 401: the window did not keep it (SH1.4).
  assert.deepEqual((await exchange(opening, { ...exchanged, '/api/v1/auth/session': 401 })).went, ['/desktop/open?refused=401']);
  // A backend without the question of the first page answers 404: the page decides then, not the shell.
  assert.deepEqual((await exchange(opening, exchanged)).went, ['/']);
  assert.deepEqual((await exchange(opening, { ...exchanged, '/api/v1/desktop/session': null })).went, ['/desktop/open?refused=0']);
});

test('the code exchange does nothing on any other page', async () => {
  for (const href of [
    'http://127.0.0.1:24117/chat#c=Zm9vYmFyYmF6cXV4cXV1eA',
    'http://localhost:24117/desktop/open#c=Zm9vYmFyYmF6cXV4cXV1eA',
    'https://127.0.0.1:24117/desktop/open#c=Zm9vYmFyYmF6cXV4cXV1eA',
    'http://127.0.0.1:24117/desktop/open#c=short',
    'http://127.0.0.1:24117/desktop/open?refused=403',
  ]) {
    const { asked, went } = await exchange(href, exchanged);
    assert.deepEqual([asked, went], [[], []], href);
  }
});

// specs/2026-11-16-desk-app-links.md AL1.4: a link that waited for the window rides its opening — `to`, which the core of the
// shell built of the checked values of the link, coded before the fragment —, and the exchange goes there instead of the
// home once the session is made; only an address of the page of the edition of exactly its form, anything else is the home.
const placeOfLink = '/manuscript/open?saga=0b7c1a52-5d1e-4f43-9f6b-2a8c1e0b9d44&chapter=7c3f0a52-5d1e-4f43-9f6b-2a8c1e0b9d45&p=15&rev=3';
const openingTo = (to) => `http://127.0.0.1:24117/desktop/open?n=0&to=${encodeURIComponent(to)}#c=Zm9vYmFyYmF6cXV4cXV1eA`;

test('the exchange goes to the place of a link that waited, and only of its own form', async () => {
  const ok = { ...exchanged, '/api/v1/auth/session': 200 };
  const { asked, went } = await exchange(openingTo(placeOfLink), ok);
  assert.deepEqual(asked[0], ['address', `/desktop/open?n=0&to=${encodeURIComponent(placeOfLink)}`]);
  assert.deepEqual(went, [placeOfLink]);
  assert.deepEqual((await exchange(openingTo('/manuscript/open?refused=1'), ok)).went, ['/manuscript/open?refused=1']);
  const bare = '/manuscript/open?saga=0b7c1a52-5d1e-4f43-9f6b-2a8c1e0b9d44&chapter=7c3f0a52-5d1e-4f43-9f6b-2a8c1e0b9d45';
  assert.deepEqual((await exchange(openingTo(bare), ok)).went, [bare]);
  // A refused code goes to its report, the place or not.
  assert.deepEqual((await exchange(openingTo(placeOfLink), { ...exchanged, '/api/v1/desktop/session': 403 })).went, ['/desktop/open?refused=403']);
  for (const to of [
    '/chat',
    '/',
    'https://evil.example/',
    '//evil.example/manuscript/open?refused=1',
    '/manuscript/open?refused=2',
    '/manuscript/books?projectId=p',
    `${placeOfLink}&x=1`,
    `${placeOfLink}#x`,
    placeOfLink.replace('0b7c1a52', '0B7C1A52'),
    placeOfLink.replace('p=15', 'p=015'),
    placeOfLink.replace('p=15', 'p=100000'),
    '/manuscript/open?saga=javascript:alert(1)&chapter=7c3f0a52-5d1e-4f43-9f6b-2a8c1e0b9d45',
  ]) {
    assert.deepEqual((await exchange(openingTo(to), ok)).went, ['/'], to);
  }
  // Not coded as the shell codes it, not where the shell puts it, or broken: the home.
  for (const href of [
    `http://127.0.0.1:24117/desktop/open?n=0&to=${placeOfLink}#c=Zm9vYmFyYmF6cXV4cXV1eA`,
    `http://127.0.0.1:24117/desktop/open?to=${encodeURIComponent(placeOfLink)}&n=0#c=Zm9vYmFyYmF6cXV4cXV1eA`,
    `http://127.0.0.1:24117/desktop/open?n=0&to=${encodeURIComponent(placeOfLink)}&to=%2F#c=Zm9vYmFyYmF6cXV4cXV1eA`,
    'http://127.0.0.1:24117/desktop/open?n=0&to=%E0%A4%A#c=Zm9vYmFyYmF6cXV4cXV1eA',
  ]) {
    assert.deepEqual((await exchange(href, ok)).went, ['/'], href);
  }
});

/**
 * Runs the script that gives the page the place of a link (desktop/src-tauri/crates/core/src/visit.js) on `href` with its
 * argument, as the core of the shell gives it; `takes` — whether a listener of the page takes the place (the edition of the
 * app does, src/editions/desktop/open-place/link-visits.tsx). Tells the events it sent and what it answered; the history is
 * not touched (AL1.4: the page follows the place by the navigation of its router).
 */
function visit(href, to, takes = true) {
  const script = readFileSync(path.join(repositoryRoot, 'desktop', 'src-tauri', 'crates', 'core', 'src', 'visit.js'), 'utf8');
  const url = new URL(href);
  const events = [];
  const history = new Proxy({}, { get: () => { throw new Error('the script touched the history'); } });
  class CustomEvent {
    constructor(type, init) { this.type = type; this.detail = init?.detail; this.cancelable = init?.cancelable === true; this.defaultPrevented = false; }
    preventDefault() { if (this.cancelable) this.defaultPrevented = true; }
  }
  const dispatchEvent = (event) => {
    events.push([event.type, event.detail]);
    if (takes) event.preventDefault();
    return !event.defaultPrevented;
  };
  const answer = vm.runInNewContext(script, {
    location: { protocol: url.protocol, hostname: url.hostname }, history, CustomEvent, dispatchEvent, CANONKEEPER_VISIT_TO: to,
  });
  return { answer, events };
}

test('the move of the window gives the page of its backend the place, and nothing else', () => {
  assert.deepEqual(visit('http://127.0.0.1:24117/chat?projectId=p', placeOfLink), { answer: true, events: [['canonkeeper:link', placeOfLink]] });
  assert.equal(visit('http://127.0.0.1:24117/settings', '/manuscript/open?refused=1').answer, true);
  // A page that does not take the place — not the app, or not yet — answers no: the shell notes navigate_failed.
  assert.deepEqual(visit('http://127.0.0.1:24117/chat', placeOfLink, false), { answer: false, events: [['canonkeeper:link', placeOfLink]] });
  for (const href of ['tauri://localhost/index.html', 'http://tauri.localhost/index.html', 'https://127.0.0.1:24117/', 'http://localhost:24117/']) {
    assert.deepEqual(visit(href, placeOfLink), { answer: false, events: [] }, href);
  }
  for (const to of ['/chat', 'javascript:alert(1)', `${placeOfLink}&x=1`, 17, undefined]) {
    assert.deepEqual(visit('http://127.0.0.1:24117/chat', to), { answer: false, events: [] }, String(to));
  }
  // One form of the place for both scripts and the listener of the edition of the app, one name of the event.
  const read = (...file) => readFileSync(path.join(repositoryRoot, ...file), 'utf8');
  const pattern = (text) => text.match(/const place = (\/\^\\\/manuscript[^\n]*\/)u?;/u)?.[1];
  const core = (file) => read('desktop', 'src-tauri', 'crates', 'core', 'src', file);
  const listener = read('src', 'editions', 'desktop', 'open-place', 'link-visits.tsx');
  assert.ok(pattern(core('visit.js')));
  assert.equal(pattern(core('exchange.js')), pattern(core('visit.js')));
  assert.equal(pattern(listener), pattern(core('visit.js')));
  assert.match(listener, /export const linkEvent = 'canonkeeper:link';/u);
});

test('the set is laid out again from scratch', () => {
  withStand(({ root, postgres, codex, out, build }) => {
    writeFile(path.join(out, 'codex-homes', 'stale', 'x'), 'left from an earlier run\n');
    assemble({ root, target: linux, postgres, codex, out, build });
    assert.ok(!existsSync(path.join(out, 'codex-homes', 'stale')));
    assert.ok(existsSync(path.join(out, 'codex', 'codex-app-server')));
  });
});

// specs/2026-11-13-desk-secrets.md SE1.5 (specs/2026-11-12-desk-shell.md SH3.2): the secrets of the app go between the
// shell and the backend only; the page of the window has the five commands of the shell and none of secrets.
// specs/2026-11-13-desk-upgrade.md UG3.4: the page of the start has its own list (capabilities/start.json), and no page
// has a permission of the updater. specs/2026-11-13-desk-obsidian.md OB1.2: the fifth is pick_folder; no page has a
// permission of the windows of the system or of the files (dialog:, fs:), and the command answers an outcome, never a path.
test('the page of the window has the five commands of the shell and none of secrets', () => {
  const shell = path.join(repositoryRoot, 'desktop', 'src-tauri');
  const four = ['open_external', 'reveal_data_dir', 'launch_notes', 'close_window', 'pick_folder'];
  const start = ['launch_state', 'reveal_data_dir', 'write_to_us', 'download_version', 'open_copy', 'restore_question', 'restore_copy', 'close_window'];
  const allowed = (names) => names.map((name) => `allow-${name.replaceAll('_', '-')}`);
  const every = [...new Set([...four, ...start])];
  const window = readFileSync(path.join(shell, 'src', 'shell.rs'), 'utf8');
  const handler = window.match(/generate_handler!\[([^\]]*)\]/u);
  assert.ok(handler, 'shell.rs names no handler of commands');
  // A command of the page of the start, and pick_folder, are named by their modules (src/shell/start_page.rs, folder.rs).
  assert.deepEqual(handler[1].split(',').map((name) => name.trim().replace(/^(start_page|folder)::/u, '')).filter(Boolean).toSorted(), every.toSorted());
  const permissions = window.match(/const PAGE_COMMANDS: \[&str; \d+\] =\s*\[([^\]]*)\]/u);
  assert.ok(permissions, 'shell.rs names no permissions of the page');
  assert.deepEqual([...permissions[1].matchAll(/"([^"]+)"/gu)].map((match) => match[1]), allowed(four));
  const manifest = readFileSync(path.join(shell, 'build.rs'), 'utf8').match(/\.commands\(&\[([^\]]*)\]/u);
  assert.ok(manifest, 'build.rs names no commands');
  assert.deepEqual([...manifest[1].matchAll(/"([^"]+)"/gu)].map((match) => match[1]).toSorted(), every.toSorted());
  const capabilities = path.join(shell, 'capabilities');
  for (const name of readdirSync(capabilities)) {
    const capability = readFileSync(path.join(capabilities, name), 'utf8');
    assert.ok(!capability.includes('updater:'), `${name} gives a page a permission of the updater`);
    assert.ok(!/["']dialog:|["']fs:/u.test(capability), `${name} gives a page a permission of the windows or the files`);
  }
  const startPage = JSON.parse(readFileSync(path.join(capabilities, 'start.json'), 'utf8'));
  assert.deepEqual(startPage.permissions.toSorted(), allowed(start).toSorted());
  assert.equal(startPage.local, true);
  assert.ok(!permissions[1].includes('updater:'));
  assert.ok(!/dialog:|fs:/u.test(permissions[1]), 'the page of the backend gets a permission of the windows or the files');
  const cargo = readFileSync(path.join(shell, 'Cargo.toml'), 'utf8');
  assert.ok(!/tauri-plugin-(dialog|fs)/u.test(cargo), 'the shell takes a plugin whose commands give a page paths');
  const folder = readFileSync(path.join(shell, 'src', 'shell', 'folder.rs'), 'utf8');
  const command = folder.match(/async fn pick_folder\(([^)]*)\)\s*->\s*([^{]+)\{/u);
  assert.ok(command, 'folder.rs names no command pick_folder');
  assert.match(command[2], /Result<serde_json::Value, String>/u);
  assert.match(folder, /outcome\.json\(\)/u, 'pick_folder answers anything but the outcome of the choice');
  // The page of the start takes nothing from its address: its words and buttons come from the command of state.
  const script = readFileSync(path.join(repositoryRoot, 'desktop', 'app', 'start.js'), 'utf8');
  assert.ok(!/\blocation\b/u.test(script), 'start.js reads its address');
  for (const command of start) assert.ok(script.includes(`'${command}'`), `start.js does not call ${command}`);
});

// specs/2026-11-13-desk-updates.md UB0.1: the page of the start lays out what launch_state says as the boards draw it — a
// step under the sign of work (44·39); a failure with the way out in a row under its words, «Закрыть» and the actions of
// the reason at the bottom in the order of the shell, the main one last and focused, and no main button where the reason
// has no action (44·40). The screens are shaped as the shell serializes them (crates/core/src/launch.rs, words.rs).
test('the page of the start lays out a step and a failure as the boards draw them', async () => {
  const app = path.join(repositoryRoot, 'desktop', 'app');
  const dom = new JSDOM(readFileSync(path.join(app, 'index.html'), 'utf8'), { runScripts: 'outside-only', pretendToBeVisual: true });
  const { window } = dom;
  const document = window.document;
  let screen = { view: 'starting' };
  const invoked = [];
  window.__TAURI__ = { core: { invoke: async (command) => (invoked.push(command), command === 'launch_state' ? screen : null) } };
  try {
    window.eval(readFileSync(path.join(app, 'start.js'), 'utf8'));
    const show = async (next) => {
      screen = next;
      await window.refresh();
    };
    const button = (id, label, place, main = false) => ({ id, label, place, main });
    const wayOut = [button('folder', 'Открыть папку с данными', 'wayOut'), button('contact', 'Написать нам', 'wayOut')];
    const footer = (node) => [...node.children].map((child) => [child.textContent, child.className]);

    await show({ view: 'step', words: 'Сохраняю копию данных перед обновлением…' });
    const starting = document.getElementById('starting');
    assert.equal(starting.hidden, false);
    const loader = starting.querySelector('.loader');
    assert.ok(loader && loader.compareDocumentPosition(document.getElementById('title')) & window.Node.DOCUMENT_POSITION_FOLLOWING, 'no sign of work over the words of the step');
    assert.equal(document.getElementById('title').textContent, 'Сохраняю копию данных перед обновлением…');
    assert.equal(document.getElementById('hint').hidden, true);

    await show({
      view: 'failure',
      words: 'Обновление не применилось: данные целы.',
      buttons: [...wayOut, button('close', 'Закрыть', 'footer'), button('copy', 'Открыть папку с копией', 'footer', true)],
    });
    const failure = document.getElementById('failure');
    assert.equal(failure.hidden, false);
    assert.equal(starting.hidden, true);
    assert.deepEqual([...failure.children].slice(1).map((child) => child.id), ['words', 'way-out', 'actions']);
    const row = document.getElementById('way-out');
    assert.equal(row.hidden, false);
    assert.deepEqual(footer(row), [['Открыть папку с данными', 'small'], ['Написать нам', 'small']]);
    const actions = document.getElementById('actions');
    assert.deepEqual(footer(actions), [['Закрыть', 'quiet'], ['Открыть папку с копией', '']]);
    assert.equal(document.activeElement, actions.lastElementChild, 'the main action is not focused');
    actions.lastElementChild.click();
    await new Promise((resolve) => setImmediate(resolve));
    assert.equal(invoked.at(-1), 'open_copy');

    // A reason without an action of its own: «Закрыть» only, and no main button.
    await show({ view: 'failure', words: 'Помощник не запустился.', buttons: [...wayOut, button('close', 'Закрыть', 'footer')] });
    assert.deepEqual(footer(actions), [['Закрыть', 'quiet']]);
    // A failure without the way out has no row under its words.
    await show({ view: 'failure', words: 'Данные не стёрты.', buttons: [button('close', 'Закрыть', 'footer')] });
    assert.equal(row.hidden, true);
    assert.equal(row.children.length, 0);
  } finally {
    window.close();
  }
});

/**
 * The ids a table of the source names: `table` is the text inside the brackets or braces of the table, its entries are
 * string literals or names of constants of the same file.
 */
function idsOf(source, table, constant) {
  return table.split(',').map((entry) => entry.trim()).filter(Boolean).map((entry) => {
    const literal = entry.match(/^"([^"]*)"$/u);
    if (literal) return literal[1];
    const value = source.match(constant(entry));
    assert.ok(value, `${entry} is not a constant of the file`);
    return value[1];
  });
}

// specs/2026-11-13-desk-secrets.md SE1.2: the records the shell keeps are one closed set, named once on each side.
test('the shell and the backend name one set of the secrets of the app', () => {
  const rust = readFileSync(path.join(repositoryRoot, 'desktop', 'src-tauri', 'crates', 'core', 'src', 'secrets.rs'), 'utf8');
  const rustTable = rust.match(/pub const RECORDS: \[&str; \d+\] = \[([^\]]*)\];/u);
  assert.ok(rustTable, 'secrets.rs names no RECORDS');
  const shell = idsOf(rust, rustTable[1], (name) => new RegExp(`pub const ${name}: &str = "([^"]*)";`, 'u'));
  const go = readFileSync(path.join(repositoryRoot, 'backend', 'internal', 'domain', 'desktop', 'secrets.go'), 'utf8');
  const goTable = go.match(/var secretRecords = \[\]string\{([^}]*)\}/u);
  assert.ok(goTable, 'secrets.go names no secretRecords');
  const backend = idsOf(go, goTable[1], (name) => new RegExp(`\\b${name}\\s*=\\s*"([^"]*)"`, 'u'));
  assert.deepEqual(shell, backend);
  assert.ok(shell.length > 0 && shell.includes('provider:own'), shell.join(','));
  // SE1.1, SE2.5: the longest value is the one the Credential Manager keeps whatever its characters — 2560 bytes of UTF-16.
  const rustLongest = rust.match(/pub const MAX_VALUE: usize = (\d+);/u);
  assert.ok(rustLongest, 'secrets.rs names no MAX_VALUE');
  const goLongest = go.match(/\bMaxSecretBytes\s*=\s*(\d+)\b/u);
  assert.ok(goLongest, 'secrets.go names no MaxSecretBytes');
  assert.equal(Number(rustLongest[1]), Number(goLongest[1]));
  assert.equal(Number(rustLongest[1]) * 2, 2560);
});

// specs/2026-11-13-desk-updates.md UB1.4, UB1.6, UB3.3, UB3.7: the updater of the release requires the version in a
// signature, allows no downgrade and nothing unsafe, names the addresses of the core over https and its main key; the smoke
// build of an update is a feature of compilation nothing turns on by default.
test('the updater of the release is the one of the core and nothing unsafe', () => {
  const shell = path.join(repositoryRoot, 'desktop', 'src-tauri');
  const config = JSON.parse(readFileSync(path.join(shell, 'tauri.conf.json'), 'utf8'));
  const updater = config.plugins?.updater;
  assert.ok(updater, 'tauri.conf.json has no plugins.updater');
  assert.equal(updater.requireSignedVersion, true);
  assert.deepEqual(updater.windows, { installMode: 'passive' });
  const unsafe = Object.keys(updater).filter((key) => /^dangerous|allowDowngrades/u.test(key));
  assert.deepEqual(unsafe, []);
  assert.equal(config.bundle.createUpdaterArtifacts, false);
  const policy = readFileSync(path.join(shell, 'crates', 'core', 'src', 'update', 'policy.rs'), 'utf8');
  const constant = (name) => policy.match(new RegExp(`pub const ${name}: &str = "([^"]+)";`, 'u'))?.[1];
  assert.equal(updater.pubkey, constant('MAIN_KEY'));
  assert.deepEqual(updater.endpoints, [constant('SITE_MANIFEST'), constant('RELEASES_MANIFEST')]);
  for (const endpoint of updater.endpoints) assert.match(endpoint, /^https:\/\/[^/]+\/.*\/(open|desktop-app)\//u, endpoint);
  assert.notEqual(constant('MAIN_KEY'), constant('SPARE_KEY'));
  for (const manifest of [path.join(shell, 'Cargo.toml'), path.join(shell, 'crates', 'core', 'Cargo.toml')]) {
    const text = readFileSync(manifest, 'utf8');
    assert.ok(!/^default\s*=.*smoke/mu.test(text), `${manifest} turns the smoke build on by default`);
  }
  assert.ok(!JSON.stringify(config).includes('127.0.0.1'), 'the configuration of the release names the loopback');
});

/** The steps that read a secret, as [job, step]. */
function secretsOf(workflow) {
  const found = [];
  for (const [id, job] of Object.entries(workflow.jobs)) {
    if (/\bsecrets\./u.test(JSON.stringify({ ...job, steps: undefined }))) found.push([id, '(the job)']);
    for (const step of job.steps) if (/\bsecrets\./u.test(JSON.stringify(step))) found.push([id, step.name ?? step.uses]);
  }
  return found;
}

const tag = "startsWith(github.ref, 'refs/tags/v')";
const caches = /^(actions\/cache|Swatinem\/rust-cache)@/u;

// specs/2026-11-13-desk-updates.md UB3.1, UB3.6: the jobs of the workflow and their rights are one table. The build reads
// and keeps no secret; the release runs on a tag v* only: the tool built without a cache, the signature in the
// environment release with the secret in its one step, nothing installed and no cache there, the tool checked by its sum;
// the provenance with the rights of an attestation; the draft with the right to write releases and no secret.
test('the jobs of the workflow and their rights are the table of a release', () => {
  const workflow = yaml.load(readFileSync(path.join(repositoryRoot, 'desktop', 'ci', 'desktop-build.yml'), 'utf8'));
  const table = {
    postgres: { permissions: undefined, if: undefined, environment: undefined },
    app: { permissions: undefined, if: undefined, environment: undefined },
    'smoke-update': { permissions: { contents: 'read' }, if: undefined, environment: undefined },
    'smoke-update-fedora': { permissions: { contents: 'read' }, if: undefined, environment: undefined },
    'release-tool': { permissions: { contents: 'read' }, if: tag, environment: undefined },
    sign: { permissions: { contents: 'read' }, if: tag, environment: 'release' },
    attest: { permissions: { contents: 'read', attestations: 'write', 'id-token': 'write' }, if: tag, environment: undefined },
    'release-draft': { permissions: { contents: 'write' }, if: tag, environment: undefined },
  };
  assert.deepEqual(Object.keys(workflow.jobs).toSorted(), Object.keys(table).toSorted());
  for (const [id, rights] of Object.entries(table)) {
    const job = workflow.jobs[id];
    assert.deepEqual({ permissions: job.permissions, if: job.if, environment: job.environment }, rights, id);
  }
  for (const id of ['release-tool', 'sign']) {
    const uses = workflow.jobs[id].steps.map((step) => step.uses ?? '');
    assert.ok(!uses.some((use) => caches.test(use) || use.startsWith('actions/setup-node@')), `${id} takes a cache or installs packages`);
    const commands = workflow.jobs[id].steps.map((step) => step.run ?? '').join('\n');
    assert.ok(!/\bnpm\b|\bapt-get\b|\bpip\b|cargo install/u.test(commands), `${id} installs something`);
  }
  const sign = workflow.jobs.sign;
  assert.deepEqual(sign.needs, ['app', 'release-tool']);
  const signing = sign.steps.find((step) => step.name === 'Sign by the main key of the environment release');
  assert.deepEqual(Object.keys(signing.env), ['CANONKEEPER_UPDATE_KEY']);
  assert.match(signing.run, /canonkeeper-release" sign --version/u);
  // specs/2026-11-16-desk-app-mirror.md AM1.1: the manifest of the site comes from the constant of the policy, not from an
  // option of the step — the step signs with the options it always had.
  assert.deepEqual(signing.run.match(/--[a-z-]+/gu), ['--version', '--base-url', '--notes', '--key-env']);
  const checked = sign.steps.findIndex((step) => /sha256sum -c/u.test(step.run ?? ''));
  assert.ok(checked >= 0 && checked < sign.steps.indexOf(signing), 'the tool is used before its sum is checked');
  const proved = sign.steps.findIndex((step) => /canonkeeper-release" check --tag "\$GITHUB_REF_NAME"/u.test(step.run ?? ''));
  assert.ok(proved > sign.steps.indexOf(signing), 'the release is not checked after its signature');
  assert.equal(sign.steps[proved].env, undefined);
  // The jobs of a release take only the artifacts of the build and of the tool, by their names: never one of a smoke run.
  const taken = ['release-tool', 'sign', 'attest', 'release-draft'].flatMap((id) => workflow.jobs[id].steps)
    .filter((step) => step.uses?.startsWith('actions/download-artifact@')).map((step) => step.with);
  for (const artifact of taken) assert.ok(artifact.name && !artifact.pattern && !artifact.name.startsWith('smoke'), JSON.stringify(artifact));
  assert.match(workflow.jobs['release-draft'].steps.at(-1).run, /gh release create "\$GITHUB_REF_NAME" .*--draft --verify-tag/u);
  assert.ok(workflow.jobs.attest.steps.some((step) => step.uses?.startsWith('actions/attest-build-provenance@')));
});

// UB3.5: every file the bundler takes into an installer is pinned by its sum to a release that does not move, laid
// before the build and checked after it; no step takes a release that moves. UB3.3: the program of each package is
// checked for the mark of its kind before the installers leave the build.
// UB2.4, specs/2026-11-15-desk-mcp.md MC1.8: before its files are copied, and before the app is removed, the installer of
// Windows first ends the programs of the bridge of its directory — Claude Code, Claude Desktop or Codex never stop them —, then
// waits for the other programs of the directory and ends the ones still running. The directory reaches PowerShell by the
// environment, never inside its command — a name with an apostrophe, a dollar sign or a backtick stays a name — and the
// variable is gone before the installer starts the app; removed with its data, the app takes away the directory of the
// sockets of the bridge in the home the same way. Where PowerShell is, the command runs as NSIS gives it (one attempt instead
// of thirty) over a directory with such a name and ends the bridge and the program held there.
// specs/2026-11-15-desk-ci-shell.md CS5.1, CS5.2: the installer is a 32-bit program, and the PowerShell it finds by its name is
// the 32-bit one, whose Get-Process has no path of a 64-bit process. The step takes the 64-bit PowerShell where the system
// has one, finds the processes by the path CIM gives (Win32_Process.ExecutablePath, whatever the bitness of the caller) and
// ends them by their number. Where PowerShell has no CIM (Linux), a function of the same answer stands in for it, and the
// order — the bridges, the wait, the end — and a program beside the directory, under a name it begins, are checked.
test('the installer of Windows ends the bridges, then waits for the programs of its directory whatever its name', async () => {
  const conf = JSON.parse(readFileSync(path.join(repositoryRoot, 'desktop', 'src-tauri', 'tauri.conf.json'), 'utf8'));
  assert.equal(conf.bundle.windows.nsis.installerHooks, 'nsis/hooks.nsh');
  const hooks = readFileSync(path.join(repositoryRoot, 'desktop', 'src-tauri', 'nsis', 'hooks.nsh'), 'utf8');
  const macro = (name) => hooks.match(new RegExp(`!macro ${name}\\n([\\s\\S]*?)!macroend`, 'u'))?.[1] ?? '';
  for (const step of ['NSIS_HOOK_PREINSTALL', 'NSIS_HOOK_PREUNINSTALL']) {
    assert.ok(macro(step).startsWith('  !insertmacro CANONKEEPER_STOP_PROGRAMS'), `${step} does not end the programs first`);
  }
  const stop = macro('CANONKEEPER_STOP_PROGRAMS');
  const exec = stop.match(/nsExec::Exec `([^`]*)`/u);
  assert.ok(exec, 'the step runs no PowerShell');
  assert.ok(!exec[1].includes('$INSTDIR') && !hooks.match(/nsExec::Exec `[^`]*\$PROFILE/u), 'a directory stands inside a command of PowerShell');
  const at = (text) => stop.indexOf(text);
  assert.ok(at('StrCpy $0 "$INSTDIR\\"') >= 0 && at('StrCpy $0 "$INSTDIR\\"') < at('SetEnvironmentVariable(t "CANONKEEPER_INSTALL_DIR", t r0)'));
  assert.ok(at('SetEnvironmentVariable(t "CANONKEEPER_INSTALL_DIR", t r0)') < at('nsExec::Exec'));
  assert.ok(at('SetEnvironmentVariable(t "CANONKEEPER_INSTALL_DIR", p 0)') > at('nsExec::Exec'), 'the variable stays for the app');
  const native = '$WINDIR\\Sysnative\\WindowsPowerShell\\v1.0\\powershell.exe';
  assert.ok(at(`StrCpy $0 "${native}"`) >= 0 && at('StrCpy $0 "powershell.exe"') >= 0, 'the PowerShell of SysWOW64 by its name');
  assert.ok(at('SetEnvironmentVariable(t "CANONKEEPER_INSTALL_DIR", t r0)') < at('StrCpy $0 "powershell.exe"'), 'the directory is lost before the variable');
  assert.ok(at('StrCpy $0 "powershell.exe"') < at(`\${If} \${FileExists} "${native}"`) && at(`\${If} \${FileExists} "${native}"`) < at(`StrCpy $0 "${native}"`) &&
    at(`StrCpy $0 "${native}"`) < at('nsExec::Exec'), 'not the 64-bit PowerShell where the system has one');
  assert.ok(exec[1].startsWith('"$0" -NoProfile '), exec[1]);
  const command = exec[1].match(/-Command "(.*)"$/u)[1].replaceAll('$$', '$');
  assert.ok(command.startsWith('$d = $env:CANONKEEPER_INSTALL_DIR;'), command);
  assert.ok(command.includes('Get-CimInstance -ClassName Win32_Process') && command.includes('$_.ExecutablePath.StartsWith($d, [StringComparison]::OrdinalIgnoreCase)'), command);
  assert.ok(!/Get-Process|\$_\.Path\b/u.test(command), 'a path of a process by Get-Process');
  assert.ok(command.includes('Stop-Process -Id $_.ProcessId -Force') && !/\|\s*Stop-Process/u.test(command), 'a process is not ended by its number');
  assert.ok(command.indexOf("'canonkeeper-mcp.exe'") >= 0 && command.indexOf('Stop-Process') < command.indexOf('for ('), 'the bridges are not ended before the wait');
  // CS5.3: the steps of CI that install over the app and remove it name, at a failure, what runs from its directory.
  const app = yaml.load(readFileSync(path.join(repositoryRoot, 'desktop', 'ci', 'desktop-build.yml'), 'utf8')).jobs.app.steps;
  for (const name of [/^An installation over the app /u, /^Uninstall without the box /u]) {
    const step = app.find((entry) => name.test(entry.name ?? ''));
    assert.ok(step, String(name));
    assert.ok(step.run.includes('Get-CimInstance -ClassName Win32_Process | Where-Object { $_.ExecutablePath -and $_.ExecutablePath.StartsWith("$env:APP_DIR\\"'), step.name);
    const throws = step.run.split('\n').filter((line) => /\bthrow "/u.test(line) && !line.includes('removed the data'));
    assert.ok(throws.length >= 2 && throws.every((line) => line.includes('$(Get-Held)')), `${step.name}: a failure that names nothing`);
  }
  const uninstall = macro('NSIS_HOOK_PREUNINSTALL');
  assert.ok(uninstall.indexOf('${If} $DeleteAppDataCheckboxState = 1') < uninstall.indexOf('StrCpy $0 "$PROFILE\\.canonkeeper"'));
  assert.ok(uninstall.indexOf('SetEnvironmentVariable(t "CANONKEEPER_SOCKETS_DIR", p 0)') > uninstall.indexOf('Remove-Item -LiteralPath'));
  const pwsh = spawnSync('pwsh', ['-NoProfile', '-NonInteractive', '-Command', 'exit 0'], { env: { PATH: process.env.PATH, HOME: tmpdir() } });
  const python = spawnSync('python3', ['-I', '-c', 'import sys; print(sys.executable)'], { encoding: 'utf8' });
  if (pwsh.status !== 0 || python.status !== 0) return;
  const root = mkdtempSync(path.join(tmpdir(), 'canonkeeper-install-'));
  const directory = path.join(root, "O'Neil $x `y");
  const beside = path.join(root, "O'Neil $x `y-beside");
  mkdirSync(directory);
  mkdirSync(beside);
  const run = (where, name) => {
    const program = path.join(where, name);
    writeFileSync(program, readFileSync(python.stdout.trim()));
    chmodSync(program, 0o755);
    const child = spawn(program, ['-I', '-c', 'import time; time.sleep(60)'], { stdio: 'ignore' });
    return { child, ended: new Promise((resolve) => child.on('exit', () => resolve(true))) };
  };
  const bridge = run(directory, 'canonkeeper-mcp.exe');
  const held = run(directory, 'held');
  const stranger = run(beside, 'canonkeeper-mcp.exe');
  const env = { PATH: process.env.PATH, HOME: root };
  const cim = spawnSync('pwsh', ['-NoProfile', '-NonInteractive', '-Command', 'if (Get-Command Get-CimInstance -ErrorAction SilentlyContinue) { exit 0 }; exit 1'], { env });
  const standIn = cim.status === 0 ? '' : 'function Get-CimInstance { [CmdletBinding()] param([string]$ClassName) ' +
    'Get-Process | ForEach-Object { [pscustomobject]@{ ProcessId = $_.Id; ExecutablePath = $_.Path } } }; ';
  const told = 'function Stop-Process { [CmdletBinding()] param([int[]]$Id, [switch]$Force) foreach ($one in $Id) { "stop $one" }; ' +
    'Microsoft.PowerShell.Management\\Stop-Process -Id $Id -Force }; ' +
    'function Start-Sleep { [CmdletBinding()] param([int]$Seconds) "wait"; Microsoft.PowerShell.Utility\\Start-Sleep -Seconds $Seconds }; ';
  try {
    const result = spawnSync('pwsh', ['-NoProfile', '-NonInteractive', '-Command', standIn + told + command.replace('-lt 30', '-lt 1')], {
      env: { ...env, CANONKEEPER_INSTALL_DIR: `${directory}${path.sep}` }, encoding: 'utf8', timeout: 60_000,
    });
    assert.equal(result.status, 0, `${result.stdout}${result.stderr}`);
    const steps = result.stdout.trim().split('\n').map((line) => line.trim());
    assert.equal(steps[0], `stop ${bridge.child.pid}`, steps.join(' | '));
    assert.ok(steps.indexOf('wait') > 0 && steps.indexOf(`stop ${held.child.pid}`) > steps.indexOf('wait'), steps.join(' | '));
    assert.ok(!steps.includes(`stop ${stranger.child.pid}`), 'a program beside the directory was ended');
    for (const [name, one] of [['the bridge', bridge], ['the program held in the directory', held]]) {
      const gone = await Promise.race([one.ended, new Promise((resolve) => setTimeout(() => resolve(false), 5000))]);
      assert.ok(gone, `${name} still runs`);
    }
    const strangerGone = await Promise.race([stranger.ended, new Promise((resolve) => setTimeout(() => resolve(false), 1000))]);
    assert.ok(!strangerGone, 'a program beside the directory, under a name it begins, is gone');
    const sockets = path.join(root, '.canonkeeper');
    mkdirSync(sockets);
    writeFileSync(path.join(sockets, '0a1b2c3d.sock'), '');
    const remove = uninstall.match(/nsExec::Exec `([^`]*)`/u)[1].match(/-Command "(.*)"$/u)[1].replaceAll('$$', '$');
    const removed = spawnSync('pwsh', ['-NoProfile', '-NonInteractive', '-Command', remove], {
      env: { ...env, CANONKEEPER_SOCKETS_DIR: sockets }, encoding: 'utf8', timeout: 60_000,
    });
    assert.equal(removed.status, 0, `${removed.stdout}${removed.stderr}`);
    assert.ok(!existsSync(sockets), 'the directory of the sockets stayed');
  } finally {
    for (const one of [bridge, held, stranger]) one.child.kill('SIGKILL');
    rmSync(root, { force: true, recursive: true });
  }
});

// specs/2026-11-16-desk-app-links.md AL2.1: the installer of Windows registers the scheme canonkeeper:// of the current user by
// the native writes of NSIS — no command runs, and the directory stands in the value as it is —, the command is exactly the
// program of the installation with the link as its one argument, and the uninstaller takes the key away with or without the
// data, only while its command is this installation's.
test('the installer of Windows registers the scheme canonkeeper and its uninstaller takes away only its own', () => {
  const conf = JSON.parse(readFileSync(path.join(repositoryRoot, 'desktop', 'src-tauri', 'tauri.conf.json'), 'utf8'));
  assert.equal(conf.mainBinaryName, 'canonkeeper', 'the command of the scheme names the program canonkeeper.exe');
  const hooks = readFileSync(path.join(repositoryRoot, 'desktop', 'src-tauri', 'nsis', 'hooks.nsh'), 'utf8');
  const macro = (name) => hooks.match(new RegExp(`!macro ${name}\\n([\\s\\S]*?)!macroend`, 'u'))?.[1] ?? '';
  const install = macro('NSIS_HOOK_POSTINSTALL');
  const uninstall = macro('NSIS_HOOK_POSTUNINSTALL');
  const command = `'"$INSTDIR\\canonkeeper.exe" "%1"'`;
  for (const [name, step] of [['NSIS_HOOK_POSTINSTALL', install], ['NSIS_HOOK_POSTUNINSTALL', uninstall]]) {
    assert.ok(step.trim(), `${name} is missing`);
    assert.ok(!/nsExec|powershell|ExecWait|ExecShell|\bExec\b/iu.test(step), `${name} runs a command`);
  }
  const key = 'HKCU "Software\\Classes\\canonkeeper';
  assert.deepEqual(install.trim().split('\n').map((line) => line.trim()), [
    `WriteRegStr ${key}" "" "URL:CanonKeeper"`,
    `WriteRegStr ${key}" "URL Protocol" ""`,
    `WriteRegStr ${key}\\DefaultIcon" "" "$INSTDIR\\canonkeeper.exe,0"`,
    `WriteRegStr ${key}\\shell\\open\\command" "" ${command}`,
  ]);
  const at = (text) => uninstall.indexOf(text);
  const read = at(`ReadRegStr $0 ${key}\\shell\\open\\command" ""`);
  const own = at(`\${If} $0 == ${command}`);
  const remove = at(`DeleteRegKey ${key}"`);
  assert.ok(read >= 0 && read < own && own < remove && remove < at('${EndIf}'), 'the key is taken away without the check of its command');
  assert.ok(at('Push $0') < read && at('Pop $0') > at('${EndIf}'), 'the step does not give $0 back to the caller');
  assert.ok(!uninstall.includes('DeleteAppDataCheckboxState'), 'the key goes only with the data');
  assert.ok(!macro('NSIS_HOOK_PREUNINSTALL').includes('DeleteRegKey'), 'the key goes before the files of the app');
  assert.equal(hooks.match(/DeleteRegKey/gu)?.length, 1);
});

// AL2.2: the .deb and the .rpm carry one template of the .desktop — the fields of the template of the bundler of Tauri 2 and
// two changes: the program takes a link (`Exec=… %u`) and opens the scheme (`MimeType=x-scheme-handler/canonkeeper;`). The
// template rendered as the bundler renders it passes desktop-file-validate where the machine has it.
test('the packages of Linux carry the .desktop that opens the links canonkeeper', () => {
  const conf = JSON.parse(readFileSync(path.join(repositoryRoot, 'desktop', 'src-tauri', 'tauri.conf.json'), 'utf8'));
  assert.equal(conf.bundle.linux.deb.desktopTemplate, 'linux/canonkeeper.desktop');
  assert.equal(conf.bundle.linux.rpm.desktopTemplate, 'linux/canonkeeper.desktop');
  assert.equal(conf.bundle.linux.appimage.desktopTemplate, undefined, 'the AppImage has no template of the .desktop in Tauri 2');
  const template = readFileSync(path.join(repositoryRoot, 'desktop', 'src-tauri', 'linux', 'canonkeeper.desktop'), 'utf8');
  // The variables of the template of the bundler (tauri-utils DebConfig::desktop_template): categories, comment (optional),
  // exec, icon, name.
  const render = (values) => template
    .replace(/\{\{#if comment\}\}\n([\s\S]*?)\{\{\/if\}\}\n/u, (_block, inner) => (values.comment ? inner : ''))
    .replace(/\{\{(\w+)\}\}/gu, (_whole, name) => {
      assert.ok(name in values, `the template names ${name}, which the bundler does not give`);
      return values[name];
    });
  const values = { categories: 'Office;', comment: 'Книги саги и помощник писателя на вашем компьютере', exec: 'canonkeeper', icon: 'canonkeeper', name: 'CanonKeeper' };
  const rendered = render(values);
  const entry = rendered.split('\n').filter((line) => line && !line.startsWith('#'));
  assert.deepEqual(entry, [
    '[Desktop Entry]',
    'Categories=Office;',
    `Comment=${values.comment}`,
    'Exec=canonkeeper %u',
    'StartupWMClass=canonkeeper',
    'Icon=canonkeeper',
    'MimeType=x-scheme-handler/canonkeeper;',
    'Name=CanonKeeper',
    'Terminal=false',
    'Type=Application',
  ]);
  assert.ok(!render({ ...values, comment: '' }).includes('Comment='));
  const validate = spawnSync('desktop-file-validate', ['--help'], { encoding: 'utf8' });
  if (validate.status !== 0) return;
  const directory = mkdtempSync(path.join(tmpdir(), 'canonkeeper-desktop-entry-'));
  try {
    const file = path.join(directory, 'CanonKeeper.desktop');
    writeFileSync(file, rendered);
    const result = spawnSync('desktop-file-validate', [file], { encoding: 'utf8' });
    assert.equal(result.status, 0, `${result.stdout}${result.stderr}`);
    assert.equal(`${result.stdout}${result.stderr}`.trim(), '', 'desktop-file-validate has words for the .desktop');
  } finally {
    rmSync(directory, { force: true, recursive: true });
  }
});

// AL2.3: CI checks what the machine cannot — the .desktop of the built packages, the key of the registry after «Install», after
// «An installation over the app» and none after «Uninstall without the box» — and the smoke run gives the running app a link by
// a second start (through the scheme on Windows, on the bus of the session on Linux) after the check that the bridge is no
// second start, and waits for the shell to take it to the window.
test('CI checks the scheme the packages register and gives the running app a link', () => {
  const steps = yaml.load(readFileSync(path.join(repositoryRoot, 'desktop', 'ci', 'desktop-build.yml'), 'utf8')).jobs.app.steps;
  const at = (pattern) => steps.findIndex((step) => pattern.test(step.name ?? ''));
  const entry = steps[at(/^The \.desktop of the \.deb and the \.rpm opens the links canonkeeper/u)];
  assert.ok(entry && entry.if === "runner.os == 'Linux'", 'no step checks the .desktop of the packages');
  assert.ok(at(/^The program of each package carries the mark/u) < steps.indexOf(entry), 'the .desktop is checked before the packages are unpacked');
  for (const text of ['for kind in deb rpm', 'desktop-file-validate "$entry"', "'^Exec=[^ ]+ %u$'", "'MimeType=x-scheme-handler/canonkeeper;'", 'cat "$entry"']) {
    assert.ok(entry.run.includes(text), text);
  }
  assert.match(steps.find((step) => /^Linux packages/u.test(step.name ?? '')).run, /\bdesktop-file-utils\b/u);
  const registered = '$command -ne "`"$dir\\canonkeeper.exe`" `"%1`""';
  assert.ok(steps[at(/^Install \(Windows/u)].run.includes(registered), 'no check of the scheme after Install');
  assert.ok(steps[at(/^An installation over the app /u)].run.includes('$command -ne "`"$env:APP_DIR\\canonkeeper.exe`" `"%1`""'), 'no check after an installation over the app');
  assert.ok(steps[at(/^Uninstall without the box /u)].run.includes("if (Test-Path -LiteralPath 'HKCU:\\Software\\Classes\\canonkeeper')"), 'no check after the uninstaller');
  const smoke = readFileSync(path.join(repositoryRoot, 'desktop', 'ci', 'smoke.mjs'), 'utf8');
  const bridge = smoke.indexOf('the bridge of the AppImage started the app a second time');
  const given = smoke.indexOf("`Start-Process '${link}'`");
  assert.ok(bridge > 0 && given > bridge, 'the link is given before the check that the bridge is no second start');
  assert.ok(smoke.includes('spawnSync(values.exe, [link]') && smoke.includes('link_opened') && smoke.includes('navigate_failed'));
  assert.ok(given < smoke.indexOf("spawnSync('taskkill'"), 'the link is given after the app stopped');
});

test('the files of the bundler are pinned and the packages carry the marks of their kinds', () => {
  const text = readFileSync(path.join(repositoryRoot, 'desktop', 'ci', 'desktop-build.yml'), 'utf8');
  assert.ok(!/continuous|releases\/latest|@main\b|@master\b/u.test(text.replaceAll('# master, toolchain pinned below', '')), 'a release that moves');
  const release = JSON.parse(readFileSync(path.join(repositoryRoot, 'desktop', 'release.json'), 'utf8'));
  for (const os of ['linux', 'windows']) assert.ok(pinsOf(release, os).length > 0, os);
  assert.throws(() => pinsOf({ bundler: { linux: [{ repository: 'a/b', tag: 'continuous', asset: 'x', place: 'x', sha256: '0'.repeat(64) }] } }, 'linux'), /moves/u);
  assert.throws(() => pinsOf({ bundler: { linux: [{ repository: 'a/b', tag: 't', asset: 'x', place: '../x', sha256: '0'.repeat(64) }] } }, 'linux'), /place/u);
  // A file of an address of its own: only https, and only an address of one file — a link that resolves to the file of
  // the day moves.
  const ofAddress = (url) => ({ bundler: { windows: [{ url, asset: 'x.exe', place: 'x.exe', sha256: '0'.repeat(64) }] } });
  assert.equal(pinsOf(ofAddress('https://files.example.test/files/0a1b/x.exe'), 'windows').length, 1);
  assert.throws(() => pinsOf(ofAddress('http://files.example.test/files/0a1b/x.exe'), 'windows'), /https/u);
  assert.throws(() => pinsOf(ofAddress('https://go.example.test/fwlink/p/?LinkId=1'), 'windows'), /moves/u);
  // The installer of Windows embeds the bootstrapper of WebView2 (docs/diagnostics/desktop-2026-11-12/07 4.2): the
  // bundler takes it from its cache when it is there (MicrosoftEdgeWebview2Setup.exe), so it is pinned, laid before
  // the build and found the same after it; no configuration of a build changes the mode.
  const conf = JSON.parse(readFileSync(path.join(repositoryRoot, 'desktop', 'src-tauri', 'tauri.conf.json'), 'utf8'));
  assert.deepEqual(conf.bundle.windows.webviewInstallMode, { type: 'embedBootstrapper' });
  const bootstrapper = pinsOf(release, 'windows').find((pin) => pin.place === 'MicrosoftEdgeWebview2Setup.exe');
  assert.ok(bootstrapper && !bootstrapper.patched && !bootstrapper.unpack, 'the bootstrapper of WebView2 is not pinned');
  const overrides = [path.join('desktop', 'src-tauri', 'tauri.bundle.json'), path.join('desktop', 'ci', 'smoke-update.mjs')];
  for (const file of overrides) assert.doesNotMatch(readFileSync(path.join(repositoryRoot, file), 'utf8'), /webviewInstallMode|webview_install_mode/u, file);
  const app = yaml.load(text).jobs.app.steps;
  const at = (pattern) => app.findIndex((step) => pattern.test(step.run ?? ''));
  const built = Math.max(at(/--bundles \$\{\{ matrix\.bundles \}\}/u), at(/--bundles appimage/u));
  assert.ok(at(/bundler-tools\.mjs fetch --os linux/u) < at(/npx tauri build/u) && at(/bundler-tools\.mjs fetch --os windows/u) < at(/npx tauri build/u));
  assert.ok(at(/bundler-tools\.mjs verify/u) > built && at(/marks --kind rpm /u) > built);
  const upload = app.findIndex((step) => step.with?.name === 'installers-${{ matrix.os }}');
  assert.ok(at(/marks --kind/u) < upload && at(/bundler-tools\.mjs verify/u) < upload);
  for (const kind of ['deb', 'rpm', 'appimage', 'nsis']) assert.ok(new RegExp(`marks --kind ${kind} `, 'u').test(text), kind);
  // A file the bundler took anew is found.
  const cache = mkdtempSync(path.join(tmpdir(), 'bundler-tools-'));
  try {
    const pins = [
      { place: 'kept', sha256: sha256Of('kept\n') },
      { place: 'patched', sha256: sha256Of('before\n'), patched: true },
    ];
    writeFileSync(path.join(cache, 'kept'), 'kept\n');
    writeFileSync(path.join(cache, 'patched'), 'after\n');
    assert.deepEqual(changed(pins, cache), []);
    writeFileSync(path.join(cache, 'kept'), 'taken anew\n');
    assert.deepEqual(changed(pins, cache), ['kept']);
  } finally {
    rmSync(cache, { force: true, recursive: true });
  }
});

function sha256Of(text) {
  const file = path.join(mkdtempSync(path.join(tmpdir(), 'sum-')), 'f');
  writeFileSync(file, text);
  const sum = sha256(file);
  rmSync(path.dirname(file), { force: true, recursive: true });
  return sum;
}

// UB3.7: the smoke run of an update is its own job with its own target directory and artifacts named smoke-*; the feature
// smoke, the tag smokeupdate and the configuration of the smoke build appear in its commands alone; N+1 is a later version
// with a later migration, signed and checked by the one signer of a release.
test('the smoke build of an update lives in its own job alone', () => {
  const workflow = yaml.load(readFileSync(path.join(repositoryRoot, 'desktop', 'ci', 'desktop-build.yml'), 'utf8'));
  const smoky = /--features smoke|--smoke\b|smoke\.conf\.json|CANONKEEPER_SMOKE_UPDATE/u;
  for (const [id, job] of Object.entries(workflow.jobs)) {
    const commands = job.steps.map((step) => `${step.run ?? ''} ${JSON.stringify(step.env ?? {})}`).join('\n');
    assert.equal(smoky.test(commands), id === 'smoke-update', id);
  }
  const smoke = workflow.jobs['smoke-update'].steps;
  const commands = smoke.map((step) => step.run ?? '').join('\n');
  assert.match(commands, /CARGO_TARGET_DIR=\$target/u);
  assert.match(commands, /"\$tool" sign --version "\$SMOKE_TO"/u);
  assert.match(commands, /"\$tool" check --tag "v\$SMOKE_TO"/u);
  for (const kind of ['appimage deb', '--kind nsis']) assert.ok(commands.includes(kind), kind);
  assert.ok(workflow.jobs['smoke-update-fedora'].steps.some((step) => /--kind rpm/u.test(step.run ?? '')));
  for (const step of [...smoke, ...workflow.jobs['smoke-update-fedora'].steps].filter((entry) => entry.uses?.startsWith('actions/upload-artifact@'))) {
    assert.match(step.with.name, /^smoke-/u);
  }
  // The pieces of the run.
  assert.equal(nextVersion('1.2.9'), '1.2.10');
  assert.equal(laterMigration(['0001_baseline.sql', '0147_model_requests_gateway.sql', 'embed.go']), '0148_smoke_update_later.sql');
  const manifest = readFileSync(path.join(repositoryRoot, 'desktop', 'src-tauri', 'Cargo.toml'), 'utf8');
  const lock = readFileSync(path.join(repositoryRoot, 'desktop', 'src-tauri', 'Cargo.lock'), 'utf8');
  const bumped = withVersion(manifest, lock, '9.9.9');
  assert.equal(versionOf(bumped.manifest), '9.9.9');
  assert.match(bumped.lock, /name = "canonkeeper-desktop"\nversion = "9\.9\.9"/u);
  assert.match(bumped.lock, /name = "canonkeeper-desktop-core"\nversion = "0\.1\.0"/u);
  assert.deepEqual(smokeConfig('47613', 'key').plugins.updater.endpoints, ['http://127.0.0.1:47613/latest.json']);
});

// specs/2026-11-15-desk-ci-shell.md CS6: a package of Linux in the smoke run is installed by the stand-in pkexec of the
// runner, so no package installed after it may bring the real one — the job installs every package of the system before
// the stand-in, and in Fedora polkit (which the package of N needs and which carries pkexec) is among the tools of the
// container. A refusal of the shell ends the run at once with its line, not after fifteen minutes.
test('the stand-in pkexec stays, and the smoke run of an update ends at a refusal of the shell', async () => {
  const workflow = yaml.load(readFileSync(path.join(repositoryRoot, 'desktop', 'ci', 'desktop-build.yml'), 'utf8'));
  for (const [id, system] of [['smoke-update', /apt-get install/u], ['smoke-update-fedora', /dnf install/u]]) {
    const steps = workflow.jobs[id].steps;
    const standIn = steps.findIndex((step) => /install -m 0755 desktop\/ci\/pkexec\.sh \/usr\/bin\/pkexec/u.test(step.run ?? ''));
    assert.ok(standIn > 0, id);
    assert.ok(steps.every((step, at) => !system.test(step.run ?? '') || at < standIn), `${id}: a package of the system after the stand-in pkexec`);
  }
  const tools = workflow.jobs['smoke-update-fedora'].steps.find((step) => /dnf install/u.test(step.run ?? ''));
  assert.match(tools.run, /^dnf install [^\n]*\bpolkit\b/mu, 'the package of N brings the real pkexec over the stand-in');

  const data = mkdtempSync(path.join(tmpdir(), 'smoke-update-'));
  try {
    mkdirSync(path.join(data, 'logs'));
    writeFileSync(path.join(data, 'settings.json'), JSON.stringify({ version: '0.1.0' }));
    const shell = path.join(data, 'logs', 'shell-2026-11-15.log');
    // specs/2026-11-17-desk-update-linux.md UL5.1: another installation of programs and an installation that did not finish end it
    // too.
    for (const said of ['update_install_refused reason=declined', 'update_install_refused reason=busy', 'update_install_unfinished', 'update_not_installed cause=held count=1']) {
      writeFileSync(shell, `2026-11-15T17:41:25Z update_installing kind=rpm\n2026-11-15T17:41:26Z ${said}\n2026-11-15T17:41:26Z backend_spawned\n`);
      const started = Date.now();
      await assert.rejects(waitForTheUpdate(data, '0.1.1', 900_000), (error) => error.message.endsWith(`2026-11-15T17:41:26Z ${said}`));
      assert.ok(Date.now() - started < 5000, `the run waited after ${said}`);
    }
    writeFileSync(path.join(data, 'settings.json'), JSON.stringify({ version: '0.1.1' }));
    writeFileSync(shell, 'a update_installing kind=rpm\na page_loaded\nb page_loaded\n');
    await waitForTheUpdate(data, '0.1.1', 5000);
  } finally {
    rmSync(data, { force: true, recursive: true });
  }
});

// UB3.4: the draft of a release carries its notes — plain text, as latest.json carries them signed — and the section
// «Вернуться на прежнюю версию» of desktop/README.md with the commands for each system; the README names the steps of the
// owner before the first release.
test('the draft of a release says how to go back and the README the steps of the owner', () => {
  const notes = readFileSync(path.join(repositoryRoot, 'desktop', 'release-notes.txt'), 'utf8');
  const control = [...notes].some((letter) => letter.charCodeAt(0) < 32 ? !'\n\t'.includes(letter) : letter.charCodeAt(0) === 127);
  assert.ok(Buffer.byteLength(notes) <= 8 * 1024 && !control, 'the notes are not plain text');
  const readme = readFileSync(path.join(repositoryRoot, 'desktop', 'README.md'), 'utf8');
  const body = releaseBody(notes, readme);
  assert.ok(body.startsWith(notes.trim()));
  assert.ok(body.includes(BACK));
  for (const command of ['sudo apt install --allow-downgrades', 'sudo dnf downgrade', 'AppImage', 'Удалить данные приложения']) {
    assert.ok(body.includes(command), command);
  }
  assert.ok(!/^## (?!Вернуться)/mu.test(body.slice(body.indexOf(BACK) + BACK.length)), 'the body runs into the next section');
  for (const step of ['CANONKEEPER_UPDATE_KEY', 'canonkeeper-release keygen', 'release', 'v*', 'запасн']) assert.ok(readme.includes(step), step);
  assert.throws(() => releaseBody('x', '# README\n'), /Вернуться/u);
});

// specs/2026-11-16-desk-app-mirror.md AM1.3: the README names the 14 files of a release — the installers and their
// signatures, both manifests and the sums, each with its signature, by the names the signer writes — and that the site
// serves the manifest of the site as its /app/open/latest.json.
test('the README names the 14 files of a release and what the mirror of the site serves', () => {
  const release = readFileSync(path.join(repositoryRoot, 'desktop', 'src-tauri', 'crates', 'core', 'src', 'update', 'release.rs'), 'utf8');
  const constant = (name) => release.match(new RegExp(`pub const ${name}: &str = "([^"]+)";`, 'u'))?.[1];
  const installers = ['CanonKeeper_<версия>_x64-setup.exe', 'CanonKeeper_<версия>_amd64.deb', 'CanonKeeper-<версия>-1.x86_64.rpm', 'CanonKeeper_<версия>_amd64.AppImage'];
  const written = [...installers, constant('MANIFEST'), constant('SITE_LATEST'), constant('SUMS')];
  assert.ok(written.every(Boolean), 'release.rs names no manifest, manifest of the site or sums');
  const expected = [...written, ...written.map((name) => `${name}.sig`)].toSorted();
  const readme = readFileSync(path.join(repositoryRoot, 'desktop', 'README.md'), 'utf8');
  const section = readme.split('Файлы выпуска — 14:')[1]?.split('\n\n')[1] ?? '';
  const named = [...new Set([...section.matchAll(/`([^`\s]+)`/gu)].map((match) => match[1]).filter((name) => !name.startsWith('https://')))].toSorted();
  assert.deepEqual(named, expected);
  assert.match(section.replace(/\s+/gu, ' '), /отдаёт этот файл как `https:\/\/canonkeeper\.ru\/app\/open\/latest\.json`/u);
});

// specs/2026-11-15-desk-updates-web.md UW2.3: «Открыть страницу выпусков» of the pages of the app and the page of the start
// open one address — the permanent page of the releases of the public repository.
test('the pages of the app and the page of the start name one page of the releases', () => {
  const shell = readFileSync(path.join(repositoryRoot, 'desktop', 'src-tauri', 'crates', 'core', 'src', 'words.rs'), 'utf8');
  const page = readFileSync(path.join(repositoryRoot, 'src', 'editions', 'desktop', 'updates', 'update-words.ts'), 'utf8');
  const ofShell = shell.match(/pub const RELEASES_URL: &str = "([^"]+)";/u);
  const ofPage = page.match(/export const releasesUrl = '([^']+)';/u);
  assert.ok(ofShell, 'words.rs names no RELEASES_URL');
  assert.ok(ofPage, 'update-words.ts names no releasesUrl');
  assert.equal(ofPage[1], ofShell[1]);
  assert.match(ofPage[1], /^https:\/\/github\.com\/[^/]+\/[^/]+\/releases$/u);
});

/**
 * A PE file as a linker writes one: the headers, one section with the import table, the table of delayed imports and
 * the names of the DLLs. `plus` — PE32+ (x64) or PE32; `byAddress` — the delayed imports of a PE32 named by an address,
 * as an old linker names them.
 */
function peFile({ imports = [], delayed = [], plus = true, byAddress = false }) {
  const imageBase = plus ? 0x140000000n : 0x400000n;
  const optionalSize = plus ? 240 : 224;
  const sectionTable = 0x58 + optionalSize;
  const raw = 0x200;
  const address = 0x1000;
  const importAt = 0;
  const delayAt = (imports.length + 1) * 20;
  let namesAt = delayAt + (delayed.length + 1) * 32;
  const section = Buffer.alloc(namesAt + [...imports, ...delayed].reduce((sum, name) => sum + name.length + 1, 0) + 16);
  const nameRva = (name) => {
    const rva = address + namesAt;
    section.write(name, namesAt, 'latin1');
    namesAt += name.length + 1;
    return rva;
  };
  imports.forEach((name, index) => {
    const at = importAt + index * 20;
    section.writeUInt32LE(address + section.length - 8, at);
    section.writeUInt32LE(nameRva(name), at + 12);
    section.writeUInt32LE(address + section.length - 8, at + 16);
  });
  delayed.forEach((name, index) => {
    const at = delayAt + index * 32;
    const rva = nameRva(name);
    section.writeUInt32LE(byAddress ? 0 : 1, at);
    section.writeUInt32LE(byAddress ? Number(imageBase) + rva : rva, at + 4);
  });
  const file = Buffer.alloc(raw + section.length);
  file.write('MZ', 0, 'latin1');
  file.writeUInt32LE(0x40, 0x3c);
  file.write('PE\0\0', 0x40, 'latin1');
  file.writeUInt16LE(plus ? 0x8664 : 0x14c, 0x44);
  file.writeUInt16LE(1, 0x46);
  file.writeUInt16LE(optionalSize, 0x54);
  const optional = 0x58;
  file.writeUInt16LE(plus ? 0x20b : 0x10b, optional);
  if (plus) file.writeBigUInt64LE(imageBase, optional + 24);
  else file.writeUInt32LE(Number(imageBase), optional + 28);
  const directories = optional + (plus ? 112 : 96);
  file.writeUInt32LE(16, directories - 4);
  if (imports.length > 0) file.writeUInt32LE(address + importAt, directories + 8);
  if (delayed.length > 0) file.writeUInt32LE(address + delayAt, directories + 13 * 8);
  file.write('.idata', sectionTable, 'latin1');
  file.writeUInt32LE(section.length, sectionTable + 8);
  file.writeUInt32LE(address, sectionTable + 12);
  file.writeUInt32LE(section.length, sectionTable + 16);
  file.writeUInt32LE(raw, sectionTable + 20);
  section.copy(file, raw);
  return file;
}

// specs/2026-11-18-desk-windows-chat.md WC2.2: the reader of the imports of a PE file — the import table and the table of
// delayed imports, of x64 and of x86, delayed imports named by an address too — and no PE in a file of another kind.
test('the imports of a PE file are its import table and its delayed imports', () => {
  const imports = ['libpq.dll', 'KERNEL32.dll', 'VCRUNTIME140.dll', 'api-ms-win-crt-runtime-l1-1-0.dll'];
  const delayed = ['SHLWAPI.dll', 'VERSION.dll'];
  for (const plus of [true, false]) assert.deepEqual(peImports(peFile({ imports, delayed, plus })), { imports, delayed });
  assert.deepEqual(peImports(peFile({ imports, delayed, plus: false, byAddress: true })), { imports, delayed });
  assert.deepEqual(peImports(peFile({ imports: ['kernel32.dll'] })), { imports: ['kernel32.dll'], delayed: [] });
  assert.deepEqual(peImports(peFile({})), { imports: [], delayed: [] });
  assert.equal(peImports(Buffer.from('max_connections = 20\n'.repeat(8))), null);
  const notPe = peFile({ imports });
  notPe.write('NE', 0x40, 'latin1');
  assert.equal(peImports(notPe), null);
  const cut = peFile({ imports, delayed });
  assert.throws(() => peImports(cut.subarray(0, 0x210)), /points outside itself|cut short/);
});

// WC2.2: every PE file of the installer imports only a file of the package or a DLL of a clean Windows 10 and 11 — the
// UCRT among them; VCRUNTIME140.dll of Visual C++ Redistributable is a refusal while the package does not carry it.
test('a PE file of the installer imports only the package and Windows, never Visual C++ Redistributable', () => {
  const root = mkdtempSync(path.join(tmpdir(), 'canonkeeper-imports-'));
  try {
    const crt = [...UCRT_DLLS].slice(0, 3);
    writeFile(path.join(root, 'postgres', 'bin', 'initdb.exe'), peFile({ imports: ['LIBPQ.dll', 'WS2_32.dll', 'KERNEL32.dll', 'VCRUNTIME140.dll', ...crt] }));
    writeFile(path.join(root, 'postgres', 'bin', 'libpq.dll'), peFile({ imports: ['Secur32.dll', 'KERNEL32.dll', ...crt] }));
    writeFile(path.join(root, 'postgres', 'bin', 'postgres.exe'), peFile({ imports: ['KERNEL32.dll', ...crt], delayed: ['vcruntime140_1.dll'] }));
    writeFile(path.join(root, 'postgres', 'lib', 'postgresql', 'plpgsql.dll'), peFile({ imports: ['postgres.exe', 'KERNEL32.dll'] }));
    writeFile(path.join(root, 'canonkeeper-backend.exe'), peFile({ imports: ['kernel32.dll'] }));
    writeFile(path.join(root, 'codex', 'codex-app-server.exe'), peFile({ imports: ['combase.dll', 'api-ms-win-core-synch-l1-2-0.dll'], delayed: ['tdh.dll'] }));
    writeFile(path.join(root, 'postgres', 'share', 'postgres.bki'), 'MZ is not a program\n');

    const before = importProblems([root]);
    assert.equal(before.checked, 6);
    assert.deepEqual(before.problems, [
      `${path.join('postgres', 'bin', 'initdb.exe')}: imports VCRUNTIME140.dll — a DLL of Visual C++ Redistributable, which a clean Windows has not`,
      `${path.join('postgres', 'bin', 'postgres.exe')}: imports late vcruntime140_1.dll — a DLL of Visual C++ Redistributable, which a clean Windows has not`,
    ]);

    writeFile(path.join(root, 'postgres', 'bin', 'VCRUNTIME140.dll'), peFile({ imports: ['KERNEL32.dll'] }));
    writeFile(path.join(root, 'postgres', 'bin', 'vcruntime140_1.dll'), peFile({ imports: ['KERNEL32.dll'] }));
    assert.deepEqual(importProblems([root]).problems, [], 'a DLL the package carries is not missing');

    writeFile(path.join(root, 'canonkeeper.exe'), peFile({ imports: ['WebView2Loader.dll', 'user32.dll'] }));
    const setup = path.join(root, 'CanonKeeper_0.1.4_x64-setup.exe');
    writeFile(setup, peFile({ imports: ['MSVCP140.dll'] }));
    assert.deepEqual(importProblems([path.join(root, 'canonkeeper.exe'), setup]).problems, [
      'canonkeeper.exe: imports WebView2Loader.dll, which is neither in the package nor a DLL of a clean Windows',
      'CanonKeeper_0.1.4_x64-setup.exe: imports MSVCP140.dll — a DLL of Visual C++ Redistributable, which a clean Windows has not',
    ]);
  } finally {
    rmSync(root, { force: true, recursive: true });
  }
  for (const name of ['KERNEL32.dll', 'api-ms-win-crt-stdio-l1-1-0.dll', 'combase.dll']) assert.ok(windowsDll(name), name);
  for (const name of ['VCRUNTIME140.dll', 'vcruntime140_1.dll', 'MSVCP140.dll', 'CONCRT140.dll', 'VCOMP140.DLL', 'api-ms-win-crt-other-l1-1-0.dll']) {
    assert.ok(!windowsDll(name), name);
  }
});

// specs/2026-11-18-desk-windows-chat.md WC2.1: the programs of the PostgreSQL set of Windows carry the C runtime of Visual
// C++ in themselves and take the Universal C Runtime of Windows, so none imports VCRUNTIME140.dll; the set of Linux is built
// with the options it was built with, to the option. The cache of the set is keyed by desktop/postgres/**: a new option
// builds the set anew.
test('the PostgreSQL set of Windows needs no Visual C++ Redistributable, the set of Linux is built as it was', () => {
  const build = path.join(repositoryRoot, 'desktop', 'postgres', 'build.py');
  const read = [
    'import importlib.util, json, sys',
    'spec = importlib.util.spec_from_file_location("build", sys.argv[1])',
    'build = importlib.util.module_from_spec(spec)',
    'spec.loader.exec_module(build)',
    'print(json.dumps({"linux": build.meson_options(False), "windows": build.meson_options(True)}))',
  ].join('\n');
  const result = spawnSync('python3', ['-I', '-B', '-c', read, build], { encoding: 'utf8' });
  assert.equal(result.status, 0, result.stderr);
  const options = JSON.parse(result.stdout);
  assert.deepEqual(options.linux, [
    '-Dicu=disabled', '-Dssl=none', '-Dllvm=disabled', '-Dzlib=disabled', '-Dlz4=disabled', '-Dzstd=disabled',
    '-Dreadline=disabled', '-Dnls=disabled', '-Duuid=none', '-Dlibxml=disabled', '-Dlibxslt=disabled',
    '-Dgssapi=disabled', '-Dldap=disabled', '-Dpam=disabled', '-Dbsd_auth=disabled', '-Dsystemd=disabled',
    '-Dbonjour=disabled', '-Dselinux=disabled', '-Dlibcurl=disabled', '-Dliburing=disabled', '-Dlibnuma=disabled',
    '-Dplperl=disabled', '-Dplpython=disabled', '-Dpltcl=disabled', '-Dtap_tests=disabled', '-Ddocs=disabled',
    '-Ddocs_pdf=disabled',
  ]);
  assert.deepEqual(options.windows, [...options.linux, '-Db_vscrt=mt', '-Dc_link_args=/NODEFAULTLIB:libucrt.lib ucrt.lib']);
  assert.match(readFileSync(build, 'utf8'), /options = meson_options\(package\.is_windows\(\)\)\n.*"--buildtype=release", \*options\]/u);
  const workflow = yaml.load(readFileSync(path.join(repositoryRoot, 'desktop', 'ci', 'desktop-build.yml'), 'utf8'));
  const cache = workflow.jobs.postgres.steps.find((step) => step.id === 'cache');
  assert.equal(cache.with.key, "pg-set-${{ matrix.os }}-${{ hashFiles('desktop/postgres/**', 'desktop/release.json') }}");
});

// specs/2026-11-18-desk-windows-chat.md WC1.4, WC2.2: on a real Windows, with the codex-app-server of the set — the package
// the release pins, checked by its sum —, the runtime tests of the Codex adapter start a thread of the chat under the
// profile of the repository and get the answer of the stub model; none of them may be skipped. After the installer is
// built, every PE file of it, unpacked, and the installer itself import only the package and Windows.
test('CI asks the chat a question on a real Windows and checks what the programs of the installer import', () => {
  const app = yaml.load(readFileSync(path.join(repositoryRoot, 'desktop', 'ci', 'desktop-build.yml'), 'utf8')).jobs.app.steps;
  const at = (pattern) => app.findIndex((step) => pattern.test(step.run ?? ''));
  const set = at(/node scripts\/desktop-app\.mjs --target/u);
  const chat = at(/go test \.\/internal\/adapters\/codex [\s\S]*-run '\^\(/u);
  assert.ok(chat > set && set >= 0, 'the runtime tests run after the set is laid out');
  assert.equal(app[chat].if, "runner.os == 'Windows'");
  assert.equal(app[chat]['working-directory'], 'backend');
  assert.equal(app[chat].env.CK_CODEX_RUNTIME_TEST_BINARY, '${{ github.workspace }}/desktop/bundle/codex/codex-app-server.exe');
  const named = /-run '\^\(([^)]+)\)\$'/u.exec(app[chat].run)?.[1].split('|') ?? [];
  for (const test of [
    'TestTheAppServerOfWindowsAloneReadsTheWholeDisk', 'TestRuntimeProfileInitializesWithPinnedSDK',
    'TestRuntimeRunsResumesAndDeletesChatAgainstStubModel', 'TestRuntimeTheProfileOfWindowsReadsTheWholeDiskAndNothingMore',
  ]) {
    assert.ok(named.includes(test), test);
  }
  const tests = readdirSync(path.join(repositoryRoot, 'backend', 'internal', 'adapters', 'codex'))
    .filter((name) => name.endsWith('_test.go'))
    .map((name) => readFileSync(path.join(repositoryRoot, 'backend', 'internal', 'adapters', 'codex', name), 'utf8'))
    .join('\n');
  for (const test of named) assert.match(tests, new RegExp(`^func ${test}\\(t \\*testing\\.T\\)`, 'mu'), `no test ${test}`);
  assert.match(app[chat].run, /test -f "\$CK_CODEX_RUNTIME_TEST_BINARY"/u, 'the step does not check the binary is there');
  assert.match(app[chat].run, /--- SKIP/u, 'the step lets a runtime test be skipped');

  const marks = at(/canonkeeper-release" marks|\$tool\.exe" marks --kind nsis/u);
  const imports = at(/node scripts\/desktop-app-imports\.mjs/u);
  assert.ok(imports > marks && marks >= 0, 'the imports are read in the installer unpacked for its marks');
  assert.equal(app[imports].if, "runner.os == 'Windows'");
  assert.equal(app[imports].run, 'node scripts/desktop-app-imports.mjs "$RUNNER_TEMP/marks/nsis" "$(ls desktop/src-tauri/target/release/bundle/nsis/*-setup.exe)"');
  assert.ok(imports < app.findIndex((step) => /^Install \(Windows/u.test(step.name ?? '')), 'the imports are read before the install');
  const manifest = JSON.parse(readFileSync(path.join(repositoryRoot, 'open-source', 'manifest.json'), 'utf8'));
  assert.ok(manifest.include.includes('scripts/desktop-app-imports.mjs'), 'the public repository has not the reader of the imports');
});
