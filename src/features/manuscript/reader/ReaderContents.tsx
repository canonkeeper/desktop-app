import { X } from 'lucide-react';
import type { ReactNode, RefObject } from 'react';
import type { ChapterListItem } from '../../../entities/manuscript/api';
import { Button } from '../../../ui/controls/Button';
import { IconButton } from '../../../ui/controls/IconButton';
import { formatCount, formatReaderChapterTitle } from '../../../ui/copy';
import { EmptyState } from '../../../ui/feedback/EmptyState';
import { SkeletonArea } from '../../../ui/feedback/Skeleton';
import { Tab, Tabs } from '../../../ui/navigation/Tabs';
import { Overlay } from '../../../ui/overlays/Overlay';
import { Portal } from '../../../ui/overlays/Portal';
import { useTextBackgroundMark } from '../../../ui/text-background';
import type { PresenceStatus } from '../../../ui/overlays/use-presence';
import type { ReaderPanel } from './reader-route';
import styles from './ReaderContents.module.css';

/** The minutes of reading of a chapter by its words: the rule of the server (library/http_chapters.go). */
export function readingMinutes(words: number) {
  return words > 0 ? Math.ceil(words / 220) : 0;
}

/** «гл. 12 „Белый порт“» — a chapter of the open book in words (specs/2026-11-10-ux1-names.md N1). */
export function chapterWords(chapter: Pick<ChapterListItem, 'displayNumber' | 'title'>) {
  const title = formatReaderChapterTitle(chapter.displayNumber, chapter.title);
  return title ? `гл. ${chapter.displayNumber} «${title}»` : `гл. ${chapter.displayNumber}`;
}

export interface ContentsNote {
  id: string;
  paragraphId: string;
  title: string;
  preview: string;
}

export interface ContentsPlace {
  key: string;
  label: string;
  before: string;
  found: string;
  after: string;
  current: boolean;
}

const tabNames: Readonly<Record<ReaderPanel, string>> = { chapters: 'Главы', notes: 'Заметки', drawings: 'Рисунки', found: 'Найдено' };

/**
 * «Содержание» (specs/2026-11-10-ux7-reader.md RD1.7; Pattern/Reader/Contents, board 40·28): a panel on the right, a sheet
 * from below on a phone, with the tabs «Главы», «Заметки», «Рисунки» and, after a search, «Найдено». The open tab is in the
 * address; Esc and «Назад» close the panel. It is painted by the background of reading.
 */
export function ReaderContents(props: ContentsProps & { sheet: boolean; state: PresenceStatus; returnFocusRef: RefObject<HTMLElement | null> }) {
  const { onClose, returnFocusRef, sheet, state } = props;
  const background = useTextBackgroundMark();
  if (!sheet) return <aside aria-label="Содержание" className={styles.panel}><ContentsBody {...props} /></aside>;
  return (
    <Portal>
      <div className={styles.sheetLayer} data-overlay-scrim="" {...background} data-state={state} onClick={(event) => event.target === event.currentTarget && onClose()}>
        {/* The panel lives in the address: its own «Назад» closes it (PA4.2). */}
        <Overlay className={styles.sheet} history={false} kind="sheet" label="Содержание" onDismiss={onClose} returnFocusRef={returnFocusRef} state={state}>
          <ContentsBody {...props} />
        </Overlay>
      </div>
    </Portal>
  );
}

interface ContentsProps {
  panel: ReaderPanel;
  onPanel: (panel: ReaderPanel) => void;
  onClose: () => void;
  chapters: readonly ChapterListItem[];
  currentChapterId: string;
  /** The minutes of the open chapter as the server counts them. */
  currentMinutes: number;
  onOpenChapter: (chapterId: string) => void;
  notes: {
    state: 'loading' | 'failed' | 'ready'; items: readonly ContentsNote[]; selectedId: string | null; onSelect: (note: ContentsNote) => void; onAll: () => void;
    /** The note open beside the text (specs/2026-11-11-ux7-notes.md NT2.3): it stands in the tab instead of the list. */
    open?: ReactNode;
  } | null;
  drawings: ReactNode;
  found: { query: string; state: 'searching' | 'failed' | 'ready'; places: readonly ContentsPlace[]; onOpen: (key: string) => void; onRetry: () => void } | null;
}

function ContentsBody({ chapters, currentChapterId, currentMinutes, drawings, found, notes, onClose, onOpenChapter, onPanel, panel }: ContentsProps) {
  const tabs: ReaderPanel[] = ['chapters', 'notes', 'drawings', ...(found ? ['found' as const] : [])];
  const shown = tabs.includes(panel) ? panel : 'chapters';
  return (
    <div className={styles.body}>
      <div className={styles.head}>
        <h2 className={styles.title}>Содержание</h2>
        <IconButton aria-label="Закрыть содержание" onClick={onClose} size="touch"><X aria-hidden="true" size={18} /></IconButton>
      </div>
      <Tabs className={styles.tabs} label="Содержание">
        {tabs.map((tab) => (
          <Tab aria-controls={`reader-contents-${tab}`} id={`reader-tab-${tab}`} key={tab} onClick={() => onPanel(tab)} selected={tab === shown}>{tabNames[tab]}</Tab>
        ))}
      </Tabs>
      <div aria-labelledby={`reader-tab-${shown}`} className={styles.list} id={`reader-contents-${shown}`} role="tabpanel" tabIndex={0}>
        {shown === 'chapters' ? (
          <ol className={styles.items}>
            {chapters.map((chapter) => {
              const current = chapter.id === currentChapterId;
              const minutes = current ? currentMinutes : readingMinutes(chapter.wordCount);
              return (
                <li key={chapter.id}>
                  <button aria-current={current ? 'page' : undefined} className={styles.chapter} onClick={() => onOpenChapter(chapter.id)} type="button">
                    <span className={styles.number}>{chapter.displayNumber}</span>
                    <span className={styles.words}>
                      <strong>{formatReaderChapterTitle(chapter.displayNumber, chapter.title) || `Глава ${chapter.displayNumber}`}</strong>
                      <span>{formatCount(chapter.wordCount, 'слово', 'слова', 'слов')}{minutes ? ` · ${minutes} мин` : ''}</span>
                    </span>
                  </button>
                </li>
              );
            })}
          </ol>
        ) : shown === 'notes' ? <NotesTab notes={notes} /> : shown === 'drawings' ? drawings
          ?? <EmptyState title="У этой главы рисунков нет" why="Рисунок главы встаёт значком на поле у своего абзаца." />
          : found ? <FoundTab found={found} /> : null}
      </div>
    </div>
  );
}

function NotesTab({ notes }: { notes: ContentsProps['notes'] }) {
  if (notes?.open) return notes.open;
  if (!notes) return <EmptyState title="К этой главе заметок нет" why="Выделите место в тексте и нажмите «Заметка»." />;
  if (notes.state === 'loading') return <SkeletonArea count={3} />;
  return (
    <>
      {notes.state === 'failed' ? <p className={styles.empty}>Заметки главы не загрузились. Все заметки — по кнопке ниже.</p>
        : notes.items.length === 0 ? <EmptyState title="К этой главе заметок нет" why="Выделите место в тексте и нажмите «Заметка» — она встанет значком на поле у абзаца." /> : (
          <ul className={styles.items}>
            {notes.items.map((note) => (
              <li key={note.id}>
                <button aria-current={note.id === notes.selectedId ? 'true' : undefined} className={styles.note} onClick={() => notes.onSelect(note)} type="button">
                  <strong>{note.title}</strong>
                  {note.preview ? <span>{note.preview}</span> : null}
                </button>
              </li>
            ))}
          </ul>
        )}
      <Button className={styles.all} onClick={notes.onAll} size="sm" variant="outline">Все заметки</Button>
    </>
  );
}

function FoundTab({ found }: { found: NonNullable<ContentsProps['found']> }) {
  if (found.state === 'searching') return <SkeletonArea count={4} />;
  if (found.state === 'failed') {
    return <div className={styles.empty}><p>Поиск не получился.</p><Button onClick={found.onRetry} size="sm" variant="outline">Повторить</Button></div>;
  }
  if (!found.places.length) return <p className={styles.empty}>В этой книге нет «{found.query}».</p>;
  return (
    <ol className={styles.items}>
      {found.places.map((place) => (
        <li key={place.key}>
          <button aria-current={place.current ? 'true' : undefined} className={styles.place} onClick={() => found.onOpen(place.key)} type="button">
            <strong>{place.label}</strong>
            <span>{place.before}<mark>{place.found}</mark>{place.after}</span>
          </button>
        </li>
      ))}
    </ol>
  );
}
