import { isApiStatusFailure } from '../../entities/api-errors';
import type { SagaChange, SagaChapterRef } from '../../entities/saga/api';
import { toHref, type RouteNavigate } from '../../shared/navigation/app-route';
import { Button } from '../../ui/controls/Button';
import { Alert } from '../../ui/feedback/Alert';
import { Skeleton } from '../../ui/feedback/Skeleton';
import styles from './SagaMap.module.css';

export interface ReaderTarget {
  navigate: RouteNavigate;
  projectId: string;
}

/**
 * A chapter opening in the reader; a plain click stays in the app, a modified click opens a tab.
 * The compact form shows only the chapter number, with the title for assistive technology.
 */
export function ChapterLink({ chapter, compact = false, target, withBook = false }: {
  chapter: SagaChapterRef;
  compact?: boolean;
  target: ReaderTarget;
  withBook?: boolean;
}) {
  const query = { projectId: target.projectId, bookId: chapter.bookId, chapterId: chapter.chapterId, mode: 'read' };
  const short = compact && chapter.number !== undefined;
  return (
    <a aria-label={short ? chapter.title : undefined} className={styles.chapterLink} href={toHref('/manuscript/books', query)}
      onClick={(event) => {
        if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
        event.preventDefault();
        target.navigate('/manuscript/books', query);
      }} title={short ? chapter.title : undefined}>
      {short ? chapter.number : <>
        {withBook ? <span className={styles.bookOrder}>Том {chapter.bookOrder} · </span> : null}
        {chapter.title}
      </>}
    </a>
  );
}

/** Card changes in reading order: where, then who and what changed. */
export function ChangeList({ changes, target }: { changes: readonly SagaChange[]; target: ReaderTarget }) {
  return (
    <ol className={styles.changes}>
      {changes.map((change, index) => (
        <li className={styles.change} key={`${change.chapter.key}:${index}`}>
          <span>
            <ChapterLink chapter={change.chapter} target={target} withBook />
            {change.paragraph ? <span className={styles.paragraph}> · ¶ {change.paragraph}</span> : null}
          </span>
          <span><strong>{change.subject}</strong> — {change.change}</span>
        </li>
      ))}
    </ol>
  );
}

export function LoadingRows({ label = 'Загрузка карточки' }: { label?: string }) {
  return (
    <div aria-label={label} className={styles.loading} role="status">
      {[0, 1, 2, 3].map((row) => <Skeleton className={styles.loadingRow} key={row} />)}
    </div>
  );
}

/** The saga search rows are not built yet, as after a restore until they are rebuilt. */
export function isSagaNotReady(error: unknown) {
  return isApiStatusFailure(error) && error.status === 503 && error.problem.code === 'saga.not_ready';
}

export function LoadError({ error, onRetry, what = 'карточку' }: { error?: unknown; onRetry: () => void; what?: string }) {
  const notReady = isSagaNotReady(error);
  return (
    <Alert tone={notReady ? 'info' : 'error'}>
      <p>{notReady ? 'Сага ещё готовится к поиску. Попробуйте через несколько минут.' : `Не удалось загрузить ${what}.`}</p>
      <Button onClick={onRetry} variant="outline">Повторить</Button>
    </Alert>
  );
}
