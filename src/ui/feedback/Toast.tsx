import { X } from 'lucide-react';
import { useContext, useEffect, useMemo, useRef, type HTMLAttributes, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import { IconButton } from '../controls/IconButton';
import { NoticeSlot } from './notice-slot';
import { useToastHost } from './toast-host';
import { ToastsContext, useToastQueue, type ToastItem, type ToastQueue } from './useToastQueue';
import styles from './feedback.module.css';

export interface ToastRegionProps extends Omit<HTMLAttributes<HTMLElement>, 'children'> {
  onDismiss: (id: number) => void;
  /** The author is on a line: it waits for him (PA1.2). */
  onHold?: (id: number) => void;
  onRelease?: (id: number) => void;
  queue: readonly ToastItem[];
}

/**
 * The area of the toasts: in the place of notices above the page when the product has one (NoticeSlot), in a sheet
 * that hosts them while it is open (toast-host.ts). New lines come at the bottom; the pointer or the focus on a line
 * keeps it; an error is an alert and stays until it is closed.
 */
export function ToastRegion({ className, onDismiss, onHold, onRelease, queue, ...props }: ToastRegionProps) {
  const regionRef = useRef<HTMLElement>(null);
  const slot = useContext(NoticeSlot);
  const host = useToastHost();
  const place = host?.element ?? slot;
  const after = host?.after;
  const lines = after === undefined ? queue : queue.filter((toast) => toast.born > after);

  useEffect(() => {
    const focusRegion = (event: KeyboardEvent) => {
      if (event.key !== 'F6') return;
      event.preventDefault();
      regionRef.current?.focus();
    };
    document.addEventListener('keydown', focusRegion);
    return () => document.removeEventListener('keydown', focusRegion);
  }, []);

  const region = (
    <section
      {...props}
      aria-label="Уведомления"
      className={`${styles.toastRegion}${place ? ` ${styles.toastRegionInFlow}` : ''}${className ? ` ${className}` : ''}`}
      ref={regionRef}
      tabIndex={-1}
    >
      {lines.map((toast) => (
        <div
          aria-atomic="true"
          className={styles.toast}
          data-tone={toast.tone}
          key={toast.id}
          onBlur={(event) => {
            if (!event.currentTarget.contains(event.relatedTarget as Node | null)) onRelease?.(toast.id);
          }}
          onFocus={() => onHold?.(toast.id)}
          onPointerEnter={() => onHold?.(toast.id)}
          onPointerLeave={(event) => {
            if (!event.currentTarget.contains(document.activeElement)) onRelease?.(toast.id);
          }}
          role={toast.tone === 'error' ? 'alert' : 'status'}
        >
          {toast.icon ? <toast.icon aria-hidden="true" className={styles.toastIcon} size={16} /> : <span className={styles.toastMark} />}
          <span className={styles.toastMessage}>
            {toast.message}
            {toast.detail ? <span className={styles.toastDetail}>{toast.detail}</span> : null}
          </span>
          {toast.action}
          <IconButton aria-label="Закрыть уведомление" onClick={() => onDismiss(toast.id)} size="sm">
            <X aria-hidden="true" size={16} />
          </IconButton>
        </div>
      ))}
    </section>
  );
  return place ? createPortal(region, place) : region;
}

/**
 * The one queue of the toasts and its one area (specs/2026-11-10-ux2-patterns-actions.md PA1.1): the root gives its own
 * queue, so the lines live above the pages and a line with «Отменить» stays when the author goes to another section.
 */
export function ToastsProvider({ children, queue }: { children: ReactNode; queue?: ToastQueue }) {
  const own = useToastQueue();
  const toasts = queue ?? own;
  const { dismiss, push } = toasts;
  // A page that puts a line does not draw again when the lines change.
  const value = useMemo(() => ({ dismiss, push }), [dismiss, push]);
  return (
    <ToastsContext.Provider value={value}>
      {children}
      <ToastRegion onDismiss={toasts.dismiss} onHold={toasts.hold} onRelease={toasts.release} queue={toasts.queue} />
    </ToastsContext.Provider>
  );
}
