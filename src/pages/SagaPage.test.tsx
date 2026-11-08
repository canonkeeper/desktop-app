import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../api/mock';
import { createAppRoute } from '../shared/navigation/app-route';
import { SagaPage } from './SagaPage';

function renderSaga(api: ReturnType<typeof createMockApiClient>, query: Record<string, string> = {}) {
  const navigate = vi.fn();
  render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
    <SagaPage api={api} navigate={navigate} onLogout={vi.fn()} route={createAppRoute('/saga', query)} userName="Мира" />
  </QueryClientProvider>);
  return navigate;
}

describe('SagaPage empty states (R2.1.3)', () => {
  it('without a saga says where the map comes from and leads to creating one', async () => {
    const api = createMockApiClient();
    api.setScenario({ actorRole: 'owner', preset: 'empty-lists' });
    const navigate = renderSaga(api);

    expect(await screen.findByText('Пока нет саги')).toBeTruthy();
    expect(screen.getByText('Карта собирается из глав загруженных книг.')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Создать сагу' }));
    expect(navigate).toHaveBeenCalledWith('/manuscript/books', { projectId: undefined });
  });

  it('says the saga is loading', async () => {
    const api = createMockApiClient();
    vi.spyOn(api, 'listProjects').mockImplementation(() => new Promise<never>(() => {}));
    renderSaga(api);

    expect(await screen.findByText('Загружаем сагу…')).toBeTruthy();
  });

  it('an old link to an unavailable saga does not claim there is none', async () => {
    renderSaga(createMockApiClient(), { projectId: 'project-gone' });

    expect(await screen.findByText('Сага недоступна: доступ к ней мог быть отозван.')).toBeTruthy();
    expect(screen.queryByText('Пока нет саги')).toBeNull();
  });
});
