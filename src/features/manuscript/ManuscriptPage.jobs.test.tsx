import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../api/mock';
import { parseRoute } from '../../shared/navigation/app-route';
import { FakeWebSocket } from '../../test/FakeWebSocket';
import { ManuscriptPage } from './ManuscriptPage';

type ManuscriptTestApi = ReturnType<typeof createMockApiClient>;
type ManuscriptTestJob = Awaited<ReturnType<ManuscriptTestApi['getJob']>>;

function renderManuscript(search = '', configureApi?: (api: ManuscriptTestApi) => void) {
  const api = createMockApiClient();
  configureApi?.(api);
  const queryClient = new QueryClient({
    defaultOptions: { mutations: { retry: false }, queries: { retry: false } },
  });
  const route = parseRoute(new URL(`http://localhost/manuscript/books${search}`));
  render(
    <QueryClientProvider client={queryClient}>
      <ManuscriptPage api={api} navigate={vi.fn()} onLogout={vi.fn()} route={route} userName="Мира Волкова" />
    </QueryClientProvider>,
  );
  return { api };
}

describe('ManuscriptPage authoritative Jobs', () => {
  beforeEach(() => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1440 });
    vi.stubGlobal('WebSocket', FakeWebSocket);
  });

  afterEach(() => {
    FakeWebSocket.reset();
    vi.unstubAllGlobals();
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it('renders the saga shelf and cancels indexing through the mock job boundary', async () => {
    renderManuscript('?indexing=active');

		expect(await screen.findByText('Книга III. Северный суд')).toBeTruthy();
		const banner = await screen.findByRole('region', { name: 'Подготовка книги' });
		const checklist = await within(banner).findByRole('list', { name: 'Этапы подготовки' });
		expect(within(checklist).getAllByRole('listitem')).toHaveLength(4);
    expect(screen.queryByText(/9 из 14|Векторизация фрагментов/)).toBeNull();

    fireEvent.click(screen.getByRole('button', { name: 'Отменить обработку' }));

    expect(await screen.findByText('Подготовка отменена.')).toBeTruthy();
  });

  it('shows cancel errors without leaving the manuscript shelf', async () => {
    renderManuscript('?indexing=active', (api) => {
      vi.spyOn(api, 'cancelJob').mockRejectedValueOnce(new Error('Индексация уже завершилась.'));
    });

    fireEvent.click(await screen.findByRole('button', { name: 'Отменить обработку' }));

    expect(await screen.findByText('Не удалось отменить подготовку.')).toBeTruthy();
    expect(screen.getByText('Книга III. Северный суд')).toBeTruthy();
  });

  it('scopes project-wide text progress to the current book', async () => {
    const { api } = renderManuscript('?bookId=book-02', (client) => {
      const listProjectJobs = client.listProjectJobs.bind(client);
      const getIndexingStatus = client.getIndexingStatus.bind(client);
      const getJob = client.getJob.bind(client);
      const textIndexJob: ManuscriptTestJob = {
        id: 'canon-text-index-job-scoped-card',
        kind: 'canon_text_index',
        status: 'running',
        progress: 0.5,
        phase: null,
        subject: {
          type: 'canon_text_index',
          projectId: 'project-white-port',
          sourceView: 'draft',
          catalogRevision: 7,
        },
        result: null,
        error: null,
        canCancel: true,
        expiresAt: null,
        createdAt: '2026-06-16T05:00:00.000Z',
        updatedAt: '2026-06-16T05:00:00.000Z',
        links: { self: '/api/v1/jobs/canon-text-index-job-scoped-card', cancel: '/api/v1/jobs/canon-text-index-job-scoped-card/cancel', timeline: null },
      };

      vi.spyOn(client, 'listProjectJobs').mockImplementation(async (...args) => {
        const response = await listProjectJobs(...args);
        return { ...response, data: [textIndexJob] };
      });
      vi.spyOn(client, 'getJob').mockImplementation(async (id) =>
        id === textIndexJob.id ? textIndexJob : getJob(id));
      vi.spyOn(client, 'getIndexingStatus').mockImplementation(async (projectId, options = {}) => {
        const response = await getIndexingStatus(projectId, options);
        const scoped = options.bookId === 'book-02' && options.sourceView === 'draft';
        return {
          ...response,
          draft: {
            exists: true as const,
            context: {
              state: 'processing' as const,
              currentBook: { bookId: 'book-02', position: 2, total: 2 },
              retryableFailedBook: null,
            },
          },
          run: null,
          steps: [
            { kind: 'import' as const, status: 'succeeded' as const, progress: null, links: { timeline: null } },
            {
              bookId: scoped ? 'book-02' : undefined,
              counters: { indexedChapters: 77, totalChapters: scoped ? 77 : 154 },
              kind: 'text_index' as const,
              links: { timeline: null },
              progress: scoped ? 1 : 0.5,
              sourceView: scoped ? 'draft' as const : undefined,
              status: 'running' as const,
            },
          ],
        };
      });
    });

    await waitFor(() => expect(api.getIndexingStatus).toHaveBeenCalledWith(
      'project-white-port',
      expect.objectContaining({ bookId: 'book-02', sourceView: 'draft' }),
    ));
    expect(await screen.findByText('Готово для поиска 77 из 77 глав.')).toBeTruthy();
    expect(screen.queryByText('Готово для поиска 77 из 154 глав.')).toBeNull();
  });

});
