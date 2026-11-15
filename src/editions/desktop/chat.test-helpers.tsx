// SPDX-License-Identifier: AGPL-3.0-only
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, within } from '@testing-library/react';
import { Suspense, useCallback, useState } from 'react';
import { createMockApiClient } from '../../api/mock';
import { projectId } from '../../api/mock/repository/fixtures';
import type { MockScenarioControl } from '../../api/mock/scenarios';
import { InShell } from '../../app/shell/AppShell.test-helpers';
import { createAppRoute, type AppRoute, type RouteNavigate } from '../../shared/navigation/app-route';
import { ToastsProvider } from '../../ui/feedback/Toast';
import { edition } from '.';

export type Api = ReturnType<typeof createMockApiClient>;
type ChatPageInput = Parameters<NonNullable<typeof edition.pages.chat>['render']>[0];

/**
 * The chat of the app as the root of the web puts it (src/App.tsx): the page of the chat of the edition in the shell under what
 * the account of the edition wraps the pages in — the update of the app with the one slot of the chat, the link with an account,
 * the slot of the line of the reading. Another address stands as its path and part.
 */
function ChatOfTheApp({ api, start }: { api: Api; start: AppRoute }) {
  const [route, setRoute] = useState(start);
  const navigate = useCallback<RouteNavigate>((path, query = {}) => setRoute(createAppRoute(path, query)), []);
  const account = edition.useAccount({ api, navigate, route } as unknown as Parameters<typeof edition.useAccount>[0]);
  const user = { id: 'user-local' } as ChatPageInput['user'];
  return account.wrapPages(
    <InShell api={api} navigate={navigate} route={route}>
      {route.path === '/chat'
        ? <Suspense fallback={null}>{edition.pages.chat!.render({ api, navigate, route, user })}</Suspense>
        : <p>{`${route.path}?part=${route.query.get('part') ?? ''}`}</p>}
    </InShell>,
  );
}

export function renderChat(api: Api, start = createAppRoute('/chat', { projectId, new: '1' })) {
  return render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { mutations: { retry: false }, queries: { retry: false } } })}>
      <ToastsProvider><ChatOfTheApp api={api} start={start} /></ToastsProvider>
    </QueryClientProvider>,
  );
}

/** The mock app linked with an account, its helper chosen, in the scenario of the test. */
export async function theAccount(scenario: Partial<MockScenarioControl> = {}) {
  const api = createMockApiClient();
  api.setScenario({ preset: 'normal', actorRole: 'owner', accountLinked: true });
  await api.updateAiSettings({ source: 'account' });
  api.setScenario({ ...api.getScenario(), ...scenario });
  return api;
}

/** The region of the chat once it stands. */
export function theChat() {
  return screen.findByRole('region', { name: 'Исследовательский чат' }, { timeout: 5000 });
}

/** The field of the question of the chat. */
export async function theField() {
  return within(await theChat()).findByRole('textbox', { name: 'Вопрос' }) as Promise<HTMLTextAreaElement>;
}
