import type {
  OperationRequest,
  OperationResponse,
  Schema,
  SourceView,
} from '../../../shared/api';
import {
  hashString,
  isNonEmptyString,
  isRecord,
  stableStringify,
} from './common';
import type { MockRepositoryContext } from './context';
import { textCoverage } from './indexing';
import {
  createCanonicalSourceLocator,
  createSourceAnchor,
  syntheticChapterExcerpt,
} from './fixtures';
import { MockApiError } from '../scenarios';

type SearchResult = Schema<'ChapterSearchResult'>;
type SearchScope = Schema<'SearchScope'>;
type SearchProjectBody = NonNullable<OperationRequest<'searchProject'>>;
type SearchProjectOptions = Partial<Pick<SearchProjectBody, 'cursor' | 'filters' | 'limit'>> & { signal?: AbortSignal };
type SearchProjectValidationOptions = {
  allowSignal?: boolean;
};

function searchCursor(fingerprint: string, offset: number) {
  return `search:${fingerprint}:${offset}`;
}

function parseSearchCursor(cursor: string | null | undefined) {
  if (!cursor) return null;
  const match = /^search:([0-9a-f]+):(\d+)$/.exec(cursor);
  if (!match) return null;
  return { fingerprint: match[1], offset: Number(match[2]) };
}

function validateSearchRequest(query: string, scope: SearchScope, options: SearchProjectOptions, validationOptions: SearchProjectValidationOptions = {}) {
  const optionObject = options as Record<string, unknown>;
  const allowedOptionKeys = new Set(['cursor', 'filters', 'limit']);
  if (validationOptions.allowSignal !== false) {
    allowedOptionKeys.add('signal');
  }
  for (const key of Object.keys(optionObject)) {
    if (!allowedOptionKeys.has(key)) {
      throw new MockApiError(400, 'Search request contains unsupported keys.', 'searchProject', 'validation_failed');
    }
  }
  if (typeof query !== 'string' || query.trim().length === 0) {
    throw new MockApiError(400, 'Search query must be a non-empty string.', 'searchProject', 'validation_failed');
  }
  if (query.length > 500) {
    throw new MockApiError(400, 'Search query must be 500 characters or fewer.', 'searchProject', 'validation_failed');
  }
  const allowedScopes = new Set<SearchScope>(['all', 'chapters']);
  if (!allowedScopes.has(scope)) {
    throw new MockApiError(400, 'Search scope is invalid.', 'searchProject', 'validation_failed');
  }
  if (options.cursor !== undefined && typeof options.cursor !== 'string') {
    throw new MockApiError(400, 'Search cursor must be a string.', 'searchProject', 'validation_failed');
  }
  if (options.limit !== undefined && (!Number.isInteger(options.limit) || options.limit < 1 || options.limit > 50)) {
    throw new MockApiError(400, 'Search limit must be an integer between 1 and 50.', 'searchProject', 'validation_failed');
  }
  return { options, query: query.trim(), scope };
}

async function searchChapters(context: MockRepositoryContext, body: OperationRequest<'searchChapters'>): Promise<OperationResponse<'searchChapters', '200'>> {
    context.applyOperation('searchChapters');
    if (!isRecord(body) || typeof body.query !== 'string' || Object.keys(body).some((key) => !['query', 'sourceView', 'limit', 'cursor'].includes(key))
      || body.limit === null || body.cursor === null || body.cursor !== undefined && (typeof body.cursor !== 'string' || !body.cursor || body.cursor.length > 512)) {
      throw new MockApiError(400, 'Invalid chapter search.', 'searchChapters');
    }
    const query = body.query.trim().toLocaleLowerCase();
    const limit = body.limit ?? 10;
    if (!query || Array.from(query).length > 500 || !['draft', 'published'].includes(body.sourceView) || !Number.isInteger(limit) || limit < 1 || limit > 50) {
      throw new MockApiError(400, 'Invalid chapter search.', 'searchChapters');
    }
    const number = /^(?:глава\s+)?(\d+)$/.exec(query)?.[1]?.replace(/^0+(?=\d)/, '');
    const rows: OperationResponse<'searchChapters', '200'>['data'] = [];
    for (const project of context.state.projects) {
      const role = context.actorRole(project.id);
      if (!['owner', 'admin', 'editor', 'viewer'].includes(role) || body.sourceView === 'draft' && role === 'viewer') continue;
      for (const book of context.state.books.filter((entry) => entry.projectId === project.id)) {
        const chapters = context.state.chapters.filter((entry) => entry.bookId === book.id).sort((a, b) => a.order - b.order);
        for (const chapter of chapters) {
          const revision = body.sourceView === 'draft' ? chapter.draftRevision : chapter.publishedRevision;
          const chapterNumber = /^Глава (\d+)\.(?: |$)/.exec(chapter.title)?.[1] ?? chapter.navigation.displayNumber;
          if (!revision || (number !== undefined ? chapterNumber.replace(/^0+(?=\d)/, '') !== number : !chapter.title.toLocaleLowerCase().includes(query))) continue;
          rows.push({ projectTitle: project.title, bookTitle: book.title, chapterTitle: chapter.title, chapterNumber,
            locator: { projectId: project.id, bookId: book.id, chapterId: chapter.id, targetView: body.sourceView, revision } });
        }
      }
    }
    rows.sort((a, b) => `${a.locator.projectId}/${a.locator.bookId}/${a.locator.chapterId}`.localeCompare(`${b.locator.projectId}/${b.locator.bookId}/${b.locator.chapterId}`));
    const fingerprint = hashString(stableStringify({ query, view: body.sourceView, limit, actor: context.currentPrincipalId() }));
    const cursor = parseSearchCursor(body.cursor);
    if (body.cursor && (!cursor || cursor.fingerprint !== fingerprint)) throw new MockApiError(400, 'Invalid chapter cursor.', 'searchChapters');
    const offset = cursor?.offset ?? 0;
    const data = rows.slice(offset, offset + limit);
    const hasMore = offset + data.length < rows.length;
    return { data, meta: { hasMore, ...(hasMore ? { nextCursor: searchCursor(fingerprint, offset + data.length) } : {}) } };
  }

function validateSearchFilters(filters: SearchProjectBody['filters'] | undefined) {
  if (filters === undefined) {
    return null;
  }
  if (!isRecord(filters)) {
    throw new MockApiError(400, 'Search filters must be an object.', 'searchProject', 'validation_failed');
  }
  const filterObject = filters as Record<string, unknown>;
  const allowedKeys = new Set(['bookId', 'chapterId', 'resultKinds', 'updatedSince']);
  for (const key of Object.keys(filterObject)) {
    if (!allowedKeys.has(key)) {
      throw new MockApiError(400, 'Search filters contain unsupported keys.', 'searchProject', 'validation_failed');
    }
  }
  if (filterObject.bookId !== undefined && !isNonEmptyString(filterObject.bookId)) {
    throw new MockApiError(400, 'Search ID filters must be non-empty strings.', 'searchProject', 'validation_failed');
  }
  if (filterObject.chapterId !== undefined && !isNonEmptyString(filterObject.chapterId)) {
    throw new MockApiError(400, 'Search ID filters must be non-empty strings.', 'searchProject', 'validation_failed');
  }
  if (filterObject.resultKinds !== undefined) {
    const allowedKinds = new Set<SearchResult['kind']>(['chapter']);
    if (
      !Array.isArray(filterObject.resultKinds) ||
      filterObject.resultKinds.length > 4 ||
      new Set(filterObject.resultKinds).size !== filterObject.resultKinds.length ||
      filterObject.resultKinds.some((kind) => typeof kind !== 'string' || !allowedKinds.has(kind as SearchResult['kind']))
    ) {
      throw new MockApiError(400, 'Search resultKinds filter is invalid.', 'searchProject', 'validation_failed');
    }
  }
  if (filterObject.updatedSince !== undefined && (typeof filterObject.updatedSince !== 'string' || Number.isNaN(Date.parse(filterObject.updatedSince)))) {
    throw new MockApiError(400, 'Search updatedSince filter is invalid.', 'searchProject', 'validation_failed');
  }
  return filterObject as {
    bookId?: string;
    chapterId?: string;
    resultKinds?: Array<SearchResult['kind']>;
    updatedSince?: string;
  };
}

export function createReaderOperations(context: MockRepositoryContext) {
  async function searchProject(
    id: string,
    query: string,
    sourceView: SourceView,
    scope: SearchScope = 'all',
    options: SearchProjectOptions = {},
    validationOptions: SearchProjectValidationOptions = {},
  ): Promise<OperationResponse<'searchProject', '200'>> {
    context.applyOperation('searchProject');
    const readableView = context.requireReadableSourceView('searchProject', id, sourceView);
    const request = validateSearchRequest(query, scope, options, validationOptions);
    const sourceLocator = { ...createCanonicalSourceLocator(readableView), projectId: id };
    const chapter = context.state.chapters.find((item) => item.id === sourceLocator.chapterId && item.bookId === sourceLocator.bookId);
    const revision = readableView === 'draft' ? chapter?.draftRevision : chapter?.publishedRevision;
    const paragraphIndex = chapter?.paragraphs.findIndex((paragraph) => paragraph.text.toLocaleLowerCase().includes(request.query.toLocaleLowerCase())) ?? -1;
    const chapterLocator: Schema<'ChapterSearchLocator'> | null = chapter && revision ? {
      ...sourceLocator, sourceRevision: revision,
      sourceAnchor: createSourceAnchor({ ...sourceLocator, sourceRevision: revision }, chapter, paragraphIndex),
    } : null;
    const allResults: SearchResult[] = context.scenario.preset === 'empty-lists' || !chapterLocator
      ? []
      : [{ id: 'search-chapter-1', kind: 'chapter', title: 'Белый порт', excerpt: chapter?.paragraphs[paragraphIndex]?.text.slice(0, 240) ?? syntheticChapterExcerpt, score: 0.97, locator: chapterLocator }];
    const filters = validateSearchFilters(request.options.filters);
    const filtered = allResults.filter((result) => {
      if (!context.locatorHasLiveParent(result.locator)) return false;
      if (filters?.bookId && result.locator.bookId !== filters.bookId) return false;
      if (filters?.chapterId && result.locator.chapterId !== filters.chapterId) return false;
      return true;
    });
    const limit = request.options.limit ?? 10;
    const fingerprint = hashString(stableStringify({ filters, limit, projectId: id, query: request.query, scope: request.scope, sourceView: readableView }));
    const cursor = parseSearchCursor(request.options.cursor);
    if (request.options.cursor && (!cursor || cursor.fingerprint !== fingerprint)) {
      throw new MockApiError(400, 'Search cursor no longer matches the request fingerprint.', 'searchProject');
    }
    const offset = cursor?.offset ?? 0;
    const data = filtered.slice(offset, offset + limit);
    const nextOffset = offset + data.length;
    return {
      query: request.query,
      scope: request.scope,
      textCoverage: textCoverage(context.state, id, readableView),
      data,
      meta: { hasMore: nextOffset < filtered.length, nextCursor: nextOffset < filtered.length ? searchCursor(fingerprint, nextOffset) : null },
      cursorFingerprint: fingerprint,
      snippetPolicy: 'bounded_240_chars',
    };
  }

  return {
    searchProject,
    searchChapters: (body: OperationRequest<'searchChapters'>) => searchChapters(context, body),
  };
}
