import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../api/mock';
import { createAppRoute } from '../shared/navigation/app-route';
import { SagaPage } from './SagaPage';

function renderSaga(api: ReturnType<typeof createMockApiClient>, query: Record<string, string> = {}) {
  const navigate = vi.fn();
  render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
    <SagaPage api={api} navigate={navigate} route={createAppRoute('/saga', query)} />
  </QueryClientProvider>);
  return navigate;
}

// Without a saga the shell leads the page to the first screen (specs/2026-11-10-ux3-first-screen.md FS1.1).
describe('SagaPage empty states (R2.1.3)', () => {
  it('says the saga is loading', async () => {
    const api = createMockApiClient();
    vi.spyOn(api, 'listProjects').mockImplementation(() => new Promise<never>(() => {}));
    renderSaga(api);

    // The form of what comes, announced as busy (PS3.1).
    await waitFor(() => expect(document.querySelector('[data-loading][aria-busy="true"]')).toBeTruthy());
    expect(screen.queryByText(/Загружаем/u)).toBeNull();
  });

  it('an old link to an unavailable saga does not claim there is none', async () => {
    renderSaga(createMockApiClient(), { projectId: 'project-gone' });

    // specs/2026-11-11-ux9-shelf-chat.md SC1.9 (W-7G): a calm block with the sagas of the author, the same section of the one chosen.
    const missing = await screen.findByRole('region', { name: 'Сага не найдена' });
    expect(screen.queryByRole('alert')).toBeNull();
    expect(within(missing).getByRole('button', { name: 'Хроники Белого порта' })).toBeTruthy();
  });
});
