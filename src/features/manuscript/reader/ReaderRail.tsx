import { Minus, Plus } from 'lucide-react';
import type { ChapterListItem } from '../../../entities/manuscript/api';
import type { ReaderDisplaySize, ReaderDisplayTheme } from '../../../entities/manuscript/display-preferences-store';
import { IconButton } from '../../../ui/controls/IconButton';
import { ReaderFocusButton } from '../../../ui/ReaderAppearanceControls';
import { formatReaderChapterTitle } from '../../../ui/copy';
import styles from './ReaderRail.module.css';

export function ReaderRail({ chapters, currentChapterId, displaySize, displayTheme, focusMode,
  onOpenChapter, onSetDisplaySize, onSetDisplayTheme, onToggleFocus }: {
  chapters: ChapterListItem[];
  currentChapterId: string;
  displaySize: ReaderDisplaySize;
  displayTheme: ReaderDisplayTheme;
  focusMode: boolean;
  onOpenChapter: (chapterId: string) => void;
  onSetDisplaySize: (size: ReaderDisplaySize) => void;
  onSetDisplayTheme: (theme: ReaderDisplayTheme) => void;
  onToggleFocus: () => void;
}) {
  return <aside aria-label="Панель чтения" className={styles.rightRail}>
    <h2 className={styles.contentsHeading}>Содержание</h2>
    <div className={styles.railBody}>
      <section className={styles.contents}>
        <p className={styles.sectionLabel}>Главы</p>
        <div className={styles.chapterList}>
          {chapters.map((chapter) => <button aria-current={chapter.id === currentChapterId ? 'page' : undefined}
            className={chapter.id === currentChapterId ? styles.activeChapterButton : styles.chapterButton}
            key={chapter.id} onClick={() => onOpenChapter(chapter.id)} type="button">
            {chapter.displayNumber}. {formatReaderChapterTitle(chapter.displayNumber, chapter.title)}
          </button>)}
        </div>
      </section>
      <section className={styles.displayControls}>
        <p className={styles.sectionLabel}>Вид</p>
        <div className={styles.controlRow}>
          <span>Размер текста</span>
          <div aria-label="Размер текста" className={styles.stepper}>
            <IconButton aria-label="Уменьшить текст" onClick={() => onSetDisplaySize('normal')}><Minus aria-hidden="true" size={16} /></IconButton>
            <span>{displaySize === 'large' ? 'Крупный' : 'Обычный'}</span>
            <IconButton aria-label="Увеличить текст" onClick={() => onSetDisplaySize('large')}><Plus aria-hidden="true" size={16} /></IconButton>
          </div>
        </div>
        <div className={styles.controlRow}>
          <span>Тема чтения</span>
          <div aria-label="Тема чтения" className={styles.swatches}>
            <IconButton aria-label="Светлая" aria-pressed={displayTheme === 'light'} className={`${styles.swatch} ${styles.lightSwatch}`} onClick={() => onSetDisplayTheme('light')} />
            <IconButton aria-label="Сепия" aria-pressed={displayTheme === 'sepia'} className={`${styles.swatch} ${styles.sepiaSwatch}`} onClick={() => onSetDisplayTheme('sepia')} />
            <IconButton aria-label="Тёмная" aria-pressed={displayTheme === 'dark'} className={`${styles.swatch} ${styles.darkSwatch}`} onClick={() => onSetDisplayTheme('dark')} />
          </div>
        </div>
        <ReaderFocusButton focusMode={focusMode} onToggleFocus={onToggleFocus} />
      </section>
    </div>
  </aside>;
}
