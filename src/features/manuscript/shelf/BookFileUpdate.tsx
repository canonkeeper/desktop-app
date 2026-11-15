import { useMutation, useQueryClient } from '@tanstack/react-query';
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
import { bookUpdateStarted, useCancelJobMutation, useImportConstraintsQuery, useManuscriptJobQuery, useUpdateBookFromFileMutation } from '../api';
import type { BookUpdateSource, BookUpdateStart } from './book-update-source';
import { BookUpdatePreview, previewHasChanges, type BookUpdatePreviewResult } from './BookUpdatePreview';
import previewStyles from './BookUpdatePreview.module.css';
import { importFailureMessage } from './import-failure';
import { localFiles, screenFiles } from './upload-files';
import { sourceQuotaMessage, uploadsQuotaHint } from './quota-messages';
import styles from './ShelfLayers.module.css';

const finished: readonly ProjectJob['status'][] = ['succeeded', 'failed', 'canceled'];

/** A reading of a source: the book, its key, and the answer of its start with the key of its update. */
interface SourceReading {
  id: number;
  book: Book;
  // The window is any source's: the owner only carries what the source answered back to it.
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  source: BookUpdateSource<any>;
  key: string;
  answer: BookUpdateStart | null;
  applyKey: string;
}

let readings = 0;

/** The end of a start of a reading the window left: nothing to say, nothing to repeat. */
class LeftReading extends Error {
  constructor() {
    super('The window left this reading.');
  }
}

/**
 * «Обновить из файла» in the book menu: the author picks a new file of the same volume, the import reads it in the
 * preview mode and the window names what would change; «Обновить книгу» sends the same file to rewrite the book in
 * place (specs/2026-10-27-update-book-from-file.md, UC2, UC6). The browser keeps the file until the author decides. The same
 * owner reads the text of a book from another source of the edition (`open`, specs/2026-11-15-desk-obsidian-book-web.md OU2.1):
 * one cycle of the Job of the preview, its stop and the update; a Job of a preview that came after its window closed is stopped.
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
  const [sourced, setSourced] = useState<SourceReading | null>(null);
  // The reading the window shows now: a start that answers another one is late.
  const current = useRef<SourceReading | null>(null);
  current.current = sourced;
  // The page is drawn: a start of a source that answers after the author left the page is late too (OU2.1).
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => { mounted.current = false; };
  }, []);
  const [previewJobId, setPreviewJobId] = useState('');
  // The Job of the preview the window shows, known with its answer: «Отмена» stops it before the window is drawn again.
  const previewJobRef = useRef('');
  const [started, setStarted] = useState<{ jobId: string; line: string } | null>(null);
  const preview = useUpdateBookFromFileMutation(api);
  const update = useUpdateBookFromFileMutation(api);
  const cancel = useCancelJobMutation(api);
  /** The window no longer shows the reading: it closed, read anew, or the author left the page. */
  const left = (reading: SourceReading) => !mounted.current || current.current?.id !== reading.id;
  const sourcePreview = useMutation({
    // A reading the window left asks the server nothing more: its repeat would read the folder in vain beside the reading the
    // window shows, which then waits for it (OU2.2). The rule of the repeats is so the one of the reading itself.
    mutationFn: async (reading: SourceReading) => {
      if (left(reading)) throw new LeftReading();
      try {
        return await reading.source.preview(reading.book, reading.key);
      } catch (error) {
        throw left(reading) ? new LeftReading() : error;
      }
    },
    retry: (failures, error) => !(error instanceof LeftReading) && (current.current?.source.repeat.retry(failures, error) ?? false),
    retryDelay: (failures, error) => current.current?.source.repeat.retryDelay(failures, error) ?? 0,
    onSuccess: (answer: BookUpdateStart, reading) => {
      void bookUpdateStarted(queryClient, answer.job, { preview: true, projectId: reading.book.projectId });
      // OU2.1: «Отмена» before the answer closed the window, or the author left the page — the Job of the preview that came
      // after it is stopped.
      if (left(reading)) {
        if (answer.job.canCancel && !finished.includes(answer.job.status)) cancel.mutate(answer.job.id);
        return;
      }
      setSourced({ ...reading, answer, applyKey: reading.source.key() });
      showPreviewJob(answer.job.id);
    },
    onSettled: () => invalidateAccountQuota(queryClient),
  });
  const sourceUpdate = useMutation({
    mutationKey: sourced?.source.updateKey,
    // The update waits in the cache of the mutations while the window of the app is open: its line knows its source by it.
    gcTime: Infinity,
    mutationFn: ({ reading, confirmUnread }: { reading: SourceReading; confirmUnread: boolean; projectId: string; bookId: string }) => (
      reading.source.apply(reading.book, reading.answer!, { key: reading.applyKey, confirmUnread })),
    retry: (failures, error) => current.current?.source.repeat.retry(failures, error) ?? false,
    retryDelay: (failures, error) => current.current?.source.repeat.retryDelay(failures, error) ?? 0,
    onSuccess: (response, { projectId: saga }) => bookUpdateStarted(queryClient, response.job, { preview: false, projectId: saga }),
    onSettled: () => invalidateAccountQuota(queryClient),
  });
  // A book update passes the same file check as every import entry (UC3.1).
  const constraints = useImportConstraintsQuery(api, projectId ?? '', canUpdate).data;
  const previewJob = useManuscriptJobQuery(api, previewJobId, Boolean(previewJobId), true).data;
  const job = useManuscriptJobQuery(api, started?.jobId ?? '', Boolean(started), true).data;
  const dialog = usePresence(picked);
  const sourceDialog = usePresence(sourced);
  const quota = useAccountQuota(api, Boolean(projectId)).data;
  // The line of the update in work stands until the answer of its Job says it ended — the answer the line of its result
  // reads too, so the result takes its place and never stands beside it (specs/2026-11-12-ux12-shell.md SH2.2).
  const ended = Boolean(started && job?.id === started.jobId && finished.includes(job.status));
  const working = started && !ended ? started.line : null;

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

  function showPreviewJob(id: string) {
    previewJobRef.current = id;
    setPreviewJobId(id);
  }

  /** A preview still reading is stopped: it writes nothing. Its Job as the cache knows it now, not as the window was drawn. */
  function stopPreview() {
    const id = previewJobRef.current;
    const shown = id ? queryClient.getQueryData<ProjectJob>(manuscriptQueryKeys.job(id)) : undefined;
    if (shown?.canCancel && !finished.includes(shown.status)) cancel.mutate(shown.id);
  }

  function close() {
    if (update.isPending || sourceUpdate.isPending) return;
    // A preview still reading the file is stopped, and its file goes at once.
    stopPreview();
    setBook(null);
    setPicked(null);
    setSourced(null);
    current.current = null;
    showPreviewJob('');
    preview.reset();
    update.reset();
    sourcePreview.reset();
    sourceUpdate.reset();
  }

  /** A new reading of a source: a new key; a book whose text has nowhere to come from opens the window without one (OU2.3). */
  function readFrom(target: Book, source: SourceReading['source']) {
    stopPreview();
    const reading: SourceReading = { id: (readings += 1), book: target, source, key: source.key(), answer: null, applyKey: '' };
    current.current = reading;
    setSourced(reading);
    showPreviewJob('');
    sourcePreview.reset();
    sourceUpdate.reset();
    if (source.reads(target)) sourcePreview.mutate(reading);
  }

  function applyFrom(confirmUnread: boolean) {
    const reading = current.current;
    if (!reading?.answer || !projectId) return;
    sourceUpdate.mutate({ reading, confirmUnread, projectId, bookId: reading.book.id }, {
      onSuccess: (response) => {
        reading.source.started();
        setStarted({ jobId: response.job.id, line: reading.source.working(reading.book.title) });
        // The line of the update in work takes the place of a former notice.
        onNotice('');
        setSourced(null);
        current.current = null;
        showPreviewJob('');
        sourcePreview.reset();
      },
    });
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
      showPreviewJob('');
      preview.mutate({ bookId: target.id, file: next, preview: true, projectId }, {
        onSuccess: (response) => showPreviewJob(response.job.id),
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
        setStarted({ jobId: response.job.id, line: `Обновляем «${target.title}» из файла ${file.name}.` });
        // The line of the update in work takes the place of a former notice.
        onNotice('');
        setBook(null);
        setPicked(null);
        showPreviewJob('');
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

  const shownSourceJob = sourced?.answer && previewJob?.id === sourced.answer.job.id ? previewJob : undefined;
  const sourceResult = shownSourceJob?.status === 'succeeded' && shownSourceJob.result?.type === 'import' && shownSourceJob.result.preview
    ? shownSourceJob.result as BookUpdatePreviewResult
    : null;
  const sourceFailure = sourcePreview.error && !sourcePreview.isPending ? { error: sourcePreview.error }
    : shownSourceJob && (shownSourceJob.status === 'failed' || shownSourceJob.status === 'canceled') ? { job: shownSourceJob } : null;
  const SourceWindow = sourceDialog.value?.source.Window;

  return {
    busy: update.isPending || sourceUpdate.isPending || Boolean(started) || Boolean(picked) || Boolean(sourced),
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
    /** The window of an update of the book from another source of the edition: a new reading (OU2.1, OU2.2). */
    open<Answer extends BookUpdateStart>(target: Book, source: BookUpdateSource<Answer>) {
      readFrom(target, source);
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
      {sourceDialog.mounted && sourceDialog.value && SourceWindow ? (
        <SourceWindow
          answer={sourced?.answer ?? null}
          applyError={sourceUpdate.error}
          applyWaits={{ count: sourceUpdate.failureCount, reason: sourceUpdate.failureReason }}
          applying={sourceUpdate.isPending}
          book={sourceDialog.value.book}
          failure={sourced ? sourceFailure : null}
          onApply={applyFrom}
          onClose={close}
          onReread={() => { if (sourced) readFrom(sourced.book, sourced.source); }}
          onRetry={() => { if (sourced) sourcePreview.mutate(sourced); }}
          reading={Boolean(sourced) && sourced?.source.reads(sourced.book) === true && !sourceFailure && !sourceResult}
          result={sourceResult}
          source={sourceDialog.value.source}
          state={sourceDialog.status}
          waits={{ count: sourcePreview.failureCount, reason: sourcePreview.failureReason }}
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
