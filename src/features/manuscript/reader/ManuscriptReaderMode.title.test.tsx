import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../../api/mock';
import { createAppRoute } from '../../../shared/navigation/app-route';
import { holdTitlePart } from '../../../shared/navigation/page-title';
import { InShell } from '../../../app/shell/AppShell.test-helpers';
import { ManuscriptReaderMode } from './ManuscriptReaderMode';

// specs/2026-11-10-ux1-shell.md SH1.3: the reader names the chapter and the book first in the tab, the shell the saga; the
// section is held here as the root of the app holds it.
let releaseSection = () => {};
beforeEach(() => {
  Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1440 });
  releaseSection = holdTitlePart('section', 'Чтение');
});
afterEach(() => {
  cleanup();
  releaseSection();
});

it('names the chapter and the book read first in the tab', async () => {
  const api = createMockApiClient();
  const route = createAppRoute('/manuscript/books', { mode: 'read', chapterId: 'chapter-12' });
  render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })}>
    <InShell api={api} route={route}><ManuscriptReaderMode api={api} navigate={vi.fn()} route={route} /></InShell>
  </QueryClientProvider>);
  await screen.findByRole('heading', { level: 1, name: /Белый порт/u });
  await waitFor(() => expect(document.title).toMatch(/^Глава \d+\. Белый порт · [^·]+ · Чтение · Хроники Белого порта — CanonKeeper$/u));
});
