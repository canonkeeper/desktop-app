/**
 * Which authors closed «Что ещё можно» in this browser (specs/2026-11-10-ux3-first-screen.md FS3.3): only the mark lives
 * here — that the saga has an answer comes from the server. On another device the row shows once more, and that is no harm.
 */
const storageKey = 'canonkeeper.more-hints.hidden';

/** Without storage the browser remembers until the page reloads. */
const hidden = new Set<string>();
let restored = false;

function restore() {
  if (restored) return;
  restored = true;
  try {
    const stored: unknown = JSON.parse(window.localStorage.getItem(storageKey) ?? '[]');
    if (Array.isArray(stored)) for (const author of stored) if (typeof author === 'string') hidden.add(author);
  } catch {
    // An unreadable record is as good as none.
  }
}

/** The author closed the row in this browser. */
export function hintsHidden(authorId: string) {
  restore();
  return hidden.has(authorId);
}

/** «Скрыть подсказки»: for good, for this author in this browser. */
export function hideHints(authorId: string) {
  restore();
  hidden.add(authorId);
  try {
    window.localStorage.setItem(storageKey, JSON.stringify([...hidden]));
  } catch {
    // Without storage the row stays closed until the page reloads.
  }
}

/** For tests: the browser forgets the marks. */
export function forgetHiddenHints() {
  hidden.clear();
  restored = false;
}
