import { useEffect } from 'react';
import type { ManuscriptApiClient } from '../../entities/manuscript/api';
import type { AppRoute, RouteNavigate } from '../../shared/navigation/app-route';
import { ManuscriptReaderMode } from './reader/ManuscriptReaderMode';
import { ManuscriptShelfPage } from './shelf/ManuscriptShelfPage';

interface ManuscriptPageProps {
  api: ManuscriptApiClient;
  navigate: RouteNavigate;
  onLogout?: () => void;
  route: AppRoute;
  userName: string;
}

export function ManuscriptPage({ api, navigate, onLogout, route, userName }: ManuscriptPageProps) {
  const manuscriptMode = route.query.get('mode');

  // Редактора больше нет: сохранённые ссылки редактора открывают тот же текст в читалке.
  useEffect(() => {
    if (manuscriptMode !== 'draft') return;
    navigate(route.path, { ...Object.fromEntries(route.query), mode: 'read' }, { replace: true });
  }, [manuscriptMode, navigate, route.path, route.query]);

  if (manuscriptMode === 'draft') return null;
  if (manuscriptMode === 'read' || route.query.has('source') || route.query.has('chatId')) {
    return <ManuscriptReaderMode api={api} navigate={navigate} onLogout={onLogout} route={route} userName={userName} />;
  }
  return <ManuscriptShelfPage api={api} navigate={navigate} onLogout={onLogout} route={route} userName={userName} />;
}
