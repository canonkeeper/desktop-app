import { useEffect, useRef } from 'react';

interface ReaderKeys {
  previous: (() => void) | null;
  next: (() => void) | null;
  find: () => void;
  /** Esc: closes the nearest thing open — «Аа», the bar of the search, «Содержание», «Только текст»; false when none. */
  escape: () => boolean;
}

function typing(target: EventTarget | null) {
  return target instanceof Element && Boolean(target.closest('input, textarea, select, [contenteditable="true"], [role="textbox"]'));
}

/**
 * The keys of the reader (specs/2026-11-10-ux7-reader.md RD1.3): Alt+← and Alt+→ — the neighbouring chapters, «/» —
 * «Найти», Esc — close a panel, the bar of the search or leave «Только текст». A modal window over the page keeps its keys,
 * and a field keeps the letters typed into it.
 */
export function useReaderKeys(keys: ReaderKeys) {
  const latest = useRef(keys);
  latest.current = keys;
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.defaultPrevented || document.querySelector('[aria-modal="true"]')) return;
      const { escape, find, next, previous } = latest.current;
      if (event.altKey && !event.ctrlKey && !event.metaKey && !event.shiftKey && !typing(event.target)
        && (event.key === 'ArrowLeft' || event.key === 'ArrowRight')) {
        const step = event.key === 'ArrowLeft' ? previous : next;
        event.preventDefault();
        step?.();
        return;
      }
      if (event.key === '/' && !event.altKey && !event.ctrlKey && !event.metaKey && !typing(event.target)) {
        event.preventDefault();
        find();
        return;
      }
      if (event.key === 'Escape' && escape()) event.preventDefault();
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, []);
}
