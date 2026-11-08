import { parseRoute, toHref, type AppRoute } from './app-route';

/** Keep the workspace URL independent of Notes filters (Notes context UC1). */
export function notesNavigationQuery(route: AppRoute, query: Record<string, string | undefined> = {}) {
  const returnTo = route.surface === 'notes'
    ? route.query.get('returnTo') ?? toHref(notesWorkspaceRoute(route).path, Object.fromEntries(notesWorkspaceRoute(route).query))
    : toHref(route.path, Object.fromEntries(route.query));
  return { ...query, returnTo: query.returnTo ?? returnTo };
}

export function notesWorkspaceRoute(route: AppRoute) {
  return parseRoute(route.query.get('returnTo') ?? toHref('/manuscript/books', {
    projectId: route.query.get('projectId') ?? undefined,
    bookId: route.query.get('bookId') ?? undefined,
  }));
}
