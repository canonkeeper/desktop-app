// SPDX-License-Identifier: AGPL-3.0-only
/* global process, console */
// The text of a draft of a release (specs/2026-11-13-desk-updates.md UB3.4, UB3.6):
//
//   node desktop/ci/release-body.mjs desktop/release-notes.txt desktop/README.md > body.md
//
// the notes of the version, which latest.json carries signed, and the section «Вернуться на прежнюю версию» of
// desktop/README.md with the commands for each system: the button «Скачать версию …» of the page of the start opens the
// page of a release, and there the author finds how to go back. Only Node built-ins.
import { readFileSync } from 'node:fs';
import { pathToFileURL } from 'node:url';

export const BACK = '## Вернуться на прежнюю версию';

/** The notes and the section of going back, from its heading to the next heading of its level. */
export function releaseBody(notes, readme) {
  const start = readme.split('\n').findIndex((line) => line.trim() === BACK);
  if (start < 0) throw new Error(`desktop/README.md has no section «${BACK.slice(3)}»`);
  const lines = readme.split('\n').slice(start);
  const end = lines.findIndex((line, at) => at > 0 && /^## /u.test(line));
  const section = (end < 0 ? lines : lines.slice(0, end)).join('\n').trim();
  return `${notes.trim()}\n\n${section}\n`.replace(/^\n+/u, '');
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const [notes, readme] = process.argv.slice(2);
  if (!notes || !readme) {
    console.error('usage: release-body.mjs <notes> <README>');
    process.exit(1);
  }
  process.stdout.write(releaseBody(readFileSync(notes, 'utf8'), readFileSync(readme, 'utf8')));
}
