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
import { createAppRoute, toHref, type RouteNavigate } from '../shared/navigation/app-route';
import { notesNavigationQuery } from '../shared/navigation/notes-route';

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

function RouterAppHost() {
  const { api } = rootRoute.useRouteContext();
  const router = useRouter();
  const location = useRouterState({ select: (state) => state.location });
  const route = createAppRoute(location.pathname, new URLSearchParams(location.searchStr));
  const historyState = location.state as { chatSourceReturnHref?: string; authEmail?: string; drawingIdea?: string; chatQuestion?: string };
  const sourceReturnHref = historyState.chatSourceReturnHref;
  const authEmail = route.surface === 'auth' && typeof historyState.authEmail === 'string' ? historyState.authEmail : undefined;
  const drawingIdea = route.surface === 'drawing' && typeof historyState.drawingIdea === 'string' ? historyState.drawingIdea : undefined;
  const chatQuestion = route.surface === 'chat' && typeof historyState.chatQuestion === 'string' ? historyState.chatQuestion : undefined;
  const chatRoute = {
    ...route,
    ...(route.surface === 'chat' && route.query.has('source') && typeof sourceReturnHref === 'string' ? { sourceReturnHref } : {}),
    ...(authEmail ? { authEmail } : {}),
    ...(drawingIdea ? { drawingIdea } : {}),
    ...(chatQuestion ? { chatQuestion } : {}),
  };
  const navigate: RouteNavigate = (path, query, options) => {
    if (options?.back) {
      router.history.back();
      return;
    }
    const href = toHref(path, path === '/notes' ? notesNavigationQuery(route, query) : query);
    const sourceState = path === '/chat' && query?.source
      ? { chatSourceReturnHref: options?.sourceReturnHref ?? (options?.replace ? sourceReturnHref : undefined) }
      : options?.authEmail ? { authEmail: options.authEmail }
        : options?.drawingIdea ? { drawingIdea: options.drawingIdea }
          : options?.chatQuestion ? { chatQuestion: options.chatQuestion } : {};
    if (options?.replace) {
      router.history.replace(href, sourceState);
      return;
    }
    router.history.push(href, sourceState);
  };

  return <App api={api} navigate={navigate} route={chatRoute} />;
}

declare module '@tanstack/react-router' {
  interface Register {
    router: ReturnType<typeof createCanonKeeperRouter>;
  }
}
