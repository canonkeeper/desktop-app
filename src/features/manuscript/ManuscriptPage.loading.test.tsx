import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../api/mock';
import { parseRoute } from '../../shared/navigation/app-route';
import { ManuscriptPage } from './ManuscriptPage';
import { ToastsProvider } from '../../ui/feedback/Toast';

const draftModuleLoad = vi.hoisted(() => ({ count: 0 }));

vi.mock('./draft/ManuscriptDraftMode', async () => {
  draftModuleLoad.count += 1;
  return vi.importActual('./draft/ManuscriptDraftMode');
});

it('does not preload the Draft chunk in reader mode', async () => {
  const queryClient = new QueryClient({
    defaultOptions: { mutations: { retry: false }, queries: { retry: false } },
  });
  const route = parseRoute(new URL('http://localhost/manuscript/books?mode=read&projectId=project-white-port&bookId=book-02&chapterId=chapter-12&paragraphId=p-12-03'));

  render(
    <QueryClientProvider client={queryClient}><ToastsProvider>
      <ManuscriptPage api={createMockApiClient()} navigate={vi.fn()} route={route} />
    </ToastsProvider></QueryClientProvider>,
  );

  expect(await screen.findByRole('heading', { level: 1, name: 'Глава 12. Белый порт' })).toBeTruthy();
  await vi.dynamicImportSettled();
  expect(draftModuleLoad.count).toBe(0);
});
