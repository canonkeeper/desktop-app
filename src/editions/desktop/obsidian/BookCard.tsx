// SPDX-License-Identifier: AGPL-3.0-only
import { AlertTriangle, ArchiveRestore, BookOpen, CircleAlert, CircleCheck, FolderSync, Link2, Unlink } from 'lucide-react';
import type { ReactNode } from 'react';
import { Button } from '../../../ui/controls/Button';
import { Skipped } from './lines';
import type { ObsidianBookPreview } from './obsidian-api';
import { bookMeta, droppedWords, noteName, refusalReason, updateWords, words } from './words';
import styles from './Obsidian.module.css';

/** What a card of a book does: its buttons by its state (OW3.2). */
export interface BookCardActions {
  /**
   * A book of the saga with a title like this one, not linked with a folder or linked with one not in its place (`missing`):
   * «Связать с «…»».
   */
  similar: { bookId: string; title: string; missing: boolean } | null;
  onLink: (book: { bookId: string; title: string }) => void;
  onUnlink: (bookId: string, title: string) => void;
  onOpenTrash: () => void;
  onToBooks: () => void;
  /** «Не брать» of a note — only of a new book (OW3.2); null for a book of the saga. */
  onSkipNote: ((path: string) => void) | null;
  /** The limit of a book file of the server: the words of `import.file_too_large` name it. */
  bookLimitBytes: number | null;
  /** The action of this card that goes now: its button says it works, the other buttons stay open. */
  working: 'link' | 'unlink' | null;
  /**
   * Why «Связать с «…»» waits: a live load of books of the saga (the server would answer `queue_busy`), the export of the saga
   * (specs/2026-11-16-desk-obsidian-export-web.md XW2.3).
   */
  linkReason: string | null;
  /** Why «Снять связь» waits: the export of the saga holds the vault (XW2.3). */
  unlinkReason?: string | null;
  /**
   * «Обновить из Obsidian…» of a book of the saga with changes in its notes (specs/2026-11-15-desk-obsidian-book-web.md OU2.8): the
   * window of the update of the book; null where the page has no such book.
   */
  onUpdate: (() => void) | null;
  /** Why «Обновить из Obsidian…» waits: a live load of books of the saga, another update. */
  updateReason: string | null;
}

function State({ children, tone }: { children: ReactNode; tone: 'ok' | 'warning' | 'error' }) {
  const Icon = tone === 'ok' ? CircleCheck : tone === 'warning' ? AlertTriangle : CircleAlert;
  return <p className={tone === 'ok' ? styles.state : `${styles.state} ${tone === 'warning' ? styles.warning : styles.error}`}><Icon aria-hidden="true" size={15} />{children}</p>;
}

/** The warnings of a book, each by its numbers and the notes it names (OW3.2): of a card and of the report of an update (OU2.5). */
export function warningsOf(book: ObsidianBookPreview) {
  const title = (path: string) => book.notes.find((note) => note.path === path)?.title || noteName(path);
  return book.warnings.map((warning) => {
    switch (warning.code) {
      case 'chapters_differ': return words.wCount(book.chapters.length, book.notes.length, warning.notes.map(title));
      case 'book_in_note': return words.wOne(title(warning.notes[0] ?? ''));
      case 'order_by_name': return words.wOrder;
      default: return words.wLong;
    }
  });
}

/** Where the place of a note comes from (OW3.2): its header, the note «_book», a number in its name, its name. */
function orderWords(note: ObsidianBookPreview['notes'][number]) {
  if (note.orderBy === 'front') return words.orderFront;
  if (note.orderBy === 'index') return words.orderIndex;
  return /\d/u.test(noteName(note.path)) ? words.orderNumber : words.orderName;
}

/**
 * A card of a book of «что получится» (specs/2026-11-15-desk-obsidian-web.md OW3.2; board 44·51, `Obsidian Book Card`): its
 * name and size, its state in words with its buttons, its warnings with notes for example, what did not go into the text, the
 * notes skipped, and «Заметки и главы (N)» — each note with its chapters and where its place comes from; «Не брать» only at a
 * note of a new book. A book of the saga keeps the composition of its load: no «Не брать» there.
 */
export function BookCard({ actions, book }: { actions: BookCardActions; book: ObsidianBookPreview }) {
  const reason = refusalReason(book.refusal, actions.bookLimitBytes);
  const { state } = book;
  const title = state.title ?? book.title;
  let numbered = 0;
  const dropped = droppedWords(book.dropped);
  return (
    <li aria-label={book.title} className={styles.card}>
      <div className={styles.cardHead}>
        <BookOpen aria-hidden="true" size={16} />
        <strong>{book.title}</strong>
        <span className={styles.muted}>{bookMeta(book.chapters.length, book.characters)}</span>
      </div>
      {state.kind === 'new' ? (book.refusal ? <State tone="error">{reason ? words.stCant(reason) : words.cantOther}</State>
        : actions.similar ? <>
          <State tone="warning">{actions.similar.missing ? words.stSimilarMissing(actions.similar.title) : words.stSimilar(actions.similar.title)}</State>
          <div className={styles.row}>
            <Button disabledReason={actions.linkReason} loading={actions.working === 'link'} onClick={() => actions.onLink(actions.similar!)} size="sm" variant="outline">
              <Link2 aria-hidden="true" size={14} />{words.linkSimilar(actions.similar.title)}
            </Button>
          </div>
        </> : <State tone="ok">{words.stNew}</State>) : null}
      {state.kind === 'linked' && !state.inTrash ? (book.refusal
        ? <State tone="error">{words.stBroken(title, reason ?? words.cantOther.toLowerCase())}</State>
        : <>
          <State tone={state.changed ? 'warning' : 'ok'}>{state.changed ? words.stChanged(title) : words.stSame(title)}</State>
          {state.changed && actions.onUpdate ? (
            <div className={styles.row}>
              <Button disabledReason={actions.updateReason} onClick={actions.onUpdate} size="sm" variant="outline">
                <FolderSync aria-hidden="true" size={14} />{updateWords.updateItem}
              </Button>
            </div>
          ) : null}
        </>) : null}
      {state.kind === 'linked' && state.inTrash ? <>
        <State tone="warning">{words.stTrash(title)}</State>
        <div className={styles.row}>
          <Button onClick={actions.onOpenTrash} size="sm" variant="outline"><ArchiveRestore aria-hidden="true" size={14} />{words.openTrash}</Button>
          <Button disabledReason={actions.unlinkReason} loading={actions.working === 'unlink'} onClick={() => actions.onUnlink(state.bookId ?? '', title)} size="sm" variant="ghost">
            <Unlink aria-hidden="true" size={14} />{words.unlinkBook}
          </Button>
        </div>
      </> : null}
      {state.kind === 'queued' ? <>
        <State tone="warning">{words.stLoading}</State>
        <div className={styles.row}><Button onClick={actions.onToBooks} size="sm" variant="outline">{words.toBooks}</Button></div>
      </> : null}
      {warningsOf(book).map((warning) => <p className={styles.warning} key={warning}>{warning}</p>)}
      {dropped ? <p className={styles.muted}>{dropped}</p> : null}
      <Skipped of="notes" skipped={book.skipped} />
      {book.notes.length ? (
        <details className={styles.skipped}>
          <summary>{words.notesToggle(book.notes.length)}</summary>
          <ul className={styles.notes}>
            {book.notes.map((note) => {
              const chapters = note.chapters.map((chapter) => words.chapter((numbered += 1), chapter)).join(', ');
              return (
                <li className={styles.note} key={note.path}>
                  <span className={styles.noteText}>
                    <span>{note.title || noteName(note.path)}</span>
                    <span className={styles.muted}>{[chapters, orderWords(note)].filter(Boolean).join(' · ')}</span>
                  </span>
                  {actions.onSkipNote ? (
                    <Button aria-label={`${words.skip} «${note.title || noteName(note.path)}»`} onClick={() => actions.onSkipNote!(note.path)} size="sm" variant="ghost">{words.skip}</Button>
                  ) : state.kind === 'linked' && !state.inTrash ? <span className={styles.muted}>{words.fixed}</span> : null}
                </li>
              );
            })}
          </ul>
        </details>
      ) : null}
    </li>
  );
}
