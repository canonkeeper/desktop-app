import styles from './ChatQuotaHint.module.css';

/**
 * A line above the question field (specs/2026-11-10-ux1-chat.md CH3.1, CH3.2, CH5.1): the questions left today, the
 * price of a correction, when a correction will be taken. A change of it is announced; `quiet` is a line that changes
 * as the author types — the count of signs — and stays silent.
 */
export function ChatQuotaHint({ id, quiet = false, text }: { id?: string; quiet?: boolean; text: string }) {
  if (!text) return null;
  return quiet
    ? <p className={styles.hint} id={id}>{text}</p>
    : <p aria-live="polite" className={styles.hint} id={id} role="status">{text}</p>;
}
