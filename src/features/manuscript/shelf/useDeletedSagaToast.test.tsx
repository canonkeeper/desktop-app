import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { useEffect, useRef } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../../api/mock';
import { ApiStatusError } from '../../../shared/api';
import { ToastRegion } from '../../../ui/feedback/Toast';
import { useToastQueue } from '../../../ui/feedback/useToastQueue';
import { useDeletedSagaToast, type DeletedSource } from './useDeletedSagaToast';

function Harness({ api, navigate, source }: { api: ReturnType<typeof createMockApiClient>; navigate: () => void; source: DeletedSource }) {
  const toasts = useToastQueue();
  const announce = useDeletedSagaToast({ api, navigate, toasts });
  const told = useRef(false);
  useEffect(() => {
    if (told.current) return;
    told.current = true;
    announce(source);
  }, [announce, source]);
  return <ToastRegion onDismiss={toasts.dismiss} queue={toasts.queue} />;
}

async function renderDeleted(configure?: (api: ReturnType<typeof createMockApiClient>) => void) {
  const api = createMockApiClient();
  const project = (await api.listProjects()).data[0]!;
  await api.deleteProject(project.id);
  configure?.(api);
  const navigate = vi.fn();
  const queryClient = new QueryClient({ defaultOptions: { mutations: { retry: false }, queries: { retry: false } } });
  const source: DeletedSource = { kind: 'project', id: project.id, title: project.title };
  render(<QueryClientProvider client={queryClient}><Harness api={api} navigate={navigate} source={source} /></QueryClientProvider>);
  return { api, navigate, project };
}

// specs/2026-11-10-ux1-shelf.md UXS4.2: «Сага «…» удалена» with «Вернуть», until it is closed.
describe('useDeletedSagaToast', () => {
  it('returns the deleted saga and opens its shelf', async () => {
    const { api, navigate, project } = await renderDeleted();
    const restore = vi.spyOn(api, 'restoreProject');

    const toast = await screen.findByRole('status');
    expect(toast.textContent).toContain(`Сага «${project.title}» удалена`);
    fireEvent.click(within(toast).getByRole('button', { name: 'Вернуть' }));

    await waitFor(() => expect(restore).toHaveBeenCalledWith(project.id));
    await waitFor(() => expect(navigate).toHaveBeenCalledWith('/manuscript/books', { projectId: project.id }));
  });

  // specs/2026-11-10-ux2-patterns-actions.md PA3.3: a deleted book says the same line and comes back by the same «Вернуть».
  it('returns a deleted book and opens the shelf on it', async () => {
    const api = createMockApiClient();
    const project = (await api.listProjects()).data[0]!;
    const book = (await api.listBooks(project.id)).data[0]!;
    await api.deleteBook(book.id);
    const restore = vi.spyOn(api, 'restoreBook');
    const navigate = vi.fn();
    const queryClient = new QueryClient({ defaultOptions: { mutations: { retry: false }, queries: { retry: false } } });
    render(<QueryClientProvider client={queryClient}><Harness api={api} navigate={navigate} source={{ kind: 'book', id: book.id, projectId: project.id, title: book.title }} /></QueryClientProvider>);
    const toast = await screen.findByRole('status');
    expect(toast.textContent).toContain(`Книга «${book.title}» удалена`);
    // One phrase of the product for what comes back (specs/2026-11-11-ux7-trash.md TB2.1): the window of the deletion says the same.
    expect(toast.textContent).toContain('Можно вернуть 7 дней — в «Недавно удалённых».');
    expect(toast.textContent).not.toContain('и позже');
    fireEvent.click(within(toast).getByRole('button', { name: 'Вернуть' }));
    await waitFor(() => expect(restore).toHaveBeenCalledWith(book.id));
    await waitFor(() => expect(navigate).toHaveBeenCalledWith('/manuscript/books', { projectId: project.id, bookId: book.id }));
    expect((await screen.findByRole('status')).textContent).toContain(`Книга «${book.title}» возвращена.`);
  });

  it('stays until it is closed', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      await renderDeleted();
      await screen.findByRole('status');
      vi.advanceTimersByTime(15_000);
      expect(screen.getByRole('button', { name: 'Вернуть' })).toBeTruthy();
    } finally {
      vi.useRealTimers();
    }
  });

  it('says a refusal by the plan in the words of «Недавно удалённые»', async () => {
    await renderDeleted((api) => {
      vi.spyOn(api, 'restoreProject').mockRejectedValue(new ApiStatusError(409, {
        type: 'about:blank', title: 'Conflict', status: 409, code: 'quota.account_projects', requestId: 'req-projects',
      }, new Response(null, { status: 409 })));
    });

    fireEvent.click(await screen.findByRole('button', { name: 'Вернуть' }));

    const refusal = await screen.findByRole('alert');
    expect(refusal.textContent).toContain('Не удалось вернуть.');
  });
});
