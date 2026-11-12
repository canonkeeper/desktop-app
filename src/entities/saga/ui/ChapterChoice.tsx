import { useEffect, useState } from 'react';
import { Button } from '../../../ui/controls/Button';
import { SearchField } from '../../../ui/controls/SearchField';
import { Select } from '../../../ui/controls/Select';
import { SkeletonArea } from '../../../ui/feedback/Skeleton';
import { useSagaContentsQuery, type SagaApiClient, type SagaChapterRef, type SourceView } from '../api';
import styles from './ChapterPicker.module.css';

/** The chapter a choice holds: its key goes to the server, its place is what the author reads. */
export type ChosenChapter = Pick<SagaChapterRef, 'key' | 'label'>;

const searchDelay = 300;

interface ChapterChoiceProps {
  api: SagaApiClient;
  disabled?: boolean;
  onChange: (chapter: ChosenChapter | null) => void;
  projectId: string;
  sourceView: SourceView;
  value: ChosenChapter | null;
}

/**
 * The chapter of a record (specs/2026-11-10-ux8-memory-records.md MR1.1): «Выбрать главу» opens the contents of the saga — a book
 * and its chapters, or a search by title over the whole saga — named by place, as the reader and the rest of the saga name them
 * (ux1-names N5.2). The chosen chapter stands by its place with «Убрать»; the author neither sees nor types its key.
 */
export function ChapterChoice({ api, disabled = false, onChange, projectId, sourceView, value }: ChapterChoiceProps) {
  const [open, setOpen] = useState(false);
  return (
    <fieldset className={styles.choice} disabled={disabled}>
      <legend className={styles.legend}>Глава</legend>
      <div className={styles.chosen}>
        <span className={value ? styles.place : styles.none}>{value ? value.label : 'Не выбрана — запись о всей саге'}</span>
        <Button aria-expanded={open} onClick={() => setOpen(!open)} size="sm" type="button" variant="outline">{value ? 'Другая глава' : 'Выбрать главу'}</Button>
        {value ? <Button onClick={() => onChange(null)} size="sm" type="button" variant="ghost">Убрать</Button> : null}
      </div>
      {open ? (
        <ChapterContents api={api} current={value} onPick={(chapter) => { onChange(chapter); setOpen(false); }} projectId={projectId} sourceView={sourceView} />
      ) : null}
    </fieldset>
  );
}

function ChapterContents({ api, current, onPick, projectId, sourceView }: {
  api: SagaApiClient; current: ChosenChapter | null; onPick: (chapter: ChosenChapter) => void; projectId: string; sourceView: SourceView;
}) {
  const [draft, setDraft] = useState('');
  const [query, setQuery] = useState('');
  useEffect(() => {
    const timer = window.setTimeout(() => setQuery(draft.trim()), searchDelay);
    return () => window.clearTimeout(timer);
  }, [draft]);
  // The chosen chapter as the server reads its key (specs/2026-11-11-ux7-reorder.md RK1.5), with the books of the saga.
  const first = useSagaContentsQuery(api, projectId, { sourceView, ...(current ? { chapter: current.key } : {}) });
  const books = first.data?.books ?? [];
  const chosen = current ? first.data?.chapter : undefined;
  // The book the author opened; until he opens one — the book of the chosen chapter, else the first.
  const [opened, setOpened] = useState<string | null>(null);
  const bookId = opened ?? chosen?.bookId ?? first.data?.bookId ?? '';
  const contents = useSagaContentsQuery(api, projectId, { sourceView, ...(query ? { q: query } : bookId ? { bookId } : {}) });
  const chapters = contents.data && (query || contents.data.bookId === bookId) ? contents.data.chapters.map((item) => item.chapter) : [];
  return (
    <div className={styles.contents}>
      {/* Enter finds, it does not send the form the choice stands in. */}
      <SearchField label="Найти главу по названию" maxLength={200} onChange={(event) => setDraft(event.target.value)} onClear={() => setDraft('')}
        onKeyDown={(event) => { if (event.key === 'Enter') event.preventDefault(); }} value={draft} />
      {query ? null : (
        <Select label="Книга" loading={first.isPending} onValueChange={setOpened}
          options={books.map((book) => ({ value: book.id, label: book.title }))} value={bookId} />
      )}
      {contents.isPending ? <SkeletonArea count={3} shape="rows" />
        : chapters.length === 0 ? <p className={styles.none} role="status">{query ? 'Такой главы не нашлось.' : 'В этой книге нет глав.'}</p>
          : (
            <ul aria-label={query ? 'Найденные главы' : 'Главы книги'} className={styles.chapters}>
              {chapters.map((chapter) => (
                <li key={chapter.key}>
                  <button aria-current={chapter.key === (chosen?.key ?? current?.key) ? 'true' : undefined} className={styles.chapter} onClick={() => onPick({ key: chapter.key, label: chapter.label })} type="button">
                    {chapter.label}
                  </button>
                </li>
              ))}
            </ul>
          )}
    </div>
  );
}
