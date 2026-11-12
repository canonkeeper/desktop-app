import { Copy, CornerDownRight, MessageSquareX } from 'lucide-react';
import type { ReactNode, RefObject } from 'react';
import { captureMessageText } from '../../shared/notes/note-capture';
import { Button } from '../../ui/controls/Button';
import { useToasts } from '../../ui/feedback/useToastQueue';
import { ChatNoteAction } from './ChatNoteAction';
import type { CanonicalAgentTimelineEvent } from './feed/types';
import styles from './AnswerActions.module.css';

/** «Неверно» puts the beginning of a correction into the field, the caret after the colon (AA1.5). */
export const wrongAnswerStart = 'Это неверно: ';

/**
 * How a button under an answer puts a question into the field: the author sends it himself (AA1.3). `unavailable` is
 * why it cannot now — a question is being answered, the questions of the day are over; none for the reader of a saga.
 */
export interface AnswerAsk {
  put: (text: string) => void;
  unavailable?: string | null | undefined;
}

interface AnswerActionsProps {
  chatId: string;
  event: CanonicalAgentTimelineEvent;
  contentRef: RefObject<HTMLElement | null>;
  /** The questions the helper offered at the end of the last answer (AA1.2). */
  continuations: readonly string[];
  ask?: AnswerAsk | undefined;
  /** «Что дальше» of the first answer of the saga, under the buttons (specs/2026-11-10-ux3-first-screen.md FS3.1). */
  next?: ReactNode;
}

/**
 * What stands under an answer of the chat (specs/2026-11-11-ux4-after-answer.md AA1): the questions the helper offered —
 * a press puts one into the field, nothing is sent — then «Неверно», «Скопировать» and «В заметки». The two last take the
 * answer as the feed shows it: without the block of continuations (AA1.4).
 */
export function AnswerActions({ ask, chatId, contentRef, continuations, event, next }: AnswerActionsProps) {
  const toasts = useToasts();
  const reason = ask?.unavailable || null;
  function copy() {
    const content = contentRef.current;
    if (!content) return;
    const { body } = captureMessageText(content);
    void (navigator.clipboard?.writeText(body) ?? Promise.reject(new Error('no clipboard'))).then(
      () => toasts.push({ message: 'Ответ скопирован.', tone: 'success' }),
      () => toasts.push({ message: 'Не получилось скопировать: выделите ответ и скопируйте сами.', tone: 'warning' }),
    );
  }
  return (
    <div className={styles.under}>
      {ask && continuations.length ? (
        <ul aria-label="Дальше можно спросить" className={styles.continuations}>
          {continuations.map((text) => (
            <li key={text}>
              <Button className={styles.continuation} disabledReason={reason} onClick={() => ask.put(text)} size="sm" variant="outline">
                <CornerDownRight aria-hidden="true" size={15} />
                <span>{text}</span>
              </Button>
            </li>
          ))}
        </ul>
      ) : null}
      <div aria-label="Действия с ответом" className={styles.actions} role="group">
        {ask ? (
          <Button disabledReason={reason} onClick={() => ask.put(wrongAnswerStart)} size="touch" variant="ghost">
            <MessageSquareX aria-hidden="true" size={16} />Неверно
          </Button>
        ) : null}
        <Button onClick={copy} onPointerDown={(press) => press.preventDefault()} size="touch" variant="ghost">
          <Copy aria-hidden="true" size={16} />Скопировать
        </Button>
        <ChatNoteAction chatId={chatId} contentRef={contentRef} event={event} />
      </div>
      {next}
    </div>
  );
}
