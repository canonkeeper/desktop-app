import { useEffect, useRef, useState } from 'react';
import { isApiStatusFailure, publicApiErrorMessage } from '../../../entities/api-errors';
import * as manuscriptFeatureApi from '../api';
import { canEditProject, textSourceView, type ManuscriptApiClient, type SearchResult } from '../../../entities/manuscript/api';
import { useReaderPreferencesStore } from '../../../entities/manuscript/display-preferences-store';
import type { AppRoute, RouteNavigate } from '../../../shared/navigation/app-route';
import type { ProjectSidebarProps } from '../../../ui/ProjectSidebar';
import { formatProjectMeta } from '../../../ui/copy';
import { SupportReference } from '../../../ui/feedback/SupportReference';
import { ToastRegion } from '../../../ui/feedback/Toast';
import { useToastQueue } from '../../../ui/feedback/useToastQueue';
import { usePresence } from '../../../ui/overlays/use-presence';
import { useNotes } from '../../../shared/notes/notes-context';
import { useBookNavigation } from '../../../entities/manuscript/useBookNavigation';
import { SearchPanel } from '../SearchPanel';
import { chapterSearchDestination, manuscriptSearchResultDestination } from '../search-result-navigation';
import { useReaderSlot, type ReaderDrawings, type ReaderDrawingsOptions } from './reader-slot';
import { ReaderCore } from './ReaderCore';
import { ChapterNavigation, EditChapterButton, ReaderChapterHeader } from './ReaderChapter';
import {
  ReaderMobileMenu,
  ReaderMobilePanel,
  type ReaderMobilePanelKind,
} from './ReaderLayers';
import { ReaderAppBar, ReaderMobileControls, ReaderProjectMenu, ReaderProjectNavigation } from './ReaderNavigation';
import { useVoicePlace } from '../../../shared/navigation/voice-place';
import { TopBarTalk } from '../../../ui/navigation/TopBarStrip';
import { ReaderRail } from './ReaderRail';
import { ReaderEmptyState, ReaderForbiddenState, ReaderLoadingState, ReaderLocatorAlert, ReaderStatusBanner } from './ReaderStates';
import { buildReaderLocator, readerSourceLanding } from './reader-locator';
import { useReaderSearch } from './useReaderSearch';
import styles from './ManuscriptReaderMode.module.css';

type LoadStatus = 'loading' | 'ready' | 'empty' | 'error' | 'forbidden';

interface ReaderPageProps {
  api: ManuscriptApiClient;
  navigate: RouteNavigate;
  /** Signing out, where the edition signs in: the account menu stands only then. */
  onLogout?: () => void;
  route: AppRoute;
  userName: string;
}

const noDrawings: ReaderDrawings = {
  chapterKey: '', toggle: null, mobileToggle: null, drawingCount: 0, drawLocked: false, notice: null, overlays: null,
};

/** The reader without the drawings of the site. */
function useNoDrawings(_options: ReaderDrawingsOptions) {
  return noDrawings;
}

export function ManuscriptReaderMode({ api, navigate, onLogout, route, userName }: ReaderPageProps) {
  const notes = useNotes();
  const requestedChapterId = route.query.get('chapterId') ?? '';
  const requestedParagraphId = route.query.get('paragraphId') ?? '';
  const requestedProjectId = route.query.get('projectId');
  const requestedBookId = route.query.get('bookId');
  const focusMode = route.query.get('focus') === '1';
  const [notice, setNotice] = useState<{ message: string; code?: string } | null>(null);
  const editChapter = manuscriptFeatureApi.useOpenChapterEditMutation(api);
  const toasts = useToastQueue();
  const { displaySize, displayTheme, hydrateReaderPreferences, setDisplaySize, setDisplayTheme } = useReaderPreferencesStore();
  const mobilePanel = readerPanelFromRoute(route.query);
  const mobileMenuOpen = route.query.get('menu') === 'open';
  const [projectMenuOpen, setProjectMenuOpen] = useState(false);
  const [accountMenuOpen, setAccountMenuOpen] = useState(false);
  const mobileChaptersTriggerRef = useRef<HTMLButtonElement | null>(null);
  const mobileNotesTriggerRef = useRef<HTMLButtonElement | null>(null);
  const mobileViewTriggerRef = useRef<HTMLButtonElement | null>(null);
  const panelReturnFocusRef = useRef<HTMLButtonElement | null>(null);
  const searchTriggerRef = useRef<HTMLButtonElement | null>(null);
  const workspaceQuery = manuscriptFeatureApi.useManuscriptReaderWorkspaceQuery(api, {
    bookId: requestedBookId,
    chapterId: requestedChapterId,
    projectId: requestedProjectId,
  }, { enabled: !route.invalidQuery });
  const workspace = workspaceQuery.data;
  const projects = workspace?.projects ?? [];
  const project = workspace?.project ?? null;
  const book = workspace?.book ?? null;
  const sourceView = textSourceView;
  const chapters = workspace?.chapters.filter((item) => item.status === 'published' || item.status === 'stale') ?? [];
  const chapter = workspace?.chapter ?? null;
  const status: LoadStatus = workspaceQuery.isPending
    ? 'loading'
    : workspaceQuery.isError && !workspace
      ? isForbidden(workspaceQuery.error) ? 'forbidden' : 'error'
      : project && book && chapter && !workspace?.loadError
        ? 'ready'
        : workspace?.loadError
          ? isForbidden(workspace.loadError) ? 'forbidden' : 'error'
          : 'empty';
  const error = workspaceQuery.isError && !workspace
    ? formatError(workspaceQuery.error, 'Не удалось открыть главу.')
    : workspace?.loadError
      ? formatError(workspace.loadError, 'Не удалось открыть главу.')
      : '';
  const errorCode = supportCode(workspaceQuery.isError && !workspace ? workspaceQuery.error : workspace?.loadError);
  const selectProjectMutation = manuscriptFeatureApi.useGetProjectMutation(api);
  const search = useReaderSearch(api, project, book, sourceView);
  const illustrated = route.query.get('illustrated') ?? undefined;
  const useDrawings = useReaderSlot()?.useDrawings ?? useNoDrawings;
  const readerDrawings = useDrawings({
    projectId: project?.id ?? '', bookId: book?.id ?? '', chapter, sourceView, canEdit: canEditProject(project), route, navigate, toasts,
  });
  // The voice call knows the chapter the author reads (specs/2026-11-03-voice-assistant.md V2.4).
  useVoicePlace(project?.id, { page: 'reader', ...(readerDrawings.chapterKey ? { chapterKey: readerDrawings.chapterKey } : {}) });

  useEffect(() => {
    hydrateReaderPreferences();
  }, [hydrateReaderPreferences]);

  const sourceLanding = readerSourceLanding(route, chapter, sourceView);
  const sourceProblem = sourceLanding.problem;
  const selectedParagraph = sourceLanding.paragraph;
  const effectiveParagraphId = selectedParagraph?.id ?? '';
  const readerLocator = chapter ? buildReaderLocator(
    project?.id ?? '',
    book?.id ?? '',
    chapter?.id ?? requestedChapterId,
    effectiveParagraphId,
    selectedParagraph?.text ?? '',
    chapter.revision,
    sourceView,
  ) : null;
  const projectTitle = project?.title ?? 'Сага';
  const projectMeta = formatProjectMeta(project);
  const bookLabel = book?.title ?? 'Книга';
  const locatorAlert = sourceProblem ?? (requestedChapterId && chapter && requestedChapterId !== chapter.id
    ? 'replaced'
    : null);

  function navigateReader(overrides: Record<string, string | undefined>) {
    if (route.invalidQuery) return;
    navigate('/manuscript/books', {
      projectId: project?.id ?? undefined,
      bookId: book?.id ?? undefined,
      mode: route.query.get('mode') ?? 'read',
      chapterId: sourceProblem ? requestedChapterId : chapter?.id ?? requestedChapterId,
      paragraphId: requestedParagraphId || effectiveParagraphId || undefined,
      expectedRevision: route.query.get('expectedRevision') ?? undefined,
      chapters: mobilePanel === 'chapters' ? 'open' : undefined,
      focus: focusMode ? '1' : undefined,
      view: mobilePanel === 'appearance' ? 'open' : undefined,
      illustrated,
      ...overrides,
    });
  }

  async function selectProject(projectId: string) {
    try {
      const nextProject = await selectProjectMutation.mutateAsync(projectId);
      setProjectMenuOpen(false);
      navigate('/manuscript/books', { projectId: nextProject.id });
    } catch (nextError) {
      setProjectMenuOpen(false);
      setNotice({ message: formatError(nextError, 'Не удалось сменить сагу.'), code: supportCode(nextError) });
    }
  }

  function openChapter(nextChapterId: string) {
    navigate('/manuscript/books', {
      projectId: project?.id ?? undefined,
      bookId: book?.id ?? undefined,
      mode: 'read',
      chapterId: nextChapterId,
      paragraphId: undefined,
      expectedRevision: undefined,
      focus: focusMode ? '1' : undefined,
      illustrated,
    });
  }

  function openMobilePanel(panel: ReaderMobilePanelKind) {
    panelReturnFocusRef.current = document.activeElement instanceof HTMLButtonElement ? document.activeElement : null;
    navigateReader({
      chapters: panel === 'chapters' ? 'open' : undefined,
      view: panel === 'appearance' ? 'open' : undefined,
    });
  }

  function closeMobilePanel() {
    navigateReader({
      chapters: undefined,
      view: undefined,
    });
  }

  function openSearchResult(result: SearchResult) {
    search.setOpen(false);
    const destination = manuscriptSearchResultDestination(result);
    navigate(destination.path, { ...destination.query, focus: focusMode ? '1' : undefined });
  }

  function renderReaderSurface() {
    if (route.invalidQuery) return <ReaderLocatorAlert kind="invalid" />;
    if (status === 'loading') {
      return <ReaderLoadingState />;
    }
    if (status === 'empty') {
      if (sourceProblem) return <ReaderLocatorAlert kind={sourceProblem} />;
      return <ReaderEmptyState onBack={() => navigate('/manuscript/books', { projectId: project?.id ?? undefined })} />;
    }
    if (status === 'forbidden') {
      return <ReaderForbiddenState message={error} onBack={() => navigate('/manuscript/books', { projectId: project?.id ?? undefined })} />;
    }
    if (status === 'error') {
      const loadError = workspace?.loadError ?? workspaceQuery.error;
      if (sourceProblem && isApiStatusFailure(loadError) && loadError.status === 404) return <ReaderLocatorAlert kind="unavailable" />;
      return <ReaderStatusBanner message={error} onRetry={() => void workspaceQuery.refetch()} supportCode={errorCode} tone="error" />;
    }
    if (!chapter) {
      return <ReaderEmptyState onBack={() => navigate('/manuscript/books', { projectId: project?.id ?? undefined })} />;
    }
    if (chapter.contentVariant !== sourceView) return <ReaderLocatorAlert kind="version" />;
    const chapterEdit = project && canEditProject(project) ? {
      pending: editChapter.isPending,
      run: () => editChapter.mutate({ projectId: project.id, chapterId: chapter.id }, {
        onSuccess: (draft) => navigate('/writing', { projectId: project.id, draft: draft.id }),
        onError: (error) => setNotice({ message: publicApiErrorMessage(error, 'Не удалось открыть главу для правки. Попробуйте ещё раз.', {
          forbidden: 'Править главы может владелец саги, администратор или редактор.',
        }), code: supportCode(error) }),
      }),
    } : null;
    return (
      <>
        <section className={styles.readingPane}>
          <ReaderChapterHeader
            bookLabel={bookLabel}
            chapter={chapter}
            drawingCount={readerDrawings.drawingCount}
            illustrations={readerDrawings.toggle}
            displaySize={displaySize}
            displayTheme={displayTheme}
            focusMode={focusMode}
            onOpenBook={() => navigate('/manuscript/books', { projectId: project?.id ?? undefined, bookId: book?.id ?? undefined })}
            onOpenChapters={() => openMobilePanel('chapters')}
            onOpenAppearance={() => openMobilePanel('appearance')}
            onSetDisplaySize={setDisplaySize}
            onSetDisplayTheme={setDisplayTheme}
            onToggleFocus={() => navigateReader({ focus: focusMode ? undefined : '1' })}
          />

          {notice ? <p className={styles.notice}>{notice.message} {notice.code ? <SupportReference code={notice.code} /> : null}</p> : null}
          {locatorAlert ? <ReaderLocatorAlert kind={locatorAlert} onOpenCurrent={sourceProblem ? () => openChapter(chapter.id) : undefined} /> : null}
          {readerDrawings.notice}

          {!sourceProblem && readerLocator ? (
          <section className={styles.readerColumn} aria-label="Текст главы" data-reading-theme={displayTheme}>
            <div className={styles.editRow}>
              {chapterEdit ? <EditChapterButton chapter={chapter} edit={chapterEdit} /> : null}
              {readerDrawings.mobileToggle}
            </div>
            <ReaderCore
              activeParagraphId={effectiveParagraphId}
              chapter={chapter}
              drawLocked={readerDrawings.drawLocked}
              drawings={readerDrawings.drawings}
              onDrawSelection={readerDrawings.onDrawSelection}
              locator={readerLocator}
              onCopySelection={(text) => {
                void navigator.clipboard?.writeText(text).then(
                  () => toasts.push({ message: 'Фрагмент скопирован', tone: 'neutral' }),
                  () => undefined,
                );
              }}
              onFindInBook={search.searchFor}
              onCaptureSelection={notes ? (text, locator) => notes.capture({ body: text,
                source: { kind: 'chapter', quote: locator.range?.quote ?? '', locator } }, navigate) : undefined}
            />
          </section>
          ) : null}

          <ChapterNavigation chapter={chapter} onOpenChapter={openChapter} />
        </section>

        <ReaderRail
          chapters={chapters}
          currentChapterId={chapter?.id ?? requestedChapterId}
          displaySize={displaySize}
          displayTheme={displayTheme}
          onOpenChapter={openChapter}
          onSetDisplaySize={setDisplaySize}
          onSetDisplayTheme={setDisplayTheme}
          focusMode={focusMode}
          onToggleFocus={() => navigateReader({ focus: focusMode ? undefined : '1' })}
        />
      </>
    );
  }

  const bookNavigation = useBookNavigation({
    api, navigate, projectId: project?.id,
    bookId: book && (book.id === requestedBookId || chapter?.id === requestedChapterId) ? book.id : undefined,
    chapterId: chapter?.id,
  });
  const sidebarProps: ProjectSidebarProps = {
    activeSection: 'reader',
    onOpenReader: bookNavigation.onOpenReader,
    onOpenNotes: () => navigate('/notes'),
    onOpenSaga: () => navigate('/saga', { projectId: project?.id ?? requestedProjectId ?? undefined }),
    onOpenWriting: () => navigate('/writing', { projectId: project?.id ?? requestedProjectId ?? undefined }),
    onOpenDrawing: () => navigate('/drawing', { projectId: project?.id ?? requestedProjectId ?? undefined }),
    onOpenGallery: () => navigate('/gallery', { projectId: project?.id ?? requestedProjectId ?? undefined }),
    onCreateNote: notes ? () => notes.capture({ body: '' }, navigate) : undefined,
    collapsed: focusMode,
    onOpenChat: () => navigate('/chat', { projectId: project?.id ?? requestedProjectId ?? undefined }),
    onOpenManuscript: () => navigate('/manuscript/books', {
      projectId: project?.id ?? undefined,
      bookId: book?.id,
    }),
    onOpenProjectMenu: () => setProjectMenuOpen((value) => !value),
    projectMeta,
    projectTitle,
  };

  const searchPresence = usePresence(search.open);
  const accountPresence = usePresence(accountMenuOpen);
  const projectPresence = usePresence(projectMenuOpen);
  const mobileMenuPresence = usePresence(mobileMenuOpen);
  const mobilePanelPresence = usePresence(mobilePanel && status !== 'loading' ? mobilePanel : null);
  const searchPanel = searchPresence.mounted ? (
    <SearchPanel
      api={api}
      onOpenChapter={(locator) => {
        search.setOpen(false);
        const destination = chapterSearchDestination(locator);
        navigate(destination.path, destination.query);
      }}
      bookName={book?.title}
      bookScopeDisabled={!book}
      coverage={search.coverage}
      state={searchPresence.status}
      onClose={() => search.setOpen(false)}
      returnFocusRef={searchTriggerRef}
      onOpenResult={openSearchResult}
      onRunSearch={search.run}
      results={search.results}
      scope={search.scope}
      searchError={search.error}
      searchRequestId={search.requestId}
      searchStatus={search.status}
      searchTerm={search.term}
      setScope={search.selectScope}
      setSearchTerm={search.setTerm}
      sourceView={sourceView}
      title="Поиск по книге"
      variant="reader"
    />
  ) : null;

  return (
    <main className={styles.workspace} data-display-size={displaySize} data-display-theme={displayTheme} data-focus={focusMode || undefined}>
      {bookNavigation.picker}
      <ReaderAppBar
        projectTitle={projectTitle}
        talk={<TopBarTalk />}
        onOpenNotes={notes ? () => notes.openWithNavigation(navigate) : undefined}
        accountMenuState={accountPresence.mounted ? accountPresence.status : null}
        mobileTitle={chapter?.title ?? 'Чтение'}
        onLogout={onLogout}
        onOpenMenu={() => {
          navigateReader({ menu: 'open' });
        }}
        onOpenSearch={() => search.setOpen(true)}
        onCloseAccount={() => setAccountMenuOpen(false)}
        onToggleAccount={() => setAccountMenuOpen((value) => !value)}
        searchButtonRef={searchTriggerRef}
        searchPanel={searchPanel}
        userName={userName}
      />

      <div className={styles.body}>
        <ReaderProjectNavigation
          sidebar={sidebarProps}
        />

        <section className={styles.content} aria-label="Чтение книги">
          {renderReaderSurface()}
        </section>
      </div>

      <ReaderMobileControls
        chaptersButtonRef={mobileChaptersTriggerRef}
        notesButtonRef={mobileNotesTriggerRef}
        onOpenChapters={() => openMobilePanel('chapters')}
        onOpenNotes={() => notes?.openWithNavigation(navigate)}
        onOpenView={() => openMobilePanel('appearance')}
        viewButtonRef={mobileViewTriggerRef}
      />

      {projectPresence.mounted ? (
        <ReaderProjectMenu onSelect={(projectId) => void selectProject(projectId)} projects={projects} state={projectPresence.status} />
      ) : null}

      {mobileMenuPresence.mounted ? (
        <ReaderMobileMenu
          onClose={() => {
            navigateReader({ menu: undefined });
          }}
          sidebar={{
            ...sidebarProps,
            onOpenProjectMenu: () => {
              navigateReader({ menu: undefined });
              setProjectMenuOpen(true);
            },
          }}
          state={mobileMenuPresence.status}
        />
      ) : null}

      {!search.open && !searchPresence.mounted && mobilePanelPresence.mounted && mobilePanelPresence.value ? (
        <ReaderMobilePanel
          state={mobilePanelPresence.status}
          chapters={chapters}
          currentChapterId={chapter?.id ?? requestedChapterId}
          displaySize={displaySize}
          displayTheme={displayTheme}
          onClose={closeMobilePanel}
          onOpenChapter={openChapter}
          onSetDisplaySize={setDisplaySize}
          onSetDisplayTheme={setDisplayTheme}
          panel={mobilePanelPresence.value}
          returnFocusRef={panelReturnFocusRef}
          focusMode={focusMode}
          onToggleFocus={() => navigateReader({ view: undefined, focus: focusMode ? undefined : '1' })}
        />
      ) : null}

      {readerDrawings.overlays}
      <ToastRegion className={styles.readerToasts} onDismiss={toasts.dismiss} queue={toasts.queue} />
    </main>
  );
}

function readerPanelFromRoute(query: URLSearchParams): ReaderMobilePanelKind | null {
  if (query.get('chapters') === 'open') return 'chapters';
  if (query.get('view') === 'open') return 'appearance';
  return null;
}

function isForbidden(error: unknown) {
  return isApiStatusFailure(error) ? error.status === 403 : typeof error === 'object' && error !== null && 'status' in error && (error as { status?: number }).status === 403;
}

function formatError(error: unknown, fallback: string) {
  return publicApiErrorMessage(error, fallback, {
    forbidden: 'Нет доступа к этой главе.',
    unauthorized: 'Время входа истекло. Войдите снова — вернём вас к главе.',
  });
}

/** The support code of a server failure; a 4xx answer explains itself (R4.2.5). */
function supportCode(error: unknown) {
  return isApiStatusFailure(error) && error.status >= 500 ? error.problem.requestId : undefined;
}
