import { useSyncExternalStore } from 'react';

// «новое» at the section «Галерея» (specs/2026-10-29-saga-illustrations.md R11.9): an ephemeral fact
// of this browser, not product state. Without browser storage the mark simply stays.
const seenKey = 'canonkeeper.gallery.seen';
const listeners = new Set<() => void>();

function readSeen() {
  try {
    return window.localStorage.getItem(seenKey) === '1';
  } catch {
    return false;
  }
}

function subscribe(listener: () => void) {
  listeners.add(listener);
  const fromOtherTab = (event: StorageEvent) => {
    if (event.key === seenKey) listener();
  };
  window.addEventListener('storage', fromOtherTab);
  return () => {
    listeners.delete(listener);
    window.removeEventListener('storage', fromOtherTab);
  };
}

/** The author opened the gallery: the section loses its mark «новое». */
export function markGallerySeen() {
  try {
    window.localStorage.setItem(seenKey, '1');
  } catch {
    return;
  }
  listeners.forEach((listener) => listener());
}

/** Whether the author has opened the gallery in this browser. */
export function useGallerySeen() {
  return useSyncExternalStore(subscribe, readSeen, () => true);
}
