import { ArrowLeft, X } from 'lucide-react';
import { useLayoutEffect, useMemo, useRef } from 'react';
import type { NotesApiClient, NotesListEntry } from '../../entities/notes/api';
import { useNotesList } from '../../entities/notes/api';
import { Button } from '../../ui/controls/Button';
import { IconButton } from '../../ui/controls/IconButton';
import { Alert } from '../../ui/feedback/Alert';
import { Skeleton } from '../../ui/feedback/Skeleton';
import { NoteCatalog, type NoteCatalogHandle, type NoteCatalogItem } from '../../ui/NoteCatalog';
import styles from './NotesPanel.module.css';

export function noteCatalogItem(entry: NotesListEntry): NoteCatalogItem {
  const type = entry.sourceKind === 'chat' ? 'Из чата' : entry.sourceKind === 'chapter' ? 'К главе' : 'Запись';
  return {
    key: entry.id,
    title: entry.title.trim() || entry.preview.split('\n').find((line) => line.trim()) || type,
    preview: entry.preview,
    type,
    source: [entry.bookTitle, entry.chapterTitle].filter(Boolean).join(' · ') || entry.projectTitle || (entry.sourceKind === 'free' ? 'Без источника' : type),
    updatedAt: entry.updatedAt,
  };
}

export function NotesList({ api, accountId, onNew, onSelect, onClose, onAll, visible = true, selectedKey }: {
  api: NotesApiClient;
  accountId: string;
  onNew: () => void;
  onSelect: (entry: NotesListEntry) => void;
  onClose: () => void;
  onAll: () => void;
  visible?: boolean;
  selectedKey?: string;
}) {
  const query = useNotesList(api, accountId, visible);
  const catalogRef = useRef<NoteCatalogHandle>(null);
  const headingRef = useRef<HTMLHeadingElement>(null);
  const entries = useMemo(() => [...new Map(query.data?.pages.flatMap((page) => page.data).map((entry) => [entry.id, entry]) ?? []).values()], [query.data]);
  const items = useMemo(() => entries.map(noteCatalogItem), [entries]);
  useLayoutEffect(() => {
    if (!visible) return;
    headingRef.current?.focus({ preventScroll: true });
    if (selectedKey) catalogRef.current?.focus(selectedKey);
  }, [visible, selectedKey]);
  return <div className={styles.layout}>
    <header className={styles.header}><h2 ref={headingRef} tabIndex={-1}>Заметки</h2><IconButton aria-label="Закрыть заметки" size="touch" onClick={onClose}><X aria-hidden="true" size={16} /></IconButton></header>
    <div className={styles.body}>
      <p className={styles.muted}>Ваши записи и заметки к доступным книгам</p>
      {query.isPending ? <div role="status" aria-label="Загрузка заметок"><Skeleton style={{ height: 112 }} /><Skeleton style={{ height: 112, marginTop: 12 }} /></div> : null}
      {query.isError && !query.data ? <Alert tone="error">Не удалось загрузить заметки. <Button variant="outline" onClick={() => void query.refetch()}>Повторить</Button></Alert> : null}
      {query.isSuccess && entries.length === 0 ? <div className={styles.empty}><h3>Пока нет заметок</h3><p className={styles.muted}>Запишите свою мысль или сохраните фрагмент из чата или главы. Записи будут доступны здесь.</p></div> : null}
      {entries.length ? <NoteCatalog ref={catalogRef} items={items} selectedKey={selectedKey} totalCount={query.data?.pages[0]?.meta.totalCount ?? entries.length} hasMore={query.hasNextPage} loadingMore={query.isFetchingNextPage} loadError={query.isFetchNextPageError} onLoadMore={() => { if (visible) void query.fetchNextPage({ cancelRefetch: false }); }} onSelect={(key) => { const entry = entries.find((item) => item.id === key); if (entry) onSelect(entry); }} /> : null}
    </div>
    <footer className={styles.footer}><Button onClick={onAll} size="touch" variant="outline">Все заметки</Button><Button className={styles.newButton} size="touch" onClick={onNew} variant="primary">Новая заметка</Button></footer>
  </div>;
}

export function NoteReadFailure({ onBack, onRetry }: { onBack: () => void; onRetry: () => void }) {
  return <div className={styles.layout}><header className={styles.header}><IconButton aria-label="К списку заметок" onClick={onBack} size="touch"><ArrowLeft aria-hidden="true" size={16} /></IconButton><h2>Заметка</h2></header><div className={styles.body}><Alert tone="error">Не удалось открыть заметку. Возможно, запись удалена или доступ изменился.</Alert><Button onClick={onRetry} variant="outline">Повторить</Button></div></div>;
}
