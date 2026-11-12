import { createContext, useContext } from 'react';

/**
 * What a page asks of the one shell (specs/2026-11-10-ux2-shell-core.md SC1.7, SC2.3, SC3.2): the windows the shell owns
 * and opens over any page — «Поиск по книгам», «Недавно удалённые» and «Новая сага». Outside the shell they open nothing.
 */
export interface ShellActions {
  openDeleted: () => void;
  openNewSaga: () => void;
  /** «Поиск по книгам» of the shell with these words: «Найти в книге» of a selection in the reader. */
  searchFor: (text: string) => void;
  /**
   * «Что изменилось» of the last update of a book from a file (specs/2026-11-11-ux7-trash.md TB4.1), with «Вернуть прежнюю
   * версию…» for its 7 days (specs/2026-11-12-ux10-book-revert.md BR3.1).
   */
  openBookChanges: (update: { bookId: string; projectId: string; jobId?: string | undefined }) => void;
}

const ShellActionsContext = createContext<ShellActions>({
  openDeleted: () => undefined, openNewSaga: () => undefined, searchFor: () => undefined, openBookChanges: () => undefined,
});

export const ShellActionsProvider = ShellActionsContext.Provider;

export function useShellActions() {
  return useContext(ShellActionsContext);
}
