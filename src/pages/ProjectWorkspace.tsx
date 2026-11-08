import { BookOpen, X } from 'lucide-react';
import { useState, type ReactNode } from 'react';
import { useManuscriptShelfQuery, useSelectManuscriptProject, type ManuscriptApiClient, type Project } from '../entities/manuscript/api';
import { useBookNavigation } from '../entities/manuscript/useBookNavigation';
import type { AppRoute, RouteNavigate } from '../shared/navigation/app-route';
import { useNotes } from '../shared/notes/notes-context';
import { useVoicePlace, type VoicePlace } from '../shared/navigation/voice-place';
import { Button } from '../ui/controls/Button';
import { IconButton } from '../ui/controls/IconButton';
import { Select } from '../ui/controls/Select';
import { formatProjectMeta } from '../ui/copy';
import { Alert } from '../ui/feedback/Alert';
import { EmptyState } from '../ui/feedback/EmptyState';
import { TopBarTalk } from '../ui/navigation/TopBarStrip';
import { WorkspaceTopBar } from '../ui/navigation/WorkspaceTopBar';
import { Drawer } from '../ui/overlays/Drawer';
import { ProjectNavigationItems, ProjectSidebar, type ProjectSidebarProps } from '../ui/ProjectSidebar';
import styles from './ProjectWorkspace.module.css';

const sectionPaths = { saga: '/saga', writing: '/writing', memory: '/memory', drawing: '/drawing', gallery: '/gallery' } as const;

interface ProjectWorkspaceProps {
  /** The sections stand in a narrow row at any width: the room goes to the work (specs/2026-11-05-drawing-one-screen.md D2.3). */
  narrowSections?: boolean;
  api: ManuscriptApiClient;
  route: AppRoute;
  navigate: RouteNavigate;
  onLogout?: () => void;
  userName: string;
  /** The section of the saga this page is: the map, the writing, the memory, the drawing or the gallery. */
  section: keyof typeof sectionPaths;
  /** «Карта саги», «Письмо», «Память», «Рисование», «Галерея»: the top bar and the name of the page. */
  title: string;
  label: string;
  /** What the section is made of, said when there is no saga yet. */
  emptyDescription: string;
  children: (project: Project) => ReactNode;
}

/**
 * The place of the voice call on a section without an object of its own (specs/2026-11-03-voice-assistant.md V2.4): the
 * saga map with its open node (specs/2026-11-04-saga-map.md, R6.4), the memory, the gallery. «Письмо» and «Рисование»
 * name their own.
 */
export function sectionPlace(section: keyof typeof sectionPaths, route: AppRoute): VoicePlace | null {
  if (section === 'saga') {
    const entry = route.query.get('node');
    return { page: 'saga', ...(entry ? { entry } : {}) };
  }
  if (section === 'gallery') {
    // The drawing open in the lightbox: «этот рисунок» of the voice (V5.4).
    const image = Number(route.query.get('image'));
    return { page: 'gallery', ...(Number.isInteger(image) && image > 0 ? { imageNumber: image } : {}) };
  }
  return section === 'memory' ? { page: section } : null;
}

/**
 * The workspace of a saga section (/saga, /writing, /memory, /drawing, /gallery): the top bar, the sections of the saga on the
 * side, the rail of a tablet, the drawer of a phone and the saga the URL names.
 */
export function ProjectWorkspace({ api, children, emptyDescription, label, narrowSections = false, navigate, onLogout, route, section, title, userName }: ProjectWorkspaceProps) {
  const notes = useNotes();
  const [accountMenuOpen, setAccountMenuOpen] = useState(false);
  const [menuOpen, setMenuOpen] = useState(false);
  const requestedProjectId = route.query.get('projectId');
  const shelfQuery = useManuscriptShelfQuery(api, { projectId: requestedProjectId });
  const selectProject = useSelectManuscriptProject(api);
  const shelf = shelfQuery.data;
  const project = shelf?.project && (!requestedProjectId || shelf.project.id === requestedProjectId) ? shelf.project : null;
  const bookNavigation = useBookNavigation({ api, navigate, projectId: project?.id });
  useVoicePlace(project?.id, sectionPlace(section, route));
  const close = (action: () => void) => () => { setMenuOpen(false); action(); };
  const sidebar: ProjectSidebarProps = {
    // What the helper remembers opens from the saga map, and stands under it (specs/2026-11-04-saga-map.md, R6.3).
    activeSection: section === 'memory' ? 'saga' : section,
    projectTitle: project?.title ?? 'Сага',
    projectMeta: project ? formatProjectMeta(project) : '',
    projectSelector: shelf?.projects.length ? <>
      <Select label="Сага" labelHidden value={project?.id ?? ''} loading={selectProject.isPending}
        options={shelf.projects.map((item) => ({ value: item.id, label: item.title }))}
        onValueChange={(projectId) => selectProject.mutate(projectId, { onSuccess: () => { setMenuOpen(false); navigate(sectionPaths[section], { projectId }); } })} />
      {selectProject.isError ? <Alert tone="error">Не удалось сменить сагу.</Alert> : null}
    </> : undefined,
    onOpenManuscript: close(() => navigate('/manuscript/books', { projectId: project?.id })),
    onOpenReader: close(bookNavigation.onOpenReader),
    onOpenChat: close(() => navigate('/chat', { projectId: project?.id })),
    onOpenSaga: close(() => navigate('/saga', { projectId: project?.id })),
    onOpenWriting: close(() => navigate('/writing', { projectId: project?.id })),
    onOpenDrawing: close(() => navigate('/drawing', { projectId: project?.id })),
    onOpenGallery: close(() => navigate('/gallery', { projectId: project?.id })),
    onOpenNotes: close(() => navigate('/notes')),
    onCreateNote: notes ? close(() => notes.capture({ body: '' })) : undefined,
  };

  return (
    <main className={styles.workspace} aria-label={label}>
      <WorkspaceTopBar title={title} userName={userName} accountMenuOpen={accountMenuOpen} talk={<TopBarTalk />}
        onDismissAccountMenu={() => setAccountMenuOpen(false)} onLogout={onLogout}
        onOpenMobileMenu={() => setMenuOpen(true)} onToggleAccountMenu={() => setAccountMenuOpen((open) => !open)} />
      <div className={styles.body} data-narrow-sections={narrowSections || undefined}>
        <div className={styles.desktopSidebar}><ProjectSidebar {...sidebar} /></div>
        <nav className={styles.tabletRail} aria-label="Планшетная навигация">
          <ProjectNavigationItems {...sidebar} compact />
        </nav>
        <div className={styles.content}>
          {shelfQuery.isError ? (
            <Alert tone="error"><p>Не удалось загрузить сагу.</p>
              <Button onClick={() => void shelfQuery.refetch()} variant="outline">Повторить</Button></Alert>
          ) : project ? children(project) : shelfQuery.isPending ? <p className={styles.status} role="status">Загружаем сагу…</p> : shelf?.projects.length ? (
            <p className={styles.status}>Сага недоступна: доступ к ней мог быть отозван.</p>
          ) : (
            <EmptyState
              action={<Button onClick={() => navigate('/manuscript/books', { projectId: undefined })} variant="primary">Создать сагу</Button>}
              className={styles.empty}
              description={emptyDescription}
              icon={<BookOpen size={22} />}
              title="Пока нет саги"
            />
          )}
        </div>
      </div>
      {bookNavigation.picker}
      {menuOpen ? (
        <Drawer canDismiss className={styles.projectDrawer} label="Навигация саги" onDismiss={() => setMenuOpen(false)}>
          <header><h2>Разделы</h2>
            <IconButton aria-label="Закрыть навигацию" onClick={() => setMenuOpen(false)} size="touch"><X aria-hidden="true" size={18} /></IconButton>
          </header>
          <ProjectSidebar {...sidebar} variant="drawer" />
        </Drawer>
      ) : null}
    </main>
  );
}
