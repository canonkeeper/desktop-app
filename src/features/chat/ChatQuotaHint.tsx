import styles from './ChatQuotaHint.module.css';

/** Questions left today above the composer (quota spec R5.3). */
export function ChatQuotaHint({ text }: { text: string }) {
  return text ? <p aria-live="polite" className={styles.hint} role="status">{text}</p> : null;
}
