import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../../api/mock';
import { projectId } from '../../../api/mock/repository/fixtures';
import { chatQueryKeys, type ChatSession } from '../../../entities/chat/api';
import { textSourceView } from '../../../entities/manuscript/api';
import { parseRoute } from '../../../shared/navigation/app-route';
import { AccountSessionContext, type AccountSession } from '../../../shared/session/account-session';
import { FakeWebSocket } from '../../../test/FakeWebSocket';
import { ManuscriptShelfPage } from './ManuscriptShelfPage';
import { ToastsProvider } from '../../../ui/feedback/Toast';

type ShelfApi = ReturnType<typeof createMockApiClient>;

beforeEach(() => vi.stubGlobal('WebSocket', FakeWebSocket));
afterEach(() => {
  FakeWebSocket.reset();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

const unverified: AccountSession = {
  busy: false,
  emailVerification: { email: 'new-author@example.test', status: 'idle', canSend: true, onSend: vi.fn() },
};

function chats(api: ShelfApi, sessions: () => ChatSession[]) {
  vi.spyOn(api, 'listChatSessions').mockImplementation(async () => ({
    data: sessions(),
    meta: { hasMore: false, nextCursor: null },
  }) as never);
}

function renderShelf(search: string, configure: (api: ShelfApi) => void = () => undefined, session?: AccountSession) {
  const api = createMockApiClient();
  api.setScenario({ actorRole: 'editor', preset: 'normal' });
  vi.spyOn(api, 'getAgentTimeline').mockResolvedValue({ events: [], eof: true, nextCursor: null } as never);
  configure(api);
  const navigate = vi.fn();
  const queryClient = new QueryClient({ defaultOptions: { mutations: { retry: false }, queries: { retry: false } } });
  const page = (nextSearch: string) => (
    <QueryClientProvider client={queryClient}><ToastsProvider>
      <AccountSessionContext.Provider value={session ?? { busy: false }}>
        <ManuscriptShelfPage
          api={api}
          navigate={navigate}
          route={parseRoute(`http://localhost/manuscript/books${nextSearch}`)}
        />
      </AccountSessionContext.Provider>
    </ToastsProvider></QueryClientProvider>
  );
  const view = render(page(search));
  return { api, navigate, queryClient, rerender: (nextSearch: string) => view.rerender(page(nextSearch)) };
}

function steps() {
  return within(screen.getByRole('region', { name: 'До первого ответа' }));
}

describe('FirstSteps: three steps to the first answer (UC2.1)', () => {
  // RED on TEST 28.10: a new account saw «Проект загружается» twice and «Примите приглашение…». 11.11: the first screen
  // names the saga, the shelf of the saga holds the steps (specs/2026-11-10-ux3-first-screen.md FS1.2, FS1.5).
  it('walks a new writer from naming the saga to the first question', async () => {
    const view = renderShelf('', (api) => {
      api.setScenario({ actorRole: 'non_member', preset: 'normal' });
      chats(api, () => []);
    });

    const first = await screen.findByRole('region', { name: 'С чего начнём?' });
    const naming = within(first).getByRole('region', { name: 'Назовите сагу' });
    expect(screen.queryByText('Проект загружается')).toBeNull();
    expect(screen.queryByText(/Примите приглашение/)).toBeNull();
    expect(screen.queryByText(/Три шага/u)).toBeNull();
    expect(within(naming).getByPlaceholderText('Например, Сага северного ветра')).toBeTruthy();

    fireEvent.change(within(naming).getByRole('textbox', { name: 'Название саги' }), { target: { value: 'Сага северного ветра' } });
    fireEvent.click(within(naming).getByRole('button', { name: 'Создать сагу' }));
    await waitFor(() => expect(view.navigate).toHaveBeenCalledWith('/manuscript/books', { projectId: 'project-2' }));

    view.rerender('?projectId=project-2');
    await waitFor(() => expect(steps().getByRole('listitem', { current: 'step' }).textContent).toContain('Загрузите книги'));
    expect(steps().getByText('Сага «Сага северного ветра» создана')).toBeTruthy();
    // «Добро пожаловать» is said once, on the first screen.
    expect(screen.queryByText(/Добро пожаловать/u)).toBeNull();
    // The step calls for the books with the entry of the shelf; the whole page takes a file (specs/2026-11-11-ux7-upload.md UP1.1).
    expect(steps().getByRole('button', { name: 'Добавить книги' })).toBeTruthy();
    expect(steps().getByText('или перетащите файлы на страницу')).toBeTruthy();
    expect(screen.getAllByText('или перетащите файлы на страницу')).toHaveLength(1);
  });

  // specs/2026-11-11-ux9-shelf-chat.md SC1.8 (J-09): the step calls for as many books as the tier puts in a saga.
  it.each([
    [1, 'Загрузите книгу', 'В саге на вашем тарифе — одна книга: начните с первой.'],
    [12, 'Загрузите книги', 'Можно сразу все книги саги — до 12. Порядок покажу перед загрузкой.'],
  ])('calls for the books a saga of the tier takes (%i)', async (limit, title, line) => {
    const view = renderShelf('', (api) => {
      api.setScenario({ actorRole: 'non_member', preset: 'normal' });
      chats(api, () => []);
      const read = api.getAccountQuota.bind(api);
      vi.spyOn(api, 'getAccountQuota').mockImplementation(async () => {
        const quota = await read();
        return { ...quota, ownedProjects: quota.ownedProjects.map((owned) => ({ ...owned, sources: { ...owned.sources, books: { ...owned.sources.books, limit } } })) };
      });
    });
    const first = await screen.findByRole('region', { name: 'С чего начнём?' });
    const naming = within(first).getByRole('region', { name: 'Назовите сагу' });
    fireEvent.change(within(naming).getByRole('textbox', { name: 'Название саги' }), { target: { value: 'Сага северного ветра' } });
    fireEvent.click(within(naming).getByRole('button', { name: 'Создать сагу' }));
    await waitFor(() => expect(view.navigate).toHaveBeenCalledWith('/manuscript/books', { projectId: 'project-2' }));
    view.rerender('?projectId=project-2');
    await waitFor(() => expect(steps().getByRole('listitem', { current: 'step' }).textContent).toContain(line));
    expect(within(steps().getByRole('listitem', { current: 'step' })).getByRole('heading', { level: 3 }).textContent).toBe(title);
  });

  // specs/2026-11-10-ux3-saga-state.md SS2.5 (W-74): while the steps go, the line of the reading stands in step 3 and its
  // button is the one button of the first question; there is no second block of the state over the books.
  it('asks the first question of a saga being read from the line in step 3, the one button of it', async () => {
    const view = renderShelf(`?projectId=${projectId}`, (api) => chats(api, () => []));

    await waitFor(() => expect(steps().getByRole('listitem', { current: 'step' }).textContent).toContain('Задайте первый вопрос'));
    expect(steps().getByText(/^\d+ (?:книга загружена|книги загружены|книг загружено)$/u)).toBeTruthy();
    const step = within(steps().getByRole('listitem', { current: 'step' }));
    const line = await step.findByRole('region', { name: 'Чтение саги' });
    expect(line.textContent).toMatch(/^Помощник читает книг/u);
    expect(screen.getAllByRole('region', { name: 'Чтение саги' })).toHaveLength(1);
    const asks = screen.getAllByRole('button', { name: /^Задать (первый )?вопрос$/u });
    expect(asks).toHaveLength(1);
    fireEvent.click(asks[0]!);

    expect(view.navigate).toHaveBeenCalledWith('/chat', { projectId });
  });

  it('hides the steps once the saga has a chat', async () => {
    let sessions: ChatSession[] = [];
    const view = renderShelf(`?projectId=${projectId}`, (api) => chats(api, () => sessions));
    await screen.findByRole('region', { name: 'До первого ответа' });

    sessions = [{ id: 'chat-1' } as ChatSession];
    await act(async () => {
      await view.queryClient.invalidateQueries({ queryKey: chatQueryKeys.sessions({ projectId, sourceView: textSourceView }), exact: true });
    });

    await waitFor(() => expect(screen.queryByRole('region', { name: 'До первого ответа' })).toBeNull());
  });

  // R2.1.2: an unverified address cannot load books, so step 2 asks to verify it instead.
  it('asks an unverified writer to verify the address instead of the drop zone', async () => {
    const api = createMockApiClient();
    const created = await api.createProject({ title: 'Сага без книг' });
    renderShelf(`?projectId=${created.id}`, (shelfApi) => {
      vi.spyOn(shelfApi, 'listProjects').mockImplementation(() => api.listProjects());
      vi.spyOn(shelfApi, 'getProject').mockImplementation((id) => api.getProject(id));
      vi.spyOn(shelfApi, 'listBooks').mockImplementation((id) => api.listBooks(id));
      chats(shelfApi, () => []);
    }, unverified);

    await waitFor(() => expect(steps().getByRole('listitem', { current: 'step' }).textContent).toContain('Загрузите книги'));
    expect(steps().getByText('Подтвердите почту new-author@example.test — и можно будет загружать книги, спрашивать, рисовать и писать.')).toBeTruthy();
    expect(screen.queryByText('или перетащите файлы на страницу')).toBeNull();
  });

  // R2.1.4, specs/2026-11-11-ux7-upload.md UP2.1: one new file goes at once, without a window or a question.
  it('does not ask to check the files once the import has started', async () => {
    renderShelf(`?projectId=${projectId}`);
    const input = await screen.findByLabelText('Файлы книг: FB2, EPUB, Word (.docx), RTF, ODT или текст (.txt, .md)');
    await waitFor(() => expect(input).toHaveProperty('disabled', false));

    fireEvent.change(input, { target: { files: [new File(['book'], 'Северный ветер.epub', { type: 'application/epub+zip' })] } });

    expect(await screen.findByRole('article', { name: 'Северный ветер.epub' })).toBeTruthy();
    expect(screen.queryByRole('dialog')).toBeNull();
  });
});
