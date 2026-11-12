import type { ReactNode } from 'react';
import { Button } from '../../../ui/controls/Button';
import styles from './ChatFeed.module.css';

/**
 * A correction of the author in the steps of a turn (specs/2026-10-30-chat-steering.md R3.3): his
 * text and the images with their «фото N». `note` says what became of it while it is not simply read.
 */
export function SteerRow({ action, images, note, text }: { action?: ReactNode; images?: ReactNode; note?: string; text: string }) {
  return (
    <div aria-label="Уточнение автора" className={styles.steer} role="note">
      <p>{text}</p>
      {images}
      {note ? <small className={styles.steerNote}>{note}</small> : null}
      {action}
    </div>
  );
}

/** Lets the author ask what the end of the turn left unread as a new question (R3.6; Ч-26). */
export function AskSteerButton({ onAsk }: { onAsk: () => void }) {
  return <Button onClick={onAsk} size="sm" variant="outline">Задать новым вопросом</Button>;
}
