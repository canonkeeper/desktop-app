import { Download, Image as ImageIcon, PenLine } from 'lucide-react';
import { useEffect, useMemo, useRef, useState, type CSSProperties } from 'react';
import { isApiStatusFailure, publicApiErrorMessage } from '../../../entities/api-errors';
import * as manuscriptFeatureApi from '../api';
import { canEditProject, textSourceView, type BookExportFormat, type ManuscriptApiClient } from '../../../entities/manuscript/api';
import {
  readerColumnWidths, readerTextSizes, textSizeStep, useReaderPreferencesStore, useTextBackground,
} from '../../../entities/manuscript/display-preferences-store';
import type { AppRoute, BackFrom, RouteNavigate } from '../../../shared/navigation/app-route';
import { formatReaderChapterTitle } from '../../../ui/copy';
import { useToasts } from '../../../ui/feedback/useToastQueue';
import type { MoreMenuItem } from '../../../ui/overlays/MoreMenu';
import { usePresence } from '../../../ui/overlays/use-presence';
import { ReaderAppearanceControls } from '../../../ui/ReaderAppearanceControls';
import { TextBackgroundContext } from '../../../ui/text-background';
import { useSagaSections } from '../../../ui/saga-sections';
import { DownloadDialog } from '../shelf/BookDownload';
import { saveFile } from '../../../ui/save-file';
import { useNarrowScreen } from '../../../ui/use-narrow-screen';
import { useNotes } from '../../../shared/notes/notes-context';
import { usePageTitlePart } from '../../../shared/navigation/page-title';
import { useVoicePlace } from '../../../shared/navigation/voice-place';
import { useShellActions } from '../../../ui/shell/shell-actions';
import { useReaderSlot, type ReaderDrawings, type ReaderDrawingsOptions } from './reader-slot';
import { ReaderBar } from './ReaderBar';
import { ChapterTurn } from './ReaderChapter';
import { ReaderContents, chapterWords } from './ReaderContents';
import { ReaderCore, type ReaderSelection } from './ReaderCore';
import { ReaderFindBar } from './ReaderFindBar';
import { ReaderLook } from './ReaderLook';
import { ReaderPhoneBar } from './ReaderNavigation';
import { ReaderEmptyState, ReaderErrorState, ReaderForbiddenState, ReaderLoadingState, StaleLinkLine, type ReaderStaleLink } from './ReaderStates';
import { buildReaderLocator, readerSourceLanding } from './reader-locator';
import { chapterQuery, readerPanel, readerQuery, type ReaderPanel } from './reader-route';
import { readingPlaceOf, rememberReadingPlace } from './reading-place';
import { useBookFind } from './use-book-find';
import { useChapterNotes, type ChapterNotesClient } from './use-chapter-notes';
import { useFindPlaces } from './use-find-places';
import { useReaderKeys } from './use-reader-keys';
import { useReaderScroll } from './use-reader-scroll';
import styles from './ManuscriptReaderMode.module.css';

type LoadStatus = 'loading' | 'ready' | 'empty' | 'error' | 'forbidden';

export type ReaderApi = ManuscriptApiClient & ChapterNotesClient;

interface ReaderPageProps {
  api: ReaderApi;
  navigate: RouteNavigate;
  route: AppRoute;
}

const noDrawings: ReaderDrawings = {
  chapterKey: '', inText: null, tab: null, drawingCount: 0, drawLocked: false, notice: null, overlays: null,
};

/** The reader without the drawings of the site. */
function useNoDrawings(_options: ReaderDrawingsOptions) {
  return noDrawings;
}

/**
 * The reader of a book (/manuscript/books?mode=read) — a screen for the text (specs/2026-11-10-ux7-reader.md): one line of
 * the reader under the top bar of the shell, the text from the first screen, the step between chapters at its end and by
 * the keys, «Аа», «Содержание» with its tabs, «Найти» in the book as a bar over the text and «Только текст». The top bar,
 * the sections and the saga are the shell's (specs/2026-11-10-ux2-shell-core.md SC1.2); the page gives its content.
 */
export function ManuscriptReaderMode({ api, navigate, route }: ReaderPageProps) {
  const notes = useNotes();
  const shell = useShellActions();
  const toasts = useToasts();
  const phone = useNarrowScreen(767);
  const prefs = useReaderPreferencesStore();
  // «Фон текста»: the chosen one, or the theme of the site while none is (specs/2026-11-12-ux11-dark-core.md TH1.3).
  const background = useTextBackground();
  const { hydrateReaderPreferences } = prefs;
  useEffect(() => hydrateReaderPreferences(), [hydrateReaderPreferences]);

  const requestedChapterId = route.query.get('chapterId') ?? '';
  const onlyText = route.query.get('focus') === '1';
  const panel = readerPanel(route.query);
  const lookOpen = route.query.get('view') === 'open';
  const findQuery = route.query.get('find');
  const [findShown, setFindShown] = useState(false);
  const findOpen = findShown || findQuery !== null;
  const [findDraft, setFindDraft] = useState(findQuery ?? '');
  const [staleClosed, setStaleClosed] = useState('');
  // A link to a chapter the book no longer has opens its first chapter, and the line over the text says so (RD3.4).
  const [goneLink, setGoneLink] = useState(false);
  // «Скачать книгу…»: the window of the kind of its file (specs/2026-11-11-ux9-shelf-chat.md SC1.5).
  const [downloading, setDownloading] = useState(false);
  const drawingsHere = useSagaSections().includes('drawing');
  const scrollerRef = useRef<HTMLDivElement>(null);
  const findFieldRef = useRef<HTMLInputElement>(null);
  const lookButtonRef = useRef<HTMLButtonElement>(null);
  const phoneLookRef = useRef<HTMLButtonElement>(null);
  const contentsButtonRef = useRef<HTMLButtonElement>(null);
  const barContentsRef = useRef<HTMLButtonElement>(null);

  const editChapter = manuscriptFeatureApi.useOpenChapterEditMutation(api);
  const bookExport = manuscriptFeatureApi.useBookExportMutation(api);
  const workspaceQuery = manuscriptFeatureApi.useManuscriptReaderWorkspaceQuery(api, {
    bookId: route.query.get('bookId'),
    chapterId: requestedChapterId,
    projectId: route.query.get('projectId'),
  });
  const workspace = workspaceQuery.data;
  const project = workspace?.project ?? null;
  const book = workspace?.book ?? null;
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
  const loadError = workspaceQuery.isError && !workspace ? workspaceQuery.error : workspace?.loadError;
  const editor = canEditProject(project);

  // A step from the reader to another section carries the way back to this chapter (PS4.1, RD3.2).
  const from: BackFrom = { section: 'manuscript', ...(chapter?.title ? { name: chapter.title } : {}) };
  const slot = useReaderSlot();
  const useDrawings = slot?.useDrawings ?? useNoDrawings;
  const readerDrawings = useDrawings({
    projectId: project?.id ?? '', bookId: book?.id ?? '', chapter, sourceView: textSourceView, canEdit: editor, route, navigate, from,
  });
  // The voice call knows the chapter the author reads (specs/2026-11-03-voice-assistant.md V2.4).
  useVoicePlace(project?.id, { page: 'reader', ...(readerDrawings.chapterKey ? { chapterKey: readerDrawings.chapterKey } : {}) });

  const goneChapter = status === 'empty' && Boolean(requestedChapterId) && chapters.length > 0 && !chapters.some((item) => item.id === requestedChapterId);
  // The chapter is the place this browser kept for the book (specs/2026-11-11-ux7-shelf.md SF1.1): when it is gone the book
  // opens at its first chapter without the line of a stale link — the author followed no link — and that chapter becomes
  // the place.
  const routeBookId = route.query.get('bookId') ?? '';
  const fromPlace = useMemo(() => Boolean(requestedChapterId) && readingPlaceOf(routeBookId) === requestedChapterId, [requestedChapterId, routeBookId]);
  useEffect(() => {
    if (!goneChapter) return;
    if (!fromPlace) setGoneLink(true);
    navigate('/manuscript/books', readerQuery(route, { chapterId: undefined, paragraphId: undefined, expectedRevision: undefined }), { replace: true });
  }, [fromPlace, goneChapter, navigate, route]);
  // The chapter read now is the place of its book in this browser: the card of the shelf opens the book here.
  const readBookId = status === 'ready' ? book?.id ?? '' : '';
  const readChapterId = status === 'ready' ? chapter?.id ?? '' : '';
  useEffect(() => {
    if (readBookId && readChapterId) rememberReadingPlace(readBookId, readChapterId);
  }, [readBookId, readChapterId]);
  const landing = readerSourceLanding(route, chapter, textSourceView);
  const wrongVersion = Boolean(chapter && chapter.contentVariant !== textSourceView);
  const replaced = Boolean(requestedChapterId && chapter && requestedChapterId !== chapter.id);
  const linkGone = goneLink || landing.problem === 'unavailable' || (replaced && !landing.problem);
  const stale: ReaderStaleLink | null = wrongVersion || (linkGone && fromPlace) ? null : linkGone ? 'unavailable' : landing.problem;
  const staleKey = `${stale}|${goneLink ? 'gone' : requestedChapterId}|${route.query.get('paragraphId') ?? ''}|${route.query.get('expectedRevision') ?? ''}`;
  const activeParagraphId = landing.paragraph?.id ?? null;
  const locator = chapter ? buildReaderLocator(project?.id ?? '', book?.id ?? '', chapter.id, activeParagraphId ?? '', landing.paragraph?.text ?? '',
    chapter.revision, textSourceView) : null;
  const title = chapter ? `Глава ${chapter.navigation.displayNumber}. ${formatReaderChapterTitle(chapter.navigation.displayNumber, chapter.title)}`.replace(/\. $/u, '') : '';
  // The tab names the chapter and the book read, and the saga (specs/2026-11-10-ux1-shell.md SH1.3).
  usePageTitlePart('object', [title || null, book?.title].filter(Boolean).join(' · '));

  const find = useBookFind({ api, projectId: project?.id ?? '', bookId: book?.id ?? '', chapter, chapters, query: findQuery ?? '' });
  const places = useFindPlaces({ chapter, find, findQuery });
  const chapterNotes = useChapterNotes(api, {
    projectId: project?.id ?? '', bookId: book?.id ?? '', chapterId: chapter?.id ?? '', chapterTitle: chapter?.title ?? '',
  });
  const scroll = useReaderScroll(scrollerRef, chapter?.id ?? '', activeParagraphId);

  const go = (overrides: Record<string, string | undefined>, options?: { replace?: boolean }) =>
    navigate('/manuscript/books', readerQuery(route, overrides), options);
  const openChapter = (chapterId: string, paragraphId?: string) =>
    navigate('/manuscript/books', chapterQuery(route, { projectId: project?.id, bookId: book?.id, chapterId, ...(paragraphId ? { paragraphId } : {}) }));
  const backToBooks = () => navigate('/manuscript/books', { projectId: project?.id ?? undefined, bookId: book?.id ?? undefined, chapterId: chapter?.id });
  // The note open beside the text is in the address (specs/2026-11-11-ux7-notes.md NT2.3); another tab closes it.
  const openNote = panel === 'notes' ? route.query.get('note') : null;
  const setPanel = (next: ReaderPanel | null) => go({ contents: next ?? undefined, chapters: undefined, view: undefined });
  /** «Содержание» closed: the focus goes back to the button that opened it, the one of the line or of the bottom bar. */
  const closePanel = () => {
    setPanel(null);
    [barContentsRef.current, contentsButtonRef.current].find((button) => button?.offsetParent)?.focus();
  };
  const toggleOnlyText = () => go({ focus: onlyText ? undefined : '1', view: undefined });
  const closeFind = () => {
    setFindShown(false);
    setFindDraft('');
    if (findQuery !== null || panel === 'found') go({ find: undefined, contents: panel === 'found' ? undefined : panel ?? undefined }, { replace: true });
  };
  const openFind = (words?: string) => {
    setFindShown(true);
    if (words !== undefined) {
      const query = words.replace(/\s+/gu, ' ').trim().slice(0, 100);
      setFindDraft(query);
      go({ find: query || undefined }, { replace: true });
    }
    requestAnimationFrame(() => findFieldRef.current?.focus());
  };
  // The words typed go to the address after a pause: the search of the book follows them there.
  useEffect(() => {
    const words = findDraft.trim();
    if (!findShown || words === (findQuery ?? '')) return undefined;
    const timer = window.setTimeout(() => go({ find: words || undefined }, { replace: true }), 300);
    return () => window.clearTimeout(timer);
  });
  const stepPlace = (step: 1 | -1) => {
    const target = places.step(step);
    if (target && target.chapterId !== chapter?.id) openChapter(target.chapterId, target.paragraphId);
  };
  const openPlace = (key: string) => {
    const target = places.choose(key);
    if (target && target.chapterId !== chapter?.id) openChapter(target.chapterId, target.paragraphId);
    if (phone) setPanel(null);
  };

  useReaderKeys({
    previous: chapter?.navigation.previous ? () => openChapter(chapter.navigation.previous!.id) : null,
    next: chapter?.navigation.next ? () => openChapter(chapter.navigation.next!.id) : null,
    find: () => openFind(),
    escape: () => {
      if (lookOpen) { go({ view: undefined }); lookButtonRef.current?.focus(); return true; }
      if (findOpen) { closeFind(); return true; }
      if (panel) { closePanel(); return true; }
      if (onlyText) { toggleOnlyText(); return true; }
      return false;
    },
  });

  const exportBook = async (format: BookExportFormat, drawings: boolean) => {
    if (!book) return;
    toasts.push({ message: `Готовим файл книги «${book.title}»…`, tone: 'neutral' });
    try {
      saveFile(await bookExport.mutateAsync({ bookId: book.id, format, drawings }));
    } catch (error) {
      toasts.push({ message: publicApiErrorMessage(error, 'Не удалось скачать книгу. Попробуйте снова.', {
        codes: { 'book_export.empty': 'Скачивать нечего: в книге нет текста.' },
      }), tone: 'error' });
    }
  };
  // The demo saga of the site takes no text of the author (specs/2026-11-09-demo-saga.md DS4.2): its chapters are not edited.
  const more: MoreMenuItem[] = [
    ...(chapter && project && editor && !project.demo ? [{ label: 'Править главу', icon: PenLine, run: () => editChapter.mutate({ projectId: project.id, chapterId: chapter.id }, {
      // The edit offers «← К главе в книге» back to this chapter (specs/2026-11-11-ux9-writing-a.md WA4.2): «Письмо» of the
      // site, or the editor the edition names (specs/2026-11-13-desk-editor.md ED1.1), which has that way back of its own.
      onSuccess: (draft) => {
        const at = slot?.editAt?.(project.id, draft.id);
        if (at) navigate(at.path, at.query);
        else navigate('/writing', { projectId: project.id, draft: draft.id }, { from: { section: 'manuscript' } });
      },
      onError: (error) => toasts.push({ message: publicApiErrorMessage(error, 'Не удалось открыть главу для правки. Попробуйте ещё раз.', {
        forbidden: 'Править главы может владелец саги, администратор или редактор.',
      }), tone: 'error' }),
    }) }] : []),
    // One «Скачать книгу…» and the window of the kind of its file, as on the shelf (specs/2026-11-11-ux9-shelf-chat.md SC1.5).
    ...(editor ? [{ label: 'Скачать книгу…', icon: Download, run: () => setDownloading(true) }] : []),
    ...(readerDrawings.inText ? [{ label: 'Показывать рисунки в тексте', icon: ImageIcon, checked: readerDrawings.inText.shown,
      run: () => readerDrawings.inText!.set(!readerDrawings.inText!.shown) }] : []),
  ];

  const placeWords = book && chapter ? {
    book: book.title,
    // «Книга N» is the place of the book on the shelf, as on its card and in the places of the saga (specs/2026-11-11-ux7-reorder.md RK1.3).
    full: `Книга ${book.place} · ${title}`,
    short: `Книга ${book.place} · гл. ${chapter.navigation.displayNumber}`,
  } : null;
  /** «Книга 2, гл. 12 «Белый порт», абзацы 3–4» — the place of a selection in words, for a question and a drawing. */
  const selectionPlace = (selection: Pick<ReaderSelection, 'paragraphIds'>) => {
    if (!book || !chapter) return '';
    const numbers = selection.paragraphIds.map((id) => chapter.paragraphs.findIndex((paragraph) => paragraph.id === id) + 1).filter((number) => number > 0);
    const from = Math.min(...numbers);
    const to = Math.max(...numbers);
    const paragraphs = numbers.length ? to > from ? `, абзацы ${from}–${to}` : `, абзац ${from}` : '';
    return `Книга ${book.place}, ${chapterWords({ displayNumber: chapter.navigation.displayNumber, title: chapter.title })}${paragraphs}`;
  };

  const lookPresence = usePresence(lookOpen && status === 'ready' ? 'open' : null);
  const contentsPresence = usePresence(panel && status === 'ready' ? panel : null);
  const look = (
    <ReaderAppearanceControls columnWidth={prefs.columnWidth} onColumnWidth={prefs.setColumnWidth} onOnlyText={toggleOnlyText} onStepSize={prefs.stepTextSize}
      onTheme={prefs.setDisplayTheme} onlyText={onlyText} step={textSizeStep(prefs.textSize)} steps={readerTextSizes.length} textSize={prefs.textSize} theme={background} />
  );

  function surface() {
    if (status === 'loading' || goneChapter) return <ReaderLoadingState />;
    if (status === 'forbidden') return <ReaderForbiddenState message={formatError(loadError, 'Нет доступа к этой главе.')} onBack={backToBooks} />;
    if (status === 'error') {
      return <ReaderErrorState message={formatError(loadError, 'Не удалось открыть главу.')} onRetry={() => void workspaceQuery.refetch()} supportCode={supportCode(loadError)} />;
    }
    if (status === 'empty' || !chapter || !locator) {
      return <ReaderEmptyState onBack={backToBooks} onRestore={editor ? shell.openDeleted : undefined} />;
    }
    if (wrongVersion) {
      return <ReaderErrorState message="Сервер прислал не тот текст главы." onRetry={() => void workspaceQuery.refetch()} />;
    }
    return (
      <>
        {stale && staleClosed !== staleKey ? <StaleLinkLine kind={stale} onClose={() => {
          setStaleClosed(staleKey);
          setGoneLink(false);
          go({ paragraphId: undefined, expectedRevision: undefined, chapterId: chapter.id }, { replace: true });
        }} /> : null}
        <ReaderCore
          activeParagraphId={activeParagraphId}
          chapter={chapter}
          drawLocked={readerDrawings.drawLocked}
          drawings={readerDrawings.drawings}
          footer={<ChapterTurn chapter={chapter} onOpenChapter={openChapter} />}
          locator={locator}
          marks={places.marks}
          notes={chapterNotes.items}
          onAsk={editor && project ? (selection) => navigate('/chat', { projectId: project.id }, {
            chatQuestion: `«${selection.text.replace(/\s+/gu, ' ').trim()}» (${selectionPlace(selection)})\n\n`, from,
          }) : undefined}
          // A note of a selection keeps its words as the cited part and starts without a thought (specs/2026-11-11-ux7-notes.md NT1.2).
          onCaptureSelection={notes ? (_text, captured) => notes.capture({ body: '', place: selectionPlace({ paragraphIds: captured.paragraphId ? [captured.paragraphId] : [] }),
            source: { kind: 'chapter', quote: captured.range?.quote ?? '', locator: captured } }, navigate) : undefined}
          onCopySelection={(text) => {
            void navigator.clipboard?.writeText(text).then(() => toasts.push({ message: 'Текст скопирован', tone: 'neutral' }), () => undefined);
          }}
          onDrawSelection={readerDrawings.onDrawSelection ? (selection) => readerDrawings.onDrawSelection!({
            text: selection.text, rect: selection.rect, paragraphIds: selection.paragraphIds, place: selectionPlace(selection),
          }) : undefined}
          onFindSelection={(text) => openFind(text)}
          // The sign of a note opens that very note beside the text (NT2.3).
          onOpenNote={(noteId) => go({ contents: 'notes', chapters: undefined, view: undefined, note: noteId })}
          title={title}
        />
      </>
    );
  }

  const ready = status === 'ready' && Boolean(chapter);
  const style = {
    '--reader-text-size': `${prefs.textSize}px`,
    '--reader-column': `${readerColumnWidths[prefs.columnWidth]}px`,
  } as CSSProperties;
  return (
    <section aria-label="Чтение книги" className={styles.workspace} data-display-size={prefs.textSize} data-display-theme={background} data-focus={onlyText || undefined}
      data-reading-theme={background} style={style}>
      {/* The windows of the sheet — «Содержание», «Вид текста», the menu of a selection — follow its background (TH1.4). */}
      <TextBackgroundContext.Provider value={background}>
      <div className={styles.scroller} data-reader-scroll="" ref={scrollerRef}>
        <header className={styles.head} data-hidden={(scroll.hidden && !findOpen && !lookOpen) || undefined}>
          {onlyText ? <p aria-hidden="true" className={styles.thinTitle}>{title}</p> : null}
          {placeWords ? (
            <div className={styles.barSlot}>
              <ReaderBar contentsButtonRef={barContentsRef} contentsOpen={Boolean(panel)} findOpen={findOpen} lookButtonRef={lookButtonRef} lookOpen={lookOpen} more={more}
                onBack={backToBooks} onContents={() => setPanel(panel ? null : 'chapters')} onFind={() => (findOpen ? closeFind() : openFind())}
                onLook={() => go({ view: lookOpen ? undefined : 'open' })} place={placeWords} progress={scroll.progress} />
            </div>
          ) : null}
          {findOpen && ready ? (
            <div className={styles.page}>
              <ReaderFindBar fieldRef={findFieldRef} onAll={() => setPanel('found')} onClose={closeFind}
                onEverywhere={() => shell.searchFor(findDraft.trim())} onNext={() => stepPlace(1)} onPrevious={() => stepPlace(-1)}
                onValue={setFindDraft} state={places.bar} value={findDraft} />
            </div>
          ) : null}
        </header>
        <section aria-label="Текст главы" className={styles.page} data-margin={chapterNotes.items.length || readerDrawings.drawings ? '' : undefined}>
          {surface()}
        </section>
      </div>
      {lookPresence.mounted && !phone ? (
        <ReaderLook onClose={() => go({ view: undefined })} returnFocusRef={lookButtonRef} sheet={false} state={lookPresence.status}>{look}</ReaderLook>
      ) : null}
      {contentsPresence.mounted && contentsPresence.value && chapter ? (
        <ReaderContents
          chapters={chapters}
          currentChapterId={chapter.id}
          currentMinutes={chapter.navigation.readingTimeMinutes}
          drawings={readerDrawings.tab}
          found={findQuery ? {
            query: findQuery,
            state: find.state === 'failed' ? 'failed' : find.state === 'ready' ? 'ready' : 'searching',
            places: places.list(chapters),
            onOpen: openPlace,
            onRetry: () => find.state === 'failed' && find.retry(),
          } : null}
          notes={{ ...chapterNotes, selectedId: openNote, onAll: () => notes?.openWithNavigation(navigate),
            open: openNote && notes?.renderNote ? notes.renderNote({ noteId: openNote, navigate, onBack: () => go({ note: undefined }, { replace: true }) }) : null,
            onSelect: (note) => {
              go({ note: note.id }, { replace: true });
              scrollerRef.current?.querySelector<HTMLElement>(`p[data-paragraph-id="${CSS.escape(note.paragraphId)}"]`)?.scrollIntoView?.({ block: 'center' });
            } }}
          onClose={closePanel}
          onOpenChapter={(chapterId) => {
            openChapter(chapterId);
            if (phone) setPanel(null);
          }}
          onPanel={(next) => go({ contents: next, chapters: undefined }, { replace: true })}
          panel={contentsPresence.value}
          returnFocusRef={contentsButtonRef}
          sheet={phone}
          state={contentsPresence.status}
        />
      ) : null}
      {ready ? (
        <ReaderPhoneBar chapter={chapter} contentsButtonRef={contentsButtonRef} lookButtonRef={phoneLookRef} onContents={() => setPanel(panel ? null : 'chapters')}
          onLook={() => go({ view: lookOpen ? undefined : 'open' })} onOpenChapter={openChapter} />
      ) : null}
      {lookPresence.mounted && phone ? (
        <ReaderLook onClose={() => go({ view: undefined })} returnFocusRef={phoneLookRef} sheet state={lookPresence.status}>{look}</ReaderLook>
      ) : null}
      {readerDrawings.notice}
      {readerDrawings.overlays}
      {downloading && book ? (
        <DownloadDialog busy={false} heading={`Скачать книгу «${book.title}»`} onClose={() => setDownloading(false)}
          drawings={drawingsHere ? { api, projectId: book.projectId, bookIds: [book.id] } : null}
          onDownload={(format, drawings) => {
            setDownloading(false);
            void exportBook(format, drawings);
          }} />
      ) : null}
      </TextBackgroundContext.Provider>
    </section>
  );
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

