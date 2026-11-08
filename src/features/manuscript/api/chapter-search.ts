import { useInfiniteQuery } from '@tanstack/react-query';
import type { ManuscriptApiClient, SourceView } from '../../../entities/manuscript/api';

type ChapterSearchClient = Pick<ManuscriptApiClient, 'searchChapters'>;

/** Account-wide chapter navigation; the auth boundary clears queries on account changes. */
export function useChapterSearch(api: ChapterSearchClient, query: string, sourceView: SourceView) {
  return useInfiniteQuery({
    queryKey: ['canonkeeper', 'chapter-search', query, sourceView],
    queryFn: ({ pageParam, signal, meta }) => (meta!.api as ChapterSearchClient).searchChapters({ query, sourceView, limit: 10, cursor: pageParam }, { signal }),
    meta: { api },
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page) => page.meta.hasMore ? page.meta.nextCursor : undefined,
    enabled: query.length > 0,
    staleTime: 0,
    retry: false,
  });
}
