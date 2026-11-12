import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { createMemoryHistory } from '@tanstack/react-router';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { createElement } from 'react';
import { afterAll, afterEach, beforeAll, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../api/mock';
import { draftBookId, projectId } from '../api/mock/repository/fixtures';
import { createAppRoute } from '../shared/navigation/app-route';
import { sectionTable } from '../ui/saga-sections';
import { backFromOfStep, CanonKeeperRouterProvider, createCanonKeeperRouter, routeOfLocation } from './router';

function createTestRouter(path = '/manuscript/books') {
  const history = createMemoryHistory({ initialEntries: [path] });
  const router = createCanonKeeperRouter(
    {
      api: createMockApiClient(),
      queryClient: new QueryClient(),
    },
    history,
  );
  return router;
}

beforeAll(() => {
  vi.stubGlobal('scrollTo', vi.fn());
});

afterAll(() => {
  vi.unstubAllGlobals();
});

afterEach(() => {
  window.history.replaceState(null, '', '/');
  vi.restoreAllMocks();
});

describe('CanonKeeper TanStack Router', () => {
  it('registers standalone Notes and retains only bounded filter route state', async () => {
    const router = createTestRouter('/notes?type=note&source=chat&projectId=p&bookId=b&note=n&query=private');
    await router.load();
    expect(router.state.location.pathname).toBe('/notes');
    const route = createAppRoute(router.state.location.pathname, new URLSearchParams(router.state.location.searchStr));
    // The saga of the notes is the saga of the shell; the filters and the open note are the address (specs/2026-11-11-ux7-notes.md).
    expect(Object.fromEntries(route.query)).toEqual({ source: 'chat', projectId: 'p', bookId: 'b', note: 'n' });
    expect(Object.fromEntries(createAppRoute('/notes', { sagas: 'all', bookId: 'b' }).query)).toEqual({ sagas: 'all' });
  });

  // specs/2026-11-10-ux2-shell-core.md SC2.1: every page of the menu is a route of its own section.
  it('routes every page of the table of the sections to its section', () => {
    expect(sectionTable.filter((entry) => entry.path).map((entry) => [entry.section, createAppRoute(entry.path!).surface])).toEqual([
      ['manuscript', 'manuscript'], ['saga', 'saga'], ['memory', 'memory'], ['chat', 'chat'], ['writing', 'writing'],
      ['drawing', 'drawing'], ['notes', 'notes'],
    ]);
  });

  it('keeps only the open draft in the /writing URL', () => {
    const route = createAppRoute('/writing', new URLSearchParams('projectId=p&draft=d&tab=hero'));
    expect(route.surface).toBe('writing');
    expect(Object.fromEntries(route.query)).toEqual({ projectId: 'p', draft: 'd' });
  });

  it('keeps only the filters of «Память» in the /memory URL', () => {
    const route = createAppRoute('/memory', new URLSearchParams('projectId=p&kind=plan&q=Кира&tab=hero'));
    expect(route.surface).toBe('memory');
    expect(Object.fromEntries(route.query)).toEqual({ projectId: 'p', kind: 'plan', q: 'Кира' });
    expect(createAppRoute('/memory', new URLSearchParams('projectId=p&kind=fact')).query.get('kind')).toBeNull();
  });

  // specs/2026-11-04-saga-map.md R6.2: the keys of the saga map; the keys of the old reference tabs are dropped.
  it('keeps only the saga map keys in the /saga URL', () => {
    const route = createAppRoute('/saga', new URLSearchParams(
      'projectId=p&sourceView=draft&node=%D0%BC%D0%B8%D1%80%D0%BE%D0%BD&focus=x&to=617&hide=place,item&only=new&chatId=c&tab=hero&character=y',
    ));
    expect(route.surface).toBe('saga');
    expect(Object.fromEntries(route.query)).toEqual({ projectId: 'p', node: 'мирон', focus: 'x', to: '617', hide: 'place,item', only: 'new' });
  });

  it('ignores legacy Chat bookId while preserving the selected history', () => {
    const route = createAppRoute('/chat', new URLSearchParams('projectId=project-white-port&bookId=book-02&chatId=chat-1&sourceView=published'));
    expect(route.invalidQuery).not.toBe(true);
    expect(Object.fromEntries(route.query)).toEqual({
      projectId: 'project-white-port', chatId: 'chat-1',
    });
  });

  it('сохраняет на /chat только каноническое query-состояние Chat', async () => {
    const route = createAppRoute('/chat', new URLSearchParams('projectId=project-white-port&bookId=book-02&chatId=chat-1&sourceView=published&prompt=private'));
    const router = createTestRouter('/chat?projectId=project-white-port&sourceView=published');

    await router.load();

    expect(route.surface).toBe('chat');
    expect(route.invalidQuery).toBe(true);
    expect(Object.fromEntries(route.query)).toEqual({
      projectId: 'project-white-port',
      chatId: 'chat-1',
    });
    expect(router.state.location.pathname).toBe('/chat');
  });

  it('marks rejected Chat values while retaining only safe query state', () => {
    const unknown = createAppRoute('/chat', new URLSearchParams('projectId=project-white-port&prompt=private'));
    const oldVersion = createAppRoute('/chat', new URLSearchParams('projectId=project-white-port&sourceView=private'));

    expect(unknown.invalidQuery).toBe(true);
    expect(Object.fromEntries(unknown.query)).toEqual({ projectId: 'project-white-port' });
    expect(oldVersion.invalidQuery).toBe(false);
    expect(Object.fromEntries(oldVersion.query)).toEqual({ projectId: 'project-white-port' });
  });

  it.each(['read', 'draft'])('preserves numeric expectedRevision in the %s router search contract', async (mode) => {
    const router = createTestRouter(`/manuscript/books?projectId=project-white-port&chapterId=chapter-12&mode=${mode}&expectedRevision=7`);
    await router.load();
    expect(router.state.matches.at(-1)?.search).toMatchObject({ projectId: 'project-white-port', chapterId: 'chapter-12', mode, expectedRevision: 7 });
    const route = createAppRoute(router.state.location.pathname, new URLSearchParams(router.state.location.searchStr));
    expect(route.query.get('expectedRevision')).toBe('7');
    expect(route.invalidQuery).toBe(false);
  });

  it('round-trips a source revision through the TanStack Router and App route', async () => {
    const router = createTestRouter('/manuscript/books');
    await router.load();

    await router.navigate({
      to: '/manuscript/books',
      search: {
        projectId: 'project-white-port',
        chapterId: 'chapter-12',
        mode: 'read',
        expectedRevision: 1,
      },
    });

    expect(router.state.location.searchStr).toContain('expectedRevision=1');
    const route = createAppRoute(router.state.location.pathname, new URLSearchParams(router.state.location.searchStr));
    expect(route.invalidQuery).toBe(false);
    expect(route.query.get('expectedRevision')).toBe('1');
  });

  // «Иллюстрированная книга» is opened by a link and survives a reload (specs/2026-11-10-ux1-drawing.md DX4.1): the
  // router reads ?illustrated=1 as a number and keeps it one, so the address the page reads stays illustrated=1.
  it('keeps «Иллюстрированная книга» of a chapter opened by its address', async () => {
    const router = createTestRouter('/manuscript/books?projectId=project-white-port&bookId=book-02&mode=read&chapterId=chapter-12&illustrated=1');
    await router.load();
    const validated = router.buildLocation({ to: router.latestLocation.pathname, search: true, params: true, hash: true, state: true, _includeValidateSearch: true } as never);
    expect(validated.searchStr).toContain('illustrated=1');
    const route = createAppRoute(validated.pathname, new URLSearchParams(validated.searchStr));
    expect(route.query.get('illustrated')).toBe('1');
    expect(route.query.get('chapterId')).toBe('chapter-12');
    // Any other value is not the switch.
    const other = createTestRouter('/manuscript/books?projectId=project-white-port&mode=read&illustrated=2');
    await other.load();
    const dropped = other.buildLocation({ to: other.latestLocation.pathname, search: true, params: true, hash: true, state: true, _includeValidateSearch: true } as never);
    expect(dropped.searchStr).not.toContain('illustrated');
    expect(dropped.searchStr).toContain('mode=read');
  });

  // specs/2026-11-10-ux1-drawing.md DX2.8, specs/2026-11-10-ux2-tariff-account.md TA3.6: «Тариф» of a refusal remembers
  // where the author came from in the history state of its entry, never in the address; another page and an unknown place
  // carry none.
  it('carries the place an author came to «Тариф» from in the history state', async () => {
    const history = createMemoryHistory({ initialEntries: ['/drawing?projectId=project-white-port'] });
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const router = createCanonKeeperRouter({ api: createMockApiClient(), queryClient }, history);
    history.push('/tariff', { returnPlace: 'drawing' } as never);
    await router.load();
    expect(history.location.search).toBe('');
    expect(routeOfLocation(router.state.location)).toMatchObject({ surface: 'tariff', returnPlace: 'drawing' });
    history.push('/tariff', { returnPlace: 'https://example.com' } as never);
    await router.load();
    expect(routeOfLocation(router.state.location).returnPlace).toBeUndefined();
    history.push('/account', { returnPlace: 'drawing' } as never);
    await router.load();
    expect(routeOfLocation(router.state.location).returnPlace).toBeUndefined();
  });

  // specs/2026-11-10-ux2-patterns-states.md PS4.2: where the author came from lives in the history state of the entry, never
  // in the address; an unknown section is no way back.
  it('carries the section an author came from by a link in the history state', async () => {
    const history = createMemoryHistory({ initialEntries: ['/chat?projectId=project-white-port'] });
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const router = createCanonKeeperRouter({ api: createMockApiClient(), queryClient }, history);
    history.push('/manuscript/books?projectId=project-white-port&mode=read', { from: { section: 'chat', name: 'Кто такой Сава?' } } as never);
    await router.load();
    expect(history.location.search).not.toContain('from');
    expect(routeOfLocation(router.state.location).from).toEqual({ section: 'chat', name: 'Кто такой Сава?' });
    history.push('/manuscript/books?projectId=project-white-port', { from: { section: 'https://example.com' } } as never);
    await router.load();
    expect(routeOfLocation(router.state.location).from).toBeUndefined();
    history.push('/saga?projectId=project-white-port');
    await router.load();
    expect(routeOfLocation(router.state.location).from).toBeUndefined();
  });

  // PS4.1, PS4.3: a step from a section carries it; a page that replaces its own address keeps it; the menu drops it.
  it('keeps the way back on a replace of the address and drops it on a step of the menu', () => {
    const chat = { section: 'chat' as const, name: 'Кто такой Сава?' };
    expect(backFromOfStep({ from: chat }, undefined)).toEqual(chat);
    expect(backFromOfStep({ replace: true }, chat)).toEqual(chat);
    expect(backFromOfStep(undefined, chat)).toBeUndefined();
    expect(backFromOfStep({ replace: true, from: null }, chat)).toBeUndefined();
    expect(backFromOfStep({ from: null }, chat)).toBeUndefined();
  });

  it('отбрасывает версию текста из маршрута рукописи', () => {
    for (const sourceView of ['published', 'draft', 'private']) {
      const route = createAppRoute('/manuscript/books', new URLSearchParams(`projectId=project-white-port&sourceView=${sourceView}&prompt=private`));
      expect(route.invalidQuery).toBe(false);
      expect(Object.fromEntries(route.query)).toEqual({ projectId: 'project-white-port' });
    }
  });

  it('drops the removed indexing activity keys from manuscript links', async () => {
    const route = createAppRoute(
      '/manuscript/books',
      new URLSearchParams('projectId=project-white-port&bookId=book-02&sourceView=published&indexingJobId=job-42&indexingRole=sources&prompt=private'),
    );

    expect(Object.fromEntries(route.query)).toEqual({
      projectId: 'project-white-port',
      bookId: 'book-02',
    });
  });

  it('ведёт из чата без готового текста в библиотеку и сохраняет history', async () => {
    const api = createMockApiClient();
    vi.spyOn(api, 'getIndexingStatus').mockResolvedValue({
      steps: [], published: { exists: false, context: null },
      draft: { exists: true, context: { state: 'ready', currentBook: null, retryableFailedBook: null } },
    });
    const history = createMemoryHistory({
      initialEntries: ['/manuscript/books', `/chat?projectId=${projectId}&bookId=${draftBookId}&sourceView=draft`],
      initialIndex: 1,
    });
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const router = createCanonKeeperRouter({ api, queryClient }, history);
    render(createElement(
      QueryClientProvider,
      { client: queryClient },
      createElement(CanonKeeperRouterProvider, { router }),
    ));

    expect(await screen.findByText('Здесь будут вопросы о вашей саге')).toBeTruthy();
    expect(screen.queryByRole('button', { name: /Черновик|Опубликован/ })).toBeNull();
    fireEvent.click(screen.getByRole('link', { name: 'к книгам' }));
    await waitFor(() => expect(history.location.pathname).toBe('/manuscript/books'));
    expect(history.location.href).not.toContain('sourceView=');
    expect(history.length).toBe(3);
  });

  it('не создаёт route или redirect для /chats', async () => {
    const route = createAppRoute('/chats', new URLSearchParams('projectId=project-white-port'));
    const router = createTestRouter('/chats?projectId=project-white-port');

    await router.load();

    expect(route.surface).toBe('unknown');
    expect([...route.query.entries()]).toEqual([]);
    expect(router.state.location.pathname).toBe('/chats');
  });

  // R1.1: the root path is the guest home; no redirect happens before the session is known.
  it('keeps the root path as the home page without search state', async () => {
    const route = createAppRoute('/', new URLSearchParams('projectId=project-white-port&prompt=private'));
    const router = createTestRouter('/?projectId=project-white-port');

    await router.load();

    expect(route.surface).toBe('home');
    expect([...route.query.entries()]).toEqual([]);
    expect(router.state.location.pathname).toBe('/');
  });

  it('takes a signed-in writer from the home page to the shelf in place of the history entry', async () => {
    const history = createMemoryHistory({ initialEntries: ['/'] });
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const router = createCanonKeeperRouter({ api: createMockApiClient(), queryClient }, history);
    render(createElement(
      QueryClientProvider,
      { client: queryClient },
      createElement(CanonKeeperRouterProvider, { router }),
    ));

    await waitFor(() => expect(history.location.pathname).toBe('/manuscript/books'));
    expect(history.length).toBe(1);
  });

  it('keeps auth return targets as supporting route state', async () => {
    const route = createAppRoute('/auth/login', new URLSearchParams('returnTo=/manuscript/books&prompt=private'));
    const router = createTestRouter('/auth/login?returnTo=/manuscript/books');

    await router.load();

    expect(route.surface).toBe('auth');
    expect(route.query.get('returnTo')).toBe('/manuscript/books');
    expect(route.query.has('prompt')).toBe(false);
    expect(router.state.location.pathname).toBe('/auth/login');
    expect(sectionTable.map((item) => item.path)).not.toContain('/auth/login');
  });

  it('keeps only same-origin product return targets', () => {
    const external = createAppRoute('/auth/login', new URLSearchParams('returnTo=//example.test/private'));
    const authLoop = createAppRoute('/auth/login', new URLSearchParams('returnTo=/auth/register'));
    const product = createAppRoute('/auth/login', new URLSearchParams('returnTo=/chat?projectId=project-white-port&prompt=private'));

    expect(external.query.has('returnTo')).toBe(false);
    expect(authLoop.query.has('returnTo')).toBe(false);
    expect(product.query.get('returnTo')).toBe('/chat?projectId=project-white-port');
  });

  it('keeps unknown paths on the fallback surface without private search state', async () => {
    const route = createAppRoute('/missing-route', new URLSearchParams('projectId=project-white-port&prompt=private'));
    const router = createTestRouter('/missing-route?projectId=project-white-port');

    await router.load();

    expect(route.surface).toBe('unknown');
    expect([...route.query.entries()]).toEqual([]);
    expect(router.state.location.pathname).toBe('/missing-route');
  });

});

