import { X } from 'lucide-react';
import type { RefObject } from 'react';
import type { ChapterListItem } from '../../../entities/manuscript/api';
import type { ReaderDisplaySize, ReaderDisplayTheme } from '../../../entities/manuscript/display-preferences-store';
import { IconButton } from '../../../ui/controls/IconButton';
import { SegmentedControl } from '../../../ui/navigation/SegmentedControl';
import { Overlay } from '../../../ui/overlays/Overlay';
import { ProjectSidebar, type ProjectSidebarProps } from '../../../ui/ProjectSidebar';
import { ReaderFocusButton } from '../../../ui/ReaderAppearanceControls';
import { Portal } from '../../../ui/overlays/Portal';
import type { PresenceStatus } from '../../../ui/overlays/use-presence';
import { formatReaderChapterTitle } from '../../../ui/copy';
import styles from './ReaderLayers.module.css';

export type ReaderMobilePanelKind = 'appearance' | 'chapters';

export function ReaderMobilePanel({ chapters, currentChapterId, displaySize, displayTheme, focusMode,
  onClose, onOpenChapter, onSetDisplaySize, onSetDisplayTheme, onToggleFocus, panel, returnFocusRef, state }: {
  chapters: ChapterListItem[];
  currentChapterId: string;
  displaySize: ReaderDisplaySize;
  displayTheme: ReaderDisplayTheme;
  focusMode: boolean;
  onClose: () => void;
  onOpenChapter: (chapterId: string) => void;
  onSetDisplaySize: (size: ReaderDisplaySize) => void;
  onSetDisplayTheme: (theme: ReaderDisplayTheme) => void;
  onToggleFocus: () => void;
  panel: ReaderMobilePanelKind;
  returnFocusRef: RefObject<HTMLButtonElement | null>;
  state: PresenceStatus;
}) {
  return <Portal>
    <div className={styles.drawerLayer} data-overlay-scrim="" data-state={state} onClick={(event) => event.target === event.currentTarget && onClose()}>
      <Overlay kind="sheet" label={panel === 'chapters' ? 'Главы' : 'Вид'} onDismiss={onClose} returnFocusRef={returnFocusRef} state={state}>
        <section className={styles.sheet}>
          <div className={styles.sheetHandle} />
          <div className={styles.sheetTitleRow}>
            <div><h2 tabIndex={-1}>{panel === 'chapters' ? 'Главы' : 'Вид'}</h2>
              {panel === 'appearance' ? <p className={styles.sheetSubtitle}>Настройки чтения</p> : null}
            </div>
            <IconButton aria-label="Закрыть панель" onClick={onClose} size="touch"><X aria-hidden="true" size={18} /></IconButton>
          </div>
          {panel === 'chapters' ? <div className={styles.chapterList}>
            {chapters.map((chapter) => <button aria-current={chapter.id === currentChapterId ? 'page' : undefined}
              className={chapter.id === currentChapterId ? styles.activeChapterButton : styles.chapterButton}
              key={chapter.id} onClick={() => onOpenChapter(chapter.id)} type="button">
              <span>{chapter.displayNumber}</span><strong>{formatReaderChapterTitle(chapter.displayNumber, chapter.title)}</strong>
            </button>)}
          </div> : <div className={styles.appearanceSettings}>
            <div className={styles.appearanceRow}><strong>Размер текста</strong>
              <SegmentedControl fullWidth label="Размер текста" onValueChange={onSetDisplaySize}
                options={[{ label: 'Обычный', value: 'normal' }, { label: 'Крупный', value: 'large' }]} size="touch" value={displaySize} />
            </div>
            <div className={styles.appearanceRow}><strong>Тема чтения</strong>
              <SegmentedControl fullWidth label="Тема чтения" onValueChange={onSetDisplayTheme}
                options={[{ label: 'Светлая', value: 'light' }, { label: 'Сепия', value: 'sepia' }, { label: 'Тёмная', value: 'dark' }]} size="touch" value={displayTheme} />
            </div>
            <ReaderFocusButton focusMode={focusMode} onToggleFocus={onToggleFocus} />
          </div>}
        </section>
      </Overlay>
    </div>
  </Portal>;
}

export function ReaderMobileMenu({ onClose, sidebar, state }: { onClose: () => void; sidebar: ProjectSidebarProps; state: PresenceStatus }) {
  return <Portal>
    <div className={styles.drawerLayer} data-overlay-scrim="" data-state={state} onClick={(event) => event.target === event.currentTarget && onClose()}>
      <Overlay kind="drawer" label="Меню чтения" onDismiss={onClose} state={state}>
        <aside className={styles.mobileDrawer}>
          <div className={styles.drawerHeader}><h2>Навигация</h2>
            <IconButton aria-label="Закрыть меню" onClick={onClose} size="touch"><X aria-hidden="true" size={18} /></IconButton>
          </div>
          <ProjectSidebar {...sidebar} variant="drawer"
            onOpenChat={() => { onClose(); sidebar.onOpenChat(); }}
            onOpenManuscript={() => { onClose(); sidebar.onOpenManuscript(); }}
            onOpenReader={sidebar.onOpenReader ? () => { onClose(); sidebar.onOpenReader?.(); } : undefined}
            onOpenNotes={sidebar.onOpenNotes ? () => { onClose(); sidebar.onOpenNotes?.(); } : undefined}
            onOpenSaga={sidebar.onOpenSaga ? () => { onClose(); sidebar.onOpenSaga?.(); } : undefined}
            onOpenWriting={sidebar.onOpenWriting ? () => { onClose(); sidebar.onOpenWriting?.(); } : undefined}
            onOpenDrawing={sidebar.onOpenDrawing ? () => { onClose(); sidebar.onOpenDrawing?.(); } : undefined}
            onOpenGallery={sidebar.onOpenGallery ? () => { onClose(); sidebar.onOpenGallery?.(); } : undefined}
            onCreateNote={sidebar.onCreateNote ? () => { onClose(); sidebar.onCreateNote?.(); } : undefined} />
        </aside>
      </Overlay>
    </div>
  </Portal>;
}
