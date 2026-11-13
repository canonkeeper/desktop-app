import { useQuery } from '@tanstack/react-query';
import { Upload } from 'lucide-react';
import { sagaHasAnswerQueryOptions } from '../../../entities/chat/api';
import { Button } from '../../../ui/controls/Button';
import { LinkAction } from '../../../ui/controls/LinkAction';
import { type ChangeEvent, useRef, useState } from 'react';
import { publicApiErrorMessage } from '../../../entities/api-errors';
import type { Book, BookExportFormat, ManuscriptApiClient } from '../../../entities/manuscript/api';
import * as manuscriptApi from '../../../entities/manuscript/api';
import type { AppRoute, RouteNavigate } from '../../../shared/navigation/app-route';
import { formatProjectMeta } from '../../../ui/copy';
import { usePresence } from '../../../ui/overlays/use-presence';
import { EmailVerificationNotice } from '../../../ui/feedback/EmailVerificationNotice';
import { LongWork } from '../../../ui/feedback/LongWork';
import { emailUnverifiedShort, useAccountSession } from '../../../shared/session/account-session';
import { useBookExportMutation, useRecentlyDeletedQuery, useManuscriptJobQuery, useRestartIndexingMutation } from '../api';
import { BookCoverEditor } from './BookCoverEditor';
import { FirstScreen, firstSagaTitle } from './FirstScreen';
import { FirstSteps, useFirstStep } from './FirstSteps';
import { MoreHints } from './MoreHints';
import { useReconcileFinishedQueue } from './useReconcileFinishedQueue';
import { RejectedImportFiles } from './RejectedImportFiles';
import { UploadOrderDialog } from './UploadOrderDialog';
import { UploadCard } from './UploadCard';
import { UploadLine, useUploadEnd } from './UploadLine';
import { BookParseSummary } from './BookParseSummary';
import { PageFileDropOverlay, usePageFileDrop } from './PageFileDrop';
import { useUpload } from './useUpload';
import { Confirm } from '../../../ui/overlays/Confirm';
import { useSagaState } from './SagaState';
import { bookStateOf, waitingLine } from '../../../entities/manuscript/preparation-copy';
import { saveFile } from '../../../ui/save-file';
import { BookActionMenu } from './BookActionMenu';
import { DownloadDialog } from './BookDownload';
import { useSagaSections } from '../../../ui/saga-sections';
import { ShelfBooks } from './ShelfBooks';
import { useBookFileUpdate } from './BookFileUpdate';
import { ConfirmDelete } from './ShelfLayers';
import { useDeletedSagaToast } from './useDeletedSagaToast';
import { useToasts } from '../../../ui/feedback/useToastQueue';
import { noDemoOffer, noProofreadOffer, useShelfSlot } from './shelf-slot';
import {
  DeletedSagasEntry,
  DemoSagaNotice,
  FirstProjectPanel,
  ShelfBooksHeader,
  ShelfHeader,
  type ShelfLoadStatus,
  ShelfStatusMessages,
  ProjectMissingState,
} from './ShelfHeader';
import { ShelfQuotaHints } from './ShelfQuotaHints';
import { useAccountQuota } from '../../../entities/account-quota/api';
import { useBookNavigation } from '../../../entities/manuscript/useBookNavigation';
import { readerHrefOf, useShelfBookActions } from './useShelfBookActions';
import { useBookMove } from './useBookMove';
import { useShelfProjectActions } from './useShelfProjectActions';
import { useVoiceFirstSaga, useVoicePlace } from '../../../shared/navigation/voice-place';
import { useShellActions } from '../../../ui/shell/shell-actions';
import { isActiveImportJob, isActiveJob, isNewBookOnItsWay } from './shelf-jobs';
import { updateLine } from './book-revert-words';
import styles from './ManuscriptShelfPage.module.css';

/** The books a saga takes by the counters of the account (SC1.8): null while they are not read or nothing is limited. */
function sagaBooksLimit(quota: ReturnType<typeof useAccountQuota>['data'], projectId: string | null) {
  const limit = quota?.ownedProjects.find((owned) => owned.projectId === projectId)?.sources.books?.limit ?? 0;
  return limit > 0 ? limit : null;
}

interface ManuscriptShelfPageProps {
  api: ManuscriptApiClient;
  navigate: RouteNavigate;
  route: AppRoute;
}

/**
 * «Книги», the shelf: the books of the saga of the address, their preparation and upload. The top bar, the
 * sections, the menu of the saga, «Поиск по книгам» and «Недавно удалённые» are the shell's
 * (specs/2026-11-10-ux2-shell-core.md SC1.2); the shelf asks it for the windows it opens.
 */
export function ManuscriptShelfPage(props: ManuscriptShelfPageProps) {
  const projectRouteKey = props.route.query.get('projectId') ?? '';
  return <ManuscriptShelfProjectRoute key={projectRouteKey} {...props} />;
}

function ManuscriptShelfProjectRoute({ api, navigate, route }: ManuscriptShelfPageProps) {
  const requestedProjectId = route.query.get('projectId');
  // An old address with `bookId` opens the shelf as usual: there is no chosen book (specs/2026-11-11-ux7-shelf.md SF1.2).
  const requestedBookId = route.query.get('bookId');
  const [notice, setNotice] = useState('');
  // «Как разобралась книга» of the books it names (specs/2026-11-11-ux7-upload.md UP4.2).
  const [summary, setSummary] = useState<string[] | null>(null);
  // «Скачать книгу…» of a book: the window of the kind of its file (specs/2026-11-11-ux9-shelf-chat.md SC1.5).
  const [downloading, setDownloading] = useState<Book | null>(null);
  const drawingsHere = useSagaSections().includes('drawing');
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const shell = useShellActions();
  const toasts = useToasts();
  const announceDeleted = useDeletedSagaToast({ api, navigate, toasts });
  const shelfQuery = manuscriptApi.useManuscriptShelfQuery(api, { projectId: requestedProjectId });
  const workspace = shelfQuery.data;
  const projects = workspace?.projects ?? [];
  const project = workspace?.project ?? null;
  useVoicePlace(project?.id, { page: 'books' });
  const books = workspace?.books ?? [];
  const canEditManuscript = workspace?.canEditManuscript ?? false;
  // The place of reading the author came back with from the reader: the book and its chapter (specs/2026-11-10-ux2-shell.md S2.2).
  const readingChapterId = route.query.get('chapterId');
  const readingBook = readingChapterId ? books.find((book) => book.id === requestedBookId) ?? null : null;
  const bookNavigation = useBookNavigation({
    api, navigate, projectId: project?.id, bookId: readingBook?.id, chapterId: readingChapterId ?? undefined,
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
  // A new book on its way to the shelf by a single import (specs/2026-11-11-import-speed.md IS2.3).
  const bookOnItsWay = jobs.some(isNewBookOnItsWay);
  // The status of the saga the shelf, its line and the words of its books say (specs/2026-11-10-ux3-saga-state.md UC2);
  // while a new book is put on the shelf and its text is indexed, it is read until the saga has text to read (IS2.2).
  const indexingStatusQuery = manuscriptApi.useIndexingStatusQuery(api, project?.id ?? '', {
    enabled: Boolean(project), follow: true,
    untilText: bookOnItsWay || jobs.some((job) => job.kind === 'canon_text_index' && isActiveJob(job)),
  });
  // What the upload window says of readiness is what the server says of the saga (specs/2026-11-10-ux1-shelf.md UXS3.3). A
  // reading that waits for the helper reads nothing: the books move, and the end of an upload says its line
  // (specs/2026-11-12-desk-window.md WN3.2).
  const sagaWaiting = waitingLine(indexingStatusQuery.data?.preparation);
  const sagaPreparing = Boolean(indexingStatusQuery.data?.preparation && !sagaWaiting)
    || indexingStatusQuery.data?.[sourceView]?.context?.state === 'processing';
  // «Что ещё можно» in place of the steps, once the saga has its first answer (specs/2026-11-10-ux3-first-screen.md FS3.3).
  const sagaHasAnswer = useQuery({
    ...sagaHasAnswerQueryOptions(api, { projectId: project?.id ?? '', sourceView }),
    enabled: Boolean(project) && canEditManuscript,
  }).data === true;
  // One way from a file to a book (specs/2026-11-11-ux7-upload.md): the upload is the server's queue and the files of this tab.
  const upload = useUpload({ api, books, canEdit: canEditManuscript && !(project?.demo ?? false), projectId: project?.id ?? null });
  const latestImportQueue = upload.latest;
  const terminalImportQueue = latestImportQueue
    && (latestImportQueue.status === 'failed' || latestImportQueue.status === 'succeeded' || latestImportQueue.status === 'canceled')
    ? latestImportQueue
    : null;

  useReconcileFinishedQueue({ projectId: project?.id ?? null, requestedProjectId, queue: terminalImportQueue });

  const explicitProjectMissing = requestedProjectId !== null
    && shelfQuery.data !== undefined
    && shelfQuery.data.project === null;
  const shelfLoaded = !explicitProjectMissing && (status === 'empty' || status === 'ready');
  const deletedSagaCount = useRecentlyDeletedQuery(api, shelfLoaded && !project).data?.sagas.total ?? 0;
  const firstStepOfSaga = useFirstStep({
    api, bookCount: books.length, canEdit: canEditManuscript, project, loaded: shelfLoaded,
  });
  // The demo saga of the site takes no books of the author (specs/2026-11-09-demo-saga.md DS4.2): its shelf has no
  // upload, and «Три шага» — the steps to a saga of his own — are not about it.
  const demo = project?.demo === true;
  const firstStep = demo ? null : firstStepOfSaga;
  const shelfSlot = useShelfSlot();
  // The update of a book from a file in work closes a new upload and the deletion of its book; the reading of the saga
  // after it is told by the status, not by Jobs (SS2.1).
  const activeBookUpdateJob = jobs.find((job) => manuscriptApi.isBookUpdateJob(job)
    && (job.status === 'queued' || job.status === 'running' || job.status === 'canceling'));
  const importJobId = activeBookUpdateJob?.id ?? '';
  const importJobQuery = useManuscriptJobQuery(api, importJobId, Boolean(importJobId));
  const importJob = importJobQuery.data?.subject.projectId === project?.id ? importJobQuery.data : null;
  const activeImportJob = importJob && isActiveImportJob(importJob) ? importJob : null;
  const bookActions = useShelfBookActions({
    api,
    books,
    jobs,
    canEditManuscript,
    navigate,
    onDeleted: (book) => announceDeleted({ kind: 'book', id: book.id, projectId: book.projectId, title: book.title }),
    toasts,
  });
  const bookUpdate = useBookFileUpdate({ api, canUpdate: canEditManuscript, onNotice: setNotice, projectId: project?.id ?? null });
  const bookExport = useBookExportMutation(api);
  // An unverified address imports nothing: every import entry stays closed (UC1.4).
  const emailUnverified = Boolean(useAccountSession()?.emailVerification);
  // The books of all sagas at their limit: the zone of a drop says so instead of calling for a file (PR2.7).
  const quota = useAccountQuota(api, canEditManuscript).data;
  const uploadRunning = Boolean(upload.queue) || upload.sending || Boolean(activeImportJob);
  // Why «Добавить книги» and a drop on the page wait now (UP1.1, UP3.5).
  const importReason = emailUnverified ? emailUnverifiedShort
    : upload.blockedReason
      ?? (activeImportJob || bookUpdate.busy ? 'Дождитесь, пока книга обновится из файла.' : null);
  const importBlocked = Boolean(importReason);
  // «Переставить…» and the drag of a card (specs/2026-11-11-ux7-reorder.md RK2): not while books are read or uploaded.
  const bookMove = useBookMove({
    api, books, canEdit: canEditManuscript, demo, reading: sagaPreparing, toasts,
    uploading: uploadRunning || bookUpdate.busy || Boolean(activeBookUpdateJob),
  });
  const { createFirstProject, creatingFirstProject, foundFirstSaga, quotaRefusalOfNotice } = useShelfProjectActions({ api, navigate, setNotice });

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

  function openImportFiles(files: readonly File[], bookId: string | null = null) {
    if (importBlocked || !project || !canEditManuscript || files.length === 0) return;
    void upload.choose(files, books.find((book) => book.id === bookId) ?? null);
  }

  function handleImportInputChange(event: ChangeEvent<HTMLInputElement>) {
    const files = [...(event.target.files ?? [])];
    event.target.value = '';
    openImportFiles(files);
  }

  // UP1.1: a file carried over any place of the shelf is taken there; the browser never opens it in place of the page.
  const drop = usePageFileDrop({
    blockedReason: importReason, enabled: Boolean(project) && !demo && !explicitProjectMissing,
    onFiles: openImportFiles, onRefused: (reason) => toasts.push({ message: 'Книги сейчас не добавить', detail: reason, tone: 'warning' }),
  });
  const uploadEnd = useUploadEnd({
    books, latest: latestImportQueue, onLook: setSummary, preparing: sagaPreparing, toasts, waiting: sagaWaiting,
  });

  async function downloadBook(book: Book, format: BookExportFormat, drawings: boolean) {
    // While the file is made, the line of the long work stands in the place of the notice (PS3.3); its end is a toast (PA1.1).
    try {
      const file = await bookExport.mutateAsync({ bookId: book.id, format, drawings });
      saveFile(file);
      toasts.push({ message: `Скачано: ${file.fileName}`, tone: 'success' });
    } catch (error) {
      toasts.push({
        message: 'Книга не скачалась',
        detail: publicApiErrorMessage(error, 'Не удалось скачать книгу. Попробуйте снова.', {
          codes: { 'book_export.empty': 'В этой книге пока нет текста: скачивать нечего.' },
          forbidden: 'Скачать книгу может её владелец, администратор или редактор.',
        }),
        tone: 'error',
      });
    }
  }

  // The first steps call for the books with the same entry as the top of the shelf (UP1.1): there is no zone of their own.
  const addBooks = !explicitProjectMissing ? (
    <div className={styles.addBooks}>
      <Button disabledReason={importReason} onClick={chooseImportFile} variant="primary">
        <Upload aria-hidden="true" size={16} />Добавить книги
      </Button>
      <span>или перетащите файлы на страницу</span>
    </div>
  ) : null;
  // «Попробовать на примере» of the site stands on a shelf without a book (specs/2026-11-09-demo-saga.md DS1.1), and
  // stays while the example is being opened, whatever the shelf shows by then.
  const useDemoOffer = shelfSlot?.useDemoOffer ?? noDemoOffer;
  const offer = useDemoOffer({ emailUnverified, onOpenSaga: (projectId) => navigate('/manuscript/books', { projectId }) });
  const demoOffer = offer.preparing || (shelfLoaded && !demo && books.length === 0) ? offer.element : null;
  // «Вычитать мою главу» of a build with «Письмо» (specs/2026-11-10-ux3-first-screen.md FS1.2, FS1.5): the first screen
  // founds «Моя сага» for it; the card stays under the steps until the saga has a book.
  const useProofreadOffer = shelfSlot?.useProofreadOffer ?? noProofreadOffer;
  const proofreadOffer = useProofreadOffer({
    projectId: project?.id ?? null,
    emailUnverified,
    foundSaga: () => foundFirstSaga(firstSagaTitle),
    onOpenDraft: (projectId, draftId) => navigate('/writing', { projectId, draft: draftId, tab: 'check' }),
  });
  useVoiceFirstSaga(workspace && projects.length === 0 ? () => createFirstProject(firstSagaTitle) : null);
  const projectMeta = explicitProjectMissing ? 'Сага недоступна' : formatProjectMeta(project);
  const exportingBook = bookExport.isPending ? books.find((book) => book.id === bookExport.variables?.bookId) : undefined;
  const sagaReading = useSagaState({
    api, books, canEdit: canEditManuscript, jobs, projectId: project?.id ?? '',
    // The file of a volume the server reads now: the line of the reading names it while it is parsed (SS2.1).
    fileName: upload.volumes.find((volume) => volume.state === 'loading')?.fileName ?? null,
    onAskQuestion: () => navigate('/chat', { projectId: project?.id }),
    // A reader opens the book he read last, else the first one (SC1.2).
    onRead: books.length ? () => bookActions.openBook(readingBook ?? books[0]!) : undefined,
  });
  // UP3.1: one line of the saga over the books — the upload over the reading.
  const uploadLineElement = <UploadLine end={uploadEnd.end} onLook={uploadEnd.look} upload={upload} />;
  const sagaState = <>{uploadLineElement}{sagaReading}</>;
  const sagaStateShown = Boolean(sagaReading) || Boolean(upload.line || upload.stopped || uploadEnd.end || upload.failure);
  const summaryBooks = summary ? books.filter((book) => summary.includes(book.id)) : [];
  const sourceNames = Object.fromEntries((latestImportQueue?.items ?? [])
    .filter((item) => item.bookId).map((item) => [item.bookId!, item.sourceFileName]));

  const bookMenuPresence = usePresence(bookActions.menuBook);
  // «Что изменилось» of the last update of a book from a file, for the 7 days the server keeps it (specs/2026-11-12-ux10-book-revert.md BR3.1).
  const openChanges = (book: Book) => shell.openBookChanges({ bookId: book.id, projectId: book.projectId, jobId: book.update?.jobId ?? undefined });
  const deletePresence = usePresence(bookActions.deleteTarget);
  const lastMenuAnchor = useRef(bookActions.menuAnchor);
  if (bookActions.menuAnchor) lastMenuAnchor.current = bookActions.menuAnchor;
  // «Дочитать» of a book is the restart of the reading of the saga, the one of its line (SF2.2).
  const restart = useRestartIndexingMutation(api);
  // Why «Новая версия из файла…» and «Удалить книгу…» of a book wait now (specs/2026-11-10-ux2-patterns-refusals.md PR1.1).
  const updateReasonOf = (book: Book) => bookActions.isBookUpdating(book.id) ? 'Книга уже обновляется из файла.'
    : emailUnverified ? emailUnverifiedShort
      : uploadRunning || bookUpdate.busy ? 'Пока идёт загрузка книг — новую версию можно будет выбрать после неё.' : null;
  // UP3.5: while books are uploaded no book of the saga is deleted — the server holds the places of the volumes.
  const deleteReasonOf = (book: Book) => bookActions.isBookUpdating(book.id) ? 'Пока книга обновляется из файла, удалить её нельзя.'
    : upload.queue ? 'Пока идёт загрузка книг, удалить книгу нельзя — дождитесь её конца.' : null;

  // Before the first saga — one screen with its ways in (specs/2026-11-10-ux3-first-screen.md FS1.2).
  if (firstStep === 1) {
    return (
      <div className={styles.content}>
        <FirstScreen
          createSaga={<FirstProjectPanel creating={creatingFirstProject} onCreate={createFirstProject} stacked />}
          example={demoOffer}
          proofread={proofreadOffer}
        />
        <ShelfStatusMessages emptySaga={false} error={null} notice={notice} noticeRefusal={quotaRefusalOfNotice(notice)}
          onRetry={() => void shelfQuery.refetch()} retrying={false} status="empty" />
        {/* UXS4.1: an author without a saga finds the sagas he deleted. */}
        {deletedSagaCount > 0 ? <DeletedSagasEntry count={deletedSagaCount} onOpen={shell.openDeleted} /> : null}
      </div>
    );
  }

  return (
    <>
        <section className={styles.content} aria-label="Книги саги">
          {explicitProjectMissing ? null : <ShelfHeader
            canEditManuscript={canEditManuscript}
            demo={demo}
            fileInputRef={fileInputRef}
            importBlocked={importBlocked}
            importReason={importReason ?? undefined}
            onChooseImportFile={chooseImportFile}
            onImportInputChange={handleImportInputChange}
            projectExists={Boolean(project)}
            projectMeta={projectMeta}
            continueReading={readingBook ? { book: readingBook.title, onOpen: bookNavigation.onOpenReader } : undefined}
            materials={project ? <>
              {shelfSlot?.materialsFromFiles ? 'Навыки, места и герои из таблиц — в ' : 'Навыки, места и герои — в '}
              <LinkAction onClick={() => navigate('/memory', { projectId: project.id, part: 'materials' })}>«Ваши материалы»</LinkAction>
            </> : undefined}
          />}
          {explicitProjectMissing || !canEditManuscript || demo ? null
            : <ShelfQuotaHints api={api} projectId={project?.id ?? null} />}

          {explicitProjectMissing ? (
            <ProjectMissingState onChoose={(id) => navigate('/manuscript/books', { projectId: id })} onCreate={shell.openNewSaga} sagas={projects} />
          ) : <ShelfStatusMessages
            emptySaga={Boolean(project) && firstStep === null && !demo && !bookOnItsWay && upload.volumes.length === 0}
            error={shelfQuery.isError && !workspace ? shelfQuery.error : null}
            notice={notice || (bookUpdate.working ?? '')}
            noticeRefusal={quotaRefusalOfNotice(notice)}
            onRetry={() => void shelfQuery.refetch()}
            retrying={shelfQuery.isRefetching}
            status={status}
            work={exportingBook ? <LongWork label={`Готовим файл книги «${exportingBook.title}»`} title={`Готовим файл книги «${exportingBook.title}»`} /> : undefined}
          />}
          <RejectedImportFiles files={upload.refused.map((file) => ({ fileName: file.file.name, message: file.reason }))}
            onChoose={chooseImportFile} onDismiss={upload.dismissRefused} />
          {/* The steps, and once the saga has answered, what else it can (FS3.3); the example takes no hints of its own. */}
          {!firstStep && project && sagaHasAnswer && canEditManuscript && !demo ? (
            <MoreHints authorId={project.currentMembership.userId} onOpen={(path) => navigate(path, { projectId: project.id })} />
          ) : null}
          {firstStep ? <FirstSteps
            ways={firstStep === 2 && (demoOffer || proofreadOffer) ? <>{demoOffer}{proofreadOffer}</> : null}
            bookCount={books.length}
            sagaBooks={sagaBooksLimit(quota, project?.id ?? null)}
            onAskFirstQuestion={() => navigate('/chat', { projectId: project?.id })}
            projectTitle={project?.title ?? null}
            step={firstStep}
            textReady={indexingStatusQuery.data?.[sourceView]?.exists === true}
            sagaState={firstStep === 3 && sagaStateShown ? sagaState : null}
            uploadBooks={<>{emailUnverified ? <EmailVerificationNotice /> : addBooks}{firstStep === 2 ? sagaState : null}</>}
          /> : null}
          {/* SS2.1, SS2.5: one block of the state of the saga over the books; while the first steps go, it stands in them. */}
          {firstStep === 2 || firstStep === 3 ? null : sagaState}

          {project ? <ShelfBooksHeader /> : null}
          {!explicitProjectMissing ? <ShelfBooks
            books={books}
            finishing={restart.isPending}
            hrefOf={readerHrefOf}
            onFinish={() => restart.mutate({ projectId: project?.id ?? '', sourceView }, {
              onError: (error) => toasts.push({
                message: 'Не удалось дочитать', detail: publicApiErrorMessage(error, 'Попробуйте ещё раз.'), tone: 'error',
              }),
            })}
            onChanges={openChanges}
            onOpen={bookActions.openBook}
            onOpenMenu={bookActions.openBookMenu}
            move={bookMove.available ? { onDrop: bookMove.drop, reason: bookMove.reason, refuse: bookMove.refuse } : null}
            fileOver={importReason ? null : drop.overBook}
            onRename={bookActions.rename}
            onRenameEnd={bookActions.endRename}
            renaming={bookActions.renaming}
            stateOf={(book) => bookStateOf(book, indexingStatusQuery.data, bookActions.isBookUpdating(book.id))}
            upload={{
              queue: upload.queue, volumes: upload.volumes,
              card: (volume, shape) => (
                <UploadCard api={api} onFile={(itemId, file) => void upload.replaceFile(itemId, file)} onSkip={(itemId) => void upload.skip(itemId)}
                  quota={quota} shape={shape} skipping={upload.skipping === volume.itemId} volume={volume} />
              ),
            }}
          /> : null}

          {firstStep === 2 ? null : demo ? (
            <DemoSagaNotice credit={shelfSlot?.demoCredit} onCreateSaga={shell.openNewSaga} />
          ) : !explicitProjectMissing && canEditManuscript ? <EmailVerificationNotice /> : null}
          {firstStep === 2 ? null : demoOffer}
        </section>

      <PageFileDropOverlay blockedReason={importReason} book={books.find((book) => book.id === drop.overBook)?.title} over={drop.over} />
      {upload.order ? (
        <UploadOrderDialog books={books} busy={upload.sending} onClose={upload.closeOrder} onReplaceRefused={upload.replaceRefused}
          onRows={upload.setOrderRows} onSend={(rows) => void upload.sendRows(rows)} refused={upload.order.refused}
          room={upload.room} rows={upload.order.rows} />
      ) : null}
      {upload.question ? (
        <Confirm
          action="Заменить"
          alternative={{ label: 'Добавить новой', onClick: upload.addAnyway }}
          consent="no"
          onCancel={upload.dismissQuestion}
          onConfirm={() => {
            const { book, file } = upload.question!;
            upload.dismissQuestion();
            bookUpdate.read(book, file.file);
          }}
          title={upload.question.cover
            ? `Заменить «${upload.question.book.title}» этим файлом или добавить новой книгой?`
            : `«${upload.question.book.title}» уже есть в саге. Заменить её этим файлом или добавить новой книгой?`}
        >
          «Заменить» покажет, что изменится в книге, — до вашего «Обновить книгу» она не меняется.
        </Confirm>
      ) : null}
      {summary && summaryBooks.length > 0 ? (
        <BookParseSummary api={api} books={summaryBooks} onClose={() => setSummary(null)}
          onRead={(book) => { setSummary(null); bookActions.openBook(book); }}
          onRename={(book) => bookActions.startRename(book)} sourceNames={sourceNames} toasts={toasts} />
      ) : null}

      {bookMenuPresence.mounted && bookMenuPresence.value ? (
        <BookActionMenu
          anchor={bookActions.menuAnchor ?? lastMenuAnchor.current}
          book={bookMenuPresence.value}
          canEdit={canEditManuscript}
          deleteReason={deleteReasonOf(bookMenuPresence.value)}
          onChangeCover={() => bookActions.changeCover(bookMenuPresence.value!)}
          onClose={bookActions.closeBookMenu}
          onDelete={() => bookActions.requestDeleteBook(bookMenuPresence.value!)}
          onDownload={() => {
            const book = bookMenuPresence.value!;
            bookActions.closeBookMenu();
            setDownloading(book);
          }}
          moveReason={bookMove.reason}
          onMove={bookMove.available ? () => {
            const book = bookMenuPresence.value!;
            bookActions.leaveMenu();
            bookMove.open(book, bookActions.menuTrigger);
          } : undefined}
          onNewChapter={shelfSlot?.newChapterAt && !demo ? () => {
            const at = shelfSlot.newChapterAt!(bookMenuPresence.value!);
            bookActions.closeBookMenu();
            navigate(at.path, at.query);
          } : undefined}
          onParseSummary={() => {
            const book = bookMenuPresence.value!;
            bookActions.closeBookMenu();
            setSummary([book.id]);
          }}
          onReadFromStart={() => bookActions.readFromStart(bookMenuPresence.value!)}
          onRename={() => bookActions.startRename(bookMenuPresence.value!)}
          onUpdateFromFile={demo ? undefined : () => {
            const book = bookMenuPresence.value!;
            bookActions.closeBookMenu();
            bookUpdate.choose(book);
          }}
          state={bookMenuPresence.status}
          updated={bookMenuPresence.value.update ? { summary: updateLine(bookMenuPresence.value.update), onOpen: () => {
            const book = bookMenuPresence.value!;
            bookActions.closeBookMenu();
            openChanges(book);
          } } : undefined}
          updateReason={updateReasonOf(bookMenuPresence.value)}
        />
      ) : null}
      {downloading ? (
        <DownloadDialog busy={false} heading={`Скачать книгу «${downloading.title}»`} onClose={() => setDownloading(null)}
          drawings={drawingsHere ? { api, projectId: downloading.projectId, bookIds: [downloading.id] } : null}
          onDownload={(format, drawings) => {
            const book = downloading;
            setDownloading(null);
            void downloadBook(book, format, drawings);
          }} />
      ) : null}
      {bookUpdate.element}
      {bookMove.element}

      {canEditManuscript && bookActions.coverEditorBook ? (
        <BookCoverEditor api={api} book={bookActions.coverEditorBook} onClose={bookActions.closeCoverEditor} />
      ) : null}

      {deletePresence.mounted && deletePresence.value ? (
        <ConfirmDelete
          book={deletePresence.value}
          error={bookActions.deleteError}
          isDeleting={bookActions.deletingBook}
          onClose={bookActions.dismissDeleteConfirmation}
          onDelete={() => void bookActions.deleteBook(deletePresence.value!)}
          state={deletePresence.status}
        />
      ) : null}


    </>
  );
}
