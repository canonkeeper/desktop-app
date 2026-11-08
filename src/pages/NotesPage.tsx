import { X } from 'lucide-react';
import { useState } from 'react';
import { useManuscriptShelfQuery, useSelectManuscriptProject, type ManuscriptApiClient } from '../entities/manuscript/api';
import { useBookNavigation } from '../entities/manuscript/useBookNavigation';
import type { NotesApiClient } from '../entities/notes/api';
import type { CurrentUser } from '../entities/session/api';
import { NotesWorkspace } from '../features/notes/NotesWorkspace';
import { toHref, type AppRoute, type RouteNavigate } from '../shared/navigation/app-route';
import { notesWorkspaceRoute } from '../shared/navigation/notes-route';
import { useNotesViewport } from '../features/notes/useNotesViewport';
import { useNotes } from '../shared/notes/notes-context';
import { Button } from '../ui/controls/Button';
import { IconButton } from '../ui/controls/IconButton';
import { Select } from '../ui/controls/Select';
import { formatProjectMeta } from '../ui/copy';
import { Alert } from '../ui/feedback/Alert';
import { useVoicePlace } from '../shared/navigation/voice-place';
import { TopBarTalk } from '../ui/navigation/TopBarStrip';
import { WorkspaceTopBar } from '../ui/navigation/WorkspaceTopBar';
import { Drawer } from '../ui/overlays/Drawer';
import { ProjectNavigationItems, ProjectSidebar, type ProjectSidebarProps } from '../ui/ProjectSidebar';
import styles from './NotesPage.module.css';

interface NotesPageProps {
  apiClient: NotesApiClient & ManuscriptApiClient;
  user: CurrentUser;
  route: AppRoute;
  navigate: RouteNavigate;
  onLogout?: () => void;
}

export function NotesPage({ apiClient, user, route, navigate, onLogout }: NotesPageProps) {
  const notes = useNotes();
  const viewport = useNotesViewport();
  const [accountMenuOpen, setAccountMenuOpen] = useState(false);
  const [menuOpen, setMenuOpen] = useState(false);
  const workspaceRoute = notesWorkspaceRoute(route);
  const requestedProjectId = workspaceRoute.query.get('projectId');
  const requestedBookId = workspaceRoute.query.get('bookId');
  const shelfQuery = useManuscriptShelfQuery(apiClient, { projectId: requestedProjectId });
  const selectProject = useSelectManuscriptProject(apiClient);
  const shelf = shelfQuery.data;
  const project = shelf?.project && (!requestedProjectId || shelf.project.id === requestedProjectId) ? shelf.project : null;
  const navigationProjectId = requestedProjectId ?? project?.id;
  useVoicePlace(project?.id, { page: 'notes' });
  const workspaceQuery = { ...Object.fromEntries(workspaceRoute.query), projectId: navigationProjectId };
  const bookNavigation = useBookNavigation({
    api: apiClient, navigate, projectId: project?.id,
    bookId: project && shelf?.books.some((book) => book.id === requestedBookId) ? requestedBookId ?? undefined : undefined,
    chapterId: workspaceRoute.query.get('chapterId') ?? undefined,
  });
  const sidebar: ProjectSidebarProps = {
    activeSection: 'notes',
    projectTitle: project?.title ?? 'Сага',
    projectMeta: project ? formatProjectMeta(project) : 'Все заметки',
    projectSelector: shelf?.projects.length ? <><Select label="Сага" labelHidden value={navigationProjectId ?? ''} loading={selectProject.isPending}
      options={shelf.projects.map((item) => ({ value: item.id, label: item.title }))}
      onValueChange={(projectId) => {
        selectProject.mutate(projectId, { onSuccess: () => {
          setMenuOpen(false);
          navigate('/notes', { ...Object.fromEntries(route.query), returnTo: toHref('/manuscript/books', { projectId }) });
        } });
      }} />{selectProject.isError ? <Alert tone="error">Не удалось сменить сагу.</Alert> : null}</> : undefined,
    onOpenManuscript: () => { setMenuOpen(false); navigate('/manuscript/books', { projectId: navigationProjectId }); },
    onOpenChat: () => { setMenuOpen(false); navigate('/chat', workspaceRoute.surface === 'chat' ? workspaceQuery : { projectId: navigationProjectId }); },
    onOpenReader: () => { setMenuOpen(false); if (workspaceRoute.surface === 'manuscript' && workspaceRoute.query.get('mode') === 'read') navigate('/manuscript/books', workspaceQuery); else bookNavigation.onOpenReader(); },
    onOpenNotes: () => { setMenuOpen(false); navigate('/notes'); },
    onOpenSaga: () => { setMenuOpen(false); navigate('/saga', { projectId: navigationProjectId }); },
    onOpenWriting: () => { setMenuOpen(false); navigate('/writing', { projectId: navigationProjectId }); },
    onOpenDrawing: () => { setMenuOpen(false); navigate('/drawing', { projectId: navigationProjectId }); },
    onOpenGallery: () => { setMenuOpen(false); navigate('/gallery', { projectId: navigationProjectId }); },
    onCreateNote: notes ? () => { setMenuOpen(false); notes.capture({ body: '' }); } : undefined,
  };

  return (
    <main className={styles.workspace} aria-label="Страница заметок">
      <WorkspaceTopBar compact={viewport.short} title="Заметки" userName={user.displayName} accountMenuOpen={accountMenuOpen} talk={<TopBarTalk />}
        onDismissAccountMenu={() => setAccountMenuOpen(false)} onLogout={onLogout}
        onOpenMobileMenu={() => setMenuOpen(true)} onToggleAccountMenu={() => setAccountMenuOpen((open) => !open)} />
      <div className={styles.body}>
        <div className={styles.desktopSidebar}><ProjectSidebar {...sidebar} /></div>
        <nav className={styles.tabletRail} aria-label="Планшетная навигация">
          <ProjectNavigationItems {...sidebar} compact />
        </nav>
        <div className={styles.content}>
          <NotesWorkspace api={apiClient} accountId={user.id} route={route} navigate={navigate} />
        </div>
      </div>
      {bookNavigation.picker}
      {menuOpen ? (
        <Drawer canDismiss className={styles.projectDrawer} label="Навигация саги" onDismiss={() => setMenuOpen(false)}>
          <header><h2>Разделы</h2>
            <IconButton aria-label="Закрыть навигацию" onClick={() => setMenuOpen(false)} size="touch"><X aria-hidden="true" size={18} /></IconButton>
          </header>
          {shelfQuery.isPending ? <p role="status">Загружаем саги…</p> : null}
          {shelfQuery.isError ? <Alert tone="error"><p>Не удалось загрузить саги.</p>
            <Button onClick={() => void shelfQuery.refetch()} variant="outline">Повторить</Button></Alert> : null}
          <ProjectSidebar {...sidebar} variant="drawer" />
        </Drawer>
      ) : null}
    </main>
  );
}
