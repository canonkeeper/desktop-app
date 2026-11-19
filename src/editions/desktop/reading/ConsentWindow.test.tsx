// SPDX-License-Identifier: AGPL-3.0-only
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { useState } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiStatusError } from '../../../shared/api';
import type { RouteNavigate } from '../../../shared/navigation/app-route';
import { ToastsProvider } from '../../../ui/feedback/Toast';
import { ConsentWindow } from './ConsentWindow';
import { ReadingClientProvider, type ReadingClient, type ReadingEstimate, type ReadingSample } from './reading-api';

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const plain = (text: string | null | undefined) => (text ?? '').replace(/\u00a0/gu, ' ');

/** The estimate of the board 44·27 «С ценами». */
const priced: ReadingEstimate = {
  chapters: 612, inputTokens: 14_000_000, outputTokens: 5_000_000, seconds: 18_000, amount: 680_000_000, unit: 'RUB', layerShare: 0.28,
  host: 'api.deepseek.com', local: false, kind: 'rough', assumptions: ['calibration_fallback'], suggestedStop: 1_000_000_000,
  sampleChapters: 3, chapterEdit: { inputTokens: 1, outputTokens: 1, amount: 1 }, namesWait: false, threadsWait: false,
};

const card = (n: number) => ({
  chapterId: `00000000-0000-4000-8000-00000000010${n}`, place: `Книга 1, гл. ${n} „Туманный берег“`,
  summary: 'Ирна возвращается в северную гавань и узнаёт, что маяк погас.', events: ['Возвращение Ирны', 'маяк погас'], people: ['Ирна', 'смотритель Тарвин'],
});

function sampleOf(sample: Partial<ReadingSample>): ReadingSample {
  return {
    state: 'none', failure: null, done: 0, total: 3, chapters: [], seconds: 0,
    spent: { inputTokens: 0, outputTokens: 0, amount: 0, unit: 'RUB', approximate: false }, ...sample,
  };
}

function serverOf(estimate: ReadingEstimate, sample: ReadingSample = sampleOf({ total: 0 })) {
  const client = {
    getReadingEstimate: vi.fn(async () => estimate),
    getReadingSample: vi.fn(async () => sample),
    startReadingSample: vi.fn(async () => sampleOf({ state: 'queued' })),
    cancelReadingSample: vi.fn(async () => undefined),
    setReadingConsent: vi.fn(async () => ({}) as never),
  };
  return client;
}

/** The window over a page: open by its button, as the line opens it. */
function Page({ navigate }: { navigate: RouteNavigate }) {
  const [open, setOpen] = useState(true);
  return (
    <>
      <button onClick={() => setOpen(true)} type="button">Прочитать книги…</button>
      {open ? <ConsentWindow navigate={navigate} onClose={() => setOpen(false)} projectId="saga-1" /> : null}
    </>
  );
}

function renderWindow(client: ReturnType<typeof serverOf>, navigate: RouteNavigate = vi.fn()) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}><ToastsProvider>
      <ReadingClientProvider value={client as unknown as ReadingClient}><Page navigate={navigate} /></ReadingClientProvider>
    </ToastsProvider></QueryClientProvider>,
  );
  return queryClient;
}

async function windowOpen() {
  const window = await screen.findByRole('dialog', { name: 'Прочитать книги своим ИИ?' });
  await within(window).findByText(/^Помощник прочитает|^Главы прочитаны/u);
  return window;
}

const buttons = (window: HTMLElement) => within(window).getAllByRole('button').map((button) => plain(button.textContent)).filter((label) => label !== '');
const field = (window: HTMLElement) => within(window).getByRole('textbox', { name: 'Остановиться, когда потрачено' }) as HTMLInputElement;

// specs/2026-11-13-desk-consent-web.md RW2.1, RW2.4 (board 44·27): every number and unit is of the answer of the server.
describe('the window of the consent', () => {
  it('with prices: what the helper does, where the text goes, the estimate, the stop the server offers, the sample first', async () => {
    renderWindow(serverOf(priced));
    const window = await windowOpen();
    expect(plain(window.textContent)).toContain('Помощник прочитает 612 глав и узнает, кто есть кто, что произошло и где это в книге. Текст книг уйдёт вашему провайдеру api.deepseek.com. Учится ли провайдер на ваших текстах и хранит ли их, решают его правила — проверьте их до чтения');
    expect(plain(window.textContent)).toContain('Оценка: ≈ 14 млн токенов на вход и ≈ 5 млн на выход · ≈ 680 ₽ · около 5 часов');
    expect(plain(window.textContent)).toContain('Оценка грубая. После пробы на трёх главах она станет точной');
    expect(field(window).value).toBe('1 000');
    expect(within(window).getByText('₽')).toBeTruthy();
    expect(within(window).getByText('Чтение встанет на паузу и спросит вас')).toBeTruthy();
    expect(buttons(window)).toEqual(['Закрыть', 'Не сейчас', 'Прочитать всё', 'Попробовать на 3 главах'].filter((label) => label !== 'Закрыть'));
    expect(within(window).queryByText(/Укажите цены/u)).toBeNull();
  });

  it('without prices: no sum, the way to «Дополнительно», the stop in millions of tokens', async () => {
    const navigate = vi.fn<RouteNavigate>();
    renderWindow(serverOf({ ...priced, amount: null, unit: 'tokens', suggestedStop: 25_000_000 }), navigate);
    const window = await windowOpen();
    expect(plain(window.textContent)).toContain('Оценка: ≈ 14 млн токенов на вход и ≈ 5 млн на выход · около 5 часов');
    expect(plain(window.textContent)).not.toContain('₽');
    expect(field(window).value).toBe('25');
    fireEvent.click(within(window).getByRole('button', { name: 'Укажите цены в «Дополнительно», чтобы видеть сумму' }));
    expect(navigate).toHaveBeenCalledWith('/settings', { part: 'helper' });
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  });

  it('with the model on this computer: the program at its address gets the text, not a provider in the network', async () => {
    renderWindow(serverOf({ ...priced, amount: null, unit: 'tokens', host: '127.0.0.1:11434', local: true, suggestedStop: 25_000_000 }));
    const window = await windowOpen();
    expect(window.textContent).toContain('Текст книг получит программа на этом компьютере (127.0.0.1:11434) — не провайдер в сети.');
    expect(window.textContent).not.toContain('не уходит');
    // specs/2026-11-19-desk-trust.md TW1.4: the rules of a provider are not a word of the program on this computer.
    expect(window.textContent).not.toContain('решают его правила');
    expect(within(window).queryByText(/Укажите цены/u)).toBeNull();
  });

  it('with fewer chapters than three: the sample of as many as wait', async () => {
    renderWindow(serverOf({ ...priced, chapters: 2, sampleChapters: 2 }));
    const window = await windowOpen();
    expect(window.textContent).toContain('Помощник прочитает 2 главы');
    expect(window.textContent).toContain('После пробы на 2 главах она станет точной');
    expect(within(window).getByRole('button', { name: 'Попробовать на 2 главах' })).toBeTruthy();
  });

  // specs/2026-11-15-desk-reading-rest.md RR2.7: the button is named by what is left — the two signs of the server.
  it.each([
    [{ namesWait: true, threadsWait: true }, 'имена героев и сюжетные нити', 'Свести имена и нити'],
    [{ namesWait: true, threadsWait: false }, 'имена героев', 'Свести имена'],
    [{ namesWait: false, threadsWait: true }, 'сюжетные нити', 'Свести нити'],
  ])('with the chapters read and %o waiting: says so, no sample, its button by what is left', async (signs, left, button) => {
    renderWindow(serverOf({ ...priced, chapters: 0, sampleChapters: 0, ...signs, amount: 90_000_000 }));
    const window = await windowOpen();
    expect(window.textContent).toContain(`Главы прочитаны. Помощнику осталось свести ${left}.`);
    expect(window.textContent).not.toContain('пробы');
    expect(buttons(window)).toEqual(['Не сейчас', button]);
  });
});

// RW2.2: the sample — its work with «Остановить», its chapters and the estimate after it, its reason, its stop.
describe('the sample in the window', () => {
  it('starts with its key of a repeat and runs with «Остановить»; the window closed and opened again shows how far', async () => {
    const client = serverOf(priced);
    renderWindow(client);
    let window = await windowOpen();
    client.getReadingSample.mockResolvedValue(sampleOf({ state: 'running', done: 1 }));
    fireEvent.click(within(window).getByRole('button', { name: 'Попробовать на 3 главах' }));
    await waitFor(() => expect(client.startReadingSample).toHaveBeenCalledWith('saga-1', { idempotencyKey: expect.any(String) }));
    const work = await within(window).findByRole('region', { name: 'Проба чтения' });
    await waitFor(() => expect(plain(work.textContent)).toContain('Читаю 3 главы: 1 из 3'));
    expect(work.textContent).toContain('можно закрыть окно — проба продолжится');
    // Closed: the sample goes on — nothing stops it; opened again, its work is there.
    fireEvent.click(within(window).getByRole('button', { name: 'Закрыть' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(client.cancelReadingSample).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Прочитать книги…' }));
    window = await windowOpen();
    const again = await within(window).findByRole('region', { name: 'Проба чтения' });
    fireEvent.click(within(again).getByRole('button', { name: 'Остановить' }));
    await waitFor(() => expect(client.cancelReadingSample).toHaveBeenCalledWith('saga-1'));
  });

  it('is ready: its time and what it spent, its chapters, the estimate after it, «Прочитать остальное»', async () => {
    renderWindow(serverOf({ ...priced, kind: 'sample', inputTokens: 14_200_000, outputTokens: 5_100_000, amount: 690_000_000 }, sampleOf({
      state: 'succeeded', done: 3, chapters: [card(1), card(2), card(3)], seconds: 240,
      spent: { inputTokens: 48_000, outputTokens: 19_500, amount: 4_000_000, unit: 'RUB', approximate: true },
    })));
    const window = await windowOpen();
    await waitFor(() => expect(plain(window.textContent)).toContain('Проба готова: 3 главы за 4 минуты · потрачено ≈ 4 ₽'));
    expect(plain(window.textContent)).toContain('Теперь оценка: ≈ 14,2 млн токенов на вход и ≈ 5,1 млн на выход · ≈ 690 ₽ · около 5 часов');
    expect(within(window).getAllByRole('heading', { level: 3 }).map((heading) => heading.textContent)).toEqual([
      'Книга 1, гл. 1 „Туманный берег“', 'Книга 1, гл. 2 „Туманный берег“', 'Книга 1, гл. 3 „Туманный берег“',
    ]);
    expect(within(window).getAllByText('Пересказ')).toHaveLength(3);
    expect(within(window).getAllByText('Возвращение Ирны · маяк погас')).toHaveLength(3);
    expect(within(window).getAllByText('Ирна, смотритель Тарвин')).toHaveLength(3);
    expect(window.textContent).not.toContain('Оценка грубая');
    expect(buttons(window)).toEqual(['Не читать', 'Прочитать остальное']);
    // Board 44·27 «Проба готова»: the result and its chapters, then the estimate after them.
    const result = within(window).getByRole('region', { name: /^Проба готова/u });
    expect(result.compareDocumentPosition(within(window).getByText(/^Теперь оценка/u)) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it.each([
    ['funds', 'Проба не получилась: провайдер отказал — кончились средства на счёте или он пока не принимает запросы', false],
    ['key', 'Проба не получилась: провайдер не принял ключ', true],
    ['model', 'Проба не получилась: у провайдера нет модели с таким названием', true],
    ['connection', 'Проба не получилась: нет связи с провайдером', false],
    ['paused', 'Проба не получилась: чтение остановилось, не дочитав главы пробы', false],
    ['helper', 'Проба не получилась: помощник не подключён', true],
    ['other', 'Проба не получилась: ответ помощника не пришёл', false],
  ] as const)('did not read (%s): its reason, the settings when they help, «Попробовать ещё раз»', async (failure, words, settings) => {
    const navigate = vi.fn<RouteNavigate>();
    renderWindow(serverOf(priced, sampleOf({ state: 'failed', failure })), navigate);
    const window = await windowOpen();
    const reason = await within(window).findByText(words);
    expect(reason).toBeTruthy();
    expect(within(window).queryByRole('button', { name: 'Открыть настройки помощника' }) !== null).toBe(settings);
    expect(buttons(window).slice(-3)).toEqual(['Не сейчас', 'Прочитать всё', 'Попробовать ещё раз']);
    if (settings) {
      fireEvent.click(within(window).getByRole('button', { name: 'Открыть настройки помощника' }));
      expect(navigate).toHaveBeenCalledWith('/settings', { part: 'helper' });
    }
  });

  it('was stopped: what it read and spent, its chapter, «Прочитать всё» the main one', async () => {
    renderWindow(serverOf(priced, sampleOf({
      state: 'canceled', done: 1, chapters: [card(1)], seconds: 80, spent: { inputTokens: 16_000, outputTokens: 6_500, amount: 1_000_000, unit: 'RUB', approximate: true },
    })));
    const window = await windowOpen();
    await waitFor(() => expect(plain(window.textContent)).toContain('Проба остановлена: прочитано 1 из 3 глав · потрачено ≈ 1 ₽'));
    expect(buttons(window)).toEqual(['Не сейчас', 'Попробовать ещё раз', 'Прочитать всё']);
    // Board 44·27 «Проба остановлена»: the rough estimate stays over what the sample read.
    const result = within(window).getByRole('region', { name: /^Проба остановлена/u });
    expect(result.compareDocumentPosition(within(window).getByText(/^Оценка:/u)) & Node.DOCUMENT_POSITION_PRECEDING).toBeTruthy();
  });
});

// RW2.3: «Прочитать всё» and «Прочитать остальное» give the consent with the sum of the field in the unit of the estimate; an
// empty field asks first; the window closes once the status is read; a refusal keeps the window and what was written.
describe('the consent from the window', () => {
  it('sends the sum of the field in the unit of the estimate and closes', async () => {
    const client = serverOf(priced);
    renderWindow(client);
    const window = await windowOpen();
    fireEvent.change(field(window), { target: { value: '1 500' } });
    fireEvent.click(within(window).getByRole('button', { name: 'Прочитать всё' }));
    await waitFor(() => expect(client.setReadingConsent).toHaveBeenCalledWith('saga-1', { state: 'allowed', stopAt: 1_500_000_000 }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  });

  // Enter in the field is the main button of the window, as everywhere: while the sample is the main one it starts the sample,
  // never the paid reading of all the books; while the sample runs it gives nothing.
  it('takes Enter in the field for the main button: the sample while it is the main one', async () => {
    const client = serverOf(priced);
    renderWindow(client);
    const window = await windowOpen();
    expect(within(window).getAllByRole('button').filter((button) => button.getAttribute('type') === 'submit').map((button) => plain(button.textContent)))
      .toEqual(['Попробовать на 3 главах']);
    fireEvent.change(field(window), { target: { value: '1 500' } });
    fireEvent.submit(field(window).form!);
    await waitFor(() => expect(client.startReadingSample).toHaveBeenCalledTimes(1));
    expect(client.setReadingConsent).not.toHaveBeenCalled();
  });

  it('gives nothing by Enter while the sample runs, and the reading by Enter once the sample is done', async () => {
    const client = serverOf(priced, sampleOf({ state: 'running', done: 1 }));
    renderWindow(client);
    const window = await windowOpen();
    await within(window).findByRole('region', { name: 'Проба чтения' });
    fireEvent.submit(field(window).form!);
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(client.setReadingConsent).not.toHaveBeenCalled();
    expect(client.startReadingSample).not.toHaveBeenCalled();
    cleanup();
    const done = serverOf({ ...priced, kind: 'sample' }, sampleOf({ state: 'succeeded', done: 3, chapters: [card(1)], seconds: 240 }));
    renderWindow(done);
    const ready = await windowOpen();
    await within(ready).findByRole('button', { name: 'Прочитать остальное' });
    fireEvent.submit(field(ready).form!);
    await waitFor(() => expect(done.setReadingConsent).toHaveBeenCalledWith('saga-1', { state: 'allowed', stopAt: 1_000_000_000 }));
  });

  it('sends millions of tokens without prices', async () => {
    const client = serverOf({ ...priced, amount: null, unit: 'tokens', suggestedStop: 25_000_000 });
    renderWindow(client);
    const window = await windowOpen();
    fireEvent.click(within(window).getByRole('button', { name: 'Прочитать всё' }));
    await waitFor(() => expect(client.setReadingConsent).toHaveBeenCalledWith('saga-1', { state: 'allowed', stopAt: 25_000_000 }));
  });

  it('asks before a reading without a stop sum and names the estimate; «Отмена» goes back to the field', async () => {
    const client = serverOf(priced);
    renderWindow(client);
    const window = await windowOpen();
    fireEvent.change(field(window), { target: { value: '' } });
    fireEvent.click(within(window).getByRole('button', { name: 'Прочитать всё' }));
    let question = await screen.findByRole('dialog', { name: 'Читать без суммы остановки?' });
    expect(plain(question.textContent)).toContain('Помощник прочитает все 612 глав и книги, которые вы добавите позже, — примерно на 680 ₽. Остановить чтение можно кнопкой «Пауза»');
    expect(client.setReadingConsent).not.toHaveBeenCalled();
    fireEvent.click(within(question).getByRole('button', { name: 'Отмена' }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Читать без суммы остановки?' })).toBeNull());
    fireEvent.click(within(window).getByRole('button', { name: 'Прочитать всё' }));
    question = await screen.findByRole('dialog', { name: 'Читать без суммы остановки?' });
    fireEvent.click(within(question).getByRole('button', { name: 'Читать без суммы' }));
    await waitFor(() => expect(client.setReadingConsent).toHaveBeenCalledWith('saga-1', { state: 'allowed', stopAt: null }));
  });

  it('refuses at the field what is not a sum, and keeps the window and the words on a refusal of the server', async () => {
    const client = serverOf(priced);
    renderWindow(client);
    const window = await windowOpen();
    fireEvent.change(field(window), { target: { value: 'тысяча' } });
    fireEvent.click(within(window).getByRole('button', { name: 'Прочитать всё' }));
    expect(plain((await within(window).findByRole('alert')).textContent)).toBe('Напишите сумму числом — например 1 000');
    expect(within(window).getByText('Чтение встанет на паузу и спросит вас')).toBeTruthy();
    client.setReadingConsent.mockRejectedValueOnce(new ApiStatusError(409, {
      type: 'about:blank', title: 'Conflict', status: 409, detail: 'not connected', code: 'helper.not_connected', requestId: 'request-1',
    }, new Response(null, { status: 409 })));
    fireEvent.change(field(window), { target: { value: '2000' } });
    fireEvent.click(within(window).getByRole('button', { name: 'Прочитать всё' }));
    await waitFor(() => expect(window.textContent).toContain('Помощник не подключён — подключите его в настройках помощника.'));
    expect(screen.getByRole('dialog', { name: 'Прочитать книги своим ИИ?' })).toBeTruthy();
    expect(field(window).value).toBe('2000');
  });
});
