import { ChevronDown, Plus, SlidersHorizontal, X } from 'lucide-react';
import { useEffect, useMemo, useRef, useState } from 'react';
import { useNotesList, type NotesApiClient, type NotesFilters, type NoteResolvedSource } from '../../entities/notes/api';
import { useManuscriptShelfQuery, type ManuscriptApiClient } from '../../entities/manuscript/api';
import type { AppRoute, RouteNavigate } from '../../shared/navigation/app-route';
import { manuscriptSourceDestination } from '../../shared/navigation/manuscript-route';
import { useNotes } from '../../shared/notes/notes-context';
import { formatCount, pluralize } from '../../ui/copy';
import { Button } from '../../ui/controls/Button';
import { SearchField } from '../../ui/controls/SearchField';
import { Select } from '../../ui/controls/Select';
import { NoteCatalog, type NoteCatalogHandle } from '../../ui/NoteCatalog';
import { Alert } from '../../ui/feedback/Alert';
import { Skeleton } from '../../ui/feedback/Skeleton';
import { Overlay } from '../../ui/overlays/Overlay';
import { ToastRegion } from '../../ui/feedback/Toast';
import { useToastQueue } from '../../ui/feedback/useToastQueue';
import { NoteDetails } from './NoteDetails';
import type { NoteEditorHandle } from './NoteEditor';
import { noteCatalogItem } from './NotesList';
import { NotesSourceFilter, noteSources } from './NotesSourceFilter';
import { useNotesViewport } from './useNotesViewport';
import styles from './NotesWorkspace.module.css';

export function NotesWorkspace({ api, accountId, route, navigate }: {
  api: NotesApiClient & ManuscriptApiClient; accountId: string; route: AppRoute; navigate: RouteNavigate;
}) {
  const notes = useNotes();
  const [search, setSearch] = useState('');
  const [queryText, setQueryText] = useState('');
  const [sourceOpen, setSourceOpen] = useState(false);
  const [selection, setSelection] = useState<string | null>(null);
  const { compact, overlay: narrow } = useNotesViewport();
  const editorRef = useRef<NoteEditorHandle>(null);
  const catalogRef = useRef<NoteCatalogHandle>(null);
  const returnToKey = useRef<string | undefined>(undefined);
  const toasts = useToastQueue();
  const filters: NotesFilters = {
    query: queryText || undefined,
    source: (route.query.get('source') as NotesFilters['source']) || undefined,
    projectId: route.query.get('projectId') || undefined,
    bookId: route.query.get('bookId') || undefined,
  };
  const signature = JSON.stringify(filters);
  const shelf = useManuscriptShelfQuery(api, { projectId: filters.projectId ?? '' }).data;
  const sourceLabel = filters.bookId ? shelf?.books.find((book) => book.id === filters.bookId)?.title ?? 'Выбрана книга'
    : filters.projectId ? shelf?.projects.find((project) => project.id === filters.projectId)?.title ?? 'Выбрана сага' : 'Все источники';
  const searchTooLong = Array.from(search).length > 200;
  const query = useNotesList(api, accountId, !route.invalidQuery && !searchTooLong, filters);
  const entries = useMemo(() => [...new Map(query.data?.pages.flatMap((page) => page.data).map((entry) => [entry.id, entry]) ?? []).values()], [query.data]);
  const items = useMemo(() => entries.map(noteCatalogItem), [entries]);
  const count = query.data?.pages[0]?.meta.totalCount ?? 0;
  const currentSource = filters.source ?? '';

  useEffect(() => { if (searchTooLong) return; const timer = window.setTimeout(() => setQueryText(search.trim()), 250); return () => window.clearTimeout(timer); }, [search, searchTooLong]);
  useEffect(() => {
    if (selection || !returnToKey.current) return;
    catalogRef.current?.focus(returnToKey.current);
    returnToKey.current = undefined;
  }, [selection]);

  function changeFilters(next: Partial<NotesFilters>) {
    const { query: _query, ...navigation } = { ...filters, ...next };
    navigate('/notes', navigation);
  }
  function reset() { setSearch(''); setQueryText(''); navigate('/notes'); }
  function close() {
    returnToKey.current = selection ?? undefined;
    setSelection(null);
  }
  function leave(run: () => void) { if (editorRef.current) editorRef.current.leave(run); else run(); }
  function openSource(source: NoteResolvedSource, noteId: string) {
    if (source.kind === 'chat') navigate('/chat', { projectId: source.projectId, chatId: source.chatId, noteId });
    else { const target = manuscriptSourceDestination(source.locator); navigate(target.path, target.query); }
  }

  return <section className={styles.workspace} aria-label="Каталог заметок">
    <div className={styles.background}>
    <div className={styles.heading}><div><h1>Заметки</h1><p>{query.isPending ? 'Загрузка заметок…' : query.data ? `${queryText || filters.source || filters.projectId ? `${pluralize(count, 'Найдена', 'Найдено', 'Найдено')} ` : ''}${formatCount(count, 'заметка', 'заметки', 'заметок')}` : 'Ваши записи и заметки к книгам'}</p></div><Button onClick={() => leave(() => { setSelection(null); notes?.capture({ body: '' }); })} size="touch" variant="primary"><Plus size={16} />Новая заметка</Button></div>
    <div className={styles.searchRow}><div className={styles.search}><SearchField label="Поиск по названию и тексту" placeholder={compact ? 'Поиск заметок…' : 'Поиск по названию и тексту'} value={search} onChange={(event) => setSearch(event.target.value)} onClear={() => setSearch('')} error={Array.from(search).length > 200 ? 'Не больше 200 символов в поисковом запросе.' : undefined} /></div>
      {compact ? <Button aria-label="Фильтры заметок" onClick={() => setSourceOpen(true)} size="touch" variant={currentSource || filters.projectId ? 'secondary' : 'ghost'}><SlidersHorizontal size={16} />Фильтры</Button> : null}
    </div>
    <div className={styles.filters}>
      <div className={styles.typeFilter}><Select label="Тип заметки" onValueChange={(value) => changeFilters({ source: (value || undefined) as NotesFilters['source'] })} options={noteSources} value={currentSource} /></div>
      <Button className={styles.sourceTrigger} onClick={() => setSourceOpen(true)} size="touch" variant="outline"><span>{sourceLabel}</span><ChevronDown size={16} /></Button>
      {search || filters.source || filters.projectId ? <Button onClick={reset} size="touch" variant="ghost"><X size={16} />Сбросить</Button> : null}
    </div>
    </div>
    <div className={styles.columns}>
      <div className={styles.list}>
        {route.invalidQuery ? <Alert tone="error">Не удалось прочитать фильтры. <Button onClick={reset} variant="outline">Сбросить</Button></Alert>
          : query.isPending ? <div className={styles.loading} role="status" aria-label="Загрузка заметок">{Array.from({ length: 6 }, (_, index) => <Skeleton key={index} style={{ height: 64 }} />)}</div>
            : query.isError && !query.data ? <div className={styles.empty}><h2>Не удалось загрузить заметки</h2><p>Поиск и фильтры сохранены.</p><Button onClick={() => void query.refetch()} variant="outline">Повторить</Button></div>
              : !entries.length ? <div className={styles.empty}><h2>{queryText || filters.source || filters.projectId ? 'Ничего не найдено' : 'Пока нет заметок'}</h2><p>{queryText || filters.source || filters.projectId ? 'Измените запрос или сбросьте фильтры.' : 'Запишите мысль или сохраните фрагмент из чата или главы.'}</p>{queryText || filters.source || filters.projectId ? <Button onClick={reset} variant="outline">Сбросить фильтры</Button> : null}</div>
                : <NoteCatalog key={signature} ref={catalogRef} items={items} totalCount={count} selectedKey={selection ?? undefined} onSelect={(key) => leave(() => { if (entries.some((item) => item.id === key)) setSelection(key); })} hasMore={query.hasNextPage} loadingMore={query.isFetchingNextPage} loadError={query.isFetchNextPageError} onLoadMore={() => void query.fetchNextPage({ cancelRefetch: false })} />}
      </div>
      {selection ? <div className={styles.detailLayer} data-overlay-scrim={narrow ? '' : undefined} onPointerDown={(event) => { if (narrow && event.target === event.currentTarget) leave(close); }}>
        <Overlay className={styles.detail} kind="drawer" label="Заметка" modal={narrow} onDismiss={() => leave(close)} onKeyDown={(event) => { if (!narrow && event.key === 'Escape') { event.preventDefault(); leave(close); } }}>
          <NoteDetails api={api} accountId={accountId} noteId={selection}
            editorRef={editorRef} embedded key={selection} onBack={narrow ? close : undefined} onClose={close}
            onSaved={(outcome) => { if (outcome === 'deleted') close(); toasts.push({ message: outcome === 'saved' ? 'Заметка сохранена' : 'Заметка удалена', tone: 'success' }); }} onSource={openSource} />
        </Overlay>
      </div> : null}
    </div>
    {sourceOpen ? <NotesSourceFilter api={api} projectId={filters.projectId} bookId={filters.bookId} source={compact ? currentSource : undefined} onClose={() => setSourceOpen(false)} onApply={(projectId, bookId, source) => { setSourceOpen(false); changeFilters({ projectId, bookId, ...(source === undefined ? {} : { source: (source || undefined) as NotesFilters['source'] }) }); }} /> : null}
    {toasts.queue.length ? <ToastRegion onDismiss={toasts.dismiss} queue={toasts.queue} /> : null}
  </section>;
}
