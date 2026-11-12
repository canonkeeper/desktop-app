import { X } from 'lucide-react';
import { useRef, type ReactNode } from 'react';
import { Button } from '../controls/Button';
import { IconButton } from '../controls/IconButton';
import { useNarrowScreen } from '../use-narrow-screen';
import { Dialog } from './Dialog';
import { Sheet } from './Sheet';
import type { PresenceStatus } from './use-presence';
import styles from './Confirm.module.css';

/** The two lines a deletion says, word for word (specs/2026-11-10-ux2-patterns-actions.md PA3.1). */
export const deletionLines = {
  /** What the server gives back from «Недавно удалённые»: a saga, a book, a chapter, a drawing (specs/2026-11-11-ux7-trash.md). */
  returns: 'Можно вернуть 7 дней — в «Недавно удалённых».',
  /** Everything else: a chat, a variant or an edit of a drawing, an own picture, a note, a record, a material, a chapter in work. */
  gone: 'Вернуть будет нельзя.',
} as const;

export interface ConfirmProps {
  /** The question: «Удалить книгу «…»?». */
  title: string;
  /** The one line of what follows; a deletion says one of `deletionLines`. */
  children: ReactNode;
  /** At most one more line: what goes with it, in the words of the author («…со всеми версиями»). */
  also?: ReactNode;
  /** The action by the verb of the question: «Удалить книгу». */
  action: string;
  /** No way back: the action is red and the focus stands on «Отмена». */
  irreversible?: boolean;
  /** The action runs: it is busy on its button, the window does not close. */
  busy?: boolean;
  /** Why the action did not happen: a line in the window, which stays open. */
  error?: ReactNode;
  onConfirm: () => void;
  onCancel: () => void;
  state?: PresenceStatus;
  /** The voice presses the action only after the author's «да» (V5, A4); a plain choice may say `no`. */
  consent?: 'yes' | 'no';
  /**
   * A second answer of a question of two ways (specs/2026-11-11-ux7-upload.md UP2.2: «Заменить» or «Добавить новой»):
   * an outline button between «Отмена» and the action.
   */
  alternative?: { label: string; onClick: () => void };
}

/**
 * The one confirmation of the product (specs/2026-11-10-ux2-patterns-actions.md UC2; board 15·01, `Pattern/Common/Confirm`
 * and `Confirm/Mobile`): the question, one line of what follows, «Отмена» on the left and the action by the same verb on the
 * right. Red only for what has no way back; on a phone always a sheet from below. Esc, a click beside it and «Назад» are
 * «Отмена».
 */
export function Confirm({
  action,
  alternative,
  also,
  busy = false,
  children,
  consent = 'yes',
  error,
  irreversible = false,
  onCancel,
  onConfirm,
  state = 'open',
  title,
}: ConfirmProps) {
  const phone = useNarrowScreen(767);
  const cancelRef = useRef<HTMLButtonElement>(null);
  const actionRef = useRef<HTMLButtonElement>(null);
  const cancel = () => {
    if (!busy) onCancel();
  };
  const body = (
    <section className={styles.confirm} data-confirm="">
      {phone ? <div aria-hidden="true" className={styles.handle} /> : null}
      <div className={styles.head}>
        <h2 className={styles.title} data-overlay-heading="">{title}</h2>
        {phone ? null : (
          <IconButton aria-label="Закрыть" disabled={busy} onClick={cancel} size="sm">
            <X aria-hidden="true" size={16} />
          </IconButton>
        )}
      </div>
      <div className={styles.line}>{children}</div>
      {also ? <p className={styles.line}>{also}</p> : null}
      {error ? <p className={styles.error} role="alert">{error}</p> : null}
      <div className={styles.actions}>
        <Button disabled={busy} onClick={cancel} ref={cancelRef} size={phone ? 'touch' : 'md'} variant="outline">Отмена</Button>
        {alternative ? (
          <Button disabled={busy} onClick={alternative.onClick} size={phone ? 'touch' : 'md'} variant="outline">{alternative.label}</Button>
        ) : null}
        <Button data-voice-consent={consent} loading={busy} onClick={onConfirm} ref={actionRef} size={phone ? 'touch' : 'md'}
          variant={irreversible ? 'danger' : 'primary'}>{action}</Button>
      </div>
    </section>
  );
  const focus = irreversible ? cancelRef : actionRef;
  return phone ? (
    <Sheet canDismiss={!busy} className={styles.sheet} initialFocusRef={focus} label={title} onDismiss={cancel} state={state}>{body}</Sheet>
  ) : (
    <Dialog canDismiss={!busy} dismissOnScrim initialFocusRef={focus} label={title} onDismiss={cancel} state={state} width={440}>{body}</Dialog>
  );
}
