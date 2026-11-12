import type {
  ActiveSourceListOptions,
  Book,
  BookCoverUploadOptions,
  BookExportFormat,
  BookExportOptions,
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

// The kinds of a book file as the server names them (specs/2026-11-10-book-file-kinds.md K1.2).
const allowedImportExtensions = ['.fb2', '.epub', '.docx', '.txt', '.md', '.rtf', '.odt', '.fb2.zip'] as const;
type ImportExtension = (typeof allowedImportExtensions)[number];
const allowedImportMimeTypesByExtension = {
  '.fb2': ['application/x-fictionbook+xml', 'text/xml', 'application/xml'],
  '.epub': ['application/epub+zip'],
  '.docx': ['application/vnd.openxmlformats-officedocument.wordprocessingml.document'],
  '.txt': ['text/plain'],
  '.md': ['text/markdown', 'text/x-markdown', 'text/plain'],
  '.rtf': ['application/rtf', 'text/rtf'],
  '.odt': ['application/vnd.oasis.opendocument.text'],
  '.fb2.zip': ['application/zip', 'application/x-zip-compressed'],
} as const satisfies Record<ImportExtension, readonly string[]>;
const allowedImportMimeTypes = [...new Set(Object.values(allowedImportMimeTypesByExtension).flat())];
// A browser names a text by nothing or by octet-stream; the server takes it as a text (K1.2).
const unnamedTextExtensions = new Set<ImportExtension>(['.txt', '.md']);
const maxImportFileSizeBytes = 52_428_800;
const maxCoverFileSizeBytes = 5_242_880;
const coverMimeTypes = new Set(['image/jpeg', 'image/png', 'image/webp']);

/**
 * The stored order of a new book, as the backend gives it: after the living books of the shelf (specs/2026-11-11-ux7-upload.md
 * UP2.3) — the place a deleted book keeps for its return is not filled. Null when two books share an order.
 */
export function nextShelfOrder(books: readonly Book[], projectId: string) {
  const orders = new Set<number>();
  for (const book of books) {
    if (book.projectId !== projectId) continue;
    if (orders.has(book.order)) return null;
    orders.add(book.order);
  }
  return Math.max(0, ...orders) + 1;
}

/** The kind of a book file by the end of its name, the longest first (K1.1); '' for none. */
function extensionForFileName(name: string): ImportExtension | '' {
  const lower = name.trim().toLowerCase();
  return [...allowedImportExtensions].sort((left, right) => right.length - left.length).find((kind) => lower.endsWith(kind)) ?? '';
}

/** The name of a book file without the end of its kind: the title of a book that names none. */
export function importFileTitle(name: string) {
  const kind = extensionForFileName(name);
  return kind ? name.slice(0, name.length - kind.length) : name;
}

function importMimeTypeMatches(extension: ImportExtension, contentType: string) {
  const type = contentType.toLowerCase();
  if (unnamedTextExtensions.has(extension) && (type === '' || type === 'application/octet-stream')) return true;
  return (allowedImportMimeTypesByExtension[extension] as readonly string[]).includes(type);
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
  const extension = extensionForFileName(details.name);
  if (!extension) {
    throw new MockApiError(415, 'Import file type is not supported.', operationId);
  }
  if (typeof input.file !== 'string' && !importMimeTypeMatches(extension, details.contentType)) {
    throw new MockApiError(415, 'Import file type is not supported.', operationId);
  }
}

/**
 * The preflight of a queue file, as the server runs it: a refused type or an empty file keeps the
 * item with the code of its reason instead of failing the upload (UC3.4).
 */
export function importQueuePreflightError(input: Pick<MockImportBookInput, 'file'>) {
  const details = validateImportFilePart(input, 'uploadImportQueueItem');
  const extension = extensionForFileName(details.name);
  if (!extension) {
    return { code: 'import.unsupported_media_type', message: 'Import file type is not supported.' };
  }
  if (typeof input.file !== 'string' && !importMimeTypeMatches(extension, details.contentType)) {
    return { code: 'import.content_mismatch', message: 'Import file content does not match its declared type.' };
  }
  if (details.sizeBytes === 0) {
    return { code: 'empty_import_content', message: 'Import file has no usable content.' };
  }
  return null;
}

/**
 * A book as the backend gives it: «Книга N» is its rank among the living books of its saga in the order of the shelf,
 * 1…n without gaps (specs/2026-11-11-ux7-reorder.md RK1.6), whatever the stored order holds.
 */
export function placedBook(books: readonly Book[], book: Book): Book {
  const shelf = books.filter((each) => each.projectId === book.projectId)
    .sort((left, right) => left.order - right.order || left.id.localeCompare(right.id));
  return { ...clone(book), place: shelf.findIndex((each) => each.id === book.id) + 1 };
}

export function createBookOperations(context: MockRepositoryContext) {
  const out = (book: Book) => placedBook(context.state.books, book);
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
    const data = context.state.books.filter((book) => book.projectId === id).sort((left, right) => left.order - right.order)
      .map((book) => withOpening(out(book)));
    return paginateList('listBooks', context.scenario.preset === 'empty-lists' ? [] : data, pagination, { projectId: id, state });
  }

  /** How a book begins, as the listing of the server says (specs/2026-11-11-ux9-shelf-chat.md SC1.3). */
  function withOpening(book: Book): Book {
    const first = context.state.chapters.filter((chapter) => chapter.bookId === book.id).sort((left, right) => left.order - right.order)[0];
    const text = (first?.paragraphs ?? []).map((paragraph) => paragraph.text.trim()).filter(Boolean).slice(0, 4).join(' ').slice(0, 300);
    return text ? { ...book, opening: text } : book;
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
      place: 0,
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
    return out(book);
  }

  async function getBook(id: string): Promise<OperationResponse<'getBook', '200'>> {
    context.applyOperation('getBook');
    return out(context.bookFor(id, 'getBook'));
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
    return out(book);
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
    return out(book);
  }

  async function getBookExport(id: string, format: BookExportFormat, options: BookExportOptions = {}): Promise<DownloadedFile> {
    context.applyOperation('getBookExport');
    const book = context.bookFor(id, 'getBookExport', ['editor', 'admin', 'owner']);
    if (book.chapterCount < 1) {
      throw new MockApiError(422, 'The book has no text to export.', 'getBookExport', 'book_export.empty');
    }
    const drawings = options.drawings ? ' с рисунками' : '';
    return { blob: new Blob([`Книга «${book.title}» в файле ${format}${drawings}`]), fileName: `${book.title}.${format}` };
  }

  async function deleteBookCover(id: string): Promise<OperationResponse<'deleteBookCover', '200'>> {
    context.applyOperation('deleteBookCover');
    const book = context.bookFor(id, 'deleteBookCover', ['editor', 'admin', 'owner']);
    const cover = context.state.bookCovers[id];
    if (!cover?.active) {
      book.coverUrl = null;
      return out(book);
    }
    cover.active = false;
    book.coverUrl = null;
    book.updatedAt = now;
    return out(book);
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
    return out(book);
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
    // The snapshot of its last update goes with the book (specs/2026-11-12-ux10-book-revert.md BR1.2).
    delete book.update;
    context.state.deletedBooks.push(deletedSource(book));
    context.state.books = context.state.books.filter((book) => book.id !== id);
    // The running cards and thread layer of the saga stop with the book, as the fence does
    // (specs/2026-11-08-deletion-stops-processing.md R1.1.1).
    context.state.jobs = context.state.jobs
      .filter((job) => !removedJobIds.has(job.id))
      .map((job) => job.subject.projectId === book.projectId && job.status === 'running'
        && (job.kind === 'chapter_cards' || job.kind === 'saga_layer')
        ? { ...job, status: 'canceling' as const, canCancel: false }
        : job);
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
    return out(source.resource);
  }

  /** The fingerprint of a book whose last update can be returned: it changes with the update. */
  function revertFingerprint(book: Book) {
    return [...`${book.id}:${book.update?.at ?? ''}`].reduce((hash, sign) => (hash * 31 + sign.charCodeAt(0)) >>> 0, 7)
      .toString(16).padStart(8, '0').repeat(4);
  }

  /** A book of a saga the author edits whose last update from a file can be returned (BR2.4). */
  function updatedBook(id: string, operationId: 'previewBookUpdateRevert' | 'revertBookUpdate') {
    const book = context.bookFor(id, operationId, ['editor', 'admin', 'owner']);
    if (context.state.projects.find((project) => project.id === book.projectId)?.demo) {
      throw new MockApiError(409, 'The demo saga takes no text.', operationId, 'demo_saga.read_only');
    }
    if (context.state.jobs.some((job) => job.kind === 'import' && job.subject.type === 'import' && job.subject.bookId === id
      && !job.subject.preview && (job.status === 'queued' || job.status === 'running' || job.status === 'canceling'))) {
      throw new MockApiError(409, 'The book is being updated from a file.', operationId, 'book.busy');
    }
    if (!book.update || book.update.revertUntil <= now) {
      throw new MockApiError(404, 'The book has no update to return from.', operationId, 'book.update_gone');
    }
    return book as Book & { update: NonNullable<Book['update']> };
  }

  /**
   * What «Вернуть прежнюю версию» would do (specs/2026-11-12-ux10-book-revert.md BR2.1): the update of the mock changes the
   * words of the first chapter, so its text comes back.
   */
  async function previewBookUpdateRevert(id: string): Promise<OperationResponse<'previewBookUpdateRevert', '200'>> {
    context.applyOperation('previewBookUpdateRevert');
    const book = updatedBook(id, 'previewBookUpdateRevert');
    const changed = context.state.chapters.filter((chapter) => chapter.bookId === id).sort((left, right) => left.order - right.order)[0];
    return {
      bookId: book.id, bookTitle: book.title, updatedAt: book.update.at, revertUntil: book.update.revertUntil,
      fingerprint: revertFingerprint(book),
      text: changed ? [{ id: changed.id, title: changed.title }] : [], place: [], returned: [], removed: [], kept: [],
    };
  }

  /** The return itself (BR2.2): as the preview said, or `book.update_changed`; the update of the book is gone after it. */
  async function revertBookUpdate(id: string, body: OperationRequest<'revertBookUpdate'>): Promise<OperationResponse<'revertBookUpdate', '200'>> {
    context.applyOperation('revertBookUpdate');
    const book = updatedBook(id, 'revertBookUpdate');
    if (body.fingerprint !== revertFingerprint(book)) {
      throw new MockApiError(409, 'The book changed after the preview.', 'revertBookUpdate', 'book.update_changed');
    }
    delete (book as Book).update;
    book.updatedAt = now;
    return out(book);
  }

  function nextImportBookOrder(id: string, operationId: 'createBook' | 'importBookFile') {
    const nextOrder = nextShelfOrder(context.state.books, id);
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
        // The preview tells what an update would do, the update keeps what it did (specs/2026-11-11-ux7-trash.md TB4.1).
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
    previewBookUpdateRevert,
    revertBookUpdate,
    uploadBookCover,
    deleteBookCover,
    getBookExport,
    setBookCoverFromImage,
    getImportConstraints,
    importBookFile,
  };
}
