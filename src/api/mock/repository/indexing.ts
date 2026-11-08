import type {
  GetIndexingStatusOptions,
  Job,
  OperationRequest,
  OperationResponse,
  RequiredIdempotencyOptions,
  Schema,
  SourceView,
} from '../../../shared/api';
import {
  chapterInView,
  createJob,
  isTerminalJobStatus,
  now,
  rejectUnknownKeys,
  startResponse,
  validateSourceView,
} from './common';
import type { MockRepositoryContext } from './context';
import type { MockState } from './state';
import { settleImportQueueBooks } from './import-queues';
import { MockApiError } from '../scenarios';

type IndexingStep = OperationResponse<'getIndexingStatus', '200'>['steps'][number];
type IndexingViewEnvelope = Schema<'IndexingViewEnvelope'>;
type IndexingJobKind = Extract<Job['kind'], 'canon_text_index' | 'chapter_cards'>;
type BookIndexState = { id: string; chapters: number; indexed: number; carded: number };

function viewChapterIds(state: MockState, bookId: string, sourceView: SourceView) {
  return state.chapters
    .filter((chapter) => chapter.bookId === bookId && chapterInView(chapter, sourceView))
    .map((chapter) => chapter.id);
}

/** Книги версии текста по порядку: сколько глав, сколько из них с актуальным индексом и с карточкой. */
function viewBooks(state: MockState, projectId: string, sourceView: SourceView): BookIndexState[] {
  const indexed = new Set(state.indexedChapterIds[sourceView]);
  const carded = new Set(state.cardedChapterIds[sourceView]);
  return state.books
    .filter((book) => book.projectId === projectId)
    .sort((left, right) => left.order - right.order)
    .map((book) => {
      const chapterIds = viewChapterIds(state, book.id, sourceView);
      const current = chapterIds.filter((id) => indexed.has(id));
      return {
        id: book.id,
        chapters: chapterIds.length,
        indexed: current.length,
        carded: current.filter((id) => carded.has(id)).length,
      };
    })
    .filter((book) => book.chapters > 0);
}

function bookReady(book: BookIndexState) {
  return book.indexed === book.chapters && book.carded === book.chapters;
}

export function textCoverage(state: MockState, projectId: string, sourceView: SourceView): Schema<'TextCoverage'> {
  const books = viewBooks(state, projectId, sourceView);
  const nonCurrentBooks = books.filter((book) => book.indexed < book.chapters).length;
  return {
    currentBooks: books.length - nonCurrentBooks,
    totalBooks: books.length,
    nonCurrentBooks,
    totalNonCurrentBooks: nonCurrentBooks,
    truncated: false,
  };
}

function isIndexingJob(job: Job, kind: IndexingJobKind, sourceView: SourceView) {
  const { subject } = job;
  return (subject.type === 'canon_text_index' || subject.type === 'chapter_cards')
    && subject.type === kind
    && subject.sourceView === sourceView;
}

function isActive(job: Job | undefined) {
  return job !== undefined && !isTerminalJobStatus(job.status);
}

function latestJob(jobs: Job[], matches: (job: Job) => boolean) {
  return [...jobs].reverse().find(matches);
}

function latestTextJob(jobs: Job[], sourceView: SourceView) {
  return latestJob(jobs, (job) => isIndexingJob(job, 'canon_text_index', sourceView));
}

/** Активное построение карточек версии важнее более нового завершённого. */
function latestCardsJob(jobs: Job[], sourceView: SourceView) {
  return latestJob(jobs, (job) => isIndexingJob(job, 'chapter_cards', sourceView) && isActive(job))
    ?? latestJob(jobs, (job) => isIndexingJob(job, 'chapter_cards', sourceView));
}

function canMutate(context: MockRepositoryContext, projectId: string) {
  return ['editor', 'admin', 'owner'].includes(context.actorRole(projectId));
}

/**
 * Версия готова, когда у каждой книги есть индекс и карточки. Пока её индекс или карточки
 * в работе, текущая книга — первая неготовая; иначе автор может перезапустить индекс.
 */
function viewEnvelope(context: MockRepositoryContext, projectId: string, sourceView: SourceView): IndexingViewEnvelope {
  const books = viewBooks(context.state, projectId, sourceView);
  if (books.length === 0) return { exists: false, context: null };
  const position = books.findIndex((book) => !bookReady(book));
  const book = books[position];
  if (!book) return { exists: true, context: { state: 'ready', currentBook: null, retryableFailedBook: null } };
  const jobs = context.state.jobs.filter((job) => job.subject.projectId === projectId);
  if (isActive(latestTextJob(jobs, sourceView)) || isActive(latestCardsJob(jobs, sourceView))) {
    return {
      exists: true,
      context: { state: 'processing', currentBook: { bookId: book.id, position: position + 1, total: books.length }, retryableFailedBook: null },
    };
  }
  return {
    exists: true,
    context: { state: 'partial', currentBook: null, retryableFailedBook: canMutate(context, projectId) ? { bookId: book.id } : null },
  };
}

function stepFromJob(
  kind: IndexingStep['kind'],
  job: Job | undefined,
  canRetry: boolean,
  target: Pick<IndexingStep, 'bookId' | 'sourceView'>,
): IndexingStep {
  if (!job) return { kind, status: 'pending', progress: null, ...target };
  const terminal = isTerminalJobStatus(job.status);
  return {
    kind,
    status: job.status,
    progress: job.status === 'succeeded' ? 1 : job.progress,
    ...target,
    jobId: job.id,
    jobCreatedAt: job.createdAt,
    jobStartedAt: job.status === 'queued' ? null : job.createdAt,
    jobCompletedAt: terminal ? job.updatedAt : null,
    ...(job.status === 'queued' ? {} : { startedAt: job.createdAt }),
    ...(terminal ? { completedAt: job.updatedAt } : {}),
    ...(job.canCancel ? { canCancel: true as const } : {}),
    ...(canRetry && job.status === 'failed' ? { canRetry: true as const } : {}),
  };
}

function importStep(job: Job | undefined, canRetry: boolean, bookId: string): IndexingStep {
  const step = stepFromJob('import', job, canRetry, { bookId });
  if (!job) return step;
  const chapters = job.result?.type === 'import' ? job.result.chapterCount : undefined;
  return {
    ...step,
    importPhase: job.status === 'queued' ? 'queued' : job.status === 'succeeded' ? 'saved' : 'parsing',
    ...(chapters === undefined ? {} : { counters: { parsedChapters: chapters, savedChapters: chapters, totalChapters: chapters } }),
  };
}

function bookSteps(jobs: Job[], book: BookIndexState, sourceView: SourceView, canRetry: boolean): IndexingStep[] {
  const imported = importStep(
    latestJob(jobs, (job) => job.kind === 'import' && job.result?.type === 'import' && job.result.bookId === book.id),
    canRetry,
    book.id,
  );
  let text: IndexingStep = {
    ...stepFromJob('text_index', latestTextJob(jobs, sourceView), canRetry, { sourceView }),
    counters: { indexedChapters: book.indexed, totalChapters: book.chapters },
  };
  if (book.indexed === book.chapters) text = { ...text, status: 'succeeded', progress: 1 };
  else if (text.status === 'succeeded') text = { ...text, status: 'pending', progress: null };
  let cards: IndexingStep = {
    ...stepFromJob('cards', latestCardsJob(jobs, sourceView), canRetry, { sourceView }),
    progress: book.carded / book.chapters,
    counters: { indexedChapters: book.carded, totalChapters: book.chapters },
  };
  // Завершённое построение покрывало прежний текст: карточки этой книги построит следующее.
  if (bookReady(book)) cards = { ...cards, status: 'succeeded', progress: 1 };
  else if (cards.status === 'succeeded') cards = { ...cards, status: 'pending' };
  const succeeded = bookReady(book) && imported.status !== 'failed' && imported.status !== 'canceled';
  return [
    imported,
    text,
    cards,
    { kind: 'ready', status: succeeded ? 'succeeded' : 'pending', progress: succeeded ? 1 : null, bookId: book.id, sourceView },
  ];
}

function nextJobId(state: MockState, prefix: string) {
  let sequence = state.jobs.length + 1;
  while (state.jobs.some((job) => job.id === `${prefix}-${sequence}`)) sequence += 1;
  return `${prefix}-${sequence}`;
}

function createIndexingJob(state: MockState, kind: IndexingJobKind, projectId: string, sourceView: SourceView) {
  const job = kind === 'canon_text_index'
    ? createJob({
        id: nextJobId(state, 'canon-text-index-job'),
        kind,
        status: 'queued',
        progress: 0,
        subject: { type: kind, projectId, sourceView, catalogRevision: 7 },
        canCancel: true,
      })
    : createJob({
        id: nextJobId(state, 'chapter-cards-job'),
        kind,
        status: 'queued',
        progress: 0,
        subject: { type: kind, projectId, sourceView },
        canCancel: true,
      });
  state.jobs.push(job);
  return job;
}

/** Системное задание версии текста без создателя; ещё не начатое задание той же версии переиспользуется. */
function queueIndexingJob(state: MockState, kind: IndexingJobKind, projectId: string, sourceView: SourceView) {
  return state.jobs.find((job) => (
    job.status === 'queued' && job.subject.projectId === projectId && isIndexingJob(job, kind, sourceView)
  )) ?? createIndexingJob(state, kind, projectId, sourceView);
}

/** Успешный импорт публикует текст и ставит текстовые индексы черновика и опубликованной версии. */
export function queueImportTextIndexes(state: MockState, projectId: string) {
  queueIndexingJob(state, 'canon_text_index', projectId, 'draft');
  queueIndexingJob(state, 'canon_text_index', projectId, 'published');
}

function completeTextIndex(state: MockState, job: Job, projectId: string, sourceView: SourceView, chapterIds: string[]) {
  const indexed = state.indexedChapterIds[sourceView];
  indexed.push(...chapterIds.filter((id) => !indexed.includes(id)));
  Object.assign(job, { status: 'succeeded', progress: 1, canCancel: false, updatedAt: now });
  job.result = { type: 'canon_text_index', sourceView, textCoverage: textCoverage(state, projectId, sourceView) };
  if (sourceView === 'draft') {
    // Текст черновика уже ищется: книги готовы, а карточки строятся в фоне.
    for (const book of state.books) {
      if (book.projectId === projectId && book.status !== 'ready' && viewChapterIds(state, book.id, 'draft').length > 0) {
        book.status = 'ready';
        book.updatedAt = now;
      }
    }
    settleImportQueueBooks(state, projectId);
  }
  queueIndexingJob(state, 'chapter_cards', projectId, sourceView);
}

function completeChapterCards(state: MockState, job: Job, sourceView: SourceView, chapterIds: string[]) {
  const indexed = new Set(state.indexedChapterIds[sourceView]);
  const carded = state.cardedChapterIds[sourceView];
  const built = chapterIds.filter((id) => indexed.has(id) && !carded.includes(id));
  carded.push(...built);
  Object.assign(job, { status: 'succeeded', progress: 1, canCancel: false, updatedAt: now });
  job.result = { type: 'chapter_cards', sourceView, chapters: chapterIds.length, built: built.length };
}

/** Имитация фоновых работников: каждое чтение Job индекса или карточек продвигает его на один шаг. */
export function advanceIndexingJob(state: MockState, job: Job) {
  const { subject } = job;
  if (subject.type !== 'canon_text_index' && subject.type !== 'chapter_cards') return;
  if (job.status === 'queued') {
    job.status = 'running';
    job.updatedAt = now;
    return;
  }
  if (job.status !== 'running') return;
  const chapterIds = state.books
    .filter((book) => book.projectId === subject.projectId)
    .flatMap((book) => viewChapterIds(state, book.id, subject.sourceView));
  if (subject.type === 'canon_text_index') {
    completeTextIndex(state, job, subject.projectId, subject.sourceView, chapterIds);
  } else {
    completeChapterCards(state, job, subject.sourceView, chapterIds);
  }
}

export function createIndexingOperations(context: MockRepositoryContext) {
  async function getIndexingStatus(
    id: string,
    options: GetIndexingStatusOptions = {},
  ): Promise<OperationResponse<'getIndexingStatus', '200'>> {
    context.applyOperation('getIndexingStatus');
    context.requireProjectAccess('getIndexingStatus', id);
    const { bookId, sourceView } = options;
    if (Boolean(bookId) !== Boolean(sourceView)) {
      throw new MockApiError(400, 'Indexing status scope is incomplete.', 'getIndexingStatus', 'validation_failed');
    }
    const response: OperationResponse<'getIndexingStatus', '200'> = {
      published: viewEnvelope(context, id, 'published'),
      steps: [],
    };
    if (canMutate(context, id)) {
      response.draft = viewEnvelope(context, id, 'draft');
    }
    if (bookId && sourceView) {
      const book = viewBooks(context.state, id, sourceView).find((candidate) => candidate.id === bookId);
      if (!book) {
        throw new MockApiError(404, 'Resource not found.', 'getIndexingStatus');
      }
      const jobs = context.state.jobs.filter((job) => job.subject.projectId === id && context.jobIsVisible(job));
      response.steps = bookSteps(jobs, book, sourceView, canMutate(context, id));
    }
    return response;
  }

  async function startProjectIndexing(
    id: string,
    body: OperationRequest<'startProjectIndexing'>,
    options: RequiredIdempotencyOptions,
  ): Promise<OperationResponse<'startProjectIndexing', '202'>> {
    context.applyOperation('startProjectIndexing');
    context.requireProjectAccess('startProjectIndexing', id, ['editor', 'admin', 'owner']);
    rejectUnknownKeys('startProjectIndexing', body, ['sourceView']);
    const sourceView = validateSourceView('startProjectIndexing', body.sourceView);
    if (!context.projectHasSourceView(id, sourceView)) {
      throw new MockApiError(404, 'Resource not found.', 'startProjectIndexing');
    }
    // Перезапуск всегда создаёт новый индекс всей версии текста; его успех поставит карточки.
    return context.idempotentCommand('startProjectIndexing', id, options, body, () => {
      const job = createIndexingJob(context.state, 'canon_text_index', id, sourceView);
      context.markJobCreator(job);
      return startResponse(context.visibleJob(job));
    });
  }

  return { getIndexingStatus, startProjectIndexing };
}
