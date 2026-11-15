// SPDX-License-Identifier: AGPL-3.0-only
import { QueryClient } from '@tanstack/react-query';
import { cleanup, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { FakeWebSocket } from '../../../test/FakeWebSocket';
import { obsidianKeys } from './obsidian-api';
import { renderAt, theApp } from './obsidian.test-helpers';

beforeEach(() => vi.stubGlobal('WebSocket', FakeWebSocket));
afterEach(() => {
  cleanup();
  FakeWebSocket.reset();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

/** A saga of the app with a book and a load of «Книги» whose volume no file of this tab is sent to. */
async function aLoad() {
  const { api, saga } = await theApp();
  await api.createBook(saga, { title: 'Застава' });
  await api.createImportQueue(saga, { items: [{ clientItemId: 'obsidian-1', position: 1, sourceFileName: 'Долгая зима.md' }] }, { idempotencyKey: 'idem-bridge-volume' });
  return { api, saga };
}

// specs/2026-11-15-desk-obsidian-web.md OW4.6 through the edition of the app: while the write of the books of «Загрузить» goes,
// «Книги» draw its volume «Загружается» without «Выбрать файл»; once it answered, «Книги» lead it in their own words.
describe('the volumes of a load from an Obsidian folder on «Книги»', () => {
  it('are «Загружается» without «Выбрать файл» while the write of the books goes', { timeout: 20_000 }, async () => {
    const { api, saga } = await aLoad();
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    void client.getMutationCache().build(client, { mutationKey: obsidianKeys.loadBooks, mutationFn: () => new Promise(() => undefined) })
      .execute({ projectId: saga });
    renderAt(api, `/manuscript/books?projectId=${saga}`, { client });
    const card = await screen.findByRole('article', { name: 'Долгая зима.md' }, { timeout: 15_000 });
    expect(within(card).getByText('Загружается')).toBeTruthy();
    expect(within(card).queryByText('Выбрать файл')).toBeNull();
    expect(screen.queryByText('Файл не дошёл')).toBeNull();
  });

  it('are the volumes of «Книги» again once the write answered', { timeout: 20_000 }, async () => {
    const { api, saga } = await aLoad();
    renderAt(api, `/manuscript/books?projectId=${saga}`);
    const card = await screen.findByRole('article', { name: 'Долгая зима.md' }, { timeout: 15_000 });
    expect(within(card).getByText('Файл не дошёл')).toBeTruthy();
  });
});
