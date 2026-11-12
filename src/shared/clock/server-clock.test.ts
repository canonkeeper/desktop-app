import { afterEach, describe, expect, it, vi } from 'vitest';
import { createCanonKeeperApiClient } from '../api';
import { forgetServerClock, serverNow } from './server-clock';

afterEach(() => {
  forgetServerClock();
  vi.useRealTimers();
});

// specs/2026-11-11-ux9-shelf-chat.md SC3.8 (J-80): the time of the server is taken from its answers, so a counter of a work
// counts by it and not by a clock of the device that runs ahead or behind.
describe('the clock of the server', () => {
  it('takes the difference of the clocks from the Date of an answer and keeps it until it changes by more than a second', async () => {
    vi.useFakeTimers({ toFake: ['Date'] });
    vi.setSystemTime(new Date('2026-11-11T13:00:00.000Z'));
    // The device runs an hour ahead of the server.
    const api = createCanonKeeperApiClient({
      baseUrl: 'https://api.example.test/api/v1',
      fetch: async () => new Response(JSON.stringify({ data: [], meta: { hasMore: false, nextCursor: null } }), {
        headers: { 'Content-Type': 'application/json', Date: 'Wed, 11 Nov 2026 12:00:00 GMT' }, status: 200,
      }),
    });
    expect(Math.abs(serverNow() - Date.parse('2026-11-11T13:00:00.000Z'))).toBeLessThan(1000);
    await api.listProjects();
    expect(Math.abs(serverNow() - Date.parse('2026-11-11T12:00:00.500Z'))).toBeLessThan(1000);
  });
});
