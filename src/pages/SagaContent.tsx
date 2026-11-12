import { loadFailureWords } from '../entities/api-errors';
import type { ReactNode } from 'react';
import { useManuscriptShelfQuery, type ManuscriptApiClient, type Project } from '../entities/manuscript/api';
import type { AppRoute, RouteNavigate } from '../shared/navigation/app-route';
import { useVoicePlace, type VoicePlace } from '../shared/navigation/voice-place';
import { ErrorBlock } from '../ui/feedback/ErrorBlock';
import { SagaMissing } from '../ui/feedback/SagaMissing';
import { SkeletonArea } from '../ui/feedback/Skeleton';
import styles from './SagaContent.module.css';

type SagaSectionPage = 'saga' | 'writing' | 'memory' | 'drawing';

/** What did not open, in the words of the section (PR3.1). */
const notOpened: Record<SagaSectionPage, string> = {
  saga: 'Не открылась карта саги',
  writing: 'Не открылось «Письмо»',
  memory: 'Не открылось «Что помнит помощник»',
  drawing: 'Не открылись «Рисунки»',
};

/**
 * The place of the voice call on a section without an object of its own (specs/2026-11-03-voice-assistant.md V2.4): the
 * saga map with its open node (specs/2026-11-04-saga-map.md, R6.4), the memory. «Письмо» and «Рисунки» name their own.
 */
export function sectionPlace(section: SagaSectionPage, route: AppRoute): VoicePlace | null {
  if (section === 'saga') {
    const entry = route.query.get('node');
    return { page: 'saga', ...(entry ? { entry } : {}) };
  }
  return section === 'memory' ? { page: section } : null;
}

interface SagaContentProps {
  api: ManuscriptApiClient;
  route: AppRoute;
  navigate: RouteNavigate;
  /** The section of the saga this page is: the map, the writing, the memory or the drawings. */
  section: SagaSectionPage;
  children: (project: Project) => ReactNode;
}

/**
 * The content of a section of the saga the address names (specs/2026-11-10-ux2-shell-core.md SC1.2): the section once the
 * saga is read — by the same query of the shelf the shell reads — or why it is not there. The top bar, the sections and
 * the saga are the shell's; an author without a saga is on the first screen, where the shell leads him
 * (specs/2026-11-10-ux3-first-screen.md FS1.1).
 */
export function SagaContent({ api, children, navigate, route, section }: SagaContentProps) {
  const requestedProjectId = route.query.get('projectId');
  const shelfQuery = useManuscriptShelfQuery(api, { projectId: requestedProjectId });
  const shelf = shelfQuery.data;
  const project = shelf?.project && (!requestedProjectId || shelf.project.id === requestedProjectId) ? shelf.project : null;
  useVoicePlace(project?.id, sectionPlace(section, route));
  // The one block of an error takes the place of the content; the shell, the section and the saga stay
  // (specs/2026-11-10-ux2-patterns-refusals.md PR3.1, PR3.2, PR3.5).
  if (shelfQuery.isError) {
    return (
      <ErrorBlock className={styles.state} {...loadFailureWords(shelfQuery.error)} onRetry={() => void shelfQuery.refetch()}
        retrying={shelfQuery.isRefetching} title={notOpened[section]} />
    );
  }
  if (project) return children(project);
  // While the saga is read: the form of what comes (PS3.1). The heading is the section's own, one element from its first
  // moment, so the focus that a change of the page brings to it stays there (specs/2026-11-10-ux1-shell.md SH2).
  if (shelfQuery.isPending) return <div className={styles.state}><SkeletonArea shape="cards" /></div>;
  // A saga that is not his: a calm block with his sagas, each opening this same section (specs/2026-11-11-ux9-shelf-chat.md SC1.9).
  return (
    <div className={styles.state}>
      <SagaMissing onChoose={(id) => navigate(route.path, { projectId: id })} sagas={shelf?.projects ?? []} />
    </div>
  );
}
