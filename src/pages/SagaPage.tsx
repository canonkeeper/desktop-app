import { canEditProject, textSourceView, type ManuscriptApiClient } from '../entities/manuscript/api';
import { SagaMap, type SagaMapProps } from '../features/saga/SagaMap';
import type { AppRoute, RouteNavigate } from '../shared/navigation/app-route';
import { SagaContent } from './SagaContent';

interface SagaPageProps {
  api: SagaMapProps['api'] & ManuscriptApiClient;
  route: AppRoute;
  navigate: RouteNavigate;
}

/** «Карта» (/saga, specs/2026-11-04-saga-map.md): the graph of the saga of the address. */
export function SagaPage({ api, route, navigate }: SagaPageProps) {
  return (
    <SagaContent api={api} navigate={navigate} route={route} section="saga">
      {(project) => (
        <SagaMap api={api} canEdit={canEditProject(project)} navigate={navigate} projectId={project.id} route={route} sourceView={textSourceView} />
      )}
    </SagaContent>
  );
}
