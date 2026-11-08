import { useQueryClient } from '@tanstack/react-query';
import { FileUp, X } from 'lucide-react';
import { type ChangeEvent, useEffect, useRef, useState } from 'react';
import { invalidateAccountQuota, quotaFailureCode, useAccountQuota } from '../../../entities/account-quota/api';
import { publicApiErrorMessage } from '../../../entities/api-errors';
import { manuscriptQueryKeys, type Book, type ManuscriptApiClient, type ProjectJob } from '../../../entities/manuscript/api';
import { Button } from '../../../ui/controls/Button';
import { IconButton } from '../../../ui/controls/IconButton';
import { formatCount } from '../../../ui/copy';
import { Alert } from '../../../ui/feedback/Alert';
import { Dialog } from '../../../ui/overlays/Dialog';
import { usePresence, type PresenceStatus } from '../../../ui/overlays/use-presence';
import { useCancelJobMutation, useImportConstraintsQuery, useManuscriptJobQuery, useUpdateBookFromFileMutation } from '../api';
import { BookUpdatePreview, previewHasChanges, type BookUpdatePreviewResult } from './BookUpdatePreview';
import previewStyles from './BookUpdatePreview.module.css';
import { importFailureMessage } from './import-failure';
import { screenImportFiles } from './import-queue';
import { sourceQuotaMessage, uploadsQuotaHint } from './quota-messages';
import styles from './ShelfLayers.module.css';

type ImportResult = Extract<NonNullable<ProjectJob['result']>, { type: 'import' }>;

const finished: readonly ProjectJob['status'][] = ['succeeded', 'failed', 'canceled'];

/**
 * «Обновить из файла» in the book menu: the author picks a new file of the same volume, the import reads it in the
 * preview mode and the window names what would change; «Обновить книгу» sends the same file to rewrite the book in
 * place (specs/2026-10-27-update-book-from-file.md, UC2, UC6). The browser keeps the file until the author decides.
 */
export function useBookFileUpdate({ api, canUpdate, onNotice, projectId }: {
  api: Pick<ManuscriptApiClient, 'cancelJob' | 'getAccountQuota' | 'getImportConstraints' | 'getJob' | 'importBookFile'>;
  canUpdate: boolean;
  onNotice: (message: string) => void;
  projectId: string | null;
}) {
  const queryClient = useQueryClient();
  const inputRef = useRef<HTMLInputElement>(null);
  const [book, setBook] = useState<Book | null>(null);
  const [picked, setPicked] = useState<{ book: Book; file: File } | null>(null);
  const [previewJobId, setPreviewJobId] = useState('');
  const [started, setStarted] = useState<{ jobId: string; title: string } | null>(null);
  const preview = useUpdateBookFromFileMutation(api);
  const update = useUpdateBookFromFileMutation(api);
  const cancel = useCancelJobMutation(api);
  // A book update passes the same file check as every import entry (UC3.1).
  const constraints = useImportConstraintsQuery(api, projectId ?? '', canUpdate).data;
  const previewJob = useManuscriptJobQuery(api, previewJobId, Boolean(previewJobId), true).data;
  const job = useManuscriptJobQuery(api, started?.jobId ?? '', Boolean(started), true).data;
  const dialog = usePresence(picked);
  const quota = useAccountQuota(api, Boolean(projectId)).data;

  useEffect(() => {
    if (!started || job?.id !== started.jobId || !finished.includes(job.status)) return;
    setStarted(null);
    onNotice(job.status === 'succeeded' && job.result?.type === 'import'
      ? updateSummary(started.title, job.result)
      : job.status === 'canceled' ? 'Обновление книги отменено.' : failureNotice(job.error?.code, sourceQuotaMessage(job.error?.code, quota)));
    if (projectId) {
      void Promise.all([
        invalidateAccountQuota(queryClient),
        queryClient.invalidateQueries({ queryKey: manuscriptQueryKeys.shelf({ projectId }), exact: true }),
        queryClient.invalidateQueries({ queryKey: manuscriptQueryKeys.indexingStatus(projectId), exact: false }),
      ]);
    }
  }, [job, onNotice, projectId, queryClient, quota, started]);

  function close() {
    if (update.isPending) return;
    // A preview still reading the file is stopped: it writes nothing, and its file goes at once.
    if (previewJob && previewJob.id === previewJobId && previewJob.canCancel && !finished.includes(previewJob.status)) {
      cancel.mutate(previewJob.id);
    }
    setBook(null);
    setPicked(null);
    setPreviewJobId('');
    preview.reset();
    update.reset();
  }

  function pick(event: ChangeEvent<HTMLInputElement>) {
    const next = event.target.files?.[0] ?? null;
    event.target.value = '';
    // UC3.1: a file the server would refuse is not sent; the author gets its reason at once.
    const rejected = next && constraints ? screenImportFiles([next], constraints).rejected[0] : undefined;
    if (next && constraints && !rejected && book && projectId) {
      setPicked({ book, file: next });
      setPreviewJobId('');
      preview.mutate({ bookId: book.id, file: next, preview: true, projectId }, {
        onSuccess: (response) => setPreviewJobId(response.job.id),
      });
      return;
    }
    if (next) onNotice(rejected?.message ?? 'Не удалось проверить файл. Повторите позже.');
    setBook(null);
  }

  function confirm() {
    if (!picked || !projectId) return;
    const { book: target, file } = picked;
    update.mutate({ bookId: target.id, file, preview: false, projectId }, {
      onSuccess: (response) => {
        setStarted({ jobId: response.job.id, title: target.title });
        onNotice(`Обновляем «${target.title}» из файла ${file.name}.`);
        setBook(null);
        setPicked(null);
        setPreviewJobId('');
        preview.reset();
      },
    });
  }

  const shownPreviewJob = previewJob?.id === previewJobId ? previewJob : undefined;
  const previewError = preview.error
    ? sourceQuotaMessage(quotaFailureCode(preview.error), quota) ?? publicApiErrorMessage(preview.error, 'Не удалось прочитать файл.')
    : shownPreviewJob && (shownPreviewJob.status === 'failed' || shownPreviewJob.status === 'canceled')
      ? importFailureMessage(shownPreviewJob.error?.code) ?? sourceQuotaMessage(shownPreviewJob.error?.code, quota)
        ?? 'Повторите — если не получится, напишите в поддержку.'
      : '';
  const result = shownPreviewJob?.status === 'succeeded' && shownPreviewJob.result?.type === 'import' && shownPreviewJob.result.preview
    ? shownPreviewJob.result as BookUpdatePreviewResult
    : null;

  return {
    busy: update.isPending || Boolean(started) || Boolean(picked),
    choose(target: Book) {
      setBook(target);
      setPicked(null);
      inputRef.current?.click();
    },
    element: <>
      <input accept=".fb2,.epub,.docx" aria-hidden="true" hidden onChange={pick} ref={inputRef} tabIndex={-1} type="file" />
      {dialog.mounted && dialog.value ? (
        <BookFileUpdateWindow
          book={dialog.value.book}
          error={previewError}
          file={dialog.value.file}
          hint={uploadsQuotaHint(quota)}
          onClose={close}
          onConfirm={confirm}
          pending={update.isPending}
          result={result}
          state={dialog.status}
          updateError={update.error
            ? sourceQuotaMessage(quotaFailureCode(update.error), quota) ?? publicApiErrorMessage(update.error, 'Не удалось начать обновление книги.')
            : ''}
        />
      ) : null}
    </>,
  };
}

function failureNotice(code: string | undefined, quotaReason: string | undefined) {
  const reason = importFailureMessage(code) ?? quotaReason ?? 'Повторите — если не получится, напишите в поддержку.';
  return `Не удалось обновить книгу из файла. ${reason}`;
}

function updateSummary(title: string, result: ImportResult) {
  const counts: Array<[string, number | undefined]> = [
    ['изменено', result.changedChapters],
    ['только знаки', result.punctuationChapters],
    ['переименовано', result.renamedChapters],
    ['добавлено', result.addedChapters],
    ['удалено', result.removedChapters],
    ['без изменений', result.unchangedChapters],
  ];
  const parts = counts.filter(([, count]) => (count ?? 0) > 0).map(([label, count]) => `${label} ${count}`);
  return `Книга «${title}» обновлена${parts.length ? `: глав ${parts.join(', ')}` : ''}.`;
}

/** The window of R6.5: reading the file, what would change, or that nothing would. */
function BookFileUpdateWindow({ book, error, file, hint, onClose, onConfirm, pending, result, state, updateError }: {
  book: Book;
  error: string;
  file: File;
  hint: string;
  onClose: () => void;
  onConfirm: () => void;
  pending: boolean;
  result: BookUpdatePreviewResult | null;
  state: PresenceStatus;
  updateError: string;
}) {
  const changes = result ? previewHasChanges(result) : false;
  const title = error
    ? 'Не удалось прочитать файл'
    : !result ? 'Читаем файл…' : changes ? `Что изменится в «${book.title}»` : 'В файле нет изменений';
  return (
    <Dialog surface="content" label="Обновить книгу из файла" onDismiss={pending ? () => undefined : onClose} scrimClassName={styles.deleteDialogScrim} state={state}>
      <section className={`${styles.modal} ${result && changes ? previewStyles.previewModal : ''}`}>
        <div aria-hidden="true" className={styles.sheetHandle} />
        <div className={styles.modalHeader}>
          <h2>{title}</h2>
        </div>
        <div className={styles.modalBody}>
          {error ? <Alert tone="error">{error}</Alert> : null}
          {!error && !result ? (
            <p className={styles.modalCopy} role="status">«{book.title}» не меняется, пока вы не нажмёте «Обновить книгу».</p>
          ) : null}
          {!error && result && !changes ? (
            <p className={styles.modalCopy} role="status">Текст «{book.title}» совпадает с файлом {file.name}. Обновлять нечего.</p>
          ) : null}
          {!error && result && changes ? <>
            <p className={styles.modalCopy}>Файл {file.name}: {formatCount(result.chapterCount, 'глава', 'главы', 'глав')}.</p>
            <BookUpdatePreview result={result} />
            {hint && !updateError ? <p className={styles.modalCopy} role="status">{hint}</p> : null}
            {updateError ? <Alert tone="error">{updateError}</Alert> : null}
          </> : null}
        </div>
        <div className={styles.modalActions}>
          {result && changes && !error ? <>
            <Button className={styles.cancelAction} disabled={pending} onClick={onClose}>
              Отмена
            </Button>
            {/* The update rewrites the book and may take the threads away: the voice presses it after the author's «да» (R6.8). */}
            <Button data-voice-consent="yes" loading={pending} onClick={onConfirm} variant="primary">
              <FileUp aria-hidden="true" className={styles.mobileOnly} size={16} />
              {pending ? 'Загружаем...' : 'Обновить книгу'}
            </Button>
          </> : (
            <Button className={styles.cancelAction} onClick={onClose}>
              {error || result ? 'Закрыть' : 'Отмена'}
            </Button>
          )}
        </div>
        {/* «Закрыть» of an error or of a file without changes is the button of the window; one name, one button. */}
        {!error && (!result || changes) ? (
          <IconButton aria-label="Закрыть" className={styles.desktopClose} disabled={pending} onClick={onClose}>
            <X aria-hidden="true" size={18} />
          </IconButton>
        ) : null}
      </section>
    </Dialog>
  );
}
