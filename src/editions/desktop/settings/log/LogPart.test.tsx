// SPDX-License-Identifier: AGPL-3.0-only
import { focusManager, QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { CanonKeeperApiClient, OperationResponse, Schema } from '../../../../shared/api';
import { ToastsProvider } from '../../../../ui/feedback/Toast';
import { LogPart } from './LogPart';
import { dayWords } from './log-words';

const saved = vi.hoisted(() => ({ files: [] as Array<{ fileName: string; blob: Blob }> }));
// saveFile hands the file over at once, and so does its mock: a mock that read the text before it noted the file noted it
// after the line «сохранён» whenever the machine held the thread a millisecond (specs/2026-11-15-desk-hygiene1.md HG1.3).
vi.mock('../../../../ui/save-file', () => ({
  saveFile: ({ blob, fileName }: { blob: Blob; fileName: string }) => {
    saved.files.push({ fileName, blob });
  },
}));

/** The files the part handed over, with their texts. */
function savedFiles() {
  return Promise.all(saved.files.map(async ({ fileName, blob }) => ({ fileName, text: await blob.text() })));
}

type Page = OperationResponse<'listSendLog', '200'>;
type Row = Schema<'SendLogRow'>;

/**
 * A moment of the computer of the test: the day before `days` days at the time — whatever its zone. The rows stand at 00:30
 * and 23:30 of the clock of the computer: the days by UTC instead of the days of the computer move one of them to another
 * day in every zone but UTC itself.
 */
function local(days: number, hours: number, minutes: number) {
  const moment = new Date();
  moment.setDate(moment.getDate() - days);
  moment.setHours(hours, minutes, 0, 0);
  return moment.toISOString();
}

function row(overrides: Partial<Row>): Row {
  return {
    key: 'job|day', kind: 'reading', startedAt: local(0, 0, 30), lastAt: local(0, 0, 50), saga: { title: 'Северный предел', deleted: false }, chat: null,
    whatWent: 'Книга 1 — 4 главы целиком', recipients: ['provider.example.com'], bytes: 48_000, sendings: 8, running: false, firstTime: false, own: null,
    ...overrides,
  };
}

const question = row({
  key: 'question|today', kind: 'question', startedAt: local(0, 0, 30), chat: { title: 'Кто стоит на заставе?', deleted: false },
  whatWent: 'ваш вопрос; Книга 2, гл. 12 „Белый порт“, абзацы 3–9', bytes: 6_200, sendings: 2,
  own: { requests: 2, tokens: 48_000, estimated: false, costs: [{ currency: 'RUB', micros: 3_000_000 }] },
});
const reading = row({ key: 'reading|yesterday', startedAt: local(1, 23, 30), running: true, firstTime: true });
const older = row({ key: 'check|older', kind: 'check', startedAt: local(5, 0, 30), saga: null, whatWent: 'без вашего текста', bytes: 1_200, own: null });

function page(rows: Row[], extra: Partial<Page> = {}): Page {
  return { rows, next: null, clearedAt: null, sagas: 1, ...extra };
}

function client(overrides: Partial<CanonKeeperApiClient> = {}) {
  return {
    listSendLog: vi.fn(async () => page([question, reading, older])),
    listSendLogSendings: vi.fn(async () => ({
      sendings: [
        { at: local(0, 0, 30), recipient: 'provider.example.com', bytes: 2_100, whatWent: 'ваш вопрос', firstTime: false, outcome: null, repeat: false },
        { at: local(0, 0, 31), recipient: 'provider.example.com', bytes: 4_100, whatWent: 'Книга 2, гл. 12 „Белый порт“, абзацы 3–9', firstTime: false, outcome: 'ответ оборвался', repeat: false },
        { at: local(0, 0, 32), recipient: 'provider.example.com', bytes: 4_100, whatWent: 'Книга 2, гл. 12 „Белый порт“, абзацы 3–9', firstTime: false, outcome: null, repeat: true },
      ],
      next: 'more-sendings',
    })),
    getSendLogFile: vi.fn(async () => ({ fileName: 'canonkeeper-journal-2026-11-13.txt', text: 'Журнал отправок CanonKeeper\n' })),
    clearSendLog: vi.fn(async () => ({ clearedAt: new Date().toISOString() })),
    ...overrides,
  };
}

/** The part under the query client of the app: its reads stay fresh for 30 s (src/app/AppProviders.tsx). */
function renderLog(api: ReturnType<typeof client>, kind: string | null = null) {
  const choose = vi.fn();
  const queries = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: 30_000 } } });
  const view = render(
    <QueryClientProvider client={queries}>
      <ToastsProvider><LogPart api={api as unknown as CanonKeeperApiClient} choose={choose} kind={kind} /></ToastsProvider>
    </QueryClientProvider>,
  );
  return { choose, queries, view };
}

const zone = Intl.DateTimeFormat().resolvedOptions().timeZone;

beforeEach(() => {
  saved.files = [];
});

afterEach(() => {
  vi.useRealTimers();
  focusManager.setFocused(undefined);
});

// specs/2026-11-14-desk-journal.md JR3.1, JR3.2: the rows by the days of the computer of the author, each «время · вид · что ушло ·
// получатель · размер», the chat of a question, the mark of a work that goes and of the first time, the details of a row.
describe('«Журнал» of «Настройки»', () => {
  it('says what the journal is and lays the rows by days in the zone of the computer', async () => {
    const api = client();
    renderLog(api);
    expect(await screen.findByText(dayWords(question.startedAt))).toBeTruthy();
    expect(dayWords(question.startedAt)).toMatch(/^Сегодня, /u);
    expect(dayWords(reading.startedAt)).toMatch(/^Вчера, /u);
    expect(screen.getByText('Что из ваших книг и чатов приложение отправляло помощнику и отдавало вашим программам на этом компьютере: когда, кому и что.')).toBeTruthy();
    expect(screen.getByText('Места названы так, как они сейчас стоят в книгах.')).toBeTruthy();
    expect(screen.getByText(dayWords(reading.startedAt))).toBeTruthy();
    expect(screen.getByRole('heading', { name: dayWords(older.startedAt) })).toBeTruthy();
    expect(screen.getAllByRole('heading', { level: 3 })).toHaveLength(3);
    expect(api.listSendLog).toHaveBeenCalledWith({ zone }, expect.anything());
    const today = screen.getByRole('region', { name: dayWords(question.startedAt) });
    const line = within(today).getByRole('listitem');
    expect(line.textContent).toContain('00:30');
    expect(line.textContent).toContain('Вопрос');
    expect(line.textContent).toContain('ваш вопрос; Книга 2, гл. 12 „Белый порт“, абзацы 3–9');
    expect(line.textContent).toContain('provider.example.com');
    expect(line.textContent).toContain('6 КБ');
    expect(line.textContent).toContain('«Кто стоит на заставе?»');
    // One saga in the journal: no row names it.
    expect(screen.queryByText(/Северный предел/u)).toBeNull();
    const yesterday = screen.getByRole('region', { name: dayWords(reading.startedAt) });
    expect(within(yesterday).getByText('идёт · весь прежний разговор этого чата — впервые этому получателю')).toBeTruthy();
    // No key of a chapter, «¶», step or Job, no tokens before the details.
    expect(document.body.textContent).not.toMatch(/¶|Job|card_pass|токен/u);
  });

  it('names the saga of a row only while the journal names several, and a deleted chat', async () => {
    const api = client({ listSendLog: vi.fn(async () => page([{ ...question, chat: { title: null, deleted: true } }], { sagas: 2 })) });
    renderLog(api);
    const line = (await screen.findAllByRole('listitem'))[0]!;
    expect(line.textContent).toContain('удалённый чат · «Северный предел»');
  });

  it('writes the kind into the address and asks the rows of the kind of the address', async () => {
    const api = client();
    const { choose } = renderLog(api, 'reading');
    await waitFor(() => expect(api.listSendLog).toHaveBeenCalledWith({ zone, kind: 'reading' }, expect.anything()));
    fireEvent.click(await screen.findByRole('button', { name: /^Вид отправок/u }));
    fireEvent.click(await screen.findByRole('option', { name: 'Проверка связи' }));
    expect(choose).toHaveBeenLastCalledWith('check');
    fireEvent.click(screen.getByRole('button', { name: /^Вид отправок/u }));
    fireEvent.click(await screen.findByRole('option', { name: 'Все виды' }));
    expect(choose).toHaveBeenLastCalledWith(null);
  });

  it('drops a kind of the address there is not', async () => {
    const api = client();
    renderLog(api, 'letters');
    await waitFor(() => expect(api.listSendLog).toHaveBeenCalledWith({ zone }, expect.anything()));
  });

  it('shows the next page while the server gives a cursor', async () => {
    const listSendLog = vi.fn(async (query: { cursor?: string }) => (query.cursor ? page([older]) : page([question], { next: 'cursor-of-the-test' })));
    const api = client({ listSendLog: listSendLog as never });
    renderLog(api);
    fireEvent.click(await screen.findByRole('button', { name: 'Показать ещё' }));
    expect(await screen.findByRole('heading', { name: dayWords(older.startedAt) })).toBeTruthy();
    expect(listSendLog).toHaveBeenLastCalledWith({ zone, cursor: 'cursor-of-the-test' }, expect.anything());
    expect(screen.queryByRole('button', { name: 'Показать ещё' })).toBeNull();
  });

  it('reads the rows again every 30 seconds while it is open', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const api = client();
    renderLog(api);
    await screen.findAllByRole('listitem');
    const before = vi.mocked(api.listSendLog).mock.calls.length;
    await act(async () => {
      await vi.advanceTimersByTimeAsync(30_000);
    });
    await waitFor(() => expect(vi.mocked(api.listSendLog).mock.calls.length).toBeGreaterThan(before));
  });

  it('opens the details of a row: its sendings, the conversation, the numbers of the helper, the outcomes and «повтор»', async () => {
    const api = client();
    renderLog(api);
    const line = (await screen.findAllByRole('listitem'))[0]!;
    fireEvent.click(within(line).getByRole('button', { name: 'Подробно' }));
    expect(await within(line).findByText('2 отправки · всего 6 КБ')).toBeTruthy();
    expect(within(line).getByText('Вместе с вопросом уходят указания помощнику и прежний разговор этого чата, если он был')).toBeTruthy();
    expect(within(line).getByText('2 запроса · 48 тыс. токенов · ≈ 3 ₽')).toBeTruthy();
    expect(await within(line).findByText(/ответ оборвался$/u)).toBeTruthy();
    expect(within(line).getByText(/повтор$/u)).toBeTruthy();
    expect(api.listSendLogSendings).toHaveBeenCalledWith({ row: question.key, zone }, expect.anything());
    // Board 44·10: a sending is «время · что ушло · размер · исход» — the helper of a row of one helper is not said again;
    // an open row has no «Подробно», its «Свернуть» is the last of its details.
    const sendings = within(line).getAllByRole('listitem').map((item) => item.textContent);
    expect(sendings).toEqual([
      '00:30 · ваш вопрос · 2 КБ',
      '00:31 · Книга 2, гл. 12 „Белый порт“, абзацы 3–9 · 4 КБ · ответ оборвался',
      '00:32 · Книга 2, гл. 12 „Белый порт“, абзацы 3–9 · 4 КБ · повтор',
    ]);
    expect(within(line).queryByRole('button', { name: 'Подробно' })).toBeNull();
    expect(within(line).getAllByRole('button').at(-1)?.textContent).toBe('Свернуть');
    fireEvent.click(within(line).getByRole('button', { name: 'Показать ещё' }));
    await waitFor(() => expect(api.listSendLogSendings).toHaveBeenLastCalledWith({ row: question.key, zone, cursor: 'more-sendings' }, expect.anything()));
    fireEvent.click(within(line).getByRole('button', { name: 'Свернуть' }));
    expect(within(line).queryByText('2 отправки · всего 6 КБ')).toBeNull();
    expect(within(line).getByRole('button', { name: 'Подробно' })).toBeTruthy();
  });

  it('names the helper of each sending of a row that went to several', async () => {
    const api = client({ listSendLog: vi.fn(async () => page([{ ...question, recipients: ['provider.example.com', 'сервер CanonKeeper'] }])) });
    renderLog(api);
    const line = (await screen.findAllByRole('listitem'))[0]!;
    fireEvent.click(within(line).getByRole('button', { name: 'Подробно' }));
    expect(await within(line).findByText('00:30 · ваш вопрос · provider.example.com · 2 КБ')).toBeTruthy();
  });

  it('reads the first page again on the return to the window, though its rows are fresh', async () => {
    const api = client();
    renderLog(api);
    await screen.findAllByRole('listitem');
    const before = vi.mocked(api.listSendLog).mock.calls.length;
    act(() => {
      focusManager.setFocused(false);
      focusManager.setFocused(true);
    });
    await waitFor(() => expect(vi.mocked(api.listSendLog).mock.calls.length).toBeGreaterThan(before));
    expect(vi.mocked(api.listSendLog).mock.calls.at(-1)?.[0]).toEqual({ zone });
  });

  it('reads again only the first page, not the pages «Показать ещё» loaded', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const fresh = row({ key: 'reading|fresh', startedAt: local(0, 0, 40), whatWent: 'Книга 3 — 1 глава целиком' });
    let reads = 0;
    const listSendLog = vi.fn(async (query: { cursor?: string }) => {
      if (query.cursor) return page([older]);
      reads += 1;
      return reads === 1 ? page([question, reading], { next: 'cursor-of-the-test' }) : page([fresh, question], { next: 'cursor-of-the-new-first-page' });
    });
    const api = client({ listSendLog: listSendLog as never });
    renderLog(api);
    fireEvent.click(await screen.findByRole('button', { name: 'Показать ещё' }));
    expect(await screen.findByRole('heading', { name: dayWords(older.startedAt) })).toBeTruthy();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(30_000);
    });
    expect(await screen.findByText('Книга 3 — 1 глава целиком')).toBeTruthy();
    expect(listSendLog.mock.calls.filter(([query]) => query.cursor)).toHaveLength(1);
    // The row the new first page pushed out stays, and so does the page «Показать ещё» loaded.
    expect(screen.getByText(dayWords(reading.startedAt))).toBeTruthy();
    expect(screen.getByRole('heading', { name: dayWords(older.startedAt) })).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Показать ещё' })).toBeNull();
  });

  it('reads the open details of a row that goes on again when the count of its sendings grows', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    let count = 2;
    const going = { ...question, running: true };
    const line = (index: number) => ({ at: local(0, 0, 30 + index), recipient: 'provider.example.com', bytes: 1_000, whatWent: `отправка ${index + 1}`, firstTime: false, outcome: null, repeat: false });
    const api = client({
      listSendLog: vi.fn(async () => page([{ ...going, sendings: count }])),
      listSendLogSendings: vi.fn(async () => ({ sendings: Array.from({ length: count }, (_, index) => line(index)), next: null })),
    });
    renderLog(api);
    const item = (await screen.findAllByRole('listitem'))[0]!;
    fireEvent.click(within(item).getByRole('button', { name: 'Подробно' }));
    expect(await within(item).findByText(/отправка 2 · 1000 Б$/u)).toBeTruthy();
    count = 3;
    await act(async () => {
      await vi.advanceTimersByTimeAsync(30_000);
    });
    expect(await within(item).findByText('3 отправки · всего 6 КБ')).toBeTruthy();
    expect(await within(item).findByText(/отправка 3 · 1000 Б$/u)).toBeTruthy();
  });

  it('keeps the rows when a read again fails', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    let fail = false;
    const listSendLog = vi.fn(async () => {
      if (fail) throw new Error('offline');
      return page([question]);
    });
    const { queries } = renderLog(client({ listSendLog }));
    await screen.findAllByRole('listitem');
    fail = true;
    await act(async () => {
      await vi.advanceTimersByTimeAsync(30_000);
    });
    await waitFor(() => expect(listSendLog.mock.calls.length).toBeGreaterThan(1));
    expect(screen.queryByRole('alert')).toBeNull();
    expect(screen.getAllByRole('listitem')).toHaveLength(1);
    // A read of all the rows again (after a clearing) that fails leaves them too: the block of the failure is for no rows.
    await act(async () => {
      await queries.invalidateQueries();
    });
    await waitFor(() => expect(listSendLog.mock.calls.length).toBeGreaterThan(2));
    expect(screen.queryByRole('alert')).toBeNull();
    expect(screen.getAllByRole('listitem')).toHaveLength(1);
  });

  it('writes an estimate of the tokens with «≈» and no sum without the prices', async () => {
    const api = client({ listSendLog: vi.fn(async () => page([{ ...reading, own: { requests: 6, tokens: 48_000, estimated: true, costs: [] } }])) });
    renderLog(api);
    fireEvent.click(await screen.findByRole('button', { name: 'Подробно' }));
    expect(await screen.findByText('6 запросов · ≈ 48 тыс. токенов')).toBeTruthy();
  });
});

// JR3.3: the states of the part.
describe('the states of «Журнал»', () => {
  it('says nothing went yet, without the choice and the clearing', async () => {
    renderLog(client({ listSendLog: vi.fn(async () => page([])) }));
    expect(await screen.findByText('Ещё ничего не уходило')).toBeTruthy();
    expect(screen.getByText('Здесь появится каждая отправка помощнику и каждое чтение вашей программой: время, куда, что ушло и сколько.')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Очистить журнал…' })).toBeNull();
  });

  it('says when it was cleared, empty or with rows', async () => {
    const clearedAt = new Date(2026, 10, 13, 14, 5).toISOString();
    const empty = renderLog(client({ listSendLog: vi.fn(async () => page([], { clearedAt })) }));
    expect(await screen.findByText('Журнал очищен 13 ноября в 14:05')).toBeTruthy();
    expect(screen.getByText('Новые отправки появятся здесь.')).toBeTruthy();
    empty.view.unmount();
    renderLog(client({ listSendLog: vi.fn(async () => page([question], { clearedAt })) }));
    expect(await screen.findByText('Журнал очищен 13 ноября в 14:05 — раньше записей нет')).toBeTruthy();
  });

  it('gives all the kinds back when a kind has no rows', async () => {
    const { choose } = renderLog(client({ listSendLog: vi.fn(async () => page([])) }), 'materials');
    expect(await screen.findByText('Отправок этого вида нет')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Показать все виды' }));
    expect(choose).toHaveBeenCalledWith(null);
  });

  it('shows the skeleton while it reads, and the failure with «Повторить»', async () => {
    let fail = true;
    const listSendLog = vi.fn(async () => {
      if (fail) throw new Error('offline');
      return page([question]);
    });
    renderLog(client({ listSendLog }));
    expect(document.querySelector('[aria-busy="true"]')).toBeTruthy();
    expect(await screen.findByRole('alert', { name: 'Не открылся журнал' })).toBeTruthy();
    fail = false;
    fireEvent.click(screen.getByRole('button', { name: 'Повторить' }));
    expect(await screen.findAllByRole('listitem')).toHaveLength(1);
  });
});

// JR3.3: «Очистить журнал…» asks; «Отмена» sends nothing; «Очистить журнал» sends one operation and says so; a failure says its
// words and leaves the rows. «Сохранить в файл…» saves the file of the server.
describe('the clearing and the saving of «Журнал»', () => {
  it('asks before it clears, sends nothing on «Отмена» and one operation on «Очистить журнал»', async () => {
    const api = client();
    renderLog(api);
    fireEvent.click(await screen.findByRole('button', { name: 'Очистить журнал…' }));
    const dialog = await screen.findByRole('dialog', { name: 'Очистить журнал?' });
    expect(within(dialog).getByText('Вернуть будет нельзя.')).toBeTruthy();
    expect(within(dialog).getByText('Книги и чаты не изменятся.')).toBeTruthy();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Отмена' }));
    expect(api.clearSendLog).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Очистить журнал…' }));
    fireEvent.click(within(await screen.findByRole('dialog', { name: 'Очистить журнал?' })).getByRole('button', { name: 'Очистить журнал' }));
    expect(await screen.findByText('Журнал очищен')).toBeTruthy();
    expect(api.clearSendLog).toHaveBeenCalledTimes(1);
  });

  it('says a clearing that failed and keeps the rows', async () => {
    const api = client({ clearSendLog: vi.fn(async () => { throw new Error('offline'); }) });
    renderLog(api);
    fireEvent.click(await screen.findByRole('button', { name: 'Очистить журнал…' }));
    fireEvent.click(within(await screen.findByRole('dialog', { name: 'Очистить журнал?' })).getByRole('button', { name: 'Очистить журнал' }));
    expect(await screen.findByText('Журнал не очистился. Попробуйте ещё раз')).toBeTruthy();
    expect(screen.getAllByRole('listitem').length).toBeGreaterThan(0);
  });

  it('saves the file of the server under its name', async () => {
    const api = client();
    renderLog(api, 'question');
    fireEvent.click(await screen.findByRole('button', { name: 'Сохранить в файл…' }));
    expect(await screen.findByText('Журнал сохранён в файл: Загрузки → canonkeeper-journal-2026-11-13.txt')).toBeTruthy();
    expect(api.getSendLogFile).toHaveBeenCalledWith({ zone, kind: 'question' });
    expect(await savedFiles()).toEqual([{ fileName: 'canonkeeper-journal-2026-11-13.txt', text: 'Журнал отправок CanonKeeper\n' }]);
  });

  it('says a saving that failed in its own words', async () => {
    renderLog(client({ getSendLogFile: vi.fn(async () => { throw new Error('offline'); }) }));
    fireEvent.click(await screen.findByRole('button', { name: 'Сохранить в файл…' }));
    expect(await screen.findByText('Журнал не сохранился. Попробуйте ещё раз')).toBeTruthy();
    expect(screen.queryByText('Не открылся журнал')).toBeNull();
    expect(screen.getAllByRole('listitem').length).toBeGreaterThan(0);
  });
});

// specs/2026-11-15-desk-mcp.md MC4.4, Data «журнал»: a row of the readings of a program is its kind «Свой Claude или Codex», to
// the program, with the line under it; its details count the calls; the choice has the kind.
describe('the readings of a program in «Журнал»', () => {
  const program = row({
    key: 'program|today', kind: 'program', startedAt: local(0, 0, 40), whatWent: 'Книга 1, гл. 3 „Переправа“, страницами ответа',
    recipients: ['Claude Code'], bytes: 216_000, sendings: 6, own: null,
  });

  it('shows the row of a program with its line and counts its calls', async () => {
    const api = client({
      listSendLog: vi.fn(async () => page([program])),
      listSendLogSendings: vi.fn(async () => ({
        sendings: [{ at: local(0, 0, 41), recipient: 'Claude Code', bytes: 36_000, whatWent: 'Книга 1, гл. 3 „Переправа“ · страница 2 из 6 ответа', firstTime: false, outcome: null, repeat: false }],
        next: null,
      })),
    });
    renderLog(api);
    const line = (await screen.findByText('Книга 1, гл. 3 „Переправа“, страницами ответа')).closest('li')!;
    expect(within(line).getByText('Свой Claude или Codex')).toBeTruthy();
    expect(within(line).getByText('Claude Code')).toBeTruthy();
    expect(within(line).getByText('Отдано вашей программе — куда она отправит это дальше, решают она и её провайдер')).toBeTruthy();
    fireEvent.click(within(line).getByRole('button', { name: 'Подробно' }));
    expect(await within(line).findByText(/^6 вызовов · всего /u)).toBeTruthy();
    expect(await within(line).findByText(/страница 2 из 6 ответа/u)).toBeTruthy();
  });

  it('chooses the readings of the programs', async () => {
    const api = client();
    renderLog(api, 'program');
    await waitFor(() => expect(api.listSendLog).toHaveBeenCalledWith({ zone, kind: 'program' }, expect.anything()));
    fireEvent.click(await screen.findByRole('button', { name: /^Вид отправок/u }));
    expect(await screen.findByRole('option', { name: 'Свой Claude или Codex' })).toBeTruthy();
  });
});
