import { FilePlus2, FileText, X } from 'lucide-react';
import { useRef } from 'react';
import { Button } from '../../ui/controls/Button';
import { IconButton } from '../../ui/controls/IconButton';
import { formatCount } from '../../ui/copy';
import { chatChapterAccept, signs, type ChatChapterState } from './chat-chapter';
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
      <IconButton aria-label="Приложить главу" disabled={disabled} onClick={() => input.current?.click()}
        size={compact ? 'touch' : 'md'} title="Приложить главу: .docx, .txt или .md, до 10 МБ">
        <FilePlus2 aria-hidden="true" size={17} />
      </IconButton>
    )}
  </>;
}

/** The chapter attached to the question, the long paste waiting for the author's answer and why a file was refused (CA7.1, CA7.2). */
export function ChatChapterStrip({ chapter }: { chapter: ChatChapterState }) {
  if (!chapter.chapter && chapter.paste === null && !chapter.problem) return null;
  return (
    <div className={styles.strip}>
      {chapter.chapter ? (
        <div aria-label="Приложенная глава" className={styles.chip} role="group">
          <FileText aria-hidden="true" size={15} />
          <span className={styles.chipLabel}>Глава: {chapter.chapter.label}</span>
          <IconButton aria-label="Убрать приложение" onClick={chapter.remove} size="sm"><X aria-hidden="true" size={14} /></IconButton>
        </div>
      ) : null}
      {chapter.paste !== null ? (
        <div aria-label="Длинная вставка" className={styles.offer} role="group">
          <span>Вставка длиннее вопроса: {formatCount(signs(chapter.paste), 'знак', 'знака', 'знаков')}. Приложить её как главу?</span>
          <span className={styles.offerActions}>
            <Button onClick={chapter.acceptPaste} size="sm" variant="primary">Приложить как главу</Button>
            <Button onClick={chapter.dismissPaste} size="sm" variant="ghost">Не вставлять</Button>
          </span>
        </div>
      ) : null}
      {chapter.problem ? <p className={styles.problem} role="alert">{chapter.problem}</p> : null}
    </div>
  );
}
