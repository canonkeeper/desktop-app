import { ArrowLeft, ArrowRight } from 'lucide-react';
import type { ReaderChapter } from '../../../entities/manuscript/api';
import { Button } from '../../../ui/controls/Button';
import styles from './ReaderChapter.module.css';

/** Why the step past an end of the book does not go (RD1.3): the button stays and says it. */
export const firstChapterReason = 'Это первая глава книги.';
export const lastChapterReason = 'Это последняя глава книги.';

/**
 * The step between chapters at the end of the text, not fixed (specs/2026-11-10-ux7-reader.md RD1.3; Pattern/Reader/Chapter
 * Turn): «← Глава 11 · 12 из 48 · Глава 13 →». At an end of the book the button stays and says why; Alt+← and Alt+→ are
 * the same steps.
 */
export function ChapterTurn({ chapter, onOpenChapter }: { chapter: ReaderChapter; onOpenChapter: (chapterId: string) => void }) {
  const { next, position, previous, total } = chapter.navigation;
  return (
    <nav aria-label="Навигация по главам" className={styles.turn}>
      <Button aria-keyshortcuts="Alt+ArrowLeft" aria-label={previous ? `Предыдущая глава: глава ${previous.displayNumber}` : 'Предыдущая глава'}
        disabledReason={previous ? null : firstChapterReason} onClick={() => previous && onOpenChapter(previous.id)} variant="outline">
        <ArrowLeft aria-hidden="true" size={17} />
        <span>{previous ? `Глава ${previous.displayNumber}` : 'Предыдущая'}</span>
      </Button>
      <span className={styles.position}>{position} из {total}</span>
      <Button aria-keyshortcuts="Alt+ArrowRight" aria-label={next ? `Следующая глава: глава ${next.displayNumber}` : 'Следующая глава'}
        disabledReason={next ? null : lastChapterReason} onClick={() => next && onOpenChapter(next.id)} variant="outline">
        <span>{next ? `Глава ${next.displayNumber}` : 'Следующая'}</span>
        <ArrowRight aria-hidden="true" size={17} />
      </Button>
    </nav>
  );
}
