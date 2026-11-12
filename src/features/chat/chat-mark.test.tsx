import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, renderHook, waitFor } from '@testing-library/react';
import type { ReactNode } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../api/mock';
import { projectId } from '../../api/mock/repository/fixtures';
import { createIdempotencyKey } from '../../shared/api';
import { forgetAllTurns, forgetTurn, markAnswerUnseen, useChatMark, useWatchedTurns, watchTurn } from './answer-ready';

type Place = { surface: 'saga' | 'chat'; chatId?: string };

function wrapper({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>{children}</QueryClientProvider>;
}

function mark(api?: ReturnType<typeof createMockApiClient>) {
  return renderHook(({ chatId, surface }: Place) => useChatMark({ surface, query: new URLSearchParams(chatId ? { chatId } : {}) }, 'p1', api),
    { initialProps: { surface: 'saga' } as Place, wrapper });
}

// specs/2026-11-10-ux2-shell.md S2.5, specs/2026-11-11-ux4-after-answer.md AA2.2: the mark of «Чат» in the menu — an answer
// on its way and the number of the answers the author has not opened.
describe('the mark of the chat in the menu', () => {
  afterEach(() => act(() => forgetAllTurns()));

  it('says an answer is on its way, counts the ready ones and forgets each once its chat is opened', () => {
    const { rerender, result } = mark();
    expect(result.current).toBeNull();
    act(() => watchTurn({ chatId: 'c1', jobId: 'j1', projectId: 'p1' }));
    expect(result.current).toEqual({ preparing: true, ready: 0 });
    act(() => {
      forgetTurn('j1');
      markAnswerUnseen({ chatId: 'c1', jobId: 'j1', projectId: 'p1' });
      markAnswerUnseen({ chatId: 'c2', jobId: 'j2', projectId: 'p1', label: 'Кто такой Сава' });
      // An answer of another saga is not counted in the menu of this one.
      markAnswerUnseen({ chatId: 'c9', jobId: 'j9', projectId: 'p9' });
    });
    expect(result.current).toEqual({ preparing: false, ready: 2 });
    rerender({ surface: 'chat', chatId: 'c1' });
    expect(result.current).toBeNull();
    rerender({ surface: 'saga' });
    expect(result.current).toEqual({ preparing: false, ready: 1 });
    rerender({ surface: 'chat', chatId: 'c2' });
    rerender({ surface: 'saga' });
    expect(result.current).toBeNull();
  });

  it('says an answer is on its way for a chat of the saga the list shows working, and awaits it', async () => {
    const api = createMockApiClient();
    const chat = await api.createChatSession(projectId, { scope: 'project', sourceView: 'published', title: 'Сава' }, { idempotencyKey: createIdempotencyKey('createChatSession') });
    const turn = await api.createChatTurn(chat.id, { body: 'Кто такой Сава?' }, { idempotencyKey: createIdempotencyKey('createChatTurn') });
    const { result } = renderHook(() => ({
      mark: useChatMark({ surface: 'saga', query: new URLSearchParams() }, projectId, api),
      watched: useWatchedTurns(),
    }), { wrapper });
    await waitFor(() => expect(result.current.mark).toEqual({ preparing: true, ready: 0 }));
    expect(result.current.watched).toEqual([expect.objectContaining({ chatId: chat.id, jobId: turn.job.id, projectId, label: 'Сава' })]);
    void vi;
  });
});
