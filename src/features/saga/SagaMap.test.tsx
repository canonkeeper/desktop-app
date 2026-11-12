import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../api/mock';
import { projectId } from '../../api/mock/repository/fixtures';
import type { SourceView } from '../../entities/saga/api';
import { createAppRoute } from '../../shared/navigation/app-route';
import type { MapCanvasProps } from './MapCanvas';
import { HiddenPanel } from './HiddenPanel';
import { SagaMap } from './SagaMap';
import { ToastsProvider } from '../../ui/feedback/Toast';

// WebGL is not in jsdom: the canvas is a stand-in that shows what it would draw.
vi.mock('./MapCanvas', () => ({
  MapCanvas: ({ graph }: MapCanvasProps) => <div data-links={graph.links.length} data-nodes={graph.nodes.map((node) => node.key).join(',')} data-testid="saga-map-canvas" />,
}));

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

type Api = ReturnType<typeof createMockApiClient>;

function wideScreen() {
  vi.stubGlobal('matchMedia', (query: string) => ({
    addEventListener: () => undefined, matches: false, media: query, removeEventListener: () => undefined,
  }));
}

function renderMap(query: Record<string, string> = {}, sourceView: SourceView = 'draft', api: Api = createMockApiClient()) {
  wideScreen();
  const navigate = vi.fn();
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}><ToastsProvider>
      <SagaMap api={api} canEdit navigate={navigate} projectId={projectId} route={createAppRoute('/saga', { projectId, ...query })} sourceView={sourceView} />
    </ToastsProvider></QueryClientProvider>,
  );
  return { api, navigate };
}

function lastNavigation(navigate: ReturnType<typeof vi.fn>) {
  const [path, query, options] = navigate.mock.calls.at(-1) ?? [];
  return { options, path, query: Object.fromEntries(Object.entries(query ?? {}).filter(([, value]) => value !== undefined)) };
}

const canvas = () => screen.getByTestId('saga-map-canvas');

// specs/2026-11-04-saga-map.md UC3–UC6.
describe('«Карта саги»', () => {
  it('draws the saga to its last chapter with the counts of the categories', async () => {
    renderMap();
    await waitFor(() => expect(canvas().dataset.nodes).toBe('мара,сава,гильдия смотрителей,белый порт,кинжал мары,северная пристань'));
    expect(canvas().dataset.links).toBe('3');
    const filters = screen.getByRole('group', { name: 'Что показывать' });
    expect(within(filters).getByRole('button', { name: /Герои\s*2/u }).getAttribute('aria-pressed')).toBe('true');
    expect(within(filters).getByRole('button', { name: /Места\s*2/u })).toBeTruthy();
    expect(screen.getByText('Показаны все: 6')).toBeTruthy();
    expect(screen.getByText('Вся сага')).toBeTruthy();
    expect(within(screen.getByRole('list', { name: 'Узлы на карте' })).getAllByRole('button')).toHaveLength(6);
  });

  it('opens a node from the list beside the canvas and from the search, also an entry that is not on the map', async () => {
    const { navigate } = renderMap();
    await screen.findByTestId('saga-map-canvas');
    fireEvent.click(within(screen.getByRole('list', { name: 'Узлы на карте' })).getByRole('button', { name: 'Сава' }));
    expect(lastNavigation(navigate)).toEqual({ options: undefined, path: '/saga', query: { projectId, node: 'сава' } });

    const field = screen.getByRole('searchbox', { name: 'Найти на карте' });
    fireEvent.focus(field);
    fireEvent.change(field, { target: { value: 'северянка' } });
    const results = screen.getByRole('list', { name: 'Найдено' });
    fireEvent.click(within(results).getByRole('button', { name: /Мара/u }));
    expect(lastNavigation(navigate).query).toEqual({ projectId, node: 'мара' });

    fireEvent.focus(field);
    fireEvent.change(field, { target: { value: 'младший' } });
    const elsewhere = await within(screen.getByRole('list', { name: 'Найдено' })).findByRole('button', { name: /Младший смотритель.*нет на карте/u });
    fireEvent.click(elsewhere);
    expect(lastNavigation(navigate).query).toEqual({ projectId, node: 'младший смотритель' });

    fireEvent.focus(field);
    fireEvent.change(field, { target: { value: 'сава' } });
    expect(screen.getByRole('list', { name: 'Найдено' })).toBeTruthy();
    fireEvent.blur(field);
    expect(screen.queryByRole('list', { name: 'Найдено' })).toBeNull();
  });

  // R10.6: an entry off the map comes from the prepared rows, and the glossary is not asked.
  it('finds an entry off the map without the glossary and asks once for a query', async () => {
    const api = createMockApiClient();
    const glossary = vi.spyOn(api, 'listSagaGlossary');
    const search = vi.spyOn(api, 'searchSagaMap');
    const { navigate } = renderMap({}, 'draft', api);
    await screen.findByTestId('saga-map-canvas');
    const field = screen.getByRole('searchbox', { name: 'Найти на карте' });
    fireEvent.focus(field);
    fireEvent.change(field, { target: { value: 'младший' } });
    const found = await within(screen.getByRole('list', { name: 'Найдено' })).findByRole('button', { name: /Младший смотритель.*нет на карте/u });
    expect(search).toHaveBeenCalledTimes(1);
    expect(search.mock.calls[0]![1]).toMatchObject({ sourceView: 'draft', q: 'младший', limit: 8 });
    fireEvent.click(found);
    expect(lastNavigation(navigate).query).toEqual({ projectId, node: 'младший смотритель' });
    fireEvent.focus(field);
    fireEvent.change(field, { target: { value: 'младший' } });
    await within(screen.getByRole('list', { name: 'Найдено' })).findByRole('button', { name: /Младший смотритель/u });
    expect(search).toHaveBeenCalledTimes(1);
    expect(glossary).not.toHaveBeenCalled();
  });

  it('opens the first found node by Enter and spends the key: the drawer of a narrow window takes the focus', async () => {
    const { navigate } = renderMap();
    await screen.findByTestId('saga-map-canvas');
    const field = screen.getByRole('searchbox', { name: 'Найти на карте' });
    fireEvent.change(field, { target: { value: 'мара' } });
    expect(fireEvent.keyDown(field, { key: 'Enter' })).toBe(false);
    expect(lastNavigation(navigate).query).toEqual({ projectId, node: 'мара' });
  });

  it('hides a category and keeps it in the URL', async () => {
    const { navigate } = renderMap({ hide: 'place' });
    await waitFor(() => expect(canvas().dataset.nodes).toBe('мара,сава,гильдия смотрителей,кинжал мары'));
    const filters = screen.getByRole('group', { name: 'Что показывать' });
    expect(within(filters).getByRole('button', { name: /Места/u }).getAttribute('aria-pressed')).toBe('false');
    fireEvent.click(within(filters).getByRole('button', { name: /Предметы/u }));
    expect(lastNavigation(navigate)).toEqual({ options: { replace: true }, path: '/saga', query: { projectId, hide: 'place,item' } });
  });

  it('moves the graph with the scale at once and keeps the chapter in the URL after a pause', async () => {
    const { navigate } = renderMap();
    const slider = await screen.findByRole('slider', { name: 'Сага на момент главы' });
    fireEvent.change(slider, { target: { value: '0' } });
    expect(canvas().dataset.nodes).toBe('мара,сава,белый порт,кинжал мары,северная пристань');
    // specs/2026-11-10-ux1-names.md N4.2: the scale names the chapter by its place, never by its key.
    expect(screen.getByText('Книга 1, гл. 1 „Туманный берег“')).toBeTruthy();
    expect(slider.getAttribute('aria-valuetext')).toBe('Книга 1, гл. 1 „Туманный берег“');
    expect(document.body.textContent).not.toMatch(/b\d+c\d+|¶/u);
    await waitFor(() => expect(lastNavigation(navigate)).toEqual({ options: { replace: true }, path: '/saga', query: { projectId, to: 'b1c1' } }));
  });

  it('shows the map up to the chapter of the URL and the surroundings of a node', async () => {
    const { navigate } = renderMap({ to: 'b2c12', focus: 'сава' });
    await waitFor(() => expect(canvas().dataset.nodes).toBe('мара,сава'));
    expect(screen.getByText('Окружение:')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Вся карта' }));
    expect(lastNavigation(navigate).query).toEqual({ projectId, to: 'b2c12' });
  });

  it('opens the card of a node: its names, where it first appears, its links, events and the chat', async () => {
    const { navigate } = renderMap({ node: 'мара' });
    const card = await screen.findByRole('complementary', { name: 'Карточка: Мара' });
    expect(await within(card).findByText('Также: Северянка')).toBeTruthy();
    expect(within(card).getByText('в 4 главах')).toBeTruthy();
    const links = within(card).getByRole('region', { name: 'Связи' });
    // specs/2026-11-11-ux9-shelf-chat.md SC3.5 (W-5C): the links in the words of the author.
    expect(within(links).getByText('Вместе в 1 событии')).toBeTruthy();
    expect(within(within(links).getByRole('list', { name: 'Вместе: Сава' })).getByText(/Сава дарит Маре старый кинжал/u)).toBeTruthy();
    fireEvent.click(within(links).getByRole('button', { name: 'Подробнее: Сава' }));
    expect(within(links).getByRole('list', { name: 'Главы: Сава' })).toBeTruthy();
    fireEvent.click(within(links).getByRole('button', { name: 'Сава' }));
    expect(lastNavigation(navigate).query).toEqual({ projectId, node: 'сава' });

    const events = within(card).getByRole('region', { name: 'События' });
    fireEvent.click(within(events).getAllByRole('link')[0]!);
    expect(lastNavigation(navigate).path).toBe('/manuscript/books');
    // The reader offers «← К карте: Мара» back to the card (specs/2026-11-10-ux2-patterns-states.md PS4.1).
    expect(lastNavigation(navigate).options).toEqual({ from: { section: 'saga', name: 'Мара' } });

    fireEvent.click(within(card).getByRole('button', { name: 'Спросить в чате' }));
    // SC3.5 (W-5E, П-13): the question needs no case of the name, and the chat offers «← К карте: Мара».
    expect(navigate).toHaveBeenLastCalledWith('/chat', { projectId }, {
      chatQuestion: 'Расскажи про героя: Мара — кто это, главные события и с кем связан по книге.', from: { section: 'saga', name: 'Мара' },
    });
    expect(within(card).getByRole('region', { name: 'Что изменилось' })).toBeTruthy();
    fireEvent.click(within(card).getByRole('button', { name: 'Показать, с кем связан' }));
    expect(lastNavigation(navigate).query).toEqual({ projectId, node: 'мара', focus: 'мара' });
    fireEvent.click(within(card).getByRole('button', { name: 'Закрыть карточку' }));
    expect(lastNavigation(navigate).query).toEqual({ projectId });
  });

  it('reads the card up to the chapter of the map', async () => {
    const { api } = renderMap({ node: 'мара', to: 'b1c1' });
    const read = vi.spyOn(api, 'getSagaMapNode');
    const card = await screen.findByRole('complementary', { name: 'Карточка: Мара' });
    expect(await within(card).findByText('в 1 главе')).toBeTruthy();
    expect(read.mock.calls.every(([, options]) => options.to === 'b1c1')).toBe(true);
  });

  // R10.5: what the map knows is there before the card comes, and a card read once is not read again.
  it('shows the name, the category and the chapters of a node at once, and the rest in place', async () => {
    const api = createMockApiClient();
    const answer = api.getSagaMapNode.bind(api);
    let release: () => void = () => undefined;
    const held = new Promise<void>((resolve) => { release = resolve; });
    vi.spyOn(api, 'getSagaMapNode').mockImplementation(async (...args) => {
      await held;
      return answer(...args);
    });
    renderMap({ node: 'мара' }, 'draft', api);
    const card = await screen.findByRole('complementary', { name: 'Карточка: Мара' });
    expect(within(card).getByRole('heading', { level: 2, name: 'Мара' })).toBeTruthy();
    expect(within(card).getByText('персонаж')).toBeTruthy();
    expect(within(card).getByText('в 4 главах')).toBeTruthy();
    // The card loads in the form of its rows, announced as busy (PS3.1).
    expect(card.querySelector('[data-loading][aria-busy="true"]')).toBeTruthy();
    expect(within(card).queryByRole('region', { name: 'Связи' })).toBeNull();
    release();
    expect(await within(card).findByRole('region', { name: 'Связи' })).toBeTruthy();
    expect(card.querySelector('[data-loading]')).toBeNull();
    expect(within(card).getByText('в 4 главах')).toBeTruthy();
  });

  it('asks for a card once for a revision of the map', async () => {
    const api = createMockApiClient();
    const read = vi.spyOn(api, 'getSagaMapNode');
    wideScreen();
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const page = (node?: string) => (
      <QueryClientProvider client={queryClient}><ToastsProvider>
        <SagaMap api={api} canEdit navigate={vi.fn()} projectId={projectId} route={createAppRoute('/saga', { projectId, ...(node ? { node } : {}) })} sourceView="draft" />
      </ToastsProvider></QueryClientProvider>
    );
    const { rerender } = render(page('мара'));
    await within(await screen.findByRole('complementary', { name: 'Карточка: Мара' })).findByRole('region', { name: 'Связи' });
    expect(read).toHaveBeenCalledTimes(1);
    rerender(page());
    expect(screen.queryByRole('complementary', { name: 'Карточка: Мара' })).toBeNull();
    rerender(page('мара'));
    const again = screen.getByRole('complementary', { name: 'Карточка: Мара' });
    expect(within(again).getByRole('region', { name: 'Связи' })).toBeTruthy();
    expect(read).toHaveBeenCalledTimes(1);
  });

  // R10.3: the map of a saga with cards is prepared in the background and the page waits for it by itself.
  it('says the map is being prepared and asks again until it is there', async () => {
    const api = createMockApiClient();
    const ready = await api.getSagaMap(projectId, { sourceView: 'draft' });
    const read = vi.spyOn(api, 'getSagaMap').mockResolvedValueOnce({
      revision: '', coverage: { chapters: 3, withCards: 3 }, chapters: [], hero: null, nodesTotal: 0, nodes: [], links: [], building: true,
    }).mockResolvedValue(ready);
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      renderMap({}, 'draft', api);
      expect(await screen.findByText('Карта собирается — страница обновится сама')).toBeTruthy();
      expect(screen.queryByText(/Карта появится/u)).toBeNull();
      await act(async () => { await vi.advanceTimersByTimeAsync(5_100); });
      await waitFor(() => expect(screen.getByRole('group', { name: 'Что показывать' })).toBeTruthy());
      expect(read).toHaveBeenCalledTimes(2);
      await act(async () => { await vi.advanceTimersByTimeAsync(11_000); });
      expect(read).toHaveBeenCalledTimes(2);
    } finally {
      vi.useRealTimers();
    }
  });

  // R9.5, R10.3: a map behind what the author changed stays on the screen and says so, until the new one comes by itself.
  it('says the map is being updated and takes the new one by itself', async () => {
    const api = createMockApiClient();
    const ready = await api.getSagaMap(projectId, { sourceView: 'draft' });
    const read = vi.spyOn(api, 'getSagaMap').mockResolvedValueOnce({ ...ready, building: true }).mockResolvedValue({ ...ready, revision: 'next' });
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      renderMap({}, 'draft', api);
      // One line of the long work, with no term the server did not give (PS3.3).
      const work = await screen.findByRole('region', { name: 'Карта обновляется' });
      expect(within(work).getByText('можно уйти — новое появится на карте само')).toBeTruthy();
      expect(within(work).queryByRole('progressbar')).toBeNull();
      expect(canvas().dataset.nodes).toContain('мара');
      await act(async () => { await vi.advanceTimersByTimeAsync(5_100); });
      await waitFor(() => expect(screen.queryByRole('region', { name: 'Карта обновляется' })).toBeNull());
      expect(read).toHaveBeenCalledTimes(2);
    } finally {
      vi.useRealTimers();
    }
  });

  // R9.5: a card of a name the rows do not have yet waits for the build instead of saying there is no such hero.
  it('opens a card the map is still being built for when the new map comes', async () => {
    const api = createMockApiClient();
    const ready = await api.getSagaMap(projectId, { sourceView: 'draft' });
    const mara = await api.getSagaMapNode(projectId, { sourceView: 'draft', key: 'мара' });
    vi.spyOn(api, 'getSagaMap').mockResolvedValueOnce({ ...ready, building: true }).mockResolvedValue({ ...ready, revision: 'next' });
    const answer = api.getSagaMapNode.bind(api);
    const read = vi.spyOn(api, 'getSagaMapNode').mockImplementationOnce(answer).mockResolvedValue({ ...mara, key: 'новая запись', name: 'Новая запись' });
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      renderMap({ node: 'новая запись' }, 'draft', api);
      expect(await screen.findByText('Карта обновляется')).toBeTruthy();
      expect(screen.queryByText('Такого героя в саге больше нет')).toBeNull();
      await act(async () => { await vi.advanceTimersByTimeAsync(5_100); });
      expect(await screen.findByRole('heading', { level: 2, name: 'Новая запись' })).toBeTruthy();
      expect(read).toHaveBeenCalledTimes(2);
    } finally {
      vi.useRealTimers();
    }
  });

  const record = {
    id: 'record-1', name: 'Мара', kind: 'character' as const, description: 'Старое описание', properties: [], section: '', sourceName: '', sourceKind: 'text' as const,
    revision: 1, createdAt: '2026-11-04T10:00:00Z', updatedAt: '2026-11-04T10:00:00Z',
  };

  // R9.6: the card is from the rows of the last build, and what the server answered about the record is on it at once.
  it('shows a changed record on the card at once and asks the map, not the card, again', async () => {
    const api = createMockApiClient();
    const mara = await api.getSagaMapNode(projectId, { sourceView: 'draft', key: 'мара' });
    const card = vi.spyOn(api, 'getSagaMapNode').mockResolvedValue({ ...mara, material: record });
    const update = vi.spyOn(api, 'updateAuthorMaterial').mockResolvedValue({ ...record, description: 'Новое описание', revision: 2 });
    renderMap({ node: 'мара' }, 'draft', api);
    const block = await screen.findByRole('region', { name: 'По вашим материалам' });
    expect(within(block).getByText('Старое описание')).toBeTruthy();
    const map = vi.spyOn(api, 'getSagaMap');
    fireEvent.click(within(block).getByRole('button', { name: 'Изменить' }));
    const dialog = screen.getByRole('dialog', { name: 'Изменить запись' });
    fireEvent.change(within(dialog).getByLabelText('Описание'), { target: { value: 'Новое описание' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Сохранить' }));
    await waitFor(() => expect(within(screen.getByRole('region', { name: 'По вашим материалам' })).getByText('Новое описание')).toBeTruthy());
    expect(card).toHaveBeenCalledTimes(1);
    await waitFor(() => expect(map).toHaveBeenCalledTimes(1));

    fireEvent.click(within(screen.getByRole('region', { name: 'По вашим материалам' })).getByRole('button', { name: 'Изменить' }));
    fireEvent.click(within(screen.getByRole('dialog', { name: 'Изменить запись' })).getByRole('button', { name: 'Сохранить' }));
    await waitFor(() => expect(update).toHaveBeenCalledTimes(2));
    expect(update.mock.calls[1]![2]).toMatchObject({ expectedRevision: 2 });
  });

  it('takes a deleted record off the card, and closes a card that was only that record', async () => {
    const api = createMockApiClient();
    const mara = await api.getSagaMapNode(projectId, { sourceView: 'draft', key: 'мара' });
    vi.spyOn(api, 'getSagaMapNode').mockResolvedValue({ ...mara, material: record });
    vi.spyOn(api, 'deleteAuthorMaterial').mockResolvedValue(undefined);
    const { navigate } = renderMap({ node: 'мара' }, 'draft', api);
    const block = await screen.findByRole('region', { name: 'По вашим материалам' });
    fireEvent.click(within(block).getByRole('button', { name: 'Действия записи' }));
    fireEvent.click(within(block).getByRole('menuitem', { name: 'Удалить запись…' }));
    fireEvent.click(within(screen.getByRole('dialog', { name: /Удалить запись/u })).getByRole('button', { name: 'Удалить запись' }));
    await waitFor(() => expect(screen.queryByRole('region', { name: 'По вашим материалам' })).toBeNull());
    expect(screen.getByRole('complementary', { name: 'Карточка: Мара' })).toBeTruthy();
    expect(navigate).not.toHaveBeenCalled();
    cleanup();

    const own = { ...mara, first: null, mentions: 0, links: [], events: [], changes: [], eventsTotal: 0, changesTotal: 0, material: record };
    vi.spyOn(api, 'getSagaMapNode').mockResolvedValue(own);
    const second = renderMap({ node: 'мара' }, 'draft', api);
    const only = await screen.findByRole('region', { name: 'По вашим материалам' });
    fireEvent.click(within(only).getByRole('button', { name: 'Действия записи' }));
    fireEvent.click(within(only).getByRole('menuitem', { name: 'Удалить запись…' }));
    fireEvent.click(within(screen.getByRole('dialog', { name: /Удалить запись/u })).getByRole('button', { name: 'Удалить запись' }));
    await waitFor(() => expect(lastNavigation(second.navigate).query).toEqual({ projectId }));
  });

  // specs/2026-11-10-ux3-saga-state.md SS3.2: the map says what the helper has read, by the counts and the time of the
  // shelf, without «карточки глав» and the shelf; it leads to the books while nothing is read.
  it('says when the helper has read a part of the saga, and leads to the books while nothing is read', async () => {
    renderMap({}, 'published');
    expect(await screen.findByText('Помощник прочитал 1 из 2 глав — карта дополняется сама')).toBeTruthy();
    cleanup();

    const api = createMockApiClient();
    vi.spyOn(api, 'getSagaMap').mockResolvedValue({ revision: 'r', coverage: { chapters: 3, withCards: 0 }, chapters: [], hero: null, nodesTotal: 0, nodes: [], links: [] });
    vi.spyOn(api, 'getIndexingStatus').mockResolvedValue({
      published: { exists: true, context: { state: 'processing', currentBook: { bookId: 'book-01', position: 1, total: 1 }, retryableFailedBook: null } },
      steps: [],
      preparation: {
        remainingSeconds: 240, letterPromised: false, chaptersRead: 0, chaptersTotal: 12, booksRead: 0, booksTotal: 1,
        currentBookId: 'book-01', askable: true, books: [{ bookId: 'book-01', chaptersRead: 0, chaptersTotal: 12 }],
      },
    });
    const { navigate } = renderMap({}, 'draft', api);
    expect(await screen.findByText('Карта появится, когда помощник прочитает книгу')).toBeTruthy();
    expect(await screen.findByText('Прочитано 0 из 12 глав · осталось ≈ 4 мин')).toBeTruthy();
    expect(document.body.textContent).not.toMatch(/карточк|полк/iu);
    fireEvent.click(screen.getByRole('button', { name: 'К книгам' }));
    expect(navigate).toHaveBeenCalledWith('/manuscript/books', { projectId });
  });

  it('says plainly that the map has nobody yet', async () => {
    const api = createMockApiClient();
    vi.spyOn(api, 'getSagaMap').mockResolvedValue({ revision: 'r', coverage: { chapters: 3, withCards: 3 }, chapters: [], hero: null, nodesTotal: 0, nodes: [], links: [] });
    renderMap({}, 'draft', api);
    expect(await screen.findByText('На карте пока никого нет')).toBeTruthy();
    expect(screen.getByText('Героев и мест, которые встречаются хотя бы в двух главах, помощник не нашёл.')).toBeTruthy();
  });

  // R11.2, R11.3: the author edits the map and takes off what is plainly not needed.
  it('takes a link off the map with its reason in sight, and «Отменить» returns it', async () => {
    renderMap({ node: 'мара' });
    const card = await screen.findByRole('complementary', { name: 'Карточка: Мара' });
    const links = await within(card).findByRole('region', { name: 'Связи' });
    expect(within(within(links).getByRole('list', { name: 'Вместе: Сава' })).getByRole('link')).toBeTruthy();
    fireEvent.click(within(links).getByRole('button', { name: 'Убрать связь: Сава' }));
    expect(await screen.findByText('Связь Мара — Сава убрана с карты')).toBeTruthy();
    await waitFor(() => expect(within(links).queryByRole('button', { name: 'Сава' })).toBeNull());
    fireEvent.click(screen.getByRole('button', { name: 'Отменить' }));
    expect(await within(links).findByRole('button', { name: 'Сава' })).toBeTruthy();
    expect(screen.queryByText('Связь Мара — Сава убрана с карты')).toBeNull();
  });

  it('takes a node off the map, finds it in «Убранное с карты» and returns it by one press', async () => {
    const api = createMockApiClient();
    const { navigate } = renderMap({ node: 'гильдия смотрителей' }, 'draft', api);
    const card = await screen.findByRole('complementary');
    fireEvent.click(await within(card).findByRole('button', { name: 'Убрать с карты: Гильдия смотрителей' }));
    expect(await screen.findByText('Фракция «Гильдия смотрителей» убрана с карты')).toBeTruthy();
    await waitFor(() => expect(canvas().dataset.nodes).not.toContain('гильдия'));
    // The route of the test stays: the card the page closed reads as the card of a node off the map.
    expect(await within(card).findByText('Убрано с карты. Поиск и карточка остаются')).toBeTruthy();
    fireEvent.click(await screen.findByRole('button', { name: /Убранное/u }));
    expect(lastNavigation(navigate).query).toMatchObject({ removed: 'open' });
    fireEvent.click(within(card).getByRole('button', { name: 'Вернуть на карту: Гильдия смотрителей' }));
    await waitFor(() => expect(canvas().dataset.nodes).toContain('гильдия'));
    await waitFor(() => expect(screen.queryByRole('button', { name: /Убранное/u })).toBeNull());
  });

  it('lists what is off the map; a reader of the saga sees it without the buttons', async () => {
    const api = createMockApiClient();
    const hidden = await api.hideSagaMapItem(projectId, 'published', { nodeKey: 'мара', otherKey: 'сава' });
    wideScreen();
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const view = render(
      <QueryClientProvider client={queryClient}><ToastsProvider>
        <HiddenPanel api={api} canEdit={false} decisions={{ hideLink: vi.fn(), hideNode: vi.fn(), pending: false, restore: vi.fn() }} onOpen={vi.fn()} projectId={projectId} sourceView="published" />
      </ToastsProvider></QueryClientProvider>,
    );
    expect(await screen.findByText('Мара — Сава')).toBeTruthy();
    expect(screen.getByText('связь убрана')).toBeTruthy();
    expect(screen.queryByRole('button', { name: /Вернуть/u })).toBeNull();
    view.unmount();
    await api.restoreSagaMapItem(projectId, hidden.id);
    expect((await api.listSagaMapHidden(projectId, { sourceView: 'published' })).items).toEqual([]);
  });

  // specs/2026-11-10-ux2-memory-page.md MP1.5: one link «Что помнит помощник» to the part of the materials, no panel of the map.
  it('links to what the helper remembers instead of a panel of the materials', async () => {
    const { navigate } = renderMap();
    const link = await screen.findByRole('link', { name: 'Что помнит помощник' });
    expect(link.getAttribute('href')).toBe(`/memory?projectId=${projectId}&part=materials`);
    expect(screen.queryByRole('button', { name: /Материалы/u })).toBeNull();
    fireEvent.click(link);
    expect(navigate).toHaveBeenCalledWith('/memory', { projectId, part: 'materials' });
  });

  it('keeps «Убранное» and «Что помнит помощник» in «⋯» on a phone', async () => {
    const api = createMockApiClient();
    await api.hideSagaMapItem(projectId, 'draft', { nodeKey: 'мара', otherKey: 'сава' });
    const navigate = vi.fn();
    vi.stubGlobal('matchMedia', (query: string) => ({
      addEventListener: () => undefined, matches: true, media: query, removeEventListener: () => undefined,
    }));
    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><ToastsProvider>
        <SagaMap api={api} canEdit navigate={navigate} projectId={projectId} route={createAppRoute('/saga', { projectId })} sourceView="draft" />
      </ToastsProvider></QueryClientProvider>,
    );
    const more = await screen.findByRole('button', { name: 'Ещё' });
    expect(screen.queryByRole('link', { name: 'Что помнит помощник' })).toBeNull();
    fireEvent.click(more);
    const menu = screen.getByRole('menu', { name: 'Ещё' });
    await waitFor(() => expect(within(menu).getByRole('menuitem', { name: /^Убранное/u })).toBeTruthy());
    fireEvent.click(within(menu).getByRole('menuitem', { name: 'Что помнит помощник' }));
    expect(navigate).toHaveBeenCalledWith('/memory', { projectId, part: 'materials' });
  });
});
