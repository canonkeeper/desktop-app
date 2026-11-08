import type { ProgressHTMLAttributes } from 'react';
import styles from './feedback.module.css';

export interface ProgressProps extends Omit<ProgressHTMLAttributes<HTMLProgressElement>, 'value'> {
  value?: number;
}

export function Progress({ className, max = 1, value, ...props }: ProgressProps) {
  return <progress {...props} className={`${styles.progress}${className ? ` ${className}` : ''}`} max={max} value={value} />;
}
