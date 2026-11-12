import { BookCheck, FileCheck2, FilePlus2, FileText, Library, SearchCheck, X } from 'lucide-react';
import { useRef, type ReactNode } from 'react';
import { Button } from '../../ui/controls/Button';
import { IconButton } from '../../ui/controls/IconButton';
import { formatCount } from '../../ui/copy';
import { chapterLabel, chapterSize, chatChapterAccept, signs, type ChatChapterState } from './chat-chapter';
import styles from './ChatChapter.module.css';

/**
 * «Приложить главу» of the question field (specs/2026-11-07-chat-attach-chapter.md CA7.1): the window of the browser for
 * a chapter file; the input is emptied, so the same file may come again.
 */
export function ChatChapterButton({ chapter, compact, disabled, row = false }: { chapter: ChatChapterState; compact: boolean; disabled: boolean; row?: boolean }) {
  const input = useRef<HTMLInputElement>(null);
  return <>
    <input accept={chatChapterAccept} aria-hidden="true" className={styles.fileInput} ref={input} tabIndex={-1} type="file"
      onChange={(event) => {
        const file = event.currentTarget.files?.[0];
        event.currentTarget.value = '';
        if (file) chapter.attachFile(file);
      }} />
    {row ? (
      <Button className={styles.attachRow} disabled={disabled} onClick={() => input.current?.click()} size="touch" variant="ghost">
        <FilePlus2 aria-hidden="true" size={16} />Приложить главу
      </Button>
    ) : (
      // The word stays beside the icon, under it on a phone (specs/2026-11-10-ux2-patterns-refusals.md PR1.4; W-57).
      <Button aria-label="Приложить главу" caption={compact} disabled={disabled} onClick={() => input.current?.click()}
        size={compact ? 'touch' : 'md'} title="Приложить главу: .docx, .txt или .md, до 10 МБ" variant="ghost">
        <FilePlus2 aria-hidden="true" size={17} />{compact ? 'Приложить' : 'Приложить главу'}
      </Button>
    )}
  </>;
}

/** The ready actions of an attached chapter before the question goes (specs/2026-11-11-ux4-after-answer.md AA4.1). */
export interface ChapterQuickActions {
  /** «Сверить с книгой» and «Найти противоречия»: their question goes with the chapter. */
  ask?: { onAsk: (question: string) => void; unavailable?: string | null | undefined } | undefined;
  /** What a question costs, by the rule of «Спросить»: one line under the buttons. */
  price?: string | undefined;
  /** «Вычитать» of the site: the chapter goes to the proofreading of «Письмо», no question of the chat. */
  proofread?: { start: () => void; pending: boolean; problem: ReactNode } | undefined;
}

/** The questions of the actions, as the author would ask them. */
export const chapterQuestions = {
  compare: 'Сверь приложенную главу с книгой: ложится ли она на канон — имена, кто что знает, события, порядок и время?',
  contradictions: 'Найди противоречия приложенной главы с книгой: каждое — место главы, что сказано там и что сказано в книге.',
} as const;

/**
 * The chapter attached to the question, the long paste waiting for the author's answer and why a file was refused (CA7.1,
 * CA7.2): the chapter by its name and size in words, its ready actions, the line of a second file that took its place,
 * and a book sent to «Книги».
 */
export function ChatChapterStrip({ actions, chapter, onOpenBooks }: { actions?: ChapterQuickActions; chapter: ChatChapterState; onOpenBooks?: (() => void) | undefined }) {
  if (!chapter.chapter && chapter.paste === null && !chapter.problem && !chapter.notice) return null;
  const size = chapter.chapter ? chapterSize(chapter.chapter) : '';
  const ask = actions?.ask;
  const reason = ask?.unavailable || null;
  return (
    <div className={styles.strip}>
      {chapter.chapter ? (
        <div aria-label="Приложенная глава" className={styles.attached} role="group">
          <div className={styles.chip}>
            <FileText aria-hidden="true" size={15} />
            <span className={styles.chipLabel}>Глава: {chapterLabel(chapter.chapter)}{size ? ` · ${size}` : ''}</span>
            <IconButton aria-label="Убрать приложение" onClick={chapter.remove} size="sm"><X aria-hidden="true" size={14} /></IconButton>
          </div>
          {ask || actions?.proofread ? (
            <div aria-label="Что сделать с главой" className={styles.quick} role="group">
              {ask ? <>
                <Button disabledReason={reason} onClick={() => ask.onAsk(chapterQuestions.compare)} size="sm" variant="outline">
                  <BookCheck aria-hidden="true" size={15} />Сверить с книгой
                </Button>
                <Button disabledReason={reason} onClick={() => ask.onAsk(chapterQuestions.contradictions)} size="sm" variant="outline">
                  <SearchCheck aria-hidden="true" size={15} />Найти противоречия
                </Button>
              </> : null}
              {actions?.proofread ? (
                <Button data-voice-consent="yes" loading={actions.proofread.pending} onClick={actions.proofread.start} size="sm" variant="outline">
                  <FileCheck2 aria-hidden="true" size={15} />Вычитать
                </Button>
              ) : null}
            </div>
          ) : null}
          {actions?.price ? <p className={styles.price}>{actions.price}</p> : null}
          {actions?.proofread?.problem}
        </div>
      ) : null}
      {chapter.notice ? <p className={styles.notice} role="status">{chapter.notice}</p> : null}
      {chapter.paste !== null ? (
        <div aria-label="Длинная вставка" className={styles.offer} role="group">
          <span>Вставка длиннее вопроса: {formatCount(signs(chapter.paste), 'знак', 'знака', 'знаков')}. Приложить её как главу?</span>
          <span className={styles.offerActions}>
            <Button onClick={chapter.acceptPaste} size="sm" variant="primary">Приложить как главу</Button>
            <Button onClick={chapter.dismissPaste} size="sm" variant="ghost">Не вставлять</Button>
          </span>
        </div>
      ) : null}
      {chapter.problem ? (
        <div className={styles.problemRow}>
          <p className={styles.problem} role="alert">{chapter.problem.text}</p>
          {chapter.problem.books && onOpenBooks ? <Button onClick={onOpenBooks} size="sm" variant="outline"><Library aria-hidden="true" size={15} />Открыть «Книги»</Button> : null}
        </div>
      ) : null}
    </div>
  );
}
