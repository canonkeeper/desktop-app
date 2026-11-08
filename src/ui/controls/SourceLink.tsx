import { BookOpen } from 'lucide-react';
import type { ButtonHTMLAttributes } from 'react';
import styles from './SourceLink.module.css';

export function SourceLink({ children, className, ...props }: ButtonHTMLAttributes<HTMLButtonElement>) {
  return <button {...props} type="button" className={[styles.link, className].filter(Boolean).join(' ')}>
    <BookOpen aria-hidden="true" size={14} />
    <span>{children}</span>
  </button>;
}
