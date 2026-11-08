import type { HTMLAttributes, ReactNode } from 'react';
import styles from './feedback.module.css';

export interface EmptyStateProps extends Omit<HTMLAttributes<HTMLDivElement>, 'title'> {
  action?: ReactNode;
  description?: ReactNode;
  icon?: ReactNode;
  title: ReactNode;
}

export function EmptyState({ action, className, description, icon, title, ...props }: EmptyStateProps) {
  return (
    <div {...props} className={`${styles.emptyState}${className ? ` ${className}` : ''}`}>
      {icon ? <span aria-hidden="true" className={styles.emptyIcon}>{icon}</span> : null}
      <strong>{title}</strong>
      {description ? <p>{description}</p> : null}
      {action}
    </div>
  );
}
