import type {
  ImportBookRequestOptions,
  OperationRequest,
  OperationResponse,
  RequiredIdempotencyOptions,
  Schema,
} from '../../../shared/api';
import {
  clone,
  createJob,
  findById,
  isNonEmptyString,
  isRecord,
  now,
  rejectUnknownKeys,
} from './common';
import type { MockRepositoryContext } from './context';
import type { MockState } from './state';
import { importFileDetails, importQueuePreflightError, nextShelfOrder } from './books';
import { MockApiError, type MockActorRole } from '../scenarios';

type ImportQueue = Schema<'ImportQueue'>;
type ImportQueueItem = Schema<'ImportQueueItem'>;
type QueueUploadFile = Parameters<typeof importFileDetails>[0];
export type ImportQueueUploadInput = {
  itemId: string;
  file: QueueUploadFile;
};

const activeStatuses = new Set(['uploading', 'draft', 'running']);
const editableStatuses = new Set(['uploading', 'draft']);

function invalid(operationId: 'createImportQueue' | 'startImportQueue', message: string) {
  return new MockApiError(422, message, operationId, 'validation_failed');
}

function positiveInteger(value: unknown) {
  return Number.isInteger(value) && (value as number) >= 1;
}

function contiguous(values: number[]) {
  return values.every((value, index) => value === index + 1);
}

function ordered(values: number[]) {
  return [...values].sort((left, right) => left - right);
}

function queueUrl(projectId: string, queueId: string) {
  return `/api/v1/projects/${projectId}/import-queues/${queueId}`;
}

function statusUrl(projectId: string, bookId: string) {
  return `/manuscript/books?${new URLSearchParams({ projectId, bookId, sourceView: 'draft' })}`;
}

function refreshLinks(queue: ImportQueue) {
  queue.links = {
    self: queueUrl(queue.projectId, queue.id),
    cancel: activeStatuses.has(queue.status) ? queueUrl(queue.projectId, queue.id) : null,
  };
  queue.items = [...queue.items].sort((left, right) => left.position - right.position).map((item) => ({
    ...item,
    links: { status: item.bookId ? statusUrl(queue.projectId, item.bookId) : null },
  }));
}

function projection(queue: ImportQueue) {
  refreshLinks(queue);
  return clone(queue);
}

function queueFor(
  context: MockRepositoryContext,
  projectId: string,
  queueId: string,
  operationId: 'listImportQueues' | 'getImportQueue' | 'uploadImportQueueItem' | 'skipImportQueueItem' | 'startImportQueue' | 'cancelImportQueue',
  allowed?: MockActorRole[],
) {
  context.projectFor(projectId, operationId, allowed);
  const queue = findById(context.state.importQueues, queueId, operationId);
  if (queue.projectId !== projectId) {
    throw new MockApiError(404, 'Resource not found.', operationId);
  }
  return queue;
}

function validateCreateBody(body: OperationRequest<'createImportQueue'>) {
  rejectUnknownKeys('createImportQueue', body, ['items']);
  const items = body?.items;
  if (!Array.isArray(items) || items.length === 0) throw invalid('createImportQueue', 'Import queue requires at least one item.');
  const clientItemIds = new Set<string>();
  const positions: number[] = [];
  for (const item of items) {
    if (!isRecord(item)) throw invalid('createImportQueue', 'Import queue item must be an object.');
    rejectUnknownKeys('createImportQueue', item, ['clientItemId', 'position', 'sourceFileName']);
    if (!isNonEmptyString(item.clientItemId) || clientItemIds.has(item.clientItemId)) {
      throw invalid('createImportQueue', 'Import queue client item ids must be unique non-empty strings.');
    }
    if (!positiveInteger(item.position)) throw invalid('createImportQueue', 'Import queue positions must be positive integers.');
    if (!isNonEmptyString(item.sourceFileName)) throw invalid('createImportQueue', 'Import queue source filename is required.');
    clientItemIds.add(item.clientItemId);
    positions.push(item.position as number);
  }
  if (!contiguous(ordered(positions))) throw invalid('createImportQueue', 'Import queue positions must be contiguous.');
  return body;
}

function requireVersion(queue: ImportQueue, expectedVersion: number) {
  if (queue.version !== expectedVersion) {
    throw new MockApiError(409, 'Import queue version is stale.', 'startImportQueue', 'version_conflict');
  }
}

function nextOrder(context: MockRepositoryContext, projectId: string, operationId: 'createImportQueue' | 'startImportQueue') {
  const next = nextShelfOrder(context.state.books, projectId);
  if (next === null) {
    throw new MockApiError(409, 'Existing book order is not contiguous.', operationId, 'import_sequence_conflict');
  }
  return next;
}

/**
 * A volume waits for the author at its place (specs/2026-11-11-ux7-upload.md UP3.2): its import Job did not read the
 * file, or its file did not pass the check and it has no Job — and the author has not skipped it.
 */
function waits(item: ImportQueueItem) {
  return item.processingStatus === 'failed' && (item.importJobId !== null || item.preflightStatus === 'invalid');
}

/** The import Job of a volume at its place, as the start of the queue and «Заменить файл» queue it. */
function queueVolumeJob(context: MockRepositoryContext, queue: ImportQueue, item: ImportQueueItem, attempt: number) {
  const order = context.state.importQueueOrders[item.id]!;
  const jobId = attempt === 0 ? `${queue.id}-import-job-${item.position}` : `${queue.id}-import-job-${item.position}-${attempt}`;
  const job = createJob({
    id: jobId,
    kind: 'import',
    status: 'queued',
    progress: 0,
    // As the backend names a volume of a queue (specs/2026-11-11-import-speed.md IS2.3: the shelf tells only a single
    // import by its own line).
    subject: {
      type: 'import', id: queue.projectId, projectId: queue.projectId, expectedBookOrder: order,
      importQueueId: queue.id, importQueueItemId: item.id,
    },
    result: {
      type: 'import',
      bookId: attempt === 0 ? `${queue.id}-book-${item.position}` : `${queue.id}-book-${item.position}-${attempt}`,
      chapterCount: 1,
      paragraphCount: 1,
      importMode: 'new_book',
      sourceFileName: item.sourceFileName,
    },
    canCancel: true,
  });
  context.state.jobs.push(job);
  context.markJobCreator(job);
  Object.assign(item, { importJobId: jobId, processingStatus: 'queued', failureClass: null, failedStep: null, updatedAt: now });
}

export function createImportQueueOperations(context: MockRepositoryContext) {
  async function createImportQueue(
    projectId: string,
    body: OperationRequest<'createImportQueue'>,
    options: RequiredIdempotencyOptions,
  ): Promise<OperationResponse<'createImportQueue', '201'>> {
    context.applyOperation('createImportQueue');
    context.projectFor(projectId, 'createImportQueue', ['editor', 'admin', 'owner']);
    const request = validateCreateBody(body);
    return context.idempotentCommand('createImportQueue', projectId, options, request, () => {
      if (context.state.importQueues.some((queue) => queue.projectId === projectId && activeStatuses.has(queue.status))) {
        throw new MockApiError(409, 'An active import queue already exists.', 'createImportQueue', 'active_import_queue_exists');
      }
      nextOrder(context, projectId, 'createImportQueue');
      const queueId = `import-queue-${context.state.nextImportQueueId}`;
      const queue: ImportQueue = {
        id: queueId,
        projectId,
        status: 'uploading',
        version: 1,
        intendedItemCount: request.items.length,
        currentItemId: null,
        createdAt: now,
        updatedAt: now,
        startedAt: null,
        completedAt: null,
        links: { self: queueUrl(projectId, queueId), cancel: queueUrl(projectId, queueId) },
        items: request.items.map((item): ImportQueueItem => ({
          id: `${queueId}-item-${item.position}`,
          clientItemId: item.clientItemId,
          position: item.position,
          sourceFileName: item.sourceFileName,
          mimeType: null,
          sizeBytes: null,
          preflightStatus: 'uploading',
          preflightError: null,
          processingStatus: 'uploading',
          failureClass: null,
          failedStep: null,
          importJobId: null,
          bookId: null,
          updatedAt: now,
          links: { status: null },
        })),
      };
      context.state.importQueues.push(queue);
      context.state.nextImportQueueId += 1;
      return projection(queue);
    });
  }

  async function listImportQueues(projectId: string): Promise<OperationResponse<'listImportQueues', '200'>> {
    context.applyOperation('listImportQueues');
    context.projectFor(projectId, 'listImportQueues');
    return { data: context.state.importQueues.filter((queue) => queue.projectId === projectId).reverse().map(projection) };
  }

  async function getImportQueue(projectId: string, queueId: string): Promise<OperationResponse<'getImportQueue', '200'>> {
    context.applyOperation('getImportQueue');
    const queue = queueFor(context, projectId, queueId, 'getImportQueue');
    settleCanceledImportQueue(context.state, queue);
    return projection(queue);
  }

  /**
   * The file of a volume: of a queue being prepared, or «Заменить файл» of a volume that waits in a running queue —
   * checked as any file, and then given a new import Job at its place (UP3.2).
   */
  async function uploadImportQueueItem(
    projectId: string,
    queueId: string,
    input: ImportQueueUploadInput,
    options: ImportBookRequestOptions,
  ): Promise<OperationResponse<'uploadImportQueueItem', '200'>> {
    context.applyOperation('uploadImportQueueItem');
    const queue = queueFor(context, projectId, queueId, 'uploadImportQueueItem', ['editor', 'admin', 'owner']);
    rejectUnknownKeys('uploadImportQueueItem', input, ['itemId', 'file']);
    if (!isNonEmptyString(input?.itemId)) {
      throw new MockApiError(400, 'Import queue itemId is required.', 'uploadImportQueueItem', 'validation_failed');
    }
    const preflightError = importQueuePreflightError(input);
    return context.idempotentCommand('uploadImportQueueItem', `${projectId}:${queueId}`, options, input, () => {
      const item = queue.items.find((candidate) => candidate.id === input.itemId);
      if (!item) throw new MockApiError(404, 'Resource not found.', 'uploadImportQueueItem');
      const details = importFileDetails(input.file);
      if (queue.status === 'running') {
        if (!waits(item)) {
          throw new MockApiError(409, 'Only a volume that waits takes a new file.', 'uploadImportQueueItem', 'conflict');
        }
        Object.assign(item, {
          sourceFileName: details.name,
          preflightStatus: preflightError ? 'invalid' : 'ready',
          preflightError,
          updatedAt: now,
        });
        if (!preflightError) {
          Object.assign(item, { mimeType: details.contentType || null, sizeBytes: details.sizeBytes });
          context.state.importQueueReplacements[item.id] = (context.state.importQueueReplacements[item.id] ?? 0) + 1;
          queueVolumeJob(context, queue, item, context.state.importQueueReplacements[item.id]!);
        }
        queue.version += 1;
        queue.updatedAt = now;
        return projection(queue);
      }
      if (!editableStatuses.has(queue.status)) {
        throw new MockApiError(409, 'Only an editable import queue accepts uploads.', 'uploadImportQueueItem', 'queue_not_editable');
      }
      Object.assign(item, {
        sourceFileName: details.name,
        mimeType: details.contentType || null,
        sizeBytes: details.sizeBytes,
        preflightStatus: preflightError ? 'invalid' : 'ready',
        preflightError,
        processingStatus: preflightError ? 'failed' : 'ready',
        failedStep: preflightError ? 'preflight' : null,
        updatedAt: now,
      });
      // Every file received — checked or not — the queue is a draft (UP1.2).
      queue.status = queue.items.every((candidate) => candidate.preflightStatus !== 'uploading') ? 'draft' : 'uploading';
      queue.version += 1;
      queue.updatedAt = now;
      return projection(queue);
    });
  }

  /** «Пропустить» of a volume that waits (UP3.2). */
  async function skipImportQueueItem(
    projectId: string,
    queueId: string,
    itemId: string,
  ): Promise<OperationResponse<'skipImportQueueItem', '200'>> {
    context.applyOperation('skipImportQueueItem');
    const queue = queueFor(context, projectId, queueId, 'skipImportQueueItem', ['editor', 'admin', 'owner']);
    const item = queue.items.find((candidate) => candidate.id === itemId);
    if (!item) throw new MockApiError(404, 'Resource not found.', 'skipImportQueueItem');
    if (queue.status !== 'running' || !waits(item)) {
      throw new MockApiError(409, 'Only a volume that waits is skipped.', 'skipImportQueueItem', 'conflict');
    }
    Object.assign(item, { processingStatus: 'skipped', updatedAt: now });
    queue.version += 1;
    queue.updatedAt = now;
    completeImportQueue(queue);
    return projection(queue);
  }

  async function startImportQueue(
    projectId: string,
    queueId: string,
    body: OperationRequest<'startImportQueue'>,
    options: RequiredIdempotencyOptions,
  ): Promise<OperationResponse<'startImportQueue', '202'>> {
    context.applyOperation('startImportQueue');
    const queue = queueFor(context, projectId, queueId, 'startImportQueue', ['editor', 'admin', 'owner']);
    rejectUnknownKeys('startImportQueue', body, ['expectedVersion']);
    if (!positiveInteger(body?.expectedVersion)) throw invalid('startImportQueue', 'Expected queue version is invalid.');
    return context.idempotentCommand('startImportQueue', `${projectId}:${queueId}`, options, body, () => {
      requireVersion(queue, body.expectedVersion);
      if (queue.status !== 'draft') throw new MockApiError(409, 'Only a ready draft import queue can start.', 'startImportQueue', 'queue_not_draft');
      if (queue.items.some((item) => item.preflightStatus === 'uploading')) {
        throw invalid('startImportQueue', 'Every file of the queue must be received.');
      }
      // The order of the volumes is the order of the rows, after the books of the shelf (UP2.3).
      const first = nextOrder(context, projectId, 'startImportQueue');
      for (const item of [...queue.items].sort((left, right) => left.position - right.position)) {
        context.state.importQueueOrders[item.id] = first + item.position - 1;
        // A file that did not pass the check waits at its place without a Job (UP1.2).
        if (item.preflightStatus === 'ready') queueVolumeJob(context, queue, item, 0);
      }
      queue.status = 'running';
      queue.version += 1;
      queue.currentItemId = queue.items.find((item) => item.importJobId)?.id ?? null;
      queue.startedAt = now;
      queue.updatedAt = now;
      return projection(queue);
    });
  }

  async function cancelImportQueue(
    projectId: string,
    queueId: string,
  ): Promise<OperationResponse<'cancelImportQueue', '200'>> {
    context.applyOperation('cancelImportQueue');
    const queue = queueFor(context, projectId, queueId, 'cancelImportQueue', ['editor', 'admin', 'owner']);
    if (!activeStatuses.has(queue.status)) {
      throw new MockApiError(409, 'Import queue is already terminal.', 'cancelImportQueue', 'queue_not_active');
    }
    const currentItem = queue.items.find((item) => item.id === queue.currentItemId);
    for (const item of queue.items) {
      const job = item.importJobId ? context.state.jobs.find((candidate) => candidate.id === item.importJobId) : undefined;
      if (job && job.status !== 'succeeded' && job.status !== 'failed') {
        Object.assign(job, { status: 'canceled', canCancel: false, updatedAt: now });
      }
      if (item.processingStatus === 'failed' || item.processingStatus === 'skipped') {
        item.updatedAt = now;
      } else if (item.processingStatus === 'succeeded' || item.processingStatus === 'indexing') {
        // Импортированный том сохраняет книгу, а её индекс черновика завершается уже без очереди.
        continue;
      } else if (item.id === currentItem?.id && item.processingStatus === 'importing') {
        Object.assign(item, { processingStatus: 'canceling', updatedAt: now });
      } else {
        Object.assign(item, { processingStatus: 'canceled', updatedAt: now });
      }
    }
    queue.status = 'canceled';
    queue.completedAt = now;
    queue.version += 1;
    queue.updatedAt = now;
    return projection(queue);
  }

  return {
    createImportQueue,
    listImportQueues,
    getImportQueue,
    uploadImportQueueItem,
    skipImportQueueItem,
    startImportQueue,
    cancelImportQueue,
  };
}

function linkedQueueItem(state: MockState, jobId: string) {
  for (const queue of state.importQueues) {
    const item = queue.items.find((candidate) => candidate.importJobId === jobId);
    if (item) return { item, queue };
  }
  return undefined;
}

function settleCanceledImportQueue(state: MockState, queue: ImportQueue) {
  if (queue.status !== 'canceled') return;
  const item = queue.items.find((candidate) => candidate.id === queue.currentItemId);
  if (item?.processingStatus !== 'canceling') return;
  const job = item.importJobId ? state.jobs.find((candidate) => candidate.id === item.importJobId) : undefined;
  if (!job || (job.status !== 'succeeded' && job.status !== 'failed' && job.status !== 'canceled')) return;
  item.processingStatus = 'canceled';
  queue.currentItemId = null;
  queue.version += 1;
  queue.updatedAt = now;
}

/**
 * A volume starts once the volumes before it are on the shelf and read, wait for the author or were skipped
 * (UP3.2): a volume in work or a book still read holds it back.
 */
export function canProcessImportQueueJob(state: MockState, jobId: string) {
  const linked = linkedQueueItem(state, jobId);
  if (!linked) return true;
  return linked.queue.items
    .filter((item) => item.position < linked.item.position)
    .every((item) => item.processingStatus === 'skipped' || waits(item)
      || (item.processingStatus === 'succeeded' && item.bookId !== null));
}

export function reconcileImportQueueAfterJob(state: MockState, jobId: string) {
  const linked = linkedQueueItem(state, jobId);
  if (!linked || linked.queue.status !== 'running') return;
  const { item, queue } = linked;
  const job = state.jobs.find((candidate) => candidate.id === jobId);
  if (job?.status === 'succeeded' && job.result?.type === 'import') {
    // Импортированный том ждёт, пока текстовый индекс черновика сделает его книгу готовой.
    item.bookId = job.result.bookId;
    item.processingStatus = 'indexing';
    item.updatedAt = now;
  } else if (job?.status === 'failed') {
    // The volume waits for the author at its place; the volumes after it go on (UP3.2).
    item.processingStatus = 'failed';
    item.failureClass = 'unexpected_deterministic_failure';
    item.failedStep = 'import';
    item.updatedAt = now;
  } else {
    return;
  }
  queue.currentItemId = queue.items.find((candidate) => candidate.processingStatus === 'queued'
    || candidate.processingStatus === 'importing' || candidate.processingStatus === 'indexing')?.id ?? null;
  queue.version += 1;
  queue.updatedAt = now;
  refreshLinks(queue);
}

/** A running queue ends once its volumes are on the shelf and read or skipped; every volume skipped — it stops. */
function completeImportQueue(queue: ImportQueue) {
  if (queue.status !== 'running') return;
  const kept = queue.items.filter((item) => item.processingStatus !== 'skipped');
  if (kept.length === 0) queue.status = 'canceled';
  else if (kept.every((item) => item.processingStatus === 'succeeded')) queue.status = 'succeeded';
  else return;
  queue.completedAt = now;
  queue.currentItemId = null;
}

/**
 * После текстового индекса черновика том с готовой или удалённой книгой завершён:
 * очередь переходит к следующему тому, а карточки глав её не держат.
 */
export function settleImportQueueBooks(state: MockState, projectId: string) {
  for (const queue of state.importQueues) {
    if (queue.projectId !== projectId) continue;
    const settled = queue.items.filter((item) => {
      const book = state.books.find((candidate) => candidate.id === item.bookId);
      return item.processingStatus === 'indexing' && (!book || book.status === 'ready');
    });
    if (settled.length === 0) continue;
    for (const item of settled) {
      item.processingStatus = 'succeeded';
      item.updatedAt = now;
    }
    queue.currentItemId = queue.items.find((item) => item.processingStatus === 'queued'
      || item.processingStatus === 'importing' || item.processingStatus === 'indexing')?.id ?? null;
    completeImportQueue(queue);
    queue.version += 1;
    queue.updatedAt = now;
    refreshLinks(queue);
  }
}
