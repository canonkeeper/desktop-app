import { useQuery } from '@tanstack/react-query';
import { BookOpen, MessageSquare, Play } from 'lucide-react';
import type { ReactNode } from 'react';
import { publicApiErrorMessage } from '../../../entities/api-errors';
import { chatSessionsQueryOptions } from '../../../entities/chat/api';
import { textSourceView, useIndexingStatusQuery, type Book, type ManuscriptApiClient, type ProjectJob } from '../../../entities/manuscript/api';
import { finishReadingWord } from '../../../entities/manuscript/preparation-copy';
import { Button } from '../../../ui/controls/Button';
import { LongWork } from '../../../ui/feedback/LongWork';
import { SupportReference } from '../../../ui/feedback/SupportReference';
import { useRestartIndexingMutation } from '../api';
import { sagaStateAnnouncement, sagaStateOf } from './saga-state';
import { isNewBookOnItsWay } from './shelf-jobs';
import styles from './SagaState.module.css';

interface SagaStateOptions {
  api: ManuscriptApiClient;
  projectId: string;
  books: readonly Pick<Book, 'id' | 'title'>[];
  /** The Jobs of the saga the shelf reads: the gathering of its story lines among them. */
  jobs: readonly ProjectJob[];
  /** The file of a new book this tab sends, while the server parses it. */
  fileName?: string | null;
  canEdit: boolean;
  onAskQuestion: () => void;
  /** «Читать» of a reader of the saga: he asks nothing, so his main button reads (specs/2026-11-11-ux9-shelf-chat.md SC1.2). */
  onRead?: (() => void) | undefined;
}

const sagaLayerWorks = (job: ProjectJob) => job.kind === 'saga_layer' && (job.status === 'queued' || job.status === 'running');

/**
 * The one block of the state of the saga on the shelf (specs/2026-11-10-ux3-saga-state.md UC2): the line of the reading
 * — its words, its time, whether to ask and its one button — from getIndexingStatus, followed until the reading ends, and
 * under it, while the story lines are gathered, a quiet line without buttons (SS2.4). There is no stop: the reading costs
 * the author nothing, a wrong file goes with «Удалить книгу», a stopped reading goes on with «Дочитать» (SS2.3; the word of
 * the book it stopped on, specs/2026-11-11-ux7-shelf.md SF2.4). Null
 * when there is nothing to say: the page then knows the first steps keep their own button (SS2.5).
 */
export function useSagaState({ api, books, canEdit, fileName, jobs, onAskQuestion, onRead, projectId }: SagaStateOptions): ReactNode {
  const status = useIndexingStatusQuery(api, projectId, { follow: true }).data;
  const context = status?.published.exists ? status.published.context : null;
  const stalledBookId = context?.state === 'partial' ? context.retryableFailedBook?.bookId ?? '' : '';
  const stalledBook = useIndexingStatusQuery(api, projectId, {
    bookId: stalledBookId, enabled: stalledBookId !== '', sourceView: textSourceView,
  }).data;
  const chats = useQuery({
    ...chatSessionsQueryOptions(api, { projectId, sourceView: textSourceView }),
    enabled: projectId !== '',
  }).data?.data;
  const restart = useRestartIndexingMutation(api);
  const readyAt = status?.sagaReady?.at;
  // The end is answered by a chat started after it (SS2.1): the page reads the time of the server and the chats.
  const askedSinceReady = Boolean(readyAt && chats?.some((chat) => Date.parse(chat.createdAt) >= Date.parse(readyAt)));
  const view = sagaStateOf({
    status, stalledBook, fileName, askedSinceReady, importing: jobs.some(isNewBookOnItsWay),
    bookTitle: (bookId) => books.find((book) => book.id === bookId)?.title,
  });
  const gathering = jobs.some(sagaLayerWorks);
  if (!view && !gathering) return null;

  const askLabel = chats && chats.length > 0 ? 'Задать вопрос' : 'Задать первый вопрос';
  let action = null;
  if (!canEdit) {
    // A reader of the saga asks nothing: the main button of the line reads (SC1.2, W-82).
    action = onRead && (view?.kind === 'ready' || (view?.kind === 'reading' && view.ask))
      ? <Button onClick={onRead} size="sm" variant="primary"><BookOpen aria-hidden="true" size={14} />Читать</Button>
      : null;
  } else if (view?.kind === 'reading' && view.ask) {
    action = <Button onClick={onAskQuestion} size="sm" variant="primary"><MessageSquare aria-hidden="true" size={14} />Задать вопрос</Button>;
  } else if (view?.kind === 'ready') {
    action = <Button onClick={onAskQuestion} size="sm" variant="primary"><MessageSquare aria-hidden="true" size={14} />{askLabel}</Button>;
  } else if (view?.kind === 'stalled' && view.retry && canEdit) {
    action = (
      <Button loading={restart.isPending} onClick={() => restart.mutate({ projectId, sourceView: textSourceView })} size="sm" variant="primary">
        <Play aria-hidden="true" size={14} />{finishReadingWord}
      </Button>
    );
  }
  return (
    <div className={styles.sagaState}>
      {view ? (
        <LongWork
          action={action}
          announcement={sagaStateAnnouncement(view)}
          detail={view.note || undefined}
          label="Чтение саги"
          progress={view.kind === 'reading' ? view.progress : null}
          title={view.title}
          tone={view.kind === 'ready' ? 'done' : view.kind === 'stalled' ? 'stalled' : 'work'}
        >
          {view.kind === 'stalled' && view.retry ? <SupportReference code={view.supportCode} /> : null}
          {restart.isError ? (
            <p className={styles.refusal} role="alert">{publicApiErrorMessage(restart.error, 'Не удалось продолжить чтение. Попробуйте ещё раз.')}</p>
          ) : null}
        </LongWork>
      ) : null}
      {gathering ? <p className={styles.quiet}>Дособираю сюжетные линии — спрашивать и читать это не мешает</p> : null}
    </div>
  );
}
