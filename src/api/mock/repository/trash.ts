import type { OperationResponse } from '../../../shared/api';
import type { MockRepositoryContext } from './context';
import { placedBook } from './books';

type RecentlyDeleted = OperationResponse<'listRecentlyDeleted', '200'>;

const managers = ['admin', 'owner'];
const editors = ['editor', 'admin', 'owner'];

function group<T>(items: T[]) {
  return { items: items.slice(0, 100), total: items.length };
}

const newestFirst = <T extends { deletedAt: string }>(left: T, right: T) => right.deletedAt.localeCompare(left.deletedAt);

/**
 * «Недавно удалённые» of the mock (specs/2026-11-11-ux7-trash.md TB1.1), by the rules of the returns: the deleted sagas of
 * an owner or administrator, the deleted books of live sagas and the deleted chapters of live books for an editor and
 * above, the deleted drawings an author may return.
 */
export function createTrashOperations(context: MockRepositoryContext) {
  async function listRecentlyDeleted(): Promise<RecentlyDeleted> {
    context.applyOperation('listRecentlyDeleted');
    const { state } = context;
    const live = new Map(state.projects.map((project) => [project.id, project]));
    const may = (projectId: string, roles: string[]) => roles.includes(context.actorRole(projectId));
    const sagas = state.deletedProjects
      .filter((source) => may(source.resource.id, managers))
      .map((source) => ({
        id: source.resource.id, title: source.resource.title, deletedAt: source.deletedAt, restoreUntil: source.restoreUntil,
        books: state.books.filter((book) => book.projectId === source.resource.id).length,
      }));
    const books = state.deletedBooks
      .filter((source) => live.has(source.resource.projectId) && may(source.resource.projectId, editors))
      .map((source) => ({
        id: source.resource.id, projectId: source.resource.projectId, sagaTitle: live.get(source.resource.projectId)!.title,
        title: source.resource.title, deletedAt: source.deletedAt, restoreUntil: source.restoreUntil,
      }));
    const chapters = state.deletedChapters.flatMap((source) => {
      const book = state.books.find((candidate) => candidate.id === source.resource.bookId);
      const saga = book && live.get(book.projectId);
      if (!book || !saga || !may(book.projectId, editors)) return [];
      return [{
        id: source.resource.id, projectId: book.projectId, sagaTitle: saga.title, bookId: book.id, bookTitle: book.title,
        bookDisplayNumber: String(placedBook(state.books, book).place), title: source.resource.title, order: source.resource.order,
        deletedAt: source.deletedAt, restoreUntil: source.restoreUntil,
      }];
    });
    // The books whose last update from a file can be returned (specs/2026-11-12-ux10-book-revert.md BR3.3).
    const bookUpdates = state.books.flatMap((book) => {
      const saga = live.get(book.projectId);
      if (!book.update || !saga || saga.demo || !may(book.projectId, editors)) return [];
      return [{
        id: book.id, projectId: book.projectId, sagaTitle: saga.title, title: book.title,
        updatedAt: book.update.at, revertUntil: book.update.revertUntil,
      }];
    });
    const principal = context.currentPrincipalId();
    const drawings = state.gallery.deletedDrawings.flatMap(({ drawing, deletedAt, restoreUntil }) => {
      const saga = live.get(drawing.projectId);
      const role = context.actorRole(drawing.projectId);
      if (!saga || !(role === 'owner' || (drawing.createdBy === principal && editors.includes(role)))) return [];
      return [{
        id: drawing.id, projectId: drawing.projectId, sagaTitle: saga.title, title: drawing.brief.title || drawing.idea.slice(0, 80),
        ...(drawing.current ? { number: drawing.current } : {}), deletedAt, restoreUntil,
      }];
    });
    return {
      sagas: group(sagas.sort(newestFirst)), books: group(books.sort(newestFirst)),
      chapters: group(chapters.sort(newestFirst)), drawings: group(drawings.sort(newestFirst)),
      bookUpdates: group(bookUpdates.sort((left, right) => right.updatedAt.localeCompare(left.updatedAt))),
    };
  }

  return { listRecentlyDeleted };
}
