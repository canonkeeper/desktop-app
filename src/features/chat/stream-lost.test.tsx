import { act, renderHook } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { conversationFailure, streamLostBlockMs, useStreamLostLong } from './research-chat-presentation';

afterEach(() => vi.useRealTimers());

// specs/2026-11-11-ux4-after-answer.md AA2.6 (Ч-74): a short break of the stream is no alarm; a minute without it is.
describe('a break of the stream of the feed', () => {
  it('is a block only once it did not mend in a minute', () => {
    vi.useFakeTimers();
    const { rerender, result } = renderHook(({ connection }) => useStreamLostLong(connection), { initialProps: { connection: 'connected' } });
    rerender({ connection: 'reconnecting' });
    expect(result.current).toBe(false);
    act(() => vi.advanceTimersByTime(streamLostBlockMs - 1_000));
    expect(result.current).toBe(false);
    rerender({ connection: 'connected' });
    rerender({ connection: 'reconnecting' });
    act(() => vi.advanceTimersByTime(streamLostBlockMs - 1_000));
    expect(result.current).toBe(false);
    act(() => vi.advanceTimersByTime(1_000));
    expect(result.current).toBe(true);
    const input = { panelFailure: null, limitNamed: false, selectedChatMissing: false, sessionError: null, sessionErrorRequestId: '', jobIsActive: true };
    expect(conversationFailure({ ...input, connection: 'connected' }).failure).toBeNull();
    expect(conversationFailure({ ...input, connection: 'reconnecting' }).failure?.title).toBe('Связь прервалась');
  });
});
