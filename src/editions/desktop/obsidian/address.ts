// SPDX-License-Identifier: AGPL-3.0-only

/**
 * The address of the page of the bridge with Obsidian in «Книги» of the app (specs/2026-11-15-desk-obsidian-web.md OW1.1): the
 * saga and the step (`step=preview` — «что получится»); where the author came from is the state of the history.
 */
export const obsidianPath = '/manuscript/obsidian';

export function obsidianAt(projectId: string) {
  return { path: obsidianPath, query: { projectId } };
}
