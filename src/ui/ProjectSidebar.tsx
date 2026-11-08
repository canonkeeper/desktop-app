import { BookOpen, Brush, ChevronDown, Library, MessageSquare, NotebookPen, Palette, PenLine, Plus, StickyNote, Waypoints } from 'lucide-react';
import type { ReactNode } from 'react';
import { useGallerySeen } from '../shared/navigation/gallery-seen';
import { IconButton } from './controls/IconButton';
import { Badge } from './feedback/Badge';
import { useSagaSections, type SagaSection } from './saga-sections';
import styles from './ProjectSidebar.module.css';

export interface ProjectSidebarProps {
  activeSection: SagaSection;
  collapsed?: boolean;
  onOpenChat: () => void;
  onOpenManuscript: () => void;
  onOpenReader?: () => void;
  onOpenNotes?: () => void;
  onOpenSaga?: () => void;
  /** «Письмо»: the chapters in writing of the saga. */
  onOpenWriting?: () => void;
  /** «Рисование»: the drawing workshop of the saga (specs/2026-11-02-image-workshop.md IW9.1). */
  onOpenDrawing?: () => void;
  /** «Галерея»: the drawings of the saga, or the page of the feature without them (illustrations R11.9). */
  onOpenGallery?: () => void;
  onCreateNote?: () => void;
  onOpenProjectMenu?: () => void;
  projectSelector?: ReactNode;
  projectMeta: string;
  projectTitle: string;
  variant?: 'sidebar' | 'drawer';
}

export function ProjectSidebar({
  activeSection,
  collapsed = false,
  onOpenChat,
  onOpenManuscript,
  onOpenReader,
  onOpenNotes,
  onOpenSaga,
  onOpenWriting,
  onOpenDrawing,
  onOpenGallery,
  onCreateNote,
  onOpenProjectMenu,
  projectSelector,
  projectMeta,
  projectTitle,
  variant = 'sidebar',
}: ProjectSidebarProps) {
  const touchClass = variant === 'drawer' ? ` ${styles.touchTarget}` : '';
  const compact = collapsed && variant === 'sidebar';
  const inner = (
    <>
      {!compact ? (
        <>
          {projectSelector ?? <button className={`${styles.projectButton}${touchClass}`} disabled={!onOpenProjectMenu} onClick={onOpenProjectMenu} type="button">
            {projectTitle}
            <ChevronDown aria-hidden="true" color="var(--ck-muted-foreground)" size={16} />
          </button>}
          {projectMeta ? <p className={styles.projectMeta}>{projectMeta}</p> : null}
        </>
      ) : null}
      <nav aria-label="Разделы саги">
        {!compact ? <p className={styles.sectionLabel}>Разделы</p> : null}
        <div className={styles.navList}>
          <ProjectNavigationItems activeSection={activeSection} compact={compact}
            onOpenManuscript={onOpenManuscript} onOpenChat={onOpenChat}
            onOpenReader={onOpenReader} onOpenNotes={onOpenNotes} onOpenSaga={onOpenSaga} onOpenWriting={onOpenWriting} onOpenDrawing={onOpenDrawing} onOpenGallery={onOpenGallery}
            onCreateNote={onCreateNote}
            touch={variant === 'drawer'} />
        </div>
      </nav>
    </>
  );

  if (variant === 'drawer') {
    return <div className={styles.sidebarDrawer}>{inner}</div>;
  }
  return (
    <aside className={`${styles.sidebar}${compact ? ` ${styles.collapsed}` : ''}`} aria-label="Навигация саги">
      {inner}
    </aside>
  );
}

type NavigationItemsProps = Pick<ProjectSidebarProps, 'activeSection' | 'onOpenChat' | 'onOpenManuscript' | 'onOpenReader' | 'onOpenNotes' | 'onOpenSaga' | 'onOpenWriting' | 'onOpenDrawing' | 'onOpenGallery' | 'onCreateNote'> & {
  compact?: boolean;
  touch?: boolean;
};

/**
 * The sections of the saga: a list in the sidebar and the drawer, a narrow column in `compact` — every section named in
 * words under its icon at any width (specs/2026-11-03-narrow-windows-navigation.md N2.1).
 */
export function ProjectNavigationItems({ activeSection, compact = false, touch = false, ...actions }: NavigationItemsProps) {
  // «новое» until the author opens the gallery once (R11.9).
  const galleryNew = !useGallerySeen();
  const sections = useSagaSections();
  const items: { section: SagaSection; label: string; Icon: typeof Library; onClick?: () => void }[] = [
    { section: 'manuscript', label: 'Мои книги', Icon: compact ? NotebookPen : Library, onClick: actions.onOpenManuscript },
    { section: 'reader', label: 'Чтение', Icon: BookOpen, onClick: actions.onOpenReader },
    { section: 'chat', label: 'Чат', Icon: MessageSquare, onClick: actions.onOpenChat },
    { section: 'saga', label: 'Карта саги', Icon: Waypoints, onClick: actions.onOpenSaga },
    { section: 'writing', label: 'Письмо', Icon: PenLine, onClick: actions.onOpenWriting },
    { section: 'drawing', label: 'Рисование', Icon: Palette, onClick: actions.onOpenDrawing },
    { section: 'gallery', label: 'Галерея', Icon: Brush, onClick: actions.onOpenGallery },
    { section: 'notes', label: 'Заметки', Icon: StickyNote, onClick: actions.onOpenNotes },
  ];
  return items.filter((item) => item.onClick && sections.includes(item.section)).map(({ section, label, Icon, onClick }) => {
    const marked = section === 'gallery' && galleryNew;
    const name = marked ? `${label} новое` : label;
    const control = <button key={section} aria-current={activeSection === section ? 'page' : undefined} aria-label={name}
      className={`${activeSection === section ? styles.activeNavButton : styles.navButton}${compact ? ` ${styles.compactButton}` : ''}${touch ? ` ${styles.touchTarget}` : ''}`}
      onClick={onClick} type="button">
      <Icon aria-hidden="true" size={compact ? 20 : 15} />
      {label}
      {marked && compact ? <span aria-hidden="true" className={styles.newDot} /> : null}
      {marked && !compact ? <>{' '}<Badge aria-hidden="true" className={styles.newMark} tone="info">новое</Badge></> : null}
    </button>;
    return section === 'notes' && !compact && actions.onCreateNote ? (
      <div className={`${styles.notesRow}${touch ? ` ${styles.touchRow}` : ''}`} key={section}>
        {control}
        <IconButton aria-label="Создать заметку" title="Создать заметку" className={styles.createNoteButton}
          onClick={actions.onCreateNote} size={touch ? 'touch' : 'sm'}><Plus aria-hidden="true" size={16} /></IconButton>
      </div>
    ) : control;
  });
}
