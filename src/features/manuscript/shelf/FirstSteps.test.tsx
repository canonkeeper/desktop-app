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
    <QueryClientProvider client={queryClient}>
      <AccountSessionContext.Provider value={session ?? { busy: false }}>
        <ManuscriptShelfPage
          api={api}
          navigate={navigate}
          onLogout={vi.fn()}
          route={parseRoute(`http://localhost/manuscript/books${nextSearch}`)}
          userName="Новый автор"
        />
      </AccountSessionContext.Provider>
    </QueryClientProvider>
  );
  const view = render(page(search));
  return { api, navigate, queryClient, rerender: (nextSearch: string) => view.rerender(page(nextSearch)) };
}

function steps() {
  return within(screen.getByRole('region', { name: 'Добро пожаловать в Canon Keeper' }));
}

describe('FirstSteps: three steps to the first answer (UC2.1)', () => {
  // RED on TEST 28.10: a new account saw «Проект загружается» twice and «Примите приглашение…».
  it('walks a new writer from naming the saga to the first question', async () => {
    const view = renderShelf('', (api) => {
      api.setScenario({ actorRole: 'non_member', preset: 'normal' });
      chats(api, () => []);
    });

    const panel = await screen.findByRole('region', { name: 'Добро пожаловать в Canon Keeper' });
    expect(within(panel).getByText('Три шага до первого ответа по вашей саге')).toBeTruthy();
    expect(within(panel).getByRole('listitem', { current: 'step' }).textContent).toContain('Назовите сагу');
    expect(screen.queryByText('Проект загружается')).toBeNull();
    expect(screen.queryByText(/Примите приглашение/)).toBeNull();
    expect(within(panel).getByPlaceholderText('Например, Сага северного ветра')).toBeTruthy();

    fireEvent.change(within(panel).getByRole('textbox', { name: 'Название саги' }), { target: { value: 'Сага северного ветра' } });
    fireEvent.click(within(panel).getByRole('button', { name: 'Создать сагу' }));
    await waitFor(() => expect(view.navigate).toHaveBeenCalledWith('/manuscript/books', { projectId: 'project-2' }));

    view.rerender('?projectId=project-2');
    await waitFor(() => expect(steps().getByRole('listitem', { current: 'step' }).textContent).toContain('Загрузите книги'));
    expect(steps().getByText('Сага «Сага северного ветра» создана')).toBeTruthy();
    expect(steps().getByText('Перетащите FB2, EPUB или DOCX')).toBeTruthy();
    // The first steps hold the only drop zone of an empty saga.
    expect(screen.getAllByText('Перетащите FB2, EPUB или DOCX')).toHaveLength(1);
  });

  it('asks the first question of a saga with prepared text in a new chat', async () => {
    const view = renderShelf(`?projectId=${projectId}`, (api) => chats(api, () => []));

    await waitFor(() => expect(steps().getByRole('listitem', { current: 'step' }).textContent).toContain('Задайте первый вопрос'));
    expect(steps().getByText(/^Загружено книг: \d+$/)).toBeTruthy();
    const ask = steps().getByRole('button', { name: 'Задать первый вопрос' });
    await waitFor(() => expect(ask).toHaveProperty('disabled', false));
    fireEvent.click(ask);

    expect(view.navigate).toHaveBeenCalledWith('/chat', { projectId });
  });

  it('hides the steps once the saga has a chat', async () => {
    let sessions: ChatSession[] = [];
    const view = renderShelf(`?projectId=${projectId}`, (api) => chats(api, () => sessions));
    await screen.findByRole('region', { name: 'Добро пожаловать в Canon Keeper' });

    sessions = [{ id: 'chat-1' } as ChatSession];
    await act(async () => {
      await view.queryClient.invalidateQueries({ queryKey: chatQueryKeys.sessions({ projectId, sourceView: textSourceView }), exact: true });
    });

    await waitFor(() => expect(screen.queryByRole('region', { name: 'Добро пожаловать в Canon Keeper' })).toBeNull());
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
    expect(steps().getByText('Подтвердите адрес new-author@example.test')).toBeTruthy();
    expect(screen.queryByText('Перетащите FB2, EPUB или DOCX')).toBeNull();
  });

  // R2.1.4
  it('does not ask to check the files once the import has started', async () => {
    renderShelf(`?projectId=${projectId}`);
    const input = await screen.findByLabelText('Файлы книг: FB2, EPUB или DOCX');
    await waitFor(() => expect(input).toHaveProperty('disabled', false));

    fireEvent.change(input, { target: { files: [new File(['book'], 'Том 5.epub', { type: 'application/epub+zip' })] } });

    await screen.findByRole('dialog', { name: 'Загрузка книг' });
    expect(screen.queryByText('Проверьте файлы и начните передачу.')).toBeNull();
  });
});
