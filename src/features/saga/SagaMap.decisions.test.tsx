import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../api/mock';
import { projectId } from '../../api/mock/repository/fixtures';
import { createAppRoute } from '../../shared/navigation/app-route';
import { ToastsProvider } from '../../ui/feedback/Toast';
import type { MapCanvasProps } from './MapCanvas';
import { SagaMap } from './SagaMap';

// WebGL is not in jsdom: the canvas is a stand-in that shows what it would draw and which nodes are pale.
vi.mock('./MapCanvas', () => ({
  MapCanvas: ({ graph }: MapCanvasProps) => (
    <div data-dim={graph.nodes.filter((node) => node.dim).map((node) => node.key).join(',')} data-nodes={graph.nodes.map((node) => node.key).join(',')} data-testid="saga-map-canvas" />
  ),
}));

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

type Api = ReturnType<typeof createMockApiClient>;

function renderMap(query: Record<string, string> = {}, api: Api = createMockApiClient()) {
  vi.stubGlobal('matchMedia', (media: string) => ({ addEventListener: () => undefined, matches: false, media, removeEventListener: () => undefined }));
  const navigate = vi.fn();
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}><ToastsProvider>
      <SagaMap api={api} canEdit navigate={navigate} projectId={projectId} route={createAppRoute('/saga', { projectId, ...query })} sourceView="draft" />
    </ToastsProvider></QueryClientProvider>,
  );
  return { api, navigate };
}

const notices = () => screen.getByRole('region', { name: 'Уведомления' });
const canvas = () => screen.getByTestId('saga-map-canvas');
const onlyOnMap = 'Только на карте. Книга и ответы помощника не меняются';

// specs/2026-11-11-ux8-map.md MP3: the decisions of the author answer with a line of the one area, the card says what to do.
describe('«Карта саги»: the decisions of the author, the search and the card', () => {
  it('takes a link off the map by «Убрать связь»: a line with «Отменить» in the one area, no strip and no second undo', async () => {
    renderMap({ node: 'мара' });
    const card = await screen.findByRole('complementary', { name: 'Карточка: Мара' });
    const links = await within(card).findByRole('region', { name: 'Связи' });
    const remove = within(links).getByRole('button', { name: 'Убрать связь: Сава' });
    expect(remove.getAttribute('title')).toBe(onlyOnMap);
    fireEvent.click(remove);
    const line = await within(notices()).findByRole('status');
    expect(line.textContent).toContain('Связь Мара — Сава убрана с карты');
    expect(line.textContent).toContain(onlyOnMap);
    await waitFor(() => expect(within(links).queryByRole('button', { name: 'Сава' })).toBeNull());
    expect(screen.queryByRole('alert')).toBeNull();
    fireEvent.click(within(line).getByRole('button', { name: 'Отменить' }));
    expect(await within(links).findByRole('button', { name: 'Сава' })).toBeTruthy();
  });

  it('takes a node off the map by «Убрать с карты»: the card closes, the line names its kind and gives «Отменить»', async () => {
    const { navigate } = renderMap({ node: 'гильдия смотрителей' });
    const card = await screen.findByRole('complementary');
    const remove = await within(card).findByRole('button', { name: 'Убрать с карты: Гильдия смотрителей' });
    expect(remove.getAttribute('title')).toBe(onlyOnMap);
    fireEvent.click(remove);
    const line = await within(notices()).findByRole('status');
    expect(line.textContent).toContain('Фракция «Гильдия смотрителей» убрана с карты');
    expect(line.textContent).toContain(onlyOnMap);
    await waitFor(() => expect(navigate).toHaveBeenLastCalledWith('/saga', expect.objectContaining({ node: undefined }), undefined));
    await waitFor(() => expect(canvas().dataset.nodes).not.toContain('гильдия'));
    fireEvent.click(within(line).getByRole('button', { name: 'Отменить' }));
    await waitFor(() => expect(canvas().dataset.nodes).toContain('гильдия'));
  });

  it('says in one line of the card of a node off the map that it is off and gives «Вернуть на карту»', async () => {
    const api = createMockApiClient();
    await api.hideSagaMapItem(projectId, 'draft', { nodeKey: 'сава' });
    renderMap({ node: 'сава' }, api);
    const card = await screen.findByRole('complementary', { name: 'Карточка: сава' });
    expect(await within(card).findByText('Убрано с карты. Поиск и карточка остаются')).toBeTruthy();
    expect(within(card).queryByRole('button', { name: /Убрать с карты/u })).toBeNull();
    expect(within(card).queryByText(/Вы убрали/u)).toBeNull();
    fireEvent.click(within(card).getByRole('button', { name: 'Вернуть на карту: Сава' }));
    await waitFor(() => expect(canvas().dataset.nodes).toContain('сава'));
  });

  it('says by which name a node was found and where it first appears; nothing found leads to the chat', async () => {
    const { navigate } = renderMap();
    const field = await screen.findByRole('searchbox', { name: 'Найти на карте' });
    fireEvent.focus(field);
    fireEvent.change(field, { target: { value: 'северянка' } });
    const mara = within(screen.getByRole('list', { name: 'Найдено' })).getByRole('button', { name: /Мара/u });
    expect(mara.textContent).toContain('также: Северянка · впервые: Книга 1, гл. 1 „Туманный берег“');
    fireEvent.change(field, { target: { value: 'сава' } });
    const sava = within(screen.getByRole('list', { name: 'Найдено' })).getByRole('button', { name: /Сава/u });
    expect(sava.textContent).toContain('впервые: Книга 1, гл. 1 „Туманный берег“');
    expect(sava.textContent).not.toContain('также');

    fireEvent.change(field, { target: { value: 'дракон' } });
    const found = screen.getByRole('list', { name: 'Найдено' });
    expect(await within(found).findByText('На карте такого нет.')).toBeTruthy();
    fireEvent.click(within(found).getByRole('button', { name: 'Спросить в чате про „дракон“' }));
    expect(navigate).toHaveBeenLastCalledWith('/chat', { projectId }, { chatQuestion: 'Что в саге известно про «дракон»?', from: { section: 'saga' } });
  });

  it('says that a name is no more in the saga and puts it into the search by «Найти похожее»', async () => {
    const { navigate } = renderMap({ node: 'пропавший' });
    const card = await screen.findByRole('complementary', { name: 'Карточка: пропавший' });
    expect(await within(card).findByText('Этого имени в саге больше нет')).toBeTruthy();
    expect(within(card).getByText('Возможно, книгу обновили.')).toBeTruthy();
    fireEvent.click(within(card).getByRole('button', { name: 'Найти похожее' }));
    const field = screen.getByRole('searchbox', { name: 'Найти на карте' }) as HTMLInputElement;
    expect(field.value).toBe('пропавший');
    expect(document.activeElement).toBe(field);
    expect(navigate).toHaveBeenLastCalledWith('/saga', expect.objectContaining({ node: undefined }), undefined);
  });

  it('asks the chat about a node by its kind, never «кто или что это»', async () => {
    const { navigate } = renderMap({ node: 'мара' });
    const card = await screen.findByRole('complementary', { name: 'Карточка: Мара' });
    fireEvent.click(await within(card).findByRole('button', { name: 'Спросить в чате' }));
    expect(navigate).toHaveBeenLastCalledWith('/chat', { projectId }, {
      chatQuestion: 'Расскажи про героя: Мара — кто это, главные события и с кем связан по книге.', from: { section: 'saga', name: 'Мара' },
    });
    cleanup();
    const place = renderMap({ node: 'белый порт' });
    fireEvent.click(await within(await screen.findByRole('complementary')).findByRole('button', { name: 'Спросить в чате' }));
    expect(place.navigate).toHaveBeenLastCalledWith('/chat', { projectId }, {
      chatQuestion: 'Расскажи про место: Белый порт — что там происходит и кто там бывает по книге.', from: { section: 'saga', name: 'Белый порт' },
    });
  });

  it('shows only what the book does not know yet by the address of «Показать новые на карте»; «Вся карта» lifts it', async () => {
    const api = createMockApiClient();
    await api.createAuthorMaterial(projectId, { name: 'Чёрный маяк', kind: 'place', description: 'Маяк над Белым портом.', properties: [] }, { idempotencyKey: 'idem-black-lighthouse' });
    const { navigate } = renderMap({ only: 'new', hide: 'material' }, api);
    await waitFor(() => expect(canvas().dataset.nodes).toContain('черный маяк'));
    expect(canvas().dataset.dim).toContain('мара');
    expect(canvas().dataset.dim).not.toContain('черный маяк');
    expect(screen.getByText('Ваши записи, которых нет в книге: 1')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Вся карта' }));
    expect(navigate).toHaveBeenLastCalledWith('/saga', expect.objectContaining({ only: undefined }), undefined);
  });
});
