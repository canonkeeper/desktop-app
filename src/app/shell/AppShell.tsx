import { Suspense, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { useAccountQuota } from '../../entities/account-quota/api';
import { SpaceLimitLine, spaceTaken } from '../../entities/account-quota/ui/SpaceLimitLine';
import { canEditProject, textSourceView, useManuscriptShelfQuery, useSelectManuscriptProject } from '../../entities/manuscript/api';
import type { CurrentUser } from '../../entities/session/api';
import { edition } from '@edition';
import { useChatMark } from '../../features/chat/answer-ready';
import { ConfirmProjectDelete } from '../../features/manuscript/shelf/ShelfLayers';
import { firstSagaTitle } from '../../features/manuscript/shelf/FirstScreen';
import { useDeletedSagaToast } from '../../features/manuscript/shelf/useDeletedSagaToast';
import { BookUpdateChanges, useBookUpdateLines, type BookChanges } from '../../features/manuscript/shelf/BookUpdateLines';
import { useBookRevert } from '../../features/manuscript/shelf/BookRevert';
import { useAloneInSaga, useSagaSettings } from '../../features/manuscript/shelf/useSagaActions';
import { DownloadDialog, useSagaDownload } from '../../features/manuscript/shelf/BookDownload';
import { useShelfSearch } from '../../features/manuscript/shelf/useShelfSearch';
import { SearchPanel } from '../../features/manuscript/SearchPanel';
import { chapterSearchDestination } from '../../features/manuscript/search-result-navigation';
import type { CanonKeeperApiClient } from '../../shared/api';
import { toHref, type AppRoute, type RouteNavigate } from '../../shared/navigation/app-route';
import { notesNavigationQuery, notesWorkspaceRoute } from '../../shared/navigation/notes-route';
import { sectionTitle, usePageTitlePart } from '../../shared/navigation/page-title';
import { useSagaChoice } from '../../ui/navigation/SagaMenu';
import { usePresence } from '../../ui/overlays/use-presence';
import { useSagaSections, type SagaSection } from '../../ui/saga-sections';
import { BackLine, backWords } from '../../ui/shell/BackLine';
import { ReaderLine } from '../../ui/shell/ReaderLine';
import { Shell } from '../../ui/shell/Shell';
import { ShellActionsProvider } from '../../ui/shell/shell-actions';
import { activeSection, contentWidth, isReaderRoute, narrowColumn, sectionWord } from '../../ui/shell/shell-routes';
import { useToasts } from '../../ui/feedback/useToastQueue';
import { SagaSettings } from '../../ui/shell/SagaSettings';
import { PageFallback, RouteFallback } from '../RouteFallback';
import { FirstSagaLine } from './FirstSagaLine';
import { NewSagaDialog } from './NewSagaDialog';
import { RecentlyDeleted } from './RecentlyDeleted';

interface AppShellProps {
  api: CanonKeeperApiClient;
  navigate: RouteNavigate;
  route: AppRoute;
  user: CurrentUser;
  /** Signing out, where the edition signs in (specs/2026-11-07-desktop-app.md DA2.7). */
  onLogout?: () => void;
  children: ReactNode;
}

/** The saga the address names: the saga of the place the notes were opened from, the parameter of the rest (SC1.3). */
function sagaOf(route: AppRoute) {
  return route.surface === 'notes' ? notesWorkspaceRoute(route).query.get('projectId') : route.query.get('projectId');
}

/**
 * Where a choice of another saga takes the author (S3.3): the same section of that saga, the list of the section from a
 * workplace of a chapter or a drawing, the shelf from the reader and from a page without a saga.
 */
function sagaChangePlace(route: AppRoute, projectId: string): [string, Record<string, string | undefined>] {
  if (route.surface === 'notes') return ['/notes', { ...Object.fromEntries(route.query), returnTo: toHref('/manuscript/books', { projectId }) }];
  const section = activeSection(route);
  if (!section || isReaderRoute(route)) return ['/manuscript/books', { projectId }];
  return [route.path, { projectId }];
}

/**
 * The one shell of the author around the page of the address (specs/2026-11-10-ux2-shell-core.md UC1): the saga by the same
 * query of the shelf the pages read, the menu of the saga, the sections of the edition, «Поиск по книгам» and «Недавно
 * удалённые» over any page.
 */
export function AppShell({ api, children, navigate, onLogout, route, user }: AppShellProps) {
  // The one area of the toasts of the root (specs/2026-11-10-ux2-patterns-actions.md PA1.1).
  const toasts = useToasts();
  const requested = sagaOf(route);
  const shelfQuery = useManuscriptShelfQuery(api, { projectId: requested });
  const selectProject = useSelectManuscriptProject(api);
  const shelf = shelfQuery.data;
  const project = shelf?.project && (!requested || shelf.project.id === requested) ? shelf.project : null;
  const section = activeSection(route);
  // The tab names the saga of a page of the saga (SH1.3); «Тарифы» and the documents keep their own name.
  usePageTitlePart('saga', section ? project?.title : undefined);
  const editionSections = useSagaSections();
  // The mark of «Чат» by the turns the browser awaits and the chats of this saga the list shows working (AA2.2).
  const chat = useChatMark(route, section !== null ? project?.id : null, api);
  // The marks of the closed sections of the edition by the same mechanism: «Рисунки» of the site (ux6-drawing-flow DF2.2).
  const marks = edition.useSectionMarks?.(route, {
    api, projectId: section !== null ? project?.id ?? null : null, canEdit: project ? canEditProject(project) : false,
  });
  const drawing = marks?.drawing ?? null;
  // «Что помнит помощник» while a file of the materials is read and its records wait (specs/2026-11-11-ux9-shelf-chat.md SC3.6).
  const memory = marks?.memory ?? null;
  const [deletedOpen, setDeletedOpen] = useState(false);
  const deletedTrigger = useRef<HTMLElement | null>(null);
  const routeBookId = route.surface === 'manuscript' ? route.query.get('bookId') : null;
  const book = shelf?.books.find((item) => item.id === routeBookId) ?? null;
  // A result opens where it lies; the reader read in focus stays in focus (specs/2026-11-10-ux2-shell-core.md SC1.7).
  const focusReading = isReaderRoute(route) && route.query.get('focus') === '1';
  const searchNavigate = (path: string, query?: Record<string, string | undefined>) => navigate(
    path, focusReading && path === '/manuscript/books' ? { ...query, focus: '1' } : query,
  );
  const search = useShelfSearch({ api, book, navigate: searchNavigate, project, sourceView: textSourceView });
  const searchPresence = usePresence(search.searchOpen);
  const searchForRef = useRef(search.searchFor);
  searchForRef.current = search.searchFor;
  const { resetForProject } = search;
  // The search of another saga starts empty.
  useEffect(() => resetForProject(), [project?.id, resetForProject]);
  const [newSaga, setNewSaga] = useState(false);
  // «Скачать всю сагу…» (specs/2026-11-11-ux9-shelf-chat.md SC1.5): the books with text, in the order of the shelf.
  const [sagaDownloadOpen, setSagaDownloadOpen] = useState(false);
  const sagaDownload = useSagaDownload({ api, toasts });
  const sagaBooks = (shelf?.project && shelf.project.id === project?.id ? shelf.books : []).filter((book) => book.chapterCount > 0);
  const [settings, setSettings] = useState<{ error: string | null } | null>(null);
  const [deleting, setDeleting] = useState<{ error: string | null } | null>(null);
  const announceDeleted = useDeletedSagaToast({ api, navigate, toasts });
  // The result of an update of a book from a file comes wherever the author is (specs/2026-11-11-ux7-trash.md TB4.1), and the
  // book can be returned to the version before it for 7 days (specs/2026-11-12-ux10-book-revert.md BR3).
  const [bookChanges, setBookChanges] = useState<BookChanges | null>(null);
  const shelfOfSaga = shelf?.project && shelf.project.id === project?.id ? shelf : null;
  useBookUpdateLines({ api, books: shelfOfSaga?.books ?? [], jobs: shelfOfSaga?.jobs ?? [], onChanges: setBookChanges, toasts });
  const changedBook = shelfOfSaga?.books.find((item) => item.id === bookChanges?.bookId) ?? null;
  const bookRevert = useBookRevert({ api, onReturned: () => setBookChanges(null), toasts });
  // An author alone in his saga is told no role (specs/2026-11-11-ux9-shelf-chat.md SC1.1).
  const alone = useAloneInSaga(api, project, settings !== null);
  const sagaSettings = useSagaSettings({ api, navigate, onDeleted: (deleted) => announceDeleted({ kind: 'project', id: deleted.id, title: deleted.title }) });
  const deletePresence = usePresence(deleting && project ? project : null);
  // The sagas take their place: «Новая сага» says so in one quiet line; its window says the refusal after the press (PR2.7).
  const quota = useAccountQuota(api).data;
  const newSagaNote = spaceTaken('quota.projects', quota) ? <SpaceLimitLine as="span" code="quota.projects" quota={quota} /> : undefined;
  const sagaChoice = useSagaChoice({
    createNote: newSagaNote,
    current: project, sagas: shelf?.projects ?? [], choosing: selectProject.isPending, failed: selectProject.isError,
    onChoose: (projectId) => selectProject.mutate(projectId, { onSuccess: () => {
      sagaChoice.close();
      navigate(...sagaChangePlace(route, projectId));
    } }),
    onCreate: () => setNewSaga(true),
    onSettings: () => setSettings({ error: null }),
    // The whole saga in one archive, for whoever may take the file of a book (SC1.5).
    onDownload: project && canEditProject(project) && sagaBooks.length > 0 ? () => setSagaDownloadOpen(true) : undefined,
    // An old link to a saga that is gone: the name says so and its menu leads to another (board 14·10).
    missing: requested && shelf && !project && shelf.projects.length ? 'Сага не найдена' : undefined,
  });

  async function saveName(name: string) {
    if (!project) return;
    const error = await sagaSettings.rename(project, name);
    if (error) {
      setSettings({ error });
      return;
    }
    setSettings(null);
    if (name.trim() !== project.title) toasts.push({ message: 'Сага переименована.', tone: 'success' });
  }

  async function deleteSaga() {
    if (!project) return;
    const error = await sagaSettings.remove(project);
    setDeleting(error ? { error } : null);
  }

  // The windows a page opens through the shell: the focus comes back to what opened them.
  const actions = useMemo(() => ({
    openDeleted: () => {
      deletedTrigger.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
      setDeletedOpen(true);
    },
    openNewSaga: () => setNewSaga(true),
    searchFor: (text: string) => void searchForRef.current(text),
    openBookChanges: setBookChanges,
  }), []);

  function openSection(next: SagaSection, trigger: HTMLElement) {
    // While the shelf is read the saga of the address stands for it.
    const projectId = project?.id ?? requested ?? undefined;
    if (next === 'deleted') {
      deletedTrigger.current = trigger;
      setDeletedOpen(true);
      return;
    }
    if (next === 'notes') {
      navigate('/notes', notesNavigationQuery(route));
      return;
    }
    // From the notes the section they were opened from opens where the author was: the chat, the chapter, the drawing.
    const origin = route.surface === 'notes' ? notesWorkspaceRoute(route) : null;
    if (origin && activeSection(origin) === next && origin.query.get('projectId') === (projectId ?? null)) {
      navigate(origin.path, Object.fromEntries(origin.query));
      return;
    }
    if (next === 'manuscript') {
      // The book read and its chapter go back to the shelf with the author: «Продолжить чтение» opens them (S2.2).
      const reading = route.surface === 'manuscript';
      navigate('/manuscript/books', {
        projectId,
        bookId: reading ? route.query.get('bookId') ?? undefined : undefined,
        chapterId: reading && isReaderRoute(route) ? route.query.get('chapterId') ?? undefined : undefined,
      });
      return;
    }
    if (next === 'chat' && route.surface === 'chat') {
      navigate('/chat', { projectId, chatId: route.query.get('chatId') ?? undefined });
      return;
    }
    const path = { saga: '/saga', memory: '/memory', chat: '/chat', writing: '/writing', drawing: '/drawing' }[next];
    navigate(path, { projectId });
  }

  const noSaga = Boolean(shelf && shelf.projects.length === 0);
  const firstSaga = noSaga && section !== null;
  // Without a saga every page of a saga is the first screen (specs/2026-11-10-ux3-first-screen.md FS1.1): the shelf
  // without an address of a saga shows it, any other one leads there once the shelf read is settled.
  const elsewhere = firstSaga && !(route.surface === 'manuscript' && !isReaderRoute(route) && [...route.query.keys()].length === 0);
  const settled = !shelfQuery.isFetching;
  useEffect(() => {
    if (elsewhere && settled) navigate('/manuscript/books', undefined, { replace: true });
  }, [elsewhere, navigate, settled]);
  // The name of the content for the reader of the screen; the top bar names only the saga, or the first one to come
  // (specs/2026-11-11-ux2-polish.md PL1.1).
  const title = firstSaga ? 'Первая сага' : sectionWord(route, sectionTitle(route)) ?? '';
  // A reader and an editor of a saga that is not theirs read one line of their role (specs/2026-11-10-ux2-patterns-refusals.md PR3.4).
  const role = project && section !== null ? project.currentMembership.role : null;
  const limited = role === 'viewer' || role === 'editor' ? role : null;
  // A saga the author did not name himself — «Поговорить» or a proofreading founded it — says its name until he renames
  // it or puts a book in it (FS1.4, FS2.1).
  const unnamed = project && project.title === firstSagaTitle && shelf?.books.length === 0 && (role === 'owner' || role === 'admin');
  return (
    <Shell
      account={onLogout ? { onLogout, userEmail: user.email, userName: user.displayName } : undefined}
      // «Настройки» in the place of the account menu where the edition has their page (specs/2026-11-13-desk-settings.md ST1.1).
      settings={edition.pages.settings ? { active: route.surface === 'settings', onOpen: () => navigate('/settings') } : undefined}
      bare={focusReading}
      chat={chat}
      drawing={drawing}
      memory={memory}
      label={title}
      narrow={narrowColumn(route)}
      newSagaNote={newSagaNote}
      notice={limited ? <ReaderLine role={limited} /> : unnamed ? <FirstSagaLine name={project.title} onRename={() => setSettings({ error: null })} /> : undefined}
      onNewSaga={() => setNewSaga(true)}
      onOpenSearch={() => search.openSearch(isReaderRoute(route) ? 'book' : 'project')}
      overlays={overlays()}
      saga={sagaChoice.saga}
      searchRef={search.searchTriggerRef}
      place={firstSaga ? title : undefined}
      sections={noSaga ? null : { active: section, items: editionSections, onOpen: openSection }}
      width={contentWidth(route)}
      // The way back to the section the author came from by a link: a step of the history of the tab (PS4.1).
      back={route.from ? <BackLine onBack={() => navigate(route.path, undefined, { back: true })} words={backWords(route.from)} /> : undefined}
    >
      <ShellActionsProvider value={actions}>
        <Suspense fallback={<PageFallback />}>{elsewhere ? <RouteFallback /> : children}</Suspense>
      </ShellActionsProvider>
    </Shell>
  );

  function overlays() {
    return <>
      {sagaChoice.menu}
      {searchPresence.mounted ? (
        <SearchPanel
          api={api}
          bookName={book?.title}
          bookScopeDisabled={!book}
          coverage={search.searchCoverage}
          onClose={() => {
            search.setSearchOpen(false);
            search.searchTriggerRef.current?.focus();
          }}
          onOpenChapter={(locator) => {
            search.setSearchOpen(false);
            const destination = chapterSearchDestination(locator);
            searchNavigate(destination.path, destination.query);
          }}
          onOpenResult={search.openSearchResult}
          onRunSearch={(event) => {
            event?.preventDefault();
            void search.runSearch();
          }}
          results={search.searchResults}
          returnFocusRef={search.searchTriggerRef}
          scope={search.searchScope}
          searchError={search.searchError}
          searchRequestId={search.searchRequestId}
          searchStatus={search.searchStatus}
          searchTerm={search.searchTerm}
          setScope={search.selectSearchScope}
          setSearchTerm={search.setSearchTerm}
          sourceView={textSourceView}
          state={searchPresence.status}
          title="Поиск по книгам"
        />
      ) : null}
      {/* «Тариф» of a refused new saga steps aside to the page of the tier; «← Вернуться к новой саге» brings the window
          back (specs/2026-11-10-ux2-patterns-refusals.md PR2.4). */}
      {newSaga && route.surface !== 'tariff' ? <NewSagaDialog api={api} navigate={navigate} onClose={() => setNewSaga(false)} /> : null}
      {settings && project ? (
        <SagaSettings
          alone={alone}
          error={settings.error}
          name={project.title}
          onClose={() => setSettings(null)}
          onDelete={() => {
            setSettings(null);
            setDeleting({ error: null });
          }}
          onSave={(name) => void saveName(name)}
          role={project.currentMembership.role}
          saving={sagaSettings.renaming}
        />
      ) : null}
      {deletePresence.mounted && deletePresence.value ? (
        <ConfirmProjectDelete
          error={deleting?.error ?? null}
          isDeleting={sagaSettings.deleting}
          onClose={() => setDeleting(null)}
          onDelete={() => void deleteSaga()}
          project={deletePresence.value}
          state={deletePresence.status}
        />
      ) : null}
      {sagaDownloadOpen && project ? (
        <DownloadDialog busy={sagaDownload.busy} error={sagaDownload.error}
          drawings={editionSections.includes('drawing') ? { api, projectId: project.id, bookIds: sagaBooks.map((book) => book.id) } : null}
          heading={`Скачать всю сагу «${project.title}»`} onClose={() => { setSagaDownloadOpen(false); sagaDownload.reset(); }}
          onDownload={(format, drawings) => void sagaDownload.download(project.title, sagaBooks, format, drawings).then(() => setSagaDownloadOpen(false), () => undefined)}
          progress={sagaDownload.progress} sagaBooks={sagaBooks.length} />
      ) : null}
      {bookChanges ? (
        <BookUpdateChanges api={api} asking={bookRevert.asking === bookChanges.bookId} book={changedBook}
          canRevert={canEditProject(project)} jobId={changedBook?.update?.jobId ?? bookChanges.jobId} onClose={() => setBookChanges(null)}
          onRevert={() => changedBook && bookRevert.ask({ bookId: changedBook.id, title: changedBook.title })} />
      ) : null}
      {bookRevert.element}
      {/* «Недавно удалённые» of every saga of the author (specs/2026-11-11-ux7-trash.md TB1.1): the server lists what each
          role may return. */}
      {deletedOpen ? (
        <RecentlyDeleted
          api={api}
          notify={toasts.push}
          onClose={() => {
            setDeletedOpen(false);
            deletedTrigger.current?.focus();
          }}
          onOpen={(place) => {
            // What came back opens in its place, and the window steps aside for it.
            setDeletedOpen(false);
            navigate(place.path, place.query);
          }}
          onRevertBook={(book) => bookRevert.ask({ bookId: book.id, title: book.title })}
          revertAsking={bookRevert.asking}
        />
      ) : null}
    </>;
  }
}
