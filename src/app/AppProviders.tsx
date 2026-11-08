import { MutationCache, QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { type ReactNode, useState } from 'react';
import { isEmailUnverifiedFailure, sessionQueryKeys } from '../entities/session/api';

export function createCanonKeeperQueryClient() {
  const queryClient: QueryClient = new QueryClient({
    // A refusal for an unverified address means the current user is stale: read it again
    // so every surface shows «Подтвердите адрес» (R1.4.4).
    mutationCache: new MutationCache({
      onError: (error) => {
        if (isEmailUnverifiedFailure(error)) {
          void queryClient.invalidateQueries({ queryKey: sessionQueryKeys.currentUser() });
        }
      },
    }),
    defaultOptions: {
      queries: {
        gcTime: 5 * 60 * 1000,
        retry: 1,
        staleTime: 30 * 1000,
      },
    },
  });
  return queryClient;
}

export function AppProviders({ children, queryClient: providedQueryClient }: { children: ReactNode; queryClient?: QueryClient }) {
  const [queryClient] = useState(
    () => providedQueryClient ?? createCanonKeeperQueryClient(),
  );

  return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
}
