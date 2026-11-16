import type { OperationRequest, OperationResponse, Schema } from '../../../shared/api';
import { MockApiError } from '../scenarios';
import type { MockRepositoryContext } from './context';

type DesktopAppKind = Schema<'DesktopAppFile'>['kind'];

/** The open version of the mirror of the site the mock serves: the sizes of the board 44·74 in bytes. */
const mirror: { version: string; files: { kind: DesktopAppKind; name: string; bytes: number }[] } = {
  version: '0.1.0',
  files: [
    { kind: 'windows', name: 'CanonKeeper_0.1.0_x64-setup.exe', bytes: 82 * 1048576 },
    { kind: 'deb', name: 'CanonKeeper_0.1.0_amd64.deb', bytes: 120 * 1048576 },
    { kind: 'rpm', name: 'CanonKeeper-0.1.0-1.x86_64.rpm', bytes: 121 * 1048576 },
    { kind: 'appimage', name: 'CanonKeeper_0.1.0_amd64.AppImage', bytes: 176 * 1048576 },
  ],
};

/**
 * The page «CanonKeeper на компьютере» and its switch as the backend keeps them (specs/2026-11-16-desk-app-page.md AP1): the
 * page is shown only while the switch is on and the mirror has a version; the switch is the administrator's.
 */
export function createDesktopAppOperations(context: MockRepositoryContext) {
  const opened = () => (context.getScenario().desktopAppNoMirror === true ? null : mirror);

  function admin(): OperationResponse<'getAdminDesktopApp', '200'> {
    const version = opened();
    return {
      shown: context.state.desktopAppShown,
      mirror: version ? { version: version.version, files: version.files.map(({ bytes, kind }) => ({ kind, bytes })) } : null,
    };
  }

  function adminOperation(operationId: 'getAdminDesktopApp' | 'setAdminDesktopApp') {
    context.applyOperation(operationId);
    if (!context.state.session) throw new MockApiError(401, 'Сессия истекла. Войдите снова.', operationId, 'auth.session_required');
    if (context.state.session.user.impersonation) throw new MockApiError(403, 'Forbidden', operationId, 'admin.forbidden');
  }

  async function getDesktopApp(): Promise<OperationResponse<'getDesktopApp', '200'>> {
    context.applyOperation('getDesktopApp');
    const version = opened();
    if (!context.state.desktopAppShown || !version) return { shown: false };
    return {
      shown: true,
      version: version.version,
      files: version.files.map(({ bytes, kind, name }) => ({ kind, url: `/app/open/${version.version}/${name}`, bytes })),
    };
  }

  async function getAdminDesktopApp(): Promise<OperationResponse<'getAdminDesktopApp', '200'>> {
    adminOperation('getAdminDesktopApp');
    return admin();
  }

  async function setAdminDesktopApp(body: OperationRequest<'setAdminDesktopApp'>): Promise<OperationResponse<'setAdminDesktopApp', '200'>> {
    adminOperation('setAdminDesktopApp');
    if (typeof body.shown !== 'boolean') throw new MockApiError(400, 'validation failed', 'setAdminDesktopApp', 'request.validation');
    context.state.desktopAppShown = body.shown;
    return admin();
  }

  return { getDesktopApp, getAdminDesktopApp, setAdminDesktopApp };
}
