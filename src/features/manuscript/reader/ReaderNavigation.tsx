import { BookOpen, LogOut, Menu, NotebookPen, Search, Settings2 } from 'lucide-react';
import { useRef, type ReactNode, type RefObject } from 'react';
import type { Project } from '../../../entities/manuscript/api';
import { ProjectNavigationItems, ProjectSidebar, type ProjectSidebarProps } from '../../../ui/ProjectSidebar';
import { NotesButton } from '../../../ui/NotesButton';
import { AccountActingMark, AccountSessionItems, AccountUnreadMark, useAccountButtonLabel } from '../../../ui/navigation/AccountSession';
import { TopBarStrip } from '../../../ui/navigation/TopBarStrip';
import { Button } from '../../../ui/controls/Button';
import type { PresenceStatus } from '../../../ui/overlays/use-presence';
import styles from './ReaderNavigation.module.css';

export function ReaderAppBar({
  accountMenuState,
  mobileTitle,
  onLogout,
  onOpenMenu,
  onOpenNotes,
  onOpenSearch,
  onCloseAccount,
  onToggleAccount,
  searchButtonRef,
  searchPanel,
  talk,
  userName,
  projectTitle,
}: {
  accountMenuState: PresenceStatus | null;
  mobileTitle: string;
  onLogout?: () => void;
  onOpenMenu: () => void;
  onOpenNotes?: () => void;
  onOpenSearch: () => void;
  onCloseAccount: () => void;
  onToggleAccount: () => void;
  searchButtonRef: RefObject<HTMLButtonElement | null>;
  searchPanel: ReactNode;
  /** «Поговорить» of the voice co-author (specs/2026-11-03-voice-assistant.md V2.1). */
  talk?: ReactNode;
  userName: string;
  projectTitle?: string;
}) {
  const accountButtonRef = useRef<HTMLButtonElement>(null);
  const accountLabel = useAccountButtonLabel();
  return (<>
    <header className={styles.globalBar}>
      <button aria-label={`Открыть меню: ${projectTitle ?? 'Canon Keeper'}`} className={styles.brand} onClick={onOpenMenu} title={mobileTitle} type="button">
        <Menu aria-hidden="true" className={styles.brandMenuIcon} size={20} />
        <span aria-hidden="true">{projectTitle ?? 'Canon Keeper'}</span>
      </button>
      <Button aria-label="Глобальный поиск" className={styles.globalSearchButton} onClick={onOpenSearch} ref={searchButtonRef} variant="outline">
        <Search aria-hidden="true" size={16} />
        <span>Глобальный поиск</span>
      </Button>
      <div className={styles.barActions}>
        {talk}
        <NotesButton onOpen={onOpenNotes} />
        {onLogout ? (
          <button aria-label={accountLabel} className={styles.avatarButton} onClick={onToggleAccount} ref={accountButtonRef} type="button">
            {userName.slice(0, 1)}
            <AccountActingMark className={styles.actingMark} />
            <AccountUnreadMark className={styles.unreadMark} />
          </button>
        ) : null}
      </div>
      {searchPanel}
      {accountMenuState && onLogout ? (
        <div aria-label="Меню аккаунта" className={styles.accountMenu} data-pop="" data-state={accountMenuState} role="menu">
          <p className={styles.accountName}>{userName}</p>
          <AccountSessionItems itemRole="menuitem" onDone={() => { onCloseAccount(); accountButtonRef.current?.focus(); }} />
          <Button className={styles.accountItem} onClick={onLogout} role="menuitem" size="touch" variant="ghost">
            <LogOut aria-hidden="true" size={17} />
            Выйти
          </Button>
        </div>
      ) : null}
    </header>
    <TopBarStrip />
  </>);
}

export function ReaderProjectNavigation({ sidebar }: { sidebar: ProjectSidebarProps }) {
  return (
    <>
      <div className={styles.desktopSidebar}><ProjectSidebar {...sidebar} /></div>
      <nav aria-label="Планшетная навигация" className={styles.tabletRail}>
        <ProjectNavigationItems {...sidebar} compact />
      </nav>
    </>
  );
}

export function ReaderMobileControls({ chaptersButtonRef, notesButtonRef, onOpenChapters, onOpenNotes, onOpenView, viewButtonRef }: { chaptersButtonRef: RefObject<HTMLButtonElement | null>; notesButtonRef: RefObject<HTMLButtonElement | null>; onOpenChapters: () => void; onOpenNotes: () => void; onOpenView: () => void; viewButtonRef: RefObject<HTMLButtonElement | null> }) {
  return (
    <nav aria-label="Мобильные действия чтения" className={styles.mobileControls}>
      <button onClick={onOpenChapters} ref={chaptersButtonRef} type="button"><BookOpen aria-hidden="true" size={18} />Главы</button>
      <button onClick={onOpenNotes} ref={notesButtonRef} type="button"><NotebookPen aria-hidden="true" size={18} />Заметки</button>
      <button onClick={onOpenView} ref={viewButtonRef} type="button"><Settings2 aria-hidden="true" size={18} />Вид</button>
    </nav>
  );
}

export function ReaderProjectMenu({ onSelect, projects, state }: { onSelect: (projectId: string) => void; projects: Project[]; state: PresenceStatus }) {
  return (
    <div aria-label="Выбор саги" className={styles.projectMenu} data-pop="" data-state={state} role="menu">
      {projects.map((project) => (
        <button className={styles.menuItem} key={project.id} onClick={() => onSelect(project.id)} role="menuitem" type="button">{project.title}</button>
      ))}
    </div>
  );
}
