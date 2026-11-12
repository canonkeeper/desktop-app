import { useId, type HTMLAttributes, type ReactNode } from 'react';
import styles from './EntryCard.module.css';

interface EntryCardProps extends Omit<HTMLAttributes<HTMLElement>, 'title'> {
  title: string;
  /** What the way gives, in a line under its name. */
  text?: ReactNode;
  children?: ReactNode;
}

/**
 * One way into the product as a card (specs/2026-11-10-ux3-first-screen.md FS1.2, FS2.2; `Pattern/Shelf/First Screen`,
 * `Pattern/Shelf/Demo Offer`, `Pattern/Proofread/Card`): «Назовите сагу», «Посмотреть на примере», «Вычитать мою главу»
 * look alike wherever they stand — the first screen, a saga without a book, «Письмо».
 */
export function EntryCard({ children, className, text, title, ...props }: EntryCardProps) {
  const titleId = useId();
  return (
    <section aria-labelledby={titleId} className={className ? `${styles.entry} ${className}` : styles.entry} {...props}>
      <h2 className={styles.title} id={titleId}>{title}</h2>
      {text ? <p className={styles.text}>{text}</p> : null}
      {children}
    </section>
  );
}

/** A line of the card under its actions: muted, or the words of a failure. */
export function EntryLine({ children, tone = 'muted', ...props }: HTMLAttributes<HTMLParagraphElement> & { tone?: 'muted' | 'error' }) {
  return <p className={styles.line} data-tone={tone} {...props}>{children}</p>;
}
