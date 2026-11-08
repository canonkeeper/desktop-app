import type { CanonKeeperApiClient, NotesListEntry, NotesListOptions, PersonalNote } from '../../../shared/api';
import { MockApiError } from '../scenarios';
import { clone, paginateList, rejectUnknownKeys } from './common';
import type { MockRepositoryContext } from './context';

type NotesOperations = Pick<CanonKeeperApiClient, 'listNotes' | 'createPersonalNote' | 'getPersonalNote' | 'updatePersonalNote' | 'deletePersonalNote' | 'resolvePersonalNoteSource'>;

function validateQuery(options: NotesListOptions) {
  rejectUnknownKeys('listNotes', options, ['query', 'source', 'projectId', 'bookId', 'limit', 'cursor', 'signal']);
  if ((options.query !== undefined && (typeof options.query !== 'string' || Array.from(options.query.trim()).length > 200)) ||
      (options.source !== undefined && !['free', 'chat', 'chapter'].includes(options.source)) ||
      [options.projectId, options.bookId].some((id) => id !== undefined && (typeof id !== 'string' || !id.trim())) ||
      (options.bookId !== undefined && !options.projectId)) {
    throw new MockApiError(400, 'Invalid notes query.', 'listNotes', 'validation_failed');
  }
  return { query: options.query?.trim().toLowerCase() ?? '', source: options.source, projectId: options.projectId, bookId: options.bookId };
}

function validateText(body: unknown, title: unknown, operation: 'createPersonalNote' | 'updatePersonalNote') {
  if ((body !== undefined && (typeof body !== 'string' || !body.trim() || Array.from(body).length > 100000)) ||
      (title !== undefined && (typeof title !== 'string' || Array.from(title).length > 200)) ||
      (body === undefined && (operation === 'createPersonalNote' || title === undefined))) {
    throw new MockApiError(422, 'Invalid note text or title.', operation, 'validation_failed');
  }
}

export function createNotesOperations(context: MockRepositoryContext): NotesOperations {
  function personal(id: string) {
    const note = context.state.personalNotes.find((entry) => entry.id === id && entry.ownerUserId === context.currentPrincipalId());
    if (!note) throw new MockApiError(404, 'Note not found.', 'getPersonalNote');
    return note;
  }
  function dto(note: PersonalNote): PersonalNote {
    return clone({ id: note.id, title: note.title, body: note.body, revision: note.revision, createdAt: note.createdAt, updatedAt: note.updatedAt, source: note.source });
  }
  function sourceScope(note: PersonalNote) {
    const source = note.source;
    if (!source) return {};
    try {
      if (source.kind === 'chat') {
        const chat = context.state.chats.find((entry) => entry.id === source.chatId);
        if (!chat) return {};
        context.requireReadableSourceView('listNotes', chat.projectId, chat.sourceView);
        return { projectId: chat.projectId, projectTitle: context.projectFor(chat.projectId, 'listNotes').title };
      }
      context.requireReadableSourceView('listNotes', source.locator.projectId, source.locator.targetView);
      if (!context.locatorHasLiveParent(source.locator)) return {};
      return {
        projectId: source.locator.projectId, bookId: source.locator.bookId,
        projectTitle: context.projectFor(source.locator.projectId, 'listNotes').title,
        bookTitle: context.bookFor(source.locator.bookId, 'listNotes').title,
        chapterTitle: context.chapterFor(source.locator.chapterId, 'listNotes').title,
      };
    } catch (error) {
      if (!(error instanceof MockApiError) || ![403, 404].includes(error.status)) throw error;
      return {};
    }
  }
  return {
    /** Search accessible notes before paging; the catalog receives summaries only. */
    async listNotes(options = {}) {
      context.applyOperation('listNotes');
      const filters = validateQuery(options);
      const entries: NotesListEntry[] = [];
      function include(entry: NotesListEntry, text: string, scope: { projectId?: string; bookId?: string }) {
        if ((filters.source && filters.source !== entry.sourceKind) ||
            (filters.projectId && filters.projectId !== scope.projectId) || (filters.bookId && filters.bookId !== scope.bookId) ||
            !text.toLowerCase().includes(filters.query)) return;
        entries.push(entry);
      }
      for (const note of context.state.personalNotes) {
        if (note.ownerUserId !== context.currentPrincipalId()) continue;
        const { projectId, bookId, ...titles } = sourceScope(note);
        include({ id: note.id, sourceKind: note.source?.kind ?? 'free', title: note.title,
          preview: Array.from(note.body).slice(0, 240).join(''), updatedAt: note.updatedAt, ...titles }, `${note.title}\n${note.body}`, { projectId, bookId });
      }
      entries.sort((a, b) => b.updatedAt.localeCompare(a.updatedAt) || b.id.localeCompare(a.id));
      const page = paginateList('listNotes', entries, { limit: options.limit ?? 30, cursor: options.cursor }, { account: context.currentPrincipalId(), ...filters });
      return { ...page, meta: { ...page.meta, totalCount: entries.length } };
    },
    async createPersonalNote(input, options) {
      context.applyOperation('createPersonalNote');
      return context.idempotentCommand('createPersonalNote', context.currentPrincipalId(), options, input, () => {
        validateText(input.body, input.title, 'createPersonalNote');
        const now = new Date().toISOString();
        const note = { id: crypto.randomUUID(), title: input.title ?? '', body: input.body, source: clone(input.source ?? null), revision: 1, createdAt: now, updatedAt: now, ownerUserId: context.currentPrincipalId() };
        context.state.personalNotes.push(note);
        return dto(note);
      });
    },
    async getPersonalNote(id) { context.applyOperation('getPersonalNote'); return dto(personal(id)); },
    async updatePersonalNote(id, input) {
      context.applyOperation('updatePersonalNote');
      const note = personal(id);
      if (note.revision !== input.expectedRevision) throw new MockApiError(409, 'Revision conflict.', 'updatePersonalNote');
      validateText(input.body, input.title, 'updatePersonalNote');
      Object.assign(note, { body: input.body ?? note.body, title: input.title ?? note.title, revision: note.revision + 1, updatedAt: new Date().toISOString() });
      return dto(note);
    },
    async deletePersonalNote(id, revision) {
      context.applyOperation('deletePersonalNote');
      const note = personal(id);
      if (note.revision !== revision) throw new MockApiError(409, 'Revision conflict.', 'deletePersonalNote');
      context.state.personalNotes = context.state.personalNotes.filter((entry) => entry !== note);
    },
    async resolvePersonalNoteSource(id) {
      context.applyOperation('resolvePersonalNoteSource');
      const source = personal(id).source;
      if (!source) return { state: 'unavailable' };
      try {
        if (source.kind === 'chat') {
          const chat = context.state.chats.find((entry) => entry.id === source.chatId);
          if (!chat) return { state: 'unavailable' };
          context.requireReadableSourceView('resolvePersonalNoteSource', chat.projectId, chat.sourceView);
          return { state: 'current', source: { ...source, projectId: chat.projectId, sourceView: chat.sourceView } };
        }
        const chapter = context.chapterFor(source.locator.chapterId, 'resolvePersonalNoteSource');
        context.requireReadableSourceView('resolvePersonalNoteSource', source.locator.projectId, source.locator.targetView);
        if (chapter.revision !== source.locator.revision) return { state: 'changed', source: { ...source, locator: { ...source.locator, revision: chapter.revision, paragraphId: undefined, range: undefined } } };
        return { state: 'current', source: clone(source) };
      } catch (error) {
        if (!(error instanceof MockApiError) || ![403, 404].includes(error.status)) throw error;
        return { state: 'unavailable' };
      }
    },
  };
}
