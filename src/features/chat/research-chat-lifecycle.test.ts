import { afterEach, describe, expect, it, vi } from 'vitest';
import type { ChatJob } from '../../entities/chat/api';
import { ApiStatusError } from '../../shared/api';
import { reconcileCanceledJob, retryAfterSeconds } from './research-chat-lifecycle';

afterEach(() => vi.useRealTimers());

function rateLimit(header?: string) {
  const headers = new Headers();
  if (header !== undefined) headers.set('Retry-After', header);
  return new ApiStatusError(429, {
    code: 'rate_limit.exceeded',
    detail: 'Slow down.',
    requestId: 'request-rate-limit',
    status: 429,
    title: 'Too Many Requests',
    type: 'about:blank',
  }, new Response(null, { headers, status: 429 }));
}

describe('retryAfterSeconds', () => {
  it('accepts only authoritative delta-seconds', () => {
    expect(retryAfterSeconds(rateLimit('58'))).toBe(58);
    expect(retryAfterSeconds(rateLimit('0'))).toBe(0);
    expect(retryAfterSeconds(rateLimit('1.5'))).toBeNull();
    expect(retryAfterSeconds(rateLimit('Fri, 05 Sep 2026 12:00:00 GMT'))).toBeNull();
    expect(retryAfterSeconds(rateLimit())).toBeNull();
    expect(retryAfterSeconds(new Error('network'))).toBeNull();
  });
});

describe('reconcileCanceledJob', () => {
  function job(id: string, status: ChatJob['status']) {
    return { id, status } as ChatJob;
  }

  it.each(['queued', 'running', 'canceling'] as const)('polls an exact %s Job until its authoritative terminal status', async (status) => {
    vi.useFakeTimers();
    const read = vi.fn()
      .mockResolvedValueOnce(job('job-exact', status))
      .mockResolvedValueOnce(job('job-exact', 'canceling'))
      .mockResolvedValueOnce(job('job-exact', 'canceled'));

    const outcome = reconcileCanceledJob(read, 'job-exact');
    await vi.advanceTimersByTimeAsync(4_000);

    await expect(outcome).resolves.toMatchObject({ id: 'job-exact', status: 'canceled' });
    expect(read).toHaveBeenCalledTimes(3);
    expect(read).toHaveBeenCalledWith('job-exact');
  });

  it('rejects a foreign Job and stops after thirty seconds of canceling', async () => {
    await expect(reconcileCanceledJob(
      vi.fn().mockResolvedValue(job('job-foreign', 'canceled')),
      'job-exact',
    )).resolves.toBeNull();

    vi.useFakeTimers();
    const read = vi.fn().mockResolvedValue(job('job-exact', 'canceling'));
    const outcome = reconcileCanceledJob(read, 'job-exact');
    await vi.advanceTimersByTimeAsync(30_000);

    await expect(outcome).resolves.toBeNull();
    expect(read).toHaveBeenCalledTimes(16);
  });
});
