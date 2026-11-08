import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react';

export type ToastTone = 'neutral' | 'success' | 'warning' | 'error';
/** A persistent toast stays until it is closed or its action is used. */
export type ToastInput = { action?: ReactNode; message: string; persistent?: boolean; tone: ToastTone };
export type ToastItem = ToastInput & { expiresAt?: number; id: number };

const toastDuration: Partial<Record<ToastTone, number>> = {
  neutral: 6_000,
  success: 6_000,
  warning: 10_000,
};

export function useToastQueue() {
  const [queue, setQueue] = useState<ToastItem[]>([]);
  const nextId = useRef(0);
  const dismiss = useCallback((id: number) => {
    setQueue((current) => current.filter((toast) => toast.id !== id));
  }, []);
  const push = useCallback((input: ToastInput) => {
    const id = ++nextId.current;
    const duration = input.persistent ? undefined : toastDuration[input.tone];
    const toast: ToastItem = { ...input, expiresAt: duration ? Date.now() + duration : undefined, id };
    setQueue((current) => [...current, toast].slice(-3));
    return id;
  }, []);

  useEffect(() => {
    const timers = queue.flatMap((toast) => toast.expiresAt
      ? [window.setTimeout(() => dismiss(toast.id), Math.max(0, toast.expiresAt - Date.now()))]
      : []);
    return () => timers.forEach(window.clearTimeout);
  }, [dismiss, queue]);

  return { dismiss, push, queue };
}
