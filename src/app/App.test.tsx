import { onlineManager, QueryClient, QueryClientProvider, useQuery } from '@tanstack/react-query';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../api/mock';
import { ApiStatusError, NetworkApiError, type ApiProblem } from '../shared/api';
import { useAuthShellStore } from './auth-store';
import { createAppRoute, parseRoute } from '../shared/navigation/app-route';
import { App } from '../App';

vi.mock('../features/chat/ChatPage', () => ({
  ChatPage: ({ onLogout }: { onLogout: () => void }) => (
    <main aria-label="Страница чата">
      Чат загружен
      <button onClick={onLogout} type="button">Выйти</button>
    </main>
  ),
}));

vi.mock('../pages/NotesPage', () => ({
  NotesPage: ({ onLogout }: { onLogout: () => void }) => <main aria-label="Страница заметок">
    <button onClick={onLogout} type="button">Выйти</button>
  </main>,
}));

function statusError(status: number) {
  const problem: ApiProblem = {
    type: 'about:blank',
    title: status === 401 ? 'Unauthorized' : 'Service Unavailable',
    status,
    code: status === 401 ? 'unauthorized' : 'service_unavailable',
    requestId: 'req-app-test',
  };
  return new ApiStatusError(status, problem, new Response(null, { status }));
}

function RouteQueryProbe({ load, outageScope }: { load: () => Promise<string>; outageScope?: 'local' }) {
  useQuery({
    queryKey: ['canonkeeper', 'route-probe'],
    queryFn: load,
    meta: outageScope ? { outageScope } : undefined,
    retry: false,
  });
  return null;
}

describe('App', () => {
  it('opens the authenticated Notes route without requiring a project', async () => {
    render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <App api={createMockApiClient()} navigate={vi.fn()} route={createAppRoute('/notes')} />
    </QueryClientProvider>);
    expect(await screen.findByRole('main', { name: 'Страница заметок' })).toBeTruthy();
  });

  it('даёт понятные выходы с неизвестной страницы без технических маршрутов', async () => {
    const navigate = vi.fn();
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(<QueryClientProvider client={queryClient}>
      <App api={createMockApiClient()} navigate={navigate} route={parseRoute('/unknown-private-path')} />
    </QueryClientProvider>);
    expect(await screen.findByRole('heading', { name: 'Страница не найдена' })).toBeTruthy();
    expect(screen.queryByText('/unknown-private-path')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'К моим книгам' }));
    expect(navigate).toHaveBeenCalledWith('/manuscript/books');
    fireEvent.click(screen.getByRole('button', { name: 'Открыть чат' }));
    expect(navigate).toHaveBeenCalledWith('/chat');
  });

  beforeEach(() => {
    onlineManager.setOnline(true);
    useAuthShellStore.getState().clearAuthMessage();
  });

  afterEach(() => {
    onlineManager.setOnline(true);
  });

  it('рендерит authenticated-ветку канонического маршрута /chat', async () => {
    const queryClient = new QueryClient({
      defaultOptions: { mutations: { retry: false }, queries: { retry: false } },
    });

    render(
      <QueryClientProvider client={queryClient}>
        <App
          api={createMockApiClient()}
          navigate={vi.fn()}
          route={createAppRoute('/chat', { projectId: 'project-white-port', sourceView: 'published' })}
        />
      </QueryClientProvider>,
    );

    expect(await screen.findByRole('main', { name: 'Страница чата' })).toBeTruthy();
  });

  it.each([
    ['network error', new NetworkApiError(new TypeError('fetch failed'))],
    ['server 5xx', statusError(503)],
  ])('показывает global unavailable вместо формы входа при cold %s', async (_case, failure) => {
    const api = createMockApiClient();
    vi.spyOn(api, 'getCurrentUser').mockRejectedValue(failure);
    const navigate = vi.fn();
    const queryClient = new QueryClient({
      defaultOptions: { mutations: { retry: false }, queries: { retry: false } },
    });

    render(
      <QueryClientProvider client={queryClient}>
        <App
          api={api}
          navigate={navigate}
          route={createAppRoute('/manuscript/books')}
        />
      </QueryClientProvider>,
    );

    expect(await screen.findByText('Сервис временно недоступен')).toBeTruthy();
    expect(screen.queryByRole('heading', { name: 'С возвращением' })).toBeNull();
    expect(navigate).not.toHaveBeenCalled();
    expect(onlineManager.isOnline()).toBe(false);
    // A failure with a request id gives its code and a letter to support (R4.2.5).
    if (failure instanceof ApiStatusError) {
      expect(screen.getByText('Попытка 1')).toBeTruthy();
      expect(screen.getByText('req-app-test').closest('span')?.textContent).toBe('Код для поддержки: req-app-test');
      expect(decodeURIComponent(screen.getByRole('link', { name: 'Написать в поддержку' }).getAttribute('href') ?? ''))
        .toContain('Canon Keeper: ошибка req-app-test');
    } else {
      expect(screen.queryByText(/Код для поддержки/u)).toBeNull();
    }
  });

  it('сохраняет текущую страницу inert и восстанавливает её Retry без навигации', async () => {
    const api = createMockApiClient();
    const currentUser = await api.getCurrentUser();
    const getCurrentUser = vi.spyOn(api, 'getCurrentUser')
      .mockResolvedValueOnce(currentUser)
      .mockRejectedValueOnce(statusError(503))
      .mockResolvedValue(currentUser);
    const loadRoute = vi.fn<() => Promise<string>>()
      .mockResolvedValueOnce('cached route')
      .mockRejectedValueOnce(statusError(503))
      .mockResolvedValue('fresh route');
    const navigate = vi.fn();
    const queryClient = new QueryClient({
      defaultOptions: { mutations: { retry: false }, queries: { retry: false } },
    });

    render(
      <QueryClientProvider client={queryClient}>
        <App
          api={api}
          navigate={navigate}
          route={createAppRoute('/chat', { projectId: 'project-white-port', sourceView: 'published' })}
        />
        <RouteQueryProbe load={loadRoute} />
      </QueryClientProvider>,
    );

    await screen.findByRole('main', { name: 'Страница чата' });
    await waitFor(() => expect(loadRoute).toHaveBeenCalledTimes(1));
    await act(async () => {
      await queryClient.invalidateQueries({ queryKey: ['canonkeeper', 'route-probe'] });
    });

    expect(await screen.findByText('Сервис временно недоступен')).toBeTruthy();
    expect(getCurrentUser).toHaveBeenCalledTimes(2);
    expect(Boolean((document.body.firstElementChild as HTMLElement).inert)).toBe(true);
    expect(onlineManager.isOnline()).toBe(false);
    expect(navigate).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole('button', { name: 'Повторить' }));

    await waitFor(() => expect(screen.queryByText('Сервис временно недоступен')).toBeNull());
    expect(getCurrentUser).toHaveBeenCalledTimes(3);
    expect(loadRoute).toHaveBeenCalledTimes(3);
    expect(Boolean((document.body.firstElementChild as HTMLElement).inert)).toBe(false);
    expect(screen.getByRole('main', { name: 'Страница чата' })).toBeTruthy();
    expect(onlineManager.isOnline()).toBe(true);
    expect(navigate).not.toHaveBeenCalled();
  });

  it('keeps a feature 5xx local when the authoritative session probe succeeds', async () => {
    const api = createMockApiClient();
    const getCurrentUser = vi.spyOn(api, 'getCurrentUser');
    const loadRoute = vi.fn<() => Promise<string>>()
      .mockResolvedValueOnce('cached route')
      .mockRejectedValueOnce(statusError(503));
    const queryClient = new QueryClient({
      defaultOptions: { mutations: { retry: false }, queries: { retry: false } },
    });

    render(
      <QueryClientProvider client={queryClient}>
        <App
          api={api}
          navigate={vi.fn()}
          route={createAppRoute('/chat', { projectId: 'project-white-port', sourceView: 'published' })}
        />
        <RouteQueryProbe load={loadRoute} />
      </QueryClientProvider>,
    );

    await screen.findByRole('main', { name: 'Страница чата' });
    await waitFor(() => expect(loadRoute).toHaveBeenCalledTimes(1));
    await act(async () => {
      await queryClient.invalidateQueries({ queryKey: ['canonkeeper', 'route-probe'] });
    });

    await waitFor(() => expect(getCurrentUser).toHaveBeenCalledTimes(2));
    expect(screen.queryByText('Сервис временно недоступен')).toBeNull();
    expect(Boolean((document.body.firstElementChild as HTMLElement).inert)).toBe(false);
  });

  it('не превращает локальную ошибку timeline в global outage', async () => {
    const api = createMockApiClient();
    const loadRoute = vi.fn<() => Promise<string>>()
      .mockResolvedValueOnce('cached route')
      .mockRejectedValueOnce(statusError(503));
    const queryClient = new QueryClient({
      defaultOptions: { mutations: { retry: false }, queries: { retry: false } },
    });

    render(
      <QueryClientProvider client={queryClient}>
        <App
          api={api}
          navigate={vi.fn()}
          route={createAppRoute('/chat', { projectId: 'project-white-port', sourceView: 'published' })}
        />
        <RouteQueryProbe load={loadRoute} outageScope="local" />
      </QueryClientProvider>,
    );

    await screen.findByRole('main', { name: 'Страница чата' });
    await waitFor(() => expect(loadRoute).toHaveBeenCalledTimes(1));
    await act(async () => {
      await queryClient.invalidateQueries({ queryKey: ['canonkeeper', 'route-probe'] });
    });
    await waitFor(() => expect(queryClient.getQueryState(['canonkeeper', 'route-probe'])?.status).toBe('error'));

    expect(screen.queryByText('Сервис временно недоступен')).toBeNull();
    expect(Boolean((document.body.firstElementChild as HTMLElement).inert)).toBe(false);
    expect(onlineManager.isOnline()).toBe(true);
  });

});
