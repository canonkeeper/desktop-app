// SPDX-License-Identifier: AGPL-3.0-only
import { useMutationState } from '@tanstack/react-query';
import { createContext, useCallback, useContext, useMemo, type ReactNode } from 'react';
import { fileUpdateKey } from '../../../features/manuscript/api';
import {
  BookUpdateWordsProvider, type BookUpdateWords, type UpdateSourceLines,
} from '../../../features/manuscript/shelf/book-update-words';
import { obsidianKeys, useObsidianVault, type ObsidianClient } from './obsidian-api';
import { updateWords } from './words';

type VaultClient = Pick<ObsidianClient, 'getObsidianVault'>;

const VaultClientContext = createContext<VaultClient | null>(null);

function useVaultClient() {
  const api = useContext(VaultClientContext);
  if (!api) throw new Error('The words of the update of a book of the app are read outside of their provider.');
  return api;
}

const noBooks: ReadonlySet<string> = new Set();

/** The books of a saga that hold a folder of Obsidian (`getObsidianVault.books`): their title is not the name of a file (OU3.4). */
function useFolderBooksOfTheApp(projectId: string | null): ReadonlySet<string> {
  const vault = useObsidianVault(useVaultClient(), projectId ?? '', Boolean(projectId)).data;
  return useMemo(() => (vault?.books?.length ? new Set(vault.books.map((book) => book.bookId)) : noBooks), [vault]);
}

/** The lines of an update of a book from its folder (Data «итог обновления»). */
const obsidianLines: UpdateSourceLines = { done: updateWords.done, failed: updateWords.failed };
/** The lines of an update whose source the app does not know: the neutral line of success, the neutral repeat. */
const unknownLines: UpdateSourceLines = { failed: updateWords.failedUnknown };

/** The Jobs of the updates this window started under a key of the cache of the mutations, joined in one stable string. */
function useStartedUnder(mutationKey: readonly unknown[]) {
  return useMutationState({
    filters: { mutationKey, status: 'success' },
    select: (mutation) => (mutation.state.data as { job?: { id?: string } } | undefined)?.job?.id ?? '',
  }).join(',');
}

/**
 * The lines of the result of an update (OU3.2) by what this window started — the server keeps no source of an update: «Обновить
 * книгу» of «Обновить из Obsidian» says «из Obsidian», an update from a file of this window says the words of the file (null),
 * and an update the window did not start (the app restarted while it went) is told without a source, whatever the book holds.
 */
function useSourceLinesOfTheApp() {
  const fromFolder = useStartedUnder(obsidianKeys.update);
  const fromFile = useStartedUnder(fileUpdateKey);
  return useCallback((jobId: string): UpdateSourceLines | null => (
    fromFolder.split(',').includes(jobId) ? obsidianLines : fromFile.split(',').includes(jobId) ? null : unknownLines
  ), [fromFile, fromFolder]);
}

/** The words of the app (specs/2026-11-15-desk-obsidian-book-web.md, Data): no file where the app does not know, no site. */
const appWords: BookUpdateWords = {
  updated: updateWords.updated,
  revert: updateWords.revert,
  trashTitle: updateWords.trashTitle,
  updating: updateWords.updating,
  edits: updateWords.edits,
  forecast: false,
  useFolderBooks: useFolderBooksOfTheApp,
  useSourceLines: useSourceLinesOfTheApp,
};

/** The words of the update of a book of the app over its pages and its shell (`wrapPages`, OU3.1). */
export function AppBookUpdateWords({ api, children }: { api: VaultClient; children?: ReactNode }) {
  return (
    <VaultClientContext.Provider value={api}>
      <BookUpdateWordsProvider value={appWords}>{children}</BookUpdateWordsProvider>
    </VaultClientContext.Provider>
  );
}
