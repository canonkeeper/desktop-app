import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, renderHook, waitFor } from '@testing-library/react';
import { createElement, type PropsWithChildren } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { FakeWebSocket } from '../../../test/FakeWebSocket';
import type { AgentTimelineEvent } from '../../../shared/api';
import { useCanonicalAgentTimeline } from './index';

afterEach(() => {
  FakeWebSocket.reset();
  vi.useRealTimers();
});

function event(
  cursor: string,
  id: string,
  kind: AgentTimelineEvent['kind'] = 'assistant',
): AgentTimelineEvent {
  return {
    cursor,
    id,
    jobId: 'job-1',
    kind,
    logicalSection: 0,
    role: 'main',
    text: id,
  };
}

function page(events: AgentTimelineEvent[], nextCursor: string | null, eof: boolean) {
  return {
    eof,
    events,
    needsReconcile: false,
    nextCursor,
  } as never;
}

function pageWithoutEof(events: AgentTimelineEvent[], nextCursor: string) {
	return { events, needsReconcile: false, nextCursor } as never;
}

function renderTimeline(
  api: { getAgentTimeline: ReturnType<typeof vi.fn> },
  options: Omit<Partial<Parameters<typeof useCanonicalAgentTimeline>[0]>, 'api'> = {},
) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const wrapper = ({ children }: PropsWithChildren) => createElement(QueryClientProvider, { client: queryClient }, children);
  const rendered = renderHook(() => useCanonicalAgentTimeline({
    api: api as Parameters<typeof useCanonicalAgentTimeline>[0]['api'],
    createSocket: (url) => new FakeWebSocket(url) as unknown as WebSocket,
    jobId: 'job-1',
    projectId: 'project-1',
    ...options,
  }), { wrapper });
  return { ...rendered, queryClient };
}

describe('useCanonicalAgentTimeline', () => {
  it('marks timeline query failures as local to the owning surface', () => {
    const getAgentTimeline = vi.fn(() => new Promise<never>(() => undefined));
    const { queryClient } = renderTimeline({ getAgentTimeline });

    const query = queryClient.getQueryCache().find({
      queryKey: ['canonkeeper', 'agent-timeline', 'project-1', 'job-1'],
    });
    expect(query?.meta).toMatchObject({ outageScope: 'local' });
  });

	it('drains every snapshot page before opening one stream', async () => {
    const getAgentTimeline = vi.fn()
      .mockResolvedValueOnce(page([event('tl_MQ', 'event-1')], 'tl_Mg', false))
      .mockResolvedValueOnce(page([event('tl_Mg', 'event-2')], null, true));
    const { result } = renderTimeline({ getAgentTimeline });

    await waitFor(() => expect(FakeWebSocket.instances).toHaveLength(1));

    expect(getAgentTimeline).toHaveBeenNthCalledWith(1, 'project-1', 'job-1', expect.objectContaining({ cursor: undefined }));
    expect(getAgentTimeline).toHaveBeenNthCalledWith(2, 'project-1', 'job-1', expect.objectContaining({ cursor: 'tl_Mg' }));
    await waitFor(() => expect(result.current.events).toHaveLength(2));
    expect(result.current.events.map((item) => item.id)).toEqual(['event-1', 'event-2']);
	});

	it('does not treat a page without explicit EOF as terminal', async () => {
		const getAgentTimeline = vi.fn()
      .mockResolvedValueOnce(pageWithoutEof([event('tl_MQ', 'event-1')], 'tl_Mg'))
      .mockResolvedValueOnce(page([event('tl_Mg', 'event-2')], null, true));
		renderTimeline({ getAgentTimeline });

		await waitFor(() => expect(getAgentTimeline).toHaveBeenCalledTimes(2));
    expect(getAgentTimeline).toHaveBeenNthCalledWith(2, 'project-1', 'job-1', expect.objectContaining({ cursor: 'tl_Mg' }));
	});

  it('replays from the exact cursor, keeps order idempotent, and calls terminal once', async () => {
    const onTerminalEvent = vi.fn();
    const getAgentTimeline = vi.fn()
      .mockResolvedValueOnce(page([event('tl_MQ', 'event-1')], null, true))
      .mockResolvedValueOnce(page([
        event('tl_Mg', 'event-2'),
        event('tl_Mw', 'terminal', 'turn_outcome'),
      ], null, true));
    const { result } = renderTimeline({ getAgentTimeline }, { onTerminalEvent });
    await waitFor(() => expect(FakeWebSocket.instances).toHaveLength(1));
    const first = FakeWebSocket.instances[0]!;

    act(() => {
      first.message({
        cursor: 'tl_Mw',
        event: {
          ...event('tl_Mw', 'terminal', 'turn_outcome'),
          cursor: 'tl_Mw',
          outcome: 'failed',
        },
        type: 'event',
      });
      first.serverClose();
    });
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 1_100));
    });
    await waitFor(() => expect(FakeWebSocket.instances).toHaveLength(2));

    expect(getAgentTimeline).toHaveBeenLastCalledWith('project-1', 'job-1', { cursor: 'tl_Mw' });
    expect(result.current.events.map((item) => item.id)).toEqual(['event-1', 'event-2', 'terminal']);
    expect(onTerminalEvent).toHaveBeenCalledTimes(1);

    act(() => {
      FakeWebSocket.instances[1]?.message({
        cursor: 'tl_Mw',
        event: { ...event('tl_Mw', 'terminal', 'turn_outcome'), cursor: 'tl_Mw', outcome: 'failed' },
        type: 'event',
      });
    });
    expect(onTerminalEvent).toHaveBeenCalledTimes(1);
    expect(result.current.connection).toBe('connected');
  });

  it('keeps reconnecting separate from authoritative Job state', async () => {
    const getAgentTimeline = vi.fn().mockResolvedValue(page([event('tl_MQ', 'event-1')], null, true));
    const { result } = renderTimeline({ getAgentTimeline });
    await waitFor(() => expect(FakeWebSocket.instances).toHaveLength(1));
    act(() => {
      FakeWebSocket.instances[0]?.message({ message: 'Повторяем подключение.', type: 'reconnecting' });
    });

    expect(result.current.connection).toBe('reconnecting');
    expect(result.current.events).toHaveLength(1);
  });

  it('preserves server order for opaque cursors and upserts replay duplicates', async () => {
    const ordered = [
      { ...event('tl_eA', 'event-z'), cursor: 'tl_eA' },
      { ...event('tl_YQ', 'event-a'), cursor: 'tl_YQ' },
    ] as unknown as AgentTimelineEvent[];
    const getAgentTimeline = vi.fn().mockResolvedValue(page(ordered, null, true));
    const { result } = renderTimeline({ getAgentTimeline });

    await waitFor(() => expect(FakeWebSocket.instances).toHaveLength(1));
    expect(result.current.events.map((item) => item.id)).toEqual(['event-z', 'event-a']);

    act(() => {
      FakeWebSocket.instances[0]?.message({
        cursor: 'tl_YQ',
        event: ordered[1],
        type: 'event',
      });
    });

    expect(result.current.events.map((item) => item.id)).toEqual(['event-z', 'event-a']);
  });

  it('uses event id rather than cursor as identity', async () => {
    const getAgentTimeline = vi.fn().mockResolvedValue(page([event('tl_MQ', 'child')], null, true));
    const { result } = renderTimeline({ getAgentTimeline });
    await waitFor(() => expect(FakeWebSocket.instances).toHaveLength(1));

    act(() => {
      FakeWebSocket.instances[0]?.message({
        cursor: 'tl_MQ',
        event: event('tl_MQ', 'root-final'),
        type: 'event',
      });
    });

    expect(result.current.events.map((item) => item.id)).toEqual(['child', 'root-final']);
  });

  it('accumulates deltas ephemerally and replaces them by canonical id', async () => {
    const toolRequest = {
      ...event('tl_tool_request', 'tool-request', 'tool_request'),
      tool: { name: 'read_source', status: 'running' },
    };
    const toolResult = {
      ...event('tl_tool_result', 'tool-result', 'tool_result'),
      tool: { name: 'read_source', status: 'completed', success: true },
    };
    const getAgentTimeline = vi.fn()
      .mockResolvedValueOnce(page([toolRequest, toolResult], null, true))
      .mockResolvedValue(page([], null, true));
    const { result, queryClient } = renderTimeline({ getAgentTimeline });
    await waitFor(() => expect(FakeWebSocket.instances).toHaveLength(1));
    const socket = FakeWebSocket.instances[0]!;
    const queryData = () => queryClient.getQueryData(['canonkeeper', 'agent-timeline', 'project-1', 'job-1']);

    act(() => {
      socket.message({
        cursor: 'tl_delta_assistant',
        delta: {
          id: 'assistant-item-0000000000001',
          kind: 'assistant',
          jobId: 'job-1',
          logicalSection: 0,
          providerItemId: 'item-1',
          providerTurnId: 'turn-1',
          role: 'main',
          text: 'Перв',
        },
        type: 'delta',
      });
    });
    expect(result.current.events.map((item) => item.id)).toEqual([
      'tool-request',
      'tool-result',
      'assistant-item-0000000000001',
    ]);
    expect(result.current.events.at(-1)?.text).toBe('Перв');

    act(() => {
      socket.message({
        cursor: 'tl_delta_assistant',
        delta: {
          id: 'assistant-item-0000000000001',
          kind: 'assistant',
          jobId: 'job-1',
          logicalSection: 0,
          providerItemId: 'item-1',
          providerTurnId: 'turn-1',
          role: 'main',
          text: 'ый ответ',
        },
        type: 'delta',
      });
      socket.message({
        cursor: 'tl_delta_reasoning',
        delta: {
          id: 'reasoning-item-0000000000001',
          kind: 'reasoning',
          jobId: 'job-1',
          logicalSection: 0,
          providerItemId: 'item-2',
          providerTurnId: 'turn-1',
          role: 'main',
          text: 'Думаю',
        },
        type: 'delta',
      });
    });
    expect(result.current.events.map((item) => item.id)).toEqual([
      'tool-request',
      'tool-result',
      'assistant-item-0000000000001',
      'reasoning-item-0000000000001',
    ]);
    expect(result.current.events.at(-2)?.text).toBe('Первый ответ');
    expect(result.current.events.at(-1)?.kind).toBe('reasoning');
    const canonicalData = queryData() as { pages: Array<{ events: AgentTimelineEvent[] }> };
    expect(canonicalData.pages.flatMap((page) => page.events).map((item) => item.id))
      .toEqual(['tool-request', 'tool-result']);

    act(() => {
      socket.message({ type: 'reconnecting' });
    });
    await waitFor(() => expect(getAgentTimeline).toHaveBeenCalledTimes(2));
    expect(getAgentTimeline).toHaveBeenLastCalledWith('project-1', 'job-1', { cursor: 'tl_tool_result' });

    act(() => {
      socket.message({
        cursor: 'tl_snapshot',
        snapshot: {
          eof: true,
          events: [
            toolRequest,
            toolResult,
            {
              ...event('tl_canonical_assistant', 'assistant-item-0000000000001'),
              text: 'Канонический ответ',
            },
          ],
          needsReconcile: false,
          nextCursor: null,
        },
        type: 'snapshot',
      });
    });
    expect(result.current.events.map((item) => item.id)).toEqual([
      'tool-request',
      'tool-result',
      'assistant-item-0000000000001',
      'reasoning-item-0000000000001',
    ]);
    expect(result.current.events.at(-2)?.text).toBe('Канонический ответ');

    act(() => {
      socket.message({
        cursor: 'tl_canonical_reasoning',
        event: {
          ...event('tl_canonical_reasoning', 'reasoning-item-0000000000001', 'reasoning'),
          text: 'Канонический размышления',
        },
        type: 'event',
      });
    });
    expect(result.current.events.map((item) => item.id)).toEqual([
      'tool-request',
      'tool-result',
      'assistant-item-0000000000001',
      'reasoning-item-0000000000001',
    ]);
    expect(result.current.events.at(-1)?.text).toBe('Канонический размышления');
    const promotedData = queryData() as { pages: Array<{ events: AgentTimelineEvent[] }> };
    expect(promotedData.pages.flatMap((page) => page.events).map((item) => item.id)).toEqual([
      'tool-request',
      'tool-result',
      'assistant-item-0000000000001',
      'reasoning-item-0000000000001',
    ]);
  });

  it('keeps live reasoning before a later canonical tool event', async () => {
    const user = event('tl_user', 'user', 'user');
    const getAgentTimeline = vi.fn().mockResolvedValue(page([user], null, true));
    const { result } = renderTimeline({ getAgentTimeline });
    await waitFor(() => expect(FakeWebSocket.instances).toHaveLength(1));
    const socket = FakeWebSocket.instances[0]!;

    act(() => {
      socket.message({
        cursor: 'tl_reasoning',
        delta: {
          id: 'reasoning-item:0',
          kind: 'reasoning',
          jobId: 'job-1',
          logicalSection: 0,
          providerItemId: 'reasoning-item',
          providerTurnId: 'turn-1',
          role: 'main',
          text: 'Проверяю источник.',
        },
        type: 'delta',
      });
      socket.message({
        cursor: 'tl_tool',
        snapshot: {
          eof: true,
          events: [
            user,
            {
              ...event('tl_tool', 'tool:0', 'tool_request'),
              tool: { name: 'source.read', status: 'running' },
            },
          ],
          needsReconcile: false,
          nextCursor: null,
        },
        type: 'snapshot',
      });
    });

    expect(result.current.events.map((item) => item.id)).toEqual([
      'user',
      'reasoning-item:0',
      'tool:0',
    ]);
  });

  it('ignores a queued delta after its canonical item is already in the snapshot', async () => {
    const canonical = {
      ...event('tl_assistant', 'assistant-item:0'),
      providerItemId: 'assistant-item',
      providerTurnId: 'turn-1',
      text: 'Готовый ответ',
    };
    const getAgentTimeline = vi.fn().mockResolvedValue(page([], null, true));
    const { result } = renderTimeline({ getAgentTimeline });
    await waitFor(() => expect(FakeWebSocket.instances).toHaveLength(1));
    const socket = FakeWebSocket.instances[0]!;

    act(() => {
      socket.message({
        cursor: canonical.cursor,
        snapshot: {
          eof: true,
          events: [canonical],
          needsReconcile: false,
          nextCursor: null,
        },
        type: 'snapshot',
      });
      socket.message({
        cursor: canonical.cursor,
        delta: {
          id: canonical.id,
          kind: 'assistant',
          jobId: 'job-1',
          logicalSection: 0,
          providerItemId: 'assistant-item',
          providerTurnId: 'turn-1',
          role: 'main',
          text: 'Готовый ответ',
        },
        type: 'delta',
      });
    });

    expect(result.current.events).toHaveLength(1);
    expect(result.current.events[0]?.text).toBe('Готовый ответ');
  });

  // A page that comes before a render shows its fetch still asks for the next one (D6.7 of the drawing workshop met it:
  // a turn of the helper on three pages of the mock stopped after the second).
  it('reads every page of a timeline whose pages come at once', async () => {
    const getAgentTimeline = vi.fn()
      .mockResolvedValueOnce(page([event('tl_1', 'question', 'user'), event('tl_2', 'reasoning', 'reasoning')], 'tl_2', false))
      .mockResolvedValueOnce(page([event('tl_3', 'tool', 'tool_result'), event('tl_4', 'answer')], 'tl_4', false))
      .mockResolvedValueOnce(page([event('tl_5', 'outcome', 'turn_outcome')], null, true));
    const { result } = renderTimeline({ getAgentTimeline });
    await waitFor(() => expect(result.current.events.map((item) => item.id)).toEqual(['question', 'reasoning', 'tool', 'answer', 'outcome']));
    expect(getAgentTimeline.mock.calls.map((call) => (call[2] as { cursor?: string }).cursor)).toEqual([undefined, 'tl_2', 'tl_4']);
  });

  it('reports a turn outcome from the initial snapshot once for authoritative Job refresh', async () => {
    const onTerminalEvent = vi.fn();
    const turnOutcome = event('tl_MQ', 'turn-outcome', 'turn_outcome');
    turnOutcome.outcome = 'failed';
    const getAgentTimeline = vi.fn().mockResolvedValue(page([turnOutcome], null, true));
    const { result } = renderTimeline({ getAgentTimeline }, { onTerminalEvent });

    await waitFor(() => expect(onTerminalEvent).toHaveBeenCalledTimes(1));
    expect(result.current.events).toHaveLength(1);

    act(() => {
      FakeWebSocket.instances[0]?.message({
        cursor: turnOutcome.cursor,
        event: turnOutcome,
        type: 'event',
      });
    });

    expect(onTerminalEvent).toHaveBeenCalledTimes(1);
  });

  it('keeps latest live context usage separately for each agent role', async () => {
    const getAgentTimeline = vi.fn().mockResolvedValue(page([], null, true));
    const { result } = renderTimeline({ getAgentTimeline });
    await waitFor(() => expect(FakeWebSocket.instances).toHaveLength(1));

    act(() => {
      FakeWebSocket.instances[0]?.message({
        context: {
          contextWindow: 128_000,
          providerTurnId: 'turn-root',
          role: 'main',
          usedTokens: 96_000,
        },
        type: 'context',
      });
      FakeWebSocket.instances[0]?.message({
        context: {
          contextWindow: 64_000,
          providerTurnId: 'turn-reviewer',
          role: 'source_evidence',
          usedTokens: 16_000,
        },
        type: 'context',
      });
    });

    const contextByRole = (result.current as unknown as {
      contextByRole?: Record<string, { usedTokens?: number }>;
    }).contextByRole;
    expect(contextByRole?.main?.usedTokens).toBe(96_000);
    expect(contextByRole?.source_evidence?.usedTokens).toBe(16_000);
    expect(FakeWebSocket.instances).toHaveLength(1);
  });
});
