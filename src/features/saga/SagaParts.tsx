import type { MouseEvent } from 'react';
import { loadFailureWords } from '../../entities/api-errors';
import { ErrorBlock } from '../../ui/feedback/ErrorBlock';
import { isApiStatusFailure } from '../../entities/api-errors';
import type { SagaChange, SagaChapterRef } from '../../entities/saga/api';
import { toHref, type RouteNavigate } from '../../shared/navigation/app-route';
import { paragraphsLabel } from '../../shared/text/place';
import { Button } from '../../ui/controls/Button';
import { LinkAction } from '../../ui/controls/LinkAction';
import { Alert } from '../../ui/feedback/Alert';
import { SkeletonArea } from '../../ui/feedback/Skeleton';
import styles from './SagaMap.module.css';

export interface ReaderTarget {
  navigate: RouteNavigate;
  projectId: string;
}

/**
 * A chapter opening in the reader; a plain click stays in the app, a modified click opens a tab.
 * The compact form shows only the chapter number, with the title for assistive technology; with its book the chapter
 * reads as its place, «Книга 2, гл. 12 „Белый порт“» (specs/2026-11-10-ux1-names.md N4.2).
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
    // A link-action of the controls: its target is 44 px under a finger by the control (specs/2026-11-11-ux9-shelf-chat.md SC2.1, W-64).
    <LinkAction aria-label={short ? chapter.title : undefined} href={toHref('/manuscript/books', query)}
      onClick={(event: MouseEvent<HTMLAnchorElement>) => {
        if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
        event.preventDefault();
        target.navigate('/manuscript/books', query);
      }} title={short ? chapter.title : undefined}>
      {short ? chapter.number : withBook ? chapter.label : chapter.title}
    </LinkAction>
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
            {change.paragraph ? <span className={styles.paragraph}>, {paragraphsLabel(change.paragraph)}</span> : null}
          </span>
          <span><strong>{change.subject}</strong> — {change.change}</span>
        </li>
      ))}
    </ol>
  );
}

/** A card or a list of the map while it loads: the form of its rows, announced as busy (PS3.1). */
export function LoadingRows() {
  return <SkeletonArea className={styles.loading} count={4} shape="rows" />;
}

/** The saga search rows are not built yet, as after a restore until they are rebuilt. */
export function isSagaNotReady(error: unknown) {
  return isApiStatusFailure(error) && error.status === 503 && error.problem.code === 'saga.not_ready';
}

/**
 * A part of the map that did not open: the one block of an error (specs/2026-11-10-ux2-patterns-refusals.md PR3.2); a
 * saga still prepared is not an error.
 */
export function LoadError({ error, onRetry, title = 'Не открылась карточка' }: { error?: unknown; onRetry: () => void; title?: string }) {
  if (isSagaNotReady(error)) {
    return (
      <Alert tone="info">
        <p>Помощник ещё читает сагу. Попробуйте через несколько минут.</p>
        <Button onClick={onRetry} variant="outline">Повторить</Button>
      </Alert>
    );
  }
  return <ErrorBlock {...loadFailureWords(error)} onRetry={onRetry} title={title} />;
}
