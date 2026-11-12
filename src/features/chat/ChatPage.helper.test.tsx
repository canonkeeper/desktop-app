import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { useCallback, useState } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../api/mock';
import { projectId } from '../../api/mock/repository/fixtures';
import { InShell } from '../../app/shell/AppShell.test-helpers';
import { createAppRoute, type RouteNavigate } from '../../shared/navigation/app-route';
import { reasonOf } from '../../test/disabled-reason';
import { ChatPage } from './ChatPage';

beforeEach(() => { vi.stubGlobal('innerWidth', 1440); vi.stubGlobal('innerHeight', 900); });
afterEach(() => vi.unstubAllGlobals());

const line = 'Помощник ещё не подключён. Читать книги и искать по ним можно уже сейчас';

function ChatWithAddress({ api }: { api: ReturnType<typeof createMockApiClient> }) {
  const [route, setRoute] = useState(() => createAppRoute('/chat', { projectId, new: '1' }));
  const navigate = useCallback<RouteNavigate>((path, query = {}) => setRoute(createAppRoute(path, query)), []);
  return (
    <InShell api={api} navigate={navigate} route={route}>
      <ChatPage api={api} navigate={navigate} route={route} />
    </InShell>
  );
}

// specs/2026-11-12-desk-window.md WN3.2, WN3.3: in a build whose helper is not connected the chat says it in the one line of
// the reading, a question put into the field stays there, and nothing is sent — no chat and no question is made.
describe('ChatPage without the helper', () => {
  it('says the helper is not connected and sends no question', async () => {
    const api = createMockApiClient();
    api.setScenario({ actorRole: 'owner', preset: 'normal', helperMissing: true });
    const createChat = vi.spyOn(api, 'createChatSession');
    const createTurn = vi.spyOn(api, 'createChatTurn');
    const queryClient = new QueryClient({ defaultOptions: { mutations: { retry: false }, queries: { retry: false } } });
    render(<QueryClientProvider client={queryClient}><ChatWithAddress api={api} /></QueryClientProvider>);

    const chat = await screen.findByRole('region', { name: 'Исследовательский чат' }, { timeout: 5000 });
    expect((await within(chat).findByRole('note', { name: 'Помощник ещё не подключён' })).textContent).toBe(line);
    expect(within(chat).queryByText(/Помощник (ещё )?читает/u)).toBeNull();

    // A first question of the saga goes into the field as before.
    const field = within(chat).getByRole('textbox', { name: 'Вопрос' }) as HTMLTextAreaElement;
    const first = await within(chat).findByRole('button', { name: /кто это и что важного происходит/u });
    fireEvent.click(first);
    expect(field.value).toBe(first.textContent);

    fireEvent.change(field, { target: { value: 'Кто остановил лампу маяка?' } });
    const ask = within(chat).getByRole('button', { name: 'Спросить' });
    expect(ask.getAttribute('aria-disabled')).toBe('true');
    expect(reasonOf(ask)).toBe(line);
    fireEvent.click(ask);
    fireEvent.keyDown(field, { key: 'Enter' });
    expect(field.value).toBe('Кто остановил лампу маяка?');
    expect(createChat).not.toHaveBeenCalled();
    expect(createTurn).not.toHaveBeenCalled();
  });
});
