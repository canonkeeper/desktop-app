import { useQueryClient } from '@tanstack/react-query';
import { FileUp, RotateCcw, X } from 'lucide-react';
import { type ChangeEvent, useEffect, useRef, useState } from 'react';
import { invalidateAccountQuota, quotaFailureCode, useAccountQuota } from '../../../entities/account-quota/api';
import { isApiStatusFailure, isTransientApiFailure, publicApiErrorMessage } from '../../../entities/api-errors';
import type { ContactRequest } from '../../../shared/session/account-session';
import { manuscriptQueryKeys, type Book, type ManuscriptApiClient, type ProjectJob } from '../../../entities/manuscript/api';
import { Button } from '../../../ui/controls/Button';
import { IconButton } from '../../../ui/controls/IconButton';
import { formatCount } from '../../../ui/copy';
import { Alert } from '../../../ui/feedback/Alert';
import { WriteToUs } from '../../../ui/feedback/SupportReference';
import { Dialog } from '../../../ui/overlays/Dialog';
import { usePresence, type PresenceStatus } from '../../../ui/overlays/use-presence';
import { useCancelJobMutation, useImportConstraintsQuery, useManuscriptJobQuery, useUpdateBookFromFileMutation } from '../api';
import { BookUpdatePreview, previewHasChanges, type BookUpdatePreviewResult } from './BookUpdatePreview';
import previewStyles from './BookUpdatePreview.module.css';
import { importFailureMessage } from './import-failure';
import { localFiles, screenFiles } from './upload-files';
import { sourceQuotaMessage, uploadsQuotaHint } from './quota-messages';
import styles from './ShelfLayers.module.css';

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
  const [started, setStarted] = useState<{ jobId: string; title: string; fileName: string } | null>(null);
  const preview = useUpdateBookFromFileMutation(api);
  const update = useUpdateBookFromFileMutation(api);
  const cancel = useCancelJobMutation(api);
  // A book update passes the same file check as every import entry (UC3.1).
  const constraints = useImportConstraintsQuery(api, projectId ?? '', canUpdate).data;
  const previewJob = useManuscriptJobQuery(api, previewJobId, Boolean(previewJobId), true).data;
  const job = useManuscriptJobQuery(api, started?.jobId ?? '', Boolean(started), true).data;
  const dialog = usePresence(picked);
  const quota = useAccountQuota(api, Boolean(projectId)).data;
  // The line of the update in work stands until the answer of its Job says it ended — the answer the line of its result
  // reads too, so the result takes its place and never stands beside it (specs/2026-11-12-ux12-shell.md SH2.2).
  const ended = Boolean(started && job?.id === started.jobId && finished.includes(job.status));
  const working = started && !ended ? `Обновляем «${started.title}» из файла ${started.fileName}.` : null;

  // The line of the result is the shell's, so it comes on any page (specs/2026-11-11-ux7-trash.md TB4.1): here the
  // update only ends.
  useEffect(() => {
    if (!started || job?.id !== started.jobId || !finished.includes(job.status)) return;
    setStarted(null);
    if (projectId) {
      void Promise.all([
        invalidateAccountQuota(queryClient),
        queryClient.invalidateQueries({ queryKey: manuscriptQueryKeys.shelf({ projectId }), exact: true }),
        queryClient.invalidateQueries({ queryKey: manuscriptQueryKeys.indexingStatus(projectId), exact: false }),
      ]);
    }
  }, [job, projectId, queryClient, started]);

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
    readFile(book, next);
  }

  /** The window «Что изменится» reads a new file of the book (UC2); a file the server would refuse is not sent (UC3.1). */
  function readFile(target: Book | null, next: File | null) {
    const rejected = next && constraints ? screenFiles(localFiles([next]), constraints).refused[0] : undefined;
    if (next && constraints && !rejected && target && projectId) {
      setBook(target);
      setPicked({ book: target, file: next });
      setPreviewJobId('');
      preview.mutate({ bookId: target.id, file: next, preview: true, projectId }, {
        onSuccess: (response) => setPreviewJobId(response.job.id),
      });
      return;
    }
    if (next) onNotice(rejected?.reason ?? 'Не удалось проверить файл. Повторите позже.');
    setBook(null);
  }

  function confirm() {
    if (!picked || !projectId) return;
    const { book: target, file } = picked;
    update.mutate({ bookId: target.id, file, preview: false, projectId }, {
      onSuccess: (response) => {
        setStarted({ jobId: response.job.id, title: target.title, fileName: file.name });
        // The line of the update in work takes the place of a former notice.
        onNotice('');
        setBook(null);
        setPicked(null);
        setPreviewJobId('');
        preview.reset();
      },
    });
  }

  const shownPreviewJob = previewJob?.id === previewJobId ? previewJob : undefined;
  // A refusal by a limit says it in the words of the one table, with «Написать нам» on the topic of the tier (PR2.3).
  const previewLimit = Boolean(preview.error && sourceQuotaMessage(quotaFailureCode(preview.error), quota))
    || Boolean(!preview.error && shownPreviewJob?.status === 'failed' && !importFailureMessage(shownPreviewJob.error?.code) && sourceQuotaMessage(shownPreviewJob.error?.code, quota));
  const updateLimit = Boolean(update.error && sourceQuotaMessage(quotaFailureCode(update.error), quota));
  const previewEnded = Boolean(shownPreviewJob && (shownPreviewJob.status === 'failed' || shownPreviewJob.status === 'canceled'));
  const previewError = preview.error
    ? sourceQuotaMessage(quotaFailureCode(preview.error), quota) ?? publicApiErrorMessage(preview.error, 'Не удалось прочитать файл.')
    : previewEnded
      ? importFailureMessage(shownPreviewJob!.error?.code) ?? sourceQuotaMessage(shownPreviewJob!.error?.code, quota)
        ?? 'Это сбой на нашей стороне. Повторите, а если не поможет — напишите нам.'
      : '';
  // A failure on our side — neither of the file nor of a limit — is repeated with the same file, and «Написать нам» stands
  // beside «Повторить» with its code (SH2.3).
  const previewOurs = preview.error
    ? isTransientApiFailure(preview.error)
    : previewEnded && !importFailureMessage(shownPreviewJob!.error?.code) && !sourceQuotaMessage(shownPreviewJob!.error?.code, quota);
  const previewCode = preview.error ? (isApiStatusFailure(preview.error) ? preview.error.problem.requestId : undefined) : shownPreviewJob?.error?.requestId;
  const retry: ContactRequest | null = previewOurs && picked ? {
    topic: 'bug', ...(previewCode ? { code: previewCode } : {}), draft: `Не удалось прочитать файл ${picked.file.name} для книги «${picked.book.title}».`,
  } : null;
  const result = shownPreviewJob?.status === 'succeeded' && shownPreviewJob.result?.type === 'import' && shownPreviewJob.result.preview
    ? shownPreviewJob.result as BookUpdatePreviewResult
    : null;

  return {
    busy: update.isPending || Boolean(started) || Boolean(picked),
    /** The line of the update in work for the place of the notices of the shelf; none once its Job ended. */
    working,
    choose(target: Book) {
      setBook(target);
      setPicked(null);
      inputRef.current?.click();
    },
    /** «Заменить» of the question of a file like a book of the shelf (specs/2026-11-11-ux7-upload.md UP2.2): its file at once. */
    read(target: Book, file: File) {
      readFile(target, file);
    },
    element: <>
      <input aria-hidden="true" hidden onChange={pick} ref={inputRef} tabIndex={-1} type="file" />
      {dialog.mounted && dialog.value ? (
        <BookFileUpdateWindow
          book={dialog.value.book}
          error={previewError}
          errorLimit={previewLimit}
          file={dialog.value.file}
          hint={uploadsQuotaHint(quota)}
          onClose={close}
          onConfirm={confirm}
          onRetry={() => readFile(dialog.value!.book, dialog.value!.file)}
          retry={retry}
          pending={update.isPending}
          result={result}
          state={dialog.status}
          updateError={update.error
            ? sourceQuotaMessage(quotaFailureCode(update.error), quota) ?? publicApiErrorMessage(update.error, 'Не удалось начать обновление книги.')
            : ''}
          updateLimit={updateLimit}
        />
      ) : null}
    </>,
  };
}

/** The window of R6.5: reading the file, what would change, or that nothing would. */
function BookFileUpdateWindow({ book, error, errorLimit, file, hint, onClose, onConfirm, onRetry, pending, result, retry, state, updateError, updateLimit }: {
  book: Book;
  error: string;
  errorLimit: boolean;
  updateLimit: boolean;
  file: File;
  hint: string;
  onClose: () => void;
  onConfirm: () => void;
  onRetry: () => void;
  pending: boolean;
  result: BookUpdatePreviewResult | null;
  /** A failure on our side: «Повторить» reads the same file again, «Написать нам» goes with what it prepared. */
  retry: ContactRequest | null;
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
          {error && retry ? (
            <Alert className={previewStyles.failure} tone="error">
              <p>{error}</p>
              <div className={previewStyles.failureActions}>
                <Button onClick={onRetry} size="sm" variant="outline"><RotateCcw aria-hidden="true" size={15} />Повторить</Button>
                <WriteToUs {...retry} />
              </div>
            </Alert>
          ) : error ? <Alert tone="error">{error}{errorLimit ? <> <WriteToUs draft={error} look="link" topic="tariff" /></> : null}</Alert> : null}
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
            {updateError ? <Alert tone="error">{updateError}{updateLimit ? <> <WriteToUs draft={updateError} look="link" topic="tariff" /></> : null}</Alert> : null}
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
              Обновить книгу
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
