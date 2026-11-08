import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../api/mock';
import { projectId } from '../../api/mock/repository/fixtures';
import { ApiStatusError } from '../../shared/api';
import { ResearchChatPanel } from './ResearchChatPanel';

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
    })}>
      <ResearchChatPanel
        api={api}
        canCreate
        onOpenSource={vi.fn()}
        onSelectChatId={onSelectChatId}
        projectId={projectId}
        selectedChatId={selectedChatId}
        sourceView="published"
      />
    </QueryClientProvider>,
  );
  return onSelectChatId;
}

async function openDelete(title: string) {
  fireEvent.click(await screen.findByRole('button', { name: `Действия чата «${title}»` }));
  const menu = await screen.findByRole('menu', { name: `Действия чата «${title}»` });
  fireEvent.click(within(menu).getByRole('menuitem', { name: 'Удалить чат' }));
  return screen.findByRole('dialog', { name: 'Удалить чат' });
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
    const panel = (chatId: string) => <QueryClientProvider client={client}>
      <ResearchChatPanel api={api} canCreate onOpenSource={vi.fn()} onSelectChatId={onSelect}
        projectId={projectId} selectedChatId={chatId} sourceView="published" />
    </QueryClientProvider>;
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

    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Удалить чат' })).toBeNull());
    // The notice keeps its code in the letter it offers (R4.2.1).
    const notice = await screen.findByRole('alert');
    expect(notice.textContent).not.toContain('Код для поддержки');
    expect(decodeURIComponent(within(notice).getByRole('link', { name: 'Написать в поддержку' }).getAttribute('href') ?? ''))
      .toContain('Canon Keeper: ошибка delete-403');
    expect(screen.queryByRole('textbox', { name: 'Вопрос' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Новый чат' })).toBeNull();
    expect(screen.getByRole('button', { name: selected.title })).toBeTruthy();
  });

  it('uses a mobile sheet and blocks dismissal while deletion is pending', async () => {
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
    const deleteButton = await screen.findByRole('button', { name: 'Удалить чат' });
    const composer = screen.getByRole('textbox', { name: 'Вопрос' });
    expect(deleteButton.compareDocumentPosition(composer) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(getComputedStyle(deleteButton).backgroundColor).toBe('rgba(0, 0, 0, 0)');
    expect(getComputedStyle(deleteButton).color).toBe('var(--ck-destructive-text)');
    fireEvent.click(deleteButton);
    const sheet = await screen.findByRole('dialog', { name: 'Удалить чат' });

    expect(sheet.getAttribute('data-kind')).toBe('sheet');
    fireEvent.click(within(sheet).getByRole('button', { name: 'Удалить чат' }));
    await waitFor(() => expect(within(sheet).getByRole('button', { name: 'Отмена' })).toHaveProperty('disabled', true));
    fireEvent.keyDown(sheet, { key: 'Escape' });
    fireEvent.pointerDown(sheet.parentElement!);
    expect(screen.getByRole('dialog', { name: 'Удалить чат' })).toBe(sheet);
    expect(api.deleteChatSession).toHaveBeenCalledTimes(1);
  });

  it('names the selected chat and opens the next session after success', async () => {
    const api = createMockApiClient();
    const selected = await createChat(api, 'Удаляемый чат');
    const next = await createChat(api, 'Следующий чат');
    const onSelectChatId = renderPanel(api, selected.id);
    const dialog = await openDelete(selected.title);

    expect(within(dialog).getByRole('heading').textContent).toContain('Удаляемый чат');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Удалить чат' }));

    await waitFor(() => expect(onSelectChatId).toHaveBeenCalledWith(next.id));
    expect(await screen.findByText(/Чат удалён/u)).toBeTruthy();
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
    expect(decodeURIComponent(within(dialog).getByRole('link', { name: 'Написать в поддержку' }).getAttribute('href') ?? ''))
      .toContain('Canon Keeper: ошибка delete-503');
    expect(within(dialog).getByRole('button', { name: 'Повторить' })).toBeTruthy();
    expect(await screen.findByRole('button', { name: selected.title })).toBeTruthy();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Повторить' }));
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
    expect(await screen.findByText(/Чат уже удалён/u)).toBeTruthy();
    expect(screen.queryByText(/Не удалось удалить чат/u)).toBeNull();
  });
});
