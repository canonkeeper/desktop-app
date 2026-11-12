import { type QueryClient } from '@tanstack/react-query';
import {
  RouterProvider,
  createRoute,
  createRootRouteWithContext,
  createRouter,
  useRouter,
  useRouterState,
  type RouterHistory,
} from '@tanstack/react-router';
import { edition } from '@edition';
import { App } from '../App';
import { ClientErrorScreen } from '../app/ClientErrorScreen';
import { currentUserQueryOptions } from '../entities/session/api';
import type { CanonKeeperApiClient } from '../shared/api';
import { createAppRoute, isBackFrom, isReturnPlace, toHref, type AppRoute, type BackFrom, type RouteNavigate } from '../shared/navigation/app-route';
import { notesNavigationQuery } from '../shared/navigation/notes-route';
import { holdOverlaySteps, stepBackPastOverlays } from '../ui/overlays/overlay-history';

export interface CanonKeeperRouterContext {
  api: CanonKeeperApiClient;
  queryClient: QueryClient;
}

const rootRoute = createRootRouteWithContext<CanonKeeperRouterContext>()({
  component: RouterAppHost,
  loader: async ({ context }) => {
    await context.queryClient.ensureQueryData(currentUserQueryOptions(context.api)).catch(() => null);
  },
  notFoundComponent: RouterAppHost,
});

// One router for every edition: its routes are the table of the edition (specs/2026-11-07-desktop-app.md DA16.4).
const routeTree = rootRoute.addChildren(edition.routes.map((route) => createRoute({ getParentRoute: () => rootRoute, ...route })));

interface CanonKeeperRouterOptions {
  /** Перезагрузка страницы с экрана ошибки; тесты подменяют её. */
  reload?: () => void;
}

export function createCanonKeeperRouter(
  context: CanonKeeperRouterContext,
  history?: RouterHistory,
  options: CanonKeeperRouterOptions = {},
) {
  return createRouter({
    context,
    history,
    routeTree,
    defaultErrorComponent: ({ error }) => <ClientErrorScreen error={error} reload={options.reload} />,
  });
}

export function CanonKeeperRouterProvider({
  router,
}: {
  router: ReturnType<typeof createCanonKeeperRouter>;
}) {
  return <RouterProvider router={router} />;
}

/**
 * The route of the app at a location of the router: the address and what its history entry carries beside it — each part
 * only on the page it is for, and a place to return to only when it is one the app knows.
 */
export function routeOfLocation(location: { pathname: string; searchStr: string; state: unknown }): AppRoute {
  const route = createAppRoute(location.pathname, new URLSearchParams(location.searchStr));
  const historyState = (location.state ?? {}) as { chatSourceReturnHref?: unknown; authEmail?: unknown; sendLetter?: unknown; drawingIdea?: unknown; chatQuestion?: unknown; returnPlace?: unknown; from?: unknown; drawingList?: unknown };
  const sourceReturnHref = historyState.chatSourceReturnHref;
  const authEmail = route.surface === 'auth' && typeof historyState.authEmail === 'string' ? historyState.authEmail : undefined;
  const drawingIdea = route.surface === 'drawing' && typeof historyState.drawingIdea === 'string' ? historyState.drawingIdea : undefined;
  const chatQuestion = route.surface === 'chat' && typeof historyState.chatQuestion === 'string' ? historyState.chatQuestion : undefined;
  const returnPlace = route.surface === 'tariff' && isReturnPlace(historyState.returnPlace) ? historyState.returnPlace : undefined;
  const drawingList = route.surface === 'drawing' && route.query.has('drawing') && typeof historyState.drawingList === 'string'
    ? historyState.drawingList.slice(0, 2000) : undefined;
  return {
    ...route,
    ...(route.surface === 'chat' && route.query.has('source') && typeof sourceReturnHref === 'string' ? { sourceReturnHref } : {}),
    ...(authEmail ? { authEmail } : {}),
    ...(route.surface === 'auth' && historyState.sendLetter === true ? { sendLetter: true } : {}),
    ...(drawingIdea ? { drawingIdea } : {}),
    ...(chatQuestion ? { chatQuestion } : {}),
    ...(returnPlace ? { returnPlace } : {}),
    // The section the author came from by a link (specs/2026-11-10-ux2-patterns-states.md PS4.2).
    ...(isBackFrom(historyState.from) ? { from: historyState.from } : {}),
    // The list of «Рисунки» an open drawing came from (specs/2026-11-10-ux6-drawings-section.md DS1.9).
    ...(drawingList !== undefined ? { drawingList } : {}),
  };
}

/**
 * The way back a step carries (specs/2026-11-10-ux2-patterns-states.md PS4.1–PS4.3): the section it comes from; a page that
 * only replaces its own address keeps the one it had; a step of the menu and any other step start without one.
 */
export function backFromOfStep(options: Parameters<RouteNavigate>[2], current: BackFrom | undefined) {
  if (options?.from === null) return undefined;
  return options?.from ?? (options?.replace ? current : undefined);
}

function RouterAppHost() {
  const { api } = rootRoute.useRouteContext();
  const router = useRouter();
  const location = useRouterState({ select: (state) => state.location });
  const route = routeOfLocation(location);
  const sourceReturnHref = (location.state as { chatSourceReturnHref?: string }).chatSourceReturnHref;
  const navigate: RouteNavigate = (path, query, options) => {
    if (options?.back) {
      // From under an open window — a deletion asked in one — the step takes the entries of the windows with it.
      stepBackPastOverlays((delta) => router.history.go(delta));
      return;
    }
    const href = toHref(path, path === '/notes' ? notesNavigationQuery(route, query) : query);
    const sourceState = path === '/chat' && query?.source
      ? { chatSourceReturnHref: options?.sourceReturnHref ?? (options?.replace ? sourceReturnHref : undefined) }
      : options?.authEmail ? { authEmail: options.authEmail }
        : options?.sendLetter ? { sendLetter: true }
          : options?.drawingIdea ? { drawingIdea: options.drawingIdea }
            : options?.chatQuestion ? { chatQuestion: options.chatQuestion }
              : options?.returnPlace ? { returnPlace: options.returnPlace } : {};
    const from = backFromOfStep(options, route.from);
    // The list a drawing was opened from stays with the steps that only replace the address of a drawing (DS1.9).
    const drawingList = path === '/drawing' && query?.drawing ? options?.drawingList ?? (options?.replace ? route.drawingList : undefined) : undefined;
    const state = { ...sourceState, ...(from ? { from } : {}), ...(drawingList !== undefined ? { drawingList } : {}) };
    // A window that closes by the same press steps back only after this page is in the history (specs/2026-11-10-ux2-patterns-actions.md UC4).
    holdOverlaySteps(href);
    if (options?.replace) {
      router.history.replace(href, state);
      return;
    }
    router.history.push(href, state);
  };

  return <App api={api} navigate={navigate} route={route} />;
}

declare module '@tanstack/react-router' {
  interface Register {
    router: ReturnType<typeof createCanonKeeperRouter>;
  }
}
