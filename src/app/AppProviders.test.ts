import { describe, expect, it, vi } from 'vitest';
import { ApiStatusError } from '../shared/api';
import { sessionQueryKeys } from '../entities/session/api';
import { createCanonKeeperQueryClient } from './AppProviders';

function refusal(code: string) {
  return new ApiStatusError(403, { type: 'about:blank', title: 'Forbidden', status: 403, code, requestId: 'req' }, new Response(null, { status: 403 }));
}

describe('createCanonKeeperQueryClient', () => {
  // R1.4.4, R5.6: a refusal for an unverified address reads the current user again.
  it('invalidates the current user after 403 auth.email_unverified only', async () => {
    const queryClient = createCanonKeeperQueryClient();
    const invalidate = vi.spyOn(queryClient, 'invalidateQueries');
    const run = (code: string) => queryClient.getMutationCache()
      .build(queryClient, { mutationFn: () => Promise.reject(refusal(code)), retry: false })
      .execute(undefined)
      .catch(() => undefined);

    await run('forbidden');
    expect(invalidate).not.toHaveBeenCalled();
    await run('auth.email_unverified');
    expect(invalidate).toHaveBeenCalledWith({ queryKey: sessionQueryKeys.currentUser() });
  });
});
