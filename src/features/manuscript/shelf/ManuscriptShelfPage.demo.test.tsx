import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../../api/mock';
import { projectId } from '../../../api/mock/repository/fixtures';
import { parseRoute } from '../../../shared/navigation/app-route';
import { AccountSessionContext, type AccountSession } from '../../../shared/session/account-session';
import { FakeWebSocket } from '../../../test/FakeWebSocket';
import { InShell } from '../../../app/shell/AppShell.test-helpers';
import { ManuscriptShelfPage } from './ManuscriptShelfPage';
import { ShelfSlotProvider, type ShelfDemoOfferOptions, type ShelfSlot } from './shelf-slot';
import { ToastsProvider } from '../../../ui/feedback/Toast';

type ShelfApi = ReturnType<typeof createMockApiClient>;

beforeEach(() => vi.stubGlobal('WebSocket', FakeWebSocket));
afterEach(() => {
  FakeWebSocket.reset();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

const refusal = 'Это сага-пример. Создайте свою сагу — книги загружаются в неё';

/** The offer of a build: its button, and whether the example is being opened. */
function offerSlot(preparing = false, seen?: (options: ShelfDemoOfferOptions) => void): ShelfSlot {
  return {
    useDemoOffer: (options) => {
      seen?.(options);
      return { element: <button onClick={() => options.onOpenSaga('project-demo')} type="button">Попробовать на примере</button>, preparing };
    },
    demoCredit: 'Текст: источник примера',
  };
}

function renderShelf(
  search: string, slot: ShelfSlot | null, configureApi?: (api: ShelfApi) => void, api = createMockApiClient(), session: AccountSession = { busy: false },
  shell = false,
) {
  vi.spyOn(api, 'getAgentTimeline').mockResolvedValue({ events: [], eof: true, nextCursor: null } as never);
  configureApi?.(api);
  const navigate = vi.fn();
  const queryClient = new QueryClient({ defaultOptions: { mutations: { retry: false }, queries: { retry: false } } });
  const route = parseRoute(`http://localhost/manuscript/books${search}`);
  const page = (value: AccountSession) => (
    <QueryClientProvider client={queryClient}><ToastsProvider>
      <AccountSessionContext.Provider value={value}>
        <ShelfSlotProvider value={slot}>
          {shell
            ? <InShell api={api} navigate={navigate} route={route}><ManuscriptShelfPage api={api} navigate={navigate} route={route} /></InShell>
            : <ManuscriptShelfPage api={api} navigate={navigate} route={route} />}
        </ShelfSlotProvider>
      </AccountSessionContext.Provider>
    </ToastsProvider></QueryClientProvider>
  );
  const view = render(page(session));
  return { api, navigate, rerender: (value: AccountSession) => view.rerender(page(value)) };
}

const noBooks = (api: ShelfApi) => {
  vi.spyOn(api, 'listBooks').mockResolvedValue({ data: [], meta: { hasMore: false, nextCursor: null } } as never);
};

// specs/2026-11-09-demo-saga.md DS1.1, DS4.
describe('ManuscriptShelfPage and the demo saga', () => {
  it('offers the example of the build to an author without a saga, and its press leads to the shelf of the example', async () => {
    const { navigate } = renderShelf('', offerSlot(), (api) => api.setScenario({ preset: 'normal', actorRole: 'non_member' }));
    // The offer is a way in of the first screen (specs/2026-11-10-ux3-first-screen.md FS1.2).
    const first = await screen.findByRole('region', { name: 'С чего начнём?' });
    fireEvent.click(within(first).getByRole('button', { name: 'Попробовать на примере' }));
    // specs/2026-11-11-import-speed.md IS2.2: the shelf of the example opens at once.
    expect(navigate).toHaveBeenCalledWith('/manuscript/books', { projectId: 'project-demo' });
  });

  // specs/2026-11-11-import-speed.md IS2.2, IS2.3: opened at once, the shelf of the example is not empty while the server
  // puts its book on it — the line of the book on its way, by the Jobs of the saga, as for any single import.
  it('tells the book of the example on its way to its shelf, and not a saga without books', async () => {
    const api = createMockApiClient();
    const saga = await api.openDemoSaga();
    renderShelf(`?projectId=${saga.id}`, offerSlot(), (shelfApi) => {
      noBooks(shelfApi);
      vi.spyOn(shelfApi, 'listProjectJobs').mockResolvedValue({ data: [{
        id: 'job-seed', kind: 'import', status: 'running', progress: null, phase: null,
        subject: { type: 'import', id: 'job-seed', projectId: saga.id }, result: null, error: null, canCancel: true,
        expiresAt: null, createdAt: '2026-11-11T13:01:00.000Z', updatedAt: '2026-11-11T13:01:00.000Z',
        links: { self: '/api/v1/jobs/job-seed', cancel: null, timeline: null },
      }], meta: { hasMore: false, nextCursor: null } } as never);
      vi.spyOn(shelfApi, 'getIndexingStatus').mockResolvedValue({ published: { exists: false, context: null }, steps: [] } as never);
    }, api, undefined, true);

    const line = await screen.findByRole('region', { name: 'Чтение саги' });
    expect(line.textContent).toMatch(/^Загружаю книгу в сагу…/u);
    expect(within(line).getByText('Сейчас она появится здесь, и помощник начнёт её читать')).toBeTruthy();
    expect(screen.queryByText('В саге пока нет книг')).toBeNull();
    expect(screen.queryByRole('button', { name: 'Попробовать на примере' })).toBeNull();
    expect(screen.getByText(refusal)).toBeTruthy();
  });

  it('offers the example in a saga without books, under the upload', async () => {
    renderShelf(`?projectId=${projectId}`, offerSlot(), noBooks);
    const offer = await screen.findByRole('button', { name: 'Попробовать на примере' });
    const upload = screen.getByText('или перетащите файлы на страницу');
    expect(upload.compareDocumentPosition(offer) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it('shows no offer on a shelf with books', async () => {
    renderShelf(`?projectId=${projectId}`, offerSlot());
    await screen.findByRole('article', { name: 'Книга III. Северный суд' });
    expect(screen.queryByRole('button', { name: 'Попробовать на примере' })).toBeNull();
  });

  it('keeps the offer on any shelf while the example is being opened', async () => {
    renderShelf(`?projectId=${projectId}`, offerSlot(true));
    await screen.findByRole('article', { name: 'Книга III. Северный суд' });
    expect(screen.getByRole('button', { name: 'Попробовать на примере' })).toBeTruthy();
  });

  it('tells the offer whether the address of the author is verified', async () => {
    const seen: ShelfDemoOfferOptions[] = [];
    const session: AccountSession = { busy: false, emailVerification: { email: 'new-author@example.test', status: 'idle', canSend: true, onSend: vi.fn() } };
    const view = renderShelf(`?projectId=${projectId}`, offerSlot(false, (options) => seen.push(options)), noBooks, undefined, session);
    await screen.findByRole('button', { name: 'Попробовать на примере' });
    expect(seen.at(-1)?.emailUnverified).toBe(true);
    view.rerender({ busy: false });
    expect(seen.at(-1)?.emailUnverified).toBe(false);
  });

  it('shows no offer in a build without the demo saga', async () => {
    renderShelf(`?projectId=${projectId}`, null, noBooks);
    await screen.findByText('В саге пока нет книг');
    expect(screen.queryByRole('button', { name: 'Попробовать на примере' })).toBeNull();
  });

  it('shows the shelf of the demo saga with its mark and without any upload', async () => {
    const api = createMockApiClient();
    const saga = await api.openDemoSaga();
    expect(saga.demo).toBe(true);
    const chats = vi.spyOn(api, 'listChatSessions');
    renderShelf(`?projectId=${saga.id}`, offerSlot(), undefined, api, undefined, true);

    expect(await screen.findByRole('article', { name: 'Игрок' })).toBeTruthy();
    // «Три шага» lead to a saga of the author's own: the example, a saga without a chat yet, does not show them.
    await waitFor(() => expect(chats).toHaveBeenCalled());
    await act(async () => { await chats.mock.results.at(-1)!.value; });
    expect(screen.queryByRole('region', { name: 'До первого ответа' })).toBeNull();
    const content = screen.getByRole('region', { name: 'Книги саги' });
    expect(within(content).getByRole('heading', { level: 1, name: 'Книги' })).toBeTruthy();
    // The mark «Пример» stands by the name of the saga in the top bar; the shelf is «Книги» (specs/2026-11-10-ux2-patterns-states.md PS1.1).
    expect(within(screen.getByRole('banner')).getAllByText('Пример').length).toBeGreaterThan(0);
    // No way to bring a book of the author into the example: no button, no file field, no drop zone, no offer.
    expect(screen.queryByRole('button', { name: 'Добавить книги' })).toBeNull();
    expect(screen.queryByLabelText('Файлы книг: FB2, EPUB, Word (.docx), RTF, ODT или текст (.txt, .md)')).toBeNull();
    expect(screen.queryByText('или перетащите файлы на страницу')).toBeNull();
    expect(screen.queryByRole('button', { name: 'Попробовать на примере' })).toBeNull();
    expect(within(content).getByText(refusal)).toBeTruthy();
    expect(within(content).getByText('Текст: источник примера')).toBeTruthy();

    // The book of the example is read and deleted as any book, and is not rewritten from a file.
    fireEvent.click(screen.getByRole('button', { name: 'Действия книги Игрок' }));
    const menu = await screen.findByRole('menu', { name: 'Действия книги' });
    expect(within(menu).getByRole('menuitem', { name: 'Читать с начала' })).toBeTruthy();
    expect(within(menu).getByRole('menuitem', { name: 'Удалить книгу…' })).toBeTruthy();
    expect(within(menu).queryByRole('menuitem', { name: /Новая версия из файла/u })).toBeNull();
    fireEvent.keyDown(menu, { key: 'Escape' });

    // «Создать свою сагу» opens the one window of a new saga, the one of the menu of the saga (SC3.2).
    fireEvent.click(within(content).getByRole('button', { name: 'Создать свою сагу' }));
    const dialog = await screen.findByRole('dialog', { name: 'Новая сага' });
    expect(within(dialog).getByRole('textbox', { name: 'Название саги' })).toBeTruthy();
  });

  it('marks the demo saga among the sagas of the author', async () => {
    const api = createMockApiClient();
    const saga = await api.openDemoSaga();
    renderShelf(`?projectId=${projectId}`, null, undefined, api, undefined, true);
    await screen.findByRole('article', { name: 'Книга III. Северный суд' });
    // His own saga carries no mark.
    expect(within(screen.getByRole('region', { name: 'Книги саги' })).queryByText('Пример')).toBeNull();
    expect(within(screen.getByRole('banner')).queryByText('Пример')).toBeNull();

    fireEvent.click(screen.getByRole('button', { name: 'Сага: Хроники Белого порта' }));
    const sagas = await screen.findByRole('menu', { name: 'Выбор саги' });
    const option = within(sagas).getByRole('menuitem', { name: `${saga.title} Пример` });
    expect(within(option).getByText('Пример')).toBeTruthy();
  });
});
