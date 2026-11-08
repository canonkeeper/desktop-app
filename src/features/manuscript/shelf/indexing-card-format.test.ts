import { describe, expect, it } from 'vitest';
import {
  activeIndexingStepText,
  cardsWaitingTurn,
  elapsedTime,
  formatRemaining,
  indexingStepCompactFacts,
  indexingStepFacts,
  partialResultMessage,
  remainingPreparationEstimate,
  stepTimingLabel,
  type IndexingStepView,
} from './indexing-card-format';

const startedAt = '2026-08-10T09:00:00.000Z';
const now = Date.parse(startedAt);

function cards(totalChapters: number | undefined, indexedChapters: number, elapsedSeconds = 0): [IndexingStepView, number] {
  return [{
    kind: 'cards',
    progress: null,
    startedAt,
    status: 'running',
    counters: totalChapters === undefined ? undefined : { indexedChapters, totalChapters },
  }, now + elapsedSeconds * 1_000];
}

// UC2.2 Logic Details: by the owner measurements until three cards exist, then by the observed pace.
describe('remainingPreparationEstimate', () => {
  it.each([
    ['12 chapters, none ready', cards(12, 0), '≈ 3 мин'],
    ['629 chapters, none ready', cards(629, 0), '≈ 32 мин'],
    ['100 chapters, 50 ready in 150 s', cards(100, 50, 150), '≈ 3 мин'],
  ])('%s', (_label, [step, at], expected) => {
    expect(formatRemaining(remainingPreparationEstimate(step, at)!)).toBe(expected);
  });

  it('waits for the chapters before it names a time', () => {
    const [step, at] = cards(undefined, 0);
    expect(remainingPreparationEstimate(step, at)).toBeNull();
    expect(remainingPreparationEstimate({ kind: 'import', progress: null, status: 'running' }, now)).toBeNull();
  });

  it('estimates the whole book by the measurements while import and text search run', () => {
    expect(remainingPreparationEstimate({
      kind: 'import', progress: null, status: 'running', counters: { totalChapters: 100, parsedChapters: 40 },
    }, now)).toBe(300);
    expect(remainingPreparationEstimate({
      kind: 'text_index', progress: 0.5, status: 'running', counters: { indexedChapters: 50, totalChapters: 12 },
    }, now)).toBe(180);
  });

  it('rounds to the writer units', () => {
    expect(formatRemaining(45)).toBe('меньше минуты');
    expect(formatRemaining(61)).toBe('≈ 2 мин');
    expect(formatRemaining(60 * 60)).toBe('≈ 60 мин');
    expect(formatRemaining(75 * 60)).toBe('≈ 1 ч 20 мин');
    expect(formatRemaining(119 * 60 + 30)).toBe('≈ 2 ч');
  });
});

describe('stepTimingLabel', () => {
  it.each(['pending', 'queued'] as const)('hides stale runtime while a step is %s', (status) => {
    expect(stepTimingLabel({
      kind: 'cards',
      progress: null,
      startedAt: '2026-08-10T09:00:00.000Z',
      status,
    }, Date.parse('2026-08-10T09:05:00.000Z'))).toBeNull();
  });

  it('omits terminal duration when completion time is unavailable', () => {
    const step = {
      kind: 'cards' as const,
      progress: null,
      startedAt: '2026-08-10T09:00:00.000Z',
      status: 'failed' as const,
    };

    expect(stepTimingLabel(step, Date.parse('2026-08-10T09:05:00.000Z'))).toBeNull();
    expect(stepTimingLabel(step, Date.parse('2026-08-10T10:00:00.000Z'))).toBeNull();
  });

  it('keeps elapsed time live while exact cleanup is canceling', () => {
    expect(stepTimingLabel({
      kind: 'cards',
      progress: null,
      startedAt: '2026-08-10T09:00:00.000Z',
      status: 'canceling',
    }, Date.parse('2026-08-10T09:05:00.000Z'))).toBe('Работает 05:00');
  });

  it('freezes terminal duration at the explicit completion time', () => {
    const step = {
      completedAt: '2026-08-10T09:00:30.000Z',
      kind: 'cards' as const,
      progress: null,
      startedAt: '2026-08-10T09:00:00.000Z',
      status: 'failed' as const,
    };

    expect(stepTimingLabel(step, Date.parse('2026-08-10T09:05:00.000Z'))).toBe('00:30');
    expect(stepTimingLabel(step, Date.parse('2026-08-10T10:00:00.000Z'))).toBe('00:30');
  });
});

describe('indexingStepFacts', () => {
  it('formats active responsive counters without inventing equal import totals', () => {
    expect(activeIndexingStepText({
      kind: 'text_index', status: 'running', progress: 0.5,
      counters: { indexedChapters: 7, totalChapters: 14 },
    })).toMatchObject({ detail: 'Готово для поиска 7 из 14 глав.', mobileMeta: '7 / 14 · Сейчас' });
    expect(activeIndexingStepText({
      kind: 'import', status: 'running', progress: null,
      counters: { parsedChapters: 4, savedChapters: 3, totalChapters: 6 },
    }).mobileMeta).toBe('разобрано 4/6 · сохранено 3/6 · Сейчас');
  });

  it('keeps pending active rows unstarted despite stale timing', () => {
    expect(activeIndexingStepText({
      kind: 'cards', status: 'pending', progress: null,
      startedAt: '2026-08-10T09:00:00.000Z',
    })).toMatchObject({ status: 'Не начат · —', mobileMeta: 'Не начат · —' });
  });

  it('returns only factual counters for compact terminal rows', () => {
    expect(indexingStepFacts({
      counters: { indexedChapters: 9, totalChapters: 14 },
      kind: 'text_index',
      progress: null,
      status: 'partial',
    })).toBe('Готово глав для поиска: 9 из 14');
    expect(indexingStepFacts({ kind: 'ready', progress: null, status: 'succeeded' })).toBeNull();
    expect(indexingStepFacts({
      counters: { indexedChapters: 3, totalChapters: 8 },
      kind: 'cards',
      progress: 3 / 8,
      status: 'running',
    })).toBe('Карточки готовы для 3 из 8 глав');
  });

  it('keeps mobile import and text counters on one compact line', () => {
    expect(indexingStepCompactFacts({
      counters: { bytesTransferred: 2_800_000, parsedChapters: 14, savedChapters: 14, totalChapters: 14 },
      kind: 'import',
      progress: null,
      status: 'succeeded',
    })).toBe('2,8 МБ · разобрано 14/14 · сохранено 14/14');
    expect(indexingStepCompactFacts({
      counters: { indexedChapters: 14, totalChapters: 14 },
      kind: 'text_index',
      progress: null,
      status: 'succeeded',
    })).toBe('14/14 глав в поиске');
  });
});

describe('terminal timing and partial results', () => {
  it('labels a non-zero subsecond duration honestly', () => {
    expect(elapsedTime('2026-08-10T09:00:00.000Z', '2026-08-10T09:00:00.500Z')).toBe('меньше 1 с');
  });

  it('names what a partial version already offers and what it misses', () => {
    expect(partialResultMessage([
      { kind: 'text_index', progress: 1, status: 'succeeded' },
      { kind: 'cards', progress: 0.5, status: 'pending' },
    ])).toBe('Доступно: поиск по тексту. Не готово: карточки части глав. Сейчас обработка не выполняется.');
  });
});

describe('cards waiting for their turn (quota spec R12.6)', () => {
  it('names the built chapters after a portion and nothing before it', () => {
    const step = (indexedChapters: number) => ({
      kind: 'cards' as const, status: 'queued' as const, progress: null, counters: { indexedChapters, totalChapters: 50 },
    });
    expect(cardsWaitingTurn(step(0))).toBeNull();
    expect(cardsWaitingTurn(step(12))).toEqual({
      state: 'Ждёт очереди',
      message: 'Карточки строятся по очереди с другими книгами: готово 12 из 50 глав. Можно закрыть страницу — работа продолжится.',
    });
    expect(cardsWaitingTurn({ ...step(12), status: 'running' })).toBeNull();
  });
});
