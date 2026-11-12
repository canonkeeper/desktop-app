import { BookOpen, Play, Square } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import { QuotaRefusal } from '../../../entities/account-quota/ui/QuotaRefusal';
import type { Book, ImportQueue } from '../../../entities/manuscript/api';
import { Button } from '../../../ui/controls/Button';
import { LongWork } from '../../../ui/feedback/LongWork';
import type { Toasts } from '../../../ui/feedback/useToastQueue';
import { Confirm } from '../../../ui/overlays/Confirm';
import { formatCount } from '../../../ui/copy';
import { uploadEndWords } from './upload-files';
import type { UploadController } from './useUpload';
import styles from './SagaState.module.css';

/** The end of an upload the author has not looked at yet: its words and the books it put on the shelf (UP4.1). */
export type UploadEnd = Readonly<{ queueId: string; message: string; detail: string; look: string; bookIds: string[] }>;

const lookedKey = (queueId: string) => `canonkeeper.upload.looked.${queueId}`;
/** The line of the end stands a day: an upload of the week before is history. */
const endLineFor = 24 * 60 * 60 * 1000;

function looked(queueId: string) {
  try {
    return window.localStorage.getItem(lookedKey(queueId)) === '1';
  } catch {
    return false;
  }
}

/**
 * The end of an ended upload: its books once the shelf has every one of them — the books are read a moment after the
 * queue, and a count said before that would be short. A book deleted since leaves the upload unsaid: it is history.
 */
function endOf(queue: ImportQueue, books: readonly Book[], preparing: boolean, waiting: string | null): UploadEnd | null {
  const bookIds = queue.items.filter((item) => item.processingStatus === 'succeeded').map((item) => item.bookId)
    .filter((id): id is string => Boolean(id));
  if (bookIds.length === 0 || bookIds.some((id) => !books.some((book) => book.id === id))) return null;
  const chapters = books.filter((book) => bookIds.includes(book.id)).reduce((sum, book) => sum + book.chapterCount, 0);
  return { queueId: queue.id, bookIds, ...uploadEndWords(bookIds.length, chapters, preparing, waiting) };
}

/**
 * The end of an upload (specs/2026-11-11-ux7-upload.md UP4.1): a line of the one area of toasts when an upload this tab saw
 * going ends, and the same words in the line of the saga until the author looks at how the books were read. Whether the
 * saga is read is the server's (`preparing`), and so is the line of a reading that waits for the helper (`waiting`,
 * specs/2026-11-12-desk-window.md WN3.2).
 */
export function useUploadEnd({ books, latest, onLook, preparing, toasts, waiting = null }: {
  books: readonly Book[];
  latest: ImportQueue | null;
  onLook: (bookIds: string[]) => void;
  preparing: boolean;
  toasts: Toasts;
  waiting?: string | null;
}) {
  const seen = useRef(new Set<string>());
  const told = useRef(new Set<string>());
  const [, setLookedAt] = useState(0);
  const finished = latest?.status === 'succeeded' ? endOf(latest, books, preparing, waiting) : null;
  // The line waits to be looked at for a day by the clock of the server; the toast says an end this tab saw at once.
  const end = finished && latest && !looked(latest.id)
    && Date.now() - Date.parse(latest.completedAt ?? latest.updatedAt) < endLineFor ? finished : null;
  const look = (shown: UploadEnd) => {
    try {
      window.localStorage.setItem(lookedKey(shown.queueId), '1');
    } catch {
      // The line stays on this page; it goes with the next upload.
    }
    setLookedAt(Date.now());
    onLook(shown.bookIds);
  };
  useEffect(() => {
    if (!latest) return;
    if (['uploading', 'draft', 'running'].includes(latest.status)) seen.current.add(latest.id);
    if (latest.status !== 'succeeded' || !seen.current.has(latest.id) || told.current.has(latest.id) || !finished) return;
    told.current.add(latest.id);
    toasts.push({
      message: finished.message, detail: finished.detail, tone: 'success',
      action: <Button onClick={() => look(finished)} size="sm" variant="ghost">{finished.look}</Button>,
    });
    // The toast is said once per upload: `told` keeps the uploads already said.
  }, [latest, finished]);
  return { end, look };
}

/**
 * The line of the saga about the upload (UP3.1, UP3.3; board 35·23–35·26): how far it went and what goes, with «Остановить
 * остальные» and its question; a stopped upload with «Продолжить» while this tab holds its files; a queue that did not start
 * with «Начать»; the end with «Посмотреть, как разобрались». A refusal before or after a request stands under it.
 */
export function UploadLine({ end, onLook, upload }: { end: UploadEnd | null; onLook: (end: UploadEnd) => void; upload: UploadController }) {
  const failure = upload.failure;
  const refusal = failure?.code && failure.words ? (
    <QuotaRefusal announce code={failure.code} place="shelf" words={failure.words} />
  ) : failure ? <p className={styles.refusal} role="alert">{failure.message}</p> : null;
  let line = null;
  if (upload.line) {
    const remaining = upload.line.remaining;
    line = (
      <LongWork
        action={upload.idleDraft ? (
          <Button onClick={upload.startDraft} size="sm" variant="primary"><Play aria-hidden="true" size={14} />Начать</Button>
        ) : remaining > 0 && upload.queue?.status === 'running' ? (
          <Button onClick={upload.askStop} size="sm" variant="outline"><Square aria-hidden="true" size={14} />Остановить остальные</Button>
        ) : null}
        announcement={upload.line.title}
        detail={upload.idleDraft ? 'Файлы дошли, но загрузка не началась. Нажмите «Начать».' : upload.line.detail}
        label="Загрузка книг"
        progress={upload.line.progress}
        title={upload.idleDraft ? 'Загрузка не началась' : upload.line.title}
      />
    );
  } else if (upload.stopped) {
    line = (
      <LongWork
        action={<Button onClick={upload.continueUpload} size="sm" variant="primary"><Play aria-hidden="true" size={14} />Продолжить</Button>}
        announcement="Загрузка остановлена"
        detail={`Ещё ${formatCount(upload.stopped.files.length, 'файл', 'файла', 'файлов')} здесь — загрузку можно продолжить`}
        label="Загрузка книг"
        title={`Загрузка остановлена: добавлено ${upload.stopped.done} из ${upload.stopped.total}`}
        tone="stalled"
      />
    );
  } else if (end) {
    line = (
      <LongWork
        action={<Button onClick={() => onLook(end)} size="sm" variant="ghost"><BookOpen aria-hidden="true" size={14} />{end.look}</Button>}
        announcement={end.message}
        detail={end.detail}
        label="Загрузка книг"
        title={end.message}
        tone="done"
      />
    );
  }
  if (!line && !refusal && !upload.stopAsked) return null;
  const left = upload.line?.remaining ?? 0;
  return (
    <div className={styles.sagaState}>
      {line}
      {refusal}
      {upload.stopAsked ? (
        <Confirm
          action="Не загружать"
          busy={upload.stopping}
          onCancel={upload.keepGoing}
          onConfirm={() => void upload.confirmStop()}
          title={`Не загружать ${left === 1 ? 'оставшуюся книгу' : `оставшиеся ${formatCount(left, 'книгу', 'книги', 'книг')}`}?`}
        >
          Готовые книги останутся в саге.
        </Confirm>
      ) : null}
    </div>
  );
}
