// SPDX-License-Identifier: AGPL-3.0-only
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../api/mock';
import { App } from '../../App';
import { ApiStatusError, NetworkApiError, type CanonKeeperApiClient } from '../../shared/api';
import { createAppRoute } from '../../shared/navigation/app-route';
import { useDesktopAccount } from './account/account-api';
import { useDesktopState } from './settings/desktop-state';
import { useAppUpdate } from './updates/app-update';
import { useWelcomeState } from './updates/welcome-state';

afterEach(() => vi.restoreAllMocks());

function serverFailure() {
  const problem = { type: 'about:blank', title: 'Service Unavailable', status: 503, code: 'service_unavailable', requestId: 'req-window' };
  return new ApiStatusError(503, problem, new Response(null, { status: 503 }));
}

type Reader = (api: CanonKeeperApiClient) => { isError: boolean };
type Operation = 'getAppUpdate' | 'getAccount' | 'getDesktopState';

/** The reads of the state of the window, each by the one hook of its part; the operation the read asks. */
const reads: [string, Operation, Reader][] = [
  ['the update', 'getAppUpdate', (api) => useAppUpdate(api)],
  ['the link with the account', 'getAccount', (api) => useDesktopAccount(api)],
  ['the state of the app', 'getDesktopState', (api) => useDesktopState(api)],
  ['«Добро пожаловать»', 'getDesktopState', (api) => useWelcomeState(api)],
];

function ReadOfTheWindow({ api, read }: { api: CanonKeeperApiClient; read: Reader }) {
  const state = read(api);
  return state.isError ? <p>the read failed</p> : null;
}

// specs/2026-11-17-desk-update-linux.md UL4.1: a failure of a read of the state of the window is said by its part; it does not
// make the root probe the session (getCurrentUser) or show «Сервис временно недоступен» — the other reads of a page do.
describe('the reads of the state of the window', () => {
  it.each(reads.flatMap(([name, operation, read]) => [
    [name, 'a 5xx', operation, read, serverFailure()] as const,
    [name, 'no network', operation, read, new NetworkApiError(new TypeError('fetch failed'))] as const,
  ]))('%s: %s is its own failure', async (_name, _failure, operation, read, failure) => {
    const api = createMockApiClient();
    const probe = vi.spyOn(api, 'getCurrentUser');
    const operations = api as unknown as Record<Operation, () => Promise<unknown>>;
    vi.spyOn(operations, operation).mockRejectedValue(failure);
    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <App api={api} navigate={vi.fn()} route={createAppRoute('/notes')} />
        <ReadOfTheWindow api={api} read={read} />
      </QueryClientProvider>,
    );
    expect(await screen.findByText('the read failed', {}, { timeout: 5000 })).toBeTruthy();
    await waitFor(() => expect(probe).toHaveBeenCalledTimes(1));
    await new Promise((resolve) => setTimeout(resolve, 100));
    expect(probe).toHaveBeenCalledTimes(1);
    expect(screen.queryByText('Сервис временно недоступен')).toBeNull();
  });
});
