import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { renderHook, waitFor } from '@testing-library/react';
import type { PropsWithChildren } from 'react';
import { expect, it } from 'vitest';
import { createMockApiClient } from '../../../api/mock';
import { useManuscriptShelfQuery } from './queries';

it.each([['', null], [null, '']] as const)(
  'keeps distinct fresh shelf caches when moving from project %j to %j', async (first, next) => {
    const api = createMockApiClient();
    const client = new QueryClient({ defaultOptions: { queries: { staleTime: 30_000, retry: false } } });
    const wrapper = ({ children }: PropsWithChildren) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
    const { result, rerender } = renderHook(({ projectId }: { projectId: string | null }) =>
      useManuscriptShelfQuery(api, { projectId }), { wrapper, initialProps: { projectId: first } });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    rerender({ projectId: next });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    await waitFor(() => expect(Boolean(result.current.data?.books.length)).toBe(next === null));
    expect(Boolean(result.current.data?.project)).toBe(next === null);
  },
);

it('keeps the shelf while the address takes the id of the saga it already shows, and never another saga', async () => {
  const api = createMockApiClient();
  const other = await api.createProject({ title: 'Другая сага' });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const wrapper = ({ children }: PropsWithChildren) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  const { result, rerender } = renderHook(({ projectId }: { projectId: string | null }) =>
    useManuscriptShelfQuery(api, { projectId }), { wrapper, initialProps: { projectId: null as string | null } });
  await waitFor(() => expect(result.current.isSuccess).toBe(true));
  const shown = result.current.data?.project?.id ?? '';
  expect(shown).toBeTruthy();
  expect(shown).not.toBe(other.id);

  rerender({ projectId: shown });
  // At once, before the shelf of the id answers: the page under it stays.
  expect(result.current.data?.project?.id).toBe(shown);
  expect(result.current.isPending).toBe(false);
  await waitFor(() => expect(result.current.isPlaceholderData).toBe(false));
  expect(result.current.data?.project?.id).toBe(shown);

  rerender({ projectId: other.id });
  expect(result.current.data).toBeUndefined();
  await waitFor(() => expect(result.current.data?.project?.id).toBe(other.id));
});
