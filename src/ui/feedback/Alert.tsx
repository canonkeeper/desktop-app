import type { HTMLAttributes } from 'react';
import styles from './feedback.module.css';

export interface AlertProps extends Omit<HTMLAttributes<HTMLDivElement>, 'role'> {
  /** `note` keeps an advisory notice out of the live announcements an alert makes. */
  role?: 'alert' | 'note';
  tone?: 'neutral' | 'info' | 'success' | 'warning' | 'error';
}

export function Alert({ className, role = 'alert', tone = 'neutral', ...props }: AlertProps) {
  return <div {...props} className={`${styles.alert} ${styles[tone]}${className ? ` ${className}` : ''}`} data-tone={tone} role={role} />;
}
