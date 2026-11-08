import { ListRestart, Plus, Upload, UploadCloud } from 'lucide-react';
import { useState, type ChangeEventHandler, type DragEventHandler, type ReactNode, type RefObject } from 'react';
import { isApiStatusFailure, publicApiErrorMessage } from '../../../entities/api-errors';
import { Button } from '../../../ui/controls/Button';
import { Alert } from '../../../ui/feedback/Alert';
import { Skeleton } from '../../../ui/feedback/Skeleton';
import { SupportReference } from '../../../ui/feedback/SupportReference';
import styles from './ManuscriptShelfPage.module.css';

export type ShelfLoadStatus = 'loading' | 'ready' | 'empty' | 'error';

interface ShelfHeaderProps {
  canEditManuscript: boolean;
  fileInputRef: RefObject<HTMLInputElement | null>;
  importBlocked: boolean;
  onChooseImportFile: () => void;
  onImportInputChange: ChangeEventHandler<HTMLInputElement>;
  onOpenImportQueue: () => void;
  projectExists: boolean;
  projectMeta: string;
  projectTitle: string;
  showActions?: boolean;
  showOpenImportQueue: boolean;
}

interface ShelfStatusMessagesProps {
  /** The failed shelf request, if the shelf could not load. */
  error: unknown;
  /** The saga has no book and the first steps do not already ask for one. */
  emptySaga: boolean;
  importQueueActive: boolean;
  notice: string;
  onRetry: () => void;
  retrying: boolean;
  status: ShelfLoadStatus;
}

export function ShelfHeader({
  canEditManuscript,
  fileInputRef,
  importBlocked,
  onChooseImportFile,
  onImportInputChange,
  onOpenImportQueue,
  projectExists,
  projectMeta,
  projectTitle,
  showActions = true,
  showOpenImportQueue,
}: ShelfHeaderProps) {
  const importDisabled = !projectExists || !canEditManuscript || importBlocked;
  return (
    <div className={styles.headerRow}>
      <div className={styles.titleBlock}>
        <h1>{projectTitle}</h1>
        {projectMeta ? <p className={styles.projectMeta}>{projectExists ? 'Сага · ' : ''}{projectMeta}</p> : null}
      </div>
      {showActions ? <div className={styles.topActions}>
        {showOpenImportQueue ? (
          <button
            className={styles.uploadButton}
            onClick={onOpenImportQueue}
            type="button"
          >
            <ListRestart aria-hidden="true" size={17} />
            Открыть очередь
          </button>
        ) : null}
        {canEditManuscript ? <>
          <button
            className={`${styles.uploadButton} ${styles.uploadFileButton}`}
            disabled={importDisabled}
            onClick={onChooseImportFile}
            type="button"
          >
            <Upload aria-hidden="true" size={16} />
            <span>Загрузить файл</span>
          </button>
          <input
            accept=".fb2,.epub,.docx"
            aria-label="Файлы книг: FB2, EPUB или DOCX"
            className={styles.fileInput}
            disabled={importDisabled}
            multiple
            onChange={onImportInputChange}
            ref={fileInputRef}
            type="file"
          />
        </> : null}
      </div> : null}
    </div>
  );
}

export function ShelfBooksHeader() {
  return (
    <div className={styles.shelfSectionHeader}>
      <p className={styles.sectionLabel}>Книги саги</p>
    </div>
  );
}

/** Step 1 of the first steps and «Новая сага» of the saga menu: the saga name is a form draft (R2.1.2). */
export function FirstProjectPanel({ creating, heading, onCreate }: {
  creating: boolean;
  heading?: ReactNode;
  onCreate: (title: string) => Promise<boolean>;
}) {
  const [title, setTitle] = useState('');
  return (
    <form
      className={heading ? styles.firstProjectPanel : styles.sagaForm}
      onSubmit={(event) => {
        event.preventDefault();
        void onCreate(title).then((created) => { if (created) setTitle(''); });
      }}
    >
      {heading ? <div>{heading}</div> : null}
      <label className={styles.field}>
        <span>Название саги</span>
        <input
          aria-label="Название саги"
          autoComplete="off"
          onChange={(event) => setTitle(event.target.value)}
          placeholder="Например, Сага северного ветра"
          value={title}
        />
      </label>
      <div className={styles.formActions}>
        <Button loading={creating} type="submit" variant="primary">
          <Plus aria-hidden="true" size={17} />
          {creating ? 'Создаём…' : 'Создать сагу'}
        </Button>
      </div>
    </form>
  );
}

export function ShelfStatusMessages({
  error,
  emptySaga,
  importQueueActive,
  notice,
  onRetry,
  retrying,
  status,
}: ShelfStatusMessagesProps) {
  return (
    <>
      {importQueueActive ? <p className={styles.notice}>Книги появляются на полке по мере готовности</p> : null}
      {status === 'loading' ? (
        <div aria-label="Загружаем книги" className={styles.shelfSkeleton} role="status">
          <Skeleton className={styles.shelfSkeletonLine} />
          <Skeleton className={styles.shelfSkeletonLine} />
          <Skeleton className={styles.shelfSkeletonLine} />
        </div>
      ) : null}
      {status === 'empty' && emptySaga ? (
        <div className={styles.notice}>
          <strong className={styles.noticeTitle}>В саге пока нет книг</strong>
          <span>Загрузите первую книгу — через пару минут по ней можно задавать вопросы.</span>
        </div>
      ) : null}
      {status === 'error' ? (
        <Alert className={styles.loadError} tone="error">
          <strong>Не удалось загрузить книги</strong>
          <span>{publicApiErrorMessage(error, 'Попробуйте ещё раз.')}</span>
          <Button loading={retrying} onClick={onRetry} size="sm">Повторить</Button>
          <SupportReference code={isApiStatusFailure(error) ? error.problem.requestId : null} />
        </Alert>
      ) : null}
      {notice ? <p className={styles.notice} role="status">{notice}</p> : null}
    </>
  );
}

export function ProjectMissingState({
  onOpenChat,
  onOpenLibrary,
}: {
  onOpenChat: () => void;
  onOpenLibrary: () => void;
}) {
  return (
    <Alert className={styles.projectMissing} tone="error">
      <div>
        <h2>Сага не найдена</h2>
        <p>Сага недоступна или вы перешли по старой ссылке.</p>
      </div>
      <div className={styles.projectMissingActions}>
        <Button onClick={onOpenLibrary} variant="primary">Мои книги</Button>
        <Button onClick={onOpenChat} variant="outline">Открыть чат</Button>
      </div>
    </Alert>
  );
}

export function ShelfImportDropzone({
  active,
  disabled,
  onChooseFile,
  onDrop,
  onSetActive,
}: {
  active: boolean;
  disabled: boolean;
  onChooseFile: () => void;
  onDrop: DragEventHandler<HTMLButtonElement>;
  onSetActive: (active: boolean) => void;
}) {
  return (
    <button
      className={active ? `${styles.dropZone} ${styles.dropZoneActive}` : styles.dropZone}
      disabled={disabled}
      onClick={onChooseFile}
      onDragLeave={() => onSetActive(false)}
      onDragOver={(event) => {
        event.preventDefault();
        onSetActive(true);
      }}
      onDrop={onDrop}
      type="button"
    >
      <span>
        <UploadCloud aria-hidden="true" size={30} />
        <strong className={styles.desktopDropLabel}>Перетащите FB2, EPUB или DOCX</strong>
        <strong className={styles.mobileDropLabel}>Загрузить FB2, EPUB или DOCX</strong>
        <span className={styles.muted}>Разделим на главы и подготовим к вопросам · до 50 МБ</span>
      </span>
    </button>
  );
}
