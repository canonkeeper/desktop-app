/**
 * What the author typed and the server did not take (specs/2026-11-07-writing-screen.md WS4.11;
 * specs/2026-11-11-ux5-writing-path.md WP5.2). It is kept in this browser until a save passes, so leaving the chapter,
 * closing the tab or the browser with a save that failed loses nothing. The key names the author and the chapter:
 * another account in the same browser never sees it; a sign-out forgets it (`author-drafts.ts` of the shared code, by the
 * prefix), and a copy older than a month is forgotten when read. One store for the sheet of «Письмо» and the editor of the
 * desktop app (specs/2026-11-13-desk-editor.md ED1.2): a chapter has one key whichever screen typed it.
 */
export interface UnsavedCopy {
  /** When it was kept. */
  at: string;
  /** The revision of the draft the author typed on. */
  revision: number;
  edits: { title?: string; idea?: string; text?: string };
}

const fields = ['title', 'idea', 'text'] as const;
/** The prefix the sign-out forgets by (author-drafts.ts). */
const prefix = 'canonkeeper.writing.unsaved.';
/** How long a copy waits for its author: a month. */
export const unsavedKeptMs = 30 * 24 * 60 * 60 * 1000;
const storageKey = (author: string, draftId: string) => `${prefix}${author}.${draftId}`;

function storage(): Storage | null {
  try {
    return window.localStorage;
  } catch {
    return null;
  }
}

export function readUnsaved(author: string, draftId: string, now = Date.now()): UnsavedCopy | null {
  if (!author) return null;
  try {
    const store = storage();
    const raw = store?.getItem(storageKey(author, draftId));
    if (!raw) return null;
    const value = JSON.parse(raw) as Partial<UnsavedCopy>;
    if (typeof value.at !== 'string' || typeof value.revision !== 'number' || !value.edits || typeof value.edits !== 'object') return null;
    if (now - Date.parse(value.at) > unsavedKeptMs) {
      store?.removeItem(storageKey(author, draftId));
      return null;
    }
    const edits: UnsavedCopy['edits'] = {};
    for (const field of fields) {
      const text = value.edits[field];
      if (typeof text === 'string') edits[field] = text;
    }
    return Object.keys(edits).length ? { at: value.at, revision: value.revision, edits } : null;
  } catch {
    return null;
  }
}

export function keepUnsaved(author: string, draftId: string, copy: UnsavedCopy) {
  if (!author) return;
  try {
    storage()?.setItem(storageKey(author, draftId), JSON.stringify(copy));
  } catch {
    // The storage may be off or full: the text lives while the page is open.
  }
}

export function forgetUnsaved(author: string, draftId: string) {
  if (!author) return;
  try {
    storage()?.removeItem(storageKey(author, draftId));
  } catch {
    // see keepUnsaved
  }
}
