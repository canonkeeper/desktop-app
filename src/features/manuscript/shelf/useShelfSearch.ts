import { useCallback, useEffect, useRef, useState } from 'react';
import { isApiStatusFailure, publicApiErrorMessage } from '../../../entities/api-errors';
import type {
  Book,
  ManuscriptApiClient,
  Project,
  SearchResult,
  SourceView,
} from '../../../entities/manuscript/api';
import * as manuscriptFeatureApi from '../api';
import { manuscriptSearchResultDestination } from '../search-result-navigation';
import type { SearchScope } from '../SearchPanel';

type SearchStatus = 'loading' | 'ready' | 'empty' | 'error';

interface ShelfSearchOptions {
  api: ManuscriptApiClient;
  book: Book | null;
  navigate: (path: string, query?: Record<string, string | undefined>) => void;
  project: Project | null;
  sourceView: SourceView;
}

export function useShelfSearch({ api, book, navigate, project, sourceView }: ShelfSearchOptions) {
  const [searchOpen, setSearchOpen] = useState(false);
  const [searchScope, setSearchScope] = useState<SearchScope>('project');
  const [searchTerm, setSearchTerm] = useState('');
  const searchTriggerRef = useRef<HTMLButtonElement | null>(null);
  const searchProjectMutation = manuscriptFeatureApi.useSearchProjectMutation(api);
  const resetSearch = searchProjectMutation.reset;
  const searchResults = searchProjectMutation.isPending ? [] : searchProjectMutation.data?.data ?? [];
  const searchCoverage = searchProjectMutation.isPending ? null : searchProjectMutation.data?.textCoverage ?? null;
  const searchStatus: SearchStatus = searchProjectMutation.isPending
    ? 'loading'
    : searchProjectMutation.isError
      ? 'error'
      : searchProjectMutation.data && searchProjectMutation.data.data.length === 0
        ? 'empty'
        : 'ready';
  const searchError = searchProjectMutation.isError
    ? publicApiErrorMessage(searchProjectMutation.error, 'Поиск временно недоступен.')
    : '';
  const searchRequestId = searchProjectMutation.isError && isApiStatusFailure(searchProjectMutation.error)
    ? searchProjectMutation.error.problem.requestId
    : undefined;

  useEffect(() => {
    if (searchScope === 'book' && !book) setSearchScope('project');
  }, [book, searchScope]);

  const clearSearchResultState = useCallback(() => {
    resetSearch();
  }, [resetSearch]);

  useEffect(() => {
    clearSearchResultState();
  }, [clearSearchResultState, sourceView]);

  const resetForProject = useCallback(() => {
    clearSearchResultState();
  }, [clearSearchResultState]);

  async function runSearch(selectedScope = searchScope) {
    if (!project) return;
    const query = searchTerm.trim();
    if (!query) {
      clearSearchResultState();
      return;
    }
    try {
      await searchProjectMutation.mutateAsync({
        projectId: project.id,
        query,
        scope: 'all',
        filters: selectedScope === 'book' && book ? { bookId: book.id } : undefined,
        sourceView,
      });
    } catch { /* React Query keeps the typed error for presentation. */ }
  }

  function selectSearchScope(nextScope: SearchScope) {
    setSearchScope(nextScope);
    if (searchTerm.trim()) void runSearch(nextScope);
  }

  function openSearchResult(result: SearchResult) {
    setSearchOpen(false);
    const destination = manuscriptSearchResultDestination(result);
    navigate(destination.path, destination.query);
  }

  return {
    openSearchResult,
    resetForProject,
    runSearch,
    searchCoverage,
    searchError,
    searchRequestId,
    searchOpen,
    searchResults,
    searchScope,
    searchStatus,
    searchTerm,
    searchTriggerRef,
    setSearchOpen,
    setSearchTerm,
    selectSearchScope,
  };
}
