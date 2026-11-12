import { keepPreviousData, useInfiniteQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { createIdempotencyKey, type CanonKeeperApiClient, type OperationRequest, type OperationResponse, type Schema } from '../../shared/api';

export type AuthorMemoryApiClient = Pick<CanonKeeperApiClient,
  'listAuthorMemory' | 'createAuthorMemory' | 'updateAuthorMemory' | 'deleteAuthorMemory'>;
export type AuthorMemoryRecord = Schema<'AuthorMemoryRecord'>;
export type AuthorMemoryKind = Schema<'AuthorMemoryKind'>;
export type AuthorMemoryPage = OperationResponse<'listAuthorMemory', '200'>;
export type AuthorMemoryDraft = OperationRequest<'createAuthorMemory'>;
export { createIdempotencyKey };

/** How the page names a kind. */
export const authorMemoryKinds: ReadonlyArray<{ value: AuthorMemoryKind; label: string; hint: string }> = [
  { value: 'plan', label: 'План', hint: 'Что вы задумали. В книге этого ещё нет, и помощник не выдаёт это за случившееся.' },
  { value: 'correction', label: 'Поправка', hint: 'Ваша поправка к фактам. Важнее того, что помощник сам вывел из книги, но не самого текста книги: при расхождении он назовёт оба.' },
  { value: 'style_rule', label: 'Правило стиля', hint: 'Как вы пишете и как просите писать.' },
  { value: 'rejected', label: 'Отвергнуто', hint: 'Что вы отвергли: помощник не предложит это снова.' },
];

export function authorMemoryKindLabel(kind: AuthorMemoryKind) {
  return authorMemoryKinds.find((item) => item.value === kind)?.label ?? kind;
}

export interface AuthorMemoryFilters {
  kind?: AuthorMemoryKind;
  q?: string;
}

/** The memory lives under the product root, so an identity change clears it too. */
export const authorMemoryQueryKeys = {
  all: ['canonkeeper', 'author-memory'] as const,
  project: (projectId: string) => [...authorMemoryQueryKeys.all, projectId] as const,
  list: (projectId: string, filters: AuthorMemoryFilters) => [...authorMemoryQueryKeys.project(projectId), 'list', filters] as const,
};

function memoryApi(meta: unknown) {
  const api = (meta as { memoryApi?: AuthorMemoryApiClient } | undefined)?.memoryApi;
  if (!api) throw new Error('Author memory query is missing API metadata.');
  return api;
}

export function useAuthorMemoryQuery(api: AuthorMemoryApiClient, projectId: string, filters: AuthorMemoryFilters) {
  return useInfiniteQuery({
    queryKey: authorMemoryQueryKeys.list(projectId, filters),
    queryFn: ({ meta, pageParam, signal }) => memoryApi(meta).listAuthorMemory(projectId, {
      ...(filters.kind ? { kind: filters.kind } : {}), ...(filters.q ? { q: filters.q } : {}),
      ...(pageParam ? { cursor: pageParam } : {}), signal,
    }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page) => (page.meta.hasMore ? page.meta.nextCursor ?? undefined : undefined),
    meta: { memoryApi: api },
    enabled: projectId.length > 0,
    placeholderData: keepPreviousData,
  });
}

/** A write of the page: what is listed comes from the server again. */
export function useAuthorMemoryMutation<TInput, TOutput>(projectId: string, write: (input: TInput) => Promise<TOutput>) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: write,
    onSuccess: () => queryClient.invalidateQueries({ queryKey: authorMemoryQueryKeys.project(projectId) }),
  });
}
