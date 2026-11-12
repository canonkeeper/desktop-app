import { useEffect } from 'react';
import type { AppRoute, RouteNavigate } from '../../shared/navigation/app-route';
import { ManuscriptReaderMode, type ReaderApi } from './reader/ManuscriptReaderMode';
import { ManuscriptShelfPage } from './shelf/ManuscriptShelfPage';
import { isReaderRoute } from '../../ui/shell/shell-routes';

interface ManuscriptPageProps {
  /** The shelf and the reader; the reader also reads the notes of the author to the chapter. */
  api: ReaderApi;
  navigate: RouteNavigate;
  route: AppRoute;
}

/** «Книги» of the saga: the shelf, or the reader of one of its books by the address. */
export function ManuscriptPage({ api, navigate, route }: ManuscriptPageProps) {
  const manuscriptMode = route.query.get('mode');

  // Редактора больше нет: сохранённые ссылки редактора открывают тот же текст в читалке.
  useEffect(() => {
    if (manuscriptMode !== 'draft') return;
    navigate(route.path, { ...Object.fromEntries(route.query), mode: 'read' }, { replace: true });
  }, [manuscriptMode, navigate, route.path, route.query]);

  if (manuscriptMode === 'draft') return null;
  if (isReaderRoute(route)) return <ManuscriptReaderMode api={api} navigate={navigate} route={route} />;
  return <ManuscriptShelfPage api={api} navigate={navigate} route={route} />;
}
