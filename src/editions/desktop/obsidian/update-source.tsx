// SPDX-License-Identifier: AGPL-3.0-only
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { FolderSync, Link2 } from 'lucide-react';
import { useMemo, useState } from 'react';
import type { Book } from '../../../entities/manuscript/api';
import { useImportConstraintsQuery } from '../../../features/manuscript/api';
import type { ShelfBookMenu, ShelfBookMenuItem, ShelfBookMenuOptions } from '../../../features/manuscript/shelf/shelf-slot';
import { createIdempotencyKey, type CanonKeeperApiClient } from '../../../shared/api';
import type { RouteNavigate } from '../../../shared/navigation/app-route';
import { obsidianAt } from './address';
import { LinkWindow } from './LinkWindow';
import {
  busyRetry, markBooksChanged, obsidianKeys, treeQueryOptions, useObsidianVault, type ObsidianClient,
  type ObsidianVault,
} from './obsidian-api';
import { UpdateWindow, type ObsidianUpdateSource } from './UpdateWindow';
import { updateWords, words } from './words';

/**
 * The update of a book from its folder on a page (OU2): the source the page gives the owner of the window — the reading and the
 * update under the keys of the owner, a repeat only for `obsidian.busy` by its Retry-After (the rule of the first part), a book
 * whose folder or the folder of the saga is not in its place opens the window without a reading —, and the window «Связать с
 * папкой Obsidian» its «Другая папка…» opens. Only the saga's folder is read here; the look over it — when the link window opens.
 */
export function useObsidianBookUpdate({ api, enabled = true, navigate, projectId, queueLive }: {
  api: ObsidianClient & Pick<CanonKeeperApiClient, 'getImportConstraints'>;
  /** Only an author who may change the books reads anything for it. */
  enabled?: boolean;
  navigate: RouteNavigate;
  projectId: string;
  /** The saga loads books now: «Связать» waits (OW2.10). */
  queueLive: boolean;
}) {
  const client = useQueryClient();
  const vault = useObsidianVault(api, projectId, enabled).data;
  const bookLimitBytes = useImportConstraintsQuery(api, projectId, enabled && Boolean(projectId)).data?.maxFileSizeBytes ?? null;
  const [linking, setLinking] = useState<Book | null>(null);
  const source = useMemo<ObsidianUpdateSource>(() => ({
    api,
    projectId,
    bookLimitBytes,
    preview: (book, key) => api.updateBookFromObsidian(projectId, book.id, { mode: 'preview' }, { idempotencyKey: key }),
    apply: (book, answer, { key, confirmUnread }) => api.updateBookFromObsidian(projectId, book.id, {
      mode: 'apply', expectedFingerprint: answer.fingerprint ?? '', ...(confirmUnread ? { confirmUnread: true } : {}),
    }, { idempotencyKey: key }),
    key: () => createIdempotencyKey('updateBookFromObsidian'),
    reads: (book) => vault?.availability !== 'missing' && vault?.books?.find((one) => one.bookId === book.id)?.presence !== 'missing',
    repeat: busyRetry,
    updateKey: obsidianKeys.update,
    working: updateWords.working,
    // The book changed: the preview of the page of the bridge is old for its books (OU2.8, the rule of the first part).
    started: () => {
      markBooksChanged(client, projectId);
      void client.invalidateQueries({ queryKey: obsidianKeys.vault(projectId) });
    },
    Window: UpdateWindow,
    link: setLinking,
    openBridge: () => {
      const at = obsidianAt(projectId);
      navigate(at.path, at.query, { from: { section: 'books' } });
    },
  }), [api, bookLimitBytes, client, navigate, projectId, vault]);
  const overlay = linking ? (
    <BookLinkWindow api={api} book={linking} onClose={() => setLinking(null)} projectId={projectId} queueLive={queueLive} vault={vault} />
  ) : null;
  return { vault, source, overlay, link: setLinking };
}

/**
 * «Связать с папкой Obsidian» of a book from «Книги» or from the window of its update (OU1.1, OU2.3): the window of the first part
 * over the folders of the look, which is read here when there is none or it is old — the same read and words as the step of the
 * choice of folders.
 */
function BookLinkWindow({ api, book, onClose, projectId, queueLive, vault }: {
  api: ObsidianClient;
  book: Book;
  onClose: () => void;
  projectId: string;
  queueLive: boolean;
  vault: ObsidianVault | undefined;
}) {
  const tree = useQuery(treeQueryOptions(api, projectId));
  const held = vault?.books?.find((one) => one.bookId === book.id) ?? vault?.relink?.find((one) => one.bookId === book.id);
  return (
    <LinkWindow api={api} books={vault?.books ?? []} folders={tree.data && !tree.isFetching ? tree.data.folders : null} onClose={onClose}
      book={{ bookId: book.id, title: book.title, ...(held ? { folder: held.folder, presence: held.presence } : {}) }}
      look={{ error: tree.error, failures: tree.failureCount, failureReason: tree.failureReason, retry: () => void tree.refetch() }}
      projectId={projectId} queueLive={queueLive} />
  );
}

/**
 * The item of the menu of a book of the app (specs/2026-11-15-desk-obsidian-book-web.md OU1; board 44·55): with the folder of the
 * saga chosen — «Обновить из Obsidian…» for a book that holds its folder, else «Связать с папкой Obsidian…»; while the saga loads
 * books it waits and says why; nothing while the folder of the saga is read or not chosen. The folder of the saga is read once
 * for the page and again at the end of a load of «Книги» (the read of the first part).
 */
export function obsidianBookMenu(api: CanonKeeperApiClient) {
  return function useObsidianBookMenu({ busy, canEdit, navigate, open, projectId, updating, uploading }: ShelfBookMenuOptions): ShelfBookMenu {
    const { link, overlay, source, vault } = useObsidianBookUpdate({ api, enabled: canEdit, navigate, projectId, queueLive: uploading });
    const items = (book: Book): ShelfBookMenuItem[] => {
      if (!canEdit || !vault?.linked) return [];
      if (vault.books?.some((one) => one.bookId === book.id)) {
        const reason = updating(book) ? updateWords.updating.already : uploading ? words.waitLoad : busy ? updateWords.updating.wait : null;
        return [{ key: 'obsidian', label: updateWords.updateItem, icon: <FolderSync aria-hidden="true" size={17} />, reason, onSelect: () => open(book, source) }];
      }
      const reason = updating(book) ? updateWords.updating.already : uploading ? words.waitLoad : null;
      return [{ key: 'obsidian', label: updateWords.linkItem, icon: <Link2 aria-hidden="true" size={17} />, reason, onSelect: () => link(book) }];
    };
    return { items, overlay };
  };
}
