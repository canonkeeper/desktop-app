import type { IndexingStatus } from '../../entities/manuscript/api';
import { chatReadingLine } from '../../entities/manuscript/preparation-copy';
import type { PreparationLine, PreparationView } from '../../entities/manuscript/preparation-slot';
import { Button } from '../../ui/controls/Button';
import { Alert } from '../../ui/feedback/Alert';
import styles from './ResearchChatPanel.module.css';

interface PreparationNoticeProps {
  /** The status of the saga of the chat: its reading, or that the reading stopped. */
  status: IndexingStatus;
  onOpenBooks: () => void;
  /**
   * The line of the reading the edition says, with its buttons and windows (specs/2026-11-13-desk-helper-web.md HW3.1,
   * specs/2026-11-13-desk-consent-web.md RW1.2); the site has none.
   */
  edition?: PreparationView | null;
  /** The author may change the saga: the buttons of the edition are his. */
  canEdit?: boolean;
}

/**
 * The advice over the field of the chat while the saga is read (specs/2026-11-10-ux3-saga-state.md SS3.1): the reading by
 * the counts of the server, as on the shelf, and that a question may be asked now — the answer before and after the end
 * of the reading is the same, so there is no plate of a partial answer. A reading that stopped sends to the books, where
 * «Продолжить» stands. Nothing once the saga is read. An edition with the slot of the line says it in the same words and
 * with the same buttons as on the shelf and the map (the app: the helper, the consent, the reading by «Свой ИИ», its pauses).
 */
export function PreparationNotice({ canEdit = false, edition, onOpenBooks, status }: PreparationNoticeProps) {
  const lines = edition?.lines ?? [];
  const instead = lines.filter((line) => !line.under);
  const note = (line: PreparationLine) => (
    <Alert aria-label={line.label} className={styles.preparation} key={line.label} role="note" tone="info">
      <span>{line.title}</span>
      {canEdit ? line.actions : null}
    </Alert>
  );
  // The lines of the edition in place of the open advice, and those that stand under whichever is said.
  return (
    <>
      {instead.length > 0 ? instead.map(note) : <OpenNotice onOpenBooks={onOpenBooks} status={status} />}
      {lines.filter((line) => line.under).map(note)}
      {edition?.windows}
    </>
  );
}

/** The open advice of the reading: the counts of the server, or that the reading stopped. */
function OpenNotice({ onOpenBooks, status }: Pick<PreparationNoticeProps, 'onOpenBooks' | 'status'>) {
  const context = status.published.exists ? status.published.context : null;
  // Advice, not an alarm: its counts change every few seconds and are not announced.
  if (status.preparation) {
    // A reading that waits is named so: the helper is not connected (WN3.2), or it waits for the author's consent (GW4.3).
    const waiting = status.preparation.waiting;
    const label = waiting === 'helper' ? 'Помощник ещё не подключён'
      : waiting === 'consent' ? 'Помощник готов прочитать книги' : 'Помощник читает книгу';
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
