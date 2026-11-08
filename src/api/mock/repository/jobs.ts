import type { Job, OperationResponse } from '../../../shared/api';
import {
  clone,
  findById,
  hashString,
  isTerminalJobStatus,
  now,
  stableStringify,
  type ProjectJobsQuery,
} from './common';
import type { MockRepositoryContext } from './context';
import { draftBookId } from './fixtures';
import { firstMissingBookOrder } from './books';
import { canProcessImportQueueJob, reconcileImportQueueAfterJob } from './import-queues';
import { advanceIndexingJob, queueImportTextIndexes } from './indexing';
import { completeAgentJob } from './agent-job-transition';
import { MockApiError } from '../scenarios';

function jobCursor(fingerprint: string, offset: number) {
  return `jobs:${fingerprint}:${offset}`;
}

function parseJobCursor(cursor: string | null | undefined) {
  if (!cursor) return null;
  const match = /^jobs:([0-9a-f]+):(\d+)$/.exec(cursor);
  if (!match) return null;
  return { fingerprint: match[1], offset: Number(match[2]) };
}

const schemaJobKinds = new Set<Job['kind']>([
  'import',
  'canon_text_index',
  'chapter_cards',
  'research_chat_turn',
]);

function validateProjectJobsQuery(query: ProjectJobsQuery = {}) {
  if (query.kind !== undefined && !schemaJobKinds.has(query.kind)) {
    throw new MockApiError(400, 'Job kind filter is invalid.', 'listProjectJobs', 'validation_failed');
  }
  const limit = query.limit ?? 50;
  if (!Number.isInteger(limit) || limit < 1 || limit > 100) {
    throw new MockApiError(400, 'Job list limit must be an integer between 1 and 100.', 'listProjectJobs', 'validation_failed');
  }
  return {
    active: query.active ?? false,
    cursor: query.cursor ?? null,
    kind: query.kind ?? null,
    limit,
  };
}

export function createJobOperations(context: MockRepositoryContext) {
  async function listProjectJobs(id: string, query: ProjectJobsQuery = {}): Promise<OperationResponse<'listProjectJobs', '200'>> {
    context.applyOperation('listProjectJobs');
    context.requireProjectAccess('listProjectJobs', id);
    const { active, cursor: rawCursor, kind, limit } = validateProjectJobsQuery(query);
    const jobs = context.state.jobs.filter(
      (job) => job.subject.projectId === id && context.jobIsVisible(job) && (!kind || job.kind === kind)
        && (!active || !isTerminalJobStatus(job.status)),
    ).reverse();
    const fingerprint = hashString(stableStringify({ active, kind, limit, projectId: id }));
    const cursor = parseJobCursor(rawCursor);
    if (rawCursor && (!cursor || cursor.fingerprint !== fingerprint)) {
      throw new MockApiError(400, 'Job list cursor no longer matches the request fingerprint.', 'listProjectJobs', 'invalid_cursor');
    }
    const offset = cursor?.offset ?? 0;
    const data = jobs.slice(offset, offset + limit).map((job) => context.visibleJob(job));
    const nextOffset = offset + data.length;
    return {
      data,
      meta: { hasMore: nextOffset < jobs.length, nextCursor: nextOffset < jobs.length ? jobCursor(fingerprint, nextOffset) : null },
    };
  }

  /** A waiting mock question waits behind the earlier waiting ones (quota spec R10.3). */
  function withQueuePosition(job: Job): Job {
    if (job.kind !== 'research_chat_turn' || job.status !== 'queued') return job;
    const ahead = context.state.jobs.filter((other) => other.kind === 'research_chat_turn'
      && other.status === 'queued' && other.createdAt < job.createdAt).length;
    return { ...job, queuePosition: ahead };
  }

  async function getJob(id: string): Promise<OperationResponse<'getJob', '200'>> {
    context.applyOperation('getJob');
    const job = context.jobFor(id, 'getJob');
    completeAgentJob(job, context.scenario.turnFailure);
    advanceIndexingJob(context.state, job);
    if (
      job.kind === 'import'
      && job.subject.type === 'import'
      && job.result?.type === 'import'
      && !isTerminalJobStatus(job.status)
      && canProcessImportQueueJob(context.state, job.id)
    ) {
      const result = job.result;
      if (!context.state.books.some((book) => book.id === result.bookId)) {
        const nextOrder = firstMissingBookOrder(context.state.books, job.subject.projectId);
        const expectedOrder = job.subject.expectedBookOrder ?? nextOrder;
        if (nextOrder === null || expectedOrder !== nextOrder) {
          const conflictCode = nextOrder === null
            ? 'import_sequence_conflict'
            : 'import_expected_order_conflict';
          Object.assign(job, {
            status: 'failed',
            progress: 1,
            canCancel: false,
            error: {
              type: 'about:blank',
              title: 'Import conflict',
              status: 409,
              code: conflictCode,
              requestId: 'req_mock_import',
            },
          result: null,
          updatedAt: now,
        });
        reconcileImportQueueAfterJob(context.state, job.id);
        return context.visibleJob(job);
        }
        const templateBook = context.state.books.find((book) => book.id === draftBookId)!;
        const templateChapter = context.state.chapters.find((chapter) => chapter.bookId === draftBookId);
        const importedChapter = templateChapter ? {
          ...clone(templateChapter),
          id: `${result.bookId}-chapter-1`,
          bookId: result.bookId,
          title: result.sourceFileName.replace(/\.[^.]+$/, ''),
          // FB2, EPUB and DOCX publish the imported text at once (`publish_imported`).
          status: 'published' as const,
          publishedAt: now,
          paragraphs: templateChapter.paragraphs.map((paragraph, index) => ({
            ...clone(paragraph),
            id: `${result.bookId}-paragraph-${index + 1}`,
          })),
          navigation: { ...clone(templateChapter.navigation), total: 1 },
        } : null;
        context.state.books.push({
          ...clone(templateBook),
          id: result.bookId,
          projectId: job.subject.projectId,
          title: result.sourceFileName.replace(/\.[^.]+$/, ''),
          subtitle: null,
          order: nextOrder,
          displayNumber: String(nextOrder),
          status: 'draft',
          chapterCount: importedChapter ? 1 : 0,
          wordCount: importedChapter?.wordCount ?? 0,
          createdAt: now,
          updatedAt: now,
        });
        if (importedChapter) context.state.chapters.push(importedChapter);
      }
      Object.assign(job, { status: 'succeeded', progress: 1, canCancel: false, updatedAt: now });
      queueImportTextIndexes(context.state, job.subject.projectId);
      reconcileImportQueueAfterJob(context.state, job.id);
    }
    return withQueuePosition(context.visibleJob(job));
  }

  async function cancelJob(id: string): Promise<OperationResponse<'cancelJob', '200' | '204'>> {
    context.applyOperation('cancelJob');
    const storedJob = findById(context.state.jobs, id, 'cancelJob');
    const creator = context.jobCreators.get(storedJob.id);
    if (
      context.actorRole(storedJob.subject.projectId) === 'viewer' &&
      storedJob.kind === 'research_chat_turn' &&
      storedJob.subject.type === 'research_chat_turn' &&
      storedJob.subject.sourceView === 'draft' &&
      storedJob.status === 'running' &&
      storedJob.canCancel &&
      creator?.principalId === context.currentPrincipalId()
    ) {
      context.requireProjectAccess('cancelJob', storedJob.subject.projectId);
      Object.assign(storedJob, { status: 'canceled', canCancel: false, updatedAt: now });
      return undefined;
    }
    const job = context.jobFor(id, 'cancelJob');
    context.requireJobCancelAccess(job);
    if (!job.canCancel || isTerminalJobStatus(job.status)) {
      return context.visibleJob(job);
    }
    Object.assign(job, { status: 'canceled', progress: job.progress, canCancel: false, updatedAt: now });
    if (job.kind === 'import') {
      const bookId = job.result?.type === 'import' ? job.result.bookId : undefined;
      const book = bookId ? context.state.books.find((candidate) => candidate.id === bookId) : undefined;
      if (book) {
        book.status = 'error';
        book.updatedAt = now;
      }
    }
    return context.visibleJob(job);
  }

  return {
    listProjectJobs,
    getJob,
    cancelJob,
  };
}

