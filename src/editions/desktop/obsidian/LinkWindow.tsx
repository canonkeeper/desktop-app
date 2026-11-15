// SPDX-License-Identifier: AGPL-3.0-only
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Link2, X } from 'lucide-react';
import { useId, useState } from 'react';
import { loadFailureWords } from '../../../entities/api-errors';
import { Button } from '../../../ui/controls/Button';
import { IconButton } from '../../../ui/controls/IconButton';
import { useToasts } from '../../../ui/feedback/useToastQueue';
import { ErrorBlock } from '../../../ui/feedback/ErrorBlock';
import { Dialog } from '../../../ui/overlays/Dialog';
import { RefusalExits, WorkLine } from './lines';
import { markBooksChanged, markTreeOld, obsidianKeys, type ObsidianClient, type ObsidianTreeFolder, type ObsidianVaultBook } from './obsidian-api';
import { refusalOf } from './refusals';
import { words } from './words';
import styles from './Obsidian.module.css';

/** The book the window links: its folder now, when it has one, and whether that folder is in its place. */
export interface LinkedBook {
  bookId: string;
  title: string;
  folder?: string;
  presence?: ObsidianVaultBook['presence'];
}

function inside(folder: string, path: string) {
  return folder === '.' ? path !== '.' : path.startsWith(`${folder}/`);
}

/**
 * Why a folder cannot be the folder of the book (OW2.10): the folder of a book of the saga, a folder inside it, a folder it
 * lies in — each names the book. The folder of a book in «Недавно удалённые» may be taken; a folder inside or around it may
 * not, as the server refuses it (`folders_overlap`).
 */
export function linkReason(path: string, bookId: string, folders: readonly ObsidianTreeFolder[], books: readonly ObsidianVaultBook[]) {
  const live = books.filter((book) => book.bookId !== bookId);
  const held = live.find((book) => book.folder === path);
  if (held) return words.heldBook(held.title);
  const others = [
    ...live.map((book) => ({ folder: book.folder, title: book.title })),
    ...folders.filter((folder) => folder.book && folder.book.bookId !== bookId && !books.some((book) => book.bookId === folder.book!.bookId))
      .map((folder) => ({ folder: folder.path, title: folder.book!.title })),
  ];
  const around = others.find((other) => inside(other.folder, path));
  if (around) return words.insideBook(around.title);
  const within = others.find((other) => inside(path, other.folder));
  return within ? words.holdsBook(within.title) : null;
}

/**
 * «Связать с папкой Obsidian» (specs/2026-11-15-desk-obsidian-web.md OW2.10; board 44·58, `Obsidian Link Window`): the book,
 * the folder it is linked with now — or that it has none and why to link —, the folders of the look on one choice. «Связать»
 * waits while the saga loads books; a refusal is said in the window by its words. The page of the bridge opens it from the
 * block of the decisions; the menu of a book opens it in the second part.
 */
export function LinkWindow({ api, book, books, folders, look, onClose, projectId, queueLive }: {
  api: Pick<ObsidianClient, 'linkBookToObsidianFolder'>;
  book: LinkedBook;
  books: readonly ObsidianVaultBook[];
  /** The folders of the look; null while it is read — from the menu of a book (specs/2026-11-15-desk-obsidian-book-web.md OU1.1). */
  folders: readonly ObsidianTreeFolder[] | null;
  /** The look read for the window: its repeats and its refusal. */
  look?: { error: unknown; failures: number; failureReason: unknown; retry: () => void };
  onClose: () => void;
  projectId: string;
  queueLive: boolean;
}) {
  const client = useQueryClient();
  const toasts = useToasts();
  const name = useId();
  // The folder of the book is chosen beforehand only when the window takes it: in the look and held by no other book (OW2.10).
  const [picked, setChosen] = useState<string | null>(null);
  const fromBefore = folders && book.folder && folders.some((folder) => folder.path === book.folder)
    && linkReason(book.folder, book.bookId, folders, books) === null ? book.folder : null;
  const chosen = picked ?? fromBefore;
  const titles = new Map(books.map((one) => [one.bookId, one.title]));
  const link = useMutation({
    mutationFn: (folder: string) => api.linkBookToObsidianFolder(projectId, book.bookId, { folder }),
    onSuccess: (_answer, folder) => {
      markBooksChanged(client, projectId);
      void client.invalidateQueries({ queryKey: obsidianKeys.vault(projectId) });
      markTreeOld(client, projectId);
      toasts.push({ message: words.linkedDone(book.title, folder), tone: 'success' });
      onClose();
    },
  });
  const refusal = link.error ? refusalOf(link.error, 'link', (bookId) => titles.get(bookId) ?? '') : null;
  const lookRefusal = !folders && look?.error ? refusalOf(look.error, 'scan') : null;
  const reason = queueLive ? words.waitLoad : !folders ? words.scanning : chosen === null ? words.pickFolder : null;
  return (
    <Dialog label={words.linkTitle} onDismiss={onClose} width={560}>
      <section className={styles.window}>
        <div className={styles.windowHead}>
          <h2 data-overlay-heading="">{words.linkTitle}</h2>
          <IconButton aria-label="Закрыть" onClick={onClose} size="sm"><X aria-hidden="true" size={16} /></IconButton>
        </div>
        <strong className={styles.text}>{book.title}</strong>
        {book.folder ? (
          <p className={styles.muted}>{book.presence === 'missing' ? words.linkMissing : words.linkedTo(book.folder)}</p>
        ) : <p className={styles.muted}>{words.linkWhy}</p>}
        {!folders ? (lookRefusal?.failure ? <ErrorBlock {...loadFailureWords(look!.error)} onRetry={look!.retry} title={words.scanFailed} />
          : lookRefusal ? <>
            <p className={styles.error} role="alert">{lookRefusal.text}</p>
            <RefusalExits exits={lookRefusal.exits} on={{ retry: look!.retry }} />
          </> : <WorkLine detail={words.scanLong} error={look?.failureReason} failures={look?.failures ?? 0} title={words.scanning} />) : null}
        {folders ? <fieldset aria-label={words.linkTitle} className={styles.choices}>
          {folders.map((folder) => {
            const why = linkReason(folder.path, book.bookId, folders, books);
            return (
              <label className={styles.choice} data-disabled={why ? '' : undefined} key={folder.path}>
                <input checked={chosen === folder.path} disabled={Boolean(why)} name={name} onChange={() => setChosen(folder.path)} type="radio" value={folder.path} />
                <span className={styles.choiceText}>
                  <span>{folder.path === '.' ? folder.name : folder.path}</span>
                  {why ? <span className={styles.muted}>{why}</span> : null}
                </span>
              </label>
            );
          })}
        </fieldset> : null}
        {refusal ? <p className={styles.error} role="alert">{refusal.failure ? loadFailureWords(link.error).cause || words.failure : refusal.text}</p> : null}
        <div className={styles.windowButtons}>
          <Button onClick={onClose} variant="outline">{words.cancel}</Button>
          <Button disabledReason={reason} loading={link.isPending} onClick={() => { if (chosen !== null) link.mutate(chosen); }} variant="primary">
            <Link2 aria-hidden="true" size={16} />{words.doLink}
          </Button>
        </div>
      </section>
    </Dialog>
  );
}
