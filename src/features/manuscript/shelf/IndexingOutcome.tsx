import { MessageSquare, RotateCcw, X } from 'lucide-react';
import type { IndexingStep } from '../../../entities/manuscript/api';
import { publicApiErrorMessage } from '../../../entities/api-errors';
import { formatCount, pluralize } from '../../../ui/copy';
import { Button } from '../../../ui/controls/Button';
import { IconButton } from '../../../ui/controls/IconButton';
import {
  indexingStepLabels,
  stepTimingLabel,
  type IndexingStepView,
} from './indexing-card-format';
import { IndexingOutcomeBanner } from './IndexingOutcomeBanner';
import styles from './BookIndexingCard.module.css';

export type IndexingOutcomeTone = 'success' | 'partial' | 'failed' | 'canceled';

function chapterCounts(steps: IndexingStep[]) {
  const counters = (kind: IndexingStep['kind']) => steps.find((step) => step.kind === kind)?.counters;
  return {
    total: counters('text_index')?.totalChapters ?? counters('import')?.totalChapters,
    indexed: counters('text_index')?.indexedChapters,
    carded: counters('cards')?.indexedChapters,
  };
}

export function IndexingOutcome({
  allSteps,
  askLabel,
  canRetry,
  displayedBookTitle,
  onAskQuestion,
  onDismiss,
  onRetry,
  retryError,
  retryPending,
  terminalCanceledStep,
  terminalFailureStep,
  tone,
}: {
  allSteps: IndexingStep[];
  /** «Задать первый вопрос» while the saga has no chat, else «Задать вопрос» (R2.2.3). */
  askLabel: string;
  canRetry: boolean;
  displayedBookTitle: string;
  onAskQuestion: () => void;
  onDismiss: () => void;
  onRetry: () => void;
  retryError: unknown;
  retryPending: boolean;
  terminalCanceledStep?: IndexingStepView;
  terminalFailureStep?: IndexingStepView;
  tone?: IndexingOutcomeTone;
}) {
  if (!tone) return null;
  const dismiss = <IconButton aria-label="Скрыть уведомление" className={styles.terminalDismissButton} onClick={onDismiss}><X aria-hidden="true" size={16} /></IconButton>;
  const retryAction = canRetry
    ? <Button loading={retryPending} onClick={onRetry} variant="primary"><RotateCcw aria-hidden="true" size={14} />{retryPending ? 'Запускаем повторно...' : 'Повторить'}</Button>
    : null;
  const retryNotice = retryError ? publicApiErrorMessage(retryError, 'Не удалось повторить обработку.') : undefined;
  const { total, indexed, carded } = chapterCounts(allSteps);
  const bookFacts = [displayedBookTitle, total === undefined ? null : formatCount(total, 'глава', 'главы', 'глав')].filter(Boolean).join(' · ');
  // The chat answers by the prepared text, so a partial book is asked too (R2.2.3).
  const ask = <Button className={styles.terminalOutcomeAsk} onClick={onAskQuestion} variant="primary"><MessageSquare aria-hidden="true" size={14} />{askLabel}</Button>;

  if (tone === 'success') {
    return <IndexingOutcomeBanner
      actions={<>{ask}{dismiss}</>}
      desktopBody={<><p className={styles.terminalOutcomeEyebrow}>{bookFacts}</p><p>Поиск по тексту и карточки глав доступны чату.</p></>}
      desktopTitle="Книга готова для чата"
      mobileBody={<><p className={styles.terminalOutcomeEyebrow}>{bookFacts}</p><p>Поиск и карточки глав доступны.</p></>}
      mobileTitle="Книга готова"
      tone="success"
    />;
  }

  if (tone === 'partial') {
    const counts = total !== undefined && indexed !== undefined && carded !== undefined
      ? `Поиск — по ${indexed} из ${total} ${pluralize(total, 'главы', 'глав', 'глав')}, карточки — для ${carded} из ${total}.`
      : 'Доступно чтение сохранённых глав и частичный поиск.';
    return <IndexingOutcomeBanner
      actions={<>{ask}{retryAction}{dismiss}</>}
      desktopBody={<><p className={styles.terminalOutcomeEyebrow}>{bookFacts}</p><p>{counts}</p><p>Чат отвечает по готовой части; запустите подготовку заново, чтобы достроить остальное.</p></>}
      desktopTitle="Книга подготовлена не полностью"
      mobileBody={<p>{counts}</p>}
      mobileTitle="Подготовлено не полностью"
      notice={retryNotice}
      tone="partial"
    />;
  }

  if (tone === 'failed' && terminalFailureStep) {
    const duration = stepTimingLabel(terminalFailureStep);
    const detail = [
      indexingStepLabels[terminalFailureStep.kind],
      `ошибка${duration ? ` после ${duration}` : ''}`,
      canRetry ? 'можно повторить' : null,
    ].filter(Boolean).join(' · ');
    return <IndexingOutcomeBanner
      actions={<>{retryAction}<IconButton aria-label="Скрыть уведомление" onClick={onDismiss}><X aria-hidden="true" size={18} /></IconButton></>}
      desktopBody={<p>{detail}</p>}
      desktopTitle="Не удалось подготовить книгу"
      mobileBody={<p>{detail}</p>}
      mobileTitle="Подготовка не завершена"
      notice={retryNotice}
      tone="failed"
    />;
  }

  if (tone === 'canceled' && terminalCanceledStep) {
    return <IndexingOutcomeBanner
      actions={dismiss}
      desktopTitle="Подготовка книги отменена"
      mobileTitle={`${displayedBookTitle} · подготовка отменена`}
      tone="canceled"
    />;
  }
  return null;
}
