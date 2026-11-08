import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../../api/mock';
import { ApiStatusError } from '../../../shared/api';
import { DeletedSourcesDialog } from './DeletedSourcesDialog';

function renderPanel(options: {
  api?: ReturnType<typeof createMockApiClient>;
  bookId?: string | null;
  onClose?: () => void;
  onRestored?: Parameters<typeof DeletedSourcesDialog>[0]['onRestored'];
  projectId?: string | null;
} = {}) {
  const api = options.api ?? createMockApiClient();
  const queryClient = new QueryClient({
    defaultOptions: {
      mutations: { retry: false },
      queries: { retry: false },
    },
  });
  render(
    <QueryClientProvider client={queryClient}>
      <DeletedSourcesDialog
        api={api}
        bookId={options.bookId ?? null}
        onClose={options.onClose ?? vi.fn()}
        onRestored={options.onRestored ?? vi.fn()}
        projectId={options.projectId ?? null}
      />
    </QueryClientProvider>,
  );
  return { api, queryClient };
}

function restoreConflictError() {
  return new ApiStatusError(
    409,
    {
      code: 'conflict',
      detail: 'closed test detail',
      requestId: 'request_test',
      status: 409,
      title: 'Conflict',
      type: 'about:blank',
    },
    new Response(null, { status: 409 }),
  );
}

function restoreServerError() {
  return new ApiStatusError(500, {
    code: 'internal_error',
    detail: 'closed test detail',
    requestId: 'request_test',
    status: 500,
    title: 'Server error',
    type: 'about:blank',
  }, new Response(null, { status: 500 }));
}

function setChapterRestoreUntil(api: ReturnType<typeof createMockApiClient>, restoreUntil: string) {
  const listChapters = api.listChapters.bind(api);
  return vi.spyOn(api, 'listChapters').mockImplementation(async (...args) => {
    const result = await listChapters(...args);
    return { ...result, data: result.data.map((source) => ({ ...source, restoreUntil })) };
  });
}

describe('DeletedSourcesDialog', () => {
  it('loads deleted chapters only with an exact active book', async () => {
    const api = createMockApiClient();
    const listChapters = vi.spyOn(api, 'listChapters');

    renderPanel({ api, projectId: 'project-white-port' });

    expect(await screen.findByRole('heading', { name: 'Недавно удалённые' })).toBeTruthy();
    expect(screen.getByRole('heading', { name: 'Главы' })).toBeTruthy();
    expect(listChapters).not.toHaveBeenCalled();
  });

  it('shows and restores one deleted chapter without resurrecting it optimistically', async () => {
    const api = createMockApiClient();
    await api.deleteChapter('chapter-16');
    const onRestored = vi.fn();

    renderPanel({ api, bookId: 'book-02', onRestored, projectId: 'project-white-port' });

    const restore = await screen.findByRole('button', { name: /Восстановить .* до/ });
    fireEvent.click(restore);

    await waitFor(() => expect(onRestored).toHaveBeenCalledTimes(1));
    expect(await screen.findByText('Источник восстановлен.')).toBeTruthy();
    expect(screen.getByRole('status')).toBeTruthy();
    await waitFor(() => expect(screen.queryByRole('button', { name: /Восстановить .* до/ })).toBeNull());
  });

  it('keeps the deleted title and remaining retention days visible', async () => {
    const api = createMockApiClient();
    await api.deleteChapter('chapter-16');
    const restoreUntil = new Date(Date.now() + 3 * 24 * 60 * 60 * 1000).toISOString();
    const listChapters = api.listChapters.bind(api);
    vi.spyOn(api, 'listChapters').mockImplementation(async (...args) => {
      const result = await listChapters(...args);
      return { ...result, data: result.data.map((source) => ({ ...source, restoreUntil })) };
    });

    renderPanel({ api, bookId: 'book-02', projectId: 'project-white-port' });

    expect(await screen.findByText('Возвращение к записи')).toBeTruthy();
    expect(screen.getByText(/Удалена .+ · осталось 3 дня/)).toBeTruthy();
  });

  it('disables every restore action while one exact source is pending', async () => {
    const api = createMockApiClient();
    const [first, second] = (await api.listBooks('project-white-port')).data;
    if (!first || !second) throw new Error('Mock library needs two books.');
    await api.deleteBook(first.id);
    await api.deleteBook(second.id);
    const restoreBook = api.restoreBook.bind(api);
    let releaseRestore: () => void = () => undefined;
    const restoreGate = new Promise<void>((resolve) => { releaseRestore = resolve; });
    vi.spyOn(api, 'restoreBook').mockImplementation(async (bookId) => {
      await restoreGate;
      return restoreBook(bookId);
    });
    renderPanel({ api, projectId: 'project-white-port' });
    const actions = await screen.findAllByRole('button', { name: /^Восстановить .* до/ });

    fireEvent.click(actions[0]!);
    await waitFor(() => expect(actions[0]).toHaveProperty('disabled', true));

    expect(actions[1]).toHaveProperty('disabled', true);
    releaseRestore();
    await waitFor(() => expect(screen.getByText('Источник восстановлен.')).toBeTruthy());
  });

  it('refreshes after an equality-boundary conflict and shows the fixed expiry message', async () => {
    const api = createMockApiClient();
    await api.deleteChapter('chapter-16');
    const listChapters = setChapterRestoreUntil(api, '2000-01-01T00:00:00.000Z');
    vi.spyOn(api, 'restoreChapter').mockRejectedValue(restoreConflictError());

    renderPanel({ api, bookId: 'book-02', projectId: 'project-white-port' });
    fireEvent.click(await screen.findByRole('button', { name: /Восстановить .* до/ }));

    expect(await screen.findByText('Срок восстановления истёк')).toBeTruthy();
    expect(screen.getByRole('alert').textContent).toContain('Обновите список удалённых источников.');
    await waitFor(() => expect(listChapters.mock.calls.length).toBeGreaterThanOrEqual(2));
  });

  it('distinguishes a concurrent restore/purge race from expiry and refreshes the exact section', async () => {
    const api = createMockApiClient();
    await api.deleteChapter('chapter-16');
    const listChapters = setChapterRestoreUntil(api, '2999-01-01T00:00:00.000Z');
    vi.spyOn(api, 'restoreChapter').mockRejectedValue(restoreConflictError());
    renderPanel({ api, bookId: 'book-02', projectId: 'project-white-port' });

    fireEvent.click(await screen.findByRole('button', { name: /Восстановить .* до/ }));

    expect(await screen.findByText('Источник уже восстановлен или удалён другим участником.')).toBeTruthy();
    expect(screen.queryByText('Срок восстановления истёк')).toBeNull();
    await waitFor(() => expect(listChapters.mock.calls.length).toBeGreaterThanOrEqual(2));
  });

  it('retries only the failed deleted-source section', async () => {
    const api = createMockApiClient();
    const listBooks = api.listBooks.bind(api);
    const books = vi.spyOn(api, 'listBooks')
      .mockRejectedValueOnce(new Error('temporary'))
      .mockImplementation(listBooks);
    renderPanel({ api, projectId: 'project-white-port' });

    const section = await screen.findByRole('region', { name: 'Книги' });
    fireEvent.click(await within(section).findByRole('button', { name: 'Повторить загрузку книг' }));

    await waitFor(() => expect(books).toHaveBeenCalledTimes(2));
    expect(within(section).queryByRole('alert')).toBeNull();
  });

  it('offers retry on the same source after a recoverable restore failure', async () => {
    const api = createMockApiClient();
    await api.deleteChapter('chapter-16');
    const restoreChapter = vi.spyOn(api, 'restoreChapter')
      .mockRejectedValueOnce(restoreServerError())
      .mockImplementation((chapterId) => createMockApiClient().restoreChapter(chapterId));
    renderPanel({ api, bookId: 'book-02', projectId: 'project-white-port' });

    const restore = await screen.findByRole('button', { name: /Восстановить .* до/ });
    fireEvent.click(restore);
    const retry = await screen.findByRole('button', { name: /Повторить .* до/ });
    fireEvent.click(retry);

    await waitFor(() => expect(restoreChapter).toHaveBeenCalledTimes(2));
  });
});
