import type { HTMLAttributes } from 'react';
import styles from './feedback.module.css';

export interface BadgeProps extends HTMLAttributes<HTMLSpanElement> {
  tone?: 'neutral' | 'success' | 'warning' | 'indexing' | 'error' | 'info';
}

export function Badge({ children, className, tone = 'neutral', ...props }: BadgeProps) {
  return (
    <span {...props} className={`${styles.badge} ${styles[tone]}${className ? ` ${className}` : ''}`}>
      {tone === 'neutral' ? null : <span aria-hidden="true" className={styles.badgeDot} />}
      {children}
    </span>
  );
}
