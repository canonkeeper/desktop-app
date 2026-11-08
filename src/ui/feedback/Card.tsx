import type { HTMLAttributes, ReactNode } from 'react';
import styles from './feedback.module.css';

export interface CardProps extends Omit<HTMLAttributes<HTMLDivElement>, 'title'> {
  actions?: ReactNode;
  description?: ReactNode;
  title?: ReactNode;
}

export function Card({ actions, children, className, description, title, ...props }: CardProps) {
  return (
    <div {...props} className={`${styles.card}${className ? ` ${className}` : ''}`}>
      {title || description ? (
        <header className={styles.cardHeader}>
          {title ? <strong>{title}</strong> : null}
          {description ? <span>{description}</span> : null}
        </header>
      ) : null}
      <div className={styles.cardContent}>{children}</div>
      {actions ? <footer className={styles.cardActions}>{actions}</footer> : null}
    </div>
  );
}
