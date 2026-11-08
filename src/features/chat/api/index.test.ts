import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, renderHook, waitFor } from '@testing-library/react';
import { createElement, type PropsWithChildren } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../../api/mock';
import { bookId, projectId } from '../../../api/mock/repository/fixtures';
import { chatQueryKeys, type ChatApiClient, type ChatScope } from '../../../entities/chat/api';
import {
  useActiveChatTurnQuery,
  useChatSessionsQuery,
  useRenameChatSessionMutation,
} from './index';

describe('chatQueryKeys', () => {
  it('включает проект, view и id выбранного ресурса', () => {
    const scope: ChatScope = {
      projectId: 'project-white-port',
      sourceView: 'draft',
    };

    expect(chatQueryKeys.session(scope, 'chat-1').at(-1)).toBe('chat-1');
  });
});

describe('paginated Chat queries', () => {
  it('объединяет две страницы чатов в серверном порядке от новых к старым', async () => {
    const sourceApi = createMockApiClient();
    const older = await sourceApi.createChatSession(projectId, {
      scope: 'project', sourceView: 'published', title: 'Старый чат',
    }, { idempotencyKey: 'idem-older-paginated-chat' });
    const newer = await sourceApi.createChatSession(projectId, {
      scope: 'project', sourceView: 'published', title: 'Новый чат',
    }, { idempotencyKey: 'idem-newer-paginated-chat' });
    const listChatSessions = vi.fn()
      .mockResolvedValueOnce({ data: [newer], meta: { hasMore: true, nextCursor: 'sessions-page-2' } })
      .mockResolvedValueOnce({ data: [older], meta: { hasMore: false, nextCursor: null } });
    const api = { listChatSessions } as unknown as ChatApiClient;
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const wrapper = ({ children }: PropsWithChildren) => createElement(QueryClientProvider, { client: queryClient }, children);

    const { result } = renderHook(() => useChatSessionsQuery(api, {
      projectId, sourceView: 'published',
    }), { wrapper });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data).toEqual({
      data: [newer, older],
      meta: { hasMore: false, nextCursor: null },
    });
    const signal = listChatSessions.mock.calls[0]?.[1].signal;
    expect(signal).toBeInstanceOf(AbortSignal);
    expect(listChatSessions).toHaveBeenNthCalledWith(1, projectId, { limit: 100, signal });
    expect(listChatSessions).toHaveBeenNthCalledWith(2, projectId, { limit: 100, cursor: 'sessions-page-2', signal });
  });

});

describe('working chats', () => {
  it('refreshes the chat list while a chat works and stops once none does', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      const session = await createMockApiClient().createChatSession(projectId, {
        scope: 'project', sourceView: 'published', title: 'Работающий чат',
      }, { idempotencyKey: 'idem-working-chat' });
      const listChatSessions = vi.fn()
        .mockResolvedValueOnce({ data: [{ ...session, latestTurnStatus: 'running' }], meta: { hasMore: false, nextCursor: null } })
        .mockResolvedValue({ data: [{ ...session, latestTurnStatus: 'succeeded' }], meta: { hasMore: false, nextCursor: null } });
      const api = { listChatSessions } as unknown as ChatApiClient;
      const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
      const wrapper = ({ children }: PropsWithChildren) => createElement(QueryClientProvider, { client: queryClient }, children);
      const { result } = renderHook(() => useChatSessionsQuery(api, { projectId, sourceView: 'published' }), { wrapper });

      await waitFor(() => expect(result.current.data?.data[0]?.latestTurnStatus).toBe('running'));
      await act(() => vi.advanceTimersByTimeAsync(4_000));
      await waitFor(() => expect(result.current.data?.data[0]?.latestTurnStatus).toBe('succeeded'));
      await act(() => vi.advanceTimersByTimeAsync(12_000));
      expect(listChatSessions).toHaveBeenCalledTimes(2);
    } finally {
      vi.useRealTimers();
    }
  });
});

describe('useActiveChatTurnQuery', () => {
  it.each(['project', 'book'] as const)('resolves the exact latest Job with a persisted %s subject', async (persistedScope) => {
    const sourceApi = createMockApiClient();
    const selectedChat = await sourceApi.createChatSession(projectId, {
      scope: 'project', sourceView: 'published', title: 'Выбранный чат',
    }, { idempotencyKey: 'idem-direct-active-selected-chat' });
    const activeTurn = await sourceApi.createChatTurn(selectedChat.id, { body: 'Точный turn' }, { idempotencyKey: 'idem-direct-active-selected-turn' });
    const api = sourceApi;
    const getChatSession = vi.spyOn(sourceApi, 'getChatSession');
    const getJob = vi.spyOn(sourceApi, 'getJob').mockResolvedValue({
      ...activeTurn.job,
      status: persistedScope === 'book' ? 'succeeded' : activeTurn.job.status,
      canCancel: persistedScope !== 'book',
      subject: {
        type: 'research_chat_turn', projectId, chatId: selectedChat.id, turnId: 'persisted-turn', sourceView: 'published',
        ...(persistedScope === 'book' ? { scope: 'book', bookId } : { scope: 'project' }),
      },
    });
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const wrapper = ({ children }: PropsWithChildren) => createElement(QueryClientProvider, { client: queryClient }, children);

    const { result } = renderHook(() => useActiveChatTurnQuery(api, {
      projectId, sourceView: 'published',
    }, selectedChat.id), { wrapper });

    await waitFor(() => expect(result.current.data?.id).toBe(activeTurn.job.id));
    if (persistedScope === 'book') expect(result.current.data?.status).toBe('succeeded');
    expect(getChatSession).toHaveBeenCalledWith(selectedChat.id);
    expect(getJob).toHaveBeenCalledWith(activeTurn.jobId);
  });
});

describe('useRenameChatSessionMutation', () => {
  it('updates only the exact session and list keys after a successful response', async () => {
    const api = createMockApiClient();
    const scope: ChatScope = { projectId, sourceView: 'published' };
    const session = await api.createChatSession(projectId, {
      scope: 'project', sourceView: 'published', title: 'Старое название',
    }, { idempotencyKey: 'idem-rename-session' });
    const queryClient = new QueryClient({ defaultOptions: { mutations: { retry: false }, queries: { retry: false } } });
    queryClient.setQueryData(chatQueryKeys.sessions(scope), {
      data: [session], meta: { hasMore: false, nextCursor: null },
    });
    queryClient.setQueryData(chatQueryKeys.session(scope, session.id), session);
    const invalidateQueries = vi.spyOn(queryClient, 'invalidateQueries');
    const wrapper = ({ children }: PropsWithChildren) => createElement(QueryClientProvider, { client: queryClient }, children);
    const { result } = renderHook(() => useRenameChatSessionMutation(api, scope), { wrapper });

    await act(async () => {
      await result.current.mutateAsync({ chatId: session.id, title: '  Новое название  ' });
    });

    expect(queryClient.getQueryData<{ data: { title: string }[] }>(chatQueryKeys.sessions(scope))?.data[0]?.title)
      .toBe('Новое название');
    expect(queryClient.getQueryData<{ title: string }>(chatQueryKeys.session(scope, session.id))?.title)
      .toBe('Новое название');
    expect(invalidateQueries).toHaveBeenCalledWith({ queryKey: chatQueryKeys.sessions(scope), exact: true });
    expect(invalidateQueries).toHaveBeenCalledWith({ queryKey: chatQueryKeys.session(scope, session.id), exact: true });
  });
});
