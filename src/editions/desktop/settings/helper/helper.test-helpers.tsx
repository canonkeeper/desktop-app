// SPDX-License-Identifier: AGPL-3.0-only
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { createMockApiClient, type MockScenarioControl } from '../../../../api/mock';
import type { CanonKeeperApiClient } from '../../../../shared/api';
import { ToastsProvider } from '../../../../ui/feedback/Toast';
import { HelperPart } from './HelperPart';

/** The computer of the app with its local author signed in, on the mock of its contract (specs/2026-11-12-desk-window.md WN4.1). */
export async function theApp(scenario: Partial<MockScenarioControl> = {}) {
  const api = createMockApiClient();
  api.startDesktop();
  if (Object.keys(scenario).length) api.setScenario({ ...api.getScenario(), ...scenario });
  await api.openDesktopSession({ code: 'code-of-the-shell' });
  return api;
}

export type TheApp = Awaited<ReturnType<typeof theApp>>;

/** «Помощник» over the app, in the toasts of the root, once its settings are read; the client of its queries is the test's. */
export async function renderHelper(api: TheApp) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <ToastsProvider><HelperPart api={api as unknown as CanonKeeperApiClient} /></ToastsProvider>
    </QueryClientProvider>,
  );
  await screen.findByRole('button', { name: /^Провайдер/u });
  return client;
}

/** The trigger of a list by its label: «Провайдер», «Модель для чата». */
export function listOf(label: string) {
  return screen.getByRole('button', { name: new RegExp(`^${label}`, 'u') });
}

/** Chooses an option of a list by its label. */
export function choose(label: string, option: string) {
  fireEvent.click(listOf(label));
  fireEvent.click(within(screen.getByRole('listbox')).getByRole('option', { name: option }));
}

/** Types into a field by its label. */
export function type(label: string, value: string) {
  fireEvent.change(screen.getByLabelText(label), { target: { value } });
}
