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
