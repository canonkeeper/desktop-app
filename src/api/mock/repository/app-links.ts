import type { CanonKeeperApiClient } from '../../../shared/api';
import { MockApiError } from '../scenarios';
import type { MockRepositoryContext } from './context';

type AppLinkOperations = Pick<CanonKeeperApiClient, 'startAppLink' | 'listLinkedApps' | 'revokeLinkedApp'>;

/** At most five apps of an account are linked at once (specs/2026-11-13-desk-link.md LK1.4). */
const maxLinkedApps = 5;

/**
 * The link of the CanonKeeper app with the account (specs/2026-11-13-desk-link.md): the page /link confirms it and gets
 * the address of the return on 127.0.0.1 the server builds from the port. The mock has no app to change the code for
 * its pass, so the confirmation links the app at once, the way the app would a moment later; «Аккаунт → Приложения на
 * компьютере» lists the live apps of the account and unlinks one. The apps are kept in the state of the mock, each
 * account its own.
 */
export function createAppLinkOperations(context: MockRepositoryContext): AppLinkOperations {
  return {
    async startAppLink(body) {
      context.applyOperation('startAppLink');
      const request = (body ?? {}) as Record<string, unknown>;
      const valid = typeof request.challenge === 'string' && /^[A-Za-z0-9_-]{43}$/u.test(request.challenge) &&
        typeof request.state === 'string' && /^[A-Za-z0-9._~-]{1,128}$/u.test(request.state) &&
        Number.isInteger(request.port) && Number(request.port) >= 20000 && Number(request.port) <= 29999 &&
        (request.platform === 'windows' || request.platform === 'linux') && Object.keys(request).length === 4;
      if (!valid) throw new MockApiError(400, 'validation failed', 'startAppLink', 'request.validation');
      // As the server: an address not confirmed links no app (LK1.4).
      if (context.state.session && !context.state.session.user.emailVerified) {
        throw new MockApiError(403, 'email address is not verified', 'startAppLink', 'auth.email_unverified');
      }
      const account = context.currentPrincipalId();
      if (context.state.linkedApps.filter((app) => app.userId === account).length >= maxLinkedApps) {
        throw new MockApiError(409, 'the account has as many linked apps as it may', 'startAppLink', 'app.too_many_apps');
      }
      const platform = request.platform as 'windows' | 'linux';
      const serial = context.state.linkedApps.reduce((last, app) => Math.max(last, Number(app.id.slice(-12))), 0) + 1;
      context.state.linkedApps = [...context.state.linkedApps, {
        userId: account, id: `9a3e0c52-0000-4000-8000-${String(serial).padStart(12, '0')}`,
        name: platform === 'windows' ? 'Компьютер с Windows' : 'Компьютер с Linux', platform,
        appVersion: '1.0.0', linkedAt: new Date().toISOString(), seenAt: null,
      }];
      const query = new URLSearchParams({ code: 'mock_link_code_00000000000000000000000000000', state: String(request.state) });
      return {
        callbackUrl: `http://127.0.0.1:${Number(request.port)}/desktop/link/callback?${query.toString()}`,
        expiresAt: new Date(Date.now() + 5 * 60_000).toISOString(),
      };
    },
    async listLinkedApps() {
      context.applyOperation('listLinkedApps');
      const account = context.currentPrincipalId();
      const apps = context.state.linkedApps.filter((app) => app.userId === account).reverse();
      return { apps: apps.map(({ id, name, platform, appVersion, linkedAt, seenAt }) => ({ id, name, platform, appVersion, linkedAt, seenAt })) };
    },
    async revokeLinkedApp(appId) {
      context.applyOperation('revokeLinkedApp');
      const account = context.currentPrincipalId();
      if (!context.state.linkedApps.some((app) => app.userId === account && app.id === appId)) {
        throw new MockApiError(404, 'no such linked app', 'revokeLinkedApp', 'app.not_found');
      }
      context.state.linkedApps = context.state.linkedApps.filter((app) => app.id !== appId);
    },
  };
}
