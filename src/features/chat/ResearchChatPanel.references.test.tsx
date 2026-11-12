import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../api/mock';
import { projectId } from '../../api/mock/repository/fixtures';
import { createIdempotencyKey, type AgentTimelineEvent, type SourceView } from '../../shared/api';
import { createAppRoute, toHref } from '../../shared/navigation/app-route';
import { FakeWebSocket } from '../../test/FakeWebSocket';
import { ChatPage } from './ChatPage';
import { ToastsProvider } from '../../ui/feedback/Toast';

beforeEach(() => { FakeWebSocket.reset(); vi.stubGlobal('WebSocket', FakeWebSocket); });
afterEach(() => vi.unstubAllGlobals());

async function renderAnswer(text: string, sourceView: SourceView = 'published', outdated = false) {
  const api = createMockApiClient();
  const session = await api.createChatSession(projectId, {
    scope: 'project', sourceView, title: 'Чат с источниками',
  }, { idempotencyKey: createIdempotencyKey('createChatSession') });
  const turn = await api.createChatTurn(session.id, { body: 'Найти источник' }, { idempotencyKey: createIdempotencyKey('createChatTurn') });
  const events: AgentTimelineEvent[] = [
    // The server names the chapters the answer cites beside their keys (specs/2026-11-10-ux1-names.md N2.1).
    { id: 'assistant-cited', cursor: 'tl_1', kind: 'assistant', role: 'main', logicalSection: 0, jobId: turn.jobId, providerTurnId: 'turn-1', phase: 'final_answer', text,
      ...(text.includes('source:b2c12') ? { places: { b2c12: 'Книга 2, гл. 12 „Белый порт“' } } : {}) },
  ];
  vi.spyOn(api, 'getAgentTimeline').mockResolvedValue({ events, eof: true, needsReconcile: false });
  if (outdated) {
    const getChatSession = api.getChatSession.bind(api);
    vi.spyOn(api, 'getChatSession').mockImplementation(async (chatId) => ({ ...(await getChatSession(chatId)), outdated: true }));
  }
  const navigate = vi.fn();
  render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><ToastsProvider>
    <ChatPage api={api} navigate={navigate}
      route={createAppRoute('/chat', { projectId, chatId: session.id, sourceView })} />
  </ToastsProvider></QueryClientProvider>);
  return { navigate, chatId: session.id };
}

describe('Research Chat citations', () => {
  it.each(['draft', 'published'] as const)('opens a cited %s fragment beside the chat', async (sourceView) => {
    const { navigate, chatId } = await renderAnswer('Мара заметила дым первой [«Белый порт», ¶3](source:b2c12/3).', sourceView);
    const link = await screen.findByRole('button', { name: 'Книга 2, гл. 12 „Белый порт“, абзац 3' });
    fireEvent.click(link);
    expect(navigate).toHaveBeenLastCalledWith(
      '/chat',
      { projectId, chatId, source: 'b2c12/3', sourceMode: undefined },
      { replace: false, sourceReturnHref: toHref('/chat', { projectId, chatId }) },
    );
  });

  it('shows a citation of the previous assistant format as an unavailable source', async () => {
    const { navigate } = await renderAnswer('Старый ответ [Гл. 12](source:s7/p8-p10).');
    expect(await screen.findByTitle('Место недоступно')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Гл. 12' })).toBeNull();
    expect(navigate).not.toHaveBeenCalled();
  });

  it('keeps an outdated chat readable and offers a new chat instead of the composer', async () => {
    await renderAnswer('Ответ прежнего помощника.', 'published', true);
    expect(await screen.findByText('Ответ прежнего помощника.')).toBeTruthy();
    expect(await screen.findByText(/вёл прежний помощник/u)).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Новый чат' })).toBeTruthy();
    expect(screen.queryByRole('textbox', { name: /Поручение|Сообщение|Вопрос/u })).toBeNull();
  });
});
