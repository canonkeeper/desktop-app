import { canEditProject, type ManuscriptApiClient } from '../entities/manuscript/api';
import { AuthorMemory, type AuthorMemoryProps } from '../features/author-memory/AuthorMemory';
import { MaterialsPart, type MaterialsPartProps } from '../features/saga/materials/MaterialsPart';
import { useSagaSlot } from '../features/saga/saga-slot';
import type { AppRoute, RouteNavigate } from '../shared/navigation/app-route';
import { Tab, Tabs } from '../ui/navigation/Tabs';
import { SagaContent } from './SagaContent';
import styles from './AuthorMemoryPage.module.css';

interface AuthorMemoryPageProps {
  api: AuthorMemoryProps['api'] & MaterialsPartProps['api'] & ManuscriptApiClient;
  route: AppRoute;
  navigate: RouteNavigate;
}

type MemoryPart = 'memory' | 'materials';

const parts: ReadonlyArray<{ part: MemoryPart; label: string }> = [
  { part: 'memory', label: 'Вы просили помнить' },
  { part: 'materials', label: 'Ваши материалы' },
];

/** The page without the import of the site: nothing is read and nothing waits for the author. */
function useNothingWaits(_options: { canEdit: boolean; projectId: string }) {
  return { running: false, waiting: 0 };
}

/**
 * «Что помнит помощник» (/memory, specs/2026-11-10-ux2-memory-page.md MP1.1): a section of the menu in the group «Сага»
 * (specs/2026-11-10-ux2-shell.md S2.1, S2.3), one page with two parts — what the author asked to remember and his materials.
 * The open part lives in the address.
 */
export function AuthorMemoryPage({ api, route, navigate }: AuthorMemoryPageProps) {
  return (
    <SagaContent api={api} navigate={navigate} route={route} section="memory">
      {(project) => <MemoryParts api={api} canEdit={canEditProject(project)} navigate={navigate} projectId={project.id} route={route} />}
    </SagaContent>
  );
}

function MemoryParts({ api, canEdit, navigate, projectId, route }: AuthorMemoryPageProps & { canEdit: boolean; projectId: string }) {
  const open: MemoryPart = route.query.get('part') === 'materials' ? 'materials' : 'memory';
  const useWaiting = useSagaSlot()?.materialsImport?.useWaiting ?? useNothingWaits;
  // The tab says the file is read and how many records wait (specs/2026-11-11-ux9-shelf-chat.md SC3.6, М-04).
  const { running, waiting } = useWaiting({ canEdit, projectId });
  const materialsNote = waiting > 0 ? `${waiting} на проверку` : running ? 'разбирается…' : null;
  // The saga stays; the filters of the records belong to their part.
  const show = (part: MemoryPart) => navigate('/memory', { projectId, ...(part === 'materials' ? { part } : {}) }, { replace: true });
  return (
    <div className={styles.page}>
      <header className={styles.header}>
        <h1 className={styles.title} data-page-heading="" tabIndex={-1}>Что помнит помощник</h1>
        <p className={styles.lead}>Помощник учитывает это в ответах и при письме глав. Заметки он не читает.</p>
      </header>
      <Tabs className={styles.tabs} label="Части страницы">
        {parts.map(({ part, label }) => (
          <Tab aria-controls="memory-part" id={`memory-tab-${part}`} key={part} onClick={() => show(part)} selected={open === part}>
            {label}
            {part === 'materials' && materialsNote ? <span className={styles.waiting}> · {materialsNote}</span> : null}
          </Tab>
        ))}
      </Tabs>
      <section aria-labelledby={`memory-tab-${open}`} className={styles.part} id="memory-part" role="tabpanel">
        {open === 'memory'
          ? <AuthorMemory api={api} canEdit={canEdit} navigate={navigate} projectId={projectId} route={route} />
          : <MaterialsPart api={api} canEdit={canEdit} navigate={navigate} projectId={projectId} query={route.query.get('q') ?? undefined} />}
      </section>
    </div>
  );
}
