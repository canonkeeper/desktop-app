import type { QueryClient } from '@tanstack/react-query';
import { manuscriptQueryKeys, type Book, type ManuscriptShelfWorkspace } from './queries';

/** A book answered with a new or removed cover replaces itself on the shelf and in the open book. */
export async function applyCoverBookResponse(queryClient: QueryClient, updated: Book) {
  const replaceBook = (shelf: ManuscriptShelfWorkspace) => ({
    ...shelf,
    books: shelf.books.map((book) => book.id === updated.id ? updated : book),
  });
  queryClient.setQueryData<ManuscriptShelfWorkspace>(
    manuscriptQueryKeys.shelf({ projectId: updated.projectId }),
    (shelf) => shelf && replaceBook(shelf),
  );
  queryClient.setQueryData<ManuscriptShelfWorkspace>(
    manuscriptQueryKeys.shelf({ projectId: undefined }),
    (shelf) => shelf?.project?.id === updated.projectId ? replaceBook(shelf) : shelf,
  );
  await Promise.all([
    queryClient.invalidateQueries({ queryKey: manuscriptQueryKeys.shelf({ projectId: updated.projectId }), exact: true }),
    queryClient.invalidateQueries({ queryKey: [...manuscriptQueryKeys.all, 'draft-workspace', updated.projectId, updated.id], exact: false }),
    queryClient.invalidateQueries({ queryKey: [...manuscriptQueryKeys.all, 'reader-workspace', updated.projectId, updated.id], exact: false }),
  ]);
}
