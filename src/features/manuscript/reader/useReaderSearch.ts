import type { FormEvent } from 'react';
import { useState } from 'react';
import { isApiStatusFailure, publicApiErrorMessage } from '../../../entities/api-errors';
import type { Book, ManuscriptApiClient, Project, SourceView } from '../../../entities/manuscript/api';
import { useSearchProjectMutation } from '../api';

type ReaderSearchStatus = 'loading' | 'ready' | 'empty' | 'error';
export type ReaderSearchScope = 'book' | 'project';

export function useReaderSearch(api: ManuscriptApiClient, project: Project | null, book: Book | null, sourceView: SourceView = 'published') {
  const [open, setOpen] = useState(false);
  const [term, setTerm] = useState('');
  const [scope, setScope] = useState<ReaderSearchScope>('book');
  const searchProject = useSearchProjectMutation(api);
  const results = searchProject.data?.data ?? [];
  const coverage = searchProject.data?.textCoverage ?? null;
  const status: ReaderSearchStatus = searchProject.isPending
    ? 'loading'
    : searchProject.isError
      ? 'error'
      : searchProject.data
        ? results.length > 0 ? 'ready' : 'empty'
        : 'ready';
  const error = searchProject.error ? readerSearchError(searchProject.error) : '';
  const requestId = searchProject.error && isApiStatusFailure(searchProject.error) ? searchProject.error.problem.requestId ?? '' : '';

  async function run(event?: FormEvent<HTMLFormElement>, selectedScope = scope, selectedTerm = term) {
    event?.preventDefault();
    if (!project) return;
    const query = selectedTerm.trim();
    if (!query) {
      searchProject.reset();
      return;
    }
    try {
      await searchProject.mutateAsync({
        projectId: project.id,
        query,
        sourceView,
        scope: 'all',
        filters: selectedScope === 'book' && book ? { bookId: book.id } : undefined,
      });
    } catch {
      // Mutation state remains the sole owner of the failed server result.
    }
  }

  function selectScope(nextScope: ReaderSearchScope) {
    setScope(nextScope);
    if (term.trim()) void run(undefined, nextScope);
  }

  function updateTerm(value: string) {
    setTerm(value);
    if (!value.trim()) searchProject.reset();
  }

  function searchFor(value: string) {
    setTerm(value);
    setOpen(true);
    void run(undefined, scope, value);
  }

  return { coverage, error, open, requestId, results, run, scope, searchFor, selectScope, setOpen, setTerm: updateTerm, status, term };
}

function readerSearchError(error: unknown) {
  const message = publicApiErrorMessage(error, 'Поиск временно недоступен.', {
    forbidden: 'Нет доступа к этой главе.',
    unauthorized: 'Время входа истекло. Войдите снова — вернём вас к главе.',
  });
  return message;
}
