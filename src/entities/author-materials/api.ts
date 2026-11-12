import { keepPreviousData, useInfiniteQuery, useQuery } from '@tanstack/react-query';
import { createIdempotencyKey, type CanonKeeperApiClient, type OperationRequest, type Schema } from '../../shared/api';

export type AuthorMaterialsApiClient = Pick<CanonKeeperApiClient,
  'listAuthorMaterials' | 'createAuthorMaterial' | 'updateAuthorMaterial' | 'deleteAuthorMaterial' | 'startAuthorMaterialsImport' | 'getAuthorMaterialsImport'
  | 'confirmAuthorMaterialsImport' | 'discardAuthorMaterialsImport' | 'cancelJob'>;
export type AuthorMaterial = Schema<'AuthorMaterial'>;
export type AuthorMaterialKind = Schema<'AuthorMaterialKind'>;
export type AuthorMaterialProperty = Schema<'AuthorMaterialProperty'>;
export type AuthorMaterialCandidate = Schema<'AuthorMaterialCandidate'>;
export type AuthorMaterialsImport = Schema<'AuthorMaterialsImport'>;
export type AuthorMaterialChoice = Schema<'AuthorMaterialChoice'>;
export type UpdateAuthorMaterial = OperationRequest<'updateAuthorMaterial'>;
export type CreateAuthorMaterial = OperationRequest<'createAuthorMaterial'>;
export { createIdempotencyKey };

/** How the pages name a kind (specs/2026-11-04-saga-map.md, R9.1). */
export const authorMaterialKinds: ReadonlyArray<{ value: AuthorMaterialKind; label: string }> = [
  { value: 'skill', label: 'Навык' },
  { value: 'place', label: 'Место' },
  { value: 'character', label: 'Герой' },
  { value: 'item', label: 'Предмет' },
  { value: 'faction', label: 'Фракция' },
  { value: 'other', label: 'Другое' },
];

export function authorMaterialKindLabel(kind: AuthorMaterialKind) {
  return authorMaterialKinds.find((item) => item.value === kind)?.label ?? kind;
}

/**
 * The line under a record (specs/2026-11-10-ux8-memory-records.md MR3.5): what it is and where it came from — «Герой · из
 * „Навыки и места.xlsx“, лист „Персонажи“», «Герой · добавлено вручную».
 */
export function materialOriginLine(material: Pick<AuthorMaterial, 'kind' | 'section' | 'sourceKind' | 'sourceName'>) {
  const kind = authorMaterialKindLabel(material.kind);
  if (material.sourceKind === 'manual') return `${kind} · добавлено вручную`;
  const source = material.sourceName ? `из „${material.sourceName}“` : 'из вставленного текста';
  const part = material.sourceKind === 'xlsx' || material.sourceKind === 'csv' ? 'лист' : 'раздел';
  return `${kind} · ${source}${material.section ? `, ${part} „${material.section}“` : ''}`;
}

export interface AuthorMaterialFilters {
  q?: string;
}

/** The materials live under the product root, so an identity change clears them too. */
export const authorMaterialQueryKeys = {
  all: ['canonkeeper', 'author-materials'] as const,
  project: (projectId: string) => [...authorMaterialQueryKeys.all, projectId] as const,
  list: (projectId: string, filters: AuthorMaterialFilters) => [...authorMaterialQueryKeys.project(projectId), 'list', filters] as const,
  import: (projectId: string) => [...authorMaterialQueryKeys.project(projectId), 'import'] as const,
};

function materialsApi(meta: unknown) {
  const api = (meta as { materialsApi?: AuthorMaterialsApiClient } | undefined)?.materialsApi;
  if (!api) throw new Error('Author materials query is missing API metadata.');
  return api;
}

export function useAuthorMaterialsQuery(api: AuthorMaterialsApiClient, projectId: string, filters: AuthorMaterialFilters, enabled = true) {
  return useInfiniteQuery({
    queryKey: authorMaterialQueryKeys.list(projectId, filters),
    queryFn: ({ meta, pageParam, signal }) => materialsApi(meta).listAuthorMaterials(projectId, {
      ...(filters.q ? { q: filters.q } : {}), ...(pageParam ? { cursor: pageParam } : {}), signal,
    }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page) => (page.meta.hasMore ? page.meta.nextCursor ?? undefined : undefined),
    meta: { materialsApi: api },
    enabled: enabled && projectId.length > 0,
    placeholderData: keepPreviousData,
  });
}

const importPollMs = 1500;

/** Whether the Job of an import still goes: the page reads the import until it ends. */
export function importIsRunning(status: AuthorMaterialsImport['jobStatus'] | undefined) {
  return status === 'queued' || status === 'running' || status === 'canceling';
}

/** The import the author has not answered, read again while its Job goes: its state is the server's. */
export function useAuthorMaterialsImportQuery(api: AuthorMaterialsApiClient, projectId: string, enabled = true) {
  return useQuery({
    queryKey: authorMaterialQueryKeys.import(projectId),
    queryFn: ({ meta, signal }) => materialsApi(meta).getAuthorMaterialsImport(projectId, { signal }),
    meta: { materialsApi: api },
    enabled: enabled && projectId.length > 0,
    refetchInterval: (query) => (importIsRunning(query.state.data?.import?.jobStatus) ? importPollMs : false),
  });
}
