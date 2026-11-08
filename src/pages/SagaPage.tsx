import { canEditProject, textSourceView, type ManuscriptApiClient } from '../entities/manuscript/api';
import { SagaMap, type SagaMapProps } from '../features/saga/SagaMap';
import type { AppRoute, RouteNavigate } from '../shared/navigation/app-route';
import { ProjectWorkspace } from './ProjectWorkspace';

interface SagaPageProps {
  api: SagaMapProps['api'] & ManuscriptApiClient;
  route: AppRoute;
  navigate: RouteNavigate;
  onLogout?: () => void;
  userName: string;
}

/** «Карта саги» (/saga, specs/2026-11-04-saga-map.md): the project workspace around the graph of the saga. */
export function SagaPage({ api, route, navigate, onLogout, userName }: SagaPageProps) {
  return (
    <ProjectWorkspace api={api} emptyDescription="Карта собирается из глав загруженных книг." label="Карта саги"
      navigate={navigate} onLogout={onLogout} route={route} section="saga" title="Карта саги" userName={userName}>
      {(project) => (
        <SagaMap api={api} canEdit={canEditProject(project)} navigate={navigate} projectId={project.id} route={route} sourceView={textSourceView} />
      )}
    </ProjectWorkspace>
  );
}
