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
import { firstMissingBookOrder, importFileDetails, importQueuePreflightError } from './books';
import { MockApiError, type MockActorRole } from '../scenarios';

type ImportQueue = Schema<'ImportQueue'>;
type ImportQueueItem = Schema<'ImportQueueItem'>;
type QueueUploadFile = Parameters<typeof importFileDetails>[0];
export type ImportQueueUploadInput = {
  itemId: string;
  file: QueueUploadFile;
};

const activeStatuses = new Set(['uploading', 'draft', 'running', 'failed']);
const editableStatuses = new Set(['uploading', 'draft']);

function invalid(operationId: 'createImportQueue' | 'updateImportQueue' | 'startImportQueue', message: string) {
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
  operationId: 'listImportQueues' | 'getImportQueue' | 'uploadImportQueueItem' | 'updateImportQueue' | 'startImportQueue' | 'cancelImportQueue',
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
    rejectUnknownKeys('createImportQueue', item, ['clientItemId', 'position', 'sourceFileName', 'detectedBookOrder']);
    if (!isNonEmptyString(item.clientItemId) || clientItemIds.has(item.clientItemId)) {
      throw invalid('createImportQueue', 'Import queue client item ids must be unique non-empty strings.');
    }
    if (!positiveInteger(item.position)) throw invalid('createImportQueue', 'Import queue positions must be positive integers.');
    if (!isNonEmptyString(item.sourceFileName)) throw invalid('createImportQueue', 'Import queue source filename is required.');
    if (item.detectedBookOrder !== null && !positiveInteger(item.detectedBookOrder)) {
      throw invalid('createImportQueue', 'Detected book order must be a positive integer or null.');
    }
    clientItemIds.add(item.clientItemId);
    positions.push(item.position as number);
  }
  if (!contiguous(ordered(positions))) throw invalid('createImportQueue', 'Import queue positions must be contiguous.');
  return body;
}

function validateUpdateBody(body: OperationRequest<'updateImportQueue'>, queue: ImportQueue) {
  rejectUnknownKeys('updateImportQueue', body, ['expectedVersion', 'items']);
  if (!positiveInteger(body?.expectedVersion)) throw invalid('updateImportQueue', 'Expected queue version is invalid.');
  if (!Array.isArray(body?.items) || body.items.length === 0) throw invalid('updateImportQueue', 'Import queue requires at least one item.');
  const itemIds = new Set(queue.items.map((item) => item.id));
  const positions: number[] = [];
  const orders: number[] = [];
  for (const item of body.items) {
    rejectUnknownKeys('updateImportQueue', item, ['id', 'position', 'confirmedBookOrder']);
    if (!itemIds.has(item?.id)) throw new MockApiError(404, 'Resource not found.', 'updateImportQueue');
    if (!positiveInteger(item.position) || !positiveInteger(item.confirmedBookOrder)) {
      throw invalid('updateImportQueue', 'Item positions and confirmed orders must be positive integers.');
    }
    positions.push(item.position);
    orders.push(item.confirmedBookOrder);
  }
  if (positions.length !== itemIds.size || new Set(positions).size !== positions.length || !contiguous(ordered(positions))) {
    throw invalid('updateImportQueue', 'Import queue items must map to contiguous positions.');
  }
  const sortedOrders = ordered(orders);
  if (new Set(sortedOrders).size !== sortedOrders.length || sortedOrders[0] !== queue.nextBookOrder
    || sortedOrders.some((order, index) => order !== queue.nextBookOrder + index)) {
    throw invalid('updateImportQueue', 'Confirmed book orders must be contiguous and start at the next project volume.');
  }
  return body;
}

function requireVersion(queue: ImportQueue, expectedVersion: number, operationId: 'updateImportQueue' | 'startImportQueue') {
  if (queue.version !== expectedVersion) {
    throw new MockApiError(409, 'Import queue version is stale.', operationId, 'version_conflict');
  }
}

function nextOrder(context: MockRepositoryContext, projectId: string, operationId: 'createImportQueue' | 'startImportQueue') {
  const next = firstMissingBookOrder(context.state.books, projectId);
  if (next === null) {
    throw new MockApiError(409, 'Existing book order is not contiguous.', operationId, 'import_sequence_conflict');
  }
  return next;
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
      const queueId = `import-queue-${context.state.nextImportQueueId}`;
      const nextBookOrder = nextOrder(context, projectId, 'createImportQueue');
      const queue: ImportQueue = {
        id: queueId,
        projectId,
        status: 'uploading',
        version: 1,
        intendedItemCount: request.items.length,
        nextBookOrder,
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
          detectedBookOrder: item.detectedBookOrder,
          // Like the backend, only a saved draft confirms a volume number; start requires it.
          confirmedBookOrder: null,
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
      if (!editableStatuses.has(queue.status)) {
        throw new MockApiError(409, 'Only an editable import queue accepts uploads.', 'uploadImportQueueItem', 'queue_not_editable');
      }
      const item = queue.items.find((candidate) => candidate.id === input.itemId);
      if (!item) throw new MockApiError(404, 'Resource not found.', 'uploadImportQueueItem');
      const details = importFileDetails(input.file);
      Object.assign(item, {
        sourceFileName: details.name,
        mimeType: details.contentType || null,
        sizeBytes: details.sizeBytes,
        preflightStatus: preflightError ? 'invalid' : 'ready',
        preflightError,
        processingStatus: preflightError ? 'uploading' : 'ready',
        updatedAt: now,
      });
      queue.status = queue.items.every((candidate) => candidate.preflightStatus === 'ready') ? 'draft' : 'uploading';
      queue.version += 1;
      queue.updatedAt = now;
      return projection(queue);
    });
  }

  async function updateImportQueue(
    projectId: string,
    queueId: string,
    body: OperationRequest<'updateImportQueue'>,
  ): Promise<OperationResponse<'updateImportQueue', '200'>> {
    context.applyOperation('updateImportQueue');
    const queue = queueFor(context, projectId, queueId, 'updateImportQueue', ['editor', 'admin', 'owner']);
    const request = validateUpdateBody(body, queue);
    requireVersion(queue, request.expectedVersion, 'updateImportQueue');
    if (!editableStatuses.has(queue.status)) {
      throw new MockApiError(409, 'Only an editable import queue can be saved.', 'updateImportQueue', 'queue_not_editable');
    }
    for (const item of request.items) {
      const target = queue.items.find((candidate) => candidate.id === item.id);
      if (!target) throw new MockApiError(404, 'Resource not found.', 'updateImportQueue');
      Object.assign(target, {
        position: item.position,
        confirmedBookOrder: item.confirmedBookOrder,
        updatedAt: now,
      });
    }
    queue.version += 1;
    queue.updatedAt = now;
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
      requireVersion(queue, body.expectedVersion, 'startImportQueue');
      if (queue.status !== 'draft') throw new MockApiError(409, 'Only a ready draft import queue can start.', 'startImportQueue', 'queue_not_draft');
      if (queue.items.some((item) => item.preflightStatus !== 'ready' || item.confirmedBookOrder === null)) {
        throw invalid('startImportQueue', 'Every import queue item must pass preflight and confirm its volume.');
      }
      const nextBookOrder = nextOrder(context, projectId, 'startImportQueue');
      const orders = ordered(queue.items.map((item) => item.confirmedBookOrder!));
      if (orders[0] !== nextBookOrder || orders.some((order, index) => order !== nextBookOrder + index)) {
        throw invalid('startImportQueue', 'Import queue volumes must be contiguous and start at the next project volume.');
      }
      for (const item of [...queue.items].sort((left, right) => left.position - right.position)) {
        const jobId = `${queue.id}-import-job-${item.position}`;
        const job = createJob({
          id: jobId,
          kind: 'import',
          status: 'queued',
          progress: 0,
          subject: { type: 'import', id: projectId, projectId, expectedBookOrder: item.confirmedBookOrder! },
          result: {
            type: 'import',
            bookId: `${queue.id}-book-${item.position}`,
            chapterCount: 1,
            paragraphCount: 1,
            importMode: 'new_book',
            sourceFileName: item.sourceFileName,
          },
          canCancel: true,
        });
        context.state.jobs.push(job);
        context.markJobCreator(job);
        Object.assign(item, {
          importJobId: jobId,
          processingStatus: 'queued',
          updatedAt: now,
        });
      }
      queue.status = 'running';
      queue.version += 1;
      queue.currentItemId = queue.items[0]!.id;
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
      if (item.processingStatus === 'failed') {
        item.updatedAt = now;
      } else if (item.processingStatus === 'succeeded' || item.processingStatus === 'indexing') {
        // Импортированный том сохраняет книгу, а её индекс черновика завершается уже без очереди.
        continue;
      } else if (item.id === currentItem?.id) {
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
    updateImportQueue,
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

export function canProcessImportQueueJob(state: MockState, jobId: string) {
  const linked = linkedQueueItem(state, jobId);
  if (!linked) return true;
  return linked.queue.items
    .filter((item) => item.position < linked.item.position)
    .every((item) => item.processingStatus === 'succeeded' && item.bookId !== null);
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
    queue.currentItemId = item.id;
  } else if (job?.status === 'failed') {
    queue.status = 'failed';
    queue.currentItemId = item.id;
    item.processingStatus = 'failed';
    item.failureClass = 'unexpected_deterministic_failure';
    item.failedStep = 'import';
    item.updatedAt = now;
  } else {
    return;
  }
  queue.version += 1;
  queue.updatedAt = now;
  refreshLinks(queue);
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
    const current = queue.items.find((item) => item.processingStatus !== 'succeeded');
    queue.currentItemId = current?.id ?? null;
    if (queue.status === 'running' && !current) {
      queue.status = 'succeeded';
      queue.completedAt = now;
    }
    queue.version += 1;
    queue.updatedAt = now;
    refreshLinks(queue);
  }
}
