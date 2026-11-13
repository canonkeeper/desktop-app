import type { LucideIcon } from 'lucide-react';
import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from 'react';

/** `info` is a notice that is no result of the author's action: a line of a launch of the app (board 44·22). */
export type ToastTone = 'neutral' | 'success' | 'warning' | 'error' | 'info';
/**
 * A line of the result of an action (specs/2026-11-10-ux2-patterns-actions.md UC1). `action` is «Отменить» or
 * «Вернуть» of a reversible action; `detail` is a quieter line under the message: what the message is about. A
 * persistent line stays until it is closed or its action is used. `icon` stands in place of the mark of the tone where the
 * board draws the line with one (the lines of a launch of the app, specs/2026-11-13-desk-settings.md ST4.4).
 */
export type ToastInput = { action?: ReactNode; detail?: string; icon?: LucideIcon; message: string; persistent?: boolean; tone: ToastTone };
/** `born` orders the lines of every queue as they came: a host may take only the lines born after it opened. */
export type ToastItem = ToastInput & { born: number; expiresAt?: number; held?: number; id: number };

let born = 0;

/** The last line born in any queue: a host that takes only new lines marks its opening by it (toast-host.ts). */
export function lastToastBorn() {
  return born;
}

/** How long a line stays (PA1.2): one with «Отменить» at least 10 s, one without an action 5 s; an error stays. */
export const toastDurations = { action: 10_000, plain: 5_000 } as const;
/** After the author leaves a held line, it stays at least this long. */
const afterHold = 2_000;
/** The lines the region shows at once: an older one goes when a fourth comes. */
const shown = 3;

function durationOf(input: ToastInput) {
  if (input.persistent || input.tone === 'error') return undefined;
  return input.action ? toastDurations.action : toastDurations.plain;
}

export function useToastQueue() {
  const [queue, setQueue] = useState<ToastItem[]>([]);
  const nextId = useRef(0);
  const dismiss = useCallback((id: number) => {
    setQueue((current) => current.filter((toast) => toast.id !== id));
  }, []);
  const push = useCallback((input: ToastInput) => {
    const id = ++nextId.current;
    const duration = durationOf(input);
    const toast: ToastItem = { ...input, born: born += 1, expiresAt: duration ? Date.now() + duration : undefined, id };
    setQueue((current) => [...current, toast].slice(-shown));
    return id;
  }, []);
  /** The author is on the line — the pointer over it or the focus in it: it waits for him (PA1.2). */
  const hold = useCallback((id: number) => {
    setQueue((current) => current.map((toast) => toast.id === id && toast.expiresAt !== undefined
      ? { ...toast, expiresAt: undefined, held: Math.max(0, toast.expiresAt - Date.now()) }
      : toast));
  }, []);
  const release = useCallback((id: number) => {
    setQueue((current) => current.map((toast) => toast.id === id && toast.held !== undefined
      ? { ...toast, expiresAt: Date.now() + Math.max(toast.held, afterHold), held: undefined }
      : toast));
  }, []);

  useEffect(() => {
    const timers = queue.flatMap((toast) => toast.expiresAt
      ? [window.setTimeout(() => dismiss(toast.id), Math.max(0, toast.expiresAt - Date.now()))]
      : []);
    return () => timers.forEach(window.clearTimeout);
  }, [dismiss, queue]);

  return { dismiss, hold, push, queue, release };
}

export type ToastQueue = ReturnType<typeof useToastQueue>;
/** What a page needs of the one area of the toasts: to put a line and to take it away. */
export type Toasts = Pick<ToastQueue, 'dismiss' | 'push'>;

/** The one queue of the toasts of a tab, owned by the root (PA1.1): a page puts its lines through it. */
export const ToastsContext = createContext<Toasts | null>(null);

/** The one area of the toasts of the root (PA1.1); a page has no area of its own. */
export function useToasts(): Toasts {
  const toasts = useContext(ToastsContext);
  if (!toasts) throw new Error('useToasts needs the one area of the toasts of the root (ToastsContext).');
  return toasts;
}
