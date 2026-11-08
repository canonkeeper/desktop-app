import { BookOpen, X } from 'lucide-react';
import { useEffect, useState } from 'react';
import { useManuscriptShelfQuery, type ManuscriptApiClient } from './api';
import type { RouteNavigate } from '../../shared/navigation/app-route';
import { Button } from '../../ui/controls/Button';
import { IconButton } from '../../ui/controls/IconButton';
import { Alert } from '../../ui/feedback/Alert';
import { Dialog } from '../../ui/overlays/Dialog';
import styles from './BookPicker.module.css';

interface BookNavigationProps {
  api: ManuscriptApiClient;
  projectId?: string;
  /** Only pass a book confirmed by the current route's workspace. */
  bookId?: string;
  chapterId?: string;
  navigate: RouteNavigate;
}

export function useBookNavigation({ api, bookId, chapterId, navigate, projectId }: BookNavigationProps) {
  const [pickerProjectId, setPickerProjectId] = useState<string | undefined | null>(null);
  useEffect(() => { setPickerProjectId(null); }, [projectId]);
  function openReader() {
    if (projectId && bookId) {
      navigate('/manuscript/books', { projectId, bookId, chapterId, mode: 'read' });
    } else {
      setPickerProjectId(projectId);
    }
  }
  return {
    onOpenReader: openReader,
    picker: pickerProjectId !== null && pickerProjectId === projectId ? (
      <Dialog width={440} label="Книга для чтения" onDismiss={() => setPickerProjectId(null)}>
        <section className={styles.picker}>
          <header><h2>Книга для чтения</h2>
            <IconButton aria-label="Закрыть выбор книги" onClick={() => setPickerProjectId(null)}><X size={18} /></IconButton>
          </header>
          <p>Выберите книгу саги.</p>
          {projectId ? <BookChoices api={api} projectId={projectId} onSelect={(id) => {
            setPickerProjectId(null);
            navigate('/manuscript/books', { projectId, bookId: id, mode: 'read' });
          }} /> : <p>Сначала выберите сагу на полке.</p>}
          <footer><Button onClick={() => setPickerProjectId(null)} variant="outline">Отмена</Button></footer>
        </section>
      </Dialog>
    ) : null,
  };
}

function BookChoices({ api, onSelect, projectId }: {
  api: ManuscriptApiClient; onSelect: (bookId: string) => void; projectId: string;
}) {
  const query = useManuscriptShelfQuery(api, { projectId });
  if (query.isPending) return <p role="status">Загружаем книги…</p>;
  if (query.isError) return <Alert tone="error"><p>Не удалось загрузить книги.</p>
    <Button onClick={() => void query.refetch()} variant="outline">Повторить</Button></Alert>;
  if (query.data.project?.id !== projectId) {
    return <Alert tone="error">Сага недоступна.</Alert>;
  }
  if (query.data.books.length === 0) return <p>В этой саге пока нет книг.</p>;
  return <div className={styles.books}>{query.data.books.map((book) => (
    <Button key={book.id} onClick={() => onSelect(book.id)} variant="outline">
      <BookOpen aria-hidden="true" size={18} /><span>{book.title}</span>
    </Button>
  ))}</div>;
}
