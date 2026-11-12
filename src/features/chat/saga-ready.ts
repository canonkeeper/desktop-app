/**
 * Which readiness of a saga this browser has already told of (FS4.3 of the spec «Сага готова», specs/2026-11-09): the
 * time the server gave for it, by project. Only «told here» lives in the browser; that the saga is ready comes from the
 * status of the server.
 */
const storageKey = 'canonkeeper.saga-ready.told';
const kept = 50;

/** Without storage the browser remembers until the page reloads. */
const told = new Map<string, string>();
let restored = false;

function restore() {
  if (restored) return;
  restored = true;
  try {
    const stored: unknown = JSON.parse(window.localStorage.getItem(storageKey) ?? '{}');
    if (stored === null || typeof stored !== 'object') return;
    for (const [projectId, at] of Object.entries(stored)) {
      if (typeof at === 'string') told.set(projectId, at);
    }
  } catch {
    // An unreadable record is as good as none.
  }
}

/** True the first time this browser hears of the readiness `at` of the saga, and never again for it. */
export function firstTelling(projectId: string, at: string) {
  restore();
  if (told.get(projectId) === at) return false;
  told.delete(projectId);
  told.set(projectId, at);
  // The oldest sagas leave first.
  while (told.size > kept) told.delete(told.keys().next().value as string);
  try {
    window.localStorage.setItem(storageKey, JSON.stringify(Object.fromEntries(told)));
  } catch {
    // Without storage the page still tells once until it reloads.
  }
  return true;
}

/** For tests: the browser forgets what it told of. */
export function forgetTellings() {
  told.clear();
  restored = false;
}
