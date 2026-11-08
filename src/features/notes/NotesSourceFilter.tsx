import { useState } from 'react';
import { X } from 'lucide-react';
import { useManuscriptShelfQuery, type ManuscriptApiClient } from '../../entities/manuscript/api';
import { Button } from '../../ui/controls/Button';
import { IconButton } from '../../ui/controls/IconButton';
import { Select } from '../../ui/controls/Select';
import { Alert } from '../../ui/feedback/Alert';
import { Dialog } from '../../ui/overlays/Dialog';
import styles from './NotesWorkspace.module.css';

export const noteSources = [
  { value: '', label: 'Все типы' }, { value: 'free', label: 'Записи без источника' },
  { value: 'chat', label: 'Из чата' }, { value: 'chapter', label: 'К главам' },
];

export function NotesSourceFilter({ api, projectId, bookId, source, onApply, onClose }: {
  api: ManuscriptApiClient;
  projectId?: string;
  bookId?: string;
  source?: string;
  onApply: (projectId?: string, bookId?: string, source?: string) => void;
  onClose: () => void;
}) {
  const [project, setProject] = useState(projectId ?? '');
  const [book, setBook] = useState(bookId ?? '');
  const [selectedSource, setSelectedSource] = useState(source ?? '');
  const query = useManuscriptShelfQuery(api, { projectId: project || undefined });
  const projects = query.data?.projects ?? [];
  const books = query.data?.project?.id === project ? query.data.books : [];
  const invalid = Boolean(project && query.isSuccess && query.data.project?.id !== project);
  const title = source === undefined ? 'Источник заметок' : 'Фильтры заметок';
  return <Dialog width={440} label={title} onDismiss={onClose}>
    <section className={styles.sourceDialog}>
      <header><h2>{title}</h2><IconButton aria-label="Закрыть выбор источника" onClick={onClose} size="touch"><X size={16} /></IconButton></header>
      <div className={styles.sourceFields}>
      {source !== undefined ? <Select label="Тип заметки" options={noteSources} value={selectedSource} onValueChange={setSelectedSource} /> : null}
      <p>Оставьте все источники или выберите сагу и книгу.</p>
      <Select label="Сага" loading={query.isPending} onValueChange={(id) => { setProject(id); setBook(''); }} options={[{ value: '', label: 'Все саги' }, ...projects.map((item) => ({ value: item.id, label: item.title }))]} value={project} />
      <Select label="Книга" disabled={!project || query.isError || invalid} loading={Boolean(project && query.isPending)} onValueChange={setBook} options={[{ value: '', label: project ? 'Все книги' : 'Сначала выберите сагу' }, ...books.map((item) => ({ value: item.id, label: item.title }))]} value={book} />
      {query.isError || invalid ? <Alert tone="error">Не удалось загрузить источники. <Button onClick={() => void query.refetch()} variant="outline">Повторить</Button></Alert> : null}
      </div>
      <footer><Button onClick={onClose} variant="outline" size="touch">Отмена</Button><Button disabled={Boolean(project && (query.isPending || query.isError || invalid || (book && !books.some((item) => item.id === book))))} onClick={() => onApply(project || undefined, book || undefined, source === undefined ? undefined : selectedSource)} variant="primary" size="touch">Применить</Button></footer>
    </section>
  </Dialog>;
}
