import { ChevronLeft, List, Search, Type } from 'lucide-react';
import type { RefObject } from 'react';
import { Button } from '../../../ui/controls/Button';
import { IconButton } from '../../../ui/controls/IconButton';
import { MoreMenu, type MoreMenuItem } from '../../../ui/overlays/MoreMenu';
import { useNarrowScreen } from '../../../ui/use-narrow-screen';
import styles from './ReaderBar.module.css';

/** The book and the chapter in words (RD1.1, RD1.9): «Книга 2 · Глава 12. Белый порт», on a phone «Книга 2 · гл. 12». */
export interface ReaderPlaceWords {
  full: string;
  short: string;
  /** The title of the book, in the hint of the line. */
  book: string;
}

/**
 * The one line of the reader under the top bar of the shell (specs/2026-11-10-ux7-reader.md RD1.1, RD1.9; Pattern/Reader/
 * Chapter Bar, board 40·24): «‹» to the books, the book and the chapter in words, «Найти», «Аа», «Содержание», «⋯» and a
 * thin line of the way through the chapter at its lower edge. It goes away while the text scrolls down and comes back
 * when it scrolls up, at the start of the chapter and when the keyboard focus is on it; a phone has «Аа» and
 * «Содержание» in its bottom bar.
 */
export function ReaderBar({ contentsButtonRef, contentsOpen, findOpen, lookButtonRef, lookOpen, more, onBack, onContents, onFind, onLook, place, progress }: {
  place: ReaderPlaceWords;
  onBack: () => void;
  findOpen: boolean;
  onFind: () => void;
  lookOpen: boolean;
  onLook: () => void;
  lookButtonRef?: RefObject<HTMLButtonElement | null>;
  contentsOpen: boolean;
  contentsButtonRef?: RefObject<HTMLButtonElement | null>;
  onContents: () => void;
  more: readonly MoreMenuItem[];
  /** How much of the chapter is behind, 0…1. */
  progress: number;
}) {
  const percent = Math.round(Math.min(1, Math.max(0, progress)) * 100);
  // On a phone «Найти» says its word under the icon (specs/2026-11-11-ux9-shelf-chat.md SC2.2, П-15).
  const phone = useNarrowScreen(767);
  return (
    <div className={styles.bar} data-reader-bar="">
      <div className={styles.row}>
        <IconButton aria-label="К книгам" className={styles.back} onClick={onBack} size="touch" title="К книгам">
          <ChevronLeft aria-hidden="true" size={18} />
        </IconButton>
        <p className={styles.place} title={place.book}>
          <span className={styles.full}>{place.full}</span>
          <span className={styles.short}>{place.short}</span>
        </p>
        <div className={styles.actions}>
          <Button aria-keyshortcuts="/" aria-label="Найти" aria-pressed={findOpen} caption={phone} className={styles.find} onClick={onFind} size="sm" variant="ghost">
            <Search aria-hidden="true" size={16} /><span className={styles.word}>Найти</span>
          </Button>
          <Button aria-expanded={lookOpen} aria-haspopup="dialog" className={styles.wide} onClick={onLook} ref={lookButtonRef} size="sm"
            title="Вид текста: размер, фон, ширина, только текст" variant="ghost">
            <Type aria-hidden="true" size={16} />Аа
          </Button>
          <Button aria-expanded={contentsOpen} className={styles.wide} onClick={onContents} ref={contentsButtonRef} size="sm" variant="ghost">
            <List aria-hidden="true" size={16} />Содержание
          </Button>
          {more.length ? <MoreMenu items={more} label="Ещё" size="touch" /> : null}
        </div>
      </div>
      <div aria-label="Прочитано в главе" aria-valuemax={100} aria-valuemin={0} aria-valuenow={percent} className={styles.progress} role="progressbar">
        <span style={{ width: `${percent}%` }} />
      </div>
    </div>
  );
}
