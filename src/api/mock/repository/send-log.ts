// SPDX-License-Identifier: AGPL-3.0-only
import type { OperationResponse } from '../../../shared/api';
import { MockApiError } from '../scenarios';
import type { MockRepositoryContext } from './context';
import { mockFullSendLog, type MockSendLogRow } from './send-log-fixture';

/** The pages of the journal, as the server gives them (JR2.2, JR2.4). */
const rowsPage = 50;
const sendingsPage = 100;

/** The operations of the journal of the window of the app (specs/2026-11-14-desk-journal.md JR2.2–JR2.6). */
export function createSendLogOperations(context: MockRepositoryContext) {
  function signedIn(operationId: 'listSendLog' | 'listSendLogSendings' | 'getSendLogFile' | 'clearSendLog') {
    context.applyOperation(operationId);
    if (!context.state.session) throw new MockApiError(401, 'Сессия отсутствует.', operationId, 'auth.session_required');
  }

  /**
   * The journal the scenario shows (sendLog): nothing; cleared and empty; cleared with rows since; full — or the journal of
   * the computer of the mock. A clearing clears every one of them.
   */
  function journal(): { rows: MockSendLogRow[]; clearedAt: string | null } {
    const { rows, clearedAt } = context.state.sendLog;
    const earlier = new Date(Date.now() - 3 * 24 * 60 * 60 * 1000);
    earlier.setHours(14, 5, 0, 0);
    switch (context.scenario.sendLog) {
      case 'nothing': return clearedAt ? { rows, clearedAt } : { rows: [], clearedAt: null };
      case 'cleared': return { rows: clearedAt ? rows : [], clearedAt: clearedAt ?? earlier.toISOString() };
      case 'cleared-rows': return { rows, clearedAt: clearedAt ?? earlier.toISOString() };
      case 'full': return clearedAt ? { rows, clearedAt } : { rows: mockFullSendLog(), clearedAt: null };
      default: return { rows, clearedAt };
    }
  }

  function rowsOf(kind: string | undefined) {
    return journal().rows.filter((entry) => !kind || entry.row.kind === kind);
  }

  /** A page from a cursor: the cursor is how many rows the pages before gave. */
  function pageOf<T>(all: T[], cursor: string | undefined, size: number) {
    const from = cursor ? Number(cursor) : 0;
    if (!Number.isInteger(from) || from < 0) return null;
    const next = from + size < all.length ? String(from + size) : null;
    return { items: all.slice(from, from + size), next };
  }

  async function listSendLog(query: { kind?: string; zone?: string; cursor?: string } = {}): Promise<OperationResponse<'listSendLog', '200'>> {
    signedIn('listSendLog');
    const all = rowsOf(query.kind).map((entry) => entry.row);
    const page = query.zone ? pageOf(all, query.cursor, rowsPage) : null;
    if (!page) throw new MockApiError(400, 'the zone or the cursor is not known', 'listSendLog', 'validation.failed');
    const sagas = new Set(journal().rows.flatMap((entry) => (entry.row.saga ? [entry.row.saga.title] : []))).size;
    return { rows: page.items, next: page.next, clearedAt: journal().clearedAt, sagas };
  }

  async function listSendLogSendings(query: { row?: string; zone?: string; cursor?: string } = {}): Promise<OperationResponse<'listSendLogSendings', '200'>> {
    signedIn('listSendLogSendings');
    const entry = journal().rows.find((candidate) => candidate.row.key === query.row);
    const page = entry && query.zone ? pageOf(entry.sendings, query.cursor, sendingsPage) : null;
    if (!page) throw new MockApiError(400, 'the row is not known', 'listSendLogSendings', 'validation.failed');
    return { sendings: page.items, next: page.next };
  }

  async function getSendLogFile(query: { kind?: string; zone?: string } = {}): Promise<OperationResponse<'getSendLogFile', '200'>> {
    signedIn('getSendLogFile');
    const choice = {
      question: 'Вопрос', reading: 'Чтение книги', materials: 'Ваши материалы', check: 'Проверка связи', program: 'Свой Claude или Codex',
    }[query.kind ?? ''] ?? 'Все виды';
    const lines = rowsOf(query.kind).map((entry) => `${entry.row.startedAt} · ${entry.row.whatWent}`);
    const empty = query.kind ? 'В этом виде ничего не уходило' : 'Ещё ничего не уходило';
    return {
      fileName: 'canonkeeper-journal-2026-11-13.txt',
      text: [`Журнал отправок CanonKeeper · ${choice}`, ...(lines.length ? lines : [empty])].join('\n') + '\n',
    };
  }

  async function clearSendLog(): Promise<OperationResponse<'clearSendLog', '200'>> {
    signedIn('clearSendLog');
    const clearedAt = new Date().toISOString();
    context.state.sendLog = { rows: [], clearedAt };
    return { clearedAt };
  }

  return { listSendLog, listSendLogSendings, getSendLogFile, clearSendLog };
}
