// SPDX-License-Identifier: AGPL-3.0-only
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render } from '@testing-library/react';
import type { ReactNode } from 'react';
import { createMockApiClient, type MockScenarioControl } from '../../../api/mock';
import type { CanonKeeperApiClient } from '../../../shared/api';
import type { AppRoute } from '../../../shared/navigation/app-route';
import { ToastsProvider } from '../../../ui/feedback/Toast';
import { AccountLinkProvider } from './AccountLink';

/** The computer of the app with its local author signed in, on the mock of its contract (specs/2026-11-12-desk-window.md WN4.1). */
export async function theApp(scenario: Partial<MockScenarioControl> = {}) {
  const api = createMockApiClient();
  api.startDesktop();
  if (Object.keys(scenario).length) api.setScenario({ ...api.getScenario(), ...scenario });
  await api.openDesktopSession({ code: 'code-of-the-shell' });
  return api;
}

export type TheApp = Awaited<ReturnType<typeof theApp>>;

/** The scenario of the return of the browser the mock has not: the next read of getAccount ends the waiting so. */
export function returnAs(api: TheApp, outcome: NonNullable<MockScenarioControl['accountLinkOutcome']>) {
  api.setScenario({ ...api.getScenario(), accountLinkOutcome: outcome });
}

/**
 * A page of the app over the link of the edition, in the toasts of the root; the client of its queries is the test's, and the
 * address the page stands at — none: a page that is not «Настройки → Помощник».
 */
export function renderWithLink(
  api: TheApp, page: ReactNode, client = new QueryClient({ defaultOptions: { queries: { retry: false } } }), route?: AppRoute,
) {
  render(
    <QueryClientProvider client={client}>
      <ToastsProvider>
        <AccountLinkProvider api={api as unknown as CanonKeeperApiClient} route={route}>{page}</AccountLinkProvider>
      </ToastsProvider>
    </QueryClientProvider>,
  );
  return client;
}
