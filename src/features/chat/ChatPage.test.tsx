import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../api/mock';
import { bookId, projectId } from '../../api/mock/repository/fixtures';
import { ApiStatusError } from '../../shared/api';
import { createAppRoute } from '../../shared/navigation/app-route';
import { InShell } from '../../app/shell/AppShell.test-helpers';
import { ChatPage } from './ChatPage';
import { NotesContext, type NotesActions } from '../../shared/notes/notes-context';
import sessionStyles from './ResearchChatSessions.module.css';

beforeEach(() => { vi.stubGlobal('innerWidth', 1440); vi.stubGlobal('innerHeight', 900); });
afterEach(() => vi.unstubAllGlobals());

function renderChat(
  api: ReturnType<typeof createMockApiClient>,
  query: Record<string, string | undefined>,
  onLogout = vi.fn(),
  notes: NotesActions | null = null,
) {
  const navigate = vi.fn();
  const queryClient = new QueryClient({
    defaultOptions: { mutations: { retry: false }, queries: { retry: false } },
  });
  const route = createAppRoute('/chat', query);
  // The chat in the shell of the root: the top bar, the sections and the saga are the shell's (SC1.1).
  render(
    <QueryClientProvider client={queryClient}>
      <NotesContext.Provider value={notes}>
        <InShell api={api} navigate={navigate} onLogout={onLogout} route={route}>
          <ChatPage api={api} navigate={navigate} route={route} />
        </InShell>
      </NotesContext.Provider>
    </QueryClientProvider>,
  );
  return navigate;
}

describe('ChatPage', () => {
  it('uses the available default project when the chat URL has no explicit project', async () => {
    renderChat(createMockApiClient(), {});
    expect(await screen.findByRole('region', { name: 'Исследовательский чат' })).toBeTruthy();
    expect(screen.queryByText('Здесь будут вопросы о вашей саге')).toBeNull();
  });
  it('keeps the current chat and draft when the next project fails to load', async () => {
    const api = createMockApiClient();
    await api.createProject({ title: 'Недоступная сага' });
    const navigate = renderChat(api, { projectId });
    await screen.findByRole('region', { name: 'Исследовательский чат' });
    fireEvent.change(screen.getByLabelText('Вопрос'), { target: { value: 'Не потерять вопрос' } });
    vi.spyOn(api, 'listBooks').mockRejectedValueOnce(new Error('Не удалось сменить сагу.'));
    fireEvent.click(screen.getByRole('button', { name: 'Сага: Хроники Белого порта' }));
    fireEvent.click(within(await screen.findByRole('menu', { name: 'Выбор саги' })).getByRole('menuitem', { name: 'Недоступная сага' }));
    await screen.findByText('Не удалось сменить сагу.');
    expect(navigate).not.toHaveBeenCalled();
    expect((screen.getByLabelText('Вопрос') as HTMLTextAreaElement).value).toBe('Не потерять вопрос');
    expect(screen.getByRole('button', { name: 'Сага: Хроники Белого порта' })).toBeTruthy();
  });
  it('creates a note by «Заметка» in place and opens the catalog only through «Заметки»', async () => {
    const capture = vi.fn();
    const navigate = renderChat(createMockApiClient(), { projectId }, vi.fn(), {
      capture, open: vi.fn(), openWithNavigation: vi.fn(),
    });
    await screen.findByRole('region', { name: 'Исследовательский чат' });
    fireEvent.click(within(screen.getByRole('banner')).getByRole('button', { name: 'Заметка' }));
    expect(capture).toHaveBeenCalledWith({ body: '' });
    expect(navigate).not.toHaveBeenCalled();
    expect(screen.getByRole('region', { name: 'Исследовательский чат' })).toBeTruthy();
    fireEvent.click(within(screen.getByRole('navigation', { name: 'Разделы' })).getByRole('button', { name: 'Заметки' }));
    expect(navigate).toHaveBeenCalledWith('/notes', expect.objectContaining({ returnTo: expect.stringContaining('/chat?') }));
  });
  // specs/2026-11-10-ux2-shell.md S2.2: a book opens from the shelf; the chat has neither «Чтение» nor a book scope.
  it('has no «Чтение» among the sections and no book scope selector', async () => {
    renderChat(createMockApiClient(), { projectId });
    await screen.findByRole('region', { name: 'Исследовательский чат' });
    expect(within(screen.getByRole('navigation', { name: 'Разделы' })).queryByRole('button', { name: 'Чтение' })).toBeNull();
    expect(screen.queryByLabelText('Область чата')).toBeNull();
  });

  it('opens saga-only history from a legacy book link without a scope selector', async () => {
    const api = createMockApiClient();
    const session = await api.createChatSession(projectId, {
      scope: 'project', sourceView: 'published', title: 'Сохранённая история книги',
    }, { idempotencyKey: crypto.randomUUID() });
    const listChatSessions = vi.spyOn(api, 'listChatSessions');
    renderChat(api, { projectId, bookId: 'former-book', chatId: session.id, sourceView: 'published' });

    expect(await screen.findByRole('region', { name: 'Исследовательский чат' })).toBeTruthy();
    expect((await screen.findByRole('button', { name: 'Сохранённая история книги' })).getAttribute('aria-current')).toBe('page');
    expect(screen.queryByLabelText('Область чата')).toBeNull();
    expect(screen.getByRole('button', { name: 'Сага: Хроники Белого порта' })).toBeTruthy();
    expect(listChatSessions).toHaveBeenCalledWith(projectId, { limit: 100, signal: expect.any(AbortSignal) });
  });

  it('keeps the Chat lifecycle unmounted while workspace authority is loading', async () => {
    const api = createMockApiClient();
    vi.spyOn(api, 'listProjects').mockImplementation(() => new Promise<never>(() => {}));
    const listChatSessions = vi.spyOn(api, 'listChatSessions');
    renderChat(api, { projectId });

    // The form of what comes, announced as busy, under the heading of the section (PS3.1).
    await waitFor(() => expect(document.querySelector('[data-loading][aria-busy="true"]')).toBeTruthy());
    expect(screen.getByRole('heading', { level: 1, name: 'Чат' })).toBeTruthy();
    expect(screen.queryByText(/Загружаем/u)).toBeNull();
    expect(screen.queryByRole('region', { name: 'Исследовательский чат' })).toBeNull();
    expect(listChatSessions).not.toHaveBeenCalled();
  });

  it('keeps the conversation inside the available viewport and the composer at the bottom', async () => {
    renderChat(createMockApiClient(), { projectId });

    const shell = document.querySelector<HTMLElement>('[data-shell]')!;
    const panel = await screen.findByRole('region', { name: 'Исследовательский чат' });
    const sessions = screen.getByLabelText('Сохранённые чаты');
    const messages = (await screen.findByRole('region', { name: 'Спросите о своей саге' })).parentElement!;
    const composer = screen.getByRole('textbox', { name: 'Вопрос' }).closest('form')!;

    expect(['100vh', '100dvh']).toContain(getComputedStyle(shell).height);
    expect(getComputedStyle(shell).overflow).toBe('hidden');
    expect(getComputedStyle(panel.closest('section[aria-labelledby="chat-page-title"]')!).overflow).toBe('hidden');
    expect(getComputedStyle(panel).overflow).toBe('hidden');
    expect(getComputedStyle(sessions).overflowY).toBe('auto');
    expect(getComputedStyle(messages).overflowY).toBe('auto');
    expect(getComputedStyle(messages.parentElement!).overflow).toBe('hidden');
    expect(getComputedStyle(composer).flexShrink).toBe('0');
  });

  it('делегирует выход единственному callback приложения', async () => {
    const onLogout = vi.fn();
    renderChat(createMockApiClient(), { projectId }, onLogout);

    fireEvent.click(screen.getByLabelText('Аккаунт'));
    fireEvent.click(await screen.findByRole('button', { name: 'Выйти' }));

    expect(onLogout).toHaveBeenCalledTimes(1);
  });

  it('открывает мобильное меню аккаунта через аватар', async () => {
    const onLogout = vi.fn();
    renderChat(createMockApiClient(), { projectId }, onLogout);

    fireEvent.click(screen.getByLabelText('Аккаунт'));
    const menu = await screen.findByRole('dialog', { name: 'Меню аккаунта' });
    expect(within(menu).getByText('Мира')).toBeTruthy();

    // «Связаться с нами» comes from the session of the page (specs/2026-11-07-contacts.md C2.3); a mailto letter is gone.
    expect(within(menu).queryByRole('link', { name: 'Написать нам' })).toBeNull();

    fireEvent.click(within(menu).getByRole('button', { name: 'Выйти' }));
    expect(onLogout).toHaveBeenCalledTimes(1);
  });

  it('хранит выбранный чат только в canonical /chat query', async () => {
    const api = createMockApiClient();
    const first = await api.createChatSession(
      projectId,
      { scope: 'project', sourceView: 'published', title: 'Первый чат' },
      { idempotencyKey: 'first-chat' },
    );
    const second = await api.createChatSession(
      projectId,
      { scope: 'project', sourceView: 'published', title: 'Второй чат' },
      { idempotencyKey: 'second-chat' },
    );
    const navigate = renderChat(api, { projectId, chatId: first.id, sourceView: 'published' });

    fireEvent.click(await screen.findByRole('button', { name: 'Второй чат' }));

    expect(navigate).toHaveBeenCalledWith('/chat', { projectId, chatId: second.id });
    expect(within(screen.getByRole('navigation', { name: 'Разделы' })).getByRole('button', { name: 'Чат' }).getAttribute('aria-current')).toBe('page');
  });

  it('показывает сохранённый чат без ложного счётчика сообщений', async () => {
    const api = createMockApiClient();
    await api.createChatSession(
      projectId,
      { scope: 'project', sourceView: 'published', title: 'Чат без счётчика' },
      { idempotencyKey: 'idem-chat-without-message-count' },
    );
    renderChat(api, { projectId, sourceView: 'published' });

    const session = await screen.findByRole('button', { name: 'Чат без счётчика' });
    expect(getComputedStyle(session).gridTemplateColumns).toBe('auto 1fr');
    expect(getComputedStyle(session).fontSize).toBe('12px');
    expect(getComputedStyle(session).lineHeight).toBe('16px');
    expect(getComputedStyle(session).fontWeight).toBe('500');
    expect(getComputedStyle(screen.getByRole('button', { name: 'Новый чат' })).fontWeight).toBe('600');
    const buttonRule = [...document.styleSheets].flatMap((sheet) => [...sheet.cssRules])
      .find((rule): rule is CSSStyleRule => rule instanceof CSSStyleRule && rule.selectorText === `.${sessionStyles.sessions} button`);
    expect(buttonRule?.style.getPropertyValue('font')).toBe('');
  });

  // specs/2026-11-11-ux2-polish.md PL2.1, PL2.4: «Чат» stands, seen, in the row of the panel — the head of the column beside
  // «Новый чат», the column folded, beside «Чаты»; it is one element from the loading on.
  it('puts its heading in the row of the panel, beside «Новый чат» or «Чаты», and keeps it from the loading on', async () => {
    const api = createMockApiClient();
    let release!: () => void;
    const held = new Promise<void>((resolve) => { release = resolve; });
    const status = api.getIndexingStatus.bind(api);
    vi.spyOn(api, 'getIndexingStatus').mockImplementation(async (...args) => { await held; return status(...args); });
    renderChat(api, { projectId, new: '1' });
    const heading = await screen.findByRole('heading', { level: 1, name: 'Чат' });
    expect(heading.hasAttribute('data-page-heading')).toBe(true);
    release();
    const panel = await screen.findByRole('region', { name: 'Исследовательский чат' });
    expect(screen.getByRole('heading', { level: 1, name: 'Чат' })).toBe(heading);
    const row = heading.parentElement!;
    expect(row.contains(panel)).toBe(false);
    await waitFor(() => expect(within(row).getByRole('button', { name: 'Новый чат' })).toBeTruthy());
    expect(within(screen.getByLabelText('Сохранённые чаты')).queryByRole('button', { name: 'Новый чат' })).toBeNull();
    expect(row.closest('[data-chat-panel]')).toBe(panel.closest('[data-chat-panel]'));
    cleanup();

    vi.stubGlobal('innerWidth', 1365);
    vi.stubGlobal('innerHeight', 640);
    renderChat(createMockApiClient(), { projectId, new: '1' });
    await screen.findByRole('region', { name: 'Исследовательский чат' });
    const folded = screen.getByRole('heading', { level: 1, name: 'Чат' }).parentElement!;
    await waitFor(() => expect(within(folded).getByRole('button', { name: 'Чаты' })).toBeTruthy());
    expect(screen.queryByLabelText('Сохранённые чаты')).toBeNull();
  });

  it('fail-closed обрабатывает неизвестный Chat query до запуска session lifecycle', async () => {
    const api = createMockApiClient();
    const listChatSessions = vi.spyOn(api, 'listChatSessions');
    const navigate = renderChat(api, { projectId, prompt: 'private' });

    expect((await screen.findByRole('alert', { name: 'Такого чата нет' })).textContent).toContain('Адрес устарел или в нём опечатка.');
    fireEvent.click(screen.getByRole('button', { name: 'К списку чатов' }));
    expect(navigate).toHaveBeenCalledWith('/chat', { projectId, chatId: undefined });
    expect(listChatSessions).not.toHaveBeenCalled();
    expect(screen.queryByRole('region', { name: 'Исследовательский чат' })).toBeNull();
  });

  it('lists the chats of both text versions together without a version control', async () => {
    const api = createMockApiClient();
    for (const [sourceView, title] of [['draft', 'Чат черновика'], ['published', 'Чат книги']] as const) {
      await api.createChatSession(projectId, { scope: 'project', sourceView, title }, { idempotencyKey: crypto.randomUUID() });
    }
    const createChatSession = vi.spyOn(api, 'createChatSession');
    renderChat(api, { projectId, sourceView: 'draft' });

    expect(await screen.findByRole('button', { name: 'Чат черновика' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Чат книги' })).toBeTruthy();
    expect(screen.queryByLabelText(/Версия текста/)).toBeNull();
    expect(screen.queryByText(/Черновик|Опубликован/)).toBeNull();
    fireEvent.change(screen.getByRole('textbox', { name: 'Вопрос' }), { target: { value: 'Кто такой Ворон?' } });
    fireEvent.click(screen.getByRole('button', { name: 'Спросить' }));
    await waitFor(() => expect(createChatSession).toHaveBeenCalledWith(projectId, expect.objectContaining({ sourceView: 'published' }), expect.anything()));
  });

  it('lets a viewer read the chats of the published text without creating one', async () => {
    const api = createMockApiClient();
    await api.createChatSession(projectId, { scope: 'project', sourceView: 'draft', title: 'Чат черновика' }, { idempotencyKey: crypto.randomUUID() });
    await api.createChatSession(projectId, { scope: 'project', sourceView: 'published', title: 'Чат книги' }, { idempotencyKey: crypto.randomUUID() });
    api.setScenario({ actorRole: 'viewer', preset: 'normal' });
    renderChat(api, { projectId });

    expect(await screen.findByRole('button', { name: 'Чат книги' })).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Чат черновика' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Новый чат' })).toBeNull();
    expect(screen.queryByText(/Черновик|Опубликован/)).toBeNull();
  });

  it('выбирает проект списком без второго sidebar и не переносит версию текста', async () => {
    const api = createMockApiClient();
    const nextProject = await api.createProject({ title: 'Песни южных островов' });
    const navigate = renderChat(api, { projectId, sourceView: 'published' });

    // One menu of the saga, the shell's (S3.1): the chat has no chip and no list of its own.
    expect(screen.queryByLabelText('Сага чата')).toBeNull();
    fireEvent.click(await screen.findByRole('button', { name: 'Сага: Хроники Белого порта' }));
    expect(screen.getAllByRole('navigation', { name: 'Разделы' })).toHaveLength(1);
    fireEvent.click(within(await screen.findByRole('menu', { name: 'Выбор саги' })).getByRole('menuitem', { name: 'Песни южных островов' }));

    await waitFor(() => expect(navigate).toHaveBeenCalledWith('/chat', { projectId: nextProject.id }));
  });

  it('says the text is not ready yet and leads to the books', async () => {
    const api = createMockApiClient();
    vi.spyOn(api, 'getIndexingStatus').mockResolvedValue({
      published: { exists: false, context: null },
      draft: { exists: true as const, context: { state: 'ready' as const, currentBook: null, retryableFailedBook: null } },
      steps: [],
    });
    const navigate = renderChat(api, { projectId });

    expect(await screen.findByText('Здесь будут вопросы о вашей саге')).toBeTruthy();
    // The saga has a book that is not read yet: the first step cannot be taken here, the line says what is needed (PS2.1).
    const empty = screen.getByText('Здесь будут вопросы о вашей саге').parentElement!;
    expect(within(empty).getByText(/Вопросы можно задавать, когда книга будет прочитана/u)).toBeTruthy();
    expect(within(empty).queryByRole('button')).toBeNull();
    expect(screen.queryByRole('region', { name: 'Исследовательский чат' })).toBeNull();
    fireEvent.click(screen.getByRole('link', { name: 'к книгам' }));
    expect(navigate).toHaveBeenCalledWith('/manuscript/books', { projectId });
  });

  // specs/2026-11-10-ux3-saga-state.md SS3.1: one line of advice while the saga is read, by the counts of the server, and
  // no plate of a partial answer; it follows the reading until its end.
  it('advises to ask now while the saga is read and follows the reading until its end', async () => {
    const api = createMockApiClient();
    const processing = { exists: true as const, context: { state: 'processing' as const, currentBook: { bookId, position: 2, total: 3 }, retryableFailedBook: null } };
    const ready = { exists: true as const, context: { state: 'ready' as const, currentBook: null, retryableFailedBook: null } };
    const reading = {
      remainingSeconds: null, letterPromised: false, chaptersRead: 120, chaptersTotal: 300, booksRead: 1, booksTotal: 3,
      currentBookId: bookId, askable: true, books: [],
    };
    let state: 'processing' | 'ready' = 'processing';
    const getIndexingStatus = vi.spyOn(api, 'getIndexingStatus').mockImplementation(async () => ({
      published: state === 'processing' ? processing : ready,
      steps: [],
      ...(state === 'processing' ? { preparation: reading } : {}),
    }));
    renderChat(api, { projectId });

    const notice = await screen.findByText('Помощник ещё читает книги: 120 из 300 глав · обычно 10–30 минут. Спрашивайте уже сейчас — ответы обо всей саге станут точнее, когда он дочитает.');
    expect(screen.getByRole('region', { name: 'Исследовательский чат' }).contains(notice)).toBe(true);
    expect(screen.queryByText(/неполным|готовятся|карточки/u)).toBeNull();
    expect(screen.getByRole('textbox', { name: 'Вопрос' })).toBeTruthy();

    const calls = getIndexingStatus.mock.calls.length;
    state = 'ready';
    await waitFor(() => expect(screen.queryByText(/Помощник ещё читает/u)).toBeNull(), { timeout: 3_000 });
    expect(getIndexingStatus.mock.calls.length).toBeGreaterThan(calls);
  });

  // FS5.6, SS3.1: the time and the letter are the server's; before the search finds a chapter the line says when to ask.
  it('names the time the server says is left, the letter, and when a question may be asked', async () => {
    const api = createMockApiClient();
    const reading = {
      remainingSeconds: 700, letterPromised: true, chaptersRead: 3, chaptersTotal: 20, booksRead: 0, booksTotal: 1,
      currentBookId: bookId, askable: true, books: [{ bookId, chaptersRead: 3, chaptersTotal: 20 }],
    };
    const status = vi.spyOn(api, 'getIndexingStatus').mockImplementation(async () => ({
      published: { exists: true as const, context: { state: 'processing' as const, currentBook: { bookId, position: 1, total: 1 }, retryableFailedBook: null } },
      steps: [],
      preparation: reading,
    }));
    renderChat(api, { projectId });

    expect(await screen.findByText('Помощник ещё читает книгу: 3 из 20 глав · осталось ≈ 12 мин. Спрашивайте уже сейчас — ответы обо всей книге станут точнее, когда он дочитает. Напишем на почту, когда он дочитает.')).toBeTruthy();
    expect(screen.getByRole('textbox', { name: 'Вопрос' })).toBeTruthy();
    status.mockRestore();
  });

  it('says the reading stopped and leads to the books, where it goes on', async () => {
    const api = createMockApiClient();
    vi.spyOn(api, 'getIndexingStatus').mockResolvedValue({
      published: { exists: true as const, context: { state: 'partial' as const, currentBook: null, retryableFailedBook: { bookId } } },
      steps: [],
    });
    const navigate = renderChat(api, { projectId });

    expect(await screen.findByText('Помощник прочитал не всю книгу — отвечаю по прочитанной части. Продолжить чтение можно на странице книг.')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'К книгам' }));
    expect(navigate).toHaveBeenCalledWith('/manuscript/books', { projectId });
  });

  it('shows no preparation notice when the saga is ready', async () => {
    const api = createMockApiClient();
    vi.spyOn(api, 'getIndexingStatus').mockResolvedValue({
      published: { exists: true as const, context: { state: 'ready' as const, currentBook: null, retryableFailedBook: null } },
      steps: [],
    });
    renderChat(api, { projectId });
    await screen.findByRole('textbox', { name: 'Вопрос' });
    expect(screen.queryByText(/Помощник ещё читает|не всю книгу/u)).toBeNull();
  });

  it('does not start the chat before the text check', async () => {
    const api = createMockApiClient();
    vi.spyOn(api, 'getIndexingStatus').mockImplementation(() => new Promise<never>(() => {}));
    renderChat(api, { projectId });

    await waitFor(() => expect(document.querySelector('[data-loading][aria-busy="true"]')).toBeTruthy());
    expect(screen.queryByText(/Проверяем/u)).toBeNull();
    expect(screen.queryByRole('region', { name: 'Исследовательский чат' })).toBeNull();
  });

  // specs/2026-11-11-ux4-after-answer.md AA2.1: «Чат» opens the latest conversation of the saga, by the time of its last question.
  describe('the latest conversation', () => {
    async function threeChats(api: ReturnType<typeof createMockApiClient>) {
      const titles = ['Старый', 'Свежий', 'Вчерашний'];
      const chats = [];
      for (const title of titles) chats.push(await api.createChatSession(projectId, { scope: 'project', sourceView: 'published', title }, { idempotencyKey: crypto.randomUUID() }));
      const asked = ['2026-11-01T10:00:00Z', '2026-11-11T08:00:00Z', '2026-11-10T20:00:00Z'];
      const list = api.listChatSessions.bind(api);
      // The list stands by the time a chat was made or renamed; the latest conversation is by its last question.
      vi.spyOn(api, 'listChatSessions').mockImplementation(async (...input) => {
        const page = await list(...input);
        return { ...page, data: page.data.map((chat) => ({ ...chat, latestTurnAt: asked[titles.indexOf(chat.title)]! })) };
      });
      return chats;
    }

    it('opens the chat asked last when the address names none', async () => {
      const api = createMockApiClient();
      const [, fresh] = await threeChats(api);
      const navigate = renderChat(api, { projectId });
      await waitFor(() => expect(navigate).toHaveBeenCalledWith('/chat', { projectId, chatId: fresh!.id }, { replace: true }));
    });

    it('opens a new chat for «Новый чат» and for a question brought from another section', async () => {
      const api = createMockApiClient();
      await threeChats(api);
      const navigate = renderChat(api, { projectId, new: '1' });
      expect(await screen.findByRole('textbox', { name: 'Вопрос' })).toBeTruthy();
      await screen.findByRole('button', { name: 'Свежий' });
      expect(navigate).not.toHaveBeenCalledWith('/chat', expect.objectContaining({ chatId: expect.any(String) }), { replace: true });
      cleanup();

      const asked = vi.fn();
      const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
      const route = { ...createAppRoute('/chat', { projectId }), chatQuestion: 'Расскажи о «Мара»' };
      render(<QueryClientProvider client={queryClient}><InShell api={api} navigate={asked} route={route}><ChatPage api={api} navigate={asked} route={route} /></InShell></QueryClientProvider>);
      await waitFor(async () => expect(((await screen.findByRole('textbox', { name: 'Вопрос' })) as HTMLTextAreaElement).value).toBe('Расскажи о «Мара»'));
      await screen.findByRole('button', { name: 'Свежий' });
      expect(asked).not.toHaveBeenCalledWith('/chat', expect.objectContaining({ chatId: expect.any(String) }), { replace: true });
    });

    // PR3: a list that did not open is the block of the panel with its retry — the search of the latest one does not
    // hold the page in its wait while the panel reads the list again.
    it('leaves a list that did not open to the block of the panel', async () => {
      const api = createMockApiClient();
      vi.spyOn(api, 'listChatSessions').mockRejectedValue(new ApiStatusError(503, {
        type: 'about:blank', title: 'Busy', status: 503, code: 'service_unavailable', requestId: 'req-chats',
      }, new Response(null, { status: 503 })));
      renderChat(api, { projectId });
      expect(await screen.findByRole('alert', { name: 'Не открылся список чатов' }, { timeout: 5000 })).toBeTruthy();
    });

    it('says «Новый чат» in the address', async () => {
      const api = createMockApiClient();
      const [old] = await threeChats(api);
      const navigate = renderChat(api, { projectId, chatId: old!.id });
      fireEvent.click(await screen.findByRole('button', { name: 'Новый чат' }));
      expect(navigate).toHaveBeenCalledWith('/chat', { projectId, new: '1' });
    });
  });
});
