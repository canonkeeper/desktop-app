// SPDX-License-Identifier: AGPL-3.0-only
import { useSyncExternalStore } from 'react';

/**
 * What the author hid by the cross of the strip of an update (specs/2026-11-15-desk-updates-web.md UW3.2): a choice of the
 * author in this window, not a state of the server — kept in the storage of the window, read and written without failing
 * the page. A version found is hidden until the next version; a failure is hidden until a new attempt — «Обновить и
 * перезапустить» or a new check (`forgetHiddenFailures`, by the owner of the actions) — so a failure of the retry shows
 * at once (UW3.5).
 */
const storageKey = 'canonkeeper.desktop.update-strip.hidden.v1';
const keptChoices = 20;
const failurePrefix = 'failure:';

/** The key of what a cross hides: a version found, or a failure of a version. */
export const hiddenStripKey = {
  version: (version: string | null) => `version:${version ?? ''}`,
  failure: (failure: string, version: string | null) => `${failurePrefix}${failure}:${version ?? ''}`,
};

const nothing: readonly string[] = [];
/** The choices a write to the storage could not keep: they live until the page is loaded again. */
let unsaved = nothing;
let seenRaw: string | null | undefined;
let seen = nothing;
const listeners = new Set<() => void>();

function parse(raw: string | null): readonly string[] {
  try {
    const kept = JSON.parse(raw ?? '[]') as unknown;
    return Array.isArray(kept) ? kept.filter((each): each is string => typeof each === 'string') : nothing;
  } catch {
    return nothing;
  }
}

/** The choices of the window now; none without its storage but those of this load — the strip simply stands. */
function hiddenNow(): readonly string[] {
  let raw: string | null;
  try {
    raw = window.localStorage.getItem(storageKey);
  } catch {
    return unsaved;
  }
  if (raw !== seenRaw) {
    seenRaw = raw;
    seen = parse(raw);
  }
  return seen;
}

function keep(next: readonly string[]) {
  const kept = next.slice(-keptChoices);
  try {
    window.localStorage.setItem(storageKey, JSON.stringify(kept));
    unsaved = nothing;
  } catch {
    unsaved = kept;
  }
  listeners.forEach((listener) => listener());
}

function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

/** What the crosses of the window hide, as it changes. */
export function useHiddenStrips() {
  return useSyncExternalStore(subscribe, hiddenNow, hiddenNow);
}

export function hideStrip(key: string) {
  keep([...hiddenNow().filter((each) => each !== key), key]);
}

/** A new attempt of the author: the failures hidden before it show again when they come back. */
export function forgetHiddenFailures() {
  const now = hiddenNow();
  const next = now.filter((each) => !each.startsWith(failurePrefix));
  if (next.length !== now.length) keep(next);
}
