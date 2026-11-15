// SPDX-License-Identifier: AGPL-3.0-only
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Folder, FolderSync, Link2, RotateCw, Unlink, X } from 'lucide-react';
import { useEffect, useState } from 'react';
import { loadFailureWords } from '../../../entities/api-errors';
import { manuscriptQueryKeys } from '../../../entities/manuscript/api';
import type { Book } from '../../../entities/manuscript/api';
import type { BookUpdateSource, BookUpdateWindowProps } from '../../../features/manuscript/shelf/book-update-source';
import { BookUpdatePreview, previewHasChanges, type PreviewSourceWords } from '../../../features/manuscript/shelf/BookUpdatePreview';
import { Button } from '../../../ui/controls/Button';
import { IconButton } from '../../../ui/controls/IconButton';
import { Alert } from '../../../ui/feedback/Alert';
import { ErrorBlock } from '../../../ui/feedback/ErrorBlock';
import { useToasts } from '../../../ui/feedback/useToastQueue';
import { Dialog } from '../../../ui/overlays/Dialog';
import { warningsOf } from './BookCard';
import { Skipped, WorkLine } from './lines';
import { markBooksChanged, markTreeOld, obsidianKeys, problemCode, useObsidianVault, type ObsidianBookUpdate, type ObsidianClient } from './obsidian-api';
import { unreadOf, updateRefusalOf, type UpdateExit, type UpdateRefusal } from './refusals';
import { droppedWords, refusalReason, updateWords, words } from './words';
import styles from './Obsidian.module.css';

/** «Обновить из Obsidian» as a source of the owner of the window of an update (specs/2026-11-15-desk-obsidian-book-web.md OU2.1). */
export interface ObsidianUpdateSource extends BookUpdateSource<ObsidianBookUpdate> {
  api: ObsidianClient;
  projectId: string;
  /** The limit of a book file of the server: the words of `import.file_too_large` name it. */
  bookLimitBytes: number | null;
  /** «Другая папка…», «Связать с папкой Obsidian…»: the window of the link of the book. */
  link: (book: Book) => void;
  /** «Открыть «Из Obsidian…»»: the page of the bridge of the saga. */
  openBridge: () => void;
}

/** The words of «Что изменится» of the notes of a folder (Data «план»): the notes of the author to the book are «Ваши заметки». */
const fromNotes: PreviewSourceWords = {
  kept: updateWords.notesKept,
  leaving: () => updateWords.notesLeaving,
  wrong: updateWords.wrong,
  text: updateWords.text,
};

/**
 * «Обновить книгу из Obsidian» (specs/2026-11-15-desk-obsidian-book-web.md OU2; board 44·56, `Obsidian Update Window`): the
 * window of the owner of «Новая версия из файла…» for the folder of a book. Its head is the folder of the book with «Другая
 * папка…» and «Снять связь»; the reading goes in two phases under one line; then «Что изменится» in the words of the notes with
 * the report of the folder under it, or that nothing changes, or why the notes do not give the book now; notes that do not read
 * now — «Прочитать заново» first, «Обновить без этих заметок» second; «Обновить книгу» reads the folder again before its Job.
 */
export function UpdateWindow({
  answer, applyError, applyWaits, applying, book, failure, onApply, onClose, onReread, onRetry, reading, result, source, state, waits,
}: BookUpdateWindowProps<ObsidianBookUpdate>) {
  const own = source as ObsidianUpdateSource;
  const client = useQueryClient();
  const toasts = useToasts();
  const vault = useObsidianVault(own.api, own.projectId).data;
  const held = vault?.books?.find((one) => one.bookId === book.id);
  const report = answer?.book ?? null;
  const folder = report?.folder ?? held?.folder ?? '';
  // What the author asked last: «Обновить книгу» or «Обновить без этих заметок» — a lost connection repeats it.
  const [confirmUnread, setConfirmUnread] = useState(false);
  const apply = (confirm: boolean) => {
    setConfirmUnread(confirm);
    onApply(confirm);
  };

  const refusal: UpdateRefusal | null = failure
    ? 'error' in failure
      ? updateRefusalOf(failure.error, problemCode(failure.error), own.bookLimitBytes)
      : updateRefusalOf(failure.job.error, failure.job.error?.code, own.bookLimitBytes)
    : applyError && !applying ? updateRefusalOf(applyError, problemCode(applyError), own.bookLimitBytes) : null;
  // A refusal that names the folder of the saga or of the book anew: it is read again; one of a live load — the loads of «Книги».
  const reread = Boolean(refusal?.reread);
  const queues = Boolean(refusal?.queues);
  useEffect(() => {
    if (reread) void client.invalidateQueries({ queryKey: obsidianKeys.vault(own.projectId) });
    if (queues) void client.invalidateQueries({ queryKey: manuscriptQueryKeys.importQueues(own.projectId) });
  }, [client, own.projectId, queues, reread]);

  const unlink = useMutation({
    mutationFn: () => own.api.unlinkBookFromObsidianFolder(own.projectId, book.id),
    onSuccess: () => {
      markBooksChanged(client, own.projectId);
      markTreeOld(client, own.projectId);
      void client.invalidateQueries({ queryKey: obsidianKeys.vault(own.projectId) });
      toasts.push({ message: words.unlinkedBook(book.title), tone: 'success' });
      onClose();
    },
    onError: () => toasts.push({ message: words.tryAgain, tone: 'error' }),
  });

  // Nothing to read: the folder of the saga or of the book is not in its place (OU2.3).
  const vaultGone = !reading && !answer && !failure && vault?.availability === 'missing';
  const bookGone = !reading && !answer && !failure && held?.presence === 'missing';
  const place = vaultGone ? 'vaultGone' : bookGone ? 'folderGone' : refusal?.place ?? null;
  const unread = refusal?.unread ?? (refusal ? null : unreadOf(report?.skipped));
  const changed = Boolean(report && !report.refusal && report.state.changed);
  const plan = result && changed && !refusal?.text && !refusal?.failure && !place;
  const nothing = result && report && !report.refusal && !report.state.changed && !refusal;
  const broken = result && report?.refusal ? updateWords.broken(refusalReason(report.refusal, own.bookLimitBytes) ?? words.cantOther.toLowerCase()) : null;

  const exits: Record<UpdateExit, () => void> = {
    readAgain: onReread,
    retry: failure && 'job' in failure ? onReread : failure ? onRetry : () => apply(confirmUnread),
    link: () => { onClose(); own.link(book); },
    otherFolder: () => { onClose(); own.link(book); },
    unlink: () => unlink.mutate(),
    openBridge: () => { onClose(); own.openBridge(); },
    without: () => apply(true),
  };
  const exitWords: Record<UpdateExit, string> = {
    readAgain: updateWords.readAgain, retry: words.retry, link: updateWords.linkItem, otherFolder: updateWords.otherFolder,
    unlink: words.unlinkBook, openBridge: updateWords.openBridge, without: updateWords.without,
  };
  const exitButton = (exit: UpdateExit, variant: 'outline' | 'ghost' = 'outline') => (
    <Button key={exit} loading={exit === 'unlink' && unlink.isPending} onClick={exits[exit]} size="sm" variant={variant}>{exitWords[exit]}</Button>
  );
  // The folder of the book does not change under a request of the window: both phases of the reading and «Обновить книгу» (OU2.3).
  const busy = reading || applying ? updateWords.reading : null;
  const folderTools = [
    <Button disabledReason={busy} key="other" onClick={exits.otherFolder} size="sm" variant="ghost"><Link2 aria-hidden="true" size={14} />{updateWords.otherFolder}</Button>,
    <Button disabledReason={busy} key="unlink" loading={unlink.isPending} onClick={exits.unlink} size="sm" variant="ghost"><Unlink aria-hidden="true" size={14} />{words.unlinkBook}</Button>,
  ];

  return (
    <Dialog label={updateWords.title} onDismiss={applying ? () => undefined : onClose} state={state} width={640}>
      <section className={styles.updateWindow}>
        <div className={styles.windowHead}>
          <h2 data-overlay-heading="">{updateWords.title}</h2>
          <IconButton aria-label={updateWords.close} disabled={applying} onClick={onClose} size="sm"><X aria-hidden="true" size={16} /></IconButton>
        </div>
        <div className={styles.updateBody}>
          {place === 'vaultGone' ? (
            <Alert tone="warning">
              <p className={styles.text}>{updateWords.vaultGone}</p>
              <div className={styles.row}>{exitButton('openBridge')}</div>
            </Alert>
          ) : place === 'folderGone' ? (
            <div className={styles.folderOfBook}>
              <Folder aria-hidden="true" size={16} />
              <p className={styles.text}>{updateWords.folderGone(folder)}</p>
              <div className={styles.row}>{folderTools}</div>
            </div>
          ) : place === 'notLinked' ? null : (
            <div className={styles.folderOfBook}>
              <Folder aria-hidden="true" size={16} />
              <p className={styles.text}>{report ? updateWords.folderCounted(folder, report.notes.length, report.chapters.length) : updateWords.folder(folder)}</p>
              <div className={styles.row}>{folderTools}</div>
            </div>
          )}
          {reading ? <WorkLine detail={updateWords.unchangedUntil(book.title)} error={waits.reason} failures={waits.count} title={updateWords.reading} /> : null}
          {applying ? <WorkLine error={applyWaits.reason} failures={applyWaits.count} title={updateWords.reading} /> : null}
          {refusal?.failure ? (
            <ErrorBlock {...loadFailureWords(failure && 'error' in failure ? failure.error : applyError)} onRetry={exits.retry} title={words.previewFailed} />
          ) : refusal && (refusal.text || place === 'notLinked') && !refusal.unread && place !== 'vaultGone' ? (
            <Alert role="alert" tone="error">
              <p className={styles.text}>{refusal.text}</p>
              {refusal.exits.length ? <div className={styles.row}>{refusal.exits.map((exit) => exitButton(exit))}</div> : null}
            </Alert>
          ) : null}
          {broken ? <Alert role="alert" tone="error"><p className={styles.text}>{broken}</p></Alert> : null}
          {nothing ? <>
            <h3 className={styles.planTitle}>{updateWords.nothingTitle}</h3>
            <p className={styles.text} role="status">{updateWords.nothingText(book.title)}</p>
          </> : null}
          {plan ? <>
            <h3 className={styles.planTitle}>{updateWords.planTitle(book.title)}</h3>
            {previewHasChanges(result) ? <BookUpdatePreview of={fromNotes} result={result} /> : <>
              <p className={styles.muted}>{updateWords.wrong}</p>
              <p className={styles.text}>{updateWords.noList}</p>
            </>}
            {report ? <FolderReport report={report} /> : null}
          </> : null}
          {(plan || refusal?.unread) && unread ? (
            <Alert role="note" tone="warning"><p className={styles.text}>{updateWords.unread(unread.cloud, unread.unreadable)}</p></Alert>
          ) : null}
        </div>
        <div className={styles.windowButtons}>
          {reading || plan || refusal?.unread ? (
            <Button disabledReason={applying ? updateWords.reading : null} onClick={onClose} variant="outline">{words.cancel}</Button>
          ) : <Button onClick={onClose} variant="outline">{updateWords.close}</Button>}
          {(plan || refusal?.unread) && unread ? <>
            <Button disabled={applying} onClick={exits.without} variant="outline">{updateWords.without}</Button>
            <Button disabled={applying} onClick={onReread} variant="primary"><RotateCw aria-hidden="true" size={16} />{updateWords.readAgain}</Button>
          </> : plan ? (
            <Button loading={applying} onClick={() => apply(false)} variant="primary"><FolderSync aria-hidden="true" size={16} />{updateWords.confirm}</Button>
          ) : null}
        </div>
      </section>
    </Dialog>
  );
}

/** «Подробнее о заметках папки» (Data «шапка окна»): the warnings of the book, what did not go into the text, the notes skipped. */
function FolderReport({ report }: { report: NonNullable<ObsidianBookUpdate['book']> }) {
  const warnings = warningsOf(report);
  const dropped = droppedWords(report.dropped);
  if (!warnings.length && !dropped && !report.skipped.length) return null;
  return (
    <details className={styles.skipped}>
      <summary>{updateWords.report}</summary>
      {warnings.map((warning) => <p className={styles.warning} key={warning}>{warning}</p>)}
      {dropped ? <p className={styles.muted}>{dropped}</p> : null}
      <Skipped of="notes" skipped={report.skipped} />
    </details>
  );
}
