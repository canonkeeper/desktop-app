// SPDX-License-Identifier: AGPL-3.0-only
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { ArrowLeft, Download, RotateCw } from 'lucide-react';
import { useEffect, useState } from 'react';
import { loadFailureWords } from '../../../entities/api-errors';
import type { Book } from '../../../entities/manuscript/api';
import { similarBook } from '../../../features/manuscript/shelf/upload-files';
import { Button } from '../../../ui/controls/Button';
import { Alert } from '../../../ui/feedback/Alert';
import { ErrorBlock } from '../../../ui/feedback/ErrorBlock';
import { useToasts } from '../../../ui/feedback/useToastQueue';
import { BookCard } from './BookCard';
import { RefusalExits, WorkLine } from './lines';
import { sameRequest, type ChoiceState } from './choice';
import { changedBooks, newBooks, noDecisions, recordsToWrite, type Decisions } from './load';
import { LoadResult } from './LoadResult';
import { liveBookIds, markBooksChanged, markTreeOld, obsidianKeys, type ObsidianClient, type ObsidianPreview, type ObsidianVault } from './obsidian-api';
import { RecordsPart } from './RecordsPart';
import { refusalOf, type RefusalExit } from './refusals';
import { useLoad, useLoadOf } from './use-load';
import { amountWords, noteName, willWords, words } from './words';
import styles from './Obsidian.module.css';

export interface PreviewQuery {
  data: ObsidianPreview | undefined;
  error: unknown;
  isFetching: boolean;
  failureCount: number;
  failureReason: unknown;
  dataUpdatedAt: number;
  errorUpdatedAt: number;
}

interface PreviewStepProps {
  api: ObsidianClient;
  projectId: string;
  preview: PreviewQuery;
  /** The choice of the folders now: a preview of another choice is old (OW3.4). */
  choice: ChoiceState | null;
  vault: ObsidianVault;
  sagaBooks: readonly Book[];
  /** The load of «Книги» of the saga that is live: its words, or null (OW3.6). */
  queueNote: string | null;
  bookLimitBytes: number | null;
  onSkipNote: (path: string) => void;
  /** The notes «Не брать» left out (OW3.2): each with «Вернуть». */
  notTaken: readonly string[];
  /** «Обновить из Obsidian…» of a book of the saga (OU2.8): the window of its update, and why it waits now. */
  update: { open: (book: Book) => void; reason: string | null; working: string | null };
  onGiveBack: (path: string) => void;
  on: Partial<Record<RefusalExit, () => void>> & {
    showAgain: () => void;
    changeChoice: () => void;
    openTrash: () => void;
    toBooks: () => void;
    toMaterials: () => void;
    loadMore: () => void;
  };
}

/** The word of the reading (OW3.5): by who reads the books and whether the saga may be read; no line without chapters. */
function readingWords(reading: ObsidianPreview['answer']['reading']) {
  if (reading.newChapters === 0) return null;
  if (reading.helper === 'none') return words.readNone;
  const amount = amountWords(reading.newChapters, reading.newCharacters);
  return reading.consent ? words.readNow(amount) : words.readLater(amount);
}

/**
 * The step «что получится» (specs/2026-11-15-desk-obsidian-web.md OW3; board 44·51, 44·52, `Obsidian Preview`): read only by a
 * press; while it goes — «Читаю заметки…»; a refusal with its words and way out; the books and the records of the preview; what
 * made it old since — «Выбор изменён», «Книги изменились» — closes «Загрузить» until «Показать заново»; the word of the reading;
 * what «Загрузить» loads and what it does not; then the result of the load.
 */
export function PreviewStep({ api, bookLimitBytes, choice, notTaken, on, onGiveBack, onSkipNote, preview, projectId, queueNote, sagaBooks, update, vault }: PreviewStepProps) {
  const client = useQueryClient();
  const toasts = useToasts();
  const [decisions, setDecisions] = useState<Decisions>(noDecisions);
  const loading = useLoad(api);
  const data = preview.data;
  const writes = useLoadOf(data?.id);
  const failedLater = preview.error && preview.errorUpdatedAt >= preview.dataUpdatedAt && !preview.isFetching;
  const refusal = failedLater ? refusalOf(preview.error, 'preview') : null;
  // `not_linked`: the folder of the saga is read again — the page says «папка не выбрана» by its answer (Data).
  const unlinked = Boolean(refusal?.reread);
  useEffect(() => {
    if (unlinked) void client.invalidateQueries({ queryKey: obsidianKeys.vault(projectId) });
  }, [client, projectId, unlinked]);
  const titles = new Map(sagaBooks.map((book) => [book.id, book.title]));
  // A book of the saga a new folder may be (OW3.2, similarBook): one that holds no folder, a book of «Нужно ваше решение», and a
  // book whose folder is not in its place — a renamed folder, which a load would make a second book.
  const missing = new Set((vault.books ?? []).filter((held) => held.presence === 'missing').map((held) => held.bookId));
  const linkable = sagaBooks.filter((book) => missing.has(book.id) || !(vault.books ?? []).some((held) => held.bookId === book.id));
  const bookAction = useMutation({
    mutationFn: ({ bookId, folder }: { bookId: string; folder: string | null; title: string }) => (folder === null
      ? api.unlinkBookFromObsidianFolder(projectId, bookId) : api.linkBookToObsidianFolder(projectId, bookId, { folder })),
    onSuccess: (_answer, { folder, title }) => {
      markBooksChanged(client, projectId);
      void client.invalidateQueries({ queryKey: obsidianKeys.vault(projectId) });
      markTreeOld(client, projectId);
      toasts.push({ message: folder === null ? words.unlinkedBook(title) : words.linkedDone(title, folder), tone: 'success' });
    },
    onError: (error) => {
      const said = refusalOf(error, 'link', (bookId) => titles.get(bookId) ?? '');
      toasts.push({ message: said.failure ? words.tryAgain : said.text, tone: 'error' });
    },
  });

  const acting = bookAction.isPending ? bookAction.variables : undefined;
  const heading = <h2 className={styles.stepTitle} data-step-heading="" id="obsidian-step" tabIndex={-1}>{words.previewTitle}</h2>;
  const back = <Button onClick={on.changeChoice} size="sm" variant="ghost"><ArrowLeft aria-hidden="true" size={14} />{words.changeChoice}</Button>;
  if (preview.isFetching) {
    return (
      <section aria-labelledby="obsidian-step" className={styles.step}>
        {heading}
        <WorkLine detail={words.readingLong} error={preview.failureReason} failures={preview.failureCount} title={words.readingNotes} />
        <div className={styles.row}>{back}</div>
      </section>
    );
  }
  if (refusal) {
    return (
      <section aria-labelledby="obsidian-step" className={styles.step}>
        {heading}
        {refusal.failure ? <ErrorBlock {...loadFailureWords(preview.error)} onRetry={on.showAgain} title={words.previewFailed} /> : (
          <Alert tone="error">
            <p className={styles.text}>{refusal.text}</p>
            <RefusalExits exits={refusal.exits} on={{ ...on, retry: on.showAgain }} />
          </Alert>
        )}
        <div className={styles.row}>{back}</div>
      </section>
    );
  }
  if (!data) return null;

  const { answer } = data;
  // The live books of the saga changed since it was read — a book given back or deleted, a load of another ended — or an action
  // of this page changed the books of a folder: «Книги изменились» (OW3.4).
  const booksMoved = data.booksChanged || JSON.stringify(liveBookIds(sagaBooks)) !== JSON.stringify(data.liveBooks);
  const stale = choice && !sameRequest(choice.request, data.request) ? words.staleChoice : booksMoved ? words.staleBooks : null;
  const started = writes.materials ?? writes.books;
  const records = recordsToWrite(data, decisions);
  const fresh = queueNote ? [] : newBooks(data);
  const will = willWords(fresh.length, records.created, records.updated);
  const changed = changedBooks(data);
  const reading = readingWords(answer.reading);
  const refusedNew = answer.books.some((book) => book.state.kind === 'new' && book.refusal);
  const nothing = !will ? (queueNote && newBooks(data).length ? queueNote
    : changed.length ? changed.map((book) => words.notUpdated(book.state.title ?? book.title)).join('. ')
      : refusedNew ? words.nothingRefused : words.nothing) : null;
  // The result of a load stands alone: no heading of the step and no word that the preview is old over it (board 44·53).
  if (started) {
    return (
      <LoadResult bookLimitBytes={bookLimitBytes} books={writes.books} materials={writes.materials} preview={data} queueNote={queueNote}
        on={{ ...on, repeatMaterials: loading.repeatMaterials, repeatBooks: loading.repeatBooks, loadBooksAgain: loading.loadBooksAgain }} />
    );
  }
  return (
    <section aria-labelledby="obsidian-step" className={styles.step}>
      {heading}
      {stale ? (
        <div className={styles.row} role="status">
          <span className={styles.warning}>{stale}</span>
          <Button onClick={on.showAgain} size="sm" variant="outline"><RotateCw aria-hidden="true" size={14} />{words.showAgain}</Button>
        </div>
      ) : null}
      <>
        {answer.books.length ? (
          <div className={styles.part}>
            <h3>{words.books}</h3>
            {update.working ? <p className={styles.muted} role="status">{update.working}</p> : null}
            {queueNote ? (
              <Alert role="note" tone="warning">
                <p className={styles.text}>{queueNote}</p>
                <RefusalExits exits={['toBooks']} on={on} />
              </Alert>
            ) : null}
            <ul className={styles.cards}>
              {answer.books.map((book) => (
                <BookCard book={book} key={book.folder} actions={{
                  similar: book.state.kind === 'new' && !book.refusal ? (() => {
                    const like = similarBook(book.title, linkable);
                    return like ? { bookId: like.id, title: like.title, missing: missing.has(like.id) } : null;
                  })() : null,
                  onLink: (like) => bookAction.mutate({ bookId: like.bookId, folder: book.folder, title: like.title }),
                  onUnlink: (bookId, title) => bookAction.mutate({ bookId, folder: null, title }),
                  onOpenTrash: on.openTrash,
                  onToBooks: on.toBooks,
                  onSkipNote: book.state.kind === 'new' ? onSkipNote : null,
                  bookLimitBytes,
                  linkReason: queueNote ? words.waitLoad : null,
                  onUpdate: (() => {
                    const ofSaga = sagaBooks.find((one) => one.id === book.state.bookId);
                    return ofSaga ? () => update.open(ofSaga) : null;
                  })(),
                  updateReason: queueNote ? words.waitLoad : update.reason,
                  working: !acting ? null : acting.folder === book.folder ? 'link'
                    : acting.folder === null && acting.bookId === book.state.bookId ? 'unlink' : null,
                }} />
              ))}
            </ul>
          </div>
        ) : null}
        {answer.materials.folders.length ? (
          <div className={styles.part}>
            <h3>{words.records}</h3>
            <RecordsPart decisions={decisions} onDecisions={setDecisions} onSkip={onSkipNote} records={answer.materials.records} />
          </div>
        ) : null}
        {notTaken.length ? (
          <details className={styles.skipped}>
            <summary>{words.notTaken(notTaken.length)}</summary>
            <ul className={styles.notes}>
              {notTaken.map((path) => (
                <li className={styles.note} key={path}>
                  <span className={styles.noteText}><span>{noteName(path)}</span></span>
                  <Button aria-label={`${words.giveBack} «${noteName(path)}»`} onClick={() => onGiveBack(path)} size="sm" variant="ghost">{words.giveBack}</Button>
                </li>
              ))}
            </ul>
          </details>
        ) : null}
        {reading ? <p className={styles.muted}>{reading}</p> : null}
        <div className={styles.bottom}>
          {will ? <p className={styles.text}>{will}</p> : null}
          {changed.map((book) => <p className={styles.muted} key={book.folder}>{words.notUpdated(book.state.title ?? book.title)}</p>)}
          <div className={styles.row}>
            <Button disabledReason={stale ? words.whyStale : nothing} onClick={() => void loading.load(projectId, data, decisions, Boolean(queueNote))} variant="primary">
              <Download aria-hidden="true" size={16} />{words.load}
            </Button>
            {back}
          </div>
        </div>
      </>
    </section>
  );
}
