import { useQueryClient } from '@tanstack/react-query';
import { type ChangeEvent, type DragEvent, useEffect, useRef, useState } from 'react';
import { invalidateAccountQuota } from '../../../entities/account-quota/api';
import { publicApiErrorMessage } from '../../../entities/api-errors';
import type { Book, BookExportFormat, ManuscriptApiClient } from '../../../entities/manuscript/api';
import * as manuscriptApi from '../../../entities/manuscript/api';
import type { AppRoute, RouteNavigate } from '../../../shared/navigation/app-route';
import { formatProjectMeta } from '../../../ui/copy';
import { usePresence } from '../../../ui/overlays/use-presence';
import { EmailVerificationNotice } from '../../../ui/feedback/EmailVerificationNotice';
import { useAccountSession } from '../../../shared/session/account-session';
import { useBookExportMutation, useCancelJobMutation, useImportQueuesQuery, useManuscriptJobQuery } from '../api';
import { BookIndexingCard } from './BookIndexingCard';
import { BookCoverEditor } from './BookCoverEditor';
import { DeletedSourcesDialog } from './DeletedSourcesDialog';
import { FirstSteps, useFirstStep } from './FirstSteps';
import { ImportQueueDialog } from './ImportQueueDialog';
import { RejectedImportFiles } from './RejectedImportFiles';
import { saveFile } from '../../../ui/save-file';
import { BookActionMenu, ShelfBooks } from './ShelfBooks';
import { useBookFileUpdate } from './BookFileUpdate';
import { ConfirmDelete, ConfirmProjectDelete } from './ShelfLayers';
import { SearchPanel } from '../SearchPanel';
import { chapterSearchDestination } from '../search-result-navigation';
import {
  FirstProjectPanel,
  ShelfBooksHeader,
  ShelfHeader,
  ShelfImportDropzone,
  type ShelfLoadStatus,
  ShelfStatusMessages,
  ProjectMissingState,
} from './ShelfHeader';
import { ShelfQuotaHints } from './ShelfQuotaHints';
import { ShelfMobileNavigation, ShelfProjectMenu, ShelfTopBar, ShelfWorkspaceNavigation } from './ShelfNavigation';
import { useBookNavigation } from '../../../entities/manuscript/useBookNavigation';
import { useNotes } from '../../../shared/notes/notes-context';
import { useShelfSearch } from './useShelfSearch';
import { useImportQueue } from './useImportQueue';
import { useShelfBookActions } from './useShelfBookActions';
import { useShelfProjectActions } from './useShelfProjectActions';
import { useVoiceFirstSaga, useVoicePlace } from '../../../shared/navigation/voice-place';
import { TopBarTalk } from '../../../ui/navigation/TopBarStrip';
import {
  activeJobBookId,
  isActiveImportJob,
  unfinishedIndexingBookId,
} from './shelf-jobs';
import styles from './ManuscriptShelfPage.module.css';

/** The saga «Поговорить» founds for an author without one: the voice asks him how to call it. */
const firstSagaTitle = 'Моя сага';

interface ManuscriptShelfPageProps {
  api: ManuscriptApiClient;
  navigate: RouteNavigate;
  onLogout?: () => void;
  route: AppRoute;
  userName: string;
}

export function ManuscriptShelfPage(props: ManuscriptShelfPageProps) {
  const projectRouteKey = props.route.query.get('projectId') ?? '';
  return <ManuscriptShelfProjectRoute key={projectRouteKey} {...props} />;
}

function ManuscriptShelfProjectRoute({ api, navigate, onLogout, route, userName }: ManuscriptShelfPageProps) {
  const queryClient = useQueryClient();
  const requestedProjectId = route.query.get('projectId');
  const requestedBookId = route.query.get('bookId');
  const [selectedBookId, setSelectedBookId] = useState(requestedBookId ?? '');
  const [notice, setNotice] = useState('');
  const [accountMenuOpen, setAccountMenuOpen] = useState(false);
  const [projectMenuOpen, setProjectMenuOpen] = useState(false);
  const [mobileMenuOpen, setMobileMenuOpen] = useState(route.query.get('menu') === 'open');
  const [deletedSourcesOpen, setDeletedSourcesOpen] = useState(false);
  const [projectDeleteOpen, setProjectDeleteOpen] = useState(false);
  const [projectCreateOpen, setProjectCreateOpen] = useState(false);
  const [dragActive, setDragActive] = useState(false);
  const [indexingCardDismissed, setIndexingCardDismissed] = useState(false);
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const deletedSourcesTriggerRef = useRef<HTMLButtonElement | null>(null);
  const reconciledTerminalQueues = useRef(new Set<string>());
  const shelfQuery = manuscriptApi.useManuscriptShelfQuery(api, { projectId: requestedProjectId });
  const workspace = shelfQuery.data;
  const projects = workspace?.projects ?? [];
  const project = workspace?.project ?? null;
  useVoicePlace(project?.id, { page: 'books' });
  const currentProjectRole = manuscriptApi.projectRole(project);
  const canDeleteProject = currentProjectRole === 'owner' || currentProjectRole === 'admin';
  const books = workspace?.books ?? [];
  const canEditManuscript = workspace?.canEditManuscript ?? false;
  const notes = useNotes();
  const bookNavigation = useBookNavigation({
    api, navigate, projectId: project?.id,
    bookId: books.some((book) => book.id === requestedBookId) ? requestedBookId ?? undefined : undefined,
    chapterId: route.query.get('chapterId') ?? undefined,
  });
  const jobs = workspace?.jobs ?? [];
  const status: ShelfLoadStatus = shelfQuery.isPending
    ? 'loading'
    : shelfQuery.isError && !workspace
      ? 'error'
      : books.length > 0
        ? 'ready'
        : 'empty';
  const sourceView = manuscriptApi.textSourceView;
  const indexingStatusQuery = manuscriptApi.useIndexingStatusQuery(api, project?.id ?? '', {
    enabled: Boolean(project),
  });
  const importQueuesQuery = useImportQueuesQuery(api, project?.id ?? '');
  const latestImportQueue = [...(importQueuesQuery.data?.data ?? [])]
    .sort((left, right) => right.updatedAt.localeCompare(left.updatedAt))[0] ?? null;
  const terminalImportQueue = latestImportQueue
    && (latestImportQueue.status === 'failed' || latestImportQueue.status === 'succeeded' || latestImportQueue.status === 'canceled')
    ? latestImportQueue
    : null;
  // Only the newest queue is active; an older failed one is history.
  const activeImportQueue = latestImportQueue
    && (latestImportQueue.status === 'uploading' || latestImportQueue.status === 'draft'
      || latestImportQueue.status === 'running' || latestImportQueue.status === 'failed')
    ? latestImportQueue
    : null;
  const activeImportQueueItem = activeImportQueue?.items.find((item) => item.id === activeImportQueue.currentItemId) ?? null;
  const currentQueueBookId = activeImportQueueItem?.bookId ?? null;
  const currentQueueJobId = activeImportQueueItem?.importJobId ?? null;

  useEffect(() => {
    if (!project || !terminalImportQueue) return;
    const terminalKey = `${terminalImportQueue.id}:${terminalImportQueue.version}`;
    if (reconciledTerminalQueues.current.has(terminalKey)) return;
    reconciledTerminalQueues.current.add(terminalKey);
    const jobIds = new Set(terminalImportQueue.items.map((item) => item.importJobId)
      .filter((jobId): jobId is string => Boolean(jobId)));
    void Promise.all([
      invalidateAccountQuota(queryClient),
      ...[...jobIds].map((jobId) => queryClient.invalidateQueries({
        queryKey: manuscriptApi.manuscriptQueryKeys.job(jobId),
        exact: true,
      })),
      queryClient.invalidateQueries({
        queryKey: manuscriptApi.manuscriptQueryKeys.indexingStatus(project.id),
        exact: false,
      }),
      // This route reads the shelf by `?projectId`, or without it for the default saga.
      ...[...new Set([project.id, requestedProjectId])].map((shelfProjectId) => queryClient.invalidateQueries({
        queryKey: manuscriptApi.manuscriptQueryKeys.shelf({ projectId: shelfProjectId }),
        exact: true,
      })),
    ]);
  }, [project, queryClient, requestedProjectId, terminalImportQueue]);
  const routeMenuBookId = route.query.get('bookMenu') === 'open' ? route.query.get('bookId') ?? selectedBookId : null;
  const routeMobileMenuOpen = route.query.get('menu') === 'open';

  useEffect(() => {
    setMobileMenuOpen(routeMobileMenuOpen);
  }, [routeMobileMenuOpen]);

  useEffect(() => {
    if (requestedBookId !== null) setSelectedBookId(requestedBookId);
  }, [requestedBookId]);

  const selectedBook = books.find((book) => book.id === selectedBookId) ?? null;
  const {
    openSearchResult,
    resetForProject,
    runSearch,
    searchCoverage,
    searchError,
    searchRequestId,
    searchOpen,
    searchResults,
    searchScope,
    searchStatus,
    searchTerm,
    searchTriggerRef,
    setSearchOpen,
    setSearchTerm,
    selectSearchScope,
  } = useShelfSearch({ api, book: selectedBook, navigate, project, sourceView });

  useEffect(() => {
    if (!workspace) return;
    if (!workspace.canEditManuscript) resetForProject();
    setSelectedBookId((current) => {
      const preferredBook =
        workspace.books
          .filter((book) => book.status === 'ready')
          .sort((left, right) => right.chapterCount - left.chapterCount || left.order - right.order)[0] ?? workspace.books[0];
      if (requestedBookId !== null) return requestedBookId;
      if (importQueuesQuery.isPending) return current;
      const indexingBookId = unfinishedIndexingBookId(indexingStatusQuery.data, sourceView);
      return currentQueueBookId
        ?? indexingBookId
        ?? workspace.books.find((book) => book.id === current)?.id
        ?? preferredBook?.id
        ?? '';
    });
  }, [
    currentQueueBookId,
    importQueuesQuery.data,
    importQueuesQuery.isPending,
    indexingStatusQuery.data,
    requestedBookId,
    resetForProject,
    sourceView,
    workspace,
  ]);

  const explicitProjectMissing = requestedProjectId !== null
    && shelfQuery.data !== undefined
    && shelfQuery.data.project === null;
  const explicitBookMissing = requestedBookId !== null
    && shelfQuery.data?.project !== null
    && shelfQuery.data?.books.every((book) => book.id !== requestedBookId) === true;
  const chatProjectId = requestedProjectId ?? project?.id ?? undefined;
  const firstStep = useFirstStep({
    api, bookCount: books.length, canEdit: canEditManuscript, project,
    loaded: !explicitProjectMissing && !explicitBookMissing && (status === 'empty' || status === 'ready'),
  });
  const currentQueuePreferredJobId = activeImportQueue?.status === 'running'
    || activeImportQueue?.status === 'failed'
    ? currentQueueJobId ?? undefined
    : undefined;
  // The active text index or cards Job of the project comes from the authoritative Jobs list;
  // the project-wide status names books, not Jobs.
  const activeIndexingJob = jobs.find((job) => (
    (job.kind === 'canon_text_index' || job.kind === 'chapter_cards')
    && (job.status === 'queued' || job.status === 'running' || job.status === 'canceling')
  ));
  const activeBookUpdateJob = jobs.find((job) => manuscriptApi.isBookUpdateJob(job)
    && (job.status === 'queued' || job.status === 'running' || job.status === 'canceling'));
  const exactPipelineJobId = currentQueuePreferredJobId ?? activeBookUpdateJob?.id ?? activeIndexingJob?.id ?? '';
  const pipelineJobQuery = useManuscriptJobQuery(api, exactPipelineJobId, Boolean(exactPipelineJobId));
  const indexingJob = pipelineJobQuery.data?.subject.projectId === project?.id ? pipelineJobQuery.data : null;
  const activeJob = indexingJob
    && (indexingJob.status === 'queued' || indexingJob.status === 'running' || indexingJob.status === 'canceling')
    ? indexingJob
    : null;
  const activeBookJobFor = (bookId: string) => indexingJob && activeJobBookId(indexingJob) === bookId
    ? indexingJob
    : null;
  const {
    changeCover,
    closeBookMenu,
    closeCoverEditor,
    coverEditorBook,
    deleteBook,
    deleteConfirmOpen,
    deletingBook,
    dismissDeleteConfirmation,
    menuBookId,
    menuPosition,
    openBook,
    openBookMenu,
    requestDeleteBook,
  } = useShelfBookActions({
    api,
    books,
    jobs,
    canEditManuscript,
    navigate,
    navigateManuscript,
    routeMenuBookId,
    selectedBook,
    selectedBookId,
    setNotice,
    setSelectedBookId,
  });
  const importQueue = useImportQueue({ api, canEditManuscript, projectId: project?.id ?? null });
  const bookUpdate = useBookFileUpdate({ api, canUpdate: canEditManuscript, onNotice: setNotice, projectId: project?.id ?? null });
  const cancelJobMutation = useCancelJobMutation(api);
  const bookExport = useBookExportMutation(api);
  // An unverified address imports nothing: every import entry stays closed (UC1.4).
  const emailUnverified = Boolean(useAccountSession()?.emailVerification);
  const importBlocked = emailUnverified || importQueue.isOpen || importQueue.operationPending
    || Boolean(activeJob && isActiveImportJob(activeJob));
  const {
    deleteCurrentProject,
    deletingProject,
    createFirstProject,
    creatingFirstProject,
    handleRestoredSource,
    renameCurrentProject,
  } = useShelfProjectActions({
    api,
    navigate,
    project,
    resetForProject,
    selectedBookId,
    setNotice,
    setProjectMenuOpen,
    setSelectedBookId,
  });
  // A stopped import created no book to prepare: the import queue explains it, and the card
  // returns to the selected book.
  const displayedIndexingJob = indexingJob?.kind === 'import'
    && (indexingJob.status === 'failed' || indexingJob.status === 'canceled')
    ? null
    : indexingJob;
  const displayedActiveJob = displayedIndexingJob?.id === activeJob?.id ? activeJob : null;
  const indexingJobSourceView = displayedIndexingJob?.subject.type === 'canon_text_index'
    || displayedIndexingJob?.subject.type === 'chapter_cards'
    ? displayedIndexingJob.subject.sourceView
    : undefined;
  const statusIndexingBookId = unfinishedIndexingBookId(
    indexingStatusQuery.data,
    indexingJobSourceView ?? sourceView,
  );
  const indexingBookId = displayedIndexingJob?.kind === 'canon_text_index' || displayedIndexingJob?.kind === 'chapter_cards'
    ? statusIndexingBookId ?? selectedBook?.id ?? null
    : displayedIndexingJob
      ? activeJobBookId(displayedIndexingJob)
      : selectedBook?.id ?? null;
  const visibleJobForBook = (bookId: string) =>
    activeBookJobFor(bookId) ?? (indexingJob &&
      (activeJobBookId(indexingJob) === bookId || statusIndexingBookId === bookId) ? indexingJob : null);
  const indexingQueueFileName = importQueue.rows.find((row) => (
    row.bookId === indexingBookId || row.jobId === displayedIndexingJob?.id
  ))?.fileName;
  const indexingBookTitle =
    (indexingBookId ? books.find((book) => book.id === indexingBookId)?.title : undefined) ??
    indexingQueueFileName ??
    selectedBook?.title ??
    project?.title ??
    'Текущая сага';
  function navigateManuscript(
    overrides: Record<string, string | undefined>,
    options?: { replace?: boolean },
  ) {
    const query = {
      projectId: requestedProjectId ?? project?.id ?? undefined,
      bookId: requestedBookId ?? (selectedBookId || undefined),
      indexing: route.query.get('indexing') ?? undefined,
      menu: mobileMenuOpen ? 'open' : undefined,
      bookMenu: menuBookId ? 'open' : undefined,
      ...overrides,
    };
    if (options) {
      navigate('/manuscript/books', query, options);
    } else {
      navigate('/manuscript/books', query);
    }
  }

  function chooseImportFile() {
    if (importBlocked) return;
    if (!project) {
      setNotice('Сначала создайте сагу.');
      return;
    }
    if (!canEditManuscript) {
      setNotice('Менять книги этой саги может её владелец, администратор или редактор.');
      return;
    }
    fileInputRef.current?.click();
  }

  function setNavigationMenuOpen(open: boolean) {
    setMobileMenuOpen(open);
    // Opening navigation must not bind a default project and remount its trigger.
    navigateManuscript({
      projectId: requestedProjectId ?? undefined,
      bookId: requestedBookId ?? undefined,
      menu: open ? 'open' : undefined,
    });
  }

  function openImportFiles(files: readonly File[]) {
    if (importBlocked || !project || !canEditManuscript || files.length === 0) return;
    setIndexingCardDismissed(false);
    importQueue.openFiles(files);
  }

  function handleImportInputChange(event: ChangeEvent<HTMLInputElement>) {
    const files = [...(event.target.files ?? [])];
    event.target.value = '';
    openImportFiles(files);
  }

  function handleDropZoneDrop(event: DragEvent<HTMLElement>) {
    event.preventDefault();
    setDragActive(false);
    openImportFiles([...(event.dataTransfer.files ?? [])]);
  }

  async function downloadBook(book: Book, format: BookExportFormat) {
    setNotice(`Готовим файл книги «${book.title}»…`);
    try {
      const file = await bookExport.mutateAsync({ bookId: book.id, format });
      saveFile(file);
      setNotice(`Скачано: ${file.fileName}`);
    } catch (error) {
      setNotice(publicApiErrorMessage(error, 'Не удалось скачать книгу. Попробуйте снова.', {
        codes: { 'book_export.empty': 'В этой книге пока нет текста: скачивать нечего.' },
        forbidden: 'Скачать книгу может её владелец, администратор или редактор.',
      }));
    }
  }

  async function cancelActiveJob() {
    if (!activeJob || cancelJobMutation.isPending || importQueue.operationPending || !canEditManuscript) return;
    const copy = activeJob.kind === 'import'
      ? { pending: 'Отменяем импорт...', success: 'Импорт отменен, контекст полки сохранен.', failure: 'Не удалось отменить импорт.' }
      : { pending: 'Отменяем подготовку…', success: 'Подготовка отменена.', failure: 'Не удалось отменить подготовку.' };
    setNotice(copy.pending);
    try {
      if (activeJob.kind === 'import') {
        if (!await importQueue.cancel()) {
          setNotice(copy.failure);
          return;
        }
      } else {
        await cancelJobMutation.mutateAsync(activeJob.id);
      }
      setNotice(copy.success);
    } catch (error) {
      setNotice(publicApiErrorMessage(error, copy.failure));
    }
  }

  const importZone = !explicitProjectMissing && !explicitBookMissing ? <ShelfImportDropzone
    active={dragActive}
    disabled={!project || !canEditManuscript || importBlocked}
    onChooseFile={chooseImportFile}
    onDrop={handleDropZoneDrop}
    onSetActive={setDragActive}
  /> : null;
  const createProject = (title: string) => createFirstProject(title).then((created) => {
    if (created) setProjectCreateOpen(false);
    return created;
  });
  useVoiceFirstSaga(workspace && projects.length === 0 ? () => createFirstProject(firstSagaTitle) : null);
  const projectTitle = explicitProjectMissing ? 'Сага не найдена' : project?.title ?? 'Сага';
  const projectMeta = explicitProjectMissing ? 'Сага недоступна' : formatProjectMeta(project);
  const menuBook = books.find((book) => book.id === menuBookId) ?? selectedBook;

  const searchPresence = usePresence(searchOpen);
  const bookMenuPresence = usePresence(menuBookId && menuBook ? menuBook : null);
  const mobileMenuPresence = usePresence(mobileMenuOpen);
  const deletePresence = usePresence(deleteConfirmOpen && selectedBook ? selectedBook : null);
  const projectDeletePresence = usePresence(projectDeleteOpen && project ? project : null);
  const lastMenuPosition = useRef(menuPosition);
  if (menuPosition) {
    lastMenuPosition.current = menuPosition;
  }

  return (
    <main className={styles.workspace}>
      <ShelfTopBar
        accountMenuOpen={accountMenuOpen}
        onDismissAccountMenu={() => setAccountMenuOpen(false)}
        onLogout={onLogout}
        onOpenMobileMenu={() => setNavigationMenuOpen(true)}
        onOpenSearch={() => setSearchOpen(true)}
        onToggleAccountMenu={() => setAccountMenuOpen((value) => !value)}
        searchTriggerRef={searchTriggerRef}
        talk={<TopBarTalk />}
        userName={userName}
      />

      {searchPresence.mounted ? (
        <SearchPanel
          api={api}
          onOpenChapter={(locator) => {
            setSearchOpen(false);
            const destination = chapterSearchDestination(locator);
            navigate(destination.path, destination.query);
          }}
          coverage={searchCoverage}
          bookName={selectedBook?.title}
          bookScopeDisabled={!selectedBook}
          scope={searchScope}
          searchRequestId={searchRequestId}
          state={searchPresence.status}
          onClose={() => {
            setSearchOpen(false);
            searchTriggerRef.current?.focus();
          }}
          onOpenResult={openSearchResult}
          onRunSearch={(event) => {
            event?.preventDefault();
            void runSearch();
          }}
          results={searchResults}
          returnFocusRef={searchTriggerRef}
          searchError={searchError}
          searchStatus={searchStatus}
          searchTerm={searchTerm}
          sourceView={sourceView}
          setScope={selectSearchScope}
          setSearchTerm={setSearchTerm}
          title="Поиск по книгам"
          variant="shelf"
        />
      ) : null}

      {bookNavigation.picker}
      <div className={styles.body}>
        <ShelfWorkspaceNavigation
          onOpenReader={bookNavigation.onOpenReader}
          onOpenNotes={() => navigate('/notes')}
          onOpenSaga={() => navigate('/saga', { projectId: chatProjectId })}
          onOpenWriting={() => navigate('/writing', { projectId: chatProjectId })}
          onOpenDrawing={() => navigate('/drawing', { projectId: chatProjectId })}
          onOpenGallery={() => navigate('/gallery', { projectId: chatProjectId })}
          onCreateNote={notes ? () => notes.capture({ body: '' }) : undefined}
          onOpenChat={() => navigate('/chat', { projectId: chatProjectId })}
          onOpenManuscript={() => navigate('/manuscript/books', {
            projectId: project?.id ?? undefined,
            bookId: selectedBook?.id,
          })}
          onOpenProjectMenu={() => setProjectMenuOpen((value) => !value)}
          projectMeta={projectMeta}
          projectTitle={projectTitle}
        />

        <section className={styles.content} aria-label="Книги саги">
          {explicitProjectMissing ? null : <ShelfHeader
            canEditManuscript={!explicitBookMissing && canEditManuscript}
            fileInputRef={fileInputRef}
            importBlocked={importBlocked}
            onChooseImportFile={chooseImportFile}
            onImportInputChange={handleImportInputChange}
            onOpenImportQueue={() => importQueue.show?.()}
            projectExists={!explicitBookMissing && Boolean(project)}
            projectMeta={projectMeta}
            projectTitle={project || status !== 'empty' ? projectTitle : 'Добро пожаловать'}
            showActions={!explicitBookMissing}
            showOpenImportQueue={!explicitBookMissing && importQueue.canReopen === true}
          />}
          {explicitProjectMissing || explicitBookMissing || !canEditManuscript ? null
            : <ShelfQuotaHints api={api} projectId={project?.id ?? null} />}

          {explicitProjectMissing ? (
            <ProjectMissingState
              onOpenChat={() => navigate('/chat')}
              onOpenLibrary={() => navigate('/manuscript/books', {})}
            />
          ) : explicitBookMissing ? (
            <p className={styles.notice}>Книга недоступна или не найдена</p>
          ) : <ShelfStatusMessages
            emptySaga={Boolean(project) && firstStep === null}
            error={shelfQuery.isError && !workspace ? shelfQuery.error : null}
            importQueueActive={Boolean(activeImportQueue && !importQueue.isDialogVisible)}
            notice={notice}
            onRetry={() => void shelfQuery.refetch()}
            retrying={shelfQuery.isRefetching}
            status={status}
          />}
          {importQueue.isOpen ? null : <RejectedImportFiles files={importQueue.rejectedFiles} onDismiss={importQueue.dismiss} />}
          {projectCreateOpen && firstStep !== 1 ? (
            <FirstProjectPanel creating={creatingFirstProject} heading={<h2>Новая сага</h2>} onCreate={createProject} />
          ) : null}
          {firstStep ? <FirstSteps
            bookCount={books.length}
            createSaga={<FirstProjectPanel creating={creatingFirstProject} onCreate={createProject} />}
            onAskFirstQuestion={() => navigate('/chat', { projectId: project?.id })}
            projectTitle={project?.title ?? null}
            step={firstStep}
            textReady={indexingStatusQuery.data?.[sourceView]?.exists === true}
            uploadBooks={emailUnverified ? <EmailVerificationNotice surface="shelf" /> : importZone}
          /> : null}
          {project && !explicitBookMissing && !indexingCardDismissed && (
            displayedIndexingJob || (selectedBook && selectedBook.wordCount > 0)
          ) ? (
            <BookIndexingCard
              api={api}
              bookId={indexingBookId}
              bookPendingCreation={Boolean(displayedIndexingJob?.kind === 'import' && indexingBookId === null)}
              bookTitle={indexingBookTitle}
              books={books}
              canCancel={Boolean(displayedActiveJob?.canCancel && canEditManuscript && !importQueue.operationPending)}
              canceling={cancelJobMutation.isPending || importQueue.operationPending}
              canEditManuscript={canEditManuscript}
              jobSourceView={indexingJobSourceView}
              onAskQuestion={() => navigate('/chat', { projectId: project.id })}
              onCancel={displayedActiveJob?.canCancel && canEditManuscript && !importQueue.operationPending ? () => void cancelActiveJob() : undefined}
              onDismiss={() => setIndexingCardDismissed(true)}
              projectId={project.id}
              sourceView={sourceView}
            />
          ) : null}

          {project && !explicitBookMissing ? <ShelfBooksHeader /> : null}
          {!explicitProjectMissing && !explicitBookMissing ? <ShelfBooks
            indexingJobFor={visibleJobForBook}
            books={books}
            onOpenMenu={openBookMenu}
            onOpenReader={(book) => void openBook(book)}
            onSelectBook={(bookId) => {
              setSelectedBookId(bookId);
              navigateManuscript({ bookId });
            }}
            selectedBookId={selectedBookId}
          /> : null}

          {/* The first steps hold the drop zone until the saga has a book (R2.1.2). */}
          {firstStep === 1 || firstStep === 2 ? null : <>
            {!explicitProjectMissing && !explicitBookMissing && canEditManuscript ? <EmailVerificationNotice surface="shelf" /> : null}
            {importZone}
          </>}
        </section>
      </div>

      <ShelfProjectMenu
        canManageProject={canDeleteProject}
        canOpenDeletedSources={!explicitBookMissing && canEditManuscript}
        currentProject={project}
        deletedSourcesTriggerRef={deletedSourcesTriggerRef}
        onCreateProject={() => {
          setProjectMenuOpen(false);
          setProjectCreateOpen(true);
        }}
        onDismiss={() => setProjectMenuOpen(false)}
        onOpenDeletedSources={() => setDeletedSourcesOpen(true)}
        onRenameProject={renameCurrentProject}
        onRequestDeleteProject={() => {
          setProjectMenuOpen(false);
          setProjectDeleteOpen(true);
        }}
        onSelectProject={(projectId) => {
          setProjectMenuOpen(false);
          navigate('/manuscript/books', { projectId });
        }}
        open={projectMenuOpen}
        projects={projects}
      />

      {importQueue.isOpen ? (
        <ImportQueueDialog
          books={books}
          onOpenItem={(row) => {
            if (!row.bookId) return;
            navigateManuscript({
              projectId: project?.id,
              bookId: row.bookId,
            });
          }}
          queue={importQueue}
        />
      ) : null}

      {bookMenuPresence.mounted && bookMenuPresence.value ? (
        <BookActionMenu
          book={bookMenuPresence.value}
          canEditManuscript={canEditManuscript}
          indexingJob={visibleJobForBook(bookMenuPresence.value.id)}
          onChangeCover={() => void changeCover(bookMenuPresence.value!)}
          onClose={closeBookMenu}
          onDelete={() => requestDeleteBook(bookMenuPresence.value!.id)}
          onDownload={(format) => {
            const book = bookMenuPresence.value!;
            closeBookMenu();
            void downloadBook(book, format);
          }}
          onOpenRead={() => void openBook(bookMenuPresence.value!)}
          onUpdateFromFile={() => {
            const book = bookMenuPresence.value!;
            closeBookMenu();
            bookUpdate.choose(book);
          }}
          position={menuPosition ?? lastMenuPosition.current}
          state={bookMenuPresence.status}
          updateDisabled={importBlocked || bookUpdate.busy}
        />
      ) : null}
      {bookUpdate.element}

      {canEditManuscript && coverEditorBook ? (
        <BookCoverEditor
          api={api}
          book={coverEditorBook}
          onClose={closeCoverEditor}
          onNotice={setNotice}
          onUpdated={(updated) => setSelectedBookId(updated.id)}
        />
      ) : null}

      <ShelfMobileNavigation
        onOpenReader={bookNavigation.onOpenReader}
        onOpenNotes={() => navigate('/notes')}
        onOpenSaga={() => navigate('/saga', { projectId: chatProjectId })}
        onOpenWriting={() => navigate('/writing', { projectId: chatProjectId })}
        onOpenDrawing={() => navigate('/drawing', { projectId: chatProjectId })}
        onOpenGallery={() => navigate('/gallery', { projectId: chatProjectId })}
        onCreateNote={notes ? () => notes.capture({ body: '' }) : undefined}
        mounted={mobileMenuPresence.mounted}
        onClose={() => setNavigationMenuOpen(false)}
        onOpenChat={() => navigate('/chat', { projectId: chatProjectId })}
        onOpenManuscript={() => navigate('/manuscript/books', {
          projectId: project?.id ?? undefined,
          bookId: selectedBook?.id,
        })}
        onOpenProjectMenu={() => {
          setNavigationMenuOpen(false);
          setProjectMenuOpen(true);
        }}
        projectMeta={projectMeta}
        projectTitle={projectTitle}
        state={mobileMenuPresence.status}
      />

      {deletePresence.mounted && deletePresence.value ? (
        <ConfirmDelete
          book={deletePresence.value}
          isDeleting={deletingBook}
          onClose={dismissDeleteConfirmation}
          onDelete={() => void deleteBook(deletePresence.value!)}
          state={deletePresence.status}
        />
      ) : null}

      {projectDeletePresence.mounted && projectDeletePresence.value ? (
        <ConfirmProjectDelete
          isDeleting={deletingProject}
          onClose={() => setProjectDeleteOpen(false)}
          onDelete={() => {
            void deleteCurrentProject().then((deleted) => {
              if (deleted) setProjectDeleteOpen(false);
            });
          }}
          project={projectDeletePresence.value}
          state={projectDeletePresence.status}
        />
      ) : null}

      {deletedSourcesOpen ? (
        <DeletedSourcesDialog
          api={api}
          bookId={selectedBook?.id ?? null}
          onClose={() => {
            setDeletedSourcesOpen(false);
            deletedSourcesTriggerRef.current?.focus();
          }}
          onRestored={(source) => void handleRestoredSource(source)}
          projectId={project?.id ?? null}
        />
      ) : null}

    </main>
  );
}
