// SPDX-License-Identifier: AGPL-3.0-only
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render } from '@testing-library/react';
import type { ComponentType } from 'react';
import { vi } from 'vitest';
import type { CanonKeeperApiClient, OperationResponse } from '../../../shared/api';
import { ToastsProvider } from '../../../ui/feedback/Toast';
import type { SettingsPartProps } from './part-props';

export type DesktopState = OperationResponse<'getDesktopState', '200'>;

/** The app of the board 44·11 and 44·16: a computer of the author with two copies before an update. */
export const appOnTheComputer: DesktopState = {
  version: '1.2.0',
  dataDir: 'C:\\Users\\author\\AppData\\Local\\ru.canonkeeper.desktop\\data',
  usage: { bytes: 1_288_490_188, counting: false },
  backups: [
    { madeAt: '2026-11-12T09:15:00.000Z', version: '1.1.4' },
    { madeAt: '2026-11-02T08:00:00.000Z', version: null },
  ],
  network: [],
};

/** The shell of the window as Tauri gives it to the page; its commands are recorded. */
export function shellOfTheWindow(answers: Record<string, unknown> = {}) {
  const invoke = vi.fn(async (command: string) => answers[command] ?? null);
  (window as { __TAURI__?: unknown }).__TAURI__ = { core: { invoke } };
  return invoke;
}

export function noShell() {
  delete (window as { __TAURI__?: unknown }).__TAURI__;
}

/** A part of «Настройки» over the state the test gives, in the toasts of the root. */
export function renderPart(Part: ComponentType<SettingsPartProps>, ...states: DesktopState[]) {
  const getDesktopState = vi.fn(async () => states.length > 1 ? states.shift()! : states[0]!);
  const api = { getDesktopState } as unknown as CanonKeeperApiClient;
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <ToastsProvider><Part api={api} /></ToastsProvider>
    </QueryClientProvider>,
  );
  return getDesktopState;
}
