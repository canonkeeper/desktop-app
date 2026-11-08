import { ArrowLeft, ArrowRight, List, PenLine, Settings2 } from 'lucide-react';
import type { ReactNode } from 'react';
import type { ReaderChapter } from '../../../entities/manuscript/api';
import type { ReaderDisplaySize, ReaderDisplayTheme } from '../../../entities/manuscript/display-preferences-store';
import { Button } from '../../../ui/controls/Button';
import { Breadcrumbs } from '../../../ui/navigation/Breadcrumbs';
import { ReaderAppearanceControls } from '../../../ui/ReaderAppearanceControls';
import { formatCount, formatReaderChapterTitle } from '../../../ui/copy';
import styles from './ReaderChapter.module.css';

export function ReaderChapterHeader({
  bookLabel,
  chapter,
  drawingCount = 0,
  illustrations,
  displaySize,
  displayTheme,
  focusMode,
  onOpenBook,
  onSetDisplaySize,
  onSetDisplayTheme,
  onToggleFocus,
  onOpenChapters,
  onOpenAppearance,
}: {
  bookLabel: string;
  chapter: ReaderChapter;
  /** The drawings of the chapter, named in its meta line (illustrations R8.9). */
  drawingCount?: number;
  /** «Иллюстрированная книга» (illustrations R8.9). */
  illustrations?: ReactNode;
  displaySize: ReaderDisplaySize;
  displayTheme: ReaderDisplayTheme;
  focusMode: boolean;
  onOpenBook: () => void;
  onSetDisplaySize: (size: ReaderDisplaySize) => void;
  onSetDisplayTheme: (theme: ReaderDisplayTheme) => void;
  onToggleFocus: () => void;
  onOpenChapters?: () => void;
  onOpenAppearance?: () => void;
}) {
  const chapterLabel = `Глава ${chapter.navigation.displayNumber}`;
  return (
    <header className={styles.header}>
      <Breadcrumbs className={styles.breadcrumbs} label="Хлебные крошки">
        <li><button onClick={onOpenBook} type="button">Мои книги</button></li>
        <li><span aria-hidden="true">/</span><button onClick={onOpenBook} type="button">{bookLabel}</button></li>
        <li aria-current="page"><span aria-hidden="true">/</span><strong>{chapterLabel}</strong></li>
      </Breadcrumbs>
      <div className={styles.titleAndControls}>
        <div className={styles.titleBlock}>
          <h1>{chapterLabel}. {formatReaderChapterTitle(chapter.navigation.displayNumber, chapter.title)}</h1>
          <p>{formatCount(chapter.wordCount, 'слово', 'слова', 'слов')} · {chapter.navigation.readingTimeMinutes} мин{drawingCount ? ` · ${formatCount(drawingCount, 'рисунок', 'рисунка', 'рисунков')}` : ''}</p>
        </div>
        <div className={styles.headerActions}>
          {illustrations}
          {onOpenChapters ? <Button className={styles.compactChapters} onClick={onOpenChapters} size="touch" variant="ghost"><List aria-hidden="true" size={18} />Главы</Button> : null}
          <div className={styles.appearanceInline}><ReaderAppearanceControls displaySize={displaySize} displayTheme={displayTheme} focusMode={focusMode}
            onSetDisplaySize={onSetDisplaySize} onSetDisplayTheme={onSetDisplayTheme} onToggleFocus={onToggleFocus} /></div>
          {onOpenAppearance ? <Button className={styles.compactAppearance} onClick={onOpenAppearance} size="touch" variant="ghost"><Settings2 aria-hidden="true" size={18} />Вид</Button> : null}
        </div>
      </div>
    </header>
  );
}

/**
 * «Править главу» (specs/2026-11-05-voice-full-access.md A8.7), named by its chapter so a screen reader and the voice
 * find it. It stands above the text at every width: the header has no room for it beside the reading controls.
 */
export function EditChapterButton({ chapter, edit }: { chapter: ReaderChapter; edit: { pending: boolean; run: () => void } }) {
  return (
    <Button aria-label={`Править главу «${chapter.title}»`} loading={edit.pending} onClick={edit.run} size="sm" variant="outline">
      <PenLine aria-hidden="true" size={15} />Править<span className={styles.editWord}>&nbsp;главу</span>
    </Button>
  );
}

export function ChapterNavigation({ chapter, onOpenChapter }: { chapter: ReaderChapter; onOpenChapter: (chapterId: string) => void }) {
  return (
    <footer aria-label="Навигация по главам" className={styles.chapterNavigation}>
      <Button aria-label="Предыдущая глава" disabled={!chapter.navigation.previous} onClick={() => chapter.navigation.previous && onOpenChapter(chapter.navigation.previous.id)} variant="outline">
        <ArrowLeft aria-hidden="true" size={17} />
        <span className={styles.navigationLabel}>Предыдущая глава</span>
      </Button>
      <span>{chapter.navigation.position} / {chapter.navigation.total}</span>
      <Button aria-label="Следующая глава" disabled={!chapter.navigation.next} onClick={() => chapter.navigation.next && onOpenChapter(chapter.navigation.next.id)} variant="outline">
        <span className={styles.navigationLabel}>Следующая глава</span>
        <ArrowRight aria-hidden="true" size={17} />
      </Button>
    </footer>
  );
}
