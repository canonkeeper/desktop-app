// SPDX-License-Identifier: AGPL-3.0-only
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import test from 'node:test';
import vm from 'node:vm';

import yaml from 'js-yaml';

import { assemble, codexHomes, codexPin, postgresPrograms, sha256, targets } from './desktop-app.mjs';
import { codexPackageVersion } from './codex-package.mjs';

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
    assert.deepEqual(built, [[`canonkeeper-backend-${linux}`, 'linux']]);
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
  assert.deepEqual(bundle.externalBin, ['../bundle/canonkeeper-backend']);
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

test('the workflow builds both systems with pinned actions and no secret', () => {
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
  assert.ok(!/\bsecrets\./u.test(text), 'the workflow reads a secret');
  const commands = steps.map((step) => step.run ?? '').join('\n');
  for (const needed of ['desktop/postgres/build.py', 'scripts/desktop-app.mjs', 'npx tauri build', '--bundles appimage', 'desktop/ci/smoke.mjs', 'cargo test']) {
    assert.ok(commands.includes(needed), needed);
  }
  // The monorepo does not run it.
  assert.ok(!existsSync(path.join(repositoryRoot, '.github', 'workflows', 'desktop-build.yml')));
});

// specs/2026-11-13-desk-export.md EX2.2, EX2.3: the workflows of the public repository run on a push to main or to a
// branch export/**, the build also on a tag v*, both by hand; a pull request runs nothing — the merge commit GitHub makes
// carries the identity of the account that opened it. The Windows job tests the copy of the Codex homes on a real
// Windows before it builds the installer. actionlint finds nothing.
test('the workflows run on a push to main and to export/** and by hand, never on a pull request', () => {
  const directory = path.join(repositoryRoot, 'desktop', 'ci');
  const workflows = readdirSync(directory).filter((name) => name.endsWith('.yml')).sort();
  assert.deepEqual(workflows, ['desktop-build.yml', 'forbidden.yml']);
  for (const name of workflows) {
    const text = readFileSync(path.join(directory, name), 'utf8');
    const workflow = yaml.load(text);
    const triggers = workflow.on;
    assert.deepEqual(Object.keys(triggers).sort(), ['push', 'workflow_dispatch'], name);
    assert.deepEqual(triggers.push.branches, ['main', 'export/**'], name);
    assert.deepEqual(triggers.push.tags, name === 'desktop-build.yml' ? ['v*'] : undefined, name);
    assert.deepEqual(workflow.permissions, { contents: 'read' }, name);
    const steps = Object.values(workflow.jobs).flatMap((job) => job.steps);
    for (const step of steps.filter((entry) => entry.uses)) assert.match(step.uses, /^[\w.-]+\/[\w.-]+@[0-9a-f]{40}$/u, `${name}: ${step.uses}`);
    assert.ok(!/\bsecrets\./u.test(text), `${name} reads a secret`);
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

test('the set is laid out again from scratch', () => {
  withStand(({ root, postgres, codex, out, build }) => {
    writeFile(path.join(out, 'codex-homes', 'stale', 'x'), 'left from an earlier run\n');
    assemble({ root, target: linux, postgres, codex, out, build });
    assert.ok(!existsSync(path.join(out, 'codex-homes', 'stale')));
    assert.ok(existsSync(path.join(out, 'codex', 'codex-app-server')));
  });
});
