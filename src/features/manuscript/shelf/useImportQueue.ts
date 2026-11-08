import { useEffect, useMemo, useRef, useState } from 'react';
import { quotaFailureCode, useAccountQuota } from '../../../entities/account-quota/api';
import { isApiStatusFailure, isTransientApiFailure, publicApiErrorMessage } from '../../../entities/api-errors';
import type { ImportQueue, ManuscriptApiClient } from '../../../entities/manuscript/api';
import { createIdempotencyKey } from '../../../entities/manuscript/api';
import {
  useCancelImportQueueMutation,
  useCreateImportQueueMutation,
  useImportQueueQuery,
  useImportQueuesQuery,
  useManuscriptJobQuery,
  useRestartIndexingMutation,
  useSaveImportQueueMutation,
  useStartImportQueueMutation,
  useUploadImportQueueItemMutation,
} from '../api';
import { isAuthorFixableImportFailure } from './import-failure';
import { useImportScreening } from './useImportScreening';
import {
  latestRestorableQueue,
  moveImportQueueItem,
  moveLocal,
  numberLocalRows,
  pendingUploads,
  projectedStatus,
  restorableQueueStatuses,
  screenImportFiles,
  serverRow,
  stageFiles,
  type ImportQueueRow,
  type ImportQueueValidation,
  type LocalQueueFile,
  type PendingUpload,
  validateImportQueue,
} from './import-queue';
import { sourceQuotaMessage, uploadsQuotaHint } from './quota-messages';

interface UseImportQueueOptions {
  api: ManuscriptApiClient;
  projectId: string | null;
  canEditManuscript?: boolean;
}

const emptyValidation: ImportQueueValidation = { valid: false, issues: [], message: null };
type QueueFailure = { message: string; supportCode: string | null };

export function useImportQueue({ api, projectId, canEditManuscript = true }: UseImportQueueOptions) {
  const [localFiles, setLocalFiles] = useState<LocalQueueFile[]>([]);
  const [isDialogVisible, setDialogVisible] = useState(true);
  const [failure, setFailure] = useState<QueueFailure | null>(null);
  const errorMessage = failure?.message ?? null;
  const [selectedQueueId, setSelectedQueueId] = useState<string | null>(null);
  const [dismissedQueueIds, setDismissedQueueIds] = useState<Set<string>>(() => new Set());
  const [volumeDrafts, setVolumeDrafts] = useState<Record<string, number | null>>({});
  const createKeyRef = useRef<string | null>(null);
  const uploadKeysRef = useRef(new Map<string, string>());
  const startKeysRef = useRef(new Map<string, string>());
  const startInFlightRef = useRef(false);
  const transferRef = useRef<{ projectId: string; queueId: string; controller: AbortController; settled: Promise<void> } | null>(null);
  const canceledTransferQueueRef = useRef<string | null>(null);
  const cancelInFlightRef = useRef(false);
  const [cancelPending, setCancelPending] = useState(false);

  const createMutation = useCreateImportQueueMutation(api);
  const uploadMutation = useUploadImportQueueItemMutation(api);
  const saveMutation = useSaveImportQueueMutation(api);
  const startMutation = useStartImportQueueMutation(api);
  const cancelMutation = useCancelImportQueueMutation(api);
  const retryIndexingMutation = useRestartIndexingMutation(api);
  const queuesQuery = useImportQueuesQuery(api, projectId ?? '');
  const screening = useImportScreening(api, projectId, canEditManuscript, localFiles);
  const { acceptedFiles, constraints } = screening;
  const quota = useAccountQuota(api, Boolean(projectId)).data;
  const listedQueues = queuesQuery.data?.data ?? null;
  const listedQueue = useMemo(
    () => latestRestorableQueue(listedQueues, dismissedQueueIds),
    [dismissedQueueIds, listedQueues],
  );
  const detailQuery = useImportQueueQuery(api, projectId ?? '', selectedQueueId ?? listedQueue?.id ?? '');
  const selectedListedQueue = selectedQueueId
    ? listedQueues?.find((candidate) => candidate.id === selectedQueueId)
    : undefined;
  const queue = localFiles.length > 0 && !selectedQueueId
    ? null
    : selectedQueueId
      ? selectedListedQueue ?? (detailQuery.data?.id === selectedQueueId ? detailQuery.data : null)
      : listedQueue;
  // The reason of a failed import is the error of its Job, such as a book over the limits.
  const failedImportJobId = queue?.items.find((item) => item.processingStatus === 'failed')?.importJobId ?? '';
  const failedImportError = useManuscriptJobQuery(api, failedImportJobId, Boolean(failedImportJobId)).data?.error;
  const rows = useMemo(
    () => queue
      ? queue.items.slice().sort((a, b) => a.position - b.position)
        .map((item) => {
          let row = serverRow(item, item.id === queue.currentItemId, queue, localFiles);
          if (failedImportError && item.importJobId === failedImportJobId) {
            row = { ...row, errorCode: row.errorCode ?? failedImportError.code, supportCode: failedImportError.requestId };
          }
          return Object.hasOwn(volumeDrafts, item.id) ? { ...row, volume: volumeDrafts[item.id]!, volumeSource: 'manual' as const } : row;
        })
      : constraints ? numberLocalRows(acceptedFiles, constraints.nextBookOrder, volumeDrafts) : [],
    [acceptedFiles, constraints, failedImportError, failedImportJobId, localFiles, queue, volumeDrafts],
  );
  const localSelection = !queue && localFiles.length > 0 && localFiles.every((local) => local.state === 'local');
  // The local list follows the same sequence rule the server checks on save and start (R3.2.3).
  const validation = useMemo(
    () => queue?.status === 'draft'
      ? validateImportQueue(rows, queue.nextBookOrder)
      : localSelection && constraints && rows.length > 0 ? validateImportQueue(rows, constraints.nextBookOrder) : emptyValidation,
    [constraints, localSelection, queue, rows],
  );
  const transferStopRequested = queue !== null && canceledTransferQueueRef.current === queue.id;
  const currentTransfer = transferRef.current?.projectId === projectId && transferRef.current?.queueId === queue?.id
    ? transferRef.current
    : null;
  const operationPending = cancelPending || transferStopRequested || createMutation.isPending || uploadMutation.isPending
    || saveMutation.isPending || startMutation.isPending || cancelMutation.isPending
    || retryIndexingMutation.isPending;
  const sequenceBlocked = queue?.status === 'draft'
    && validation.issues.some((issue) => issue.code === 'missing_volume' || issue.code === 'unexpected_volume');
  const preflightBlocked = queue?.status === 'uploading'
    && queue.items.some((item) => item.preflightStatus === 'invalid' || item.preflightError);
  const blocked = sequenceBlocked || preflightBlocked;
  const status = blocked ? 'blocked' : projectedStatus(queue, localFiles, errorMessage);
  const canReopen = !isDialogVisible && Boolean(queue && restorableQueueStatuses.has(queue.status));
  // R3.5.1: files transfer only from the tab that holds them, so after a reload an uploading queue
  // waits for the author to choose the files again.
  const transferInterrupted = queue?.status === 'uploading' && !preflightBlocked && acceptedFiles.length === 0
    && !currentTransfer && !transferStopRequested;
  // R3.4.3: a file the author has to fix, or a book over the writer's quota, is replaced, not retried.
  const authorFixableFailure = queue?.status === 'failed' && rows.some((row) => row.status === 'failed'
    && (isAuthorFixableImportFailure(row.errorCode) || sourceQuotaMessage(row.errorCode, quota) !== undefined));

  useEffect(() => {
    if (queue && canceledTransferQueueRef.current === queue.id
      && ['succeeded', 'failed', 'canceled', 'expired'].includes(queue.status)) {
      canceledTransferQueueRef.current = null;
      setLocalFiles([]);
    }
  }, [queue]);

  useEffect(() => {
    setLocalFiles([]);
    setSelectedQueueId(null);
    setDismissedQueueIds(new Set());
    setVolumeDrafts({});
    setFailure(null);
    setDialogVisible(true);
    createKeyRef.current = null;
    uploadKeysRef.current.clear();
    startKeysRef.current.clear();
  }, [projectId]);

  function openFiles(files: readonly File[]) {
    if (!canEditManuscript || !projectId || transferStopRequested || files.length === 0 || (queue && queue.status !== 'uploading')) return;
    const staged = stageFiles(files);
    setLocalFiles(staged);
    setVolumeDrafts({});
    setFailure(null);
    setDialogVisible(true);
    if (queue?.status === 'uploading') {
      setSelectedQueueId(queue.id);
      // R3.5.2: the chosen files continue the interrupted transfer at once.
      const accepted = constraints ? screenImportFiles(files, constraints).accepted : [];
      void uploadFiles(queue, pendingUploads(queue.items, staged.filter((local) => accepted.includes(local.file))));
      return;
    }
    // Another import may have added books since the shelf opened: number from the current state.
    void screening.refetch();
  }

  async function beginTransfer() {
    if (!canEditManuscript || !projectId || acceptedFiles.length === 0 || operationPending) return;
    if (queue?.status === 'uploading') {
      await uploadFiles(queue, pendingUploads(queue.items, acceptedFiles));
      return;
    }
    if (selectedQueueId || screening.refreshing || !validation.valid) return;
    setFailure(null);
    createKeyRef.current ??= createIdempotencyKey('createImportQueue');
    try {
      const created = await createMutation.mutateAsync({
        projectId,
        idempotencyKey: createKeyRef.current,
        // R3.2.2: the shown numbers, typed ones included, become the detected book orders.
        body: { items: rows.map((row, index) => ({
          clientItemId: row.id,
          position: index + 1,
          sourceFileName: row.fileName,
          detectedBookOrder: row.volume,
        })) },
      });
      createKeyRef.current = null;
      setSelectedQueueId(created.id);
      setVolumeDrafts({});
      await uploadFiles(created, pendingUploads(created.items, acceptedFiles));
    } catch (error) {
      fail(error, isTransientApiFailure(error)
        ? 'Не удалось подтвердить состояние очереди. Повторите операцию.'
        : 'Не удалось создать очередь импорта.');
    }
  }

  async function uploadFiles(target: ImportQueue, uploads: readonly PendingUpload[]) {
    if (!projectId || transferRef.current || canceledTransferQueueRef.current === target.id) return;
    const controller = new AbortController();
    let settle!: () => void;
    const transfer = { projectId, queueId: target.id, controller, settled: new Promise<void>((resolve) => { settle = resolve; }) };
    transferRef.current = transfer;
    try {
      let current = target;
      let failed = false;
      for (const { itemId, local } of uploads) {
        if (controller.signal.aborted) return;
        const key = uploadKeysRef.current.get(itemId) ?? createIdempotencyKey('uploadImportQueueItem');
        uploadKeysRef.current.set(itemId, key);
        updateLocal(local.key, { state: 'uploading', uploadedBytes: 0, totalBytes: null });
        try {
          current = await uploadMutation.mutateAsync({
            projectId,
            queueId: current.id,
            itemId,
            file: local.file,
            idempotencyKey: key,
            signal: controller.signal,
            onUploadProgress: ({ loadedBytes, totalBytes }) => {
              if (!controller.signal.aborted) updateLocal(local.key, {
                uploadedBytes: loadedBytes, totalBytes, state: 'uploading',
              });
            },
          });
          if (controller.signal.aborted) return;
          updateLocal(local.key, { state: 'uploaded' });
        } catch (error) {
          if (controller.signal.aborted) return;
          failed = true;
          updateLocal(local.key, { state: 'failed' });
          fail(error, isTransientApiFailure(error)
            ? 'Не удалось подтвердить загрузку файла. Повторите передачу.'
            : 'Не удалось загрузить файл.');
        }
      }
      if (!controller.signal.aborted && !failed && current.status === 'draft') await startIfValid(current);
    } finally {
      transferRef.current = null;
      settle();
    }
  }

  // The server starts only confirmed volume numbers, so the numbers shown from file names are
  // confirmed first; saving them starts the queue.
  async function startIfValid(target: ImportQueue, confirm = true) {
    const targetRows = target.items.slice().sort((a, b) => a.position - b.position)
      .map((item) => serverRow(item, false, target, localFiles));
    if (!validateImportQueue(targetRows, target.nextBookOrder).valid) return;
    if (confirm && target.items.some((item) => item.confirmedBookOrder === null)) await saveDraft(target, targetRows);
    else await startQueue(target);
  }

  async function startQueue(target: ImportQueue) {
    if (!projectId || target.status !== 'draft' || startInFlightRef.current || canceledTransferQueueRef.current === target.id) return;
    startInFlightRef.current = true;
    const key = startKeysRef.current.get(target.id) ?? createIdempotencyKey('startImportQueue');
    startKeysRef.current.set(target.id, key);
    try {
      const started = await startMutation.mutateAsync({
        projectId,
        queueId: target.id,
        idempotencyKey: key,
        body: { expectedVersion: target.version },
      });
      if (canceledTransferQueueRef.current === target.id) return;
      setSelectedQueueId(started.id);
      setLocalFiles([]);
      setFailure(null);
    } catch (error) {
      const quotaReason = sourceQuotaMessage(quotaFailureCode(error), quota, { volumes: target.items.length });
      if (quotaReason) setFailure({ message: quotaReason, supportCode: null });
      else fail(error, 'Не удалось начать импорт.');
    } finally {
      startInFlightRef.current = false;
    }
  }

  function move(fromId: string, toId: string) {
    if (!canEditManuscript || operationPending) return;
    if (!queue) {
      setLocalFiles((current) => moveLocal(current, fromId, toId));
    } else if (queue.status === 'draft') {
      void saveDraft(queue, moveImportQueueItem(rows, fromId, toId) as ImportQueueRow[]);
    }
  }

  function remove(id: string) {
    if (canEditManuscript && !queue && !operationPending) setLocalFiles((current) => current.filter((item) => item.key !== id));
  }

  async function replaceFile(id: string, file: File) {
    if (!canEditManuscript || operationPending) return;
    if (!queue) {
      setLocalFiles((current) => current.map((item) => item.key === id
        ? { key: item.key, file, uploadedBytes: 0, totalBytes: null, state: 'local' }
        : item));
      return;
    }
    if (queue.status !== 'draft' && queue.status !== 'uploading') return;
    const item = queue.items.find((candidate) => candidate.id === id);
    if (!item) return;
    // UC3.1: the replacement passes the same check as any chosen file.
    const rejected = constraints ? screenImportFiles([file], constraints).rejected[0] : undefined;
    if (!constraints || rejected) {
      setFailure({ message: rejected?.message ?? 'Не удалось проверить файл. Повторите позже.', supportCode: null });
      return;
    }
    const local = { key: item.clientItemId, file, uploadedBytes: 0, totalBytes: null, state: 'local' } satisfies LocalQueueFile;
    uploadKeysRef.current.delete(item.id);
    setSelectedQueueId(queue.id);
    setFailure(null);
    setLocalFiles((current) => [...current.filter((candidate) => candidate.key !== local.key), local]);
    await uploadFiles(queue, [{ itemId: item.id, local }]);
  }

  function setVolume(id: string, volume: number | null) {
    if (!canEditManuscript || operationPending) return;
    if (!queue) {
      // R3.2.2: a typed number stays a form draft until «Загрузить».
      if (localSelection) setVolumeDrafts((current) => ({ ...current, [id]: volume }));
      return;
    }
    if (queue.status !== 'draft') return;
    setVolumeDrafts((current) => ({ ...current, [id]: volume }));
    const nextRows = rows.map((row) => row.id === id ? { ...row, volume } : row);
    if (nextRows.every((row) => row.volume !== null)) void saveDraft(queue, nextRows);
  }

  async function saveDraft(target: ImportQueue, nextRows: readonly ImportQueueRow[]) {
    if (!projectId || nextRows.some((row) => row.volume === null)) return;
    try {
      const saved = await saveMutation.mutateAsync({
        projectId,
        queueId: target.id,
        body: {
          expectedVersion: target.version,
          items: nextRows.map((row, index) => ({ id: row.id, position: index + 1, confirmedBookOrder: row.volume! })),
        },
      });
      setVolumeDrafts({});
      setFailure(null);
      await startIfValid(saved, false);
    } catch (error) {
      fail(error, 'Не удалось сохранить очередь.');
    }
  }

  async function retry() {
    if (!canEditManuscript) return;
    setFailure(null);
    if (!projectId) return;
    if (!queue) {
      if (!constraints) {
        await screening.refetch();
        return;
      }
      return beginTransfer();
    }
    if (queue.status === 'failed') {
      const failed = rows.find((row) => row.status === 'failed' && row.bookId);
      if (!failed?.bookId) {
        setFailure({ message: 'Для этого этапа повторный запуск недоступен. Замените файл.', supportCode: null });
        return;
      }
      try {
        await retryIndexingMutation.mutateAsync({ projectId, sourceView: 'draft' });
        await queuesQuery.refetch();
      } catch (error) {
        fail(error, 'Не удалось повторить обработку.');
      }
      return;
    }
    const failedFiles = localFiles.filter((file) => file.state === 'failed');
    if (failedFiles.length > 0) await uploadFiles(queue, pendingUploads(queue.items, failedFiles));
    else if (queue.status === 'draft') await startIfValid(queue);
    else await detailQuery.refetch();
  }

  async function replaceQueue(files: readonly File[]) {
    if (!canEditManuscript || !queue || queue.status !== 'failed' || files.length === 0 || operationPending) return;
    const id = queue.id;
    if (!await cancel()) return;
    dismissQueue(id);
    setLocalFiles(stageFiles(files));
    setFailure(null);
    setDialogVisible(true);
    void screening.refetch();
  }

  async function cancel() {
    if (!canEditManuscript || !projectId || cancelInFlightRef.current) return false;
    const transfer = currentTransfer;
    if (operationPending && !transfer && !transferStopRequested) return false;
    if (!queue) {
      setLocalFiles([]);
      setDialogVisible(false);
      return true;
    }
    if (!restorableQueueStatuses.has(queue.status)) return false;
    cancelInFlightRef.current = true;
    setCancelPending(true);
    if (transfer || queue.status === 'uploading') canceledTransferQueueRef.current = queue.id;
    try {
      transfer?.controller.abort();
      await transfer?.settled;
      const result = await cancelMutation.mutateAsync({ projectId, queueId: queue.id });
      if (result.status !== 'canceled') {
        setFailure({
          message: result.status === 'succeeded'
            ? 'Очередь уже завершена. Отмена не выполнена.'
            : result.status === 'failed'
              ? 'Очередь завершилась с ошибкой. Отмена не выполнена.'
              : 'Отмена ещё не подтверждена. Повторите отмену очереди.',
          supportCode: null,
        });
        return false;
      }
      createKeyRef.current = null;
      setFailure(null);
      return true;
    } catch (error) {
      fail(error, 'Не удалось отменить очередь.');
      return false;
    } finally {
      cancelInFlightRef.current = false;
      setCancelPending(false);
    }
  }

  function dismissQueue(id: string) {
    setDismissedQueueIds((current) => new Set(current).add(id));
    setSelectedQueueId(null);
    setLocalFiles([]);
    setDialogVisible(false);
  }

  function dismiss() {
    if (queue && ['failed', 'succeeded', 'canceled'].includes(queue.status)) dismissQueue(queue.id);
    else if (!queue) {
      setLocalFiles([]);
      setDialogVisible(false);
    } else setDialogVisible(false);
  }

  // A transient failure carries the code support finds it by (UC4.2, R3.4.3).
  function fail(error: unknown, fallback: string) {
    setFailure({
      message: publicApiErrorMessage(error, fallback),
      supportCode: isTransientApiFailure(error) && isApiStatusFailure(error) ? error.problem.requestId ?? null : null,
    });
  }

  function updateLocal(key: string, update: Partial<LocalQueueFile>) {
    setLocalFiles((current) => current.map((item) => item.key === key ? { ...item, ...update } : item));
  }

  return {
    canCancelTransfer: canEditManuscript && !cancelPending && Boolean(queue
      && (queue.status === 'uploading' || transferStopRequested)
      && (!operationPending || currentTransfer || transferStopRequested)),
    cancelPending,
    transferStopRequested,
    beginTransfer,
    canEdit: canEditManuscript && !operationPending && !blocked && (!queue || queue.status === 'draft'),
    canMutate: canEditManuscript && !operationPending,
    canRetry: !operationPending && (Boolean(errorMessage)
      || (queue?.status === 'failed' && !authorFixableFailure)
      || (localSelection && screening.status === 'error')),
    canReopen,
    cancel,
    constraintsRefreshing: screening.refreshing,
    constraintsStatus: screening.status,
    dismiss,
    errorMessage,
    errorSupportCode: failure?.supportCode ?? null,
    hide: () => setDialogVisible(false),
    isDialogVisible,
    // A selection with no file the server takes opens no dialog: its reasons stay on the shelf.
    isOpen: queue !== null || acceptedFiles.length > 0 || (localFiles.length > 0 && !screening.screened),
    localSelection,
    move,
    nextBookOrder: queue?.nextBookOrder ?? constraints?.nextBookOrder ?? null,
    openFiles,
    operationPending,
    quotaMessage: (code: string | null | undefined) => sourceQuotaMessage(code, quota),
    rejectedFiles: screening.rejectedFiles,
    remove,
    replaceFile,
    replaceQueue,
    retry,
    rows,
    show: () => setDialogVisible(true),
    setVolume,
    status,
    transferInterrupted,
    uploadsHint: uploadsQuotaHint(quota),
    validation,
  };
}
