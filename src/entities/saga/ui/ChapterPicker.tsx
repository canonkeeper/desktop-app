import { useState } from 'react';
import { Select } from '../../../ui/controls/Select';
import { useSagaContentsQuery, type SagaApiClient, type SourceView } from '../api';
import styles from './ChapterPicker.module.css';

const none = '';

interface ChapterPickerProps {
  api: SagaApiClient;
  disabled?: boolean;
  /** The words of «no chapter»: «Без главы». */
  noneLabel?: string;
  onChange: (key: string) => void;
  projectId: string;
  sourceView: SourceView;
  /** The key of the chosen chapter; empty — none. The key goes to the server; the author reads the place. */
  value: string;
}

/**
 * The chapter of a record, a drawing or a look (specs/2026-11-10-ux1-names.md N5.2): the author chooses its book and
 * then the chapter by its place, «Книга 2, гл. 12 „Белый порт“», as the saga names it. He neither sees nor types the
 * key of the chapter. «Без главы» is the first choice: a chapter is never required.
 */
export function ChapterPicker({ api, disabled = false, noneLabel = 'Без главы', onChange, projectId, sourceView, value }: ChapterPickerProps) {
  // The chosen chapter as the server reads its kept key — a bare number that turned ambiguous names its first chapter
  // still (specs/2026-11-11-ux7-reorder.md RK1.5) — with the books of the saga.
  const first = useSagaContentsQuery(api, projectId, { sourceView, ...(value ? { chapter: value } : {}) });
  const books = first.data?.books ?? [];
  const chosen = value ? first.data?.chapter : undefined;
  // The book the author opened; until he opens one — the book of the chapter already chosen.
  const [opened, setOpened] = useState<string | null>(null);
  const bookId = opened ?? chosen?.bookId ?? none;
  const contents = useSagaContentsQuery(api, projectId, { sourceView, ...(bookId ? { bookId } : {}) });
  const chapters = bookId && contents.data?.bookId === bookId ? contents.data.chapters.map((item) => item.chapter) : [];
  const shown = chapters.some((chapter) => chapter.key === value) ? value : chosen && chapters.some((chapter) => chapter.key === chosen.key) ? chosen.key : none;
  return (
    <div className={styles.picker}>
      <Select disabled={disabled} label="Книга" loading={first.isPending}
        onValueChange={(next) => { setOpened(next); onChange(none); }}
        options={[{ value: none, label: noneLabel }, ...books.map((book) => ({ value: book.id, label: book.title }))]} value={bookId} />
      <Select disabled={disabled || !bookId} label="Глава" loading={Boolean(bookId) && chapters.length === 0 && contents.isFetching}
        onValueChange={onChange}
        options={[{ value: none, label: bookId ? 'Выберите главу' : 'Сначала книгу' }, ...chapters.map((chapter) => ({ value: chapter.key, label: chapter.label }))]}
        value={shown} />
    </div>
  );
}
