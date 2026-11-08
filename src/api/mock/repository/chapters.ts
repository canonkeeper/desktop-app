import type {
  ActiveSourceListOptions,
  DeletedSourceListOptions,
  OperationResponse,
  Schema,
  SourceView,
} from '../../../shared/api';
import {
  clone,
  deletedSource,
  deletedSourceRow,
  now,
  paginateList,
  requestedSourceListState,
  type SourceListOptions,
} from './common';
import type { MockRepositoryContext } from './context';
import { scrubAgentWorkflows } from './workflow-scrub';
import { MockApiError } from '../scenarios';

export function createChapterOperations(context: MockRepositoryContext) {

  function listChapters(id: string, options: DeletedSourceListOptions<'listChapters'>): Promise<Schema<'DeletedChapterList'>>;
  function listChapters(id: string, options?: ActiveSourceListOptions<'listChapters'>): Promise<Schema<'ChapterList'>>;
  async function listChapters(id: string, options: SourceListOptions = {}): Promise<OperationResponse<'listChapters', '200'>> {
    context.applyOperation('listChapters');
    const state = requestedSourceListState('listChapters', options.state);
    const { state: _state, ...pagination } = options;
    context.bookFor(id, 'listChapters', state === 'deleted' ? ['editor', 'admin', 'owner'] : undefined);
    if (state === 'deleted') {
      const data = context.state.deletedChapters
        .filter((source) => source.resource.bookId === id)
        .map((source) => deletedSourceRow(source));
      return paginateList('listChapters', context.scenario.preset === 'empty-lists' ? [] : data, pagination, { bookId: id, state });
    }
    const data = context.state.chapters
      .filter((chapter) => chapter.bookId === id)
      .sort((left, right) => left.order - right.order)
      .map((chapter) => ({
        id: chapter.id,
        bookId: chapter.bookId,
        title: chapter.title,
        order: chapter.order,
        displayNumber: String(chapter.order),
        status: chapter.status,
        wordCount: chapter.wordCount,
        hasDraft: chapter.draftRevision > (typeof chapter.publishedRevision === 'number' ? chapter.publishedRevision : 0),
      }));
    return paginateList('listChapters', context.scenario.preset === 'empty-lists' ? [] : data, pagination, { bookId: id, state });
  }

  async function getChapter(id: string, contentVariant: SourceView): Promise<OperationResponse<'getChapter', '200'>> {
    context.applyOperation('getChapter');
    if (contentVariant !== 'draft' && contentVariant !== 'published') {
      throw new MockApiError(400, 'Invalid chapter content variant.', 'getChapter');
    }
    const chapter = context.chapterFor(id, 'getChapter');
    const book = context.bookFor(chapter.bookId, 'getChapter');
    if (contentVariant === 'draft' && context.actorRole(book.projectId) === 'viewer') {
      throw new MockApiError(404, 'Resource not found.', 'getChapter');
    }
    const revision = contentVariant === 'draft' ? chapter.draftRevision : chapter.publishedRevision;
    if (revision === null) {
      throw new MockApiError(404, 'Resource not found.', 'getChapter');
    }
    return clone({
      ...chapter,
      contentVariant,
      revision,
    });
  }

  async function deleteChapter(id: string): Promise<void> {
    context.applyOperation('deleteChapter');
    const chapter = context.chapterFor(id, 'deleteChapter', ['editor', 'admin', 'owner']);
    const book = context.state.books.find((candidate) => candidate.id === chapter.bookId);
    if (!book) throw new MockApiError(404, 'Resource not found.', 'deleteChapter');
    scrubAgentWorkflows(context, {
      projectId: book.projectId,
      bookIds: new Set([book.id]),
      chapterIds: new Set([id]),
      sourceView: chapter.status === 'published' ? 'published' : 'draft',
    });
    context.state.deletedChapters.push(deletedSource(chapter));
    context.state.chapters = context.state.chapters.filter((candidate) => candidate.id !== id);
    book.chapterCount = Math.max(0, book.chapterCount - 1);
    book.wordCount = Math.max(0, book.wordCount - chapter.wordCount);
    book.updatedAt = now;
    const project = context.state.projects.find((candidate) => candidate.id === book.projectId);
    if (project) {
      project.chapterCount = Math.max(0, project.chapterCount - 1);
      project.wordCount = Math.max(0, project.wordCount - chapter.wordCount);
      project.updatedAt = now;
    }
  }

  async function restoreChapter(id: string): Promise<OperationResponse<'restoreChapter', '200'>> {
    context.applyOperation('restoreChapter');
    const sourceIndex = context.state.deletedChapters.findIndex((source) => source.resource.id === id);
    if (sourceIndex < 0) {
      throw new MockApiError(404, 'Resource not found.', 'restoreChapter');
    }
    const source = context.state.deletedChapters[sourceIndex]!;
    const activeBook = context.state.books.find((book) => book.id === source.resource.bookId);
    const deletedBook = context.state.deletedBooks.find((candidate) => candidate.resource.id === source.resource.bookId)?.resource;
    const parentBook = activeBook ?? deletedBook;
    if (!parentBook) {
      throw new MockApiError(404, 'Resource not found.', 'restoreChapter');
    }
    context.requireDeletedSourceRole('restoreChapter', parentBook.projectId, ['editor', 'admin', 'owner']);
    if (!activeBook || !context.state.projects.some((project) => project.id === parentBook.projectId)) {
      throw new MockApiError(409, 'Parent source is deleted.', 'restoreChapter', 'parent_deleted');
    }
    context.state.deletedChapters.splice(sourceIndex, 1);
    context.state.chapters.push(source.resource);
    activeBook.chapterCount += 1;
    activeBook.wordCount += source.resource.wordCount;
    activeBook.updatedAt = now;
    const project = context.state.projects.find((candidate) => candidate.id === activeBook.projectId);
    if (project) {
      project.chapterCount += 1;
      project.wordCount += source.resource.wordCount;
      project.updatedAt = now;
    }
    return clone(source.resource);
  }

  return {
    listChapters,
    getChapter,
    deleteChapter,
    restoreChapter,
  };
}
