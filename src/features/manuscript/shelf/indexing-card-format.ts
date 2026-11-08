import type { IndexingStep, SourceView } from '../../../entities/manuscript/api';
import { pluralize } from '../../../ui/copy';

export type IndexingStepView = IndexingStep;

// A book is ready for the chat after import, the text index of its version and the cards
// of its chapters.
export const orderedIndexingStepKinds: IndexingStep['kind'][] = ['import', 'text_index', 'cards', 'ready'];

export const indexingStepLabels: Record<IndexingStep['kind'], string> = {
  import: 'Импортируем главы',
  text_index: 'Готовим поиск по тексту',
  cards: 'Составляем карточки глав',
  ready: 'Готово для чата',
};

export const indexingStepStatusLabels: Record<IndexingStep['status'], string> = {
  pending: 'Далее',
  queued: 'В очереди',
  running: 'Сейчас',
  canceling: 'Останавливаем',
  succeeded: 'Готово',
  partial: 'Частично готово',
  failed: 'Ошибка',
  canceled: 'Отменено',
};

const indexingStepDetails: Record<IndexingStep['kind'], string> = {
  import: 'Передаём файл, разбираем и сохраняем главы.',
  text_index: 'Готовим абзацы для поиска и ссылок на текст.',
  cards: 'ИИ пересказывает каждую главу и выписывает героев, места и события.',
  ready: 'Поиск по тексту и карточки глав доступны чату.',
};

const pendingStepDetails: Partial<Record<IndexingStep['kind'], [string, string]>> = {
  cards: ['Начнётся после подготовки поиска по тексту.', 'После поиска по тексту'],
  ready: ['Книга будет готова, когда у всех глав появятся карточки.', 'Когда у всех глав будут карточки'],
};

const importPhaseLabels: Record<NonNullable<IndexingStep['importPhase']>, string> = {
  queued: 'Файл принят, импорт ждёт запуска',
  parsing: 'Разбираем главы',
  saving: 'Сохраняем книгу и главы',
  saved: 'Книга и главы сохранены',
};

// A failed text index or cards step restarts the indexing of its text version.
export function isRetryableIndexingStep(step: IndexingStep): step is IndexingStep & { canRetry: true; sourceView: SourceView } {
  return step.canRetry === true && step.sourceView !== undefined;
}

export function findCurrentIndexingStep(steps: IndexingStepView[]) {
  const priorities: IndexingStep['status'][][] = [
    ['running', 'canceling'],
    ['queued'],
    ['failed', 'canceled'],
    ['pending'],
    ['partial'],
  ];
  for (const statuses of priorities) {
    const index = steps.findIndex((step) => statuses.includes(step.status));
    if (index >= 0) return index;
  }
  return -1;
}

export function measuredStepProgress(step: IndexingStepView) {
  return step.kind === 'text_index' || step.kind === 'cards' ? step.progress : null;
}

export function formatBytes(bytes: number) {
  if (bytes < 1_000) return `${bytes} Б`;
  const divisor = bytes < 1_000_000 ? 1_000 : 1_000_000;
  const unit = divisor === 1_000 ? 'КБ' : 'МБ';
  return `${new Intl.NumberFormat('ru-RU', { maximumFractionDigits: 1 }).format(bytes / divisor)} ${unit}`;
}

/** «из 21 главы», «из 5 глав»: the total of a «done of total» counter. */
function ofChapters(total: number) {
  return `${total} ${pluralize(total, 'главы', 'глав', 'глав')}`;
}

function chapterCounters(step: IndexingStepView) {
  const counters = step.counters;
  if (counters?.indexedChapters === undefined || counters.totalChapters === undefined) return null;
  return { done: counters.indexedChapters, total: counters.totalChapters };
}

export function indexingStepFacts(step: IndexingStepView) {
  const counters = step.counters;
  if (step.kind === 'import' && counters) {
    const phase = step.importPhase ? importPhaseLabels[step.importPhase] : null;
    const bytes = counters.bytesTransferred === undefined
      ? null
      : counters.bytesTotal === undefined
        ? `Получено ${formatBytes(counters.bytesTransferred)}`
        : `Получено ${formatBytes(counters.bytesTransferred)} из ${formatBytes(counters.bytesTotal)}`;
    const parsed = counters.parsedChapters === undefined || counters.totalChapters === undefined
      ? null
      : `разобрано ${counters.parsedChapters}/${ofChapters(counters.totalChapters)}`;
    const saved = counters.savedChapters === undefined || counters.totalChapters === undefined
      ? null
      : `сохранено ${counters.savedChapters}/${ofChapters(counters.totalChapters)}`;
    const facts = [phase, bytes, parsed, saved].filter(Boolean);
    return facts.length > 0 ? facts.join(' · ') : null;
  }
  const chapters = chapterCounters(step);
  if (step.kind === 'text_index' && chapters) return `Готово глав для поиска: ${chapters.done} из ${chapters.total}`;
  if (step.kind === 'cards' && chapters) return `Карточки готовы для ${chapters.done} из ${ofChapters(chapters.total)}`;
  return null;
}

export function indexingStepCompactFacts(step: IndexingStepView) {
  const counters = step.counters;
  if (step.kind === 'import' && counters) {
    const bytes = counters.bytesTransferred === undefined ? null : formatBytes(counters.bytesTransferred);
    const parsed = counters.parsedChapters === undefined || counters.totalChapters === undefined
      ? null
      : `разобрано ${counters.parsedChapters}/${counters.totalChapters}`;
    const saved = counters.savedChapters === undefined || counters.totalChapters === undefined
      ? null
      : `сохранено ${counters.savedChapters}/${counters.totalChapters}`;
    const facts = [bytes, parsed, saved].filter(Boolean);
    return facts.length > 0 ? facts.join(' · ') : null;
  }
  const chapters = chapterCounters(step);
  if (step.kind === 'text_index' && chapters) return `${chapters.done}/${ofChapters(chapters.total)} в поиске`;
  if (step.kind === 'cards' && chapters) return `${chapters.done}/${chapters.total} карточек`;
  return null;
}

export function indexingStepDetail(step: IndexingStepView) {
  const facts = indexingStepFacts(step);
  if (facts) return `${facts}.`;
  return indexingStepDetails[step.kind];
}

export function activeIndexingStepText(step: IndexingStepView) {
  const duration = stepDuration(step);
  const status = step.status === 'pending'
    ? 'Не начат · —'
    : [indexingStepStatusLabels[step.status], duration].filter(Boolean).join(' · ');
  const pending = step.status === 'pending' ? pendingStepDetails[step.kind] : undefined;
  let detail = pending?.[0] ?? indexingStepDetail(step);
  let tabletDetail = pending?.[1] ?? detail;
  let mobileFacts = indexingStepCompactFacts(step);
  const counters = step.counters;
  const chapters = chapterCounters(step);
  if (step.kind === 'text_index' && chapters) {
    detail = `Готово для поиска ${chapters.done} из ${ofChapters(chapters.total)}.`;
    tabletDetail = detail;
    mobileFacts = `${chapters.done} / ${chapters.total}`;
  } else if (step.kind === 'cards' && chapters && step.status !== 'pending') {
    detail = `Карточки готовы для ${chapters.done} из ${ofChapters(chapters.total)}.`;
    tabletDetail = detail;
    mobileFacts = `${chapters.done} / ${chapters.total}`;
  } else if (step.kind === 'import' && counters) {
    if (step.status === 'succeeded') detail = indexingStepDetail({ ...step, importPhase: undefined });
    const bytes = counters.bytesTransferred === undefined ? null : formatBytes(counters.bytesTransferred);
    const parsed = counters.parsedChapters !== undefined && counters.totalChapters !== undefined
      ? `${counters.parsedChapters}/${counters.totalChapters}` : null;
    const saved = counters.savedChapters !== undefined && counters.totalChapters !== undefined
      ? `${counters.savedChapters}/${counters.totalChapters}` : null;
    tabletDetail = [bytes, parsed && `${parsed} разобрано`, saved && `${saved} сохранено`].filter(Boolean).join(' · ') || detail;
    if (parsed && parsed === saved) {
      mobileFacts = [bytes, `${counters.parsedChapters}/${ofChapters(counters.totalChapters!)} разобрано и сохранено`].filter(Boolean).join(' · ');
    }
  }
  const mobileStatus = step.status === 'running' && duration ? `идёт ${duration}` : status;
  const mobileMeta = [mobileFacts, step.status === 'succeeded' && mobileFacts ? duration : mobileStatus]
    .filter(Boolean).join(' · ');
  return { detail, tabletDetail, status, mobileMeta };
}

// Owner measurements (specs/2026-10-09-saga-memory.md): 629 chapters of cards in 28 min, ≈2.7 s
// a chapter, and 12 chapters in 2 min 46 s.
const measuredSecondsPerChapter = 3;
const minimumPreparationSeconds = 180;
const observedPaceAfterCards = 3;

/**
 * UC2.2 «Подготовка книги с оценкой времени»: the seconds left until the current book is ready,
 * or null while its chapters are not counted yet. Until three cards exist the owner measurements
 * give it; then the observed pace of the cards. Import and text search take seconds, so while
 * they run the measurements estimate the whole book. Only a presentation: readiness comes from
 * the backend steps (R2.2.5).
 */
export function remainingPreparationEstimate(step: IndexingStepView, now = Date.now()): number | null {
  const total = step.counters?.totalChapters;
  if (total === undefined) return null;
  const done = step.kind === 'cards' ? Math.min(step.counters?.indexedChapters ?? 0, total) : 0;
  if (step.kind !== 'cards' && step.kind !== 'import' && step.kind !== 'text_index') return null;
  const started = step.startedAt ? Date.parse(step.startedAt) : Number.NaN;
  if (done >= observedPaceAfterCards && Number.isFinite(started) && now > started) {
    return (now - started) / 1_000 / done * (total - done);
  }
  return Math.max(minimumPreparationSeconds, measuredSecondsPerChapter * (total - done));
}

/** «меньше минуты», «≈ N мин» up to an hour, then «≈ N ч M мин» with minutes up to ten. */
export function formatRemaining(seconds: number) {
  if (seconds < 60) return 'меньше минуты';
  const minutes = Math.ceil(seconds / 60);
  if (minutes <= 60) return `≈ ${minutes} мин`;
  let hours = Math.floor(seconds / 3_600);
  let rest = Math.ceil((seconds - hours * 3_600) / 600) * 10;
  if (rest === 60) {
    hours += 1;
    rest = 0;
  }
  return rest === 0 ? `≈ ${hours} ч` : `≈ ${hours} ч ${rest} мин`;
}

function durationText(milliseconds: number) {
  if (milliseconds > 0 && milliseconds < 1_000) return 'меньше 1 с';
  const totalSeconds = Math.max(0, Math.floor(milliseconds / 1_000));
  const hours = Math.floor(totalSeconds / 3_600);
  const minutes = Math.floor((totalSeconds % 3_600) / 60);
  const seconds = totalSeconds % 60;
  if (hours > 0) return `${hours} ч ${String(minutes).padStart(2, '0')} мин`;
  return `${String(minutes).padStart(2, '0')}:${String(seconds).padStart(2, '0')}`;
}

export function elapsedTime(startedAt: string | null | undefined, completedAt?: string | null, now = Date.now()) {
  if (!startedAt) return null;
  const start = Date.parse(startedAt);
  const end = completedAt ? Date.parse(completedAt) : now;
  if (!Number.isFinite(start) || !Number.isFinite(end) || end < start) return null;
  return durationText(end - start);
}

export function stepDuration(step: IndexingStepView, now = Date.now()) {
  if (step.status === 'pending' || step.status === 'queued') return null;
  if (step.status !== 'running' && step.status !== 'canceling' && !step.completedAt) return null;
  return elapsedTime(step.startedAt, step.completedAt, now);
}

export function stepTimingLabel(step: IndexingStepView, now = Date.now()) {
  const duration = stepDuration(step, now);
  if (!duration) return null;
  return step.status === 'running' || step.status === 'canceling' ? `Работает ${duration}` : duration;
}

export function partialResultMessage(steps: IndexingStepView[]) {
  const textIndex = steps.find((step) => step.kind === 'text_index');
  const cards = steps.find((step) => step.kind === 'cards');
  const available: string[] = [];
  const unavailable: string[] = [];
  if (textIndex?.status === 'succeeded') available.push('поиск по тексту');
  else unavailable.push('поиск по части глав');
  if (cards?.status === 'succeeded') available.push('карточки глав');
  else unavailable.push('карточки части глав');
  if (available.length > 0) {
    return `Доступно: ${available.join(', ')}. Не готово: ${unavailable.join(', ')}. Сейчас обработка не выполняется.`;
  }
  return 'Обработка завершилась не полностью. Сейчас она не выполняется; доступные результаты сохранены.';
}

/**
 * The cards of a book wait for their turn after a portion: a cards Job builds a few minutes of
 * chapters and lets other books' cards go first (specs/2026-10-28-writer-quotas-and-fair-queue.md
 * R12.6). Null before the first portion.
 */
export function cardsWaitingTurn(step: IndexingStepView | undefined) {
  if (step?.kind !== 'cards' || step.status !== 'queued') return null;
  const chapters = chapterCounters(step);
  if (!chapters || chapters.done < 1) return null;
  return {
    state: 'Ждёт очереди',
    message: `Карточки строятся по очереди с другими книгами: готово ${chapters.done} из ${chapters.total} глав. Можно закрыть страницу — работа продолжится.`,
  };
}
