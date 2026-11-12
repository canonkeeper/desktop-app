import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../api/mock';
import { projectId } from '../../api/mock/repository/fixtures';
import { ApiStatusError } from '../../shared/api';
import { ResearchChatPanel } from './ResearchChatPanel';
import { ToastsProvider } from '../../ui/feedback/Toast';

afterEach(() => vi.unstubAllGlobals());
beforeEach(() => { vi.stubGlobal('innerWidth', 1440); vi.stubGlobal('innerHeight', 900); });

async function createChat(api: ReturnType<typeof createMockApiClient>, title: string) {
  return api.createChatSession(projectId, {
    scope: 'project', sourceView: 'published', title,
  }, { idempotencyKey: `idem-delete-${title}` });
}

function renderPanel(api: ReturnType<typeof createMockApiClient>, selectedChatId: string) {
  const onSelectChatId = vi.fn();
  render(
    <QueryClientProvider client={new QueryClient({
      defaultOptions: { mutations: { retry: false }, queries: { retry: false } },
    })}><ToastsProvider>
      <ResearchChatPanel
        api={api}
        canCreate
        onOpenSource={vi.fn()}
        onSelectChatId={onSelectChatId}
        projectId={projectId}
        selectedChatId={selectedChatId}
        sourceView="published"
      />
    </ToastsProvider></QueryClientProvider>,
  );
  return onSelectChatId;
}

async function openDelete(title: string) {
  fireEvent.click(await screen.findByRole('button', { name: `Действия чата «${title}»` }));
  const menu = await screen.findByRole('menu', { name: `Действия чата «${title}»` });
  fireEvent.click(within(menu).getByRole('menuitem', { name: 'Удалить чат…' }));
  return screen.findByRole('dialog', { name: `Удалить чат «${title}»?` });
}

function problem(status: number) {
  return new ApiStatusError(status, {
    code: status === 404 ? 'not_found' : 'service_unavailable',
    detail: 'Delete failed.',
    requestId: `delete-${status}`,
    status,
    title: 'Delete failed',
    type: 'about:blank',
  }, new Response(null, { status }));
}

describe('Research Chat delete', () => {
  it('does not navigate after the user leaves a chat while its deletion is pending', async () => {
    const api = createMockApiClient();
    const first = await createChat(api, 'Удаляемый первый чат');
    await createChat(api, 'Соседний чат');
    const selected = await createChat(api, 'Открытый через историю');
    let release!: () => void;
    const gate = new Promise<void>((resolve) => { release = resolve; });
    const remove = api.deleteChatSession.bind(api);
    const deletion = vi.spyOn(api, 'deleteChatSession').mockImplementation(async (id) => { await gate; await remove(id); });
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const onSelect = vi.fn();
    const panel = (chatId: string) => <QueryClientProvider client={client}><ToastsProvider>
      <ResearchChatPanel api={api} canCreate onOpenSource={vi.fn()} onSelectChatId={onSelect}
        projectId={projectId} selectedChatId={chatId} sourceView="published" />
    </ToastsProvider></QueryClientProvider>;
    const view = render(panel(first.id));
    const dialog = await openDelete(first.title);
    fireEvent.click(within(dialog).getByRole('button', { name: 'Удалить чат' }));
    await waitFor(() => expect(deletion).toHaveBeenCalledOnce());
    view.rerender(panel(selected.id));
    await act(async () => { release(); await deletion.mock.results[0]!.value; });

    expect(onSelect).not.toHaveBeenCalled();
    expect(screen.getByRole('button', { name: selected.title }).getAttribute('aria-current')).toBe('page');
  });

  it('closes deletion and removes mutation controls after authority is lost', async () => {
    const api = createMockApiClient();
    const selected = await createChat(api, 'История после потери прав');
    vi.spyOn(api, 'deleteChatSession').mockRejectedValue(problem(403));
    renderPanel(api, selected.id);
    const dialog = await openDelete(selected.title);
    fireEvent.click(within(dialog).getByRole('button', { name: 'Удалить чат' }));

    await waitFor(() => expect(screen.queryByRole('dialog', { name: /Удалить чат/u })).toBeNull());
    // The notice keeps its code in the letter it offers (R4.2.1).
    const notice = await screen.findByRole('alert');
    expect(notice.textContent).not.toContain('Код для поддержки');
    expect(decodeURIComponent(within(notice).getByRole('link', { name: 'Написать нам' }).getAttribute('href') ?? ''))
      .toContain('CanonKeeper: ошибка delete-403');
    expect(screen.queryByRole('textbox', { name: 'Вопрос' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Новый чат' })).toBeNull();
    expect(screen.getByRole('button', { name: selected.title })).toBeTruthy();
  });

  // specs/2026-11-10-ux1-chat.md CH2: deleting a chat is an action of the chat, never of the question field.
  it('reaches the deletion of the open chat on a phone from «⋯» over the feed, not from the field', async () => {
    vi.stubGlobal('innerWidth', 390);
    vi.stubGlobal('matchMedia', vi.fn((query: string) => ({
      matches: query.includes('max-width') || query.includes('pointer: coarse'),
      media: query,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    })));
    const api = createMockApiClient();
    const selected = await createChat(api, 'Мобильный чат');
    vi.spyOn(api, 'deleteChatSession').mockImplementation(() => new Promise(() => {}));
    renderPanel(api, selected.id);
    await screen.findByRole('textbox', { name: 'Вопрос' });
    expect(screen.queryByRole('button', { name: 'Удалить чат' })).toBeNull();
    // The chat list is hidden behind «Чаты»: the actions of the open chat stand beside it.
    expect(screen.queryByRole('complementary', { name: 'Сохранённые чаты' })).toBeNull();
    fireEvent.click(await screen.findByRole('button', { name: 'Действия чата «Мобильный чат»' }));
    const actions = await screen.findByRole('dialog', { name: 'Действия чата «Мобильный чат»' });
    expect(within(actions).getByRole('button', { name: 'Переименовать' })).toBeTruthy();
    fireEvent.click(within(actions).getByRole('button', { name: 'Удалить чат…' }));
    const sheet = await screen.findByRole('dialog', { name: 'Удалить чат «Мобильный чат»?' });

    expect(sheet.getAttribute('data-kind')).toBe('sheet');
    expect(sheet.textContent).toContain('Вернуть будет нельзя.');
    expect(sheet.textContent).not.toMatch(/диалог/iu);
    fireEvent.click(within(sheet).getByRole('button', { name: 'Удалить чат' }));
    await waitFor(() => expect(within(sheet).getByRole('button', { name: 'Отмена' })).toHaveProperty('disabled', true));
    fireEvent.keyDown(sheet, { key: 'Escape' });
    fireEvent.pointerDown(sheet.parentElement!);
    expect(screen.getByRole('dialog', { name: 'Удалить чат «Мобильный чат»?' })).toBe(sheet);
    expect(api.deleteChatSession).toHaveBeenCalledTimes(1);
  });

  it('opens the name of the open chat for a change from «⋯» over the feed', async () => {
    vi.stubGlobal('innerWidth', 1024);
    const api = createMockApiClient();
    const selected = await createChat(api, 'Чат для названия');
    renderPanel(api, selected.id);
    fireEvent.click(await screen.findByRole('button', { name: 'Действия чата «Чат для названия»' }));
    const menu = await screen.findByRole('menu', { name: 'Действия чата «Чат для названия»' });
    fireEvent.click(within(menu).getByRole('menuitem', { name: 'Переименовать' }));
    const chats = await screen.findByRole('dialog', { name: 'Чаты' });
    expect(await within(chats).findByRole('textbox', { name: 'Название чата' })).toHaveProperty('value', 'Чат для названия');
  });

  it('has no «Удалить чат» at the question field on a wide screen: it is in the actions of the chat row', async () => {
    const api = createMockApiClient();
    const selected = await createChat(api, 'Широкий экран');
    renderPanel(api, selected.id);
    await screen.findByRole('textbox', { name: 'Вопрос' });
    expect(screen.queryByRole('button', { name: 'Удалить чат' })).toBeNull();
    // The list is on the screen: one way to the actions, at the row of the chat.
    expect(await screen.findAllByRole('button', { name: 'Действия чата «Широкий экран»' })).toHaveLength(1);
    const dialog = await openDelete(selected.title);
    expect(dialog.getAttribute('data-kind')).not.toBe('sheet');
    expect(dialog.textContent).toContain('Вернуть будет нельзя.');
  });

  // CH2.3: the confirmation belongs to the panel, so a chat that has no question field is deleted too.
  it('deletes a chat of a previous assistant, which has no question field', async () => {
    const api = createMockApiClient();
    const selected = await createChat(api, 'Чат прежнего помощника');
    const getChatSession = api.getChatSession.bind(api);
    vi.spyOn(api, 'getChatSession').mockImplementation(async (chatId) => ({ ...(await getChatSession(chatId)), outdated: true }));
    const onSelectChatId = renderPanel(api, selected.id);
    expect(await screen.findByRole('alert', { name: 'Чат прежнего помощника' })).toBeTruthy();
    expect(screen.queryByRole('textbox', { name: 'Вопрос' })).toBeNull();
    const dialog = await openDelete(selected.title);
    fireEvent.click(within(dialog).getByRole('button', { name: 'Удалить чат' }));
    await waitFor(() => expect(onSelectChatId).toHaveBeenCalledWith(''));
  });

  // specs/2026-11-10-ux2-patterns-actions.md PA3.3: after the deletion — a new chat and the line that names the chat gone.
  it('names the selected chat and opens a new chat after success', async () => {
    const api = createMockApiClient();
    const selected = await createChat(api, 'Удаляемый чат');
    await createChat(api, 'Следующий чат');
    const onSelectChatId = renderPanel(api, selected.id);
    const dialog = await openDelete(selected.title);

    expect(within(dialog).getByRole('heading').textContent).toContain('Удаляемый чат');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Удалить чат' }));

    await waitFor(() => expect(onSelectChatId).toHaveBeenCalledWith(''));
    expect(await screen.findByText('Чат «Удаляемый чат» удалён.')).toBeTruthy();
  });

  it('keeps a recoverable failure in the dialog with Retry', async () => {
    const api = createMockApiClient();
    const selected = await createChat(api, 'Сохраняемый чат');
    vi.spyOn(api, 'deleteChatSession').mockRejectedValueOnce(problem(503));
    const onSelectChatId = renderPanel(api, selected.id);
    const dialog = await openDelete(selected.title);
    fireEvent.click(within(dialog).getByRole('button', { name: 'Удалить чат' }));

    expect(await within(dialog).findByText(/Не удалось удалить чат/u)).toBeTruthy();
    expect(within(dialog).getByText('delete-503').closest('span')?.textContent).toBe('Код для поддержки: delete-503');
    expect(decodeURIComponent(within(dialog).getByRole('link', { name: 'Написать нам' }).getAttribute('href') ?? ''))
      .toContain('CanonKeeper: ошибка delete-503');
    // The action of the window tries again (PA2.1).
    expect(await screen.findByRole('button', { name: selected.title })).toBeTruthy();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Удалить чат' }));
    await waitFor(() => expect(onSelectChatId).toHaveBeenCalledWith(''));
  });

  it('treats an authoritative 404 as already deleted', async () => {
    const api = createMockApiClient();
    const selected = await createChat(api, 'Уже удалённый чат');
    const remove = api.deleteChatSession.bind(api);
    vi.spyOn(api, 'deleteChatSession').mockImplementation(async (chatId) => {
      await remove(chatId);
      throw problem(404);
    });
    const onSelectChatId = renderPanel(api, selected.id);
    const dialog = await openDelete(selected.title);
    fireEvent.click(within(dialog).getByRole('button', { name: 'Удалить чат' }));

    await waitFor(() => expect(onSelectChatId).toHaveBeenCalledWith(''));
    expect(await screen.findByText('Чат «Уже удалённый чат» уже удалён.')).toBeTruthy();
    expect(screen.queryByText(/Не удалось удалить чат/u)).toBeNull();
  });
});
