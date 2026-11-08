import type { CanonKeeperApiClient, ListAuthorMemoryOptions, OperationRequest, Schema } from '../../../shared/api';
import { MockApiError } from '../scenarios';
import { clone, rejectUnknownKeys } from './common';
import type { MockRepositoryContext } from './context';

type AuthorMemoryOperations = Pick<CanonKeeperApiClient, 'listAuthorMemory' | 'createAuthorMemory' | 'updateAuthorMemory' | 'deleteAuthorMemory'>;
type MemoryRecord = Schema<'AuthorMemoryRecord'>;
type Draft = OperationRequest<'createAuthorMemory'>;

const writers = ['owner', 'admin', 'editor'] as const;
const kinds: readonly string[] = ['plan', 'correction', 'style_rule', 'rejected'];

function validated(operationId: 'createAuthorMemory' | 'updateAuthorMemory', draft: Draft) {
  const text = typeof draft.text === 'string' ? draft.text.trim() : '';
  if (!kinds.includes(draft.kind) || !text || Array.from(text).length > 8000) {
    throw new MockApiError(400, 'Invalid record.', operationId, 'validation_failed');
  }
  return text;
}

/** The memory of the author of the mock saga; every project starts with none. */
export function createAuthorMemoryOperations(context: MockRepositoryContext) {
  const records = new Map<string, MemoryRecord[]>();
  /** The Job of the chat turn each record was remembered in. */
  const jobOf = new Map<string, string>();
  let nextId = 1;
  const ofProject = (projectId: string) => records.get(projectId) ?? [];
  const store = (projectId: string, list: MemoryRecord[]) => records.set(projectId, list);
  const fields = (operationId: 'createAuthorMemory' | 'updateAuthorMemory', projectId: string, draft: Draft) => {
    const text = validated(operationId, draft);
    const key = draft.chapter?.trim();
    if (key && key !== '312') throw new MockApiError(422, 'No such chapter.', operationId, 'author_memory.no_chapter');
    return {
      kind: draft.kind, text,
      ...(key ? { chapter: { key, chapterId: 'chapter-01-01', bookId: 'book-1', bookOrder: 1, bookTitle: 'Том', title: 'Глава 312' } } : {}),
      ...(draft.name?.trim() ? { name: draft.name.trim() } : {}),
      projectId,
    };
  };
  const record = (projectId: string, id: string, operationId: 'updateAuthorMemory' | 'deleteAuthorMemory') => {
    const found = ofProject(projectId).find((item) => item.id === id);
    if (!found) throw new MockApiError(404, 'Record not found.', operationId);
    return found;
  };

  /**
   * What the mock chat writes with saga.remember in the turn of a Job (specs/2026-11-02-writer-workshop.md, Decision log
   * 03.11): a record the feed can undo. It is no operation of the API.
   */
  async function agentRemember(projectId: string, jobId: string, draft: Draft) {
    const created = await operations.createAuthorMemory(projectId, draft, { idempotencyKey: `idem-remember-${nextId}` });
    jobOf.set(created.id, jobId);
    return created;
  }

  const operations: AuthorMemoryOperations = {
    async listAuthorMemory(projectId, options: ListAuthorMemoryOptions = {}) {
      context.requireProjectAccess('listAuthorMemory', projectId);
      rejectUnknownKeys('listAuthorMemory', options, ['kind', 'q', 'jobId', 'limit', 'cursor', 'signal']);
      const query = options.q?.trim().toLowerCase() ?? '';
      const data = ofProject(projectId).filter((item) => (!options.kind || item.kind === options.kind) &&
        (!options.jobId || jobOf.get(item.id) === options.jobId) &&
        (!query || item.text.toLowerCase().includes(query) || item.name?.toLowerCase().includes(query)));
      return clone({ data, meta: { hasMore: false, nextCursor: null, totalCount: data.length } });
    },
    async createAuthorMemory(projectId, body) {
      context.requireProjectAccess('createAuthorMemory', projectId, [...writers]);
      const { projectId: _project, ...values } = fields('createAuthorMemory', projectId, body);
      const at = new Date().toISOString();
      const created: MemoryRecord = {
        ...values, id: `memory-${nextId++}`, revision: 1, author: context.state.user.displayName, createdAt: at, updatedAt: at,
      };
      store(projectId, [created, ...ofProject(projectId)]);
      return clone(created);
    },
    async updateAuthorMemory(projectId, recordId, body) {
      context.requireProjectAccess('updateAuthorMemory', projectId, [...writers]);
      const current = record(projectId, recordId, 'updateAuthorMemory');
      if (body.expectedRevision !== current.revision) throw new MockApiError(409, 'Stale revision.', 'updateAuthorMemory', 'conflict');
      const { projectId: _project, ...values } = fields('updateAuthorMemory', projectId, body);
      const updated: MemoryRecord = {
        id: current.id, author: current.author, createdAt: current.createdAt, ...values,
        revision: current.revision + 1, updatedAt: new Date().toISOString(),
      };
      store(projectId, ofProject(projectId).map((item) => (item.id === recordId ? updated : item)));
      return clone(updated);
    },
    async deleteAuthorMemory(projectId, recordId, expectedRevision) {
      context.requireProjectAccess('deleteAuthorMemory', projectId, [...writers]);
      const current = record(projectId, recordId, 'deleteAuthorMemory');
      if (expectedRevision !== current.revision) throw new MockApiError(409, 'Stale revision.', 'deleteAuthorMemory', 'conflict');
      store(projectId, ofProject(projectId).filter((item) => item.id !== recordId));
    },
  };
  return { ...operations, agentRemember };
}
