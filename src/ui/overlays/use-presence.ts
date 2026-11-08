import { useEffect, useRef, useState } from 'react';

export type PresenceStatus = 'open' | 'closing';

interface Presence<T> {
  /** Остаётся true до завершения анимации закрытия. */
  mounted: boolean;
  /** Значение для data-state, по которому работают глобальные стили анимации. */
  status: PresenceStatus;
  /** Последнее активное значение, чтобы содержимое не исчезало во время закрытия. */
  value: T | null;
}

function prefersReducedMotion(): boolean {
  // Без matchMedia или при prefers-reduced-motion узел закрывается синхронно.
  if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') {
    return true;
  }
  return window.matchMedia('(prefers-reduced-motion: reduce)').matches;
}

/** Удерживает оверлей и последнее активное значение до завершения анимации закрытия. */
export function usePresence<T>(value: T | null | undefined | false, durationMs = 220): Presence<T> {
  const active = value !== null && value !== undefined && value !== false;
  const [status, setStatus] = useState<'open' | 'closing' | 'closed'>(active ? 'open' : 'closed');
  const retained = useRef<T | null>(active ? (value as T) : null);
  if (active) {
    retained.current = value as T;
    if (status !== 'open') setStatus('open');
  }

  useEffect(() => {
    if (active) return;
    if (retained.current === null) {
      return;
    }
    if (prefersReducedMotion()) {
      setStatus('closed');
      return;
    }
    setStatus('closing');
    const timer = window.setTimeout(() => setStatus('closed'), durationMs);
    return () => window.clearTimeout(timer);
  }, [active, durationMs]);

  return {
    mounted: status !== 'closed',
    status: status === 'closing' ? 'closing' : 'open',
    value: retained.current,
  };
}
