import { useContext, type ReactNode } from 'react';
import { vi } from 'vitest';
import type { createMockApiClient } from '../../api/mock';
import type { CurrentUser } from '../../entities/session/api';
import type { AppRoute, RouteNavigate } from '../../shared/navigation/app-route';
import { ToastsProvider } from '../../ui/feedback/Toast';
import { ToastsContext, type Toasts } from '../../ui/feedback/useToastQueue';
import { AppShell } from './AppShell';

/** The author of the tests of a page in its shell. */
export const shellUser = { id: 'user-mira', displayName: 'Мира', email: 'mira@example.test' } as CurrentUser;

/**
 * A page in the one shell the root puts around it (specs/2026-11-10-ux2-shell-core.md SC1.1): what the top bar, the
 * sections and the strips under the top bar say of a page is checked where they stand.
 */
export function InShell({ api, children, navigate = vi.fn(), onLogout = vi.fn(), route, toasts }: {
  api: ReturnType<typeof createMockApiClient>;
  children: ReactNode;
  navigate?: RouteNavigate;
  onLogout?: () => void;
  route: AppRoute;
  /** The toasts the test reads; without them the lines stand in the one area of the root. */
  toasts?: Toasts;
}) {
  const outer = useContext(ToastsContext);
  const shell = <AppShell api={api} navigate={navigate} onLogout={onLogout} route={route} user={shellUser}>{children}</AppShell>;
  if (toasts) return <ToastsContext.Provider value={toasts}>{shell}</ToastsContext.Provider>;
  return outer ? shell : <ToastsProvider>{shell}</ToastsProvider>;
}
