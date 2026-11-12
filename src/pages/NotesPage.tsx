import { useManuscriptShelfQuery, type ManuscriptApiClient } from '../entities/manuscript/api';
import type { NotesApiClient } from '../entities/notes/api';
import type { CurrentUser } from '../entities/session/api';
import { NotesWorkspace } from '../features/notes/NotesWorkspace';
import type { AppRoute, RouteNavigate } from '../shared/navigation/app-route';
import { notesWorkspaceRoute } from '../shared/navigation/notes-route';
import { useVoicePlace } from '../shared/navigation/voice-place';
import styles from './NotesPage.module.css';

interface NotesPageProps {
  apiClient: NotesApiClient & ManuscriptApiClient;
  user: CurrentUser;
  route: AppRoute;
  navigate: RouteNavigate;
}

/**
 * «Заметки» (/notes): the notes of the saga of the shell — the saga of the place the notes were opened from
 * (specs/2026-11-10-ux2-shell-core.md SC1.3) — with the own thoughts; «Все саги» is a choice of the catalog
 * (specs/2026-11-11-ux7-notes.md NT3.2). The voice call knows the notes there.
 */
export function NotesPage({ apiClient, user, route, navigate }: NotesPageProps) {
  const requestedProjectId = notesWorkspaceRoute(route).query.get('projectId');
  const shelf = useManuscriptShelfQuery(apiClient, { projectId: requestedProjectId }).data;
  const project = shelf?.project && (!requestedProjectId || shelf.project.id === requestedProjectId) ? shelf.project : null;
  useVoicePlace(project?.id, { page: 'notes' });
  return (
    <div className={styles.content}>
      <NotesWorkspace api={apiClient} accountId={user.id} route={route} navigate={navigate} />
    </div>
  );
}
