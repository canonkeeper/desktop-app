// SPDX-License-Identifier: AGPL-3.0-only
import { describe, expect, it } from 'vitest';
import { createMockApiClient } from '../index';
import type { MockScenarioControl } from '../scenarios';

const zone = 'Europe/Moscow';

/** The computer of the app with its local author signed in and the journal of the scenario. */
async function theApp(sendLog?: MockScenarioControl['sendLog']) {
  const api = createMockApiClient();
  api.startDesktop();
  if (sendLog) api.setScenario({ ...api.getScenario(), sendLog });
  await api.openDesktopSession({ code: 'code-of-the-shell' });
  return api;
}

// specs/2026-11-14-desk-journal.md JR3.1, JR3.3: the mock of the app answers every state of «Журнал» — nothing went;
// cleared and empty; cleared with rows since; a work that goes on; the first time to a helper; two sagas; a second page.
describe('the journal of the mock of the app', () => {
  it('answers the journal of its computer by default', async () => {
    const api = await theApp();
    const page = await api.listSendLog({ zone });
    expect(page.rows.length).toBe(3);
    expect(page).toMatchObject({ next: null, clearedAt: null, sagas: 1 });
  });

  it('answers a journal where nothing went', async () => {
    const page = await (await theApp('nothing')).listSendLog({ zone });
    expect(page).toEqual({ rows: [], next: null, clearedAt: null, sagas: 0 });
  });

  it('answers a journal cleared and empty, and cleared with rows since', async () => {
    const empty = await (await theApp('cleared')).listSendLog({ zone });
    expect(empty.rows).toEqual([]);
    expect(empty.clearedAt).toEqual(expect.any(String));
    const rows = await (await theApp('cleared-rows')).listSendLog({ zone });
    expect(rows.rows.length).toBe(3);
    expect(rows.clearedAt).toEqual(expect.any(String));
  });

  it('answers a full journal: two sagas, a work that goes on, the first time, a second page', async () => {
    const api = await theApp('full');
    const first = await api.listSendLog({ zone });
    expect(first.rows).toHaveLength(50);
    expect(first.sagas).toBe(2);
    expect(first.rows.some((row) => row.running)).toBe(true);
    expect(first.rows.some((row) => row.firstTime && row.recipients.length > 1)).toBe(true);
    expect(first.next).toEqual(expect.any(String));
    const second = await api.listSendLog({ zone, cursor: first.next! });
    expect(second.rows.length).toBeGreaterThan(0);
    expect(second.next).toBeNull();
    expect(new Set([...first.rows, ...second.rows].map((row) => row.key)).size).toBe(first.rows.length + second.rows.length);
    const checks = await api.listSendLog({ zone, kind: 'check' });
    expect(checks.rows.every((row) => row.kind === 'check')).toBe(true);
  });

  // specs/2026-11-15-desk-mcp.md MC4.4: the readings of a program in the full journal — a chapter by the six pages of its
  // answer and the titles of the sagas, a row of its own without a saga; its kind in the choice and in the file.
  it('answers the readings of a program in a full journal by its kind', async () => {
    const api = await theApp('full');
    const programs = await api.listSendLog({ zone, kind: 'program' });
    expect(programs.rows).toHaveLength(2);
    expect(programs.rows.every((row) => row.kind === 'program' && row.recipients[0] === 'Claude Code' && row.firstTime === false)).toBe(true);
    const sagas = programs.rows.find((row) => row.saga === null)!;
    expect(sagas.whatWent).toBe('названия саг: 2');
    const paged = programs.rows.find((row) => row.saga !== null)!;
    expect(paged).toMatchObject({ whatWent: 'Книга 1, гл. 3 „Переправа“, страницами ответа', sendings: 6 });
    const sendings = await api.listSendLogSendings({ zone, row: paged.key });
    expect(sendings.sendings.map((sending) => sending.whatWent)).toEqual(
      Array.from({ length: 6 }, (_, index) => `Книга 1, гл. 3 „Переправа“ · страница ${index + 1} из 6 ответа`),
    );
    expect(sendings.sendings.every((sending) => sending.outcome === null && !sending.repeat)).toBe(true);
    const file = await api.getSendLogFile({ zone, kind: 'program' });
    expect(file.text.split('\n')[0]).toBe('Журнал отправок CanonKeeper · Свой Claude или Codex');
    expect(file.text).toContain('Книга 1, гл. 3 „Переправа“, страницами ответа');
  });

  it('clears whatever journal it shows, and names the choice of its file', async () => {
    const api = await theApp('full');
    const file = await api.getSendLogFile({ zone, kind: 'materials' });
    expect(file.text).toBe('Журнал отправок CanonKeeper · Ваши материалы\nВ этом виде ничего не уходило\n');
    await api.clearSendLog();
    const page = await api.listSendLog({ zone });
    expect(page.rows).toEqual([]);
    expect(page.clearedAt).toEqual(expect.any(String));
  });
});
