// SPDX-License-Identifier: AGPL-3.0-only
import { useQueryClient } from '@tanstack/react-query';
import { ArchiveRestore, ArrowRight, Plus } from 'lucide-react';
import { useEffect } from 'react';
import { Button } from '../../../ui/controls/Button';
import { Alert } from '../../../ui/feedback/Alert';
import { WorkLine, RefusalExits } from './lines';
import { changedBooks, newBooks } from './load';
import { obsidianKeys, type ObsidianBookPreview, type ObsidianBooksLoad, type ObsidianMaterialsLoad, type ObsidianPreview } from './obsidian-api';
import { failureCause, quotaText, refusalOf, type RefusalExit } from './refusals';
import { useLoadOf, type BooksWrite, type MaterialsWrite, type WriteState } from './use-load';
import { namesWords, refusalReason, resultWords, words } from './words';
import styles from './Obsidian.module.css';

export interface LoadResultProps {
  preview: ObsidianPreview;
  materials: WriteState<MaterialsWrite, ObsidianMaterialsLoad> | null;
  books: WriteState<BooksWrite, ObsidianBooksLoad> | null;
  bookLimitBytes: number | null;
  /** The words of the live load of «Книги» of the saga; said only when the run did not send its new books (OW3.6). */
  queueNote: string | null;
  /** The export of the saga holds the vault: why the ways out that write again wait (specs/2026-11-16-desk-obsidian-export-web.md XW2.3). */
  fenced?: string | null;
  on: {
    showAgain: () => void;
    repeatMaterials: (write: MaterialsWrite) => void;
    repeatBooks: (write: BooksWrite) => void;
    loadBooksAgain: (write: BooksWrite) => void;
    openTrash: () => void;
    toBooks: () => void;
    toMaterials: () => void;
    loadMore: () => void;
  };
}

/** The saga of the writes of a run. */
function projectIdOf(materials: { write: { projectId: string } } | null, books: { write: { projectId: string } } | null) {
  return materials?.write.projectId ?? books?.write.projectId ?? '';
}

const lower = (text: string | null) => (text ? text.charAt(0).toLowerCase() + text.slice(1) : null);

/** The line of a book of the result (OW4.4): its outcome when it was sent, else its state of the preview. */
function bookLine(book: ObsidianBookPreview, answer: ObsidianBooksLoad | undefined, limit: number | null) {
  const outcome = answer?.folders.find((folder) => folder.folder === book.folder);
  const title = outcome?.title ?? book.state.title ?? book.title;
  const refused = (code: string | undefined) => words.bookRefused(book.title, refusalReason(code, limit) ?? lower(quotaText(code)) ?? words.cantOther.toLowerCase());
  if (outcome) {
    switch (outcome.outcome) {
      case 'queued': return { text: words.bookQueued(book.title), trash: false };
      case 'linked': return { text: words.bookLinked(title), trash: false };
      case 'in_trash': return { text: words.bookInTrash(title), trash: true };
      default: return { text: refused(outcome.refusal), trash: false };
    }
  }
  if (book.state.kind === 'queued') return { text: words.bookQueued(book.title), trash: false };
  if (book.state.kind === 'linked') return book.state.inTrash ? { text: words.bookInTrash(title), trash: true } : { text: words.bookLinked(title), trash: false };
  return book.refusal ? { text: refused(book.refusal), trash: false } : null;
}

/**
 * The result of «Загрузить» on the step «что получится» (specs/2026-11-15-desk-obsidian-web.md OW4.1, OW4.4, OW4.7; board 44·53,
 * `Obsidian Result`): while it goes — «Сохраняю записи…», «Добавляю книги в сагу…»; then the records by the groups of the
 * answer, the books by the outcome of each folder, each part apart, a part that did not pass with its words and its way out;
 * the books with changed notes named; «К книгам», «К вашим материалам», «Загрузить ещё».
 */
export function LoadResult({ bookLimitBytes, books, fenced = null, materials, on, preview, queueNote }: LoadResultProps) {
  const savingRecords = materials?.status === 'pending';
  const addingBooks = books?.status === 'pending';
  const recordsRefusal = materials?.status === 'error' ? refusalOf(materials.error, 'materials') : null;
  const booksRefusal = books?.status === 'error' ? refusalOf(books.error, 'books') : null;
  const kept = materials?.answer?.kept;
  // `not_linked` of a write: the folder of the saga is read again — the page says «папка не выбрана» by its answer (Data).
  const client = useQueryClient();
  const unlinked = Boolean(recordsRefusal?.reread || booksRefusal?.reread);
  useEffect(() => {
    if (unlinked) void client.invalidateQueries({ queryKey: obsidianKeys.vault(projectIdOf(materials, books)) });
  }, [books, client, materials, unlinked]);
  const exitsFor = (refusal: { exits: RefusalExit[] }, write: 'materials' | 'books') => (
    <RefusalExits exits={refusal.exits} on={{
      showAgain: on.showAgain, toBooks: on.toBooks,
      retry: write === 'materials' ? (materials ? () => on.repeatMaterials(materials.write) : undefined) : books ? () => on.repeatBooks(books.write) : undefined,
      loadBooksAgain: books ? () => on.loadBooksAgain(books.write) : undefined,
    }} reason={fenced} />
  );
  const bookLines = preview.answer.books.map((book) => ({ book, line: bookLine(book, books?.answer, bookLimitBytes) })).filter((each) => each.line);
  // «Книги сейчас не загрузить» only of new books this run did not send for a live load of another — its own load is not it.
  const heldBack = queueNote !== null && books === null && newBooks(preview).length > 0;
  return (
    // No heading of the step over the result (board 44·53): the focus of a change of the step stands on the result itself.
    <section className={styles.step} data-step-heading="" tabIndex={-1}>
      {savingRecords ? <WorkLine error={null} failures={0} title={words.saving} /> : null}
      {addingBooks ? <WorkLine error={null} failures={0} title={words.adding} /> : null}
      {materials && !savingRecords ? (
        <div className={styles.part}>
          <h3>{words.records}</h3>
          {materials.answer ? <>
            {resultWords(materials.answer.created.count, materials.answer.updated.count, materials.answer.replaced.count, materials.answer.unchanged.count)
              ? <p className={styles.text}>{resultWords(materials.answer.created.count, materials.answer.updated.count, materials.answer.replaced.count, materials.answer.unchanged.count)}</p> : null}
            {kept && kept.taken.count ? <p className={styles.text}>{words.keptTaken}{namesWords(kept.taken.names, kept.taken.count)}</p> : null}
            {kept && kept.edited.count ? <p className={styles.text}>{words.keptEdited}{namesWords(kept.edited.names, kept.edited.count)}</p> : null}
            {kept && kept.changed.count ? <p className={styles.text}>{words.keptChanged}{namesWords(kept.changed.names, kept.changed.count)}</p> : null}
            {kept && (kept.taken.count || kept.edited.count || kept.changed.count) ? <RefusalExits exits={['showAgain']} on={{ showAgain: on.showAgain }} reason={fenced} /> : null}
          </> : null}
          {recordsRefusal ? (
            <Alert tone="error">
              <p className={styles.text}>{words.partRecords(recordsRefusal.failure ? failureCause(materials.error) : lower(recordsRefusal.text)!)}</p>
              {exitsFor(recordsRefusal.failure ? { exits: ['retry'] } : recordsRefusal, 'materials')}
            </Alert>
          ) : null}
        </div>
      ) : null}
      {!addingBooks && (bookLines.length || books || heldBack) ? (
        <div className={styles.part}>
          <h3>{words.books}</h3>
          {booksRefusal ? (
            <Alert tone="error">
              <p className={styles.text}>{words.partBooks(booksRefusal.failure ? failureCause(books!.error) : lower(booksRefusal.text)!)}</p>
              {exitsFor(booksRefusal.failure ? { exits: ['retry'] } : booksRefusal, 'books')}
            </Alert>
          ) : null}
          {heldBack ? <p className={styles.warning}>{queueNote}</p> : null}
          {!booksRefusal ? (
            <ul className={styles.lines}>
              {bookLines.map(({ book, line }) => (
                <li className={styles.row} key={book.folder}>
                  <span className={styles.text}>{line!.text}</span>
                  {line!.trash ? <Button onClick={on.openTrash} size="sm" variant="ghost"><ArchiveRestore aria-hidden="true" size={14} />{words.openTrash}</Button> : null}
                </li>
              ))}
            </ul>
          ) : null}
          {changedBooks(preview).map((book) => <p className={styles.muted} key={book.folder}>{words.notUpdated(book.state.title ?? book.title)}</p>)}
        </div>
      ) : null}
      {!savingRecords && !addingBooks ? (
        <div className={styles.row}>
          <Button onClick={on.toBooks} size="sm" variant="outline"><ArrowRight aria-hidden="true" size={14} />{words.toBooks}</Button>
          <Button onClick={on.toMaterials} size="sm" variant="outline"><ArrowRight aria-hidden="true" size={14} />{words.toMaterials}</Button>
          <Button onClick={on.loadMore} size="sm" variant="ghost"><Plus aria-hidden="true" size={14} />{words.loadMore}</Button>
        </div>
      ) : null}
    </section>
  );
}

/**
 * The load of the preview of the saga on the choice of the folders (OW4.1): while a write goes — «Идёт загрузка из Obsidian…»,
 * once both ended and the result is not closed — «Последняя загрузка из Obsidian закончена»; «Открыть итог» leads to it.
 */
export function LoadLine({ onOpen, previewId }: { onOpen: () => void; previewId: string | undefined }) {
  const { books, materials } = useLoadOf(previewId);
  if (!books && !materials) return null;
  const going = books?.status === 'pending' || materials?.status === 'pending';
  return (
    <div className={styles.row} role="status">
      <span className={styles.text}>{going ? words.loadGoing : words.loadEnded}</span>
      <Button onClick={onOpen} size="sm" variant="outline"><ArrowRight aria-hidden="true" size={14} />{words.openResult}</Button>
    </div>
  );
}

/** A write of «Загрузить» of the preview goes now: a new preview waits for it — the vault of the saga is held by it (OW4.1). */
export function useLoadGoing(previewId: string | undefined) {
  const { books, materials } = useLoadOf(previewId);
  return books?.status === 'pending' || materials?.status === 'pending';
}
