import { vi } from 'vitest';
import { createMockApiClient } from '../api/mock/repository';
import { createCanonKeeperApiClient } from '../shared/api';
import { createApiHandlers, mswApiBaseUrl } from './msw/handlers';
import { mswServer } from './msw/server';

/**
 * The real API client against the MSW mocks of the illustrations (specs/2026-10-29-saga-illustrations.md
 * UC8, UC11), signed in as the owner of the synthetic saga, with the repository behind them to set
 * the right to illustrations or read what the web changed.
 */
export function createIllustrationTestApi() {
  const repository = createMockApiClient();
  mswServer.use(...createApiHandlers({ repository }));
  const api = createCanonKeeperApiClient({
    baseUrl: mswApiBaseUrl,
    fetch: async (request) => {
      const headers = new Headers(request.headers);
      headers.set('cookie', 'ck_session=mock-session');
      headers.set('origin', 'http://localhost:3000');
      headers.set('x-csrf-token', 'ck_msw_csrf_token_000000');
      return fetch(request, { headers });
    },
  });
  // A multipart upload does not go through the fetch above, which signs the requests of the test.
  vi.spyOn(api, 'uploadProjectImage').mockImplementation((id, input, options) => repository.uploadProjectImage(id, input, options));
  return { api, repository };
}

type Repository = ReturnType<typeof createMockApiClient>;
type Access = Awaited<ReturnType<Repository['getIllustrationAccess']>>;

/**
 * Shows the signed-in author another right than the mock keeps — active, blocked — while the
 * drawings themselves still go through the mock's own trial (the mock's admin writers are other
 * accounts than its signed-in author).
 */
export function overrideIllustrationAccess(repository: Repository, patch: Partial<Access>) {
  const read = repository.getIllustrationAccess.bind(repository);
  return vi.spyOn(repository, 'getIllustrationAccess').mockImplementation(async () => ({ ...(await read()), ...patch }));
}

/** Spends the trial drawings of the signed-in author on variants of drawing 1: the right becomes none. */
export async function spendTrialDrawings(repository: Repository, projectId: string) {
  const access = await repository.getIllustrationAccess();
  for (let index = 0; index < access.trialLeft; index += 1) {
    await repository.createProjectImageVariant(projectId, 1, {}, { idempotencyKey: `idem-test-spend-trial-${index}` });
  }
}
