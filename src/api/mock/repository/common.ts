import type {
  Book,
  Chapter,
  Job,
  JobStartResponse,
  OperationId,
  OperationParameters,
  Schema,
  SourceView,
} from '../../../shared/api';
import { applyScenario, MockApiError } from '../scenarios';

export type User = Schema<'User'>;
export type ProjectInvitation = Schema<'ProjectInvitation'>;
export type ProjectMembership = Schema<'ProjectMembership'>;
export type CursorListOptions = {
  cursor?: string | null;
  limit?: number;
  signal?: AbortSignal;
};
export type SourceListState = 'active' | 'deleted';
export type SourceListOptions = CursorListOptions & {
  state?: SourceListState;
};
export type ProjectJobsQuery = NonNullable<OperationParameters<'listProjectJobs'>['query']>;
export type DirectIdempotencyRecord = {
  fingerprint: string;
  response: unknown;
};
export type JobCreatorRecord = {
  principalId: string;
};
export type DeletedSource<T> = {
  resource: T;
  deletedAt: string;
  restoreUntil: string;
};
export type DeletedBookSource = DeletedSource<Book>;
export type DeletedChapterSource = DeletedSource<Chapter>;

export const now = '2026-06-16T05:00:00.000Z';
export const restoreUntil = new Date(Date.parse(now) + 7 * 24 * 60 * 60 * 1000).toISOString();

const apiBasePath = '/api/v1';
const minIdempotencyKeyLength = 8;
const maxIdempotencyKeyLength = 128;
const bookDisplayNumbers = ['I', 'II', 'III', 'IV', 'V', 'VI', 'VII', 'VIII', 'IX', 'X'] as const;

function clone<T>(value: T): T {
  return structuredClone(value);
}

function deletedSource<T>(resource: T): DeletedSource<T> {
  return { resource: clone(resource), deletedAt: now, restoreUntil };
}

function deletedSourceRow<T extends { id: string; title: string }>(source: DeletedSource<T>) {
  return {
    id: source.resource.id,
    title: source.resource.title,
    deletedAt: source.deletedAt,
    restoreUntil: source.restoreUntil,
  };
}

function requestedSourceListState(operationId: OperationId, state: unknown): SourceListState {
  if (state === undefined || state === 'active') return 'active';
  if (state === 'deleted') return 'deleted';
  throw new MockApiError(400, 'Source list state must be active or deleted.', operationId, 'validation_failed');
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function isNonEmptyString(value: unknown): value is string {
  return typeof value === 'string' && value.length > 0;
}

function jobUrl(jobId: string) {
  return `${apiBasePath}/jobs/${jobId}`;
}

function rejectUnknownKeys(operationId: OperationId, value: unknown, allowedKeys: readonly string[]) {
  if (!isRecord(value)) {
    throw new MockApiError(400, 'Request body must be an object.', operationId, 'validation_failed');
  }
  const allowed = new Set(allowedKeys);
  for (const key of Object.keys(value)) {
    if (!allowed.has(key)) {
      throw new MockApiError(400, 'Request body contains unsupported keys.', operationId, 'validation_failed');
    }
  }
}

function validateSourceView(operationId: OperationId, value: unknown): SourceView {
  if (value !== 'draft' && value !== 'published') {
    throw new MockApiError(400, 'Source view must be draft or published.', operationId, 'validation_failed');
  }
  return value;
}

/** Черновик содержит каждую главу, опубликованная версия — только опубликованные. */
function chapterInView(chapter: Chapter, sourceView: SourceView) {
  return sourceView === 'draft' || chapter.publishedAt !== null;
}


function normalizedEmail(value: string) {
  return value.trim().toLowerCase();
}

function stableStringify(value: unknown): string {
  if (Array.isArray(value)) {
    return `[${value.map(stableStringify).join(',')}]`;
  }
  if (value && typeof value === 'object') {
    return `{${Object.entries(value)
      .sort(([left], [right]) => left.localeCompare(right))
      .map(([key, entry]) => `${JSON.stringify(key)}:${stableStringify(entry)}`)
      .join(',')}}`;
  }
  return JSON.stringify(value) ?? 'undefined';
}

function validateIdempotencyKey(operationId: OperationId, idempotencyKey: string) {
  if (idempotencyKey.length < minIdempotencyKeyLength || idempotencyKey.length > maxIdempotencyKeyLength) {
    throw new MockApiError(400, 'Idempotency-Key must be between 8 and 128 characters.', operationId, 'validation_failed');
  }
}

function hashBytes(bytes: Uint8Array) {
  let hash = 2166136261;
  for (const byte of bytes) {
    hash ^= byte;
    hash = Math.imul(hash, 16777619);
  }
  return (hash >>> 0).toString(16).padStart(8, '0');
}

async function fileContentHash(file: File) {
  if ('arrayBuffer' in file && typeof file.arrayBuffer === 'function') {
    return hashBytes(new Uint8Array(await file.arrayBuffer()));
  }
  if ('text' in file && typeof file.text === 'function') {
    return hashString(await file.text());
  }
  return hashString('');
}

function isFileLike(value: unknown): value is File {
  return Boolean(
    value &&
    typeof value === 'object' &&
    'name' in value &&
    typeof (value as { name?: unknown }).name === 'string' &&
    'size' in value &&
    typeof (value as { size?: unknown }).size === 'number' &&
    (
      ('arrayBuffer' in value && typeof (value as { arrayBuffer?: unknown }).arrayBuffer === 'function') ||
      ('text' in value && typeof (value as { text?: unknown }).text === 'function')
    ),
  );
}

async function idempotencyFingerprintValue(value: unknown): Promise<unknown> {
  if (isFileLike(value)) {
    return {
      contentHash: await fileContentHash(value),
      lastModified: value.lastModified,
      name: value.name,
      size: value.size,
      type: value.type,
    };
  }
  if (Array.isArray(value)) {
    return Promise.all(value.map(idempotencyFingerprintValue));
  }
  if (value && typeof value === 'object') {
    return Object.fromEntries(await Promise.all(Object.entries(value).map(async ([key, entry]) => [key, await idempotencyFingerprintValue(entry)])));
  }
  return value;
}

function hashString(value: string) {
  let hash = 2166136261;
  for (let index = 0; index < value.length; index += 1) {
    hash ^= value.charCodeAt(index);
    hash = Math.imul(hash, 16777619);
  }
  return (hash >>> 0).toString(16).padStart(8, '0');
}

function bookDisplayNumber(order: number) {
  return bookDisplayNumbers[order - 1] ?? String(order);
}

function nextChapterId(chapters: Chapter[], bookId: string) {
  const prefix = `chapter-${bookId.replace('book-', '')}-`;
  let suffix = chapters.length + 1;
  while (chapters.some((chapter) => chapter.id === `${prefix}${suffix}`)) {
    suffix += 1;
  }
  return `${prefix}${suffix}`;
}

function listCursor(operationId: OperationId, fingerprint: string, offset: number) {
  return `list:${operationId}:${fingerprint}:${offset}`;
}

function parseListCursor(cursor: string | null | undefined) {
  if (!cursor) return null;
  const match = /^list:([A-Za-z0-9]+):([0-9a-f]+):(\d+)$/.exec(cursor);
  if (!match) return null;
  return { operationId: match[1], fingerprint: match[2], offset: Number(match[3]) };
}

function validateCursorListQuery(operationId: OperationId, query: CursorListOptions = {}) {
  const allowedKeys = new Set(['cursor', 'limit', 'signal']);
  for (const key of Object.keys(query as Record<string, unknown>)) {
    if (!allowedKeys.has(key)) {
      throw new MockApiError(400, 'List query contains unsupported keys.', operationId, 'validation_failed');
    }
  }
  if (query.cursor !== undefined && query.cursor !== null && typeof query.cursor !== 'string') {
    throw new MockApiError(400, 'List cursor must be a string.', operationId, 'validation_failed');
  }
  const limit = query.limit ?? 50;
  if (!Number.isInteger(limit) || limit < 1 || limit > 100) {
    throw new MockApiError(400, 'List limit must be an integer between 1 and 100.', operationId, 'validation_failed');
  }
  return { cursor: query.cursor ?? null, limit };
}

function paginateList<T>(operationId: OperationId, data: T[], query: CursorListOptions, fingerprintInput: unknown) {
  const { cursor: rawCursor, limit } = validateCursorListQuery(operationId, query);
  const fingerprint = hashString(stableStringify({ fingerprintInput, limit, operationId }));
  const cursor = parseListCursor(rawCursor);
  if (rawCursor && (!cursor || cursor.operationId !== operationId || cursor.fingerprint !== fingerprint)) {
    throw new MockApiError(400, 'List cursor no longer matches the request fingerprint.', operationId, 'invalid_cursor');
  }
  const offset = cursor?.offset ?? 0;
  const page = data.slice(offset, offset + limit);
  const nextOffset = offset + page.length;
  return {
    data: clone(page),
    meta: { hasMore: nextOffset < data.length, nextCursor: nextOffset < data.length ? listCursor(operationId, fingerprint, nextOffset) : null },
  };
}

function createJob(input: {
  id: string;
  kind: Job['kind'];
  phase?: Job['phase'];
  status: Job['status'];
  progress: number | null;
  subject: Job['subject'];
  result?: Job['result'];
  canCancel?: boolean;
}): Job {
  return {
    id: input.id,
    kind: input.kind,
    status: input.status,
    progress: input.progress,
    phase: input.phase ?? null,
    subject: input.subject,
    result: input.result ?? null,
    error: null,
    canCancel: input.canCancel ?? !isTerminalJobStatus(input.status),
    expiresAt: null,
    createdAt: now,
    updatedAt: now,
    links: { self: jobUrl(input.id), cancel: `${jobUrl(input.id)}/cancel`, timeline: null },
  };
}

const terminalJobStatuses: readonly Job['status'][] = ['succeeded', 'failed', 'canceled'];

function isTerminalJobStatus(status: Job['status']) {
  return terminalJobStatuses.includes(status);
}

function startResponse(job: Job): JobStartResponse {
  return { jobId: job.id, job: clone(job), links: { job: jobUrl(job.id) } };
}

function findById<T extends { id: string }>(items: T[], id: string, operationId: Parameters<typeof applyScenario>[0]): T {
  const item = items.find((candidate) => candidate.id === id);
  if (!item) {
    throw new MockApiError(404, 'Resource not found.', operationId);
  }
  return item;
}

function countWords(value: string) {
  return value
    .trim()
    .split(/\s+/)
    .filter(Boolean).length;
}

export {
  bookDisplayNumber,
  chapterInView,
  clone,
  countWords,
  createJob,
  deletedSource,
  deletedSourceRow,
  findById,
  hashString,
  idempotencyFingerprintValue,
  isNonEmptyString,
  isRecord,
  isTerminalJobStatus,
  jobUrl,
  nextChapterId,
  normalizedEmail,
  paginateList,
  rejectUnknownKeys,
  requestedSourceListState,
  stableStringify,
  startResponse,
  validateIdempotencyKey,
  validateSourceView,
};
