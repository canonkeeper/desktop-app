// SPDX-License-Identifier: AGPL-3.0-only
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import test from 'node:test';

import { standIn, target } from './desktop-windows-check.mjs';

const script = path.join(import.meta.dirname, 'desktop-windows-check.mjs');
const skip = process.platform === 'win32' && 'the programs of the test are scripts of the shell of Unix';

/** A directory of programs of the test: rustup lists `targets`, cargo writes how it was called and ends with `code`. */
function withPrograms({ targets, code = 0, rustup = true }, body) {
  const root = mkdtempSync(path.join(tmpdir(), 'canonkeeper-windows-check-test-'));
  try {
    const bin = path.join(root, 'bin');
    mkdirSync(bin);
    const record = path.join(root, 'cargo.txt');
    const program = (name, text) => {
      writeFileSync(path.join(bin, name), `#!/bin/sh\n${text}\n`);
      chmodSync(path.join(bin, name), 0o755);
    };
    if (rustup) program('rustup', `printf '%s\\n' ${targets.map((name) => `'${name}'`).join(' ')}`);
    program(
      'cargo',
      [
        `tool="$AR_${target.replaceAll('-', '_')}"`,
        `[ -x "$tool" ] && [ "$tool" = "$RC_${target.replaceAll('-', '_')}" ] && tools=yes || tools=no`,
        `echo "$* | tools=$tools | smoke=\${CANONKEEPER_SMOKE_UPDATE_KEY:-} \${CANONKEEPER_SMOKE_UPDATE_SOURCE:-}" >> '${record}'`,
        `exit ${code}`,
      ].join('\n'),
    );
    const result = spawnSync(process.execPath, [script], {
      env: { PATH: `${bin}${path.delimiter}/usr/bin${path.delimiter}/bin`, HOME: root },
      encoding: 'utf8',
    });
    body(result, existsSync(record) ? readFileSync(record, 'utf8').trim().split('\n') : []);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
}

test('without the target of Windows the check says it is skipped and succeeds', { skip }, () => {
  for (const options of [{ targets: ['x86_64-unknown-linux-gnu'] }, { targets: [], rustup: false }]) {
    withPrograms(options, (result, calls) => {
      assert.equal(result.status, 0, result.stderr);
      assert.match(result.stdout, /skipped: the Rust target x86_64-pc-windows-msvc is not installed/);
      assert.deepEqual(calls, [], 'cargo ran without the target');
    });
  }
});

test('with the target it runs clippy for Windows as released and as the smoke build, through the stand-in tools', { skip }, () => {
  withPrograms({ targets: ['x86_64-unknown-linux-gnu', target] }, (result, calls) => {
    assert.equal(result.status, 0, result.stderr);
    const clippy = `clippy --locked --target ${target} --workspace --all-targets`;
    assert.deepEqual(calls, [
      `${clippy} -- -D warnings | tools=yes | smoke= `,
      `${clippy} --features smoke -- -D warnings | tools=yes | smoke=key-test-windows-check http://127.0.0.1:47613/latest.json`,
    ]);
    assert.match(result.stdout, /the shell builds for Windows/);
  });
});

test('a build that fails fails the check with the code of cargo and stops', { skip }, () => {
  withPrograms({ targets: [target], code: 101 }, (result, calls) => {
    assert.equal(result.status, 101);
    assert.equal(calls.length, 1, 'the smoke build ran after a failure');
    assert.match(result.stdout, /does not build for Windows \(release\)/);
  });
});

test('the stand-in answers as llvm-rc and writes what lib.exe and llvm-rc are asked for', { skip }, () => {
  const root = mkdtempSync(path.join(tmpdir(), 'canonkeeper-windows-tool-'));
  try {
    const tool = path.join(root, 'windows-tool');
    writeFileSync(tool, standIn);
    chmodSync(tool, 0o755);
    const question = spawnSync(tool, ['-V', '/?'], { encoding: 'utf8' });
    assert.equal(question.status, 0);
    // embed-resource takes the first line for llvm-rc when it names the Resource Converter, and /no-preprocess anywhere.
    assert.match(question.stdout.split('\n')[0], /Resource Converter/);
    assert.match(question.stdout, /no-preprocess/);
    const library = path.join(root, 'ring.lib');
    const resource = path.join(root, 'resource.lib');
    assert.equal(spawnSync(tool, [`-out:${library}`, '-nologo', 'a.o', 'b.o']).status, 0);
    assert.equal(spawnSync(tool, ['/fo', resource, '/C', '65001', '/no-preprocess', '--', 'resource.rc']).status, 0);
    assert.equal(statSync(library).size, 0);
    assert.equal(statSync(resource).size, 0);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});
