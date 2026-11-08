import { canEditProject, type ManuscriptApiClient } from '../entities/manuscript/api';
import { AuthorMemory, type AuthorMemoryProps } from '../features/author-memory/AuthorMemory';
import type { AppRoute, RouteNavigate } from '../shared/navigation/app-route';
import { ProjectWorkspace } from './ProjectWorkspace';

interface AuthorMemoryPageProps {
  api: AuthorMemoryProps['api'] & ManuscriptApiClient;
  route: AppRoute;
  navigate: RouteNavigate;
  onLogout?: () => void;
  userName: string;
}

/**
 * What the helper remembers (/memory, specs/2026-11-01-writer-assistant-index.md, R4.5): the background of the helper,
 * opened from the reference and not a section of the menu (specs/2026-11-02-writer-workshop.md, Decision log 03.11).
 */
export function AuthorMemoryPage({ api, route, navigate, onLogout, userName }: AuthorMemoryPageProps) {
  return (
    <ProjectWorkspace api={api} emptyDescription="Память хранит то, что вы сказали или одобрили о своей саге." label="Что помнит помощник"
      navigate={navigate} onLogout={onLogout} route={route} section="memory" title="Что помнит помощник" userName={userName}>
      {(project) => (
        <AuthorMemory api={api} canEdit={canEditProject(project)} navigate={navigate} projectId={project.id} route={route} />
      )}
    </ProjectWorkspace>
  );
}
