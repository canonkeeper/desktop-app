import type { IndexingStatus } from '../../entities/manuscript/api';
import { chatReadingLine, waitingLine } from '../../entities/manuscript/preparation-copy';
import { Button } from '../../ui/controls/Button';
import { Alert } from '../../ui/feedback/Alert';
import styles from './ResearchChatPanel.module.css';

interface PreparationNoticeProps {
  /** The status of the saga of the chat: its reading, or that the reading stopped. */
  status: IndexingStatus;
  onOpenBooks: () => void;
}

/**
 * The advice over the field of the chat while the saga is read (specs/2026-11-10-ux3-saga-state.md SS3.1): the reading by
 * the counts of the server, as on the shelf, and that a question may be asked now — the answer before and after the end
 * of the reading is the same, so there is no plate of a partial answer. A reading that stopped sends to the books, where
 * «Продолжить» stands. Nothing once the saga is read.
 */
export function PreparationNotice({ onOpenBooks, status }: PreparationNoticeProps) {
  const context = status.published.exists ? status.published.context : null;
  // Advice, not an alarm: its counts change every few seconds and are not announced.
  if (status.preparation) {
    // A reading that waits for the helper is named so: the line says the helper is not connected (WN3.2).
    const label = waitingLine(status.preparation) ? 'Помощник ещё не подключён' : 'Помощник читает книгу';
    return (
      <Alert aria-label={label} className={styles.preparation} role="note" tone="info">
        <span>{chatReadingLine(status.preparation)}</span>
      </Alert>
    );
  }
  if (context?.state !== 'partial') return null;
  return (
    <Alert aria-label="Помощник читает книгу" className={styles.preparation} role="note" tone="info">
      <span>Помощник прочитал не всю книгу — отвечаю по прочитанной части. Продолжить чтение можно на странице книг.</span>
      <Button onClick={onOpenBooks} size="sm" variant="ghost">К книгам</Button>
    </Alert>
  );
}
