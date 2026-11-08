import { CheckCircle2, EyeOff, Info, LoaderCircle, RotateCcw, TriangleAlert, X } from 'lucide-react';
import { useRef, useState } from 'react';
import type { Book } from '../../../entities/manuscript/api';
import { Button } from '../../../ui/controls/Button';
import { Dropzone } from '../../../ui/controls/Dropzone';
import { Alert } from '../../../ui/feedback/Alert';
import { SupportReference } from '../../../ui/feedback/SupportReference';
import { Dialog } from '../../../ui/overlays/Dialog';
import { formatImportedBookCount, formatImportSagaGuard, type ImportQueueRow, type ImportQueueValidation } from './import-queue';
import { EditableRow, StatusRow } from './ImportQueueRows';
import { RejectedImportFiles } from './RejectedImportFiles';
import type { useImportQueue } from './useImportQueue';
import styles from './ImportQueueDialog.module.css';
import layerStyles from './ShelfLayers.module.css';

type Controller = ReturnType<typeof useImportQueue>;

export interface ImportQueueDialogProps {
  books?: Array<Pick<Book, 'id' | 'title'>>;
  onOpenItem: (row: ImportQueueRow) => void;
  queue: Controller;
}

export function ImportQueueDialog({ books = [], onOpenItem, queue }: ImportQueueDialogProps) {
  const draggedRowId = useRef<string | null>(null);
  const [confirmCancel, setConfirmCancel] = useState(false);
  const localSelection = queue.localSelection;
  const creatingQueue = localSelection && queue.operationPending;
  const failedRow = queue.rows.find((row) => row.status === 'failed');
  const completedCount = queue.rows.filter((row) => row.status === 'succeeded').length;
  const transferredCount = queue.rows.filter((row) => row.transferState === 'uploaded').length;
  const issuesFor = (row: ImportQueueRow) => queue.validation.issues.filter((issue) => issue.itemId === row.id);
  const title = stateTitle(queue, failedRow, transferredCount);
  const text = stateText(queue, completedCount, transferredCount);
  const tone = stateTone(queue);
  const close = () => {
    if (localSelection || queue.status === 'complete' || queue.status === 'canceled') queue.dismiss();
    else queue.hide();
  };

  if (!queue.isDialogVisible) return null;

  return (
    <><Dialog surface="content" canDismiss={!creatingQueue && !queue.cancelPending} dismissOnScrim label="Загрузка книг" onDismiss={close} scrimClassName={styles.scrim}>
      <section className={styles.dialog} data-queue-state={queue.status}>
        <header className={styles.header}>
          <div>
            <h2>{queue.status === 'uploading' && !queue.transferInterrupted ? 'Загрузка книг · передаём файлы' : 'Загрузка книг'}</h2>
            <p>FB2, EPUB или DOCX · до 50 МБ на файл</p>
            {queue.uploadsHint ? <p role="status">{queue.uploadsHint}</p> : null}
          </div>
          <button aria-label={localSelection ? 'Закрыть очередь' : 'Скрыть очередь'} className={styles.iconButton} disabled={creatingQueue || queue.cancelPending} onClick={close} type="button">
            <X aria-hidden="true" size={20} />
          </button>
        </header>

        <div className={styles.body}>
          <Alert aria-label={title} className={styles.stateAlert} tone={tone}>
            <span className={styles.stateIcon} aria-hidden="true">{stateIcon(queue, tone)}</span>
            <div>
              <h3>{title}</h3>
              {text ? <p>{text}</p> : null}
              {queue.errorSupportCode ? <SupportReference code={queue.errorSupportCode} /> : null}
            </div>
          </Alert>

          {localSelection ? (
            <Dropzone
              accept=".fb2,.epub,.docx"
              description="Выбор заменит текущий список. Файлы отправятся только после «Загрузить»."
              label="Выбрать другие файлы"
              multiple
              onChange={(event) => queue.openFiles([...(event.target.files ?? [])])}
              onFilesDrop={queue.openFiles}
            />
          ) : null}
          {queue.nextBookOrder !== null ? <p className={styles.saga}>{formatImportSagaGuard(queue.nextBookOrder)}</p> : null}

          <RejectedImportFiles files={queue.rejectedFiles} />

          {queue.status === 'editing' && queue.rows.length > 1 ? (
            <p className={styles.reorderHint}>Перетащите строку за ⋮⋮ или используйте «Выше» и «Ниже».</p>
          ) : null}

          <ol className={styles.list}>
            {queue.rows.map((row, index) => queue.status === 'editing' || (queue.status === 'blocked' && row.errorCode) ? (
              <EditableRow
                draggedRowId={draggedRowId}
                index={index}
                issues={issuesFor(row)}
                key={row.id}
                queue={queue}
                row={row}
              />
            ) : (
              <StatusRow books={books} key={row.id} onOpenItem={onOpenItem} quotaMessage={queue.quotaMessage} row={row} />
            ))}
          </ol>
        </div>

        <QueueFooter onCancelTransfer={() => setConfirmCancel(true)} queue={queue} />
      </section>
    </Dialog>{confirmCancel ? <Dialog surface="content" canDismiss={!queue.cancelPending} label="Отменить передачу?" onDismiss={() => setConfirmCancel(false)} scrimClassName={layerStyles.deleteDialogScrim}>
      <section className={layerStyles.modal}>
        <div aria-hidden="true" className={layerStyles.sheetHandle} />
        <div className={layerStyles.modalHeader}><h2>Отменить передачу?</h2></div>
        <div className={layerStyles.modalBody}>
          <p className={layerStyles.modalCopy}>{queue.status === 'uploading'
            ? 'Передача текущего файла будет прервана, остальные файлы не начнут загружаться. Уже готовые книги останутся на полке.'
            : 'Передача уже завершилась. Будет запрошена отмена очереди; уже готовые книги останутся на полке.'}</p>
        </div>
        <div className={layerStyles.modalActions}>
          <Button disabled={queue.cancelPending} onClick={() => setConfirmCancel(false)}>Назад</Button>
          <Button loading={queue.cancelPending} onClick={async () => {
            await queue.cancel();
            setConfirmCancel(false);
          }} variant="danger">Отменить передачу</Button>
        </div>
      </section>
    </Dialog> : null}</>
  );
}

function QueueFooter({ onCancelTransfer, queue }: { onCancelTransfer: () => void; queue: Controller }) {
  const retry = queue.canRetry && queue.canMutate
    ? <Button onClick={() => void queue.retry()}><RotateCcw aria-hidden="true" size={15} />Повторить</Button>
    : null;
  if (queue.localSelection) return (
    <footer className={styles.footer}>
      <Button disabled={queue.operationPending} onClick={queue.dismiss} variant="outline">Отмена</Button>
      <div className={styles.footerActions}>
        {retry}
        {/* No unchecked path: the list is sent only after the server constraints checked it (R3.1.2). */}
        <Button
          disabled={!queue.canEdit || queue.operationPending || queue.rows.length === 0 || !queue.validation.valid
            || queue.constraintsStatus !== 'ready' || queue.constraintsRefreshing}
          loading={queue.operationPending}
          onClick={() => void queue.beginTransfer()}
          variant="primary"
        >Загрузить</Button>
      </div>
    </footer>
  );
  if (queue.transferInterrupted) return (
    <footer className={styles.footer}>
      <Button disabled={!queue.canMutate} onClick={() => void queue.cancel()} variant="danger">Отменить загрузку</Button>
      <div className={styles.footerActions}>
        {queue.canMutate && queue.constraintsStatus === 'ready' ? (
          <label className={`${styles.replaceQueue} ${styles.chooseFiles}`}>Выбрать файлы<input
            accept=".fb2,.epub,.docx"
            aria-label="Выбрать файлы"
            multiple
            onChange={(event) => {
              const files = [...(event.target.files ?? [])];
              if (files.length) queue.openFiles(files);
              event.target.value = '';
            }}
            type="file"
          /></label>
        ) : null}
        <Button onClick={queue.hide} variant="outline"><EyeOff aria-hidden="true" size={15} />Скрыть</Button>
      </div>
    </footer>
  );
  if (queue.status === 'failed') return (
    <footer className={styles.footer}>
      {queue.canMutate ? (
        <Button disabled={queue.operationPending} onClick={() => void queue.cancel()} variant="danger">Отменить очередь</Button>
      ) : null}
      <div className={styles.footerActions}>
        {queue.canRetry && queue.canMutate ? <Button onClick={() => void queue.retry()} variant="primary"><RotateCcw aria-hidden="true" size={15} />Повторить</Button> : null}
        {queue.canMutate ? <label className={styles.replaceQueue}>Заменить<input aria-label="Заменить очередь" multiple onChange={(event) => {
          const files = [...(event.target.files ?? [])];
          if (files.length) void queue.replaceQueue(files);
          event.target.value = '';
        }} type="file" /></label> : null}
        <Button onClick={queue.dismiss} variant="outline"><EyeOff aria-hidden="true" size={15} />Скрыть</Button>
      </div>
    </footer>
  );
  if (queue.status === 'complete' || queue.status === 'canceled') return (
    <footer className={styles.footer}><Button onClick={queue.dismiss} variant="primary">Закрыть</Button></footer>
  );
  return (
    <footer className={styles.footer}>
      <Button disabled={queue.canCancelTransfer ? false : !queue.canMutate || queue.operationPending} onClick={() => queue.canCancelTransfer ? onCancelTransfer() : void queue.cancel()} variant="danger">{queue.transferStopRequested ? 'Повторить отмену' : queue.status === 'uploading' ? 'Отменить передачу' : 'Отменить очередь'}</Button>
      <div className={styles.footerActions}>
        {queue.status === 'blocked' ? <Button onClick={queue.hide} variant="outline">К моим книгам</Button> : null}
        {retry}
        <Button onClick={queue.hide} variant="outline"><EyeOff aria-hidden="true" size={15} />Скрыть</Button>
      </div>
    </footer>
  );
}

/** A broken number names the fix; a gap in the series names the missing book (R3.2.3). */
function numbersTitle(validation: ImportQueueValidation) {
  const issue = validation.issues[0];
  if (!issue) return null;
  return issue.code === 'missing_volume' || issue.code === 'unexpected_volume' ? 'Не хватает книги в серии' : 'Проверьте номера книг';
}

function stateTitle(queue: Controller, failed: ImportQueueRow | undefined, transferred: number) {
  if (queue.errorMessage) return queue.errorMessage;
  if (queue.localSelection) {
    if (queue.constraintsStatus === 'pending') return 'Проверяем файлы…';
    if (queue.constraintsStatus === 'error') return 'Не удалось проверить файлы';
    return numbersTitle(queue.validation) ?? 'Проверьте порядок книг';
  }
  if (queue.transferInterrupted) return 'Передача прервалась';
  if (queue.status === 'uploading') return `Передаём файлы · ${transferred} из ${queue.rows.length}`;
  if (queue.status === 'editing') return queue.validation.valid ? 'Очередь готова к автозапуску' : numbersTitle(queue.validation) ?? 'Проверьте номера книг';
  if (queue.status === 'blocked') return failed?.errorCode ? 'Файл не прошёл проверку' : 'Не хватает книги в серии';
  if (queue.status === 'processing') return 'Готовим книги по порядку';
  if (queue.status === 'failed') return `Очередь остановлена${failed?.volume ? ` на книге №${failed.volume}` : ''}`;
  if (queue.status === 'complete') return 'Книги загружены';
  return 'Очередь отменена';
}

function stateText(queue: Controller, completed: number, transferred: number) {
  if (queue.localSelection) {
    if (queue.constraintsStatus === 'pending') return '';
    if (queue.constraintsStatus === 'error') return 'Без проверки файлы не отправляются. Повторите.';
    return queue.validation.message ?? 'Файлы ещё не отправлены. Порядок строк — порядок книг в серии.';
  }
  if (queue.transferInterrupted) {
    return `Передано ${transferred} из ${queue.rows.length}. Файлы передаются из этой вкладки, поэтому после перезагрузки выберите оставшиеся файлы снова.`;
  }
  if (queue.status === 'uploading') return 'Не закрывайте вкладку, пока файлы передаются — потом подготовка пойдёт сама.';
  // A refused start (a quota, a lost request) leaves the draft as it was: it does not start by itself.
  if (queue.status === 'editing' && queue.errorMessage) return 'Очередь сохранена и не запущена.';
  if (queue.status === 'editing' && queue.validation.valid) return 'Проверка пройдена — очередь запускается автоматически.';
  if (queue.rows.some((row) => row.errorCode && row.failedStep !== 'import')) return 'Замените файл, который не прошёл предпроверку.';
  if (queue.status === 'processing') return 'Одна книга готовится, остальные ждут. Окно можно скрыть.';
  if (queue.status === 'failed') return `${completed} готово. Повторите текущую книгу, скройте очередь или завершите её заменой.`;
  if (queue.status === 'complete') return formatImportedBookCount(queue.rows.length);
  if (queue.status === 'canceled') return 'Готовые книги остались на полке; остальные не обработаны.';
  return queue.validation.message ?? 'Исправьте номера — очередь запустится автоматически.';
}

function stateTone(queue: Controller) {
  if (queue.errorMessage || queue.status === 'failed' || queue.status === 'blocked') return 'error';
  if (queue.localSelection) {
    if (queue.constraintsStatus === 'error') return 'error';
    return queue.constraintsStatus === 'pending' || queue.validation.valid ? 'info' : 'warning';
  }
  if (queue.transferInterrupted) return 'warning';
  if (queue.status === 'complete') return 'success';
  if (queue.status === 'processing' || queue.status === 'uploading' || queue.validation.valid) return 'info';
  return 'warning';
}

function stateIcon(queue: Controller, tone: ReturnType<typeof stateTone>) {
  if (tone === 'success') return <CheckCircle2 size={18} />;
  const working = queue.status === 'processing' || (queue.status === 'uploading' && !queue.transferInterrupted)
    || (queue.localSelection && queue.constraintsStatus === 'pending');
  if (working && tone === 'info') return <LoaderCircle className={styles.spinner} size={18} />;
  return tone === 'info' ? <Info size={18} /> : <TriangleAlert size={18} />;
}
