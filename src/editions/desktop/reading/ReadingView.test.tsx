// SPDX-License-Identifier: AGPL-3.0-only
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { IndexingStatus } from '../../../entities/manuscript/api';
import { useIndexingStatusQuery } from '../../../entities/manuscript/api';
import { PreparationViewProvider, usePreparationView } from '../../../entities/manuscript/preparation-slot';
import { ApiStatusError } from '../../../shared/api';
import type { RouteNavigate } from '../../../shared/navigation/app-route';
import { ToastsProvider } from '../../../ui/feedback/Toast';
import { edition } from '../index';
import { ReadingClientProvider, type ReadingClient } from './reading-api';
import type { Reading } from './reading-line';
import { useReadingView } from './ReadingView';

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const plain = (text: string | null | undefined) => (text ?? '').replace(/\u00a0/gu, ' ');

const going: Reading = {
  state: 'reading', source: 'own', reason: null, pause: null, chaptersRead: 212, chaptersTotal: 612, namesWait: false, threadsWait: false, unread: null, unreadRest: null,
  spent: 240_000_000, approximate: true, stopAt: 1_000_000_000, step: 1_000_000_000, unit: 'RUB', remainingSeconds: 10_800,
};

function statusOf(reading: Partial<Reading> | null, waiting?: 'helper' | 'consent' | 'paused'): IndexingStatus {
  return {
    published: { exists: true, context: { state: 'processing', currentBook: null, retryableFailedBook: null } }, steps: [],
    preparation: {
      remainingSeconds: null, letterPromised: false, chaptersRead: 212, chaptersTotal: 612, booksRead: 4, booksTotal: 13,
      currentBookId: 'book-05', askable: true, books: [], ...(waiting ? { waiting } : {}),
    },
    ...(reading ? { reading: { ...going, ...reading } } : {}),
  } as IndexingStatus;
}

const noSample = {
  state: 'none' as const, failure: null, done: 0, total: 0, chapters: [], seconds: 0,
  spent: { inputTokens: 0, outputTokens: 0, amount: 0, unit: 'RUB' as const, approximate: false },
};

/** The server of the test: the status of the saga it answers now, and the writes of the consent it took. */
function server(first: IndexingStatus) {
  let status = first;
  const statuses = vi.fn(async () => status);
  const client = {
    setReadingConsent: vi.fn(async () => ({}) as never),
    getReadingSample: vi.fn(async () => noSample),
    getReadingEstimate: vi.fn(),
    startReadingSample: vi.fn(),
    cancelReadingSample: vi.fn(),
  } satisfies Record<keyof ReadingClient, unknown>;
  return { client: client as unknown as ReadingClient & typeof client, statuses, answer: (next: IndexingStatus) => { status = next; } };
}

/** A place of the line: the status of the saga by React Query, the line and the windows of the slot. */
function Place({ navigate, statuses }: { navigate: RouteNavigate; statuses: () => Promise<IndexingStatus> }) {
  const status = useIndexingStatusQuery({ getIndexingStatus: statuses } as never, 'saga-1').data;
  const view = usePreparationView({ navigate, projectId: 'saga-1', status });
  return (
    <div>
      {view?.lines.map((line) => <section aria-label={line.label} key={line.label}><p>{line.title}</p>{line.actions}</section>)}
      {view?.windows}
    </div>
  );
}

function renderLine(first: IndexingStatus, navigate: RouteNavigate = vi.fn()) {
  const test = server(first);
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}><ToastsProvider>
      <ReadingClientProvider value={test.client}><PreparationViewProvider value={useReadingView}>
        <Place navigate={navigate} statuses={test.statuses} />
      </PreparationViewProvider></ReadingClientProvider>
    </ToastsProvider></QueryClientProvider>,
  );
  return test;
}

// specs/2026-11-13-desk-helper-web.md HW3.1 (board 44·3), specs/2026-11-13-desk-consent-web.md RW1.2.
describe('the slot of the line of the reading of the app', () => {
  // specs/2026-11-15-desk-account-link-web.md AW3.3: the button opens the window of the choice (board 44·4); its «Свой ИИ» —
  // «Настроить» — opens «Настройки → Помощник», the way the button had before.
  it('offers «Подключить помощника» while the reading waits for the helper, and opens the window of the choice', async () => {
    const navigate = vi.fn<RouteNavigate>();
    renderLine(statusOf(null, 'helper'), navigate);
    fireEvent.click(await screen.findByRole('button', { name: 'Подключить помощника' }));
    expect(navigate).not.toHaveBeenCalled();
    const window = screen.getByRole('dialog', { name: 'Подключить помощника' });
    fireEvent.click(within(within(window).getByRole('region', { name: 'Свой ИИ' })).getByRole('button', { name: 'Настроить' }));
    expect(navigate).toHaveBeenCalledWith('/settings', { part: 'helper' });
    expect(screen.queryByRole('dialog', { name: 'Подключить помощника' })).toBeNull();
  });

  it('says nothing of its own without the slot: the line of the site', () => {
    function Bare() {
      return <div>{usePreparationView({ navigate: vi.fn(), projectId: 'saga-1', status: statusOf(going, 'helper') })?.lines.length ?? 'none'}</div>;
    }
    render(<Bare />);
    expect(screen.getByText('none')).toBeTruthy();
  });

  it('is given once over all the pages of the app', async () => {
    const account = edition.useAccount({} as Parameters<typeof edition.useAccount>[0]);
    const navigate = vi.fn<RouteNavigate>();
    function Line() {
      const view = usePreparationView({ navigate, projectId: 'saga-1', status: statusOf(null, 'helper') });
      return <div>{view?.lines.map((line) => line.actions)}{view?.windows}</div>;
    }
    render(<QueryClientProvider client={new QueryClient()}><ToastsProvider>{account.wrapPages(<Line />)}</ToastsProvider></QueryClientProvider>);
    fireEvent.click(screen.getByRole('button', { name: 'Подключить помощника' }));
    expect(screen.getByRole('dialog', { name: 'Подключить помощника' })).toBeTruthy();
  });
});

// specs/2026-11-13-desk-consent-web.md RW2.2: while a sample runs, the line of the shelf, the chat and the map says it.
describe('the line while a sample runs', () => {
  it('says the sample by the server, and the window opens from it', async () => {
    const test = renderLine(statusOf({ state: 'waiting_consent', spent: 0, stopAt: null, step: null }, 'consent'));
    test.client.getReadingSample.mockResolvedValue({ ...noSample, state: 'running', done: 1, total: 3 } as never);
    test.client.getReadingEstimate.mockResolvedValue(new Promise(() => undefined));
    const line = await screen.findByRole('region', { name: 'Помощник готов прочитать книги' });
    await waitFor(() => expect(plain(line.textContent)).toContain('Помощник читает 3 главы на пробу: 1 из 3'));
    fireEvent.click(within(line).getByRole('button', { name: 'Прочитать книги…' }));
    expect(await screen.findByRole('dialog', { name: 'Прочитать книги своим ИИ?' })).toBeTruthy();
  });
});

// specs/2026-11-13-desk-consent-web.md RW1.3: a button sends one operation of the consent with the fields of the table of
// the server and is busy until the new status of the server comes; a refusal is a line of the one area of toasts.
describe('the buttons of the line', () => {
  it.each([
    ['Пауза', statusOf({}), { state: 'paused' }],
    ['Продолжить', statusOf({ state: 'paused', pause: 'author' }, 'paused'), { state: 'allowed' }],
    ['Повторить', statusOf({ state: 'paused', pause: 'connection' }, 'paused'), { state: 'allowed' }],
    // specs/2026-11-15-desk-reading-no-answer.md NA1.2: the provider does not answer — the author goes on, as at no connection.
    ['Повторить', statusOf({ state: 'paused', pause: 'no_answer' }, 'paused'), { state: 'allowed' }],
    ['Продолжить ещё на 1 000 ₽', statusOf({ state: 'paused', pause: 'sum', spent: 1_000_000_000 }, 'paused'), { state: 'allowed', continueBy: 1_000_000_000 }],
    // The step of the server once it went on by a step: the sum is 5,60 ₽, the step 3 ₽ — X is the step.
    ['Продолжить ещё на 3 ₽', statusOf({ state: 'paused', pause: 'sum', spent: 5_200_000, stopAt: 5_600_000, step: 3_000_000 }, 'paused'), { state: 'allowed', continueBy: 3_000_000 }],
    ['Повторить', statusOf({ state: 'done', unread: { chapters: 3, reason: 'cut_answer' } }), { state: 'allowed', retryUnread: true }],
  ] as const)('«%s» sends its change and waits for the new status', async (label, first, change) => {
    const test = renderLine(first);
    let release: () => void = () => undefined;
    test.client.setReadingConsent.mockImplementation(() => new Promise((resolve) => { release = () => resolve({} as never); }));
    const button = await screen.findByRole('button', { name: (name) => plain(name) === label });
    fireEvent.click(button);
    await waitFor(() => expect(test.client.setReadingConsent).toHaveBeenCalledWith('saga-1', change));
    // The server answered the write; the line waits for the status it reads next, and the button stays busy until then.
    const reads = test.statuses.mock.calls.length;
    test.answer(statusOf({ spent: 300_000_000 }));
    await act(async () => release());
    await waitFor(() => expect(test.statuses.mock.calls.length).toBeGreaterThan(reads));
    await waitFor(() => expect(plain(screen.getByRole('region', { name: 'Помощник читает книги' }).textContent)).toContain('потрачено ≈ 300 из 1 000 ₽'));
  });

  it('keeps the button busy until the new status is read, and the other writes wait with their reason', async () => {
    const test = renderLine(statusOf({ state: 'paused', pause: 'sum', spent: 1_000_000_000 }, 'paused'));
    let answer: (status: IndexingStatus) => void = () => undefined;
    const next = new Promise<IndexingStatus>((resolve) => { answer = resolve; });
    const button = await screen.findByRole('button', { name: (name) => plain(name) === 'Продолжить ещё на 1 000 ₽' });
    test.statuses.mockImplementation(() => next);
    fireEvent.click(button);
    await waitFor(() => expect(test.client.setReadingConsent).toHaveBeenCalled());
    // The write is answered; the status is not yet: the button is busy, «Изменить сумму…» stays a window and waits for nothing.
    await act(() => new Promise((resolve) => setTimeout(resolve, 50)));
    expect(button.getAttribute('aria-busy')).toBe('true');
    answer(statusOf({}));
    await waitFor(() => expect(screen.queryByRole('button', { name: (name) => plain(name) === 'Продолжить ещё на 1 000 ₽' })).toBeNull());
    expect(screen.getByRole('button', { name: 'Пауза' })).toBeTruthy();
  });

  it('says a refusal in the one area of toasts, and the line stays as the server says it', async () => {
    const test = renderLine(statusOf({}));
    test.client.setReadingConsent.mockRejectedValue(new ApiStatusError(409, {
      type: 'about:blank', title: 'Conflict', status: 409, detail: 'no consent', code: 'reading.no_consent', requestId: 'request-1',
    }, new Response(null, { status: 409 })));
    fireEvent.click(await screen.findByRole('button', { name: 'Пауза' }));
    // An error stays in the one area as an alert (PA1.2).
    const toast = await waitFor(() => screen.getAllByRole('alert').find((each) => each.textContent?.includes('Чтение не встало на паузу'))!);
    expect(toast.textContent).toContain('У этой саги нет согласия на чтение');
    expect(plain(screen.getByRole('region', { name: 'Помощник читает книги' }).textContent)).toContain('Помощник читает книги: 212 из 612 глав');
  });

  // specs/2026-11-15-desk-reading-rest.md RR2.4, RR2.5: «Повторить» of what did not come together — one write of the consent,
  // «Помощник пробует снова» once it went through, «Повтор не начался» with the reason of a refusal; the settings of the helper
  // open from a cut answer; while the stop sum is spent «Повторить» waits and says why, and sends nothing.
  it('repeats what did not come together: the line of the area says it tries again', async () => {
    const test = renderLine(statusOf({ state: 'done', unreadRest: { names: 'unreadable', threads: null } }));
    const line = await screen.findByRole('region', { name: 'Имена героев не сведены' });
    test.answer(statusOf({ chaptersRead: 612, namesWait: true }));
    fireEvent.click(within(line).getByRole('button', { name: 'Повторить' }));
    await waitFor(() => expect(test.client.setReadingConsent).toHaveBeenCalledWith('saga-1', { state: 'allowed', retryUnread: true }));
    expect(await screen.findByText('Помощник пробует снова')).toBeTruthy();
    await waitFor(() => expect(plain(screen.getByRole('region', { name: 'Помощник читает книги' }).textContent))
      .toContain('Главы прочитаны — помощник сводит имена героев'));
  });

  it('says «Повтор не начался» with the reason of a refusal', async () => {
    const test = renderLine(statusOf({ state: 'done', unreadRest: { names: null, threads: 'no_answer' } }));
    test.client.setReadingConsent.mockRejectedValue(new ApiStatusError(400, {
      type: 'about:blank', title: 'Bad Request', status: 400, detail: 'sum', code: 'reading.invalid', requestId: 'request-2',
    }, new Response(null, { status: 400 })));
    fireEvent.click(await screen.findByRole('button', { name: 'Повторить' }));
    const toast = await waitFor(() => screen.getAllByRole('alert').find((each) => each.textContent?.includes('Повтор не начался'))!);
    expect(toast.textContent).toContain('Сумма остановки уже потрачена');
    expect(screen.getByRole('region', { name: 'Сюжетные нити не сведены' })).toBeTruthy();
  });

  it('opens «Настройки → Помощник» from a cut answer, and keeps «Повторить» waiting while the sum is spent', async () => {
    const navigate = vi.fn<RouteNavigate>();
    const test = renderLine(statusOf({ state: 'done', unreadRest: { names: 'cut_answer', threads: null }, spent: 1_000_000_000, stopAt: 1_000_000_000 }), navigate);
    const line = await screen.findByRole('region', { name: 'Имена героев не сведены' });
    expect(within(line).getAllByRole('button').map((button) => plain(button.textContent))).toEqual(['Открыть настройки помощника', 'Изменить сумму…', 'Повторить']);
    fireEvent.click(within(line).getByRole('button', { name: 'Повторить' }));
    expect(await screen.findByText('Сумма остановки уже потрачена — назовите новую в «Изменить сумму…»')).toBeTruthy();
    expect(test.client.setReadingConsent).not.toHaveBeenCalled();
    fireEvent.click(within(line).getByRole('button', { name: 'Открыть настройки помощника' }));
    expect(navigate).toHaveBeenCalledWith('/settings', { part: 'helper' });
  });

  it('opens «Настройки → Помощник» from a pause of the key', async () => {
    const navigate = vi.fn<RouteNavigate>();
    renderLine(statusOf({ state: 'paused', pause: 'key' }, 'paused'), navigate);
    fireEvent.click(await screen.findByRole('button', { name: 'Открыть настройки помощника' }));
    expect(navigate).toHaveBeenCalledWith('/settings', { part: 'helper' });
  });
});

// specs/2026-11-13-desk-consent-web.md RW2.5: the windows of the line close with «Назад» of the browser, Esc and their own
// buttons, and the focus is back on the button of the line that opened them.
describe('the windows of the line', () => {
  /** «Назад» of the browser, done: the step of the history has come. */
  const back = () => new Promise<void>((resolve) => {
    window.addEventListener('popstate', () => resolve(), { once: true });
    window.history.back();
  });

  it('«Назад» closes the window of the consent, and the focus is back on «Прочитать книги…»', async () => {
    const test = renderLine(statusOf({ state: 'waiting_consent', spent: 0, stopAt: null, step: null }, 'consent'));
    test.client.getReadingEstimate.mockReturnValue(new Promise(() => undefined));
    const button = await screen.findByRole('button', { name: 'Прочитать книги…' });
    button.focus();
    fireEvent.click(button);
    await screen.findByRole('dialog', { name: 'Прочитать книги своим ИИ?' });
    await act(back);
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    await waitFor(() => expect(document.activeElement).toBe(screen.getByRole('button', { name: 'Прочитать книги…' })));
  });

  it('«Назад» and «Отмена» close «Сумма остановки», and the focus is back on «Изменить сумму…»', async () => {
    renderLine(statusOf({ state: 'paused', pause: 'sum', spent: 1_000_000_000 }, 'paused'));
    const button = await screen.findByRole('button', { name: 'Изменить сумму…' });
    button.focus();
    fireEvent.click(button);
    await screen.findByRole('dialog', { name: 'Сумма остановки' });
    await act(back);
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    await waitFor(() => expect(document.activeElement).toBe(screen.getByRole('button', { name: 'Изменить сумму…' })));
    fireEvent.click(screen.getByRole('button', { name: 'Изменить сумму…' }));
    const window = await screen.findByRole('dialog', { name: 'Сумма остановки' });
    fireEvent.click(within(window).getByRole('button', { name: 'Отмена' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    await waitFor(() => expect(document.activeElement).toBe(screen.getByRole('button', { name: 'Изменить сумму…' })));
  });
});

// specs/2026-11-13-desk-consent-web.md RW1.4 (board 44·42): «Изменить сумму…» — what was spent, the field in the unit of the
// consent with its hint and its refusal under it, «Без суммы» of its own; at the pause «сумма» saving goes on with the reading.
describe('«Изменить сумму…»', () => {
  const field = () => screen.getByRole('textbox', { name: 'Остановиться, когда потрачено' }) as HTMLInputElement;

  it('says what was spent and offers the sum and the step after it; a sum not over the spent is refused at the field', async () => {
    const test = renderLine(statusOf({ state: 'paused', pause: 'sum', spent: 1_000_000_000, approximate: false }, 'paused'));
    fireEvent.click(await screen.findByRole('button', { name: 'Изменить сумму…' }));
    const window = screen.getByRole('dialog', { name: 'Сумма остановки' });
    expect(plain(within(window).getByText(/^Потрачено/u).textContent)).toBe('Потрачено 1 000 ₽');
    expect(field().value).toBe('2 000');
    expect(within(window).getByText('Чтение встанет на паузу и спросит вас')).toBeTruthy();
    test.client.setReadingConsent.mockRejectedValueOnce(new ApiStatusError(400, {
      type: 'about:blank', title: 'Bad Request', status: 400, detail: 'a field is not valid', code: 'reading.invalid',
      errors: [{ path: '/stopAt', message: 'not valid', code: 'reading.invalid' }],
    } as never, new Response(null, { status: 400 })));
    fireEvent.change(field(), { target: { value: '1 000' } });
    fireEvent.click(within(window).getByRole('button', { name: 'Сохранить и продолжить' }));
    await waitFor(() => expect(test.client.setReadingConsent).toHaveBeenCalledWith('saga-1', { state: 'allowed', stopAt: 1_000_000_000 }));
    const refusal = await within(window).findByRole('alert');
    expect(plain(refusal.textContent)).toBe('Сумма должна быть больше уже потраченного: 1 000 ₽');
    // The hint stays: the refusal stands under it (the shared Field).
    expect(within(window).getByText('Чтение встанет на паузу и спросит вас')).toBeTruthy();
    expect(field().value).toBe('1 000');
  });

  it('saves a new sum and goes on: the window closes once the status says the reading goes', async () => {
    const test = renderLine(statusOf({ state: 'paused', pause: 'sum', spent: 1_000_000_000 }, 'paused'));
    fireEvent.click(await screen.findByRole('button', { name: 'Изменить сумму…' }));
    fireEvent.change(field(), { target: { value: '3000' } });
    test.answer(statusOf({ stopAt: 3_000_000_000 }));
    fireEvent.click(screen.getByRole('button', { name: 'Сохранить и продолжить' }));
    await waitFor(() => expect(test.client.setReadingConsent).toHaveBeenCalledWith('saga-1', { state: 'allowed', stopAt: 3_000_000_000 }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Сумма остановки' })).toBeNull());
    expect(plain(screen.getByRole('region', { name: 'Помощник читает книги' }).textContent)).toContain('из 3 000 ₽');
  });

  it('reads to the end without a sum by its own choice, and only changes the sum of a reading that goes', async () => {
    const test = renderLine(statusOf({}));
    fireEvent.click(await screen.findByRole('button', { name: 'Изменить сумму…' }));
    // Board 44·42 «Чтение идёт»: the field offers the sum one step past the stop of the server, as at the pause.
    expect(field().value).toBe('2 000');
    fireEvent.click(screen.getByRole('checkbox', { name: 'Без суммы — читать до конца' }));
    expect(field().disabled).toBe(true);
    fireEvent.click(screen.getByRole('button', { name: 'Сохранить' }));
    await waitFor(() => expect(test.client.setReadingConsent).toHaveBeenCalledWith('saga-1', { state: 'allowed', stopAt: null }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Сумма остановки' })).toBeNull());
  });

  /** The estimate of the server after a change of the prices: the unit the new sum is asked in. */
  const estimateIn = (unit: 'USD' | 'EUR') => ({
    chapters: 400, inputTokens: 9_000_000, outputTokens: 3_000_000, seconds: 10_800, amount: 4_100_000, unit, layerShare: 0.2,
    host: 'api.deepseek.com', local: false, kind: 'sample', assumptions: [], suggestedStop: 10_000_000, sampleChapters: 0,
    chapterEdit: { inputTokens: 1, outputTokens: 1, amount: 1 }, namesWait: false, threadsWait: false,
  });

  // At the pause «цены изменились» the sum named in the old currency is gone: the field is empty and asks the sum in the unit
  // the estimate of the server says now; saving goes on with the reading (RW1.4).
  it('after a change of the prices asks the sum in the unit the server says now, and goes on once saved', async () => {
    const test = renderLine(statusOf({ state: 'paused', pause: 'prices' }, 'paused'));
    let answer: (estimate: ReturnType<typeof estimateIn>) => void = () => undefined;
    test.client.getReadingEstimate.mockReturnValue(new Promise((resolve) => { answer = resolve; }));
    fireEvent.click(await screen.findByRole('button', { name: 'Изменить сумму…' }));
    const window = screen.getByRole('dialog', { name: 'Сумма остановки' });
    const save = within(window).getByRole('button', { name: 'Сохранить и продолжить' });
    // Until the server says the unit, the button waits on its place and says why.
    expect(save.getAttribute('aria-disabled')).toBe('true');
    fireEvent.click(save);
    expect(await screen.findByText('Читаю, в чём теперь считается сумма…')).toBeTruthy();
    expect(test.client.setReadingConsent).not.toHaveBeenCalled();
    await act(async () => answer(estimateIn('USD')));
    await waitFor(() => expect(field().disabled).toBe(false));
    expect(field().value).toBe('');
    expect(plain(document.getElementById(field().getAttribute('aria-describedby')!.split(' ')[0]!)?.textContent)).toBe('$');
    fireEvent.change(field(), { target: { value: '10' } });
    test.answer(statusOf({ unit: 'USD', spent: 4_000_000, stopAt: 10_000_000, step: 10_000_000 }));
    fireEvent.click(save);
    await waitFor(() => expect(test.client.setReadingConsent).toHaveBeenCalledWith('saga-1', { state: 'allowed', stopAt: 10_000_000 }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Сумма остановки' })).toBeNull());
    expect(plain(screen.getByRole('region', { name: 'Помощник читает книги' }).textContent)).toContain('потрачено ≈ $4 из $10');
  });

  it('says the estimate did not come after a change of the prices, with «Повторить», and asks the sum once it comes', async () => {
    const test = renderLine(statusOf({ state: 'paused', pause: 'prices' }, 'paused'));
    test.client.getReadingEstimate.mockRejectedValueOnce(new Error('no answer')).mockResolvedValueOnce(estimateIn('EUR'));
    fireEvent.click(await screen.findByRole('button', { name: 'Изменить сумму…' }));
    const window = screen.getByRole('dialog', { name: 'Сумма остановки' });
    const failure = await within(window).findByRole('alert', { name: 'Не открылась оценка чтения книг' });
    expect(within(window).getByRole('button', { name: 'Сохранить и продолжить' }).getAttribute('aria-disabled')).toBe('true');
    fireEvent.click(within(failure).getByRole('button', { name: 'Повторить' }));
    await waitFor(() => expect(field().disabled).toBe(false));
    expect(within(window).queryByRole('alert', { name: 'Не открылась оценка чтения книг' })).toBeNull();
    expect(plain(document.getElementById(field().getAttribute('aria-describedby')!.split(' ')[0]!)?.textContent)).toBe('€');
  });

  it('counts in tokens without prices', async () => {
    renderLine(statusOf({ state: 'paused', pause: 'sum', unit: 'tokens', spent: 25_000_000, stopAt: 25_000_000, step: 25_000_000, approximate: false }, 'paused'));
    fireEvent.click(await screen.findByRole('button', { name: 'Изменить сумму…' }));
    expect(plain(screen.getByText(/^Потрачено/u).textContent)).toBe('Потрачено 25 млн токенов');
    expect(field().value).toBe('50');
    // The unit of the field stands at its edge and is read with it.
    const unit = document.getElementById(field().getAttribute('aria-describedby')!.split(' ')[0]!);
    expect(plain(unit?.textContent)).toBe('млн токенов');
  });
});
