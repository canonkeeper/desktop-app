import { ArrowDown, ArrowUp, FileText, GripVertical, Trash2, Upload } from 'lucide-react';
import type { Book } from '../../../entities/manuscript/api';
import { formatFileSize } from '../../../ui/copy';
import { Button } from '../../../ui/controls/Button';
import { Badge } from '../../../ui/feedback/Badge';
import { Progress } from '../../../ui/feedback/Progress';
import { SupportReference } from '../../../ui/feedback/SupportReference';
import { importFailureMessage, transientImportFailureMessage } from './import-failure';
import type { BookNumberSource, ImportQueueIssue, ImportQueueRow } from './import-queue';
import type { useImportQueue } from './useImportQueue';
import styles from './ImportQueueDialog.module.css';

type Controller = ReturnType<typeof useImportQueue>;

const numberSourceLabels: Record<BookNumberSource, string> = {
  file: 'Из имени файла',
  order: 'По порядку',
  manual: 'Указан вручную',
};

export function EditableRow({
  draggedRowId,
  index,
  issues,
  queue,
  row,
}: {
  draggedRowId: { current: string | null };
  index: number;
  issues: ImportQueueIssue[];
  queue: Controller;
  row: ImportQueueRow;
}) {
  const editable = queue.canEdit && !row.locked;
  const issue = issues[0];
  return (
    <li
      aria-label={row.fileName}
      className={`${styles.editableRow}${issue || row.errorCode ? ` ${styles.invalidRow}` : ''}`}
      draggable={editable}
      onDragEnd={() => { draggedRowId.current = null; }}
      onDragOver={(event) => { if (editable && draggedRowId.current) event.preventDefault(); }}
      onDragStart={() => { draggedRowId.current = row.id; }}
      onDrop={(event) => {
        event.preventDefault();
        if (editable && draggedRowId.current) queue.move(draggedRowId.current, row.id);
        draggedRowId.current = null;
      }}
    >
      <div className={styles.rowMain}>
        <GripVertical aria-hidden="true" size={18} />
        <span className={styles.position}>{index + 1}</span>
        <div className={styles.fileInfo}>
          <strong>{row.fileName}</strong>
          <span>{fileExtension(row.fileName)} · {formatFileSize(row.sizeBytes)}</span>
        </div>
        <label className={styles.volumeField}>
          <span>Книга №</span>
          <input
            aria-label={`Номер книги для ${row.fileName}`}
            disabled={!editable}
            min={1}
            onChange={(event) => queue.setVolume(row.id, event.target.value ? Number(event.target.value) : null)}
            type="number"
            value={row.volume ?? ''}
          />
        </label>
        {row.errorCode ? null : (
          <Badge tone={issue ? 'error' : row.volumeSource === 'manual' ? 'neutral' : 'info'}>
            {issueLabel(issue) ?? numberSourceLabels[row.volumeSource]}
          </Badge>
        )}
      </div>
      <div className={styles.rowActions}>
        {row.errorCode
          ? <RowFailure quotaMessage={queue.quotaMessage} row={row} />
          : issue ? <span className={styles.issue}>{issue.message}</span> : <span />}
        <Button aria-label={`Переместить ${row.fileName} выше`} disabled={!editable || index === 0} onClick={() => queue.move(row.id, queue.rows[index - 1]!.id)} size="sm" variant="outline">
          <ArrowUp aria-hidden="true" size={13} />Выше
        </Button>
        <Button aria-label={`Переместить ${row.fileName} ниже`} disabled={!editable || index === queue.rows.length - 1} onClick={() => queue.move(queue.rows[index + 1]!.id, row.id)} size="sm" variant="outline">
          <ArrowDown aria-hidden="true" size={13} />Ниже
        </Button>
        {row.file || (queue.status === 'blocked' && row.errorCode) ? (
          <label className={styles.fileAction}>
            Заменить файл
            <input
              accept=".fb2,.epub,.docx"
              aria-label={`Заменить ${row.fileName}`}
              onChange={(event) => {
                const file = event.target.files?.[0];
                if (file) void queue.replaceFile(row.id, file);
                event.target.value = '';
              }}
              type="file"
            />
          </label>
        ) : null}
        {queue.localSelection ? (
          <Button aria-label={`Удалить ${row.fileName}`} onClick={() => queue.remove(row.id)} size="sm" variant="ghost">
            <Trash2 aria-hidden="true" size={14} />Удалить
          </Button>
        ) : null}
      </div>
    </li>
  );
}

export function StatusRow({
  books,
  onOpenItem,
  quotaMessage,
  row,
}: {
  books: Array<Pick<Book, 'id' | 'title'>>;
  onOpenItem: (row: ImportQueueRow) => void;
  quotaMessage: Controller['quotaMessage'];
  row: ImportQueueRow;
}) {
  const determinate = row.totalBytes !== null && row.totalBytes > 0;
  const title = books.find((book) => book.id === row.bookId)?.title ?? row.fileName;
  return (
    <li aria-label={title} className={`${styles.statusRow}${row.status === 'failed' ? ` ${styles.invalidRow}` : ''}`}>
      <div className={styles.rowMain}>
        {row.status === 'uploading' ? <Upload aria-hidden="true" size={17} /> : <FileText aria-hidden="true" size={17} />}
        <span className={styles.position}>{row.volume ?? '—'}</span>
        <div className={styles.fileInfo}>
          <strong>{title}</strong>
          <span>{row.fileName} · {fileExtension(row.fileName)} · {formatFileSize(row.sizeBytes)}</span>
        </div>
        <Badge tone={rowTone(row)}>{rowLabel(row, determinate)}</Badge>
      </div>
      {row.status === 'uploading' ? (
        <div className={styles.progressLine}>
          <Progress aria-label={`Передача ${row.fileName}`} value={determinate ? row.uploadedBytes / row.totalBytes! : undefined} />
          {determinate ? <span>{formatFileSize(row.uploadedBytes)} / {formatFileSize(row.totalBytes!)}</span> : null}
        </div>
      ) : null}
      <div className={styles.rowDetail}>
        {row.status === 'failed' ? <RowFailure quotaMessage={quotaMessage} row={row} /> : <span>{rowDetail(row)}</span>}
        {row.bookId ? (
          <Button onClick={() => onOpenItem(row)} size="sm" variant="ghost">Подробнее: {title}</Button>
        ) : null}
      </div>
    </li>
  );
}

/**
 * Why a file failed (R3.4.3): the reason and the fix for a file the author repairs or a book that
 * does not fit the writer's quota (quota spec R5.3, R5.5), otherwise a transient failure with its
 * support code.
 */
function RowFailure({ quotaMessage, row }: { quotaMessage: Controller['quotaMessage']; row: ImportQueueRow }) {
  const reason = importFailureMessage(row.errorCode) ?? quotaMessage(row.errorCode);
  if (reason) return <span className={styles.issue}>{reason}</span>;
  return (
    <span className={styles.rowFailure}>
      <span className={styles.issue}>{transientImportFailureMessage}</span>
      <SupportReference code={row.supportCode} />
    </span>
  );
}

function rowLabel(row: ImportQueueRow, determinate: boolean) {
  if (row.transferState === 'uploaded') return 'Передан · проверен';
  if (row.status === 'succeeded') return 'Готов';
  if (row.status === 'uploading') return determinate ? `Передача · ${Math.round(row.uploadedBytes / row.totalBytes! * 100)} %` : 'Передача';
  if (row.status === 'importing') return 'Импорт';
  if (row.status === 'indexing') return 'Подготовка';
  if (row.status === 'reconciling') return 'Сверяем результат';
  if (row.status === 'canceling') return 'Останавливаем';
  if (row.status === 'failed') return 'Ошибка';
  if (row.status === 'canceled') return 'Отменён';
  return 'Ожидает';
}

function rowTone(row: ImportQueueRow) {
  if (row.status === 'succeeded' || row.transferState === 'uploaded') return 'success';
  if (row.status === 'failed') return 'error';
  if (row.status === 'uploading' || row.status === 'indexing' || row.status === 'importing') return 'indexing';
  return 'neutral';
}

function rowDetail(row: ImportQueueRow) {
  if (row.status === 'succeeded') return 'Книга добавлена и подготовлена';
  if (row.status === 'indexing' || row.status === 'importing' || row.status === 'uploading') return 'Текущий файл';
  if (row.status === 'reconciling') return 'Сверяем результат';
  if (row.status === 'canceling') return 'Текущий файл · останавливаем обработку';
  return 'Не начат';
}

function issueLabel(issue: ImportQueueIssue | undefined) {
  if (issue?.code === 'unknown_volume' || issue?.code === 'invalid_volume') return 'Нет номера';
  if (issue?.code === 'duplicate_volume') return 'Повтор номера';
  return null;
}

function fileExtension(fileName: string) {
  return fileName.includes('.') ? fileName.split('.').at(-1)!.toUpperCase() : 'FILE';
}
