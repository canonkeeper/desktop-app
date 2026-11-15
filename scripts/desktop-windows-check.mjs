// SPDX-License-Identifier: AGPL-3.0-only
// The build of the desktop shell for Windows, checked on the machine of development before an export
// (specs/2026-11-15-desk-ci-shell.md CS3.1):
//
//   npm run check:desktop:windows
//
// runs `cargo clippy -D warnings` — `cargo check` with the lints of the step of CI — for x86_64-pc-windows-msvc over the
// workspace of desktop/src-tauri with all its targets, once as released and once as the smoke build of updates. Nothing
// is linked: off Windows the archiver (lib.exe) and the resource compiler (llvm-rc) the build scripts of ring and
// tauri-build call are one stand-in that writes empty files, which a check never reads; the C code of ring goes through
// the C compiler of the system. Without the target the check says it is skipped and succeeds. The linking, the
// installer and the tests on Windows stay with CI.
import { spawnSync } from 'node:child_process';
import { chmodSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { pathToFileURL } from 'node:url';

export const target = 'x86_64-pc-windows-msvc';
const variable = target.replaceAll('-', '_');
const workspace = path.resolve(import.meta.dirname, '..', 'desktop', 'src-tauri');

/**
 * The stand-in of lib.exe and llvm-rc for a check: it answers the question of embed-resource as llvm-rc does and
 * writes the empty file each of them is asked for (`-out:FILE` of lib.exe, `/fo FILE` of llvm-rc).
 */
export const standIn = `#!/bin/sh
# lib.exe and llvm-rc for cargo check only (scripts/desktop-windows-check.mjs): nothing it writes is linked.
for argument; do
  case "$argument" in '/?') echo 'OVERVIEW: LLVM Resource Converter (a stand-in for cargo check)'; echo '  /no-preprocess'; exit 0;; esac
done
output=
for argument; do
  if [ -n "$output" ]; then : > "$argument"; output=; continue; fi
  case "$argument" in -out:*) : > "\${argument#-out:}";; /fo) output=1;; esac
done
`;

/** Whether rustup has the target installed; without rustup it has not. */
export function installed(env = process.env) {
  const result = spawnSync('rustup', ['target', 'list', '--installed'], { cwd: workspace, env, encoding: 'utf8' });
  if (result.error || result.status !== 0) return false;
  return result.stdout.split(/\r?\n/).some((line) => line.trim() === target);
}

/** The runs of cargo: the shell as released and as the smoke build, whose source and key are given when it is built. */
export function runs(tool) {
  const tools = tool ? { [`AR_${variable}`]: tool, [`RC_${variable}`]: tool } : {};
  const clippy = ['clippy', '--locked', '--target', target, '--workspace', '--all-targets'];
  return [
    { name: 'release', args: [...clippy, '--', '-D', 'warnings'], env: tools },
    {
      name: 'smoke',
      args: [...clippy, '--features', 'smoke', '--', '-D', 'warnings'],
      env: {
        ...tools,
        CANONKEEPER_SMOKE_UPDATE_KEY: 'key-test-windows-check',
        CANONKEEPER_SMOKE_UPDATE_SOURCE: 'http://127.0.0.1:47613/latest.json',
      },
    },
  ];
}

export function main({ env = process.env, platform = process.platform, log = console.log } = {}) {
  if (!installed(env)) {
    log(`the check of the shell for Windows is skipped: the Rust target ${target} is not installed (rustup target add ${target})`);
    return 0;
  }
  const work = platform === 'win32' ? null : mkdtempSync(path.join(tmpdir(), 'canonkeeper-windows-check-'));
  try {
    let tool = null;
    if (work) {
      tool = path.join(work, 'windows-tool');
      writeFileSync(tool, standIn);
      chmodSync(tool, 0o755);
    }
    for (const run of runs(tool)) {
      const result = spawnSync('cargo', run.args, { cwd: workspace, env: { ...env, ...run.env }, stdio: 'inherit' });
      if (result.error) throw result.error;
      if (result.status !== 0) {
        log(`the shell does not build for Windows (${run.name}): see the errors of cargo above`);
        return result.status ?? 1;
      }
    }
    log(`the shell builds for Windows (${target}): as released and as the smoke build`);
    return 0;
  } finally {
    if (work) rmSync(work, { recursive: true, force: true });
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  process.exitCode = main();
}
