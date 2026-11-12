import { ChevronLeft, ChevronRight, List, Type } from 'lucide-react';
import type { RefObject } from 'react';
import type { ReaderChapter } from '../../../entities/manuscript/api';
import { Button } from '../../../ui/controls/Button';
import { firstChapterReason, lastChapterReason } from './ReaderChapter';
import styles from './ReaderNavigation.module.css';

/**
 * The one bottom bar of a phone (specs/2026-11-10-ux7-reader.md RD1.9; Pattern/Reader/Phone Bar): «‹ · Содержание · Аа · ›»,
 * targets of 44 px. The top bar and the sections are the shell's.
 */
export function ReaderPhoneBar({ chapter, contentsButtonRef, lookButtonRef, onContents, onLook, onOpenChapter }: {
  chapter: ReaderChapter | null;
  onOpenChapter: (chapterId: string) => void;
  onContents: () => void;
  contentsButtonRef: RefObject<HTMLButtonElement | null>;
  onLook: () => void;
  lookButtonRef: RefObject<HTMLButtonElement | null>;
}) {
  const previous = chapter?.navigation.previous ?? null;
  const next = chapter?.navigation.next ?? null;
  return (
    <nav aria-label="Чтение" className={styles.bar} data-reader-phone-bar="">
      <Button aria-label={previous ? `Предыдущая глава: глава ${previous.displayNumber}` : 'Предыдущая глава'} caption disabledReason={previous ? null : firstChapterReason} onClick={() => previous && onOpenChapter(previous.id)} variant="ghost">
        <ChevronLeft aria-hidden="true" size={18} />{previous ? `Глава ${previous.displayNumber}` : 'Предыдущая'}
      </Button>
      <Button caption onClick={onContents} ref={contentsButtonRef} variant="ghost"><List aria-hidden="true" size={18} />Содержание</Button>
      <Button aria-label="Аа — вид текста" caption onClick={onLook} ref={lookButtonRef} variant="ghost"><Type aria-hidden="true" size={18} />Аа</Button>
      <Button aria-label={next ? `Следующая глава: глава ${next.displayNumber}` : 'Следующая глава'} caption disabledReason={next ? null : lastChapterReason} onClick={() => next && onOpenChapter(next.id)} variant="ghost">
        <ChevronRight aria-hidden="true" size={18} />{next ? `Глава ${next.displayNumber}` : 'Следующая'}
      </Button>
    </nav>
  );
}
