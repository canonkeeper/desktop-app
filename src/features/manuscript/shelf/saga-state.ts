import type { IndexingStatus } from '../../../entities/manuscript/api';
import {
  chaptersOf,
  closeWords,
  readyWords,
  shelfReadingLine,
  waitingLine,
} from '../../../entities/manuscript/preparation-copy';

/**
 * The one line of the state of the saga on the shelf (specs/2026-11-10-ux3-saga-state.md SS2.1): what the helper does with
 * the books, in the words of the author and the counts of the server. `ask` — a question may be asked already; `retry` —
 * the reading stopped and «Дочитать» goes on from the chapters read.
 */
export type SagaStateView =
  | { kind: 'loading'; title: string; note: string }
  | { kind: 'reading'; title: string; note: string; ask: boolean; progress: number | null }
  | { kind: 'ready'; title: string; note: string }
  /** The reading waits for the helper (specs/2026-11-12-desk-window.md WN3.2): one line, nothing to press. */
  | { kind: 'waiting'; title: string; note: string }
  | { kind: 'stalled'; title: string; note: string; retry: boolean; supportCode: string | null };

export interface SagaStateInput {
  status: IndexingStatus | undefined;
  /** The titles of the books of the shelf. */
  bookTitle: (bookId: string) => string | undefined;
  /** The file of a new book this tab sends, while the server still parses it. */
  fileName?: string | null;
  /** A chat of the saga was started after it was read: the line of the end has been answered. */
  askedSinceReady: boolean;
  /**
   * A new book is being put on the shelf — an import of the server's Jobs of the saga in work — and the server does not
   * count its reading yet (specs/2026-11-11-import-speed.md IS2.3).
   */
  importing?: boolean;
  /** The status of the book that stopped, read by its book: its chapters read and the step that failed. */
  stalledBook?: IndexingStatus;
}

/** Why the reading stopped, in the words of the author, by the step the server says failed or was stopped. */
function stallReason(book: IndexingStatus | undefined) {
  const steps = book?.steps ?? [];
  if (steps.some((step) => step.status === 'failed')) return 'Чтение прервалось на нашей стороне.';
  if (steps.some((step) => step.status === 'canceled')) return 'Чтение остановлено.';
  return 'Чтение прервалось.';
}

/**
 * The table of SS2.1: the reading (with the file of a new book), its end until a question is asked, and its stop; before
 * the reading, a new book on its way to the shelf (IS2.3).
 */
export function sagaStateOf({ askedSinceReady, bookTitle, fileName, importing, stalledBook, status }: SagaStateInput): SagaStateView | null {
  if (!status) return null;
  const preparation = status.preparation;
  if (!preparation && importing) {
    return { kind: 'loading', title: loadingBookLine(fileName), note: 'Сейчас она появится здесь, и помощник начнёт её читать' };
  }
  const waiting = waitingLine(preparation);
  if (waiting) return { kind: 'waiting', title: waiting, note: '' };
  if (preparation) {
    const parsing = !preparation.currentBookId;
    return {
      kind: 'reading',
      title: shelfReadingLine(preparation, { bookTitle, fileName }),
      note: closeWords(preparation.letterPromised),
      ask: preparation.askable,
      progress: parsing || preparation.chaptersTotal === 0 ? null : preparation.chaptersRead / preparation.chaptersTotal,
    };
  }
  const context = status.published.exists ? status.published.context : null;
  if (context?.state === 'partial') {
    const failed = context.retryableFailedBook;
    if (!failed) {
      return { kind: 'stalled', title: 'Помощник прочитал не всю сагу — отвечает по прочитанной части', note: '', retry: false, supportCode: null };
    }
    const counters = stalledBook?.steps.find((step) => step.kind === 'cards')?.counters;
    const count = counters?.indexedChapters !== undefined && counters.totalChapters !== undefined
      ? `: ${chaptersOf(counters.indexedChapters, counters.totalChapters)}` : '';
    const title = bookTitle(failed.bookId);
    const supportCode = stalledBook?.steps.find((step) => step.status === 'failed')?.jobId ?? null;
    return {
      kind: 'stalled',
      title: `Не дочитал${title ? ` «${title}»` : ' книгу'}${count}. ${stallReason(stalledBook)}`,
      note: 'Прочитанные главы останутся — дочитать можно сейчас.',
      retry: true,
      supportCode,
    };
  }
  if (status.sagaReady && !askedSinceReady) {
    const words = readyWords(status.sagaReady);
    return { kind: 'ready', title: words.title, note: words.facts };
  }
  return null;
}

/** The line of a new book on its way to the shelf, with its file when this tab sends it. */
function loadingBookLine(fileName: string | null | undefined) {
  return fileName ? `Загружаю «${fileName}» в сагу…` : 'Загружаю книгу в сагу…';
}

/** What the screen reader hears once per state of the line, not at every count (SS2.7). */
export function sagaStateAnnouncement(view: SagaStateView | null) {
  if (!view) return '';
  return view.kind === 'reading' ? 'Помощник читает книгу' : view.title;
}
