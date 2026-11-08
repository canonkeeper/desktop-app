import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, createEvent, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../api/mock';
import { projectId } from '../../api/mock/repository/fixtures';
import { chatQueryKeys, createIdempotencyKey } from '../../entities/chat/api';
import { ResearchChatPanel } from './ResearchChatPanel';

beforeEach(() => { vi.stubGlobal('innerWidth', 1440); vi.stubGlobal('innerHeight', 900); });
afterEach(() => vi.unstubAllGlobals());

function renderPanel(api: ReturnType<typeof createMockApiClient>, selectedChatId = '', canCreate = true,
  queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })) {
  return render(<QueryClientProvider client={queryClient}>
    <ResearchChatPanel api={api} canCreate={canCreate} onOpenSource={vi.fn()} onSelectChatId={vi.fn()}
      projectId={projectId} selectedChatId={selectedChatId} sourceView="published" />
  </QueryClientProvider>);
}

describe('Research Chat saved assignment', () => {
  it('keeps the new-chat field in the flexible column without a delete button', async () => {
    renderPanel(createMockApiClient());
    const textarea = await screen.findByRole('textbox', { name: 'Вопрос' });
    const field = textarea.parentElement!.parentElement!;
    const actions = field.nextElementSibling!;
    expect(screen.queryByRole('button', { name: 'Удалить чат' })).toBeNull();
    expect(getComputedStyle(field).gridColumn).toBe('2');
    expect(getComputedStyle(actions).gridColumn).toBe('3');
  });
  it('shows pending sessions without blocking New chat for list loading alone', async () => {
    const api = createMockApiClient();
    vi.spyOn(api, 'listChatSessions').mockImplementation(() => new Promise<never>(() => {}));
    renderPanel(api);

    const loading = await screen.findByRole('status', { name: 'Загрузка чатов' });
    expect(getComputedStyle(loading).gridTemplateColumns).toBe('minmax(0, 1fr)');
    expect(screen.getByRole('button', { name: 'Новый чат' })).toHaveProperty('disabled', false);
  });

  it('replaces stale chat rows after a failed refresh and retries that list request inline', async () => {
    const api = createMockApiClient();
    const session = await api.createChatSession(projectId, {
      scope: 'project', sourceView: 'published', title: 'Устаревший чат',
    }, { idempotencyKey: createIdempotencyKey('createChatSession') });
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    renderPanel(api, session.id, true, queryClient);
    expect(await screen.findByRole('button', { name: session.title })).toBeTruthy();

    vi.spyOn(api, 'listChatSessions').mockRejectedValueOnce(new TypeError('offline'));
    const queryKey = chatQueryKeys.sessions({ projectId, sourceView: 'published' });
    act(() => { void queryClient.invalidateQueries({ queryKey, exact: true }); });

    expect(await screen.findByText('Не удалось загрузить чаты.')).toBeTruthy();
    expect(screen.queryByRole('button', { name: session.title })).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Повторить загрузку чатов' }));
    expect(await screen.findByRole('button', { name: session.title })).toBeTruthy();
  });

  it('shows the selected snapshot placeholder until its response and keeps resolved content on background refetch', async () => {
    const api = createMockApiClient();
    const session = await api.createChatSession(projectId, {
      scope: 'project', sourceView: 'published', savedAssignment: 'Поручение выбранного чата',
    }, { idempotencyKey: createIdempotencyKey('createChatSession') });
    let resolveSession!: (value: typeof session) => void;
    const readSession = vi.spyOn(api, 'getChatSession').mockImplementationOnce(() => new Promise((resolve) => { resolveSession = resolve; }));
    const readTimeline = vi.spyOn(api, 'getAgentTimeline');
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const { container } = renderPanel(api, session.id, true, queryClient);

    const loading = await screen.findByRole('status', { name: 'Загрузка чата' });
    expect(getComputedStyle(loading.firstElementChild!).gridTemplateColumns).toBe('minmax(0, 1fr)');
    expect(screen.getByRole('textbox', { name: 'Вопрос' })).toHaveProperty('disabled', true);
    expect(screen.queryByRole('region', { name: 'Сохранённый вопрос' })).toBeNull();
    expect(container.querySelector('footer[data-ephemeral]')).toBeNull();
    expect(readTimeline).not.toHaveBeenCalled();

    await act(async () => resolveSession(session));
    const saved = await screen.findByRole('region', { name: 'Сохранённый вопрос' });
    expect(within(saved).getByText('Поручение выбранного чата')).toBeTruthy();
    expect(screen.queryByRole('status', { name: 'Загрузка чата' })).toBeNull();
    expect(screen.getByRole('textbox', { name: 'Вопрос' })).toHaveProperty('disabled', false);

    readSession.mockImplementation(() => new Promise<never>(() => {}));
    const queryKey = chatQueryKeys.session({ projectId, sourceView: 'published' }, session.id);
    act(() => { void queryClient.invalidateQueries({ queryKey, exact: true }); });
    await waitFor(() => expect(queryClient.isFetching({ queryKey, exact: true })).toBe(1));
    expect(screen.getByRole('region', { name: 'Сохранённый вопрос' })).toBe(saved);
    expect(screen.queryByRole('status', { name: 'Загрузка чата' })).toBeNull();
    expect(screen.getByRole('textbox', { name: 'Вопрос' })).toHaveProperty('disabled', false);
  });

  it.each(['createChatSession', 'createChatTurn'] as const)('keeps pre-Job controls busy without a run header or focus while %s is pending', async (operation) => {
    const api = createMockApiClient();
    const pending = vi.spyOn(api, operation).mockImplementation(() => new Promise<never>(() => {}));
    const { container } = renderPanel(api);
    const composer = await screen.findByRole('textbox', { name: 'Вопрос' });
    fireEvent.change(composer, { target: { value: 'Поручение до создания Job' } });
    const submit = screen.getByRole('button', { name: 'Спросить' });
    await waitFor(() => expect(submit).toHaveProperty('disabled', false));
    fireEvent.click(submit);
    await waitFor(() => expect(pending).toHaveBeenCalledTimes(1));

    expect(composer).toHaveProperty('disabled', true);
    expect(composer).toHaveProperty('value', 'Поручение до создания Job');
    expect(submit).toHaveProperty('disabled', true);
    expect(screen.getByRole('button', { name: 'Новый чат' })).toHaveProperty('disabled', true);
    expect(screen.queryByLabelText(/Агент Canon Keeper:/u)).toBeNull();
    expect(container.querySelector('footer[data-ephemeral]')).toBeNull();
    expect(screen.queryByRole('button', { name: 'Остановить' })).toBeNull();
  });

  it('submits on Enter, preserves Shift+Enter and ignores composing Enter', async () => {
    const api = createMockApiClient();
    const session = await api.createChatSession(projectId, {
      scope: 'project', sourceView: 'published', title: 'Клавиатура',
    }, { idempotencyKey: createIdempotencyKey('createChatSession') });
    const createTurn = vi.spyOn(api, 'createChatTurn');
    renderPanel(api, session.id);
    const composer = await screen.findByRole('textbox', { name: 'Вопрос' });
    fireEvent.change(composer, { target: { value: 'Вопрос из клавиатуры' } });
    fireEvent.change(composer, { target: { value: 'Строка' } });
    const shiftEnter = createEvent.keyDown(composer, { key: 'Enter', shiftKey: true });
    const shiftPrevented = vi.spyOn(shiftEnter, 'preventDefault');
    fireEvent(composer, shiftEnter);
    expect(shiftPrevented).not.toHaveBeenCalled();
    expect(createTurn).not.toHaveBeenCalled();

    const composingEnter = createEvent.keyDown(composer, { key: 'Enter', isComposing: true });
    const composingPrevented = vi.spyOn(composingEnter, 'preventDefault');
    fireEvent(composer, composingEnter);
    expect(composingPrevented).not.toHaveBeenCalled();
    expect(createTurn).not.toHaveBeenCalled();

    fireEvent.change(composer, { target: { value: 'Вопрос из клавиатуры' } });
    const enter = createEvent.keyDown(composer, { key: 'Enter' });
    const enterPrevented = vi.spyOn(enter, 'preventDefault');
    fireEvent(composer, enter);
    expect(enterPrevented).toHaveBeenCalledOnce();
    await waitFor(() => expect(createTurn).toHaveBeenCalledWith(
      session.id, { body: 'Вопрос из клавиатуры' }, expect.anything(),
    ));
  });

  it('keeps Enter inside the existing empty-message submit guard', async () => {
    const api = createMockApiClient();
    const session = await api.createChatSession(projectId, {
      scope: 'project', sourceView: 'published', title: 'Пустой ввод',
    }, { idempotencyKey: createIdempotencyKey('createChatSession') });
    const createTurn = vi.spyOn(api, 'createChatTurn');
    renderPanel(api, session.id);
    const composer = await screen.findByRole('textbox', { name: 'Вопрос' });

    fireEvent.keyDown(composer, { key: 'Enter' });
    expect(createTurn).not.toHaveBeenCalled();
  });

  it('honors the server Start capability even when workspace editing is still allowed', async () => {
    const api = createMockApiClient();
    const session = await api.createChatSession(projectId, {
      scope: 'project', sourceView: 'published', savedAssignment: 'Ожидающее поручение',
    }, { idempotencyKey: createIdempotencyKey('createChatSession') });
    vi.spyOn(api, 'getChatSession').mockResolvedValue({ ...session, savedAssignment: { body: 'Ожидающее поручение', availableActions: [] } });
    const createTurn = vi.spyOn(api, 'createChatTurn');
    renderPanel(api, session.id);
    await screen.findByRole('region', { name: 'Сохранённый вопрос' });
    const composer = screen.getByRole('textbox', { name: 'Вопрос' });

    expect(composer).toHaveProperty('disabled', true);
    expect(screen.queryByRole('button', { name: /^(Отправить|Спросить)$/u })).toBeNull();
    fireEvent.submit(composer.closest('form')!);
    fireEvent.keyDown(composer, { key: 'Enter' });
    expect(createTurn).not.toHaveBeenCalled();
  });

  it('persists the first question in the session when Job creation fails', async () => {
    const api = createMockApiClient();
    const createSession = vi.spyOn(api, 'createChatSession');
    vi.spyOn(api, 'createChatTurn').mockRejectedValue(new Error('connection lost'));
    renderPanel(api);
    fireEvent.change(await screen.findByRole('textbox', { name: 'Вопрос' }), { target: { value: '  Сохранить это поручение  ' } });
    await waitFor(() => expect(screen.getByRole('button', { name: 'Спросить' })).toHaveProperty('disabled', false));
    fireEvent.click(screen.getByRole('button', { name: 'Спросить' }));
    await screen.findByText('Вопрос не отправлен');

    expect(createSession).toHaveBeenCalledWith(projectId, expect.objectContaining({
      scope: 'project',
      savedAssignment: 'Сохранить это поручение',
    }), expect.anything());
    expect(createSession.mock.calls[0]?.[1]).not.toHaveProperty('bookId');
    const session = (await api.listChatSessions(projectId, {})).data[0]!;
    expect((await api.getChatSession(session.id)).savedAssignment?.body).toBe('Сохранить это поручение');
  });

  it('restores the assignment from the session and starts exactly one turn without a new session', async () => {
    const api = createMockApiClient();
    const session = await api.createChatSession(projectId, {
      scope: 'project', sourceView: 'published', savedAssignment: 'Восстановленное поручение',
    }, { idempotencyKey: createIdempotencyKey('createChatSession') });
    const createSession = vi.spyOn(api, 'createChatSession');
    const createTurn = vi.spyOn(api, 'createChatTurn');
    renderPanel(api, session.id);
    const saved = await screen.findByRole('region', { name: 'Сохранённый вопрос' });

    expect(within(saved).getByText('Восстановленное поручение')).toBeTruthy();
    const kicker = within(saved).getByText('ВОПРОС СОХРАНЁН · ЕЩЁ НЕ ОТПРАВЛЕН');
    expect(getComputedStyle(kicker).fontFamily).toBe('var(--ck-font-mono)');
    expect(getComputedStyle(kicker).fontSize).toBe('11px');
    expect(getComputedStyle(kicker).fontWeight).toBe('500');
    expect(getComputedStyle(kicker).letterSpacing).toBe('1.2px');
    expect(getComputedStyle(kicker).color).toBe('var(--ck-link)');
    expect(getComputedStyle(within(saved).getByText(/^Чат создан/u)).fontFamily).toBe('var(--ck-font-mono)');
    expect(within(saved).getByRole('button', { name: 'Новый чат' }).getAttribute('data-variant')).toBe('outline');
    expect(screen.getByRole('textbox', { name: 'Вопрос' })).toHaveProperty('value', 'Восстановленное поручение');
    expect(screen.queryByText('Задача запущена')).toBeNull();
    expect(within(saved).getByRole('button', { name: 'Отправить' }).querySelector('svg')?.classList.contains('lucide-play')).toBe(true);
    fireEvent.click(within(saved).getByRole('button', { name: 'Отправить' }));
    await waitFor(() => expect(createTurn).toHaveBeenCalledTimes(1));
    expect(createTurn).toHaveBeenCalledWith(session.id, { body: 'Восстановленное поручение' }, expect.anything());
    expect(createSession).not.toHaveBeenCalled();
  });

  it('uses a 145px minimum border box without fixing the height of a long saved task', async () => {
    const api = createMockApiClient();
    const body = `${'Длинное поручение с деталями. '.repeat(60)}\nВторая строка остаётся сохранённой.`;
    const session = await api.createChatSession(projectId, {
      scope: 'project', sourceView: 'published', savedAssignment: body,
    }, { idempotencyKey: createIdempotencyKey('createChatSession') });
    renderPanel(api, session.id);
    const saved = await screen.findByRole('region', { name: 'Сохранённый вопрос' });
    const style = getComputedStyle(saved);
    expect(style.boxSizing).toBe('border-box');
    expect(style.minHeight).toBe('145px');
    expect(style.padding).toBe('13px');
    expect(['', 'auto']).toContain(style.height);
    expect(['', 'none']).toContain(style.maxHeight);
    expect(saved.querySelector('p')?.textContent).toBe(body);
    expect(getComputedStyle(saved.querySelector('p')!).whiteSpace).toBe('pre-wrap');
  });

  it('keeps the assignment readable for a viewer without Start or composer', async () => {
    const api = createMockApiClient();
    const session = await api.createChatSession(projectId, {
      scope: 'project', sourceView: 'published', title: 'Сохранённый чат', savedAssignment: 'Поручение для просмотра',
    }, { idempotencyKey: createIdempotencyKey('createChatSession') });
    renderPanel(api, session.id, false);
    const saved = await screen.findByRole('region', { name: 'Сохранённый вопрос' });

    expect(within(saved).getByText('Поручение для просмотра')).toBeTruthy();
    expect(screen.queryByRole('button', { name: /^(Отправить|Спросить)$/u })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Новый чат' })).toBeNull();
    expect(screen.queryByRole('textbox', { name: 'Вопрос' })).toBeNull();
  });
});
