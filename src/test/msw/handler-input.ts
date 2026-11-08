import { Buffer } from 'node:buffer';
import type { MockRepository } from '../../api/mock/repository';
import { MockApiError } from '../../api/mock/scenarios';
import type { FeedbackImageUploadInput, ImageUploadInput, ImportBookInput, ImportQueueItemUploadInput, OperationParameters } from '../../shared/api';

export type JsonObject = Record<string, unknown>;
type FileWithMockContent = File & { mockContent?: string };
type ProjectJobsQuery = NonNullable<OperationParameters<'listProjectJobs'>['query']>;
type CursorListQuery = {
  cursor?: string;
  limit?: number;
};
type SourceListQuery = NonNullable<OperationParameters<'listProjects'>['query']>;

export async function readJson(request: Request): Promise<JsonObject> {
  const body = await request.json();
  return body && typeof body === 'object' && !Array.isArray(body) ? (body as JsonObject) : {};
}

export async function readImportForm(request: Request): Promise<ImportBookInput> {
  const contentType = request.headers.get('content-type') ?? '';
  if (contentType.includes('multipart/form-data')) {
    const raw = Buffer.from(await request.arrayBuffer());
    const file = extractMultipartFilePart(raw);
    if (!file) {
      throw new MockApiError(422, 'Multipart import requires a file part.', 'importBookFile');
    }
    const upload = Object.assign(new File([new Uint8Array(file.body)], file.name, { type: file.contentType ?? 'application/octet-stream' }), {
      mockContent: file.body.toString('latin1'),
    }) as FileWithMockContent;
    return {
      file: upload,
      metadata: extractMultipartJsonPart(raw, 'metadata') as ImportBookInput['metadata'],
      options: extractMultipartJsonPart(raw, 'options') as ImportBookInput['options'],
    };
  }

  throw new MockApiError(415, 'Import expects multipart/form-data.', 'importBookFile');
}

export async function readImportQueueItemForm(request: Request): Promise<ImportQueueItemUploadInput> {
  const contentType = request.headers.get('content-type') ?? '';
  if (!contentType.includes('multipart/form-data')) {
    throw new MockApiError(415, 'Import queue upload expects multipart/form-data.', 'uploadImportQueueItem');
  }
  const raw = Buffer.from(await request.arrayBuffer());
  const file = extractMultipartFilePart(raw);
  const itemId = extractMultipartTextPart(raw, 'itemId');
  if (!file) {
    throw new MockApiError(422, 'Multipart import requires a file part.', 'uploadImportQueueItem');
  }
  if (!itemId) {
    throw new MockApiError(400, 'Multipart import queue itemId is required.', 'uploadImportQueueItem', 'validation_failed');
  }
  const upload = Object.assign(new File([new Uint8Array(file.body)], file.name, { type: file.contentType ?? 'application/octet-stream' }), {
    mockContent: file.body.toString('latin1'),
  }) as File & { mockContent?: string };
  return { itemId, file: upload };
}

/** The author's own image (illustrations R15.1): the file and the caption and edition fields. */
export async function readImageUploadForm(request: Request): Promise<ImageUploadInput> {
  const contentType = request.headers.get('content-type') ?? '';
  if (!contentType.includes('multipart/form-data')) {
    throw new MockApiError(400, 'An image upload expects multipart/form-data.', 'uploadProjectImage', 'request.validation');
  }
  const raw = Buffer.from(await request.arrayBuffer());
  const part = extractMultipartFilePart(raw);
  if (!part) {
    throw new MockApiError(400, 'An image upload requires a file part.', 'uploadProjectImage', 'request.validation');
  }
  const file = new File([new Uint8Array(part.body)], Buffer.from(part.name, 'latin1').toString('utf8'), { type: part.contentType ?? '' });
  const field = (name: string) => {
    const value = extractMultipartTextPart(raw, name);
    return value === undefined ? undefined : Buffer.from(value, 'latin1').toString('utf8');
  };
  const title = field('title');
  const consentVersion = field('consentVersion');
  return { file, ...(title !== undefined ? { title } : {}), ...(consentVersion !== undefined ? { consentVersion } : {}) };
}

export async function readBookCoverForm(request: Request): Promise<File> {
  const contentType = request.headers.get('content-type') ?? '';
  if (!contentType.includes('multipart/form-data')) {
    throw new MockApiError(400, 'Book cover expects multipart/form-data.', 'uploadBookCover');
  }
  const file = extractMultipartFilePart(Buffer.from(await request.arrayBuffer()));
  if (!file) {
    throw new MockApiError(400, 'Book cover requires a file part.', 'uploadBookCover');
  }
  return new File([new Uint8Array(file.body)], file.name, { type: file.contentType ?? 'application/octet-stream' });
}

export function projectJobsQuery(request: Request): ProjectJobsQuery {
  const searchParams = new URL(request.url).searchParams;
  const result: ProjectJobsQuery = {};
  const kind = searchParams.get('kind');
  const active = searchParams.get('active');
  const limit = searchParams.get('limit');
  const cursor = searchParams.get('cursor');
  if (kind) {
    result.kind = kind as ProjectJobsQuery['kind'];
  }
  if (active !== null) {
    if (active !== 'true' && active !== 'false') {
      throw new MockApiError(400, 'active must be true or false.', 'listProjectJobs', 'validation_failed');
    }
    result.active = active === 'true';
  }
  if (limit) {
    result.limit = Number(limit);
  }
  if (cursor) {
    result.cursor = cursor;
  }
  return result;
}

export function cursorListQuery(request: Request): CursorListQuery {
  const searchParams = new URL(request.url).searchParams;
  const result: CursorListQuery = {};
  const limit = searchParams.get('limit');
  const cursor = searchParams.get('cursor');
  if (limit) {
    result.limit = Number(limit);
  }
  if (cursor) {
    result.cursor = cursor;
  }
  return result;
}

function sourceListQuery(request: Request, operationId: 'listProjects' | 'listBooks' | 'listChapters'): SourceListQuery {
  const searchParams = new URL(request.url).searchParams;
  const result: SourceListQuery = {};
  const limit = searchParams.get('limit');
  const cursor = searchParams.get('cursor');
  const state = searchParams.get('state');
  if (limit) {
    result.limit = Number(limit);
  }
  if (cursor) {
    result.cursor = cursor;
  }
  if (state && state !== 'active' && state !== 'deleted') {
    throw new MockApiError(400, 'Source list state must be active or deleted.', operationId, 'validation_failed');
  }
  if (state === 'active' || state === 'deleted') {
    result.state = state;
  }
  return result;
}

export async function listProjectsForState(repository: MockRepository, request: Request) {
  const { state = 'active', ...options } = sourceListQuery(request, 'listProjects');
  return state === 'deleted'
    ? await repository.listProjects({ ...options, state })
    : await repository.listProjects({ ...options, state });
}

export async function listBooksForState(repository: MockRepository, projectId: string, request: Request) {
  const { state = 'active', ...options } = sourceListQuery(request, 'listBooks');
  return state === 'deleted'
    ? await repository.listBooks(projectId, { ...options, state })
    : await repository.listBooks(projectId, { ...options, state });
}

export async function listChaptersForState(repository: MockRepository, bookId: string, request: Request) {
  const { state = 'active', ...options } = sourceListQuery(request, 'listChapters');
  return state === 'deleted'
    ? await repository.listChapters(bookId, { ...options, state })
    : await repository.listChapters(bookId, { ...options, state });
}

function extractMultipartFilePart(raw: Buffer) {
  const text = raw.toString('latin1');
  const match = /Content-Disposition:[^\r\n]*name="file"[^\r\n]*filename="([^"]+)"[^\r\n]*\r?\n([\s\S]*?)\r?\n\r?\n([\s\S]*?)(?=\r?\n--)/i.exec(text);
  if (!match) {
    return null;
  }
  const headers = match[2] ?? '';
  return {
    body: Buffer.from(match[3] ?? '', 'latin1'),
    contentType: /content-type:\s*([^\r\n]+)/i.exec(headers)?.[1]?.trim() ?? null,
    name: match[1] ?? 'upload.bin',
  };
}

function extractMultipartJsonPart(raw: Buffer, name: string) {
  const text = raw.toString('latin1');
  const match = new RegExp(`Content-Disposition:[^\\r\\n]*name="${name}"[^\\r\\n]*(?:\\r?\\n(?!\\r?\\n)[^\\r\\n]*)*\\r?\\n\\r?\\n([\\s\\S]*?)(?=\\r?\\n--)`, 'i').exec(text);
  if (!match) {
    return undefined;
  }
  const part = match[1] ?? '';
  try {
    return JSON.parse(Buffer.from(part, 'latin1').toString('utf8').trim()) as unknown;
  } catch {
    throw new MockApiError(400, `Malformed multipart JSON part: ${name}.`, 'importBookFile');
  }
}

function extractMultipartTextPart(raw: Buffer, name: string) {
  const text = raw.toString('latin1');
  return new RegExp(`Content-Disposition:[^\\r\\n]*name="${name}"[^\\r\\n]*\\r?\\n\\r?\\n([\\s\\S]*?)(?=\\r?\\n--)`, 'i')
    .exec(text)?.[1]?.trim();
}

/** An import of the author's materials: a file part or a text part (specs/2026-11-04-saga-map.md, R9.2). */
export async function readMaterialsImportForm(request: Request) {
  const raw = Buffer.from(await request.arrayBuffer());
  const part = extractMultipartFilePart(raw);
  if (part) return { file: new File([new Uint8Array(part.body)], part.name) };
  const text = extractMultipartTextPart(raw, 'text');
  return { text: text === undefined ? '' : Buffer.from(text, 'latin1').toString('utf8') };
}

/** A chapter of the author for the proofreading (specs/2026-11-07-writing-proofread.md, PR2): a file or a text and the fields with it. */
export async function readChapterForm(request: Request) {
  const raw = Buffer.from(await request.arrayBuffer());
  const field = (name: string) => {
    const value = extractMultipartTextPart(raw, name);
    return value === undefined ? undefined : Buffer.from(value, 'latin1').toString('utf8');
  };
  const fields: Record<string, string | number> = {};
  for (const name of ['title', 'bookId', 'afterChapterId', 'body']) {
    const value = field(name);
    if (value) fields[name] = value;
  }
  const revision = field('expectedRevision');
  if (revision) fields.expectedRevision = Number(revision);
  const part = extractMultipartFilePart(raw);
  if (part) return { ...fields, file: new File([new Uint8Array(part.body)], Buffer.from(part.name, 'latin1').toString('utf8')) };
  return { ...fields, text: field('text') ?? '' };
}

/** An image of a draft of feedback (specs/2026-11-07-contacts.md F1.3): the file and the draft key. */
export async function readFeedbackImageForm(request: Request): Promise<FeedbackImageUploadInput> {
  const contentType = request.headers.get('content-type') ?? '';
  if (!contentType.includes('multipart/form-data')) {
    throw new MockApiError(400, 'An image upload expects multipart/form-data.', 'uploadFeedbackImage', 'request.validation');
  }
  const raw = Buffer.from(await request.arrayBuffer());
  const part = extractMultipartFilePart(raw);
  const draftKey = extractMultipartTextPart(raw, 'draftKey');
  if (!part || !draftKey) {
    throw new MockApiError(400, 'An image upload requires a file and a draft key.', 'uploadFeedbackImage', 'request.validation');
  }
  return { file: new File([new Uint8Array(part.body)], Buffer.from(part.name, 'latin1').toString('utf8'), { type: part.contentType ?? '' }), draftKey };
}
