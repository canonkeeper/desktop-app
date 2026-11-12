import { useEffect, useMemo, useRef, useState } from 'react';
import { quotaFailureCode, useAccountQuota } from '../../../entities/account-quota/api';
import { refusalWords, type RefusalWords } from '../../../entities/account-quota/refusal-words';
import { publicApiErrorMessage } from '../../../entities/api-errors';
import type { Book, ImportQueue, ManuscriptApiClient } from '../../../entities/manuscript/api';
import { createIdempotencyKey } from '../../../entities/manuscript/api';
import {
  useCancelImportQueueMutation,
  useCreateImportQueueMutation,
  useImportConstraintsQuery,
  useImportQueuesQuery,
  useSkipImportQueueItemMutation,
  useStartImportQueueMutation,
  useUploadImportQueueItemMutation,
} from '../api';
import {
  fileStem,
  liveItems,
  localFiles,
  orderRows,
  pendingViews,
  screenFiles,
  similarBook,
  uploadLine,
  uploadRoom,
  volumeViews,
  type LocalFile,
  type OrderRow,
  type RefusedFile,
  type TransferState,
  type UploadRoom,
  type VolumeView,
} from './upload-files';
import { fileOpening } from './book-opening';

type UploadClient = Pick<ManuscriptApiClient,
  'cancelImportQueue' | 'createImportQueue' | 'getAccountQuota' | 'getImportConstraints' | 'getImportQueue'
  | 'listImportQueues' | 'skipImportQueueItem' | 'startImportQueue' | 'uploadImportQueueItem'>;

const activeStatuses: ReadonlySet<ImportQueue['status']> = new Set(['uploading', 'draft', 'running']);

/** The window «Порядок томов» of two files and more (UP2.3): its rows, the files it did not take, what the tier takes. */
export type OrderWindow = Readonly<{ rows: OrderRow[]; refused: RefusedFile[] }>;

/** A refusal of the shelf before or after a request: a limit with its words, or a failure with its message. */
export type UploadFailure = Readonly<{ code: string | null; words: RefusalWords | null; message: string }>;

/** The upload this tab stopped while it still holds the files of the volumes not on the shelf (UP3.3). */
export type StoppedUpload = Readonly<{ done: number; total: number; files: LocalFile[] }>;

/**
 * One way from a file to a book (specs/2026-11-11-ux7-upload.md): what the author gives the page — by «Добавить книги» or a
 * drop anywhere on it — is checked here, then one new file goes at once, a file like a book of the shelf asks first, and
 * two files and more open the window of their order. Once sent, the upload is the server's queue (React Query); this tab
 * keeps only what it alone knows — its files and how far each went.
 */
export function useUpload({ api, books, canEdit, projectId }: {
  api: UploadClient;
  books: readonly Book[];
  canEdit: boolean;
  projectId: string | null;
}) {
  const queues = useImportQueuesQuery(api, projectId ?? '');
  const constraintsQuery = useImportConstraintsQuery(api, projectId ?? '', canEdit);
  const constraints = constraintsQuery.data ?? null;
  const quota = useAccountQuota(api, Boolean(projectId) && canEdit).data;
  const create = useCreateImportQueueMutation(api);
  const upload = useUploadImportQueueItemMutation(api);
  const start = useStartImportQueueMutation(api);
  const cancel = useCancelImportQueueMutation(api);
  const skipMutation = useSkipImportQueueItemMutation(api);

  // The ephemeral facts of this tab: the window, the question, its files and how far each went.
  const [order, setOrder] = useState<OrderWindow | null>(null);
  // `cover` — the file was dropped on the cover of the book; else it looks like it by its name or its beginning (SC1.3).
  const [question, setQuestion] = useState<{ file: LocalFile; book: Book; cover: boolean } | null>(null);
  const [refused, setRefused] = useState<RefusedFile[]>([]);
  const [failure, setFailure] = useState<UploadFailure | null>(null);
  const [sending, setSending] = useState<LocalFile[]>([]);
  const [transfers, setTransfers] = useState<Record<string, TransferState>>({});
  const [stopAsked, setStopAsked] = useState(false);
  const [stopped, setStopped] = useState<StoppedUpload | null>(null);
  const files = useRef(new Map<string, File>());
  const busy = useRef(false);

  useEffect(() => {
    setOrder(null);
    setQuestion(null);
    setRefused([]);
    setFailure(null);
    setSending([]);
    setTransfers({});
    setStopped(null);
    files.current.clear();
  }, [projectId]);

  const latest = useMemo(() => [...(queues.data?.data ?? [])].sort((left, right) => right.updatedAt.localeCompare(left.updatedAt))[0] ?? null,
    [queues.data]);
  const queue = latest && activeStatuses.has(latest.status) ? latest : null;
  const shelfBookIds = useMemo(() => new Set(books.map((book) => book.id)), [books]);
  const volumes: VolumeView[] = useMemo(() => queue ? volumeViews(queue, transfers, shelfBookIds) : pendingViews(sending),
    [queue, sending, shelfBookIds, transfers]);
  const sendingHere = Object.values(transfers).some((transfer) => transfer.state === 'sending' || transfer.state === 'waiting');
  // Once every volume is a book of the shelf, the line of the reading says the rest (UP3.1: one line of the saga).
  const line = queue && volumes.length > 0 ? uploadLine(queue, volumes, sendingHere) : null;
  const room = uploadRoom(quota, projectId);

  const working = Boolean(queue) || sending.length > 0;
  const blockedReason = !canEdit
    ? 'Добавлять книги в эту сагу может её владелец, администратор или редактор.'
    : working
      ? queue && line && line.remaining === 0 && volumes.some((volume) => volume.state === 'failed')
        ? `Сначала решите, что делать с ${volumes.filter((volume) => volume.state === 'failed').map((volume) => `«${fileStem(volume.fileName)}»`).join(', ')}: заменить файл или пропустить.`
        : `Дождитесь конца загрузки: идёт ${queue ? liveItems(queue).length - volumes.length + 1 : 1} из ${queue ? liveItems(queue).length : sending.length}.`
      : null;

  function fail(error: unknown, fallback: string) {
    const code = quotaFailureCode(error) ?? null;
    const words = code ? refusalWords(code, quota) : null;
    setFailure({ code: words ? code : null, words, message: words ? words.ended : publicApiErrorMessage(error, fallback) });
  }

  /**
   * «Добавить книги» and a drop anywhere on the page (UP1.1, UP1.2, UP2.1–UP2.3); a file dropped on the cover of a book is
   * asked about as that book, and a file that looks like one — by its name or by how it begins — too (SC1.3).
   */
  async function choose(chosenFiles: readonly File[], onBook: Book | null = null) {
    if (!projectId || chosenFiles.length === 0 || blockedReason) return;
    setFailure(null);
    setStopped(null);
    // Files dropped in the first moment of the page wait for what the server takes: they are not dropped silently.
    const limits = constraints ?? (await constraintsQuery.refetch()).data ?? null;
    if (!limits) {
      setFailure({ code: null, words: null, message: 'Не удалось узнать, какие файлы принимает сага. Выберите файлы ещё раз.' });
      return;
    }
    const screened = screenFiles(localFiles(chosenFiles), limits);
    if (chosenFiles.length >= 2) {
      setRefused([]);
      setOrder({ rows: orderRows(screened.chosen), refused: screened.refused });
      return;
    }
    setRefused(screened.refused);
    const only = screened.chosen[0];
    if (!only) return;
    if (onBook) {
      setQuestion({ file: only, book: onBook, cover: true });
      return;
    }
    const like = similarBook(only.file.name, books) ?? similarBook(only.file.name, books, await fileOpening(only.file));
    if (like) {
      setQuestion({ file: only, book: like, cover: false });
      return;
    }
    if (room && room.room === 0) {
      const words = refusalWords(room.code, quota);
      setFailure({ code: room.code, words, message: words?.ended ?? 'Книга не поместится.' });
      return;
    }
    void send([only]);
  }

  /** The files go: a queue of their rows, each file in turn, then its start — no confirmation of a volume (UP2.1, UP3.1). */
  async function send(chosen: readonly LocalFile[]) {
    if (!projectId || chosen.length === 0 || busy.current) return;
    busy.current = true;
    setOrder(null);
    setQuestion(null);
    setStopped(null);
    setSending([...chosen]);
    for (const local of chosen) files.current.set(local.key, local.file);
    setTransfers(Object.fromEntries(chosen.map((local) => [local.key, { state: 'waiting', sent: 0, total: null } as const])));
    try {
      const created = await create.mutateAsync({
        projectId,
        idempotencyKey: createIdempotencyKey('createImportQueue'),
        body: { items: chosen.map((local, index) => ({ clientItemId: local.key, position: index + 1, sourceFileName: local.file.name })) },
      });
      setSending([]);
      await sendFiles(created);
    } catch (error) {
      setSending([]);
      setTransfers({});
      fail(error, 'Не удалось начать загрузку. Повторите.');
    } finally {
      busy.current = false;
    }
  }

  /** Each file of the queue still to send goes from this tab; once every file is received the queue starts by itself. */
  async function sendFiles(target: ImportQueue, only?: { itemId: string; file: File }) {
    if (!projectId) return;
    let current = target;
    for (const item of [...target.items].sort((left, right) => left.position - right.position)) {
      if (item.preflightStatus !== 'uploading' && item.id !== only?.itemId) continue;
      if (only && item.id !== only.itemId) continue;
      const file = only?.file ?? files.current.get(item.clientItemId);
      if (!file) continue;
      setTransfers((state) => ({ ...state, [item.clientItemId]: { state: 'sending', sent: 0, total: file.size } }));
      try {
        current = await upload.mutateAsync({
          projectId, queueId: current.id, itemId: item.id, file, idempotencyKey: createIdempotencyKey('uploadImportQueueItem'),
          onUploadProgress: ({ loadedBytes, totalBytes }) => setTransfers((state) => ({
            ...state, [item.clientItemId]: { state: 'sending', sent: loadedBytes, total: totalBytes },
          })),
        });
        setTransfers((state) => ({ ...state, [item.clientItemId]: { state: 'sent', sent: file.size, total: file.size } }));
      } catch (error) {
        setTransfers((state) => ({ ...state, [item.clientItemId]: { state: 'failed', sent: 0, total: null } }));
        fail(error, `«${item.sourceFileName}» не дошёл до сервера. Выберите его на карточке книги снова.`);
      }
    }
    if (current.status === 'draft') await startQueue(current);
  }

  async function startQueue(target: ImportQueue) {
    if (!projectId) return;
    try {
      await start.mutateAsync({
        projectId, queueId: target.id, idempotencyKey: createIdempotencyKey('startImportQueue'), body: { expectedVersion: target.version },
      });
    } catch (error) {
      fail(error, 'Загрузка не началась. Повторите.');
    }
  }

  /**
   * «Заменить файл» of a volume that waits, and «Выбрать файл» of a file that did not reach the server: the new file goes
   * to the same place, checked as any file, and the queue takes it by itself (UP3.2).
   */
  async function replaceFile(itemId: string, file: File) {
    if (!projectId || !queue || !constraints) return;
    const check = screenFiles(localFiles([file]), constraints);
    if (check.refused.length > 0) {
      setFailure({ code: null, words: null, message: `«${file.name}»: ${check.refused[0]!.reason}` });
      return;
    }
    setFailure(null);
    const item = queue.items.find((candidate) => candidate.id === itemId);
    if (!item) return;
    files.current.set(item.clientItemId, file);
    await sendFiles(queue, { itemId, file });
  }

  /** «Пропустить»: the volume that waits leaves the upload, its place closes (UP3.2). */
  async function skip(itemId: string) {
    if (!projectId || !queue) return;
    try {
      await skipMutation.mutateAsync({ projectId, queueId: queue.id, itemId });
    } catch (error) {
      fail(error, 'Не удалось пропустить книгу. Повторите.');
    }
  }

  /** «Остановить остальные» after its question: the volumes not on the shelf stay with this tab to go on later (UP3.3). */
  async function confirmStop() {
    setStopAsked(false);
    if (!projectId || !queue) return;
    const left = volumes.map((volume) => files.current.get(volume.key) ? { key: volume.key, file: files.current.get(volume.key)! } : null)
      .filter((local): local is LocalFile => local !== null);
    const total = liveItems(queue).length;
    try {
      await cancel.mutateAsync({ projectId, queueId: queue.id });
      setTransfers({});
      setStopped(left.length > 0 ? { done: total - volumes.length, total, files: left } : null);
    } catch (error) {
      fail(error, 'Не удалось остановить загрузку. Повторите.');
    }
  }

  return {
    /** Why «Добавить книги» and a drop wait now, or null (UP1.1, UP3.5). */
    blockedReason,
    choose,
    constraintsReady: Boolean(constraints),
    failure,
    dismissFailure: () => setFailure(null),
    refused,
    dismissRefused: () => setRefused([]),
    question,
    addAnyway: () => { if (question) void send([question.file]); },
    dismissQuestion: () => setQuestion(null),
    order,
    setOrderRows: (rows: OrderRow[]) => setOrder((current) => current ? { ...current, rows } : current),
    /** «Выбрать другой» of a file the window did not take: the new file passes the same check (UP1.2). */
    replaceRefused(key: string, file: File) {
      if (!constraints || !order) return;
      const check = screenFiles(localFiles([file]), constraints);
      setOrder({
        rows: [...order.rows, ...orderRows(check.chosen)],
        refused: [...order.refused.filter((each) => each.key !== key), ...check.refused],
      });
    },
    closeOrder: () => setOrder(null),
    room,
    sendRows: (rows: readonly OrderRow[]) => send(rows),
    sending: sending.length > 0 || upload.isPending || create.isPending,
    queue,
    volumes,
    line,
    replaceFile,
    skip,
    skipping: skipMutation.isPending ? skipMutation.variables?.itemId ?? null : null,
    stopAsked,
    askStop: () => setStopAsked(true),
    keepGoing: () => setStopAsked(false),
    confirmStop,
    stopping: cancel.isPending,
    stopped,
    continueUpload: () => { if (stopped) void send(stopped.files); },
    /** A queue of this saga whose files are all received and that did not start from this tab (R3.2.5): «Начать». */
    idleDraft: queue?.status === 'draft' && !busy.current && !start.isPending ? queue : null,
    startDraft: () => { if (queue?.status === 'draft') void startQueue(queue); },
    latest,
  };
}

export type UploadController = ReturnType<typeof useUpload>;
export type { UploadRoom };
