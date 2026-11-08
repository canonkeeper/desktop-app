import type { ImportQueue, ImportQueueItem as ServerItem } from '../../../entities/manuscript/api';
import { formatCount, pluralize } from '../../../ui/copy';
import { oversizedFileMessage, unsupportedFileMessage, type ImportFileConstraints } from './import-failure';

const volumePattern = /(?:^0*(?<leading>\d+)(?=[\s._-])|(?:том|tom|volume|vol|книга|part)[\s._-]*0*(?<labelled>\d+))/giu;

export type ImportQueueItem = {
  id: string;
  fileName: string;
  sizeBytes: number;
  detectedVolume: number | null;
  volume: number | null;
  file?: File;
  errorCode?: string;
  errorMessage?: string;
  failedStep?: string;
};

export type ImportQueueIssueCode =
  | 'unknown_volume'
  | 'invalid_volume'
  | 'duplicate_volume'
  | 'missing_volume'
  | 'unexpected_volume';

export type ImportQueueIssue = {
  code: ImportQueueIssueCode;
  itemId: string;
  message: string;
};

export type ImportQueueValidation = {
  valid: boolean;
  issues: ImportQueueIssue[];
  message: string | null;
};

export function detectImportVolume(fileName: string): number | null {
  const volumes = new Set<number>();
  for (const match of fileName.matchAll(volumePattern)) {
    const raw = match.groups?.leading ?? match.groups?.labelled;
    const volume = Number(raw);
    if (Number.isSafeInteger(volume) && volume > 0) volumes.add(volume);
  }
  return volumes.size === 1 ? [...volumes][0]! : null;
}

export type RejectedImportFile = Readonly<{ file: File; message: string }>;

/**
 * UC3.1 «Отказ неподдерживаемым файлам до передачи»: a file the server would refuse by its
 * extension or size (`getImportConstraints`) never enters the list and is never transferred; it
 * gets its reason instead. The server still checks every transferred file (R3.1.3).
 */
export function screenImportFiles(files: readonly File[], constraints: ImportFileConstraints) {
  const accepted: File[] = [];
  const rejected: RejectedImportFile[] = [];
  for (const file of files) {
    const message = unsupportedFileMessage(file.name, constraints)
      ?? (file.size > constraints.maxFileSizeBytes ? oversizedFileMessage(constraints) : null);
    if (message) rejected.push({ file, message });
    else accepted.push(file);
  }
  return { accepted, rejected };
}

export function createImportQueueItems(files: readonly File[]): ImportQueueItem[] {
  return files
    .map((file, index) => {
      const detectedVolume = detectImportVolume(file.name);
      return {
        id: `${index}:${file.name}:${file.size}:${file.lastModified}`,
        file,
        fileName: file.name,
        sizeBytes: file.size,
        detectedVolume,
        volume: detectedVolume,
        originalIndex: index,
      };
    })
    .sort((left, right) => {
      if (left.volume === null) return right.volume === null ? left.originalIndex - right.originalIndex : 1;
      if (right.volume === null) return -1;
      return left.volume - right.volume || left.originalIndex - right.originalIndex;
    })
    .map(({ originalIndex: _, ...item }) => item);
}

export function moveImportQueueItem(
  items: readonly ImportQueueItem[],
  fromId: string,
  toId: string,
): ImportQueueItem[] {
  if (fromId === toId) return [...items];
  const fromIndex = items.findIndex((item) => item.id === fromId);
  const toIndex = items.findIndex((item) => item.id === toId);
  if (fromIndex < 0 || toIndex < 0) return [...items];
  const result = [...items];
  const [moving] = result.splice(fromIndex, 1);
  result.splice(toIndex, 0, moving!);
  return result;
}

export function validateImportQueue(
  items: readonly Pick<ImportQueueItem, 'id' | 'volume'>[],
  nextBookOrder: number,
): ImportQueueValidation {
  const issues: ImportQueueIssue[] = [];
  const counts = new Map<number, number>();
  for (const item of items) {
    if (item.volume === null) {
      issues.push({ code: 'unknown_volume', itemId: item.id, message: 'Укажите номер книги в серии.' });
      continue;
    }
    if (!Number.isInteger(item.volume) || item.volume < 1) {
      issues.push({ code: 'invalid_volume', itemId: item.id, message: 'Номер книги — целое число от 1.' });
      continue;
    }
    counts.set(item.volume, (counts.get(item.volume) ?? 0) + 1);
  }
  if (issues.length > 0) return invalid(issues);

  for (const item of items) {
    if (item.volume !== null && counts.get(item.volume)! > 1) {
      issues.push({ code: 'duplicate_volume', itemId: item.id, message: `Номер ${item.volume} уже у другой книги.` });
    }
  }
  if (issues.length > 0) return invalid(issues);

  for (const [index, item] of items.entries()) {
    const expected = nextBookOrder + index;
    if (item.volume === expected) continue;
    issues.push(item.volume! > expected
      ? { code: 'missing_volume', itemId: item.id, message: `Не хватает книги №${expected}: номера должны идти подряд.` }
      : { code: 'unexpected_volume', itemId: item.id, message: `Книга №${item.volume} уже есть в саге — здесь нужен номер ${expected}.` });
    break;
  }
  return issues.length > 0 ? invalid(issues) : { valid: true, issues: [], message: null };
}

export function formatImportSagaGuard(nextBookOrder: number): string {
  if (nextBookOrder === 1) return 'Порядок книг нужен для хронологии саги. В саге пока нет книг — первая получит номер 1.';
  if (nextBookOrder === 2) return 'В саге уже есть книга 1. Следующая получит номер 2, номера идут подряд.';
  return `В саге уже есть книги 1–${nextBookOrder - 1}. Следующая получит номер ${nextBookOrder}, номера идут подряд.`;
}

// Import publishes the text at once, so the queue reports added books, not drafts.
export function formatImportedBookCount(count: number): string {
  return `${formatCount(count, 'книга', 'книги', 'книг')} ${pluralize(count, 'добавлена и подготовлена', 'добавлены и подготовлены', 'добавлены и подготовлены')}.`;
}

function invalid(issues: ImportQueueIssue[]): ImportQueueValidation {
  return { valid: false, issues, message: issues[0]?.message ?? null };
}

export type LocalQueueFile = {
  key: string;
  file: File;
  uploadedBytes: number;
  totalBytes: number | null;
  state: 'local' | 'uploading' | 'uploaded' | 'failed';
};

export type ImportQueueStatus = 'idle' | 'uploading' | 'editing' | 'processing' | 'failed' | 'blocked' | 'complete' | 'canceled';

/** Where the shown book number came from (R3.2.4). */
export type BookNumberSource = 'file' | 'order' | 'manual';

export type ImportQueueRow = Readonly<{
  id: string;
  fileName: string;
  sizeBytes: number;
  detectedVolume: number | null;
  volume: number | null;
  volumeSource: BookNumberSource;
  file?: File;
  uploadedBytes: number;
  totalBytes: number | null;
  transferState: LocalQueueFile['state'] | null;
  bookId?: string;
  errorCode?: string;
  errorMessage?: string;
  failedStep?: string;
  jobId?: string;
  /** The support code of a failed import Job (UC4.2). */
  supportCode?: string;
  locked: boolean;
  status: 'pending' | 'uploading' | 'importing' | 'indexing' | 'reconciling' | 'canceling' | 'succeeded' | 'failed' | 'canceled';
}>;

export function stageFiles(files: readonly File[]): LocalQueueFile[] {
  return createImportQueueItems(files).map((item) => ({
    key: item.id,
    file: item.file!,
    uploadedBytes: 0,
    totalBytes: null,
    state: 'local',
  }));
}

export function projectedStatus(queue: ImportQueue | null, localFiles: readonly LocalQueueFile[], error: string | null): ImportQueueStatus {
  if (!queue) {
    if (localFiles.length > 0) return localFiles.some((file) => file.state !== 'local') ? 'uploading' : 'editing';
    return error ? 'failed' : 'idle';
  }
  if (queue.status === 'uploading') return 'uploading';
  if (queue.status === 'draft') return 'editing';
  if (queue.status === 'running') return 'processing';
  if (queue.status === 'failed') return 'failed';
  if (queue.status === 'succeeded') return 'complete';
  return 'canceled';
}

/**
 * UC3.2 «Номер книги в серии»: a book takes the number from its file name; when no selected file
 * names one, the files are numbered in list order from `nextBookOrder`, so moving a row renumbers
 * it. A number the author typed (a form draft until «Загрузить») wins over both.
 */
export function numberLocalRows(
  files: readonly LocalQueueFile[],
  nextBookOrder: number,
  drafts: Readonly<Record<string, number | null>>,
): ImportQueueRow[] {
  const named = files.some((local) => detectImportVolume(local.file.name) !== null);
  return files.map((local, index) => {
    if (Object.hasOwn(drafts, local.key)) return localRow(local, drafts[local.key]!, 'manual');
    return named
      ? localRow(local, detectImportVolume(local.file.name), 'file')
      : localRow(local, nextBookOrder + index, 'order');
  });
}

function localRow(local: LocalQueueFile, volume: number | null, volumeSource: BookNumberSource): ImportQueueRow {
  return {
    id: local.key,
    fileName: local.file.name,
    sizeBytes: local.file.size,
    detectedVolume: detectImportVolume(local.file.name),
    volume,
    volumeSource,
    file: local.file,
    uploadedBytes: local.uploadedBytes,
    totalBytes: local.totalBytes,
    transferState: local.state,
    locked: false,
    status: local.state === 'failed' ? 'failed' : local.state === 'uploading' ? 'uploading' : 'pending',
  };
}

export function serverRow(item: ServerItem, current: boolean, queue: ImportQueue, localFiles: readonly LocalQueueFile[]): ImportQueueRow {
  const local = queue.status === 'uploading' || queue.status === 'draft'
    ? localFiles.find((candidate) => candidate.key === item.clientItemId)
    : undefined;
  const processing = item.processingStatus;
  const status: ImportQueueRow['status'] = item.preflightStatus === 'invalid' || item.preflightError
    ? 'failed'
    : local?.state === 'failed'
      ? 'failed'
      : local?.state === 'uploading'
        ? 'uploading'
        // A file waits for its transfer from this tab; after a reload nothing transfers it.
        : queue.status === 'uploading'
          ? 'pending'
          : item.preflightStatus === 'uploading' || processing === 'uploading'
            ? 'uploading'
            : processing === 'ready' || (processing === 'queued' && !current)
              ? 'pending'
              : processing === 'importing' || processing === 'queued'
                ? 'importing'
                : processing === 'retrying' ? 'reconciling' : processing;
  return {
    id: item.id,
    fileName: item.sourceFileName,
    sizeBytes: item.sizeBytes ?? local?.file.size ?? 0,
    detectedVolume: item.detectedBookOrder,
    volume: item.confirmedBookOrder ?? item.detectedBookOrder,
    volumeSource: item.confirmedBookOrder !== null && item.confirmedBookOrder !== item.detectedBookOrder
      ? 'manual'
      : detectImportVolume(item.sourceFileName) === item.detectedBookOrder ? 'file' : 'order',
    file: queue.status === 'uploading' || queue.status === 'draft' ? local?.file : undefined,
    uploadedBytes: local?.uploadedBytes ?? 0,
    totalBytes: local?.totalBytes ?? null,
    transferState: local?.state ?? (queue.status === 'uploading' && item.preflightStatus === 'ready' ? 'uploaded' : null),
    bookId: item.bookId ?? undefined,
    errorCode: item.preflightError?.code,
    errorMessage: item.preflightError?.message,
    failedStep: item.failedStep ?? undefined,
    jobId: item.importJobId ?? undefined,
    locked: queue.status !== 'uploading' && queue.status !== 'draft',
    status,
  };
}

export type PendingUpload = { itemId: string; local: LocalQueueFile };

/**
 * The files still to transfer into a queue: an item the server has not received yet takes the
 * selected file of its client id, or else of its name (a reselection after a reload, R3.5.2).
 */
export function pendingUploads(items: readonly ServerItem[], files: readonly LocalQueueFile[]) {
  const unused = new Set(files);
  const uploads: PendingUpload[] = [];
  for (const item of items) {
    if (item.preflightStatus !== 'uploading') continue;
    const local = [...unused].find((candidate) => candidate.key === item.clientItemId)
      ?? [...unused].find((candidate) => candidate.file.name === item.sourceFileName);
    if (!local) continue;
    unused.delete(local);
    uploads.push({ itemId: item.id, local });
  }
  return uploads;
}

export function moveLocal(files: readonly LocalQueueFile[], fromId: string, toId: string) {
  const fromIndex = files.findIndex((file) => file.key === fromId);
  const toIndex = files.findIndex((file) => file.key === toId);
  if (fromIndex < 0 || toIndex < 0 || fromIndex === toIndex) return [...files];
  const moved = [...files];
  const [file] = moved.splice(fromIndex, 1);
  moved.splice(toIndex, 0, file!);
  return moved;
}

export const restorableQueueStatuses: ReadonlySet<ImportQueue['status']> = new Set(['uploading', 'draft', 'running', 'failed']);

// Only the newest queue can still be acted on: an older failed queue is history once a newer
// queue exists, for example a later import of the same volume.
export function latestRestorableQueue(queues: readonly ImportQueue[] | null, dismissed: ReadonlySet<string>) {
  const newest = queues?.slice().sort((a, b) => b.updatedAt.localeCompare(a.updatedAt))[0];
  return newest && restorableQueueStatuses.has(newest.status) && !dismissed.has(newest.id) ? newest : null;
}
