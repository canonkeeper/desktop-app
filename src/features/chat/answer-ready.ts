import { useSyncExternalStore } from 'react';

/**
 * A chat turn the author waits for. Only which Jobs are awaited lives here, an ephemeral
 * fact of the interface; their status comes from the getJob queries of the watcher.
 */
export interface WatchedTurn {
  chatId: string;
  jobId: string;
  /** The question or the chat title, for the browser notification. */
  label?: string;
  projectId: string;
}

let watched: readonly WatchedTurn[] = [];
const turnListeners = new Set<() => void>();

function publishTurns(next: readonly WatchedTurn[]) {
  watched = next;
  turnListeners.forEach((listener) => listener());
}

function subscribeTurns(listener: () => void) {
  turnListeners.add(listener);
  return () => { turnListeners.delete(listener); };
}

export function watchTurn(turn: WatchedTurn) {
  if (watched.some((item) => item.jobId === turn.jobId)) return;
  publishTurns([...watched, turn]);
}

export function forgetTurn(jobId: string) {
  if (watched.some((item) => item.jobId === jobId)) publishTurns(watched.filter((item) => item.jobId !== jobId));
}

/** An identity change or logout drops the turns of the previous account. */
export function forgetAllTurns() {
  if (watched.length) publishTurns([]);
}

export function useWatchedTurns() {
  return useSyncExternalStore(subscribeTurns, () => watched, () => watched);
}

const preferenceKey = 'canonkeeper.answer-ready.browser';
const preferenceListeners = new Set<() => void>();

export type BrowserNotice = 'unsupported' | 'denied' | 'off' | 'on';

function browserPermission() {
  return typeof window !== 'undefined' && 'Notification' in window ? window.Notification.permission : null;
}

function storedPreference() {
  try {
    return window.localStorage.getItem(preferenceKey) === '1';
  } catch {
    return false;
  }
}

function storePreference(on: boolean) {
  try {
    if (on) window.localStorage.setItem(preferenceKey, '1');
    else window.localStorage.removeItem(preferenceKey);
  } catch {
    // Without storage the choice lasts until the page reloads.
  }
  preferenceListeners.forEach((listener) => listener());
}

/** Whether a browser notification is sent when an awaited answer is ready (R6.4). */
export function browserNotice(): BrowserNotice {
  const permission = browserPermission();
  if (permission === null) return 'unsupported';
  if (permission === 'denied') return 'denied';
  return permission === 'granted' && storedPreference() ? 'on' : 'off';
}

function subscribePreference(listener: () => void) {
  preferenceListeners.add(listener);
  return () => { preferenceListeners.delete(listener); };
}

export function useBrowserNotice() {
  return useSyncExternalStore(subscribePreference, browserNotice, () => 'unsupported' as const);
}

/** Turns the browser notification on (asking the browser once) or off. */
export async function toggleBrowserNotice() {
  if (browserNotice() === 'on') {
    storePreference(false);
    return;
  }
  const permission = browserPermission();
  if (permission === null || permission === 'denied') return;
  const granted = permission === 'granted' || await window.Notification.requestPermission() === 'granted';
  storePreference(granted);
}
