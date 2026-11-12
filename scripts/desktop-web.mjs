// SPDX-License-Identifier: AGPL-3.0-only
// Builds the web of the desktop app into dist-desktop: vite with the edition of the app in its environment. The edition is
// set here and not in the text of an npm script: `CK_EDITION=desktop vite …` is the syntax of a POSIX shell, which the
// shell of Windows does not read (the first Windows build of the installers, 13.11.2026). Vite starts as a program of
// Node, so no shell is asked on either system.
import { spawnSync } from 'node:child_process';
import path from 'node:path';
import { pathToFileURL } from 'node:url';

const repositoryRoot = path.resolve(import.meta.dirname, '..');

/** The command of the build: Node, the program of vite, its arguments and the environment with the edition. */
export function desktopWebBuild(environment = process.env) {
  return {
    command: process.execPath,
    args: [path.join(repositoryRoot, 'node_modules', 'vite', 'bin', 'vite.js'), 'build', '--outDir', 'dist-desktop'],
    options: { cwd: repositoryRoot, stdio: 'inherit', env: { ...environment, CK_EDITION: 'desktop' } },
  };
}

export function buildDesktopWeb() {
  const { command, args, options } = desktopWebBuild();
  if (spawnSync(command, args, options).status !== 0) throw new Error('the web of the desktop app did not build');
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    buildDesktopWeb();
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}
