import { onlineManager, QueryClient, QueryClientProvider, useQuery } from '@tanstack/react-query';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../api/mock';
import { ApiStatusError, NetworkApiError, type ApiProblem } from '../shared/api';
import { useAuthShellStore } from './auth-store';
import { createAppRoute, parseRoute, type AppRoute } from '../shared/navigation/app-route';
import { App } from '../App';
import { ServiceUnavailable } from './ServiceUnavailable';

// The page gives its content; the shell of the root puts the top bar, the sections and the content mark around it.
vi.mock('../features/chat/ChatPage', () => ({
  ChatPage: () => (
    <section aria-label="Страница чата">
      <button type="button">Чаты</button>
      <h1 data-page-heading="" tabIndex={-1}>Чат</h1>
      Чат загружен
    </section>
  ),
}));

// The page of notes has no heading here: the focus goes to the content of the shell. `source=chat` keeps a dialog open over it.
vi.mock('../pages/NotesPage', () => ({
  NotesPage: ({ route }: { route: AppRoute }) => <>
    <section aria-label="Каталог заметок" />
    {route.query.get('source') === 'chat' ? <div aria-label="Новая заметка" aria-modal="true" role="dialog"><button type="button">Сохранить</button></div> : null}
  </>,
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
    expect(await screen.findByRole('region', { name: 'Каталог заметок' })).toBeTruthy();
    expect(screen.getByRole('main', { name: 'Заметки' })).toBeTruthy();
  });

  it('даёт понятные выходы с неизвестной страницы без технических маршрутов', async () => {
    const navigate = vi.fn();
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(<QueryClientProvider client={queryClient}>
      <App api={createMockApiClient()} navigate={navigate} route={parseRoute('/unknown-private-path')} />
    </QueryClientProvider>);
    expect(await screen.findByRole('heading', { name: 'Страница не найдена' })).toBeTruthy();
    expect(screen.queryByText('/unknown-private-path')).toBeNull();
    expect(screen.getByRole('alert', { name: 'Такой страницы нет' })).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'К книгам' }));
    expect(navigate).toHaveBeenCalledWith('/manuscript/books');
  });

  // specs/2026-11-10-ux2-shell-core.md SC1.1, SC1.9: the root puts one shell around the page, with the saga of the author.
  it('puts the page not found into the one shell: one top bar, the sections and the saga', async () => {
    const navigate = vi.fn();
    render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <App api={createMockApiClient()} navigate={navigate} route={parseRoute('/no-such-page')} />
    </QueryClientProvider>);
    expect(await screen.findByRole('button', { name: 'Сага: Хроники Белого порта' })).toBeTruthy();
    expect(screen.getAllByRole('banner')).toHaveLength(1);
    expect(screen.getAllByRole('main')).toHaveLength(1);
    expect(document.querySelectorAll('[data-page-content]')).toHaveLength(1);
    // The page names itself by its heading; the top bar names the saga (specs/2026-11-11-ux2-polish.md PL1.1).
    expect(screen.getByRole('main').querySelector('h1')?.textContent).toBe('Страница не найдена');
    expect(screen.getByRole('banner').textContent).not.toContain('Страница не найдена');
    fireEvent.click(screen.getByRole('button', { name: 'Чат' }));
    expect(navigate).toHaveBeenCalledWith('/chat', { projectId: 'project-white-port' });
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

    expect(await screen.findByRole('region', { name: 'Страница чата' })).toBeTruthy();
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
      // specs/2026-11-11-ux9-account.md AC3.3: the screen checks by itself, in the words of the page.
      expect(screen.getByText('Проверю снова через 5 с')).toBeTruthy();
      expect(screen.getByText('req-app-test').closest('span')?.textContent).toBe('Код для поддержки: req-app-test');
      expect(decodeURIComponent(screen.getByRole('link', { name: 'Написать нам' }).getAttribute('href') ?? ''))
        .toContain('CanonKeeper: ошибка req-app-test');
    } else {
      expect(screen.queryByText(/Код для поддержки/u)).toBeNull();
    }
  });

  // specs/2026-11-11-ux9-account.md AC3.3: over an open page the outage is a strip above it — the page stays readable.
  it('сохраняет текущую страницу читаемой под полосой и восстанавливает её Retry без навигации', async () => {
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

    await screen.findByRole('region', { name: 'Страница чата' });
    await waitFor(() => expect(loadRoute).toHaveBeenCalledTimes(1));
    await act(async () => {
      await queryClient.invalidateQueries({ queryKey: ['canonkeeper', 'route-probe'] });
    });

    const strip = await screen.findByRole('alert', { name: 'Сервис временно недоступен' });
    expect(strip.closest('[data-page-notices]')).toBeTruthy();
    expect(strip.textContent).toContain('Набранный текст на месте — отправить его можно будет, когда связь вернётся.');
    expect(getCurrentUser).toHaveBeenCalledTimes(2);
    expect(document.querySelector('[inert]')).toBeNull();
    expect(document.querySelector('[aria-modal="true"]')).toBeNull();
    expect(onlineManager.isOnline()).toBe(false);
    expect(navigate).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole('button', { name: 'Повторить' }));

    await waitFor(() => expect(screen.queryByRole('alert', { name: 'Сервис временно недоступен' })).toBeNull());
    expect(getCurrentUser).toHaveBeenCalledTimes(3);
    expect(loadRoute).toHaveBeenCalledTimes(3);
    expect(Boolean((document.body.firstElementChild as HTMLElement).inert)).toBe(false);
    expect(screen.getByRole('region', { name: 'Страница чата' })).toBeTruthy();
    expect(onlineManager.isOnline()).toBe(true);
    expect(navigate).not.toHaveBeenCalled();
    expect(await screen.findByText('Соединение восстановлено')).toBeTruthy();
  });

  // The flake of the full check of 12.11: «Повторить» whose read of the session another read of the page overtook left
  // without the line. The end of the outage says it, whoever read the session — the button, the check of the strip, the
  // network coming back or the page itself.
  it('says «Соединение восстановлено» when a read of the page itself ends the outage, not «Повторить»', async () => {
    const api = createMockApiClient();
    const currentUser = await api.getCurrentUser();
    vi.spyOn(api, 'getCurrentUser')
      .mockResolvedValueOnce(currentUser)
      .mockRejectedValueOnce(statusError(503))
      .mockResolvedValue(currentUser);
    const loadRoute = vi.fn<() => Promise<string>>()
      .mockResolvedValueOnce('cached route')
      .mockRejectedValueOnce(statusError(503))
      .mockResolvedValue('fresh route');
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

    await screen.findByRole('region', { name: 'Страница чата' });
    await waitFor(() => expect(loadRoute).toHaveBeenCalledTimes(1));
    await act(async () => {
      await queryClient.invalidateQueries({ queryKey: ['canonkeeper', 'route-probe'] });
    });
    await screen.findByRole('alert', { name: 'Сервис временно недоступен' });
    expect(screen.queryByText('Соединение восстановлено')).toBeNull();

    await act(async () => {
      await queryClient.refetchQueries({ predicate: (query) => query.queryKey[1] === 'session' });
    });

    await waitFor(() => expect(screen.queryByRole('alert', { name: 'Сервис временно недоступен' })).toBeNull());
    expect(await screen.findByText('Соединение восстановлено')).toBeTruthy();
    expect(onlineManager.isOnline()).toBe(true);
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

    await screen.findByRole('region', { name: 'Страница чата' });
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

    await screen.findByRole('region', { name: 'Страница чата' });
    await waitFor(() => expect(loadRoute).toHaveBeenCalledTimes(1));
    await act(async () => {
      await queryClient.invalidateQueries({ queryKey: ['canonkeeper', 'route-probe'] });
    });
    await waitFor(() => expect(queryClient.getQueryState(['canonkeeper', 'route-probe'])?.status).toBe('error'));

    expect(screen.queryByText('Сервис временно недоступен')).toBeNull();
    expect(Boolean((document.body.firstElementChild as HTMLElement).inert)).toBe(false);
    expect(onlineManager.isOnline()).toBe(true);
  });


  // specs/2026-11-10-ux1-shell.md SH2: the focus follows the author to the content of the new page.
  describe('the focus after a change of the page', () => {
    function renderAt(route: AppRoute) {
      const api = createMockApiClient();
      const queryClient = new QueryClient({ defaultOptions: { mutations: { retry: false }, queries: { retry: false } } });
      const page = (next: AppRoute) => <QueryClientProvider client={queryClient}><App api={api} navigate={vi.fn()} route={next} /></QueryClientProvider>;
      const view = render(page(route));
      return { go: (next: AppRoute) => view.rerender(page(next)) };
    }
    const frames = () => act(() => new Promise((resolve) => setTimeout(resolve, 80)));

    it('leaves the first load alone', async () => {
      renderAt(createAppRoute('/chat', { projectId: 'project-white-port' }));
      await screen.findByRole('heading', { name: 'Чат' });
      await frames();
      expect(document.activeElement).toBe(document.body);
    });

    it('stands on the heading of the new page, or on its content without one', async () => {
      const { go } = renderAt(createAppRoute('/notes'));
      await screen.findByRole('region', { name: 'Каталог заметок' });
      go(createAppRoute('/chat', { projectId: 'project-white-port' }));
      const heading = await screen.findByRole('heading', { name: 'Чат' });
      await waitFor(() => expect(document.activeElement).toBe(heading));
      go(createAppRoute('/notes'));
      await screen.findByRole('region', { name: 'Каталог заметок' });
      const notes = screen.getByRole('main', { name: 'Заметки' });
      await waitFor(() => expect(document.activeElement).toBe(notes));
    });

    it('stays where it is when only the parameters of the address change', async () => {
      const { go } = renderAt(createAppRoute('/chat', { projectId: 'project-white-port' }));
      await screen.findByRole('heading', { name: 'Чат' });
      screen.getByRole('button', { name: 'Чаты' }).focus();
      go(createAppRoute('/chat', { projectId: 'project-white-port', chatId: 'chat-1' }));
      await frames();
      expect(document.activeElement).toBe(screen.getByRole('button', { name: 'Чаты' }));
    });

    it('stays in an open dialog', async () => {
      const { go } = renderAt(createAppRoute('/chat', { projectId: 'project-white-port' }));
      await screen.findByRole('heading', { name: 'Чат' });
      go(createAppRoute('/notes', { source: 'chat' }));
      const save = await screen.findByRole('button', { name: 'Сохранить' });
      save.focus();
      await frames();
      expect(document.activeElement).toBe(save);
    });

    it('opens every page of the author with «К содержимому» first, and it leads to the content', async () => {
      renderAt(createAppRoute('/chat', { projectId: 'project-white-port' }));
      const heading = await screen.findByRole('heading', { name: 'Чат' });
      const skip = screen.getByRole('link', { name: 'К содержимому' });
      const focusable = [...document.querySelectorAll<HTMLElement>('a[href], button:not([disabled]), input, textarea, select, [tabindex]:not([tabindex="-1"])')];
      expect(focusable[0]).toBe(skip);
      fireEvent.click(skip);
      expect(document.activeElement).toBe(heading);
    });
  });
});

// specs/2026-11-11-ux9-account.md AC3.3: no network on the device is not an outage of the service; the screen checks by itself.
describe('ServiceUnavailable', () => {
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it('tells no network on the device from an outage of the service and checks by itself', () => {
    vi.useFakeTimers();
    const onLine = vi.spyOn(window.navigator, 'onLine', 'get').mockReturnValue(false);
    const onRetry = vi.fn();
    const view = render(<ServiceUnavailable attempt={1} fullPage onRetry={onRetry} retrying={false} />);
    expect(screen.getByRole('heading', { level: 1, name: 'Нет интернета на этом устройстве' })).toBeTruthy();
    expect(getComputedStyle(screen.getByText('Проверю снова через 5 с')).fontFamily).not.toMatch(/mono/iu);
    act(() => vi.advanceTimersByTime(5_000));
    expect(onRetry).toHaveBeenCalledOnce();
    view.rerender(<ServiceUnavailable attempt={2} fullPage onRetry={onRetry} retrying />);
    expect(screen.getByText('Проверяю… попытка 2')).toBeTruthy();
    // The network came back: a check at once.
    onLine.mockReturnValue(true);
    act(() => { window.dispatchEvent(new Event('online')); });
    expect(onRetry).toHaveBeenCalledTimes(2);
    view.rerender(<ServiceUnavailable attempt={2} fullPage onRetry={onRetry} retrying={false} />);
    expect(screen.getByRole('heading', { level: 1, name: 'Сервис временно недоступен' })).toBeTruthy();
    expect(screen.getByText('Проверю снова через 10 с')).toBeTruthy();
  });
});
