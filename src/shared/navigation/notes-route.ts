import { parseRoute, toHref, type AppRoute } from './app-route';

/** Keep the workspace URL independent of Notes filters (Notes context UC1). */
export function notesNavigationQuery(route: AppRoute, query: Record<string, string | undefined> = {}) {
  const returnTo = route.surface === 'notes'
    ? route.query.get('returnTo') ?? toHref(notesWorkspaceRoute(route).path, Object.fromEntries(notesWorkspaceRoute(route).query))
    : toHref(route.path, Object.fromEntries(route.query));
  return { ...query, returnTo: query.returnTo ?? returnTo };
}

/**
 * The place the notes were opened from: the page of `returnTo`, or the saga an older link of the notes named — its book
 * with it; a book filter alone is a book of the open saga, not a place (specs/2026-11-11-ux7-notes.md NT3.2).
 */
export function notesWorkspaceRoute(route: AppRoute) {
  const projectId = route.query.get('projectId') ?? undefined;
  return parseRoute(route.query.get('returnTo') ?? toHref('/manuscript/books', {
    projectId,
    bookId: projectId ? route.query.get('bookId') ?? undefined : undefined,
  }));
}
