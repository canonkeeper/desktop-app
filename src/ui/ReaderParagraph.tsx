import type { ReactNode } from 'react';
import styles from './ReaderParagraph.module.css';

export function ReaderText({ children, highlighted = false }: { children: string; highlighted?: boolean }) {
  return highlighted ? <mark className={styles.highlight}>{children}</mark> : <>{children}</>;
}

export function ReaderParagraph({ id, text, highlighted = false, onSelect, size = 'default', muted = false }: { id: string; text: string; highlighted?: boolean; onSelect?: () => void; size?: 'default' | 'compact'; muted?: boolean }) {
  const content = <ReaderText highlighted={highlighted}>{text}</ReaderText>;
  return <p className={styles.paragraph} data-paragraph-id={id} data-size={size} data-muted={muted || undefined}>
    {onSelect ? <button aria-pressed={highlighted} className={styles.paragraphButton} onClick={onSelect} type="button">{content}</button> : content}
  </p>;
}

export function ReaderPassage({ children }: { children: ReactNode }) {
  return <div className={styles.passage}>{children}</div>;
}
