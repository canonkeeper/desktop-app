import { ArchiveRestore, FolderOpen, Pencil, Plus, Trash2 } from 'lucide-react';
import { useEffect, useState, type FormEvent, type RefObject } from 'react';
import type { Project } from '../../../entities/manuscript/api';
import { Popover } from '../../../ui/overlays/Popover';
import { ProjectNavigationItems, ProjectSidebar, type ProjectSidebarProps } from '../../../ui/ProjectSidebar';
import { WorkspaceTopBar, type WorkspaceTopBarProps } from '../../../ui/navigation/WorkspaceTopBar';
import type { PresenceStatus } from '../../../ui/overlays/use-presence';
import styles from './ManuscriptShelfPage.module.css';
import { MobileMenu } from './ShelfLayers';

interface ShelfTopBarProps extends Omit<WorkspaceTopBarProps, 'title'> {
  onOpenSearch: () => void;
  searchTriggerRef: RefObject<HTMLButtonElement | null>;
}

export function ShelfTopBar(props: ShelfTopBarProps) {
  return <WorkspaceTopBar {...props} title="Мои книги" />;
}

type ShelfWorkspaceNavigationProps = Omit<ProjectSidebarProps, 'activeSection' | 'collapsed' | 'variant'>;

export function ShelfWorkspaceNavigation({
  onOpenChat,
  onOpenManuscript,
  onOpenProjectMenu,
  projectMeta,
  projectTitle,
  ...shortcuts
}: ShelfWorkspaceNavigationProps) {
  return (
    <>
      <ProjectSidebar
        {...shortcuts}
        activeSection="manuscript"
        onOpenChat={onOpenChat}
        onOpenManuscript={onOpenManuscript}
        onOpenProjectMenu={onOpenProjectMenu}
        projectMeta={projectMeta}
        projectTitle={projectTitle}
      />
      <nav className={styles.tabletRail} aria-label="Планшетная навигация">
        <ProjectNavigationItems {...shortcuts} activeSection="manuscript" compact
          onOpenManuscript={onOpenManuscript} onOpenChat={onOpenChat} />
      </nav>
    </>
  );
}

export function ShelfProjectMenu({
  canManageProject,
  canOpenDeletedSources,
  currentProject,
  deletedSourcesTriggerRef,
  onCreateProject,
  onDismiss,
  onOpenDeletedSources,
  onRenameProject,
  onRequestDeleteProject,
  onSelectProject,
  open,
  projects,
}: {
  canManageProject: boolean;
  canOpenDeletedSources: boolean;
  currentProject: Project | null;
  deletedSourcesTriggerRef: RefObject<HTMLButtonElement | null>;
  onCreateProject: () => void;
  onDismiss: () => void;
  onOpenDeletedSources: () => void;
  onRenameProject: (title: string) => Promise<boolean>;
  onRequestDeleteProject: () => void;
  onSelectProject: (projectId: string) => void;
  open: boolean;
  projects: Project[];
}) {
  const [renaming, setRenaming] = useState(false);
  const [title, setTitle] = useState('');

  useEffect(() => {
    if (!open) setRenaming(false);
    setTitle(currentProject?.title ?? '');
  }, [currentProject?.id, currentProject?.title, open]);

  async function submitRename(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (await onRenameProject(title)) setRenaming(false);
  }

  return (
    <Popover className={styles.projectMenu} label="Выбор саги" onDismiss={onDismiss} open={open}>
      {currentProject ? <>
        <div className={styles.projectMenuHeader}>
          <strong>{currentProject.title}</strong>
          <span>Ваша роль: {roleLabel(currentProject.currentMembership.role)}</span>
        </div>
        {renaming ? (
          <form className={styles.projectRename} onSubmit={(event) => void submitRename(event)}>
            <input aria-label="Новое название саги" onChange={(event) => setTitle(event.target.value)} value={title} />
            <button className={styles.menuItem} type="submit">Сохранить название</button>
            <button className={styles.menuItem} onClick={() => setRenaming(false)} type="button">Отмена</button>
          </form>
        ) : (
          <button className={styles.menuItem} disabled={!canManageProject} onClick={() => setRenaming(true)} type="button">
            <Pencil aria-hidden="true" size={17} />
            Переименовать сагу
          </button>
        )}
        <button className={styles.menuItem} onClick={onCreateProject} type="button">
          <Plus aria-hidden="true" size={17} />
          Новая сага
        </button>
        <button className={styles.menuItem} disabled={!canOpenDeletedSources} onClick={onOpenDeletedSources} ref={deletedSourcesTriggerRef} type="button">
          <ArchiveRestore aria-hidden="true" size={17} />
          Недавно удалённые
        </button>
        <button
          className={styles.dangerMenuItem}
          disabled={!canManageProject}
          onClick={onRequestDeleteProject}
          title={canManageProject ? undefined : 'Удалить сагу может её владелец или администратор.'}
          type="button"
        >
          <Trash2 aria-hidden="true" size={17} />
          Удалить сагу
        </button>
        <div className={styles.projectMenuDivider} />
      </> : (
        <button className={styles.menuItem} onClick={onCreateProject} type="button">
          <Plus aria-hidden="true" size={17} />
          Новая сага
        </button>
      )}
      {projects.filter((project) => project.id !== currentProject?.id).map((project) => (
        <button className={styles.menuItem} key={project.id} onClick={() => onSelectProject(project.id)} type="button">
          <FolderOpen aria-hidden="true" size={17} />
          {project.title}
        </button>
      ))}
    </Popover>
  );
}

function roleLabel(role: Project['currentMembership']['role']) {
  if (role === 'owner') return 'владелец';
  if (role === 'admin') return 'администратор';
  if (role === 'editor') return 'редактор';
  return 'читатель';
}

export function ShelfMobileNavigation({
  mounted,
  onClose,
  onOpenChat,
  onOpenManuscript,
  onOpenProjectMenu,
  projectMeta,
  projectTitle,
  state,
  ...shortcuts
}: ShelfWorkspaceNavigationProps & {
  mounted: boolean;
  onClose: () => void;
  state: PresenceStatus;
}) {
  if (!mounted) return null;
  return (
    <MobileMenu
      state={state}
      onClose={onClose}
      sidebar={{
        ...shortcuts,
        activeSection: 'manuscript',
        onOpenChat,
        onOpenManuscript,
        onOpenProjectMenu,
        projectMeta,
        projectTitle,
      }}
    />
  );
}
