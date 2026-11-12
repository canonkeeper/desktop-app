import { useEffect, useSyncExternalStore } from 'react';
import { lastToastBorn } from './useToastQueue';

/**
 * A place over the page that takes the toasts while it is open (specs/2026-11-10-ux2-patterns-actions.md PA1.1): a
 * sheet whose own action gives a line with «Отменить» — the card of the saga map on a narrow window — keeps that line
 * in sight instead of under its scrim. The last open place wins; without one the line stands in the place of notices. A
 * place that lists what an earlier line is about — «Недавно удалённые» with its own «Вернуть» — takes only the lines born
 * after it opened (`after`); the earlier ones wait under it and come back when it closes.
 */
export interface ToastHost {
  element: HTMLElement;
  /** Only the lines born after this one stand in the place. */
  after?: number;
}

const hosts: ToastHost[] = [];
const listeners = new Set<() => void>();

function changed() {
  listeners.forEach((listener) => listener());
}

function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

/** The host of the toasts above every other: where the one region stands now, and which lines it takes. */
export function useToastHost() {
  return useSyncExternalStore(subscribe, () => hosts.at(-1) ?? null, () => null);
}

/** Takes the toasts into `element` while it is in the page; `onlyNew` — only the lines born after it opened. */
export function useHostToasts(element: HTMLElement | null, { onlyNew = false }: { onlyNew?: boolean } = {}) {
  useEffect(() => {
    if (!element) return undefined;
    const host: ToastHost = onlyNew ? { element, after: lastToastBorn() } : { element };
    hosts.push(host);
    changed();
    return () => {
      hosts.splice(hosts.indexOf(host), 1);
      changed();
    };
  }, [element, onlyNew]);
}
