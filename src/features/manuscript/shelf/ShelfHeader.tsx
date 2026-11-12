import { ErrorBlock } from '../../../ui/feedback/ErrorBlock';
import { ArchiveRestore, BookOpen, Library, Plus, Upload } from 'lucide-react';
import { useRef, useState, type ChangeEventHandler, type ReactNode, type RefObject } from 'react';
import type { RefusalWords } from '../../../entities/account-quota/refusal-words';
import { QuotaRefusal } from '../../../entities/account-quota/ui/QuotaRefusal';
import { loadFailureWords } from '../../../entities/api-errors';
import { bookFileKinds } from '../../../entities/manuscript/book-file-kinds';
import { Button } from '../../../ui/controls/Button';
import { EmptyState } from '../../../ui/feedback/EmptyState';
import { SagaMissing } from '../../../ui/feedback/SagaMissing';
import { SkeletonArea } from '../../../ui/feedback/Skeleton';
import { formatCount } from '../../../ui/copy';
import { PageHead } from '../../../ui/shell/PageHead';
import styles from './ManuscriptShelfPage.module.css';

export type ShelfLoadStatus = 'loading' | 'ready' | 'empty' | 'error';

interface ShelfHeaderProps {
  canEditManuscript: boolean;
  /** The demo saga of the site (specs/2026-11-09-demo-saga.md DS4): no upload; its mark «Пример» is the top bar's. */
  demo?: boolean;
  fileInputRef: RefObject<HTMLInputElement | null>;
  importBlocked: boolean;
  /**
   * Why «Добавить книги» is closed for now, said by the button itself (specs/2026-11-10-ux1-shell.md SH4.4,
   * specs/2026-11-11-ux7-upload.md UP1.1, UP3.5): it stays in its place, dimmed, and a press says the reason beside it
   * instead of opening the window of files.
   */
  importReason?: string | undefined;
  onChooseImportFile: () => void;
  onImportInputChange: ChangeEventHandler<HTMLInputElement>;
  projectExists: boolean;
  projectMeta: string;
  showActions?: boolean;
  /** The line at «Добавить книги» that leads to «Ваши материалы» (specs/2026-11-11-ux9-shelf-chat.md SC1.7). */
  materials?: ReactNode;
  /**
   * «Продолжить чтение: <книга>» — the main action of a shelf the author came back to from the reader
   * (specs/2026-11-10-ux2-shell.md S2.2); none without a place of reading.
   */
  continueReading?: { book: string; onOpen: () => void } | undefined;
}

interface ShelfStatusMessagesProps {
  /** The failed shelf request, if the shelf could not load. */
  error: unknown;
  /** The saga has no book and the first steps do not already ask for one. */
  emptySaga: boolean;
  notice: string;
  /** A new saga refused by a limit: the notice is the one block of refusals (specs/2026-11-10-ux2-patterns-refusals.md PR2.2). */
  noticeRefusal?: { code: string; words: RefusalWords } | null;
  onRetry: () => void;
  retrying: boolean;
  status: ShelfLoadStatus;
  /** A long work of the shelf in the place of the notice: the file of a book being made (PS3.3). */
  work?: ReactNode;
}

export function ShelfHeader({
  canEditManuscript,
  continueReading,
  demo = false,
  fileInputRef,
  importBlocked,
  importReason,
  materials,
  onChooseImportFile,
  onImportInputChange,
  projectExists,
  projectMeta,
  showActions = true,
}: ShelfHeaderProps) {
  const importDisabled = !projectExists || !canEditManuscript || importBlocked;
  // Closed for now, the button stays and says why (specs/2026-11-10-ux2-patterns-refusals.md PR1.1, PR1.3); a reader of
  // the saga hears whose the books are (UP1.1).
  const muted = importDisabled && Boolean(importReason) && projectExists;
  const actions = showActions ? <div className={styles.topActions}>
        {projectExists && !demo && (canEditManuscript || importReason) ? <>
          <Button
            className={styles.uploadFileButton}
            data-muted={muted || undefined}
            disabled={importDisabled && !muted}
            disabledReason={muted ? importReason : null}
            onClick={onChooseImportFile}
            variant="outline"
          >
            <Upload aria-hidden="true" size={16} />
            Добавить книги
          </Button>
          {/* The system window filters nothing: a file a book cannot be made of gets its reason, as a dropped one does
              (specs/2026-11-10-ux1-names.md N8.1; L-19). A reader of the saga has the button and its reason only. */}
          {canEditManuscript ? <input
            aria-label={`Файлы книг: ${bookFileKinds}`}
            className={styles.fileInput}
            disabled={importDisabled}
            multiple
            onChange={onImportInputChange}
            ref={fileInputRef}
            type="file"
          /> : null}
          {canEditManuscript && materials ? <p className={styles.materialsLine}>{materials}</p> : null}
        </> : null}
        {continueReading ? (
          <Button className={styles.continueReading} onClick={continueReading.onOpen} variant="primary">
            <BookOpen aria-hidden="true" size={16} />
            <span>Продолжить чтение: {continueReading.book}</span>
          </Button>
        ) : null}
      </div> : null;
  // The saga and its mark «Пример» stand in the top bar; the shelf says what is in it in one line.
  return <PageHead action={actions} line={projectExists && projectMeta ? projectMeta : undefined} surface="manuscript" />;
}

/**
 * The shelf of the demo saga takes no books (specs/2026-11-09-demo-saga.md DS4.2): in place of the upload it says
 * where the author's own books go and opens the form of a new saga.
 */
export function DemoSagaNotice({ credit, onCreateSaga }: { credit?: string; onCreateSaga: () => void }) {
  return (
    <div className={styles.demoNotice}>
      <p>Это сага-пример. Создайте свою сагу — книги загружаются в неё</p>
      <Button onClick={onCreateSaga} size="sm" variant="primary">
        <Plus aria-hidden="true" size={16} />
        Создать свою сагу
      </Button>
      {credit ? <p className={styles.demoCredit}>{credit}</p> : null}
    </div>
  );
}

/** «Недавно удалённые: N саг» on a shelf without a saga, while there is one to return (specs/2026-11-10-ux1-shelf.md UXS4.1). */
export function DeletedSagasEntry({ count, onOpen }: { count: number; onOpen: () => void }) {
  return (
    <Button className={styles.deletedSagasEntry} onClick={onOpen} variant="outline">
      <ArchiveRestore aria-hidden="true" size={16} />
      Недавно удалённые: {formatCount(count, 'сага', 'саги', 'саг')}
    </Button>
  );
}

export function ShelfBooksHeader() {
  return (
    <div className={styles.shelfSectionHeader}>
      <p className={styles.sectionLabel}>Книги саги</p>
    </div>
  );
}

/**
 * «Назовите сагу» of the first screen and «Новая сага» of the saga menu: the saga name is a form draft (R2.1.2). In a card
 * of the first screen the field and the button stand one under the other (FS1.2).
 */
export function FirstProjectPanel({ creating, onCreate, stacked = false }: {
  creating: boolean;
  onCreate: (title: string) => Promise<boolean>;
  stacked?: boolean;
}) {
  const [title, setTitle] = useState('');
  const field = useRef<HTMLInputElement>(null);
  return (
    <form
      className={stacked ? `${styles.sagaForm} ${styles.sagaFormStacked}` : styles.sagaForm}
      onSubmit={(event) => {
        event.preventDefault();
        void onCreate(title).then((created) => { if (created) setTitle(''); });
      }}
    >
      <label className={styles.field}>
        <span>Название саги</span>
        <input
          aria-label="Название саги"
          autoComplete="off"
          onChange={(event) => setTitle(event.target.value)}
          placeholder="Например, Сага северного ветра"
          ref={field}
          value={title}
        />
      </label>
      <div className={styles.formActions}>
        {/* An empty name: the button stays and says why (specs/2026-11-10-ux2-patterns-refusals.md PR1.3). */}
        <Button disabledReason={title.trim() ? null : 'Сначала напишите название саги.'} loading={creating}
          onRefusedPress={() => field.current?.focus()} type="submit" variant="primary">
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
  notice,
  noticeRefusal,
  onRetry,
  retrying,
  status,
  work,
}: ShelfStatusMessagesProps) {
  return (
    <>
      {status === 'loading' ? (
        <SkeletonArea count={4} shape="tiles" />
      ) : null}
      {status === 'empty' && emptySaga ? (
        // Until the first screen of ux3-first-screen: the words of today in the three parts; «Добавить книги» above is the step.
        <EmptyState icon={<Library size={28} />} title="В саге пока нет книг"
          why="Добавьте первую книгу — кнопкой вверху или перетащите файл на страницу. Через пару минут по ней можно задавать вопросы." />
      ) : null}
      {status === 'error' ? (
        <ErrorBlock className={styles.loadError} {...loadFailureWords(error)} onRetry={onRetry} retrying={retrying} title="Не открылись книги саги" />
      ) : null}
      {work}
      {notice && noticeRefusal ? <QuotaRefusal announce code={noticeRefusal.code} place="new-saga" words={noticeRefusal.words} />
        : notice ? <p className={styles.notice} role="status">{notice}</p> : null}
    </>
  );
}

/**
 * A saga the address names and the author does not have (specs/2026-11-11-ux9-shelf-chat.md SC1.9, W-7G): the heading says
 * so, and a calm block offers his own sagas to open — no alarm, he did nothing wrong.
 */
export function ProjectMissingState({ onChoose, onCreate, sagas }: { onChoose: (projectId: string) => void; onCreate: () => void; sagas: readonly { id: string; title: string }[] }) {
  return (
    <>
      <h1 className={styles.missingTitle} data-page-heading="" tabIndex={-1}>Сага не найдена</h1>
      <SagaMissing onChoose={onChoose} onCreate={onCreate} sagas={sagas} title="Её нет среди ваших саг" why="Она удалена, доступ к ней закрыт или ссылка устарела." />
    </>
  );
}
