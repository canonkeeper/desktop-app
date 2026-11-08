import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../api/mock';
import { createAppRoute } from '../shared/navigation/app-route';
import { AuthorMemoryPage } from './AuthorMemoryPage';

afterEach(cleanup);

function renderPage(api: ReturnType<typeof createMockApiClient>) {
  const navigate = vi.fn();
  render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
    <AuthorMemoryPage api={api} navigate={navigate} onLogout={vi.fn()} route={createAppRoute('/memory', {})} userName="Мира" />
  </QueryClientProvider>);
  return navigate;
}

describe('AuthorMemoryPage (R4.5)', () => {
  // specs/2026-11-02-writer-workshop.md, Decision log 03.11: the memory is the background of the helper, no section of
  // the menu: it stands under the saga map it opens from (saga map R6.3).
  it('stands under the saga map, not in the menu, and an editor may write', async () => {
    const api = createMockApiClient();
    api.setScenario({ actorRole: 'editor', preset: 'normal' });
    renderPage(api);
    expect(await screen.findByRole('button', { name: 'Добавить запись' })).toBeTruthy();
    expect(screen.queryAllByRole('button', { name: 'Память' })).toHaveLength(0);
    const items = screen.getAllByRole('button', { name: 'Карта саги' });
    expect(items.some((item) => item.getAttribute('aria-current') === 'page')).toBe(true);
  });

  it('a viewer reads only', async () => {
    const api = createMockApiClient();
    api.setScenario({ actorRole: 'viewer', preset: 'normal' });
    renderPage(api);
    expect(await screen.findByText('Записи появятся, когда редактор саги их добавит.')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Добавить запись' })).toBeNull();
  });

  it('without a saga leads to creating one', async () => {
    const api = createMockApiClient();
    api.setScenario({ actorRole: 'owner', preset: 'empty-lists' });
    const navigate = renderPage(api);
    expect(await screen.findByText('Память хранит то, что вы сказали или одобрили о своей саге.')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Создать сагу' }));
    expect(navigate).toHaveBeenCalledWith('/manuscript/books', { projectId: undefined });
  });
});
