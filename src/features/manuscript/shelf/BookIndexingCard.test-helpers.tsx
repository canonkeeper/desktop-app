import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render } from '@testing-library/react';
import { vi } from 'vitest';
import { createMockApiClient } from '../../../api/mock';
import type { Book, IndexingStatus, IndexingStep } from '../../../entities/manuscript/api';
import { BookIndexingCard } from './BookIndexingCard';

export interface RenderCardOptions {
  apiSetup?: (api: ReturnType<typeof createMockApiClient>) => void;
  bookId?: string | null;
  bookPendingCreation?: boolean;
  bookTitle?: string;
  books?: Array<Pick<Book, 'id' | 'title'>>;
  canCancel?: boolean;
  canceling?: boolean;
  canEditManuscript?: boolean;
  currentBook?: { bookId: string; position: number; total: number } | null;
  draftEnvelope?: IndexingStatus['draft'];
  indexingState?: 'partial' | 'processing' | 'ready';
  jobSourceView?: 'draft' | 'published';
  onAskQuestion?: () => void;
  onCancel?: () => void;
  onDismiss?: () => void;
  publishedEnvelope?: IndexingStatus['published'];
  retryableFailedBookId?: string | null;
  sourceView?: 'draft' | 'published';
  steps?: IndexingStep[];
}

export function renderCard({
  apiSetup,
  bookId = 'book-02',
  bookPendingCreation,
  bookTitle = 'Книга II. Карта приливов',
  books = [{ id: 'book-02', title: 'Книга II. Карта приливов' }],
  canCancel = false,
  canceling = false,
  canEditManuscript = true,
  currentBook = null,
  draftEnvelope,
  indexingState,
  jobSourceView,
  onAskQuestion = vi.fn(),
  onCancel,
  onDismiss = vi.fn(),
  publishedEnvelope,
  retryableFailedBookId = null,
  sourceView = 'published',
  steps,
}: RenderCardOptions = {}) {
  const api = createMockApiClient();
  api.setScenario({ actorRole: 'editor', preset: 'normal' });
  apiSetup?.(api);
  const getIndexingStatus = vi.spyOn(api, 'getIndexingStatus');
  if (steps) {
    const envelope = indexingState
      ? {
          exists: true as const,
          context: {
            state: indexingState,
            currentBook,
            retryableFailedBook: retryableFailedBookId ? { bookId: retryableFailedBookId } : null,
          },
        }
      : { exists: false as const, context: null };
    getIndexingStatus.mockResolvedValue({
      draft: draftEnvelope ?? envelope,
      published: publishedEnvelope ?? envelope,
      steps,
    });
  }
  const startProjectIndexing = vi.spyOn(api, 'startProjectIndexing').mockResolvedValue({} as never);
  const cancelJob = vi.spyOn(api, 'cancelJob');
  const queryClient = new QueryClient({
    defaultOptions: { mutations: { retry: false }, queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={queryClient}>
      <BookIndexingCard
        api={api}
        bookId={bookId}
        bookPendingCreation={bookPendingCreation ?? false}
        bookTitle={bookTitle}
        books={books}
        canCancel={canCancel}
        canceling={canceling}
        canEditManuscript={canEditManuscript}
        jobSourceView={jobSourceView}
        onAskQuestion={onAskQuestion}
        onCancel={onCancel}
        onDismiss={onDismiss}
        projectId="project-white-port"
        sourceView={sourceView}
      />
    </QueryClientProvider>,
  );

  return { api, cancelJob, getIndexingStatus, onAskQuestion, onDismiss, startProjectIndexing };
}

export const orderedSteps: IndexingStep[] = [
  { kind: 'import', status: 'succeeded', progress: 1 },
  {
    kind: 'text_index', status: 'running', progress: null, sourceView: 'published',
    counters: { indexedChapters: 6, totalChapters: 14 },
  },
  { kind: 'cards', status: 'pending', progress: null, sourceView: 'published' },
  { kind: 'ready', status: 'pending', progress: null },
];
