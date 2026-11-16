// SPDX-License-Identifier: AGPL-3.0-only
import { useIsFetching, useQuery, useQueryClient } from '@tanstack/react-query';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { EditionPageProps } from '../../../app/edition';
import { loadFailureWords } from '../../../entities/api-errors';
import { useManuscriptShelfQuery, type Book, type Project } from '../../../entities/manuscript/api';
import { useImportConstraintsQuery, useImportQueuesQuery } from '../../../features/manuscript/api';
import { useBookFileUpdate } from '../../../features/manuscript/shelf/BookFileUpdate';
import { fileStem, liveItems, waits } from '../../../features/manuscript/shelf/upload-files';
import { ErrorBlock } from '../../../ui/feedback/ErrorBlock';
import { SagaMissing } from '../../../ui/feedback/SagaMissing';
import { SkeletonArea } from '../../../ui/feedback/Skeleton';
import { useToasts } from '../../../ui/feedback/useToastQueue';
import { Confirm } from '../../../ui/overlays/Confirm';
import { useShellActions } from '../../../ui/shell/shell-actions';
import { choiceState, emptyMemory, type ChoiceMemory, type ChoiceState } from './choice';
import { forgetChoice, keepChoice, readChoice } from './choice-memory';
import { ChoiceStep } from './ChoiceStep';
import { ExportPart } from './ExportPart';
import { Availability, FolderLine, useAvailability, useFolderChoice, VaultStart } from './FolderPart';
import { LoadLine, useLoadGoing } from './LoadResult';
import {
  closeExports, closeResults, dropReads, liveBookIds, obsidianKeys, readPreview, treeQueryOptions, useObsidianVault, usePreview,
} from './obsidian-api';
import { PreviewStep } from './PreviewStep';
import { useObsidianBookUpdate } from './update-source';
import { useExportGoing } from './use-export';
import { useSagaLoadGoing } from './use-load';
import { exportWords, updateWords, words } from './words';
import styles from './Obsidian.module.css';

/**
 * «Obsidian» (specs/2026-11-15-desk-obsidian-web.md OW1–OW4, specs/2026-11-16-desk-obsidian-export-web.md XW1; board 44·48–44·53,
 * 44·59–44·65): the page of the bridge of the saga of the address in «Книги» of the app — under the line of the folder the part
 * «Книги и записи из Obsidian» with its steps, and under it the part «Карта саги в Obsidian». Only an author who may change the
 * saga opens it; anyone else lands on «Книги».
 */
export function ObsidianPage({ api, navigate, route, user }: EditionPageProps) {
  const requested = route.query.get('projectId');
  const shelf = useManuscriptShelfQuery(api, { projectId: requested });
  const project = shelf.data?.project && (!requested || shelf.data.project.id === requested) ? shelf.data.project : null;
  const canEdit = shelf.data?.canEditManuscript ?? false;
  useEffect(() => {
    if (project && !canEdit) navigate('/manuscript/books', { projectId: project.id }, { replace: true });
  }, [canEdit, navigate, project]);
  if (shelf.isError) {
    return <div className={styles.page}><ErrorBlock {...loadFailureWords(shelf.error)} onRetry={() => void shelf.refetch()} retrying={shelf.isRefetching} title={words.booksFailed} /></div>;
  }
  if (!project) {
    return (
      <div className={styles.page}>
        <h1 className={styles.title} data-page-heading="" tabIndex={-1}>{words.pageTitle}</h1>
        {shelf.isPending ? <SkeletonArea shape="cards" /> : <SagaMissing onChoose={(id) => navigate(route.path, { projectId: id })} sagas={shelf.data?.projects ?? []} />}
      </div>
    );
  }
  if (!canEdit) return null;
  return <Bridge api={api} author={user.id} books={shelf.data?.books ?? []} key={project.id} navigate={navigate} project={project} route={route} />;
}

/** The page of the bridge has no place of notices of its own: the line of an update in work stands at the books (OU2.8). */
function noNotice() {}

function Bridge({ api, author, books, navigate, project, route }: Omit<EditionPageProps, 'user'> & { author: string; books: readonly Book[]; project: Project }) {
  const client = useQueryClient();
  const toasts = useToasts();
  const shell = useShellActions();
  const projectId = project.id;
  const vaultQuery = useObsidianVault(api, projectId);
  const vault = vaultQuery.data;
  const [memory, setMemoryState] = useState<ChoiceMemory>(() => readChoice(author, projectId));
  const setMemory = useCallback((next: ChoiceMemory) => {
    setMemoryState(next);
    keepChoice(author, projectId, next);
  }, [author, projectId]);
  // A new path of the folder drops the reads of the bridge and closes the result of the export (XW2.4).
  const choice = useFolderChoice(api, projectId, () => {
    dropReads(client, projectId);
    closeExports(client, projectId);
  });
  const { refetch: refetchVault } = vaultQuery;
  const rereadVault = useCallback(() => void refetchVault(), [refetchVault]);
  const availability = useAvailability(vault, rereadVault);
  const [unlinkAsked, setUnlinkAsked] = useState(false);
  const [unlinking, setUnlinking] = useState(false);
  const queues = useImportQueuesQuery(api, projectId);
  const constraints = useImportConstraintsQuery(api, projectId, true).data;
  const step = route.query.get('step') === 'preview' ? 'preview' : 'choice';
  const preview = usePreview(api, projectId);
  const loadGoing = useLoadGoing(preview.data?.id);
  // The look the choice step read, observed here without a read of its own: the preview of another choice is old (OW3.4).
  const look = useQuery({ ...treeQueryOptions(api, projectId), enabled: false });
  const tree = look.data;
  // The one fence of the bridge seen beforehand (XW2.3): while the export of the saga goes, the choice reads nothing and its
  // buttons wait; while «Загрузить», a look or a preview of the saga goes, the button of the export waits.
  const exporting = useExportGoing(projectId);
  const fenced = exporting ? exportWords.going : null;
  const loading = useSagaLoadGoing(projectId);
  const scanning = useIsFetching({ queryKey: obsidianKeys.tree(projectId) }) > 0;
  const exportWaits = loading ? words.loadGoing : scanning ? words.scanning : preview.isFetching ? words.whyReading : null;
  // The part of the export stands in its place once the step above it shows what it read (XW1.3).
  const stepRead = step === 'choice' ? (look.data !== undefined || look.isError) && !scanning : !preview.isFetching;
  const exportSettled = availability === 'missing' || fenced !== null || stepRead;

  // The live load of «Книги» of the saga: it goes, or a book waits for the author (OW3.6, OW2.10).
  const live = [...(queues.data?.data ?? [])].filter((queue) => ['uploading', 'draft', 'running'].includes(queue.status))[0] ?? null;
  const waiting = live ? liveItems(live).find(waits) : undefined;
  const queueNote = live ? (waiting ? words.queueWaits(fileStem(waiting.sourceFileName)) : words.queueRunning) : null;
  // «Обновить из Obsidian…» of a card (specs/2026-11-15-desk-obsidian-book-web.md OU2.8): the owner of the window of «Книги».
  const bookUpdate = useBookFileUpdate({ api, canUpdate: true, onNotice: noNotice, projectId });
  const folderUpdate = useObsidianBookUpdate({ api, navigate, projectId, queueLive: Boolean(live) });

  const goStep = useCallback((next: 'choice' | 'preview') => {
    navigate(route.path, { projectId, ...(next === 'preview' ? { step: 'preview' } : {}) }, { replace: true });
  }, [navigate, projectId, route.path]);
  // A step without its preview — the page read again — is the choice: the preview is read only by a press (OW3.1).
  useEffect(() => {
    if (step === 'preview' && !preview.data && !preview.isFetching && !preview.error) goStep('choice');
  }, [goStep, preview.data, preview.error, preview.isFetching, step]);
  // The focus stands on the heading of the step when the step changes (OW7.4).
  const shownStep = useRef(step);
  useEffect(() => {
    if (shownStep.current === step) return;
    shownStep.current = step;
    document.querySelector<HTMLElement>('[data-step-heading]')?.focus();
  }, [step]);

  const choiceNow: ChoiceState | null = useMemo(
    () => (tree?.folders.length ? choiceState(tree.folders, vault?.books ?? [], memory) : null), [memory, tree, vault?.books]);
  const show = (state: ChoiceState) => {
    void readPreview(client, api, projectId, state.request, liveBookIds(books)).catch(() => undefined);
    goStep('preview');
  };
  const showAgain = () => {
    if (choiceNow) show(choiceNow);
    else goStep('choice');
  };
  const rescan = () => {
    client.removeQueries({ queryKey: obsidianKeys.tree(projectId) });
    goStep('choice');
  };
  const skipNote = (path: string) => setMemory({ ...memory, notes: memory.notes.includes(path) ? memory.notes : [...memory.notes, path] });
  // The notes «Не брать» left out that the choice now leaves out, and the way back — the same change of the choice (OW3.2).
  const notTaken = choiceNow ? memory.notes.filter((path) => choiceNow.request.exclude?.includes(path)) : [];
  const giveBack = (path: string) => setMemory({ ...memory, notes: memory.notes.filter((note) => note !== path) });

  async function unlink() {
    setUnlinking(true);
    try {
      await api.unlinkObsidianVault(projectId);
      // The unlink drops the remembered choice and the reads of the bridge of the saga (OW1.6), and closes the result of the export (XW2.4).
      forgetChoice(author, projectId);
      setMemoryState(emptyMemory);
      dropReads(client, projectId);
      closeExports(client, projectId);
      await client.invalidateQueries({ queryKey: obsidianKeys.vault(projectId) });
      setUnlinkAsked(false);
      toasts.push({ message: words.unlinked, tone: 'success' });
    } catch {
      toasts.push({ message: words.tryAgain, tone: 'error' });
    } finally {
      setUnlinking(false);
    }
  }

  const toBooks = () => navigate('/manuscript/books', { projectId });
  return (
    <div className={styles.page}>
      <h1 className={styles.title} data-page-heading="" tabIndex={-1}>{words.pageTitle}</h1>
      {vaultQuery.isPending ? <SkeletonArea count={2} shape="rows" /> : null}
      {vaultQuery.isError && !vault ? <ErrorBlock {...loadFailureWords(vaultQuery.error)} onRetry={rereadVault} retrying={vaultQuery.isRefetching} title={words.scanFailed} /> : null}
      {vault && !vault.linked ? <VaultStart choice={choice} vault={vault} /> : null}
      {vault?.linked ? <>
        <FolderLine choice={choice} fenced={fenced} onUnlink={() => setUnlinkAsked(true)} vault={vault} />
        <Availability choice={choice} fenced={fenced} onCheck={rereadVault} state={availability} vault={vault} />
        {/* The part «Книги и записи из Obsidian» over any of its steps; without the folder in its place it has none (XW1.1). */}
        {availability === 'missing' ? null : <h2 className={styles.partTitle}>{words.partTitle}</h2>}
        {availability === 'missing' ? null : step === 'choice' ? <>
          <LoadLine onOpen={() => goStep('preview')} previewId={preview.data?.id} />
          <ChoiceStep api={api} fenced={fenced} loadGoing={loadGoing} memory={memory} onChange={() => void choice.choose(vault)} onMemory={setMemory} onShow={show}
            previewReading={preview.isFetching} projectId={projectId} queueLive={Boolean(live)} vault={vault} />
        </> : (
          <PreviewStep api={api} bookLimitBytes={constraints?.maxFileSizeBytes ?? null} choice={choiceNow} fenced={fenced} notTaken={notTaken} onGiveBack={giveBack} onSkipNote={skipNote} preview={preview}
            projectId={projectId} queueNote={queueNote} sagaBooks={books} vault={vault}
            update={{
              open: (book) => bookUpdate.open(book, folderUpdate.source), working: bookUpdate.working,
              reason: bookUpdate.busy ? updateWords.updating.wait : null,
            }}
            on={{
              showAgain, rescan, changeChoice: () => goStep('choice'), change: () => void choice.choose(vault), checkAgain: rereadVault,
              openTrash: shell.openDeleted, toBooks, toMaterials: () => navigate('/memory', { projectId, part: 'materials' }),
              loadMore: () => {
                closeResults(client, projectId);
                client.removeQueries({ queryKey: obsidianKeys.preview(projectId) });
                client.removeQueries({ queryKey: obsidianKeys.tree(projectId) });
                goStep('choice');
              },
            }} />
        )}
        <ExportPart api={api} focus={route.query.get('part') === 'export'} missing={availability === 'missing'} navigate={navigate} projectId={projectId}
          settled={exportSettled} vault={vault} waits={exportWaits} />
      </> : null}
      {bookUpdate.element}
      {folderUpdate.overlay}
      {unlinkAsked && vault?.linked ? (
        <Confirm action={words.unlink} also={exporting ? exportWords.unlinkGoing : undefined} busy={unlinking} onCancel={() => setUnlinkAsked(false)}
          onConfirm={() => void unlink()} title={words.unlinkTitle(project.title)}>
          {words.unlinkText}
        </Confirm>
      ) : null}
    </div>
  );
}

