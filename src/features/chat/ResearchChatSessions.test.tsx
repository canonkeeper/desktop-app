import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../api/mock';
import { projectId } from '../../api/mock/repository/fixtures';
import { createIdempotencyKey } from '../../shared/api';
import { ToastsProvider } from '../../ui/feedback/Toast';
import { forgetAllTurns } from './answer-ready';
import { ResearchChatPanel } from './ResearchChatPanel';

beforeEach(() => { vi.stubGlobal('innerWidth', 1440); vi.stubGlobal('innerHeight', 900); });
afterEach(() => {
  forgetAllTurns();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

type Api = ReturnType<typeof createMockApiClient>;

async function chat(api: Api, title: string) {
  return api.createChatSession(projectId, { scope: 'project', sourceView: 'published', title }, { idempotencyKey: createIdempotencyKey('createChatSession') });
}

function renderPanel(api: Api, selectedChatId: string) {
  const onSelectChatId = vi.fn();
  render(<QueryClientProvider client={new QueryClient({ defaultOptions: { mutations: { retry: false }, queries: { retry: false } } })}><ToastsProvider>
    <ResearchChatPanel api={api} canCreate onOpenSource={vi.fn()} onSelectChatId={onSelectChatId} projectId={projectId} selectedChatId={selectedChatId} sourceView="published" />
  </ToastsProvider></QueryClientProvider>);
  return onSelectChatId;
}

const chats = () => screen.getByRole('complementary', { name: 'Сохранённые чаты' });

// specs/2026-11-11-ux4-after-answer.md AA3: the list of chats finds the conversation of a week ago.
describe('the list of chats', () => {
  it('stands by days of the last question, with the date and «готовится» under each name', async () => {
    const api = createMockApiClient();
    const today = await chat(api, 'Кто такой Сава');
    const old = await chat(api, 'Сколько лет Маре');
    await api.createChatTurn(today.id, { body: 'Кто такой Сава?' }, { idempotencyKey: createIdempotencyKey('createChatTurn') });
    const list = api.listChatSessions.bind(api);
    vi.spyOn(api, 'listChatSessions').mockImplementation(async (...input) => {
      const page = await list(...input);
      return { ...page, data: page.data.map((item) => item.id === old.id ? { ...item, latestTurnAt: '2025-03-04T10:00:00Z' } : item) };
    });
    renderPanel(api, today.id);
    const todayGroup = await within(await screen.findByRole('complementary', { name: 'Сохранённые чаты' })).findByRole('region', { name: 'Сегодня' });
    const working = within(todayGroup).getByRole('button', { name: 'Кто такой Сава, готовится' });
    expect(working.getAttribute('title')).toBe('Кто такой Сава');
    expect(working.parentElement!.textContent).toMatch(/· готовится$/u);
    const earlier = within(chats()).getByRole('region', { name: 'Раньше' });
    expect(within(earlier).getByRole('button', { name: 'Сколько лет Маре' }).parentElement!.textContent).toContain('4 марта 2025');
  });

  it('finds a chat by its name from ten chats on and says when nothing was found', async () => {
    const api = createMockApiClient();
    for (let index = 1; index <= 9; index += 1) await chat(api, `Вопрос ${index}`);
    await chat(api, 'Пожар в Белом порту');
    renderPanel(api, '');
    const search = await screen.findByRole('searchbox', { name: 'Найти в чатах' });
    fireEvent.change(search, { target: { value: 'пожар' } });
    expect(within(chats()).getByRole('button', { name: 'Пожар в Белом порту' })).toBeTruthy();
    expect(within(chats()).queryByRole('button', { name: 'Вопрос 3' })).toBeNull();
    fireEvent.change(search, { target: { value: 'дракон' } });
    expect(within(chats()).getByText('Ничего не нашлось по «дракон»')).toBeTruthy();
    fireEvent.click(within(chats()).getByRole('button', { name: 'Сбросить поиск' }));
    expect(within(chats()).getByRole('button', { name: 'Вопрос 3' })).toBeTruthy();
  });

  it('has no search for fewer than ten chats', async () => {
    const api = createMockApiClient();
    for (let index = 1; index <= 9; index += 1) await chat(api, `Вопрос ${index}`);
    renderPanel(api, '');
    await within(await screen.findByRole('complementary', { name: 'Сохранённые чаты' })).findByRole('button', { name: 'Вопрос 9' });
    expect(screen.queryByRole('searchbox', { name: 'Найти в чатах' })).toBeNull();
  });

  it('renames a chat that is not open from its «⋯»: leaving the field keeps the name, Esc leaves the old one', async () => {
    const api = createMockApiClient();
    const open = await chat(api, 'Открытый');
    const other = await chat(api, 'Другой чат');
    const rename = vi.spyOn(api, 'renameChatSession');
    renderPanel(api, open.id);
    fireEvent.click(await screen.findByRole('button', { name: 'Действия чата «Другой чат»' }));
    fireEvent.click(within(await screen.findByRole('menu', { name: 'Действия чата «Другой чат»' })).getByRole('menuitem', { name: 'Переименовать' }));
    let field = screen.getByRole('textbox', { name: 'Название чата' });
    fireEvent.change(field, { target: { value: 'Не тот' } });
    fireEvent.keyDown(field, { key: 'Escape' });
    expect(screen.queryByRole('textbox', { name: 'Название чата' })).toBeNull();
    expect(rename).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole('button', { name: 'Действия чата «Другой чат»' }));
    fireEvent.click(within(await screen.findByRole('menu', { name: 'Действия чата «Другой чат»' })).getByRole('menuitem', { name: 'Переименовать' }));
    field = screen.getByRole('textbox', { name: 'Название чата' });
    fireEvent.change(field, { target: { value: 'Брод через реку' } });
    fireEvent.blur(field);
    await waitFor(() => expect(rename).toHaveBeenCalledWith(other.id, { title: 'Брод через реку' }));
    expect(await within(chats()).findByRole('button', { name: 'Брод через реку' })).toBeTruthy();
  });

  it('deletes a chat of the list that is not open and stays in the open one, saying which went', async () => {
    const api = createMockApiClient();
    const open = await chat(api, 'Открытый');
    await chat(api, 'Лишний');
    const select = renderPanel(api, open.id);
    fireEvent.click(await screen.findByRole('button', { name: 'Действия чата «Лишний»' }));
    fireEvent.click(within(await screen.findByRole('menu', { name: 'Действия чата «Лишний»' })).getByRole('menuitem', { name: 'Удалить чат…' }));
    const ask = await screen.findByRole('dialog', { name: 'Удалить чат «Лишний»?' });
    fireEvent.click(within(ask).getByRole('button', { name: 'Удалить чат' }));
    expect(await screen.findByText('Чат «Лишний» удалён.')).toBeTruthy();
    expect(select).not.toHaveBeenCalled();
    await waitFor(() => expect(within(chats()).queryByRole('button', { name: 'Лишний' })).toBeNull());
  });

  it('opens a new chat after the open one is deleted', async () => {
    const api = createMockApiClient();
    const open = await chat(api, 'Открытый');
    const select = renderPanel(api, open.id);
    fireEvent.click(await screen.findByRole('button', { name: 'Действия чата «Открытый»' }));
    fireEvent.click(within(await screen.findByRole('menu', { name: 'Действия чата «Открытый»' })).getByRole('menuitem', { name: 'Удалить чат…' }));
    fireEvent.click(within(await screen.findByRole('dialog', { name: 'Удалить чат «Открытый»?' })).getByRole('button', { name: 'Удалить чат' }));
    expect(await screen.findByText('Чат «Открытый» удалён.')).toBeTruthy();
    expect(select).toHaveBeenCalledWith('');
  });

  it('keeps «Удалить чат…» of a chat whose question is answered and says why', async () => {
    const api = createMockApiClient();
    const open = await chat(api, 'Открытый');
    const working = await chat(api, 'Готовится ответ');
    await api.createChatTurn(working.id, { body: 'Кто такой Сава?' }, { idempotencyKey: createIdempotencyKey('createChatTurn') });
    const remove = vi.spyOn(api, 'deleteChatSession');
    renderPanel(api, open.id);
    fireEvent.click(await screen.findByRole('button', { name: 'Действия чата «Готовится ответ»' }));
    const menu = await screen.findByRole('menu', { name: 'Действия чата «Готовится ответ»' });
    const item = within(menu).getByRole('menuitem', { name: 'Удалить чат…' });
    expect(item.getAttribute('aria-disabled')).toBe('true');
    fireEvent.click(item);
    expect(within(menu).getByText('Удалить чат можно, когда ответ будет готов или остановлен.')).toBeTruthy();
    expect(screen.queryByRole('dialog', { name: /Удалить чат/u })).toBeNull();
    expect(remove).not.toHaveBeenCalled();
  });
});
