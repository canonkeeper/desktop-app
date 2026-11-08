import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../api/mock';
import { bookId, projectId } from '../../api/mock/repository/fixtures';
import { createAppRoute } from '../../shared/navigation/app-route';
import { ChatPage } from './ChatPage';
import { NotesContext, type NotesActions } from '../../shared/notes/notes-context';
import chatStyles from './ChatPage.module.css';
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
  render(
    <QueryClientProvider client={queryClient}>
      <NotesContext.Provider value={notes}>
      <ChatPage
        api={api}
        navigate={navigate}
        onLogout={onLogout}
        route={createAppRoute('/chat', query)}
        userName="Редактор"
      />
      </NotesContext.Provider>
    </QueryClientProvider>,
  );
  return navigate;
}

describe('ChatPage', () => {
  it('uses the available default project when the chat URL has no explicit project', async () => {
    renderChat(createMockApiClient(), {});
    expect(await screen.findByRole('region', { name: 'Исследовательский чат' })).toBeTruthy();
    expect(screen.queryByText('Пока нет саги')).toBeNull();
  });
  it('keeps the current chat and draft when the next project fails to load', async () => {
    const api = createMockApiClient();
    await api.createProject({ title: 'Недоступная сага' });
    const navigate = renderChat(api, { projectId });
    await screen.findByRole('region', { name: 'Исследовательский чат' });
    fireEvent.change(screen.getByLabelText('Вопрос'), { target: { value: 'Не потерять вопрос' } });
    vi.spyOn(api, 'listBooks').mockRejectedValueOnce(new Error('Не удалось сменить сагу.'));
    fireEvent.click(screen.getByLabelText('Сага чата'));
    fireEvent.click(screen.getByRole('option', { name: 'Недоступная сага' }));
    await screen.findByText('Не удалось сменить сагу.');
    expect(navigate).not.toHaveBeenCalled();
    expect((screen.getByLabelText('Вопрос') as HTMLTextAreaElement).value).toBe('Не потерять вопрос');
    expect(screen.getByLabelText('Сага чата').textContent).toContain('Хроники Белого порта');
  });
  it('creates a note from the sidebar in place and opens the catalog only through its link', async () => {
    const capture = vi.fn();
    const navigate = renderChat(createMockApiClient(), { projectId }, vi.fn(), {
      capture, open: vi.fn(), openWithNavigation: vi.fn(),
    });
    await screen.findByRole('region', { name: 'Исследовательский чат' });
    const sidebar = screen.getByRole('complementary', { name: 'Навигация саги' });
    fireEvent.click(within(sidebar).getByRole('button', { name: 'Создать заметку' }));
    expect(capture).toHaveBeenCalledWith({ body: '' });
    expect(navigate).not.toHaveBeenCalled();
    expect(screen.getByRole('region', { name: 'Исследовательский чат' })).toBeTruthy();
    fireEvent.click(within(sidebar).getByRole('button', { name: 'Заметки' }));
    expect(navigate).toHaveBeenCalledWith('/notes');
  });
  it('opens a book picker from saga Chat without adding a book scope selector', async () => {
    const navigate = renderChat(createMockApiClient(), { projectId });
    await screen.findByRole('region', { name: 'Исследовательский чат' });
    fireEvent.click(screen.getByRole('button', { name: 'Чтение' }));
    expect(await screen.findByRole('dialog', { name: 'Книга для чтения' })).toBeTruthy();
    expect(navigate).not.toHaveBeenCalled();
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
    expect(screen.getByLabelText(/^Выбрать сагу/u)).toBeTruthy();
    expect(listChatSessions).toHaveBeenCalledWith(projectId, { limit: 100, signal: expect.any(AbortSignal) });
  });

  it('keeps the Chat lifecycle unmounted while workspace authority is loading', async () => {
    const api = createMockApiClient();
    vi.spyOn(api, 'listProjects').mockImplementation(() => new Promise<never>(() => {}));
    const listChatSessions = vi.spyOn(api, 'listChatSessions');
    renderChat(api, { projectId });

    expect(await screen.findByText('Загружаем сагу…')).toBeTruthy();
    expect(screen.queryByRole('region', { name: 'Исследовательский чат' })).toBeNull();
    expect(listChatSessions).not.toHaveBeenCalled();
  });

  it('offers the canonical project creation destination when no project exists', async () => {
    const api = createMockApiClient();
    api.setScenario({ actorRole: 'owner', preset: 'empty-lists' });
    const navigate = renderChat(api, {});

    expect(await screen.findByText('Пока нет саги')).toBeTruthy();
    expect(screen.getByText('Создайте сагу и загрузите книгу — после этого здесь можно задавать вопросы.')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Создать сагу' }));
    expect(navigate).toHaveBeenCalledWith('/manuscript/books', { projectId: undefined });
  });

  it('keeps the conversation inside the available viewport and the composer at the bottom', async () => {
    renderChat(createMockApiClient(), { projectId });

    const workspace = screen.getByRole('main');
    const panel = await screen.findByRole('region', { name: 'Исследовательский чат' });
    const sessions = screen.getByLabelText('Сохранённые чаты');
    const messages = (await screen.findByRole('region', { name: 'Спросите о своей саге' })).parentElement!;
    const composer = screen.getByRole('textbox', { name: 'Вопрос' }).closest('form')!;

    expect(['100vh', '100dvh']).toContain(getComputedStyle(workspace).height);
    expect(getComputedStyle(workspace).overflow).toBe('hidden');
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
    expect(within(menu).getByText('Редактор')).toBeTruthy();

    // «Связаться с нами» comes from the session of the page (specs/2026-11-07-contacts.md C2.3); a mailto letter is gone.
    expect(within(menu).queryByRole('link', { name: 'Написать в поддержку' })).toBeNull();

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

    fireEvent.click(await screen.findByRole('button', { name: /Второй чат/ }));

    expect(navigate).toHaveBeenCalledWith('/chat', { projectId, chatId: second.id });
    expect(screen.getByRole('button', { name: 'Чат' }).getAttribute('aria-current')).toBe('page');
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

  it('keeps the project label available in the compact layout', () => {
    const compact = [...document.styleSheets].flatMap((sheet) => [...sheet.cssRules])
      .find((rule) => rule.cssText.startsWith('@container chat-viewport (max-width: 360px)'));
    expect(compact?.cssText).not.toContain(`.${chatStyles.scopeProject}`);
  });

  it('uses one compact toolbar and keeps its heading only for assistive navigation', () => {
    const mobile = [...document.styleSheets].flatMap((sheet) => [...sheet.cssRules])
      .filter((rule): rule is CSSMediaRule => rule instanceof CSSMediaRule && rule.conditionText.includes('max-height: 760px'))
      .find((rule) => [...rule.cssRules].some((nested) => nested instanceof CSSStyleRule
        && nested.selectorText === `.${chatStyles.heading}`));
    const rules = [...(mobile?.cssRules ?? [])];
    const heading = rules.find((rule): rule is CSSStyleRule => rule instanceof CSSStyleRule
      && rule.selectorText === `.${chatStyles.heading}`);
    const title = rules.find((rule): rule is CSSStyleRule => rule instanceof CSSStyleRule
      && rule.selectorText === `.${chatStyles.heading} > div:first-child`);
    expect(heading?.style.getPropertyValue('min-height')).toBe('44px');
    expect(title?.style.getPropertyValue('clip-path')).toBe('inset(50%)');
  });

  it('keeps the compact scope pill inside a touch button without changing its labels or project action', async () => {
    const navigate = renderChat(createMockApiClient(), { projectId, bookId, sourceView: 'published' });
    await screen.findByRole('region', { name: 'Исследовательский чат' });
    const scope = screen.getByLabelText(/^Выбрать сагу/u);
    expect(scope.getAttribute('data-size')).toBe('touch');
    expect(getComputedStyle(scope).minHeight).toBe('44px');
    const pill = scope.firstElementChild!;
    expect(pill.tagName).toBe('SPAN');
    expect(getComputedStyle(pill).fontSize).toBe('12px');
    expect(getComputedStyle(pill).fontWeight).toBe('500');
    expect(getComputedStyle(pill).lineHeight).toBe('15px');
    const pillRule = [...document.styleSheets].flatMap((sheet) => [...sheet.cssRules])
      .find((rule): rule is CSSStyleRule => rule instanceof CSSStyleRule && rule.selectorText === `.${chatStyles.scopePill}`);
    expect(pillRule?.style.getPropertyValue('background')).toBe('var(--ck-secondary)');
    expect(pill.querySelector(`.${chatStyles.scopeProject}`)?.textContent).toBe('Хроники Белого порта');
    expect(pill.textContent).not.toContain('Книга II. Карта приливов');
    const libraryIcon = screen.getAllByLabelText('Мои книги').find((element) => element.tagName === 'BUTTON')?.querySelector('svg');
    expect(libraryIcon?.classList.contains('lucide-library')).toBe(true);
    expect(libraryIcon?.getAttribute('width')).toBe('16');
    const avatar = screen.getByLabelText('Аккаунт');
    expect(getComputedStyle(avatar).width).toBe('44px');
    expect(getComputedStyle(avatar).height).toBe('44px');
    fireEvent.click(scope);
    expect(await screen.findByRole('dialog', { name: 'Навигация саги' })).toBeTruthy();
    expect(navigate).not.toHaveBeenCalled();
  });

  it('fail-closed обрабатывает неизвестный Chat query до запуска session lifecycle', async () => {
    const api = createMockApiClient();
    const listChatSessions = vi.spyOn(api, 'listChatSessions');
    const navigate = renderChat(api, { projectId, prompt: 'private' });

    expect((await screen.findByRole('alert')).textContent).toContain('Неверный маршрут');
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
    const nextProject = await api.createProject({ title: 'Новая сага' });
    const navigate = renderChat(api, { projectId, sourceView: 'published' });

    const projectTrigger = await screen.findByLabelText('Сага чата');
    fireEvent.click(projectTrigger);
    expect(screen.queryByRole('dialog', { name: 'Навигация саги' })).toBeNull();
    expect(screen.getAllByRole('complementary', { name: 'Навигация саги' })).toHaveLength(1);
    const projectOptions = screen.getByRole('listbox', { name: 'Сага чата' });
    expect(projectTrigger.getAttribute('aria-controls')).toBe(projectOptions.id);
    fireEvent.click(within(projectOptions).getByRole('option', { name: 'Новая сага' }));

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

    expect(await screen.findByText('Книги ещё не загружены или не подготовлены')).toBeTruthy();
    expect(screen.getByText('Загрузите книгу — вопросы можно задавать через минуту-две после загрузки.')).toBeTruthy();
    expect(screen.queryByRole('region', { name: 'Исследовательский чат' })).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'К книгам' }));
    expect(navigate).toHaveBeenCalledWith('/manuscript/books', { projectId });
  });

  it('says the books are still being prepared and follows the preparation until it is ready (R2.3.3)', async () => {
    const api = createMockApiClient();
    const processing = { exists: true as const, context: { state: 'processing' as const, currentBook: { bookId, position: 2, total: 3 }, retryableFailedBook: null } };
    const ready = { exists: true as const, context: { state: 'ready' as const, currentBook: null, retryableFailedBook: null } };
    let state: 'processing' | 'ready' = 'processing';
    const getIndexingStatus = vi.spyOn(api, 'getIndexingStatus').mockImplementation(async (_project, options) => ({
      published: state === 'processing' ? processing : ready,
      steps: options?.bookId ? [{ kind: 'cards' as const, status: 'running' as const, progress: 0.4, counters: { indexedChapters: 120, totalChapters: 300 } }] : [],
    }));
    const navigate = renderChat(api, { projectId });

    const notice = await screen.findByText('Книги ещё готовятся: книга 2 из 3, карточки глав 120 из 300. Пока отвечаю по готовой части — ответ может быть неполным.');
    expect(screen.getByRole('region', { name: 'Исследовательский чат' }).contains(notice)).toBe(true);
    fireEvent.click(screen.getByRole('button', { name: 'Подробнее на полке' }));
    expect(navigate).toHaveBeenCalledWith('/manuscript/books', { projectId });

    const calls = getIndexingStatus.mock.calls.length;
    state = 'ready';
    await waitFor(() => expect(screen.queryByText(/Книги ещё готовятся/u)).toBeNull(), { timeout: 3_000 });
    expect(getIndexingStatus.mock.calls.length).toBeGreaterThan(calls);
  });

  it('says only part of the chapters is prepared and leads to the books', async () => {
    const api = createMockApiClient();
    vi.spyOn(api, 'getIndexingStatus').mockResolvedValue({
      published: { exists: true as const, context: { state: 'partial' as const, currentBook: null, retryableFailedBook: { bookId } } },
      steps: [],
    });
    const navigate = renderChat(api, { projectId });

    expect(await screen.findByText('Часть глав не подготовлена. Ответы — по готовой части.')).toBeTruthy();
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
    expect(screen.queryByText(/Книги ещё готовятся|Часть глав не подготовлена/u)).toBeNull();
  });

  it('does not start the chat before the text check', async () => {
    const api = createMockApiClient();
    vi.spyOn(api, 'getIndexingStatus').mockImplementation(() => new Promise<never>(() => {}));
    renderChat(api, { projectId });

    expect(await screen.findByText('Проверяем книги саги…')).toBeTruthy();
    expect(screen.queryByRole('region', { name: 'Исследовательский чат' })).toBeNull();
  });
});
