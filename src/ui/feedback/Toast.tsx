import { X } from 'lucide-react';
import { useContext, useEffect, useRef, type HTMLAttributes } from 'react';
import { createPortal } from 'react-dom';
import { IconButton } from '../controls/IconButton';
import { NoticeSlot } from './notice-slot';
import type { ToastItem } from './useToastQueue';
import styles from './feedback.module.css';

export interface ToastRegionProps extends Omit<HTMLAttributes<HTMLElement>, 'children'> {
  onDismiss: (id: number) => void;
  queue: readonly ToastItem[];
}

/** The toasts of a page, in the place of notices above it when the product has one (NoticeSlot). */
export function ToastRegion({ className, onDismiss, queue, ...props }: ToastRegionProps) {
  const regionRef = useRef<HTMLElement>(null);
  const slot = useContext(NoticeSlot);

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
      className={`${styles.toastRegion}${slot ? ` ${styles.toastRegionInFlow}` : ''}${className ? ` ${className}` : ''}`}
      ref={regionRef}
      tabIndex={-1}
    >
      {queue.map((toast) => (
        <div
          aria-atomic="true"
          className={styles.toast}
          data-tone={toast.tone}
          key={toast.id}
          role={toast.tone === 'error' ? 'alert' : 'status'}
        >
          <span className={styles.toastMark} />
          <span className={styles.toastMessage}>{toast.message}</span>
          {toast.action}
          <IconButton aria-label="Закрыть уведомление" onClick={() => onDismiss(toast.id)} size="sm">
            <X aria-hidden="true" size={16} />
          </IconButton>
        </div>
      ))}
    </section>
  );
  return slot ? createPortal(region, slot) : region;
}
