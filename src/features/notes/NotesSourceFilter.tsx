import { useState } from 'react';
import { X } from 'lucide-react';
import { Button } from '../../ui/controls/Button';
import { IconButton } from '../../ui/controls/IconButton';
import { Select, type SelectOption } from '../../ui/controls/Select';
import { Dialog } from '../../ui/overlays/Dialog';
import styles from './NotesWorkspace.module.css';

/** «Откуда» (specs/2026-11-11-ux7-notes.md NT3.1): where a note was written, in the words of the author. */
export const noteOrigins: readonly SelectOption<string>[] = [
  { value: '', label: 'Все' }, { value: 'chapter', label: 'Из книги' },
  { value: 'chat', label: 'Из чата' }, { value: 'free', label: 'Свои мысли' },
];

/** «Книга»: all the books of the open saga, each of them, and «Все саги» last (NT3.1, NT3.2). */
export function sagaBooks(books: readonly { id: string; title: string }[], withSaga: boolean): SelectOption<string>[] {
  return [
    ...(withSaga ? [{ value: '', label: 'Все книги саги' }, ...books.map((book) => ({ value: book.id, label: book.title }))] : []),
    { value: 'all', label: 'Все саги' },
  ];
}

/** The filters of a narrow window: the same «Откуда» and «Книга» in one window; «Отмена» changes nothing (board 24·23). */
export function NotesFilterDialog({ book, books, source, onApply, onClose }: {
  book: string;
  books: readonly SelectOption<string>[];
  source: string;
  onApply: (next: { source: string; book: string }) => void;
  onClose: () => void;
}) {
  const [from, setFrom] = useState(source);
  const [where, setWhere] = useState(book);
  return <Dialog width={440} label="Отбор заметок" onDismiss={onClose}>
    <section className={styles.sourceDialog}>
      <header><h2>Отбор заметок</h2><IconButton aria-label="Закрыть отбор" onClick={onClose} size="touch"><X size={16} /></IconButton></header>
      <div className={styles.sourceFields}>
        <Select label="Откуда" options={noteOrigins} value={from} onValueChange={setFrom} />
        <Select label="Книга" options={books} value={where} onValueChange={setWhere} />
      </div>
      <footer><Button onClick={onClose} variant="outline" size="touch">Отмена</Button><Button onClick={() => onApply({ source: from, book: where })} variant="primary" size="touch">Применить</Button></footer>
    </section>
  </Dialog>;
}
