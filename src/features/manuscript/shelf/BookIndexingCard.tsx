import { useQuery } from '@tanstack/react-query';
import { LoaderCircle, X } from 'lucide-react';
import { publicApiErrorMessage } from '../../../entities/api-errors';
import { sagaHasChatQueryOptions } from '../../../entities/chat/api';
import type { Book, ManuscriptApiClient, SourceView } from '../../../entities/manuscript/api';
import * as manuscriptApi from '../../../entities/manuscript/api';
import * as manuscriptFeatureApi from '../api';
import { formatCount } from '../../../ui/copy';
import { Button } from '../../../ui/controls/Button';
import { IconButton } from '../../../ui/controls/IconButton';
import { IndexingChecklist } from './IndexingChecklist';
import { IndexingOutcome } from './IndexingOutcome';
import { unfinishedIndexingBookId } from './shelf-jobs';
import {
  activeIndexingStepText,
  cardsWaitingTurn,
  findCurrentIndexingStep,
  formatRemaining,
  indexingStepDetail,
  indexingStepLabels,
  indexingStepStatusLabels,
  isRetryableIndexingStep,
  measuredStepProgress,
  partialResultMessage,
  remainingPreparationEstimate,
  stepDuration,
} from './indexing-card-format';
import styles from './BookIndexingCard.module.css';

interface BookIndexingCardProps {
  api: ManuscriptApiClient;
  bookId: string | null;
  bookPendingCreation: boolean;
  bookTitle: string;
  books: Array<Pick<Book, 'id' | 'title'>>;
  canCancel: boolean;
  canceling: boolean;
  canEditManuscript: boolean;
  jobSourceView?: SourceView;
  /** Opens a new chat of the saga. */
  onAskQuestion: () => void;
  onCancel?: () => void;
  onDismiss: () => void;
  projectId: string;
  sourceView: SourceView;
}

// BookIndexingCard shows how far one book of a text version is from being ready for the chat:
// import, text index of its version and the cards of its chapters.
export function BookIndexingCard({
  api,
  bookId,
  bookPendingCreation,
  bookTitle,
  books,
  canCancel,
  canceling,
  canEditManuscript,
  jobSourceView,
  onAskQuestion,
  onCancel,
  onDismiss,
  projectId,
  sourceView,
}: BookIndexingCardProps) {
  const statusSourceView = jobSourceView ?? sourceView;
  const preBookImport = bookId === null && bookPendingCreation;
  const statusQuery = manuscriptApi.useIndexingStatusQuery(
    api,
    projectId,
    bookId ? { bookId, sourceView: statusSourceView } : { enabled: preBookImport },
  );
  // The project-wide status only matters before the import Job creates the book; after that the
  // card follows the exact book scope and never mixes chapters of different books.
  const indexingStatus = bookId !== null || preBookImport ? statusQuery.data : undefined;
  const steps = indexingStatus?.steps ?? [];
  const envelope = indexingStatus
    ? statusSourceView === 'draft'
      ? indexingStatus.draft
      : indexingStatus.published
    : undefined;
  const context = envelope?.exists ? envelope.context : null;
  // The ready projection outranks a late Job snapshot, except during a queue upload whose book
  // does not exist yet.
  const readyProjectionWins = context?.state === 'ready' && !preBookImport;
  const currentStepIndex = readyProjectionWins ? -1 : findCurrentIndexingStep(steps);
  const currentStep = steps[currentStepIndex];
  const importInProgress = currentStep?.kind === 'import' && (
    currentStep.status === 'pending'
      || currentStep.status === 'queued'
      || currentStep.status === 'running'
      || currentStep.status === 'canceling'
  );
  const statusBookId = bookPendingCreation ? null : unfinishedIndexingBookId(indexingStatus, statusSourceView);
  // An unfinished version names the book of the card; a ready shelf keeps the user's choice.
  const statusOwnsIdentity = context?.state === 'processing' || context?.state === 'partial';
  const displayedBookId = bookId ?? (statusOwnsIdentity ? statusBookId : null);
  const displayedBookTitle = books.find((book) => book.id === displayedBookId)?.title ?? bookTitle;
  const restartMutation = manuscriptFeatureApi.useRestartIndexingMutation(api);
  // A partial version names the book to restart; the restart reindexes the whole version.
  const retryableFailedBookId = context?.retryableFailedBook?.bookId;
  const canRestart = canEditManuscript && retryableFailedBookId !== undefined && retryableFailedBookId === displayedBookId;
  const retryKind = canRestart
    ? steps.find((step) => isRetryableIndexingStep(step) && step.sourceView === statusSourceView)?.kind
    : undefined;
  const restart = () => restartMutation.mutate({ projectId, sourceView: statusSourceView });
  const terminalFailureStep = currentStep?.status === 'failed' ? currentStep : undefined;
  const terminalCanceledStep = currentStep?.status === 'canceled' ? currentStep : undefined;
  const terminalPartial = context?.state === 'partial'
    && !steps.some((step) => step.status === 'running' || step.status === 'canceling'
      || step.status === 'queued' || step.status === 'canceled' || step.status === 'failed');
  const outcomeTone = readyProjectionWins
    ? 'success'
    : terminalPartial
      ? 'partial'
      : terminalFailureStep
        ? 'failed'
        : terminalCanceledStep
          ? 'canceled'
          : undefined;
  const outcomeVisible = outcomeTone !== undefined;
  const indexingPosition = context?.currentBook ? `Книга ${context.currentBook.position} из ${context.currentBook.total}` : null;
  const hasChat = useQuery(sagaHasChatQueryOptions(api, { projectId, sourceView })).data;
  const estimate = currentStep && !outcomeVisible ? preparationEstimate(currentStep, context?.currentBook) : null;
  // R2.2.4: the chat answers by the prepared part while the cards are still being written.
  const askNow = currentStep?.kind === 'cards' && (currentStep.status === 'running' || currentStep.status === 'queued')
    && steps.some((step) => step.kind === 'text_index' && step.status === 'succeeded');
  const cardsTurn = cardsWaitingTurn(currentStep);
  let cardTitle = `«${displayedBookTitle}» ещё не подготовлена для чата`;
  if (currentStep?.status === 'queued' && !cardsTurn) cardTitle = 'Подготовка поставлена в очередь';
  else if (currentStep || context?.state === 'processing') cardTitle = `Готовим «${displayedBookTitle}» для чата`;
  let currentStageState = currentStep ? indexingStepStatusLabels[currentStep.status] : 'Сейчас';
  const activityDuration = currentStep?.status === 'running' || currentStep?.status === 'canceling' ? stepDuration(currentStep) : null;
  const compactProgress = currentStep ? measuredStepProgress(currentStep) : null;
  const counted = currentStep && (currentStep.kind === 'text_index' || currentStep.kind === 'cards')
    && currentStep.counters?.indexedChapters !== undefined && currentStep.counters.totalChapters !== undefined;
  const currentCounter = currentStep && counted ? `${activeIndexingStepText(currentStep).detail} ` : '';
  const importFacts = importInProgress && currentStep ? indexingStepDetail(currentStep) : null;
  let currentStageMessage = `${currentCounter}Можно закрыть страницу — подготовка продолжится. Когда вернётесь, здесь будет кнопка «Задать первый вопрос».`;
  if (cardsTurn) {
    currentStageState = cardsTurn.state;
    currentStageMessage = cardsTurn.message;
  } else if (currentStep?.status === 'queued') {
    currentStageMessage = 'Подготовка уже идёт — запускать её снова не нужно.';
  } else if (currentStep?.status === 'pending') {
    currentStageMessage = 'Этап ожидает запуска. Можно закрыть страницу: обработка продолжится автоматически.';
  } else if (currentStep?.status === 'partial') {
    currentStageState = 'Завершено не полностью';
    currentStageMessage = partialResultMessage(steps);
  }
  const currentStageLabel = importInProgress
    ? currentStageState
    : `${currentStageState} · этап ${currentStepIndex + 1} из ${steps.length}`;
  return (
    <section aria-label="Подготовка книги" className={styles.indexingCard} data-tone={outcomeTone}>
      {outcomeVisible ? null : <div className={styles.indexingCardHeader}>
        <LoaderCircle aria-hidden="true" className={styles.headerSpinner} data-running={currentStep?.status === 'running'} />
        <div>
          <h2>{cardTitle}</h2>
          {indexingPosition ? <p className={styles.jobFacts}>{indexingPosition}</p> : null}
        </div>
        <IconButton aria-label="Скрыть подготовку" className={styles.indexingDismiss} onClick={onDismiss}>
          <X aria-hidden="true" size={16} />
        </IconButton>
      </div>}

      <IndexingOutcome
        allSteps={steps}
        askLabel={hasChat ? 'Задать вопрос' : 'Задать первый вопрос'}
        canRetry={canRestart}
        displayedBookTitle={displayedBookTitle}
        onAskQuestion={onAskQuestion}
        onDismiss={onDismiss}
        onRetry={restart}
        retryError={restartMutation.isError ? restartMutation.error : null}
        retryPending={restartMutation.isPending}
        terminalCanceledStep={terminalCanceledStep}
        terminalFailureStep={terminalFailureStep}
        tone={outcomeTone}
      />

      {statusQuery.isPending ? <p className={styles.muted}>Проверяем подготовку…</p> : null}
      {statusQuery.isError ? (
        <p className={styles.notice}>{publicApiErrorMessage(statusQuery.error, 'Не удалось узнать, как идёт подготовка.')}</p>
      ) : null}
      {envelope?.exists === false ? <p className={styles.muted}>Текст книги пока не загружен.</p> : null}

      {steps.length > 0 ? (
        <div className={styles.indexingProgress}>
          {currentStep && !outcomeVisible ? (
            <div aria-live="polite" aria-atomic="true" className={styles.currentStep}>
              <p className={styles.sectionLabel}>
                {currentStageLabel}
                {activityDuration ? <> · <span className={styles.desktopStatusCopy}>идёт </span>{activityDuration}</> : null}
              </p>
              <h3>{indexingStepLabels[currentStep.kind]}</h3>
              {estimate ? <p className={styles.estimate}>{estimate}</p> : null}
              {importFacts ? <p>{importFacts}</p> : null}
              <p>{currentStageMessage}</p>
              {askNow ? (
                <button className={styles.askNow} onClick={onAskQuestion} type="button">
                  Можно спросить уже сейчас — ответ будет по готовой части
                </button>
              ) : null}
              {canCancel && onCancel ? (
                <div className={styles.currentStepActions}>
                  <Button aria-label="Отменить обработку" className={styles.cancelButton} loading={canceling} onClick={onCancel}>
                    Отменить
                  </Button>
                </div>
              ) : null}
            </div>
          ) : null}

          <details className={styles.stepsDetails}>
            <summary>
              Показать этапы
              {currentStepIndex >= 0 ? ` · этап ${currentStepIndex + 1} из ${steps.length}` : ` · ${steps.length} этапа`}
              {compactProgress !== null && compactProgress !== undefined ? ` · ${Math.round(compactProgress * 100)}%` : ''}
            </summary>
            <IndexingChecklist
              onRetry={restart}
              outcomeVisible={outcomeVisible}
              retryKind={retryKind}
              retryPending={restartMutation.isPending}
              steps={steps}
            />
          </details>
        </div>
      ) : null}

      {!outcomeVisible && restartMutation.isError ? (
        <p className={styles.notice}>{publicApiErrorMessage(restartMutation.error, 'Не удалось повторить обработку.')}</p>
      ) : null}
    </section>
  );
}

/** R2.2.1: the time left for the current book, and how many books of the queue wait after it. */
function preparationEstimate(step: Parameters<typeof remainingPreparationEstimate>[0], currentBook?: { position: number; total: number } | null) {
  if (step.kind === 'ready' || !['pending', 'queued', 'running'].includes(step.status)) return null;
  const seconds = remainingPreparationEstimate(step);
  if (seconds === null) return 'Оценка появится, когда главы будут разобраны';
  const left = currentBook ? currentBook.total - currentBook.position : 0;
  if (left <= 0) return `Осталось ${formatRemaining(seconds)}`;
  return `Эта книга — ещё ${formatRemaining(seconds)}; дальше: ${formatCount(left, 'книга', 'книги', 'книг')}`;
}
