import { loadFailureWords } from '../../entities/api-errors';
import { ErrorBlock } from '../../ui/feedback/ErrorBlock';
import { Plus, SlidersHorizontal, StickyNote, X } from 'lucide-react';
import { useEffect, useMemo, useRef, useState } from 'react';
import { useNotesList, type NotesApiClient, type NotesFilters, type NoteResolvedSource } from '../../entities/notes/api';
import { useManuscriptShelfQuery, type ManuscriptApiClient } from '../../entities/manuscript/api';
import type { AppRoute, RouteNavigate } from '../../shared/navigation/app-route';
import { manuscriptSourceDestination } from '../../shared/navigation/manuscript-route';
import { notesWorkspaceRoute } from '../../shared/navigation/notes-route';
import { useNotes } from '../../shared/notes/notes-context';
import { formatCount, pluralize } from '../../ui/copy';
import { Button } from '../../ui/controls/Button';
import { SearchField } from '../../ui/controls/SearchField';
import { Select } from '../../ui/controls/Select';
import { NoteCatalog, type NoteCatalogHandle } from '../../ui/NoteCatalog';
import { Alert } from '../../ui/feedback/Alert';
import { EmptyFilter, EmptyState } from '../../ui/feedback/EmptyState';
import { SkeletonArea } from '../../ui/feedback/Skeleton';
import { PageHead } from '../../ui/shell/PageHead';
import { Overlay } from '../../ui/overlays/Overlay';
import { useToasts } from '../../ui/feedback/useToastQueue';
import { NoteDetails } from './NoteDetails';
import type { NoteEditorHandle } from './NoteEditor';
import { noteCatalogItem } from './NotesList';
import { NotesFilterDialog, noteOrigins, sagaBooks } from './NotesSourceFilter';
import { useNotesViewport } from './useNotesViewport';
import styles from './NotesWorkspace.module.css';

/**
 * «Заметки» (specs/2026-11-11-ux7-notes.md NT2.1, NT3.1, NT3.2): the notes of the open saga with the own thoughts, the
 * filters «Откуда» and «Книга» («Все саги» is its last choice) and the open note — all in the address.
 */
export function NotesWorkspace({ api, accountId, route, navigate }: {
  api: NotesApiClient & ManuscriptApiClient; accountId: string; route: AppRoute; navigate: RouteNavigate;
}) {
  const notes = useNotes();
  const [search, setSearch] = useState('');
  const [queryText, setQueryText] = useState('');
  const [filtersOpen, setFiltersOpen] = useState(false);
  const selection = route.query.get('note');
  const { compact, overlay: narrow } = useNotesViewport();
  const editorRef = useRef<NoteEditorHandle>(null);
  const catalogRef = useRef<NoteCatalogHandle>(null);
  const returnToKey = useRef<string | undefined>(undefined);
  const toasts = useToasts();
  // The open saga is the saga of the shell: the saga of the place the notes were opened from (SC1.3).
  const requestedSaga = notesWorkspaceRoute(route).query.get('projectId');
  const shelfQuery = useManuscriptShelfQuery(api, { projectId: requestedSaga });
  const shelf = shelfQuery.data;
  const saga = shelf?.project && (!requestedSaga || shelf.project.id === requestedSaga) ? shelf.project : null;
  const books = saga ? shelf?.books ?? [] : [];
  const allSagas = route.query.get('sagas') === 'all' || (!shelfQuery.isPending && !saga);
  const requestedBook = route.query.get('bookId');
  const bookId = !allSagas && requestedBook && books.some((book) => book.id === requestedBook) ? requestedBook : undefined;
  const source = (route.query.get('source') as NotesFilters['source']) || undefined;
  const filters: NotesFilters = { query: queryText || undefined, source, projectId: allSagas ? undefined : saga?.id, bookId };
  const signature = JSON.stringify(filters);
  const searchTooLong = Array.from(search).length > 200;
  // The list waits for the saga of the shell: the first page is the saga's, not every note.
  const query = useNotesList(api, accountId, !route.invalidQuery && !searchTooLong && !shelfQuery.isPending, filters);
  const entries = useMemo(() => [...new Map(query.data?.pages.flatMap((page) => page.data).map((entry) => [entry.id, entry]) ?? []).values()], [query.data]);
  const items = useMemo(() => entries.map((entry) => noteCatalogItem(entry, allSagas)), [allSagas, entries]);
  const count = query.data?.pages[0]?.meta.totalCount ?? 0;
  const bookChoices = sagaBooks(books, Boolean(saga));
  const bookValue = allSagas ? 'all' : bookId ?? '';

  useEffect(() => { if (searchTooLong) return; const timer = window.setTimeout(() => setQueryText(search.trim()), 250); return () => window.clearTimeout(timer); }, [search, searchTooLong]);
  useEffect(() => {
    if (selection || !returnToKey.current) return;
    catalogRef.current?.focus(returnToKey.current);
    returnToKey.current = undefined;
  }, [selection]);

  /** The address of the catalog with its filters and the open note; the search stays in the page. */
  function address(next: { source?: string | undefined; book?: string | undefined; note?: string | null | undefined } = {}) {
    const book = 'book' in next ? next.book : bookValue;
    return {
      source: 'source' in next ? next.source || undefined : source,
      bookId: book && book !== 'all' ? book : undefined,
      sagas: book === 'all' ? 'all' : undefined,
      note: ('note' in next ? next.note : selection) ?? undefined,
    };
  }
  function changeFilters(next: { source?: string | undefined; book?: string | undefined }) {
    navigate('/notes', address(next), { replace: true });
  }
  function reset() { setSearch(''); setQueryText(''); navigate('/notes', { note: selection ?? undefined }, { replace: true }); }
  function open(key: string) {
    // The first note opened is a step of the history; another one replaces it (NT2.1).
    navigate('/notes', address({ note: key }), selection ? { replace: true } : undefined);
  }
  function close() {
    returnToKey.current = selection ?? undefined;
    navigate('/notes', address({ note: null }), { replace: true });
  }
  function leave(run: () => void) { if (editorRef.current) editorRef.current.leave(run); else run(); }
  function openSource(target: NoteResolvedSource, noteId: string, name: string) {
    // «К месту в книге»: the reader at the paragraph with this note beside the text, «← К заметке «…»» back (NT2.2).
    const from = { from: { section: 'notes', name } } as const;
    if (target.kind === 'chat') navigate('/chat', { projectId: target.projectId, chatId: target.chatId, noteId }, from);
    else { const destination = manuscriptSourceDestination(target.locator); navigate(destination.path, { ...destination.query, contents: 'notes', note: noteId }, from); }
  }

  const filtered = Boolean(queryText || source || bookId);
  const emptySection = query.isSuccess && !entries.length && !filtered;
  const newNote = <Button onClick={() => leave(() => { if (selection) close(); notes?.capture({ body: '' }); })} size="touch" variant="primary"><Plus size={16} />Новая заметка</Button>;

  return <section className={styles.workspace} aria-label="Каталог заметок">
    <div className={styles.background}>
    {/* An empty section has its first step in its empty block: the top keeps no second main button (П-4). */}
    <PageHead action={emptySection ? undefined : newNote} line={query.data && !emptySection ? `${filtered ? `${pluralize(count, 'Найдена', 'Найдено', 'Найдено')} ` : ''}${formatCount(count, 'заметка', 'заметки', 'заметок')}` : 'Ваши заметки к книгам и главам'} surface="notes" />
    <div className={styles.searchRow}><div className={styles.search}><SearchField label="Найти в заметках" placeholder={compact ? 'Найти в заметках…' : 'Найти в заметках'} value={search} onChange={(event) => setSearch(event.target.value)} onClear={() => setSearch('')} error={Array.from(search).length > 200 ? 'Не больше 200 символов в поисковом запросе.' : undefined} /></div>
      {compact ? <Button aria-label="Фильтры заметок" onClick={() => setFiltersOpen(true)} size="touch" variant={source || bookValue ? 'secondary' : 'ghost'}><SlidersHorizontal size={16} />Фильтры</Button> : null}
    </div>
    <div className={styles.filters}>
      <div className={styles.filter}><Select label="Откуда" onValueChange={(value) => changeFilters({ source: value })} options={noteOrigins} value={source ?? ''} /></div>
      <div className={styles.filter} data-wide=""><Select label="Книга" loading={shelfQuery.isPending} onValueChange={(value) => changeFilters({ book: value })} options={bookChoices} value={bookValue} /></div>
      {search || source || bookValue ? <Button onClick={reset} size="touch" variant="ghost"><X size={16} />Сбросить</Button> : null}
    </div>
    </div>
    <div className={styles.columns}>
      <div className={styles.list}>
        {route.invalidQuery ? <Alert tone="error">Не удалось прочитать фильтры. <Button onClick={reset} variant="outline">Сбросить</Button></Alert>
          : query.isPending ? <SkeletonArea count={6} shape="rows" />
            : query.isError && !query.data ? <ErrorBlock {...loadFailureWords(query.error)} kept="Ваши заметки на месте; поиск и фильтры сохранены." onRetry={() => void query.refetch()} retrying={query.isRefetching} title="Не открылись заметки" />
              : !entries.length ? (filtered
                // Nothing by the search or the filter is not an empty section (PS2.3): it gives the search and the filter back.
                ? <EmptyFilter onReset={reset} reset={queryText ? 'Сбросить поиск' : 'Сбросить отбор'} words={queryText ? `Ничего не нашлось по «${queryText}»` : undefined} />
                : <EmptyState action={newNote} icon={<StickyNote size={28} />} title="Здесь будут ваши заметки" why="Мысль к главе или отрывок из чата — под рукой, пока вы пишете." />)
                : <NoteCatalog key={signature} ref={catalogRef} items={items} totalCount={count} selectedKey={selection ?? undefined} onSelect={(key) => leave(() => { if (entries.some((item) => item.id === key)) open(key); })} hasMore={query.hasNextPage} loadingMore={query.isFetchingNextPage} loadError={query.isFetchNextPageError} onLoadMore={() => void query.fetchNextPage({ cancelRefetch: false })} />}
      </div>
      {selection ? <div className={styles.detailLayer} data-overlay-scrim={narrow ? '' : undefined} onPointerDown={(event) => { if (narrow && event.target === event.currentTarget) leave(close); }}>
        {/* The note is opened by the address: «Назад» of the browser is the step of the page, not of the window (PA4.2). */}
        <Overlay className={styles.detail} history={false} kind="drawer" label="Заметка" modal={narrow} onDismiss={() => leave(close)} onKeyDown={(event) => { if (!narrow && event.key === 'Escape') { event.preventDefault(); leave(close); } }}>
          <NoteDetails api={api} accountId={accountId} noteId={selection}
            editorRef={editorRef} embedded key={selection} onBack={narrow ? close : undefined} onClose={close}
            onSaved={(outcome) => { if (outcome === 'deleted') close(); toasts.push({ message: outcome === 'saved' ? 'Заметка сохранена' : 'Заметка удалена', tone: 'success' }); }} onSource={openSource} />
        </Overlay>
      </div> : null}
    </div>
    {filtersOpen ? <NotesFilterDialog books={bookChoices} book={bookValue} source={source ?? ''} onClose={() => setFiltersOpen(false)}
      onApply={(next) => { setFiltersOpen(false); changeFilters(next); }} /> : null}
  </section>;
}
