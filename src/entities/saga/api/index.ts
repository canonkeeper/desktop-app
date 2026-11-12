import { keepPreviousData, useQueries, useQuery, type QueryClient } from '@tanstack/react-query';
import type {
  CanonKeeperApiClient,
  GetSagaContentsOptions,
  GetSagaMapNodeOptions,
  ListSagaMapHiddenOptions,
  OperationResponse,
  SearchSagaMapOptions,
  Schema,
  SourceView,
} from '../../../shared/api';

export type { SourceView };

export type SagaApiClient = Pick<CanonKeeperApiClient, 'getSagaMap' | 'getSagaMapNode' | 'searchSagaMap' | 'getSagaContents' | 'listSagaMapHidden' | 'hideSagaMapItem' | 'restoreSagaMapItem' | 'getSagaFirstQuestions'>;
export type SagaContents = OperationResponse<'getSagaContents', '200'>;
export type SagaMap = OperationResponse<'getSagaMap', '200'>;
export type SagaMapNodeCard = OperationResponse<'getSagaMapNode', '200'>;
export type SagaFirstQuestion = Schema<'SagaFirstQuestions'>['questions'][number];
export type SagaMapNode = Schema<'SagaMapNode'>;
export type SagaMapLink = Schema<'SagaMapLink'>;
export type SagaMapHiddenItem = Schema<'SagaMapHiddenItem'>;
export type SagaMapCategory = Schema<'SagaMapCategory'>;
export type SagaChapterRef = Schema<'SagaChapterRef'>;
export type SagaChange = Schema<'SagaChange'>;
export type SagaCoverage = Schema<'SagaCoverage'>;
export type SagaBook = Schema<'SagaBook'>;

type Query<T> = Omit<T, 'signal'>;

/** Saga reads live under the product root, so an identity change clears them too. */
const mapBuildingPoll = 5_000;

export const sagaQueryKeys = {
  all: ['canonkeeper', 'saga'] as const,
  projectMaps: (projectId: string) => [...sagaQueryKeys.all, 'map', projectId] as const,
  map: (projectId: string, sourceView: SourceView) => [...sagaQueryKeys.projectMaps(projectId), sourceView] as const,
  /** Under the maps of the project: a decision of the author or a record that changes the map changes them too. */
  firstQuestions: (projectId: string, sourceView: SourceView) => [...sagaQueryKeys.projectMaps(projectId), sourceView, 'first-questions'] as const,
  projectNodes: (projectId: string) => [...sagaQueryKeys.all, 'node', projectId] as const,
  node: (projectId: string, query: Query<GetSagaMapNodeOptions>, revision: string) => [...sagaQueryKeys.projectNodes(projectId), revision, compact(query)] as const,
  mapHidden: (projectId: string, query: Query<ListSagaMapHiddenOptions>) => [...sagaQueryKeys.all, 'map-hidden', projectId, compact(query)] as const,
  projectMapHidden: (projectId: string) => [...sagaQueryKeys.all, 'map-hidden', projectId] as const,
  mapSearch: (projectId: string, query: Query<SearchSagaMapOptions>) => [...sagaQueryKeys.all, 'map-search', projectId, compact(query)] as const,
  contents: (projectId: string, query: Query<GetSagaContentsOptions>) => [...sagaQueryKeys.all, 'contents', projectId, compact(query)] as const,
};

/** Keys and requests carry only the parameters that are set. */
function compact<T extends object>(query: T): T {
  return Object.fromEntries(Object.entries(query).filter(([, value]) => value !== undefined && value !== '')) as T;
}

interface SagaQueryMeta extends Record<string, unknown> {
  sagaApi: SagaApiClient;
}

function sagaApi(meta: unknown) {
  const api = (meta as Partial<SagaQueryMeta> | undefined)?.sagaApi;
  if (!api) throw new Error('Saga query is missing CanonKeeper API client metadata.');
  return api;
}

/**
 * The graph of the saga (specs/2026-11-04-saga-map.md, UC1). The browser asks again with the revision
 * of the cards and gets the same body back from its cache while they have not changed.
 */
export function useSagaMapQuery(api: SagaApiClient, projectId: string, sourceView: SourceView) {
  return useQuery({
    queryKey: sagaQueryKeys.map(projectId, sourceView),
    queryFn: ({ meta, signal }) => sagaApi(meta).getSagaMap(projectId, { sourceView, signal }),
    meta: { sagaApi: api } satisfies SagaQueryMeta,
    enabled: projectId.length > 0,
    staleTime: 60_000,
    // A map being prepared is asked for again until it is there (R10.3).
    refetchInterval: (query) => (query.state.data?.building ? mapBuildingPoll : false),
  });
}

/** What the chat asks of the saga: only its first questions. */
export type FirstQuestionsApi = Pick<SagaApiClient, 'getSagaFirstQuestions'>;

interface FirstQuestionsMeta extends Record<string, unknown> {
  questionsApi: FirstQuestionsApi;
}

/**
 * While the map of the saga is still to come, an empty chat asks again this often, at most this many times (FQ1.10): 40
 * minutes, the time a book takes to be read; a preparation that stopped does not keep the chat asking forever.
 */
const firstQuestionsPoll = 15_000;
const firstQuestionsPolls = 160;

/**
 * The questions an empty chat of the saga offers, with the names of its book (specs/2026-11-10-first-questions.md FQ1.4,
 * FQ1.5). While the server says the map is still to come (`pending`, FQ1.9) the chat asks again, while it is visible;
 * the repeats are counted by the query itself — its first answer and 160 more.
 */
export function useSagaFirstQuestionsQuery(api: FirstQuestionsApi, projectId: string, sourceView: SourceView, enabled: boolean) {
  return useQuery({
    queryKey: sagaQueryKeys.firstQuestions(projectId, sourceView),
    queryFn: ({ meta, signal }) => (meta as FirstQuestionsMeta).questionsApi.getSagaFirstQuestions(projectId, { sourceView, signal }),
    meta: { questionsApi: api } satisfies FirstQuestionsMeta,
    enabled: projectId.length > 0 && enabled,
    staleTime: 60_000,
    refetchInterval: (query) => (query.state.data?.pending && query.state.dataUpdateCount <= firstQuestionsPolls ? firstQuestionsPoll : false),
    refetchIntervalInBackground: false,
  });
}

/** The card of a node up to a chapter (UC2): a build of the map is what changes it, so a card once read stays for its revision and a second click waits for nothing (R10.5); while another chapter loads, the card of the same node stays. */
export function useSagaMapNodeQuery(api: SagaApiClient, projectId: string, query: Query<GetSagaMapNodeOptions>, revision: string) {
  return useQuery({
    queryKey: sagaQueryKeys.node(projectId, query, revision),
    queryFn: ({ meta, signal }) => sagaApi(meta).getSagaMapNode(projectId, { ...compact(query), signal }),
    meta: { sagaApi: api } satisfies SagaQueryMeta,
    enabled: projectId.length > 0 && query.key.length > 0,
    staleTime: Infinity,
    placeholderData: (previous) => (previous && previous.key === query.key ? previous : undefined),
  });
}

/** What the author took off the map (R11.3). */
export function useSagaMapHiddenQuery(api: SagaApiClient, projectId: string, sourceView: SourceView) {
  return useQuery({
    queryKey: sagaQueryKeys.mapHidden(projectId, { sourceView }),
    queryFn: ({ meta, signal }) => sagaApi(meta).listSagaMapHidden(projectId, { sourceView, signal }),
    meta: { sagaApi: api } satisfies SagaQueryMeta,
    enabled: projectId.length > 0,
  });
}

/** A search of the saga from the prepared rows of its map (R10.6); without a query, the most mentioned entries. */
export function useSagaMapSearchQuery(api: SagaApiClient, projectId: string, query: Query<SearchSagaMapOptions>, enabled = true) {
  return useQuery({
    queryKey: sagaQueryKeys.mapSearch(projectId, query),
    queryFn: ({ meta, signal }) => sagaApi(meta).searchSagaMap(projectId, { ...compact(query), signal }),
    meta: { sagaApi: api } satisfies SagaQueryMeta,
    enabled: projectId.length > 0 && enabled,
    staleTime: 60_000,
    placeholderData: keepPreviousData,
  });
}

export function useSagaContentsQuery(api: SagaApiClient, projectId: string, query: Query<GetSagaContentsOptions>) {
  return useQuery({
    queryKey: sagaQueryKeys.contents(projectId, query),
    queryFn: ({ meta, signal }) => sagaApi(meta).getSagaContents(projectId, { ...compact(query), signal }),
    meta: { sagaApi: api } satisfies SagaQueryMeta,
    enabled: projectId.length > 0,
    placeholderData: keepPreviousData,
  });
}

function contentsQuery(api: SagaApiClient, projectId: string, sourceView: SourceView, chapter: string) {
  const query = { sourceView, chapter };
  return {
    queryKey: sagaQueryKeys.contents(projectId, query),
    queryFn: ({ meta, signal }: { meta?: Record<string, unknown>; signal: AbortSignal }) =>
      sagaApi(meta).getSagaContents(projectId, { ...query, signal }),
    meta: { sagaApi: api } satisfies SagaQueryMeta,
    staleTime: 60_000,
  };
}

/**
 * The chapters behind kept chapter keys — titles, places and reader ids — read by the server's one rule of the keys: a
 * bare number that turned ambiguous still names its first chapter (specs/2026-11-11-ux7-reorder.md RK1.5). The map is by
 * the key as kept.
 */
export function useSagaChapterRefs(api: SagaApiClient, projectId: string, sourceView: SourceView, keys: readonly string[]) {
  const asked = [...new Set(keys.filter((key) => key.length > 0))];
  const results = useQueries({ queries: asked.map((key) => ({ ...contentsQuery(api, projectId, sourceView, key), enabled: projectId.length > 0 })) });
  const refs = new Map<string, SagaChapterRef>();
  asked.forEach((key, index) => {
    const chapter = results[index]?.data?.chapter;
    if (chapter) refs.set(key, chapter);
  });
  return refs;
}

/** The chapter a kept key names, for opening it in the reader; null when the text has no such chapter now. */
export async function fetchSagaChapterRef(queryClient: QueryClient, api: SagaApiClient, projectId: string, sourceView: SourceView, key: string) {
  const contents = await queryClient.fetchQuery(contentsQuery(api, projectId, sourceView, key));
  return contents.chapter ?? null;
}
