import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { useEffect } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../api/mock';
import { projectId } from '../../api/mock/repository/fixtures';
import type { SagaMap as SagaMapData } from '../../entities/saga/api';
import { createAppRoute } from '../../shared/navigation/app-route';
import { ToastsProvider } from '../../ui/feedback/Toast';
import type { MapCanvasProps } from './MapCanvas';
import { SagaMap } from './SagaMap';

// WebGL is not in jsdom: the canvas is a stand-in that shows what it would draw and moves a camera the test watches.
const camera = { zoomIn: vi.fn(), zoomOut: vi.fn(), showAll: vi.fn() };
const browser = { webgl: true };
vi.mock('./MapCanvas', () => ({
  MapCanvas: ({ graph, onUnavailable, view }: MapCanvasProps) => {
    useEffect(() => {
      if (!browser.webgl) onUnavailable();
      if (view) view.current = camera;
    }, [onUnavailable, view]);
    return <div data-nodes={graph.nodes.length} data-testid="saga-map-canvas" />;
  },
}));

beforeEach(() => {
  browser.webgl = true;
  Object.values(camera).forEach((move) => move.mockClear());
  window.localStorage.clear();
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

function screenOf(phone: boolean) {
  vi.stubGlobal('matchMedia', (query: string) => ({
    addEventListener: () => undefined, matches: phone, media: query, removeEventListener: () => undefined,
  }));
}

function renderMap({ api = createMockApiClient(), phone = false }: { api?: ReturnType<typeof createMockApiClient>; phone?: boolean } = {}) {
  screenOf(phone);
  const navigate = vi.fn();
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = render(
    <QueryClientProvider client={queryClient}><ToastsProvider>
      <SagaMap api={api} canEdit navigate={navigate} projectId={projectId} route={createAppRoute('/saga', { projectId })} sourceView="draft" />
    </ToastsProvider></QueryClientProvider>,
  );
  return { navigate, view };
}

/** A saga of `count` names over two chapters: the most mentioned first. */
function bigSaga(count: number): SagaMapData {
  const chapter = (index: number) => ({
    key: `b1c${index + 1}`, label: `Книга 1, гл. ${index + 1} „Глава ${index + 1}“`, chapterId: `chapter-${index}`, bookId: 'book-1', bookOrder: 1, bookTitle: 'Белый порт', title: `Глава ${index + 1}`,
  });
  return {
    revision: `big-${count}`, coverage: { chapters: 2, withCards: 2 }, chapters: [chapter(0), chapter(1)], hero: null, nodesTotal: count, links: [],
    nodes: Array.from({ length: count }, (_, index) => ({ key: `имя ${index}`, name: `Имя ${index}`, category: 'character' as const, type: 'персонаж', aliases: [], chapters: index % 2 ? [1] : [0, 1] })),
  };
}

const canvas = () => screen.getByTestId('saga-map-canvas');

// specs/2026-11-11-ux8-map.md MP1: how to use the map is on the screen, not found by trying.
describe('«Карта саги»: the view, the legend and the count', () => {
  it('moves the view by «+», «−» and «Показать всё», and by the keys while the focus is on the map', async () => {
    renderMap();
    const controls = await screen.findByRole('group', { name: 'Вид карты' });
    fireEvent.click(within(controls).getByRole('button', { name: 'Приблизить' }));
    fireEvent.click(within(controls).getByRole('button', { name: 'Отдалить' }));
    fireEvent.click(within(controls).getByRole('button', { name: 'Показать всё' }));
    expect(camera.zoomIn).toHaveBeenCalledTimes(1);
    expect(camera.zoomOut).toHaveBeenCalledTimes(1);
    expect(camera.showAll).toHaveBeenCalledTimes(1);

    const map = screen.getByRole('region', { name: 'Карта саги' });
    fireEvent.keyDown(map, { key: '+' });
    fireEvent.keyDown(map, { key: '-' });
    fireEvent.keyDown(map, { key: '0' });
    expect(camera.zoomIn).toHaveBeenCalledTimes(2);
    expect(camera.zoomOut).toHaveBeenCalledTimes(2);
    expect(camera.showAll).toHaveBeenCalledTimes(2);
    // A key typed into the search is a letter of the name, not a move of the map.
    fireEvent.keyDown(screen.getByRole('searchbox', { name: 'Найти на карте' }), { key: '0' });
    expect(camera.showAll).toHaveBeenCalledTimes(2);
  });

  it('says how to read the map in one line; «×» hides it for this browser and «Как читать карту» brings it back', async () => {
    const first = renderMap();
    const legend = await screen.findByText(/^Кружок — герой, место или предмет: чем чаще в книге, тем больше\. Линия — связь: чем толще, тем больше общего\. Нажмите на кружок — кто это и с кем связан$/u);
    expect(legend).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Скрыть подсказку' }));
    expect(screen.queryByText(/^Кружок — герой/u)).toBeNull();
    first.view.unmount();

    renderMap();
    await screen.findByTestId('saga-map-canvas');
    expect(screen.queryByText(/^Кружок — герой/u)).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Ещё' }));
    fireEvent.click(within(screen.getByRole('menu', { name: 'Ещё' })).getByRole('menuitem', { name: 'Как читать карту' }));
    expect(screen.getByText(/^Кружок — герой/u)).toBeTruthy();
  });

  it('names which of the names it shows, adds the next ones and shows all of a big saga after a question', async () => {
    const api = createMockApiClient();
    vi.spyOn(api, 'getSagaMap').mockResolvedValue(bigSaga(420));
    renderMap({ api });
    expect(await screen.findByText('Показаны 60 самых частых из 420')).toBeTruthy();
    expect(screen.getByText('Редкое имя — через поиск')).toBeTruthy();
    expect(canvas().dataset.nodes).toBe('60');
    fireEvent.click(screen.getByRole('button', { name: 'Ещё 60' }));
    expect(canvas().dataset.nodes).toBe('120');
    expect(screen.getByText('Показаны 120 самых частых из 420')).toBeTruthy();

    fireEvent.click(screen.getByRole('button', { name: 'Все' }));
    const question = screen.getByRole('dialog', { name: 'Показать все 420?' });
    expect(within(question).getByText('Карта станет медленнее.')).toBeTruthy();
    expect(canvas().dataset.nodes).toBe('120');
    fireEvent.click(within(question).getByRole('button', { name: 'Показать все' }));
    await waitFor(() => expect(canvas().dataset.nodes).toBe('420'));
    expect(screen.getByText('Показаны все: 420')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Все' })).toBeNull();
  }, 20_000);

  it('shows all of a saga of up to 300 names at once', async () => {
    const api = createMockApiClient();
    vi.spyOn(api, 'getSagaMap').mockResolvedValue(bigSaga(200));
    renderMap({ api });
    fireEvent.click(await screen.findByRole('button', { name: 'Все' }));
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(canvas().dataset.nodes).toBe('200');
  });

  // MP2.4, К-04: the state of the map is one line above the graph, never two about the same thing.
  it('says in one line above the graph that the map is still read or rebuilt', async () => {
    const api = createMockApiClient();
    const published = await api.getSagaMap(projectId, { sourceView: 'published' });
    vi.spyOn(api, 'getSagaMap').mockResolvedValueOnce(published).mockResolvedValue({ ...published, building: true });
    const first = renderMap({ api });
    expect(await screen.findByText('Помощник прочитал 1 из 2 глав — карта дополняется сама')).toBeTruthy();
    expect(screen.queryByRole('region', { name: 'Карта обновляется' })).toBeNull();
    first.view.unmount();
    renderMap({ api });
    const work = await screen.findByRole('region', { name: 'Карта обновляется' });
    expect(within(work).getByText('Помощник прочитал 1 из 2 глав · можно уйти — новое появится на карте само')).toBeTruthy();
    expect(screen.queryByText(/карта дополняется сама/u)).toBeNull();
    // The line stands above the graph, not over it.
    expect(work.compareDocumentPosition(screen.getByRole('region', { name: 'Карта саги' })) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(screen.getByRole('region', { name: 'Карта саги' }).contains(work)).toBe(false);
  });

  it('says that a browser without graphics cannot draw the map, where to turn it on, and who is in the saga', async () => {
    browser.webgl = false;
    renderMap();
    expect(await screen.findByText('Этот браузер не умеет рисовать карту. Имена, поиск и карточки работают — вот кто в саге:')).toBeTruthy();
    expect(screen.getByText(/включите ускорение графики: Chrome и Edge — Настройки → Система/u)).toBeTruthy();
    expect(within(screen.getByRole('list', { name: 'Узлы на карте' })).getAllByRole('button')).toHaveLength(6);
  });

  it('keeps the bar of a phone in one row: the search, «Фильтры» in a sheet and «⋯»', async () => {
    const { navigate } = renderMap({ phone: true });
    const bar = await screen.findByRole('group', { name: 'Панель карты' });
    expect(within(bar).getByRole('searchbox', { name: 'Найти на карте' })).toBeTruthy();
    expect(within(bar).queryByRole('group', { name: 'Что показывать' })).toBeNull();
    fireEvent.click(within(bar).getByRole('button', { name: 'Фильтры' }));
    const sheet = screen.getByRole('dialog', { name: 'Что показывать' });
    fireEvent.click(within(sheet).getByRole('button', { name: /Места/u }));
    expect(navigate).toHaveBeenLastCalledWith('/saga', expect.objectContaining({ hide: 'place' }), { replace: true });

    fireEvent.click(within(bar).getByRole('button', { name: 'Ещё' }));
    const menu = screen.getByRole('menu', { name: 'Ещё' });
    expect(within(menu).getAllByRole('menuitem').map((item) => item.textContent)).toEqual(['Как читать карту', 'Что помнит помощник']);
  });
});
