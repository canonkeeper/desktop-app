import type { HTMLAttributes } from 'react';
import styles from './feedback.module.css';

export interface ChipProps extends HTMLAttributes<HTMLSpanElement> {
  tone?: 'citation' | 'indexing' | 'tag';
}

export function Chip({ className, tone = 'tag', ...props }: ChipProps) {
  return <span {...props} className={`${styles.chip} ${styles[tone]}${className ? ` ${className}` : ''}`} />;
}
