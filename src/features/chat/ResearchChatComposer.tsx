import { Send } from 'lucide-react';
import { type ClipboardEvent, type FormEvent, type KeyboardEvent, type ReactNode, type Ref, useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import { Button } from '../../ui/controls/Button';
import { Textarea } from '../../ui/controls/Textarea';
import { EmailVerificationNotice } from '../../ui/feedback/EmailVerificationNotice';
import { useAccountSession } from '../../shared/session/account-session';
import { formatRetryAfterSeconds } from './research-chat-lifecycle';
import { nextBlank, replaceBracketsReason, standingBlanks, type Blank } from './brackets';
import styles from './ResearchChatPanel.module.css';

interface ResearchChatComposerProps {
  /** What stands above the field: the block of a limit or the lines of the count and the price (specs/2026-11-10-ux1-chat.md CH3). */
  above?: ReactNode;
  active: boolean;
  /**
   * «Приложить главу» and the chapter at the field (specs/2026-11-07-chat-attach-chapter.md CA7): the button, given the
   * size of the field, and the strip above the question; none while the turn takes corrections (CA7.4).
   */
  attachButton?: (layout: { compact: boolean; row: boolean }) => ReactNode;
  /**
   * The words in brackets an example put into the field (specs/2026-11-12-ux12-shell.md SH1.1): while one stands as it was,
   * the question does not go and Tab steps between them; brackets of the author's own hold nothing.
   */
  blanks?: readonly Blank[] | undefined;
  chapterStrip?: ReactNode;
  /** A chapter stands at the field: an empty field asks for a question or one of its actions (AA4.2). */
  chapterAttached?: boolean;
  /** A paste longer than the question may be (CA7.2): the field does not cut it, it offers it as a chapter. */
  onLongPaste?: (text: string) => void;
  busy: boolean;
  /** The demo saga of the site: the field asks about it, not about a saga of the author (specs/2026-11-09-demo-saga.md DS4.3). */
  demo?: boolean;
  /** The question field, for an example to go into (UC2.3). */
  fieldRef?: Ref<HTMLTextAreaElement>;
  /** What describes the field to a screen reader: the lines above it. */
  describedBy?: string;
  onTaskChange: (taskText: string) => void;
  onSubmit: (event: FormEvent<HTMLFormElement>) => void;
  recovery: boolean;
  retryAfterSeconds?: number | null;
  retryUnavailable?: boolean;
  /**
   * The turn works and takes corrections: the field stays open and «Уточнить» is its one action, a word at any width;
   * «Остановить» stands in the status of the turn (chat steering R4.1; specs/2026-11-10-ux4-chat-waiting.md CW2.4).
   */
  steering?: boolean;
  steerSending?: boolean;
  /** The question waits in the queue: the server takes no correction yet (specs/2026-11-10-ux1-chat.md CH5.1). */
  steerWaits?: boolean;
  /** The questions of the day are over: the field says so and sending is no main action (CH3.4). */
  spent?: boolean;
  /** The author stopped the turn: the field waits for it to stop (Ч-30). */
  stopping: boolean;
  taskText: string;
  /**
   * Why no question goes now, from the server (specs/2026-11-12-desk-window.md WN3.3): the field keeps the words of the
   * author, «Спросить» stays and says it, Enter sends nothing.
   */
  unavailable?: string | null;
}

export function ResearchChatComposer({
  above,
  active,
  attachButton,
  blanks = [],
  chapterStrip,
  chapterAttached = false,
  onLongPaste,
  busy,
  demo = false,
  describedBy,
  fieldRef,
  onTaskChange,
  onSubmit,
  recovery,
  retryAfterSeconds,
  retryUnavailable = false,
  steering = false,
  steerSending = false,
  steerWaits = false,
  spent = false,
  stopping,
  taskText,
  unavailable = null,
}: ResearchChatComposerProps) {
  const [mobile, setMobile] = useState(false);
  // On the narrowest phones «Приложить главу» stands in the row over the field: beside it the field would be too narrow.
  const [narrow, setNarrow] = useState(false);
  // A touch screen has no Shift+Enter: there Enter breaks the line and only the button sends (AA2.5, Ч-72).
  const [touch, setTouch] = useState(false);
  // An unverified address asks nothing: the notice stands instead of the field (UC1.4).
  const emailUnverified = Boolean(useAccountSession()?.emailVerification) && !active;
  const retryWaiting = typeof retryAfterSeconds === 'number' && retryAfterSeconds > 0;
  const submitLabel = retryWaiting
    ? `Спросить снова через ${formatRetryAfterSeconds(retryAfterSeconds)}`
    : recovery ? 'Спросить снова' : 'Спросить';
  // Sending is the main action of the field while a message of the day is left.
  const sendVariant = spent ? {} : { variant: 'primary' as const };
  const placeholder = spent ? (mobile ? 'Вопросы закончились' : 'Вопросы на сегодня закончились')
    : steering && steerWaits ? (mobile ? 'Уточнение — позже…' : 'Уточнение — когда помощник начнёт…')
      : steering ? (mobile ? 'Уточнить…' : 'Уточнить по ходу работы…')
        : active && stopping ? 'Останавливаю ответ…'
          : mobile ? 'Ваш вопрос…' : demo ? 'Спросите сагу-пример…' : 'Спросите о своей саге…';

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

  // The words in brackets of an example are the author's to replace (specs/2026-11-11-ux9-shelf-chat.md SC3.3, Ч-01): the
  // question does not go with them, Tab steps between them — only between those the example put (SH1.1).
  const standing = steering ? [] : standingBlanks(taskText, blanks);
  const brackets = standing.length > 0;
  const sendButton = useRef<HTMLButtonElement | null>(null);
  function selectBracket(back = false, first = false) {
    const node = field.current;
    if (!node) return;
    const place = first ? standing[0] : nextBlank(standing, [node.selectionStart, node.selectionEnd], back);
    if (!place) return;
    node.focus();
    node.setSelectionRange(...place);
  }

  function handleTaskKeyDown(event: KeyboardEvent<HTMLTextAreaElement>) {
    if (event.key === 'Tab' && brackets && !event.ctrlKey && !event.altKey && !event.metaKey) {
      event.preventDefault();
      selectBracket(event.shiftKey);
      return;
    }
    if (event.key !== 'Enter' || event.shiftKey || touch || event.nativeEvent.isComposing || event.keyCode === 229) return;
    event.preventDefault();
    if (steering && steerWaits) return;
    // Enter with brackets left, or while no question goes, is the press of the closed «Спросить»: it says why.
    if (brackets || unavailable) {
      sendButton.current?.click();
      return;
    }
    event.currentTarget.form?.requestSubmit();
  }

  // The field is as high as its text: the page caps it at eight lines and scrolls beyond (CH1.7).
  const field = useRef<HTMLTextAreaElement | null>(null);
  const setField = useCallback((node: HTMLTextAreaElement | null) => {
    field.current = node;
    if (typeof fieldRef === 'function') fieldRef(node);
    else if (fieldRef) fieldRef.current = node;
  }, [fieldRef]);
  const fitField = useCallback(() => {
    const node = field.current;
    if (!node) return;
    node.style.height = '0px';
    node.style.height = `${node.scrollHeight + node.offsetHeight - node.clientHeight}px`;
  }, []);
  useLayoutEffect(fitField, [fitField, taskText, mobile, narrow, emailUnverified]);
  // The lines wrap anew when the field gets another width: the window, the list of chats, the source beside the chat.
  useEffect(() => {
    const node = field.current;
    if (!node || typeof ResizeObserver !== 'function') return undefined;
    let width = node.clientWidth;
    let frame = 0;
    const observer = new ResizeObserver(() => {
      if (node.clientWidth === width) return;
      width = node.clientWidth;
      // Outside the observer: a size set inside it would be an undelivered notification of its own.
      window.cancelAnimationFrame(frame);
      frame = window.requestAnimationFrame(fitField);
    });
    observer.observe(node);
    return () => {
      observer.disconnect();
      window.cancelAnimationFrame(frame);
    };
  }, [fitField, emailUnverified]);

  useEffect(() => {
    if (typeof window.matchMedia !== 'function') return undefined;
    const media = window.matchMedia('(max-width: 760px)');
    const narrowMedia = window.matchMedia('(max-width: 360px)');
    const touchMedia = window.matchMedia('(pointer: coarse)');
    const update = () => {
      setMobile(media.matches);
      setNarrow(narrowMedia.matches);
      setTouch(touchMedia.matches);
    };
    update();
    media.addEventListener('change', update);
    narrowMedia.addEventListener('change', update);
    touchMedia.addEventListener('change', update);
    return () => {
      media.removeEventListener('change', update);
      narrowMedia.removeEventListener('change', update);
      touchMedia.removeEventListener('change', update);
    };
  }, []);

  return (<>
    {emailUnverified ? null : above}
    <form className={styles.composer} onSubmit={onSubmit}>
      {emailUnverified || steering || active ? null : <div className={styles.composerChapter}>
        {narrow && !retryUnavailable ? attachButton?.({ compact: true, row: true }) : null}{chapterStrip}
      </div>}
      {emailUnverified ? <div className={styles.composerField}><EmailVerificationNotice /></div> : null}
      {emailUnverified ? null : <div className={styles.composerField}>
        <Textarea
          className={styles.composerTextarea}
          // The field stays open while «Спросить снова» counts down: the question may be mended meanwhile (AA2.8, Ч-35).
          disabled={(busy && !steering) || retryUnavailable}
          onKeyDown={handleTaskKeyDown}
          onPaste={handlePaste}
          label={<span className={styles.visuallyHidden}>{steering ? 'Уточнение' : 'Вопрос'}</span>}
          maxLength={8000}
          onChange={(event) => onTaskChange(event.target.value)}
          placeholder={placeholder}
          aria-describedby={describedBy}
          ref={setField}
          rows={1}
          value={taskText}
        />
      </div>}
      {emailUnverified ? null : <div className={styles.composerActions}>
        {active ? (<>
          {/* One action at the field while the turn runs; in the queue it stays and says why (CW2.5). */}
          {steering ? (
            <Button className={styles.steerButton} disabled={retryWaiting} loading={steerSending} size={mobile ? 'touch' : 'md'} type="submit" {...sendVariant}
              disabledReason={steerWaits ? 'Уточнить можно будет, когда помощник начнёт.' : !taskText.trim() ? 'Сначала напишите уточнение.' : null}
              onRefusedPress={() => field.current?.focus()}>
              <Send aria-hidden="true" size={mobile ? 16 : 17} />Уточнить
            </Button>
          ) : null}
        </>) : retryUnavailable ? null : (<>
          {narrow ? null : attachButton?.({ compact: mobile, row: false })}
          {/* An empty field: the button stays and says why (specs/2026-11-10-ux2-patterns-refusals.md PR1.3). */}
          {/* On a phone the word stands under the icon (specs/2026-11-11-ux9-shelf-chat.md SC2.2, П-15): a button of an icon
              alone is only ×, ←, ⋯. */}
          <Button aria-label={submitLabel} caption={mobile} className={mobile ? undefined : styles.sendButton} disabled={busy || retryWaiting} ref={sendButton} size={mobile ? 'touch' : 'md'} type="submit" {...sendVariant}
            disabledReason={busy || retryWaiting ? null : unavailable ?? (!taskText.trim() ? (chapterAttached ? 'Напишите вопрос или выберите действие.' : 'Сначала напишите вопрос.')
              : brackets ? replaceBracketsReason : null)}
            onRefusedPress={() => (brackets ? selectBracket(false, true) : field.current?.focus())}>
            <Send aria-hidden="true" size={mobile ? 16 : 17} />
            {mobile ? (retryWaiting ? formatRetryAfterSeconds(retryAfterSeconds) : recovery ? 'Снова' : 'Спросить') : <span className={styles.actionLabel}>{submitLabel}</span>}
          </Button>
        </>)}
      </div>}
    </form>
  </>);
}
