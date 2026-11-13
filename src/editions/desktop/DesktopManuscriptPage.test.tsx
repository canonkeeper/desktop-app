// SPDX-License-Identifier: AGPL-3.0-only
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { Suspense } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../api/mock';
import { projectId } from '../../api/mock/repository/fixtures';
import { parseRoute } from '../../shared/navigation/app-route';
import { FakeWebSocket } from '../../test/FakeWebSocket';
import { ToastsProvider } from '../../ui/feedback/Toast';
import { edition } from '.';

beforeEach(() => vi.stubGlobal('WebSocket', FakeWebSocket));
afterEach(() => {
  cleanup();
  FakeWebSocket.reset();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

function renderBooks(address: string) {
  const api = createMockApiClient();
  api.setScenario({ actorRole: 'editor', preset: 'normal' });
  const navigate = vi.fn();
  const user = { id: 'user-local' } as Parameters<NonNullable<typeof edition.pages.manuscript>['render']>[0]['user'];
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><ToastsProvider>
      <Suspense fallback={null}>{edition.pages.manuscript!.render({ api, navigate, route: parseRoute(`http://localhost${address}`), user })}</Suspense>
    </ToastsProvider></QueryClientProvider>,
  );
  return { api, navigate };
}

// specs/2026-11-13-desk-editor.md ED2.1, ED1.1: «Книги» of the app — «Новая глава» in the menu of a book, the editor at its address.
describe('«Книги» of the desktop app', () => {
  it('has «Новая глава» in the menu of a book, which opens the editor of a new chapter of it', async () => {
    const { navigate } = renderBooks(`/manuscript/books?projectId=${projectId}`);
    const button = await screen.findByRole('button', { name: 'Действия книги Книга II. Карта приливов' }, { timeout: 10_000 });
    const bookId = button.closest('article')!.getAttribute('data-book-id')!;
    fireEvent.click(button);
    const menu = await screen.findByRole('menu', { name: 'Действия книги' });
    expect(within(menu).getAllByRole('menuitem').map((item) => item.textContent)[1]).toBe('Новая глава');
    fireEvent.click(within(menu).getByRole('menuitem', { name: 'Новая глава' }));
    expect(navigate).toHaveBeenCalledWith('/manuscript/edit', { projectId, bookId });
  });

  it('opens the editor at /manuscript/edit', async () => {
    renderBooks(`/manuscript/edit?projectId=${projectId}&bookId=book-02`);
    expect(await screen.findByRole('region', { name: 'Новая глава' }, { timeout: 10_000 })).toBeTruthy();
    await waitFor(() => expect(screen.getByRole('button', { name: 'Добавить в книгу' })).toBeTruthy());
  });
});
