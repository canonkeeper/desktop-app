import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, renderHook, waitFor } from '@testing-library/react';
import { createElement, type PropsWithChildren } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { FakeWebSocket } from '../../../test/FakeWebSocket';
import { ApiStatusError, type AgentTimelineEvent } from '../../../shared/api';
import { useCanonicalAgentTimeline, type AgentTimelineConnection } from './index';

// A broken stream is a lost connection, not one failed read: the author who has just sent a
// question sees no break while the first read of the turn fails once and the next one passes
// (specs/2026-10-13-agent-feed.md, Decision log 30.10).

afterEach(() => {
  FakeWebSocket.reset();
});

const page = (events: AgentTimelineEvent[] = []) => ({ eof: true, events, needsReconcile: false, nextCursor: null }) as never;

function failure(status: number) {
  return new ApiStatusError(status, {
    type: 'about:blank', title: 'Timeline', status, code: status === 404 ? 'agent_timeline.not_found' : 'agent_timeline.unavailable',
    requestId: 'req-timeline',
  }, new Response(null, { status }));
}

function renderTimeline(getAgentTimeline: ReturnType<typeof vi.fn>) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const wrapper = ({ children }: PropsWithChildren) => createElement(QueryClientProvider, { client: queryClient }, children);
  const seen: AgentTimelineConnection[] = [];
  const rendered = renderHook(() => {
    const state = useCanonicalAgentTimeline({
      api: { getAgentTimeline } as unknown as Parameters<typeof useCanonicalAgentTimeline>[0]['api'],
      createSocket: (url) => new FakeWebSocket(url) as unknown as WebSocket,
      jobId: 'job-1',
      projectId: 'project-1',
    });
    seen.push(state.connection);
    return state;
  }, { wrapper });
  return { ...rendered, seen };
}

const wait = (ms: number) => act(async () => {
  await new Promise((resolve) => setTimeout(resolve, ms));
});

describe('useCanonicalAgentTimeline connection', () => {
  it.each([503, 404])('shows no break when the first read fails with %i and the next one passes', async (status) => {
    const getAgentTimeline = vi.fn()
      .mockRejectedValueOnce(failure(status))
      .mockResolvedValue(page());
    const { result, seen } = renderTimeline(getAgentTimeline);

    await waitFor(() => expect(FakeWebSocket.instances).toHaveLength(1), { timeout: 3_000 });
    act(() => FakeWebSocket.instances[0]?.open());

    expect(getAgentTimeline).toHaveBeenCalledTimes(2);
    expect(result.current.connection).toBe('connected');
    expect(result.current.isError).toBe(false);
    expect(seen).not.toContain('reconnecting');
    expect(seen).not.toContain('closed');
  });

  it('shows the break while reads keep failing and keeps retrying', async () => {
    const getAgentTimeline = vi.fn().mockRejectedValue(failure(503));
    const { result, seen } = renderTimeline(getAgentTimeline);

    await waitFor(() => expect(result.current.connection).toBe('reconnecting'), { timeout: 3_000 });
    expect(getAgentTimeline).toHaveBeenCalledTimes(2);
    expect(seen.slice(0, seen.indexOf('reconnecting'))).not.toContain('closed');

    await waitFor(() => expect(getAgentTimeline).toHaveBeenCalledTimes(3), { timeout: 3_000 });
    expect(result.current.connection).toBe('reconnecting');
  });

  it('stops reading a timeline the server keeps refusing', async () => {
    const getAgentTimeline = vi.fn().mockRejectedValue(failure(404));
    const { result } = renderTimeline(getAgentTimeline);

    await waitFor(() => expect(result.current.connection).toBe('closed'), { timeout: 3_000 });
    expect(getAgentTimeline).toHaveBeenCalledTimes(2);
    await wait(2_200);
    expect(getAgentTimeline).toHaveBeenCalledTimes(2);
  });

  it('shows no break when the stream drops once and the replay passes', async () => {
    const getAgentTimeline = vi.fn().mockResolvedValue(page());
    const { result, seen } = renderTimeline(getAgentTimeline);
    await waitFor(() => expect(FakeWebSocket.instances).toHaveLength(1));
    act(() => FakeWebSocket.instances[0]?.open());
    const before = seen.length;

    act(() => FakeWebSocket.instances[0]?.serverClose());
    await waitFor(() => expect(FakeWebSocket.instances).toHaveLength(2), { timeout: 3_000 });
    act(() => FakeWebSocket.instances[1]?.open());

    expect(result.current.connection).toBe('connected');
    expect(seen.slice(before)).not.toContain('reconnecting');
  });

  it('stops reconnecting when the replay after a drop is refused, until the author retries', async () => {
    const getAgentTimeline = vi.fn().mockResolvedValueOnce(page()).mockRejectedValueOnce(failure(404));
    const { result } = renderTimeline(getAgentTimeline);
    await waitFor(() => expect(FakeWebSocket.instances).toHaveLength(1));
    act(() => FakeWebSocket.instances[0]?.open());

    act(() => FakeWebSocket.instances[0]?.serverClose(1008, 'authorization expired'));
    await waitFor(() => expect(result.current.connection).toBe('closed'), { timeout: 3_000 });
    await wait(2_200);
    expect(getAgentTimeline).toHaveBeenCalledTimes(2);
    expect(FakeWebSocket.instances).toHaveLength(1);

    getAgentTimeline.mockResolvedValue(page());
    act(() => result.current.retry());
    await waitFor(() => expect(FakeWebSocket.instances).toHaveLength(2));
    act(() => FakeWebSocket.instances[1]?.open());
    expect(result.current.connection).toBe('connected');
  });
});
