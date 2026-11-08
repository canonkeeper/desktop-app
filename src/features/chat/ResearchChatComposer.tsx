import { Send, Square, Trash2 } from 'lucide-react';
import { type ClipboardEvent, type FormEvent, type KeyboardEvent, type ReactNode, type Ref, useEffect, useState } from 'react';
import { Button } from '../../ui/controls/Button';
import { IconButton } from '../../ui/controls/IconButton';
import { Textarea } from '../../ui/controls/Textarea';
import { Alert } from '../../ui/feedback/Alert';
import { EmailVerificationNotice } from '../../ui/feedback/EmailVerificationNotice';
import { SupportReference } from '../../ui/feedback/SupportReference';
import { useAccountSession } from '../../shared/session/account-session';
import { Dialog } from '../../ui/overlays/Dialog';
import { Sheet } from '../../ui/overlays/Sheet';
import { formatRetryAfterSeconds } from './research-chat-lifecycle';
import styles from './ResearchChatPanel.module.css';

interface ResearchChatComposerProps {
  active: boolean;
  /**
   * «Приложить главу» and the chapter at the field (specs/2026-11-07-chat-attach-chapter.md CA7): the button, given the
   * size of the field, and the strip above the question; none while the turn takes corrections (CA7.4).
   */
  attachButton?: (layout: { compact: boolean; row: boolean }) => ReactNode;
  chapterStrip?: ReactNode;
  /** A paste longer than the question may be (CA7.2): the field does not cut it, it offers it as a chapter. */
  onLongPaste?: (text: string) => void;
  busy: boolean;
  canStop: boolean;
  canDelete: boolean;
  deleteConfirmationOpen: boolean;
  deleteError?: string;
  deleteErrorCode?: string;
  deleteTitle?: string;
  deleting: boolean;
  /** The question field, for an example to go into (UC2.3). */
  fieldRef?: Ref<HTMLTextAreaElement>;
  onCancelDelete: () => void;
  onConfirmDelete: () => void;
  onDelete: () => void;
  onStop: () => void;
  onTaskChange: (taskText: string) => void;
  onSubmit: (event: FormEvent<HTMLFormElement>) => void;
  recovery: boolean;
  retryAfterSeconds?: number | null;
  retryUnavailable?: boolean;
  /** The turn works and takes corrections: the field stays open (chat steering R4.1). */
  steering?: boolean;
  steerSending?: boolean;
  stopping: boolean;
  taskText: string;
}

export function ResearchChatComposer({
  active,
  attachButton,
  chapterStrip,
  onLongPaste,
  busy,
  canStop,
  canDelete,
  deleteConfirmationOpen,
  deleteError,
  deleteErrorCode,
  deleteTitle,
  deleting,
  fieldRef,
  onCancelDelete,
  onConfirmDelete,
  onDelete,
  onStop,
  onTaskChange,
  onSubmit,
  recovery,
  retryAfterSeconds,
  retryUnavailable = false,
  steering = false,
  steerSending = false,
  stopping,
  taskText,
}: ResearchChatComposerProps) {
  const [mobile, setMobile] = useState(false);
  // On the narrowest phones «Приложить главу» stands in the row over the field: beside it the field would be too narrow.
  const [narrow, setNarrow] = useState(false);
  // An unverified address asks nothing: the notice stands instead of the field (UC1.4).
  const emailUnverified = Boolean(useAccountSession()?.emailVerification) && !active;
  const SendControl = mobile ? IconButton : Button;
  const retryWaiting = typeof retryAfterSeconds === 'number' && retryAfterSeconds > 0;
  const submitLabel = retryWaiting
    ? `Спросить снова через ${formatRetryAfterSeconds(retryAfterSeconds)}`
    : recovery ? 'Спросить снова' : 'Спросить';

  // The browser would cut a paste at the limit of the field without a word: a paste that does not fit is offered as a
  // chapter instead (CA7.2). Signs are counted as the server counts them.
  function handlePaste(event: ClipboardEvent<HTMLTextAreaElement>) {
    if (!onLongPaste || steering) return;
    const pasted = event.clipboardData.getData('text/plain');
    const field = event.currentTarget;
    const kept = [...field.value.slice(0, field.selectionStart)].length + [...field.value.slice(field.selectionEnd)].length;
    if (kept + [...pasted].length <= 8000) return;
    event.preventDefault();
    onLongPaste(pasted);
  }

  function handleTaskKeyDown(event: KeyboardEvent<HTMLTextAreaElement>) {
    if (event.key !== 'Enter' || event.shiftKey || event.nativeEvent.isComposing || event.keyCode === 229) return;
    event.preventDefault();
    event.currentTarget.form?.requestSubmit();
  }

  useEffect(() => {
    if (typeof window.matchMedia !== 'function') return undefined;
    const media = window.matchMedia('(max-width: 760px)');
    const narrowMedia = window.matchMedia('(max-width: 360px)');
    const update = () => {
      setMobile(media.matches);
      setNarrow(narrowMedia.matches);
    };
    update();
    media.addEventListener('change', update);
    narrowMedia.addEventListener('change', update);
    return () => {
      media.removeEventListener('change', update);
      narrowMedia.removeEventListener('change', update);
    };
  }, []);

  const deleteBody = (
    <section className={styles.deleteDialog}>
      <h2>Удалить чат «{deleteTitle}»?</h2>
      <p>История этого диалога будет удалена без возможности восстановления.</p>
      {deleteError ? (
        <Alert className={styles.deleteError} tone="error">
          <span>{deleteError}</span>
          {deleteErrorCode ? <SupportReference code={deleteErrorCode} /> : null}
          <Button loading={deleting} onClick={onConfirmDelete} variant="outline">Повторить</Button>
        </Alert>
      ) : null}
      <div className={styles.deleteDialogActions}>
        <Button disabled={deleting} onClick={onCancelDelete}>Отмена</Button>
        {!deleteError ? <Button loading={deleting} onClick={onConfirmDelete} variant="danger">Удалить чат</Button> : null}
      </div>
    </section>
  );

  return (
    <>
      <form className={styles.composer} onSubmit={onSubmit}>
        {emailUnverified || steering || active ? null : <div className={styles.composerChapter}>
          {narrow && !retryUnavailable ? attachButton?.({ compact: true, row: true }) : null}{chapterStrip}
        </div>}
        {canDelete ? (
          <IconButton aria-label="Удалить чат" className={styles.deleteButton} disabled={busy} onClick={onDelete}>
            <Trash2 aria-hidden="true" size={17} />
          </IconButton>
        ) : null}
        {emailUnverified ? <div className={styles.composerField}><EmailVerificationNotice surface="chat" /></div> : null}
        {emailUnverified ? null : <div className={styles.composerField}>
          <Textarea
            className={styles.composerTextarea}
            disabled={(busy && !steering) || retryWaiting || retryUnavailable}
            onKeyDown={handleTaskKeyDown}
            onPaste={handlePaste}
            label={<span className={styles.visuallyHidden}>{steering ? 'Уточнение' : 'Вопрос'}</span>}
            maxLength={8000}
            onChange={(event) => onTaskChange(event.target.value)}
            placeholder={steering ? (mobile ? 'Уточнить…' : 'Уточнить по ходу работы…') : mobile ? 'Ваш вопрос…' : 'Спросите о своей саге…'}
            ref={fieldRef}
            rows={3}
            value={taskText}
          />
        </div>}
        {emailUnverified ? null : <div className={styles.composerActions}>
          {active ? (<>
            <SendControl aria-label="Остановить" className={mobile ? undefined : styles.sendButton} disabled={!canStop} loading={stopping} onClick={onStop} size={mobile ? 'touch' : 'md'} {...(steering ? {} : { variant: 'primary' as const })}>
              <Square aria-hidden="true" size={14} />
              {mobile ? null : <span className={styles.actionLabel}>Остановить</span>}
            </SendControl>
            {steering ? (
              <SendControl aria-label="Уточнить" className={mobile ? undefined : styles.sendButton} disabled={retryWaiting || !taskText.trim()} loading={steerSending} size={mobile ? 'touch' : 'md'} type="submit" variant="primary">
                <Send aria-hidden="true" size={mobile ? 16 : 17} />
                {mobile ? null : <span className={styles.actionLabel}>Уточнить</span>}
              </SendControl>
            ) : null}
          </>) : retryUnavailable ? null : (<>
            {narrow ? null : attachButton?.({ compact: mobile, row: false })}
            <SendControl aria-label={submitLabel} className={mobile ? undefined : styles.sendButton} disabled={busy || retryWaiting || !taskText.trim()} size={mobile ? 'touch' : 'md'} type="submit" variant="primary">
              <Send aria-hidden="true" size={mobile ? 16 : 17} />
              {mobile ? null : <span className={styles.actionLabel}>{submitLabel}</span>}
            </SendControl>
          </>)}
        </div>}
      </form>
      {deleteConfirmationOpen ? (mobile
        ? <Sheet canDismiss={!deleting} label="Удалить чат" onDismiss={onCancelDelete}>{deleteBody}</Sheet>
        : <Dialog canDismiss={!deleting} label="Удалить чат" onDismiss={onCancelDelete} width={440}>{deleteBody}</Dialog>) : null}
    </>
  );
}
