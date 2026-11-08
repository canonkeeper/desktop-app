import type {
  ActiveSourceListOptions,
  Book,
  BookCoverUploadOptions,
  BookExportFormat,
  DeletedSourceListOptions,
  DownloadedFile,
  ImportBookInput,
  ImportBookRequestOptions,
  OperationRequest,
  OperationResponse,
  Schema,
} from '../../../shared/api';
import {
  bookDisplayNumber,
  clone,
  createJob,
  deletedSource,
  deletedSourceRow,
  now,
  paginateList,
  requestedSourceListState,
  startResponse,
  type SourceListOptions,
} from './common';
import type { MockRepositoryContext } from './context';
import { draftBookId } from './fixtures';
import { scrubAgentWorkflows } from './workflow-scrub';
import { MockApiError } from '../scenarios';

type MockImportBookInput = {
  file:
    | string
    | File
    | {
        contentType?: string | null;
        name?: string | null;
        size?: number;
        sizeBytes?: number;
        type?: string | null;
      };
  metadata?: ImportBookInput['metadata'];
  options?: ImportBookInput['options'];
};

const allowedImportExtensions = ['.fb2', '.epub', '.docx'] as const;
type ImportExtension = (typeof allowedImportExtensions)[number];
const allowedImportMimeTypesByExtension = {
  '.epub': ['application/epub+zip'],
  '.fb2': ['application/x-fictionbook+xml', 'text/xml', 'application/xml'],
  '.docx': ['application/vnd.openxmlformats-officedocument.wordprocessingml.document'],
} as const satisfies Record<ImportExtension, readonly string[]>;
const allowedImportMimeTypes = Object.values(allowedImportMimeTypesByExtension).flat();
const maxImportFileSizeBytes = 52_428_800;
const maxCoverFileSizeBytes = 5_242_880;
const coverMimeTypes = new Set(['image/jpeg', 'image/png', 'image/webp']);

export function firstMissingBookOrder(books: readonly Book[], projectId: string) {
  const orders = new Set<number>();
  for (const book of books) {
    if (book.projectId !== projectId) continue;
    if (orders.has(book.order)) return null;
    orders.add(book.order);
  }
  for (let order = 1; ; order += 1) {
    if (!orders.has(order)) return order;
  }
}

function extensionForFileName(name: string) {
  const dot = name.lastIndexOf('.');
  return dot >= 0 ? name.slice(dot).toLowerCase() : '';
}
export function importFileDetails(file: MockImportBookInput['file']) {
  if (typeof file === 'string') {
    return {
      contentType: '',
      name: file,
      sizeBytes: 0,
    };
  }
  return {
    contentType: ('type' in file ? file.type : undefined) || ('contentType' in file ? file.contentType : undefined) || '',
    name: ('name' in file ? file.name : undefined) || '',
    sizeBytes: ('size' in file ? file.size : undefined) ?? ('sizeBytes' in file ? file.sizeBytes : undefined) ?? 0,
  };
}

function validateImportFilePart(
  input: Pick<MockImportBookInput, 'file'>,
  operationId: 'importBookFile' | 'uploadImportQueueItem',
) {
  const details = importFileDetails(input.file);
  if (!details.name) {
    throw new MockApiError(422, 'Multipart import requires a file part.', operationId);
  }
  if (details.sizeBytes > maxImportFileSizeBytes) {
    throw new MockApiError(413, 'Import file exceeds the maximum allowed size.', operationId);
  }
  return details;
}

export function validateImportFileInput(input: Pick<MockImportBookInput, 'file'>, operationId: 'importBookFile') {
  const details = validateImportFilePart(input, operationId);
  const extension = extensionForFileName(details.name) as ImportExtension;
  if (!allowedImportExtensions.includes(extension)) {
    throw new MockApiError(415, 'Import file type is not supported.', operationId);
  }
  if (
    typeof input.file !== 'string' &&
    !allowedImportMimeTypesByExtension[extension].includes(details.contentType.toLowerCase() as never)
  ) {
    throw new MockApiError(415, 'Import file type is not supported.', operationId);
  }
}

/**
 * The preflight of a queue file, as the server runs it: a refused type or an empty file keeps the
 * item with the code of its reason instead of failing the upload (UC3.4).
 */
export function importQueuePreflightError(input: Pick<MockImportBookInput, 'file'>) {
  const details = validateImportFilePart(input, 'uploadImportQueueItem');
  const extension = extensionForFileName(details.name) as ImportExtension;
  if (!allowedImportExtensions.includes(extension)) {
    return { code: 'import.unsupported_media_type', message: 'Import file type is not supported.' };
  }
  if (
    typeof input.file !== 'string' &&
    !allowedImportMimeTypesByExtension[extension].includes(details.contentType.toLowerCase() as never)
  ) {
    return { code: 'import.content_mismatch', message: 'Import file content does not match its declared type.' };
  }
  if (details.sizeBytes === 0) {
    return { code: 'empty_import_content', message: 'Import file has no usable content.' };
  }
  return null;
}

export function createBookOperations(context: MockRepositoryContext) {
  function reorderProjectBook(projectId: string, bookId: string, afterBookId: string | null | undefined, operationId: 'createBook' | 'updateBook') {
    const orderedBooks = context.state.books
      .filter((book) => book.projectId === projectId)
      .sort((left, right) => left.order - right.order);
    const movedBook = orderedBooks.find((book) => book.id === bookId);
    if (!movedBook) {
      throw new MockApiError(404, 'Resource not found.', operationId);
    }

    let nextOrder = orderedBooks;
    if (afterBookId !== undefined && afterBookId !== bookId) {
      const withoutMovedBook = orderedBooks.filter((book) => book.id !== bookId);
      const insertIndex = afterBookId === null
        ? 0
        : withoutMovedBook.findIndex((book) => book.id === afterBookId) + 1;
      if (insertIndex === 0 && afterBookId !== null) {
        throw new MockApiError(404, 'Resource not found.', operationId);
      }
      withoutMovedBook.splice(insertIndex, 0, movedBook);
      nextOrder = withoutMovedBook;
    }

    nextOrder.forEach((book, index) => {
      const order = index + 1;
      const displayNumber = bookDisplayNumber(order);
      book.order = order;
      book.displayNumber = displayNumber;
    });
    context.state.books = [
      ...nextOrder,
      ...context.state.books.filter((book) => book.projectId !== projectId),
    ];
  }

  function listBooks(id: string, options: DeletedSourceListOptions<'listBooks'>): Promise<Schema<'DeletedBookList'>>;
  function listBooks(id: string, options?: ActiveSourceListOptions<'listBooks'>): Promise<Schema<'BookList'>>;
  async function listBooks(id: string, options: SourceListOptions = {}): Promise<OperationResponse<'listBooks', '200'>> {
    context.applyOperation('listBooks');
    const state = requestedSourceListState('listBooks', options.state);
    const { state: _state, ...pagination } = options;
    context.requireProjectAccess('listBooks', id, state === 'deleted' ? ['editor', 'admin', 'owner'] : undefined);
    if (state === 'deleted') {
      const data = context.state.deletedBooks
        .filter((source) => source.resource.projectId === id)
        .map((source) => deletedSourceRow(source));
      return paginateList('listBooks', context.scenario.preset === 'empty-lists' ? [] : data, pagination, { projectId: id, state });
    }
    const data = context.state.books.filter((book) => book.projectId === id).sort((left, right) => left.order - right.order);
    return paginateList('listBooks', context.scenario.preset === 'empty-lists' ? [] : data, pagination, { projectId: id, state });
  }

  async function createBook(id: string, body: OperationRequest<'createBook'>): Promise<OperationResponse<'createBook', '201'>> {
    context.applyOperation('createBook');
    context.requireProjectAccess('createBook', id, ['editor', 'admin', 'owner']);
    if (context.scenario.preset === 'failed-create') {
      throw new MockApiError(500, 'Не удалось создать книгу.', 'createBook');
    }
    const nextOrder = body.afterBookId === undefined
      ? nextImportBookOrder(id, 'createBook')
      : context.state.books.length + 1;
    const book: Book = {
      id: `book-${context.state.books.length + 1}`,
      projectId: id,
      title: body.title,
      subtitle: body.subtitle ?? null,
      order: nextOrder,
      displayNumber: String(context.state.books.length + 1),
      coverColor: body.coverColor ?? '#4F46E5',
      coverUrl: null,
      status: 'draft',
      chapterCount: 0,
      wordCount: 0,
      createdAt: now,
      updatedAt: now,
    };
    context.state.books.push(book);
    reorderProjectBook(id, book.id, body.afterBookId, 'createBook');
    return clone(book);
  }

  async function getBook(id: string): Promise<OperationResponse<'getBook', '200'>> {
    context.applyOperation('getBook');
    return clone(context.bookFor(id, 'getBook'));
  }

  async function uploadBookCover(
    id: string,
    file: File,
    options: BookCoverUploadOptions = {},
  ): Promise<OperationResponse<'uploadBookCover', '200'>> {
    context.applyOperation('uploadBookCover');
    const book = context.bookFor(id, 'uploadBookCover', ['editor', 'admin', 'owner']);
    const candidate = file as File & { contentType?: string | null; sizeBytes?: number };
    const mimeType = (candidate.type || candidate.contentType || '').toLowerCase();
    const sizeBytes = candidate.size ?? candidate.sizeBytes ?? 0;
    if (!coverMimeTypes.has(mimeType)) {
      throw new MockApiError(415, 'Cover must be JPEG, PNG, or WebP.', 'uploadBookCover', 'cover.unsupported_media');
    }
    if (sizeBytes < 1 || sizeBytes > maxCoverFileSizeBytes) {
      throw new MockApiError(413, 'Cover exceeds 5 MiB.', 'uploadBookCover', 'cover.too_large');
    }
    const version = (context.state.bookCovers[id]?.version ?? 0) + 1;
    context.state.bookCovers[id] = { version, mimeType, sizeBytes, active: true };
    book.coverUrl = `/api/v1/books/${id}/cover/${version}`;
    book.updatedAt = now;
    options.onUploadProgress?.({ loadedBytes: sizeBytes, totalBytes: sizeBytes });
    return clone(book);
  }

  /** A drawing of the saga becomes the cover of a book (A8.6): the rules of an upload, a copy of the render. */
  async function setBookCoverFromImage(id: string, imageNumber: number): Promise<OperationResponse<'setBookCoverFromImage', '200'>> {
    context.applyOperation('setBookCoverFromImage');
    const book = context.bookFor(id, 'setBookCoverFromImage', ['editor', 'admin', 'owner']);
    const image = context.state.gallery.images.find((item) => item.projectId === book.projectId && item.number === imageNumber);
    if (!image || image.status !== 'ready') {
      throw new MockApiError(404, 'The book or the drawing is not found.', 'setBookCoverFromImage', 'not_found');
    }
    if ((image.width ?? 0) < 600 || (image.height ?? 0) < 900) {
      throw new MockApiError(422, 'Cover must be at least 600 by 900 pixels.', 'setBookCoverFromImage', 'cover.too_small');
    }
    const version = (context.state.bookCovers[id]?.version ?? 0) + 1;
    context.state.bookCovers[id] = { version, mimeType: 'image/png', sizeBytes: 1, active: true };
    book.coverUrl = `/api/v1/books/${id}/cover/${version}`;
    book.updatedAt = now;
    return clone(book);
  }

  async function getBookExport(id: string, format: BookExportFormat): Promise<DownloadedFile> {
    context.applyOperation('getBookExport');
    const book = context.bookFor(id, 'getBookExport', ['editor', 'admin', 'owner']);
    if (book.chapterCount < 1) {
      throw new MockApiError(422, 'The book has no text to export.', 'getBookExport', 'book_export.empty');
    }
    return { blob: new Blob([`Книга «${book.title}» в файле ${format}`]), fileName: `${book.title}.${format}` };
  }

  async function deleteBookCover(id: string): Promise<OperationResponse<'deleteBookCover', '200'>> {
    context.applyOperation('deleteBookCover');
    const book = context.bookFor(id, 'deleteBookCover', ['editor', 'admin', 'owner']);
    const cover = context.state.bookCovers[id];
    if (!cover?.active) {
      book.coverUrl = null;
      return clone(book);
    }
    cover.active = false;
    book.coverUrl = null;
    book.updatedAt = now;
    return clone(book);
  }

  async function updateBook(id: string, body: OperationRequest<'updateBook'>): Promise<OperationResponse<'updateBook', '200'>> {
    context.applyOperation('updateBook');
    const book = context.bookFor(id, 'updateBook', ['editor', 'admin', 'owner']);
    if (body.title !== undefined) book.title = body.title;
    if (body.subtitle !== undefined) book.subtitle = body.subtitle;
    if (body.coverColor !== undefined) book.coverColor = body.coverColor;
    book.updatedAt = now;
    if (Object.hasOwn(body, 'afterBookId')) {
      reorderProjectBook(book.projectId, book.id, body.afterBookId, 'updateBook');
    }
    return clone(book);
  }

  async function deleteBook(id: string): Promise<void> {
    context.applyOperation('deleteBook');
    const book = context.bookFor(id, 'deleteBook', ['editor', 'admin', 'owner']);
    const removedChapterIds = new Set(
      context.state.chapters.filter((chapter) => chapter.bookId === id).map((chapter) => chapter.id),
    );
    scrubAgentWorkflows(context, {
      projectId: book.projectId,
      bookIds: new Set([id]),
      chapterIds: removedChapterIds,
    });
    const removedJobIds = new Set(context.state.jobs.filter((job) => {
      if ('bookId' in job.subject && job.subject.bookId === id) return true;
      return job.result !== null && 'bookId' in job.result && job.result.bookId === id;
    }).map((job) => job.id));
    context.state.deletedBooks.push(deletedSource(book));
    context.state.books = context.state.books.filter((book) => book.id !== id);
    context.state.jobs = context.state.jobs.filter((job) => !removedJobIds.has(job.id));
    for (const jobId of removedJobIds) {
      context.jobCreators.delete(jobId);
    }
  }

  async function restoreBook(id: string): Promise<OperationResponse<'restoreBook', '200'>> {
    context.applyOperation('restoreBook');
    const sourceIndex = context.state.deletedBooks.findIndex((source) => source.resource.id === id);
    if (sourceIndex < 0) {
      throw new MockApiError(404, 'Resource not found.', 'restoreBook');
    }
    const source = context.state.deletedBooks[sourceIndex]!;
    context.requireDeletedSourceRole('restoreBook', source.resource.projectId, ['editor', 'admin', 'owner']);
    if (!context.state.projects.some((project) => project.id === source.resource.projectId)) {
      throw new MockApiError(409, 'Parent project is deleted.', 'restoreBook', 'parent_deleted');
    }
    if (context.state.books.some((book) => (
      book.projectId === source.resource.projectId && book.order === source.resource.order
    ))) {
      throw new MockApiError(409, 'Existing book order is in use.', 'restoreBook');
    }
    context.state.deletedBooks.splice(sourceIndex, 1);
    context.state.books.push(source.resource);
    return clone(source.resource);
  }

  function nextImportBookOrder(id: string, operationId: 'createBook' | 'getImportConstraints' | 'importBookFile') {
    const nextOrder = firstMissingBookOrder(context.state.books, id);
    if (nextOrder === null) {
      throw new MockApiError(409, 'Existing book order is not contiguous.', operationId, 'import_sequence_conflict');
    }
    return nextOrder;
  }

  async function getImportConstraints(_id: string): Promise<OperationResponse<'getImportConstraints', '200'>> {
    context.applyOperation('getImportConstraints');
    context.requireProjectAccess('getImportConstraints', _id);
    return {
      allowedExtensions: [...allowedImportExtensions],
      allowedMimeTypes: [...allowedImportMimeTypes],
      maxFileSizeBytes: maxImportFileSizeBytes,
      nextBookOrder: nextImportBookOrder(_id, 'getImportConstraints'),
    };
  }

  async function importBookFile(
    id: string,
    input: MockImportBookInput,
    options: ImportBookRequestOptions,
  ): Promise<OperationResponse<'importBookFile', '202'>> {
    context.applyOperation('importBookFile');
    context.requireProjectAccess('importBookFile', id, ['editor', 'admin', 'owner']);
    validateImportFileInput(input, 'importBookFile');
    return context.idempotentCommand('importBookFile', id, options, input, () => createImportBookJob(id, input));
  }

  function createImportBookJob(
    id: string,
    input: MockImportBookInput,
  ): OperationResponse<'importBookFile', '202'> {
    const updatedBookId = input.options?.bookId;
    const updatesBook = input.options?.importMode === 'update_book' || input.options?.importMode === 'update_preview';
    if (updatesBook !== Boolean(updatedBookId)
      || (updatedBookId && input.metadata?.expectedBookOrder !== undefined)) {
      throw new MockApiError(422, 'An update names its book and nothing else.', 'importBookFile', 'request.validation');
    }
    if (updatedBookId) return createBookUpdateJob(id, updatedBookId, input, input.options?.importMode === 'update_preview');
    const nextOrder = nextImportBookOrder(id, 'importBookFile');
    const expectedOrder = input.metadata?.expectedBookOrder;
    if (expectedOrder !== undefined && expectedOrder !== nextOrder) {
      throw new MockApiError(409, 'Expected book order is stale.', 'importBookFile', 'import_expected_order_conflict');
    }
    const sequence = context.state.jobs.length + 1;
    const templateChapter = context.state.chapters.find((chapter) => chapter.bookId === draftBookId);
    const job = createJob({
      id: `import-job-${sequence}`,
      kind: 'import',
      status: 'queued',
      progress: 0,
      subject: { type: 'import', id, projectId: id, expectedBookOrder: expectedOrder ?? nextOrder },
      result: {
        type: 'import',
        bookId: `import-book-${sequence}`,
        chapterCount: templateChapter ? 1 : 0,
        paragraphCount: templateChapter?.paragraphs.length ?? 0,
        importMode: 'new_book',
        sourceFileName: importFileDetails(input.file).name,
      },
      canCancel: true,
    });
    context.state.jobs.push(job);
    context.markJobCreator(job);
    return startResponse(context.visibleJob(job));
  }

  /**
   * An update keeps the book; its counters say one chapter changed by one word. Its preview says the same before
   * the update, with the place of the word (specs/2026-10-27-update-book-from-file.md, UC6).
   */
  function createBookUpdateJob(id: string, bookId: string, input: MockImportBookInput, preview: boolean): OperationResponse<'importBookFile', '202'> {
    const book = context.state.books.find((item) => item.id === bookId && item.projectId === id);
    if (!book) throw new MockApiError(404, 'Resource not found.', 'importBookFile');
    const chapters = context.state.chapters.filter((chapter) => chapter.bookId === bookId);
    const sequence = context.state.jobs.length + 1;
    const changed = chapters[0];
    const job = createJob({
      id: `import-job-${sequence}`,
      kind: 'import',
      status: 'queued',
      progress: 0,
      subject: { type: 'import', id, projectId: id, bookId, ...(preview ? { preview: true as const } : {}) },
      result: {
        type: 'import',
        bookId,
        chapterCount: chapters.length,
        paragraphCount: chapters.reduce((total, chapter) => total + chapter.paragraphs.length, 0),
        importMode: preview ? 'update_preview' : 'update_book',
        sourceFileName: importFileDetails(input.file).name,
        unchangedChapters: Math.max(chapters.length - 1, 0),
        changedChapters: changed ? 1 : 0,
        addedChapters: 0,
        removedChapters: 0,
        punctuationChapters: 0,
        renamedChapters: 0,
        ...(preview ? {
          preview: {
            newCards: changed ? 1 : 0,
            threads: false,
            chapters: changed ? [{
              order: 1,
              title: changed.title,
              change: 'words' as const,
              places: [{ before: '…отряд из пятнадцати человек вышел к реке…', after: '…отряд из тринадцати человек вышел к реке…' }],
            }] : [],
          },
        } : {}),
      },
      canCancel: true,
    });
    context.state.jobs.push(job);
    context.markJobCreator(job);
    return startResponse(context.visibleJob(job));
  }

  return {
    listBooks,
    createBook,
    getBook,
    updateBook,
    deleteBook,
    restoreBook,
    uploadBookCover,
    deleteBookCover,
    getBookExport,
    setBookCoverFromImage,
    getImportConstraints,
    importBookFile,
  };
}
