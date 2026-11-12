import { useQueries, useQueryClient } from '@tanstack/react-query';
import { RotateCcw, X } from 'lucide-react';
import { useEffect, useRef } from 'react';
import {
  isBookUpdateJob, manuscriptQueryKeys, type Book, type ManuscriptApiClient, type ProjectJob,
} from '../../../entities/manuscript/api';
import { Button } from '../../../ui/controls/Button';
import { IconButton } from '../../../ui/controls/IconButton';
import { formatCount, pluralize } from '../../../ui/copy';
import { SkeletonArea } from '../../../ui/feedback/Skeleton';
import { useWriteToUsAction } from '../../../ui/feedback/SupportReference';
import type { Toasts } from '../../../ui/feedback/useToastQueue';
import { Dialog } from '../../../ui/overlays/Dialog';
import { manuscriptJobQuery, useManuscriptJobQuery } from '../api';
import { BookUpdatePreview, type BookUpdatePreviewResult } from './BookUpdatePreview';
import { bookUpdateSummary, revertTermLine, updateLine } from './book-revert-words';
import { importFailureMessage } from './import-failure';
import styles from './ShelfLayers.module.css';

type ImportResult = Extract<NonNullable<ProjectJob['result']>, { type: 'import' }>;

const terminal: readonly ProjectJob['status'][] = ['succeeded', 'failed', 'canceled'];

/** The update «Что изменилось» opens: the book and the Job whose result tells it chapter by chapter. */
export interface BookChanges {
  bookId: string;
  projectId: string;
  jobId?: string | undefined;
}

/** «изменена 1 глава, добавлены 2 главы»; nothing — «без изменений». */
export function updateWords(result: ImportResult) {
  const part = (count: number | undefined, one: string, few: string, many: string) => (count ?? 0) > 0
    ? `${pluralize(count!, one, few, many)} ${formatCount(count!, 'глава', 'главы', 'глав')}` : '';
  const parts = [
    part(result.changedChapters, 'изменена', 'изменены', 'изменено'),
    (result.punctuationChapters ?? 0) > 0
      ? `поправлены знаки в ${result.punctuationChapters} ${pluralize(result.punctuationChapters!, 'главе', 'главах', 'главах')}` : '',
    part(result.renamedChapters, 'переименована', 'переименованы', 'переименовано'),
    part(result.addedChapters, 'добавлена', 'добавлены', 'добавлено'),
    part(result.removedChapters, 'удалена', 'удалены', 'удалено'),
  ].filter(Boolean);
  return parts.length ? parts.join(', ') : 'без изменений';
}

/**
 * The line of the result of an update of a book from a file, wherever the author is when it ends (specs/2026-11-11-ux7-trash.md
 * TB4.1): «Книга «…» обновлена: изменена 1 глава · Что изменилось». The shell follows the updates the server says go on in
 * the saga it shows — on any page and on any device of the author — until each ends; the browser keeps nothing of them
 * (specs/2026-11-12-ux10-book-revert.md BR3.4). A failed or stopped update says so.
 */
export function useBookUpdateLines({ api, books, jobs, onChanges, toasts }: {
  api: Pick<ManuscriptApiClient, 'getJob'>;
  books: readonly Book[];
  jobs: readonly ProjectJob[];
  onChanges: (changes: BookChanges) => void;
  toasts: Pick<Toasts, 'push'>;
}) {
  const queryClient = useQueryClient();
  // The updates in work the server named, followed to their end even once the list of work no longer has them; an update
  // told of is not followed again while the list of work, read before its end, still names it.
  const followed = useRef(new Map<string, { projectId: string; bookId: string; title: string }>());
  const told = useRef(new Set<string>());
  for (const job of jobs) {
    if (isBookUpdateJob(job) && !terminal.includes(job.status) && !followed.current.has(job.id) && !told.current.has(job.id)
      && job.subject.type === 'import') {
      const bookId = job.subject.bookId ?? '';
      followed.current.set(job.id, { projectId: job.subject.projectId, bookId, title: books.find((book) => book.id === bookId)?.title ?? '' });
    }
  }
  const running = [...followed.current.entries()];
  // The query of the page that started the update: the page takes its line of the update away in the frame this line
  // comes (specs/2026-11-12-ux12-shell.md SH2.2).
  const updates = useQueries({ queries: running.map(([jobId]) => manuscriptJobQuery(api, jobId, true)) });
  const writeToUs = useWriteToUsAction();
  const ended = updates.map((query, index) => ({ job: query.data, entry: running[index]! }))
    .filter(({ job, entry }) => job && entry && job.id === entry[0] && terminal.includes(job.status));

  useEffect(() => {
    for (const { job, entry } of ended) {
      const [jobId, update] = entry;
      // One line for each update that ended.
      if (!followed.current.delete(jobId)) continue;
      told.current.add(jobId);
      const book = update.title ? `Книга «${update.title}»` : 'Книга';
      if (job!.status === 'succeeded' && job!.result?.type === 'import') {
        toasts.push({
          message: `${book} обновлена: ${updateWords(job!.result)}`,
          tone: 'success',
          persistent: true,
          action: <Button onClick={() => onChanges({ bookId: update.bookId, projectId: update.projectId, jobId })} size="sm" variant="ghost">Что изменилось</Button>,
        });
      } else if (job!.status === 'canceled') {
        toasts.push({ message: `Обновление ${update.title ? `книги «${update.title}»` : 'книги'} остановлено.`, tone: 'neutral' });
      } else {
        // A reason in the file the author fixes there; a failure on our side says how to repeat and gives «Написать нам»
        // with the code (SH2.3): the browser keeps no file once the update went, so the repeat is the file chosen again.
        const reason = importFailureMessage(job!.error?.code);
        toasts.push(reason ? { message: `${book} не обновилась.`, detail: reason, tone: 'error' } : {
          message: `${book} не обновилась.`,
          detail: 'Это сбой на нашей стороне — повторите: «Новая версия из файла…» в меню книги.',
          tone: 'error',
          action: writeToUs({ topic: 'bug', code: job!.error?.requestId, draft: `${book} не обновилась из файла.` }),
        });
      }
      void Promise.all([
        queryClient.invalidateQueries({ queryKey: [...manuscriptQueryKeys.all, 'shelf'] }),
        queryClient.invalidateQueries({ queryKey: manuscriptQueryKeys.indexingStatus(update.projectId), exact: false }),
      ]);
    }
  });
}

/**
 * «Что изменилось» of the last update of a book from a file (TB4.1; specs/2026-11-12-ux10-book-revert.md BR3.1): when it was,
 * the chapters it changed with their places «было → стало», read from the result of its Job, and — for the 7 days of its
 * snapshot — «Вернуть прежнюю версию…» with its term, for an editor.
 */
export function BookUpdateChanges({ api, asking, book, canRevert, jobId, onClose, onRevert }: {
  api: Pick<ManuscriptApiClient, 'getJob'>;
  /** The question of the return is being read. */
  asking: boolean;
  /** The book as the shelf of the shell reads it; null while it is read or when the shell shows another saga. */
  book: Book | null;
  canRevert: boolean;
  jobId: string | undefined;
  onClose: () => void;
  onRevert: () => void;
}) {
  const job = useManuscriptJobQuery(api, jobId ?? '', Boolean(jobId)).data;
  const result = job?.result?.type === 'import' && job.result.preview ? job.result as BookUpdatePreviewResult : null;
  const update = book?.update;
  const title = book ? `Что изменилось в «${book.title}»` : 'Что изменилось в книге';
  return (
    <Dialog label={title} onDismiss={onClose} surface="content" scrimClassName={styles.deleteDialogScrim}>
      <section className={styles.modal}>
        <div aria-hidden="true" className={styles.sheetHandle} />
        <div className={styles.modalHeader}>
          <h2>{title}</h2>
        </div>
        <div className={styles.modalBody}>
          {update ? <p className={styles.modalCopy}>{updateLine(update)}.</p> : null}
          {result ? <BookUpdatePreview done result={result} /> : update ? (
            <p className={styles.modalCopy}>Книга обновлена: {bookUpdateSummary(update)}.</p>
          ) : job || !jobId ? (
            <p className={styles.modalCopy}>Подробностей этого обновления больше нет.</p>
          ) : <SkeletonArea count={3} shape="rows" />}
          {update && canRevert ? <p className={styles.modalCopy}>{revertTermLine(update)}</p> : null}
        </div>
        {/* Board 32·28: «Вернуть прежнюю версию…» first, «Закрыть» after it — under it on a phone. */}
        <div className={styles.modalActions}>
          {update && canRevert ? (
            // The return rewrites the chapters of the book: its question asks the author's «да» (BR2.5).
            <Button loading={asking} onClick={onRevert} variant="outline">
              {asking ? null : <RotateCcw aria-hidden="true" size={16} />}
              Вернуть прежнюю версию…
            </Button>
          ) : null}
          <Button className={styles.cancelAction} onClick={onClose}>Закрыть</Button>
        </div>
        <IconButton aria-label="Закрыть" className={styles.desktopClose} onClick={onClose}>
          <X aria-hidden="true" size={18} />
        </IconButton>
      </section>
    </Dialog>
  );
}
