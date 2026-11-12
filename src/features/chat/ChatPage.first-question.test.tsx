import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { useCallback, useState } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../api/mock';
import { projectId } from '../../api/mock/repository/fixtures';
import { InShell } from '../../app/shell/AppShell.test-helpers';
import { createAppRoute, type RouteNavigate } from '../../shared/navigation/app-route';
import { ChatPage } from './ChatPage';

beforeEach(() => { vi.stubGlobal('innerWidth', 1440); vi.stubGlobal('innerHeight', 900); });
afterEach(() => vi.unstubAllGlobals());

/**
 * The chat in the one shell of the root with an address that follows it, as in the product (specs/2026-11-10-ux2-shell-core.md
 * SC1.1): the shell and the page read the saga of the same address, and choosing a chat writes the saga and the chat into it.
 */
function ChatWithAddress({ api, visited }: { api: ReturnType<typeof createMockApiClient>; visited: Array<Record<string, string | undefined>> }) {
  const [route, setRoute] = useState(() => createAppRoute('/chat', {}));
  const navigate = useCallback<RouteNavigate>((path, query = {}) => {
    visited.push(query);
    setRoute(createAppRoute(path, query));
  }, [visited]);
  return (
    <InShell api={api} navigate={navigate} route={route}>
      <ChatPage api={api} navigate={navigate} route={route} />
    </InShell>
  );
}

describe('ChatPage: the first question of a chat opened without a saga in the address', () => {
  // TEST, 11.11: «Спросить» on /chat created the chat and the turn, the address took the saga and the chat — and the
  // page went on saying «Вопрос сохранён · ещё не отправлен» with «Отправить», though the server was answering.
  it('goes on to its answer when the address takes the saga of the page', async () => {
    const api = createMockApiClient();
    let release = () => {};
    const gate = new Promise<void>((resolve) => { release = resolve; });
    const createTurn = api.createChatTurn.bind(api);
    const turn = vi.spyOn(api, 'createChatTurn').mockImplementation(async (...input) => {
      await gate;
      return createTurn(...input);
    });
    const visited: Array<Record<string, string | undefined>> = [];
    const queryClient = new QueryClient({ defaultOptions: { mutations: { retry: false }, queries: { retry: false } } });
    render(<QueryClientProvider client={queryClient}><ChatWithAddress api={api} visited={visited} /></QueryClientProvider>);

    const chat = await screen.findByRole('region', { name: 'Исследовательский чат' }, { timeout: 5000 });
    const content = document.querySelector('[data-page-content]');
    expect(content?.contains(chat)).toBe(true);
    fireEvent.change(screen.getByLabelText('Вопрос'), { target: { value: 'Кто остановил лампу маяка?' } });
    fireEvent.click(screen.getByRole('button', { name: /^Спросить/u }));
    await waitFor(() => expect(turn).toHaveBeenCalledTimes(1));
    // The address names the saga the page already showed, and the new chat.
    expect(visited.at(-1)).toMatchObject({ projectId });
    expect(visited.at(-1)?.chatId).toBeTruthy();
    // The content of the page stays mounted while the address changes: the same chat, in the same content of the shell.
    expect(screen.getByRole('region', { name: 'Исследовательский чат' })).toBe(chat);
    expect(document.querySelector('[data-page-content]')).toBe(content);
    release();

    await waitFor(() => expect((screen.getByLabelText(/^(Вопрос|Уточнение)$/u) as HTMLTextAreaElement).value).toBe(''));
    expect(screen.getByRole('region', { name: 'Исследовательский чат' })).toBe(chat);
    expect(screen.queryByRole('region', { name: 'Сохранённый вопрос' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Отправить' })).toBeNull();
    expect(turn).toHaveBeenCalledTimes(1);
  });
});
