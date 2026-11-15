// SPDX-License-Identifier: AGPL-3.0-only
import { emptyMemory, type ChoiceMemory, type FolderChoice } from './choice';

/**
 * The choice of the folders the author made, kept in this browser by the author and the saga
 * (specs/2026-11-15-desk-obsidian-web.md OW2.6): it lives past a leave of the page, a reload and a load. It is a draft of the
 * form — the links of the books and the records with the notes are the server's alone —, by paths inside the folder of the
 * saga: the absolute path of the folder is never written. The unlink of the folder forgets it; a change of the folder keeps it.
 */
const prefix = 'canonkeeper.obsidian.choice.';
const keyOf = (author: string, projectId: string) => `${prefix}${author}.${projectId}`;

function storage(): Storage | null {
  try {
    return window.localStorage;
  } catch {
    return null;
  }
}

const choicePattern = /^(?:skip|book|together|materials:(?:skill|place|character|item|faction|other))$/u;

export function readChoice(author: string, projectId: string): ChoiceMemory {
  if (!author || !projectId) return emptyMemory;
  try {
    const raw = storage()?.getItem(keyOf(author, projectId));
    if (!raw) return emptyMemory;
    const value = JSON.parse(raw) as Partial<ChoiceMemory>;
    const kinds = Object.fromEntries(Object.entries(value.kinds ?? {})
      .filter((entry): entry is [string, FolderChoice] => typeof entry[1] === 'string' && choicePattern.test(entry[1])));
    const notes = Array.isArray(value.notes) ? value.notes.filter((note): note is string => typeof note === 'string') : [];
    return { kinds, notes };
  } catch {
    return emptyMemory;
  }
}

export function keepChoice(author: string, projectId: string, memory: ChoiceMemory) {
  if (!author || !projectId) return;
  try {
    storage()?.setItem(keyOf(author, projectId), JSON.stringify(memory));
  } catch {
    // The storage may be off or full: the choice lives while the page is open.
  }
}

export function forgetChoice(author: string, projectId: string) {
  if (!author || !projectId) return;
  try {
    storage()?.removeItem(keyOf(author, projectId));
  } catch {
    // see keepChoice
  }
}
