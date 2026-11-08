import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { useState } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../api/mock';
import { projectId } from '../../api/mock/repository/fixtures';
import { ApiStatusError, createIdempotencyKey } from '../../shared/api';
import { ResearchChatPanel } from './ResearchChatPanel';

afterEach(() => vi.unstubAllGlobals());
beforeEach(() => { vi.stubGlobal('innerWidth', 1440); vi.stubGlobal('innerHeight', 900); });

async function chat(api: ReturnType<typeof createMockApiClient>, title = 'Пожар в Белом порту') {
  return api.createChatSession(projectId, {
    scope: 'project', sourceView: 'published', title,
  }, { idempotencyKey: createIdempotencyKey('createChatSession') });
}

function renderPanel(
  api: ReturnType<typeof createMockApiClient>,
  selectedChatId: string,
  canCreate = true,
) {
  const onSelectChatId = vi.fn();
  const client = new QueryClient({ defaultOptions: { mutations: { retry: false }, queries: { retry: false } } });
  function SelectedChat() {
    const [chatId, setChatId] = useState(selectedChatId);
    return <QueryClientProvider client={client}>
      <ResearchChatPanel
        api={api}
        canCreate={canCreate}
        onOpenSource={vi.fn()}
        onSelectChatId={(nextChatId) => { onSelectChatId(nextChatId); setChatId(nextChatId); }}
        projectId={projectId}
        selectedChatId={chatId}
        sourceView="published"
      />
    </QueryClientProvider>;
  }
  render(<SelectedChat />);
  return onSelectChatId;
}

async function beginRename(title: string) {
  fireEvent.click(await screen.findByRole('button', { name: `Действия чата «${title}»` }));
  const menu = await screen.findByRole('menu', { name: `Действия чата «${title}»` });
  fireEvent.click(within(menu).getByRole('menuitem', { name: 'Переименовать' }));
  return screen.getByRole('textbox', { name: 'Название чата' });
}

describe('Research Chat rename', () => {
  it('keeps the new selection when a pending rename of the previous chat later returns 409', async () => {
    const api = createMockApiClient();
    const first = await chat(api, 'Первый чат');
    const second = await chat(api, 'Выбранный позже чат');
    let rejectRename!: (reason: unknown) => void;
    const rename = vi.spyOn(api, 'renameChatSession').mockImplementation(() => new Promise((_resolve, reject) => { rejectRename = reject; }));
    const onSelectChatId = renderPanel(api, first.id);
    const input = await beginRename(first.title);
    fireEvent.change(input, { target: { value: 'Новое название' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    await waitFor(() => expect(rename).toHaveBeenCalledOnce());
    fireEvent.click(screen.getByRole('button', { name: second.title }));
    await waitFor(() => expect(screen.getByRole('button', { name: second.title }).getAttribute('aria-current')).toBe('page'));
    await act(async () => rejectRename(new ApiStatusError(409, {
      type: 'about:blank', status: 409, title: 'Conflict', code: 'conflict', requestId: 'rename-stale-selection',
    }, new Response(null, { status: 409 }))));

    await screen.findByText(/Чат больше недоступен/u);
    expect(onSelectChatId).not.toHaveBeenCalledWith('');
    expect(screen.getByRole('button', { name: second.title }).getAttribute('aria-current')).toBe('page');
  });

  it('normalizes once and replaces the active row only with the returned ChatSession', async () => {
    const api = createMockApiClient();
    const session = await chat(api);
    const rename = vi.spyOn(api, 'renameChatSession');
    renderPanel(api, session.id);
    const input = await beginRename(session.title);

    fireEvent.change(input, { target: { value: '  Белая гавань 🔥  ' } });
    fireEvent.keyDown(input, { key: 'Enter' });

    expect(await screen.findByRole('button', { name: 'Белая гавань 🔥' })).toBeTruthy();
    expect(rename).toHaveBeenCalledOnce();
    expect(rename).toHaveBeenCalledWith(session.id, { title: 'Белая гавань 🔥' });
    expect(screen.queryByRole('heading', { name: 'Белая гавань 🔥' })).toBeNull();
  });

  it('validates Unicode length before PATCH and Escape cancels without a request', async () => {
    const api = createMockApiClient();
    const session = await chat(api);
    const rename = vi.spyOn(api, 'renameChatSession');
    renderPanel(api, session.id);
    let input = await beginRename(session.title);

    fireEvent.change(input, { target: { value: '   ' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    expect(await screen.findByRole('alert')).toHaveProperty('textContent', 'Название не может быть пустым.');
    expect(rename).not.toHaveBeenCalled();

    fireEvent.change(input, { target: { value: '🔥'.repeat(121) } });
    fireEvent.keyDown(input, { key: 'Enter' });
    expect(await screen.findByText('Название не может быть длиннее 120 символов.')).toBeTruthy();
    expect(rename).not.toHaveBeenCalled();

    fireEvent.keyDown(input, { key: 'Escape' });
    expect(screen.queryByRole('textbox', { name: 'Название чата' })).toBeNull();
    input = await beginRename(session.title);
    fireEvent.blur(input);
    expect(rename).not.toHaveBeenCalled();
  });

  it('keeps a 422 server error and the draft inline', async () => {
    const api = createMockApiClient();
    const session = await chat(api);
    vi.spyOn(api, 'renameChatSession').mockRejectedValue(new ApiStatusError(422, {
      code: 'title_too_long', detail: 'Too long.', requestId: 'rename-422', status: 422,
      title: 'Validation failed', type: 'about:blank',
    }, new Response(null, { status: 422 })));
    renderPanel(api, session.id);
    const input = await beginRename(session.title);
    fireEvent.change(input, { target: { value: 'Сервер отклонил это название' } });

    fireEvent.keyDown(input, { key: 'Enter' });

    expect(await screen.findByText('Название не может быть длиннее 120 символов.')).toBeTruthy();
    expect((screen.getByRole('textbox', { name: 'Название чата' }) as HTMLInputElement).value)
      .toBe('Сервер отклонил это название');
  });

  it('removes mutation controls after an authoritative 403', async () => {
    const api = createMockApiClient();
    const session = await chat(api);
    vi.spyOn(api, 'renameChatSession').mockRejectedValue(new ApiStatusError(403, {
      code: 'forbidden', detail: 'Forbidden.', requestId: 'rename-403', status: 403,
      title: 'Forbidden', type: 'about:blank',
    }, new Response(null, { status: 403 })));
    renderPanel(api, session.id);
    const input = await beginRename(session.title);
    fireEvent.change(input, { target: { value: 'Новое название' } });
    fireEvent.keyDown(input, { key: 'Enter' });

    await waitFor(() => expect(screen.queryByRole('button', { name: `Действия чата «${session.title}»` })).toBeNull());
    expect(screen.queryByRole('textbox', { name: 'Вопрос' })).toBeNull();
    expect(await screen.findByRole('button', { name: session.title })).toBeTruthy();
  });

  it.each([404, 409])('removes a vanished row and shows a neutral toast after %s', async (status) => {
    const api = createMockApiClient();
    const session = await chat(api);
    const remove = api.deleteChatSession.bind(api);
    vi.spyOn(api, 'renameChatSession').mockImplementation(async () => {
      await remove(session.id);
      throw new ApiStatusError(status, {
        code: status === 404 ? 'not_found' : 'conflict',
        detail: 'Chat vanished.',
        requestId: `rename-${status}`,
        status,
        title: 'Chat vanished',
        type: 'about:blank',
      }, new Response(null, { status }));
    });
    const onSelectChatId = renderPanel(api, session.id);
    const input = await beginRename(session.title);
    fireEvent.change(input, { target: { value: 'Новое название' } });
    fireEvent.keyDown(input, { key: 'Enter' });

    await waitFor(() => expect(onSelectChatId).toHaveBeenCalledWith(''));
    expect(screen.queryByRole('button', { name: session.title })).toBeNull();
    expect(await screen.findByText(/Чат больше недоступен/u)).toBeTruthy();
  });

  it('blocks duplicate Enter while pending and preserves the draft on a recoverable failure', async () => {
    const api = createMockApiClient();
    const session = await chat(api);
    const rename = api.renameChatSession.bind(api);
    let release = () => {};
    const gate = new Promise<void>((resolve) => { release = resolve; });
    const request = vi.spyOn(api, 'renameChatSession').mockImplementation(async (...args) => {
      await gate;
      return rename(...args);
    });
    renderPanel(api, session.id);
    let input = await beginRename(session.title);
    fireEvent.change(input, { target: { value: 'Один запрос' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    fireEvent.keyDown(input, { key: 'Enter' });

    await waitFor(() => expect(request).toHaveBeenCalledOnce());
    expect(input).toHaveProperty('disabled', true);
    await act(async () => release());
    expect(await screen.findByRole('button', { name: 'Один запрос' })).toBeTruthy();

    request.mockRejectedValue(new ApiStatusError(503, {
      code: 'service_unavailable', detail: 'Unavailable.', requestId: 'rename-503', status: 503,
      title: 'Unavailable', type: 'about:blank',
    }, new Response(null, { status: 503 })));
    input = await beginRename('Один запрос');
    fireEvent.change(input, { target: { value: 'Черновик сохраняется' } });
    fireEvent.keyDown(input, { key: 'Enter' });

    expect(await screen.findByText(/Сервис временно недоступен/u)).toBeTruthy();
    expect((screen.getByRole('textbox', { name: 'Название чата' }) as HTMLInputElement).value)
      .toBe('Черновик сохраняется');
  });

  it('uses an Action Sheet for a coarse pointer', async () => {
    vi.stubGlobal('matchMedia', vi.fn().mockReturnValue({
      addEventListener: vi.fn(),
      matches: true,
      removeEventListener: vi.fn(),
    }));
    const api = createMockApiClient();
    const session = await chat(api);
    renderPanel(api, session.id);
    const trigger = await screen.findByRole('button', { name: `Действия чата «${session.title}»` });
    await waitFor(() => expect(trigger.getAttribute('data-size')).toBe('touch'));

    fireEvent.click(trigger);

    const sheet = await screen.findByRole('dialog', { name: `Действия чата «${session.title}»` });
    expect(screen.queryByRole('menu', { name: `Действия чата «${session.title}»` })).toBeNull();
    fireEvent.click(within(sheet).getByRole('button', { name: 'Переименовать' }));
    expect(screen.getByRole('textbox', { name: 'Название чата' })).toBeTruthy();
  });

  it('opens one history drawer on mobile and closes it after choosing a chat', async () => {
    vi.stubGlobal('innerWidth', 390);
    vi.stubGlobal('matchMedia', vi.fn((query: string) => ({
      addEventListener: vi.fn(),
      matches: query.includes('max-width') || query.includes('pointer: coarse'),
      media: query,
      removeEventListener: vi.fn(),
    })));
    const api = createMockApiClient();
    const current = await chat(api, 'Текущий чат');
    const other = await chat(api, 'Другой чат');
    await chat(api, 'Ещё один чат');
    const onSelectChatId = renderPanel(api, current.id);

    expect(screen.queryByRole('button', { name: other.title })).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Чаты' }));

    const drawer = await screen.findByRole('dialog', { name: 'Чаты' });
    expect(await within(drawer).findByRole('button', { name: current.title })).toBeTruthy();
    expect(within(drawer).getByRole('button', { name: 'Новый чат' })).toBeTruthy();
    fireEvent.click(await within(drawer).findByRole('button', { name: other.title }));
    await waitFor(() => expect(onSelectChatId).toHaveBeenCalledWith(other.id));
    expect(screen.queryByRole('dialog', { name: 'Чаты' })).toBeNull();
  });
});
