import { loadFailureWords } from '../../entities/api-errors';
import { ErrorBlock } from '../../ui/feedback/ErrorBlock';
import { ArrowLeft, StickyNote, X } from 'lucide-react';
import { useLayoutEffect, useMemo, useRef } from 'react';
import type { NotesApiClient, NotesListEntry } from '../../entities/notes/api';
import { noteLine, useNotesList } from '../../entities/notes/api';
import { Button } from '../../ui/controls/Button';
import { IconButton } from '../../ui/controls/IconButton';
import { Alert } from '../../ui/feedback/Alert';
import { EmptyState } from '../../ui/feedback/EmptyState';
import { SkeletonArea } from '../../ui/feedback/Skeleton';
import { NoteCatalog, type NoteCatalogHandle, type NoteCatalogItem } from '../../ui/NoteCatalog';
import styles from './NotesPanel.module.css';

/** Where a note stands in words (NT1.3): the place of the server, the chat, «Своя мысль»; a place that is gone says so. */
function entryPlace(entry: NotesListEntry) {
  if (entry.sourceKind === 'free') return 'Своя мысль';
  if (entry.sourceKind === 'chat') return entry.chatTitle ? `Чат «${entry.chatTitle}»` : 'Ответа в чате больше нет';
  return entry.place || 'Места в книге больше нет';
}

/**
 * A row of the catalog (specs/2026-11-11-ux7-notes.md NT1.3): the line of the note and, under it, its place; «Все саги»
 * names the saga of a note (NT3.2).
 */
export function noteCatalogItem(entry: NotesListEntry, withSaga = false): NoteCatalogItem {
  const place = entryPlace(entry);
  return {
    key: entry.id,
    line: noteLine({ title: entry.title, thought: entry.preview, quote: entry.quote }) || 'Заметка',
    place: withSaga && entry.projectTitle && entry.sourceKind !== 'free' ? `Сага «${entry.projectTitle}» · ${place}` : place,
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
  const items = useMemo(() => entries.map((entry) => noteCatalogItem(entry)), [entries]);
  useLayoutEffect(() => {
    if (!visible) return;
    headingRef.current?.focus({ preventScroll: true });
    if (selectedKey) catalogRef.current?.focus(selectedKey);
  }, [visible, selectedKey]);
  return <div className={styles.layout}>
    <header className={styles.header}><h2 ref={headingRef} tabIndex={-1}>Заметки</h2><IconButton aria-label="Закрыть заметки" size="touch" onClick={onClose}><X aria-hidden="true" size={16} /></IconButton></header>
    <div className={styles.body}>
      <p className={styles.muted}>Ваши заметки к книгам и главам</p>
      {query.isPending ? <SkeletonArea count={3} shape="rows" /> : null}
      {query.isError && !query.data ? <ErrorBlock {...loadFailureWords(query.error)} kept="Ваши заметки на месте." onRetry={() => void query.refetch()} retrying={query.isRefetching} title="Не открылись заметки" /> : null}
      {/* «Новая заметка» of the footer is the first step: the empty block names what will appear and why (PS2.1). */}
      {query.isSuccess && entries.length === 0 ? <EmptyState icon={<StickyNote size={28} />} title="Здесь будут ваши заметки"
        why="Мысль к главе или отрывок из чата — под рукой, пока вы пишете." /> : null}
      {entries.length ? <NoteCatalog ref={catalogRef} items={items} selectedKey={selectedKey} totalCount={query.data?.pages[0]?.meta.totalCount ?? entries.length} hasMore={query.hasNextPage} loadingMore={query.isFetchingNextPage} loadError={query.isFetchNextPageError} onLoadMore={() => { if (visible) void query.fetchNextPage({ cancelRefetch: false }); }} onSelect={(key) => { const entry = entries.find((item) => item.id === key); if (entry) onSelect(entry); }} /> : null}
    </div>
    <footer className={styles.footer}><Button onClick={onAll} size="touch" variant="outline">Все заметки</Button><Button className={styles.newButton} size="touch" onClick={onNew} variant="primary">Новая заметка</Button></footer>
  </div>;
}

export function NoteReadFailure({ onBack, onRetry }: { onBack: () => void; onRetry: () => void }) {
  return <div className={styles.layout}><header className={styles.header}><IconButton aria-label="К списку заметок" onClick={onBack} size="touch"><ArrowLeft aria-hidden="true" size={16} /></IconButton><h2>Заметка</h2></header><div className={styles.body}><Alert tone="error">Не удалось открыть заметку. Возможно, запись удалена или доступ изменился.</Alert><Button onClick={onRetry} variant="outline">Повторить</Button></div></div>;
}
