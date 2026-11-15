// SPDX-License-Identifier: AGPL-3.0-only
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Eye, Link2, RotateCw, Unlink } from 'lucide-react';
import { useEffect, useMemo, useState } from 'react';
import { loadFailureWords } from '../../../entities/api-errors';
import { Button } from '../../../ui/controls/Button';
import { Alert } from '../../../ui/feedback/Alert';
import { ErrorBlock } from '../../../ui/feedback/ErrorBlock';
import { useToasts } from '../../../ui/feedback/useToastQueue';
import { choiceState, pruneMemory, type ChoiceMemory, type ChoiceState, type FolderChoice } from './choice';
import { FolderList } from './FolderList';
import { LinkWindow, type LinkedBook } from './LinkWindow';
import {
  markBooksChanged, markTreeOld, obsidianKeys, treeQueryOptions,
  type ObsidianClient, type ObsidianTree, type ObsidianVault,
} from './obsidian-api';
import { RefusalExits, Skipped, WorkLine } from './lines';
import { refusalOf } from './refusals';
import { pickedLine, words } from './words';
import styles from './Obsidian.module.css';

/** A work of the server said after five seconds by what it may take (OW2.8, OW3.1). */
export function useLongWait(going: boolean) {
  const [long, setLong] = useState(false);
  useEffect(() => {
    if (!going) {
      setLong(false);
      return undefined;
    }
    const timer = window.setTimeout(() => setLong(true), 5_000);
    return () => window.clearTimeout(timer);
  }, [going]);
  return long;
}

interface ChoiceStepProps {
  api: ObsidianClient;
  projectId: string;
  vault: ObsidianVault;
  memory: ChoiceMemory;
  onMemory: (memory: ChoiceMemory) => void;
  onChange: () => void;
  onShow: (state: ChoiceState) => void;
  /** A preview of this page is still read: «Показать, что получится» waits for it (OW2.7). */
  previewReading: boolean;
  /** A write of «Загрузить» of the saga goes: it holds the vault, so a new preview waits for it (OW4.1). */
  loadGoing: boolean;
  queueLive: boolean;
}

/**
 * The step of the choice of folders (specs/2026-11-15-desk-obsidian-web.md OW2; board 44·50): the look over the folder — on
 * the entry when there is none, after a change of the folder and a load, by «Прочитать папку заново», never by itself on the
 * return to the window —, the decisions the books of the saga wait for, the folders with their choices, and the way to «что
 * получится».
 */
export function ChoiceStep({ api, loadGoing, memory, onChange, onMemory, onShow, previewReading, projectId, queueLive, vault }: ChoiceStepProps) {
  const client = useQueryClient();
  const tree = useQuery(treeQueryOptions(api, projectId));
  const folders = tree.data?.folders;
  // A folder the tree no longer has and its notes drop out of the remembered choice (OW2.6) — only by a whole look: a look cut
  // by its limits, or one whose folders of notes only in the cloud are not seen, does not show every folder there is.
  const whole = tree.data && !tree.data.truncated && !tree.data.skipped.some((skip) => skip.reason === 'cloud_only');
  useEffect(() => {
    if (!folders || !whole) return;
    const pruned = pruneMemory(memory, folders);
    if (JSON.stringify(pruned) !== JSON.stringify(memory)) onMemory(pruned);
  }, [folders, memory, onMemory, whole]);
  const state = useMemo(() => (folders?.length ? choiceState(folders, vault.books ?? [], memory) : null), [folders, memory, vault.books]);
  const long = useLongWait(tree.isFetching);
  const rescan = () => void client.refetchQueries({ queryKey: obsidianKeys.tree(projectId) });
  const choose = (path: string, choice: FolderChoice) => onMemory({ ...memory, kinds: { ...memory.kinds, [path]: choice } });

  const failed = tree.isError && !tree.isFetching ? refusalOf(tree.error, 'scan') : null;
  // `not_linked`: the folder of the saga is read again — the page says «папка не выбрана» by its answer (Data).
  const unlinked = Boolean(failed?.reread);
  useEffect(() => {
    if (unlinked) void client.invalidateQueries({ queryKey: obsidianKeys.vault(projectId) });
  }, [client, projectId, unlinked]);
  const rootName = folders?.[0]?.name ?? vault.name ?? '';
  const cloud = tree.data?.skipped.some((skip) => skip.reason === 'cloud_only') ?? false;
  const empty = tree.data && !state;

  return (
    <section aria-labelledby="obsidian-step" className={styles.step}>
      <h2 className={styles.stepTitle} data-step-heading="" id="obsidian-step" tabIndex={-1}>{words.choiceTitle(rootName)}</h2>
      <p className={styles.text}>{words.choiceExplain}</p>
      {tree.isFetching ? <WorkLine detail={long ? words.scanLong : undefined} error={tree.failureReason} failures={tree.failureCount} title={words.scanning} /> : null}
      {failed && !failed.reread ? (failed.failure
        ? <ErrorBlock {...loadFailureWords(tree.error)} onRetry={rescan} title={words.scanFailed} />
        : (
          <Alert tone="error">
            <p className={styles.text}>{failed.text}</p>
            <RefusalExits exits={failed.exits} on={{ retry: rescan, checkAgain: rescan, change: onChange }} />
          </Alert>
        )) : null}
      {tree.data?.truncated ? (
        <Alert role="note" tone="info">
          <p className={styles.text}>{words.cut}</p>
          <RefusalExits exits={['change']} on={{ change: onChange }} />
        </Alert>
      ) : null}
      {empty ? (
        <Alert role="note" tone="warning">
          <p className={styles.text}>{cloud ? words.cloudEmpty : words.noNotes}</p>
          <RefusalExits exits={[cloud ? 'rescan' : 'change']} on={{ rescan, change: onChange }} />
        </Alert>
      ) : null}
      {state ? <>
        {cloud ? <p className={styles.warning}>{words.cloudNote}</p> : null}
        <Decisions api={api} folders={folders!} projectId={projectId} queueLive={queueLive} vault={vault} />
        <FolderList onChoose={choose} state={state} />
      </> : null}
      {tree.data ? <Skipped of="files" skipped={tree.data.skipped} /> : null}
      {state ? (
        <div className={styles.footer}>
          <span className={styles.muted}>{pickedLine(state.books, state.materials)}</span>
          <div className={styles.footerActions}>
            <Button disabledReason={tree.isFetching ? words.scanning : null} onClick={rescan} size="sm" variant="ghost"><RotateCw aria-hidden="true" size={14} />{words.rescan}</Button>
            <Button disabledReason={previewReading ? words.whyReading : loadGoing ? words.loadGoing : state.reason} onClick={() => onShow(state)} variant="primary">
              <Eye aria-hidden="true" size={16} />{words.show}
            </Button>
          </div>
        </div>
      ) : tree.data ? (
        <div className={styles.footerActions}>
          <Button disabledReason={tree.isFetching ? words.scanning : null} onClick={rescan} size="sm" variant="ghost"><RotateCw aria-hidden="true" size={14} />{words.rescan}</Button>
        </div>
      ) : null}
    </section>
  );
}

/**
 * «Нужно ваше решение» over the folders (OW2.9): the books whose folder another book holds — named — and the books whose
 * folder is not in its place; each with «Связать с другой папкой…» and «Снять связь».
 */
function Decisions({ api, folders, projectId, queueLive, vault }: {
  api: ObsidianClient;
  folders: ObsidianTree['folders'];
  projectId: string;
  queueLive: boolean;
  vault: ObsidianVault;
}) {
  const client = useQueryClient();
  const toasts = useToasts();
  const [linking, setLinking] = useState<LinkedBook | null>(null);
  const books = vault.books ?? [];
  const unlink = useMutation({
    mutationFn: (book: LinkedBook) => api.unlinkBookFromObsidianFolder(projectId, book.bookId),
    onSuccess: (_answer, book) => {
      markBooksChanged(client, projectId);
      void client.invalidateQueries({ queryKey: obsidianKeys.vault(projectId) });
      markTreeOld(client, projectId);
      toasts.push({ message: words.unlinkedBook(book.title), tone: 'success' });
    },
    onError: (error) => {
      const said = refusalOf(error, 'link');
      toasts.push({ message: said.failure ? words.tryAgain : said.text, tone: 'error' });
    },
  });
  const decisions: Array<{ book: LinkedBook; text: string }> = [
    ...(vault.relink ?? []).map((book) => ({
      book, text: words.relink(book.title, book.folder, books.find((holder) => holder.folder === book.folder)?.title ?? ''),
    })),
    ...books.filter((book) => book.presence === 'missing').map((book) => ({ book, text: words.bookFolderMissing(book.title) })),
  ];
  if (!decisions.length) return null;
  return (
    <section aria-label={words.decisionTitle} className={styles.decision}>
      <h3>{words.decisionTitle}</h3>
      <ul>
        {decisions.map(({ book, text }) => (
          <li className={styles.part} key={book.bookId}>
            <p className={styles.text}>{text}</p>
            <div className={styles.row}>
              <Button onClick={() => setLinking(book)} size="sm" variant="outline"><Link2 aria-hidden="true" size={14} />{words.linkOther}</Button>
              <Button loading={unlink.isPending && unlink.variables?.bookId === book.bookId} onClick={() => unlink.mutate(book)} size="sm" variant="ghost">
                <Unlink aria-hidden="true" size={14} />{words.unlinkBook}
              </Button>
            </div>
          </li>
        ))}
      </ul>
      {linking ? <LinkWindow api={api} book={linking} books={books} folders={folders} onClose={() => setLinking(null)} projectId={projectId} queueLive={queueLive} /> : null}
    </section>
  );
}

