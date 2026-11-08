import type { ReactNode } from 'react';
import styles from './navigation.module.css';

interface BreadcrumbsProps {
  children: ReactNode;
  className?: string;
  label: string;
}

export function Breadcrumbs({ children, className, label }: BreadcrumbsProps) {
  return (
    <nav aria-label={label} className={`${styles.breadcrumbs}${className ? ` ${className}` : ''}`}>
      <ol>{children}</ol>
    </nav>
  );
}
