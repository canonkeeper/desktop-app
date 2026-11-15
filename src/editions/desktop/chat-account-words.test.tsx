// SPDX-License-Identifier: AGPL-3.0-only
import { cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { projectId } from '../../api/mock/repository/fixtures';
import { createMockApiClient } from '../../api/mock';
import type { IndexingStatus } from '../../entities/manuscript/api';
import { createIdempotencyKey } from '../../shared/api';
import { createAppRoute } from '../../shared/navigation/app-route';
import { hiddenIn } from '../../shared/copy/dictionary';
import { reasonOf } from '../../test/disabled-reason';
import { FakeWebSocket } from '../../test/FakeWebSocket';
import { forgetAllTurns, watchTurn } from '../../features/chat/answer-ready';
import { noShell, shellOfTheWindow } from './settings/settings.test-helpers';
import { countWords, elsewhereWords, refusedWordsOf, turnWords, turnWordsOf } from './chat-words';
import { renderChat, theAccount, theChat, theField, type Api } from './chat.test-helpers';

vi.mock('@tanstack/react-router', async (actual) => ({ ...await actual<object>(), useBlocker: vi.fn(() => ({ status: 'idle' })) }));

beforeEach(() => {
  vi.stubGlobal('WebSocket', FakeWebSocket);
  vi.stubGlobal('innerWidth', 1440);
  vi.stubGlobal('innerHeight', 900);
  vi.stubGlobal('matchMedia', (query: string) => ({ addEventListener: () => undefined, matches: false, media: query, removeEventListener: () => undefined }));
  shellOfTheWindow();
});

afterEach(() => {
  cleanup();
  noShell();
  forgetAllTurns();
  FakeWebSocket.reset();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

const question = 'Где стоит застава у перевала?';
const tariffs = 'https://canonkeeper.example.test/tariffs';

/**
 * A chat of the saga that waits for its first question, open: a question of a new chat would make its chat first, and the mock
 * answers before the page goes to it.
 */
async function openChat(api: Api) {
  const chat = await api.createChatSession(projectId, { scope: 'project', sourceView: 'published', title: 'Застава' },
    { idempotencyKey: createIdempotencyKey('createChatSession') });
  renderChat(api, createAppRoute('/chat', { projectId, chatId: chat.id }));
}

/** A question typed into the field of the chat of the app and sent. */
async function ask(text = question) {
  const field = await theField();
  fireEvent.change(field, { target: { value: text } });
  const send = within(await theChat()).getByRole('button', { name: 'Спросить' });
  await waitFor(() => expect(send.getAttribute('aria-disabled')).not.toBe('true'));
  fireEvent.click(send);
  return field;
}

// specs/2026-11-15-desk-account-words-web.md AW4.1: a refusal by the tier — the two parts of the words of the site, without the
// part of the next tier, «Тарифы» to the page of the server and «Подключить свой ИИ»; the words of the author stay in the field.
describe('a question of the helper of the account refused by the tier', () => {
  it('says what ended and when it comes back, with «Тарифы» and «Подключить свой ИИ»', async () => {
    const api = await theAccount({ accountTurnRefusal: { code: 'quota.chat_turns_daily' } });
    await openChat(api);
    const field = await ask();
    const block = await within(await theChat()).findByRole('alert', { name: /^Вопросы и уточнения на сегодня закончились/u });
    expect(block.textContent).toContain('Новые — после 00:00 по Москве.');
    expect(block.textContent).toContain('Ваш вопрос остался в поле — задайте его завтра.');
    expect(block.textContent).not.toMatch(/На тарифе|Слишком много вопросов подряд/u);
    const tariff = await within(block).findByRole('link', { name: 'Тарифы' });
    expect(tariff.getAttribute('href')).toBe(tariffs);
    expect(field.value).toBe(question);
    fireEvent.click(within(block).getByRole('button', { name: 'Подключить свой ИИ' }));
    expect(await screen.findByText('/settings?part=helper')).toBeTruthy();
  });

  it('«разом» without an answer prepared here says where they are prepared', async () => {
    const api = await theAccount({ accountTurnRefusal: { code: 'quota.active_turns' } });
    await openChat(api);
    await ask();
    const block = await within(await theChat()).findByRole('alert', { name: /^Сейчас готов/u });
    expect(block.textContent).toContain('Ответы на ваши вопросы готовятся на сайте или в другом приложении этого аккаунта');
    expect(await within(block).findByRole('link', { name: 'Тарифы' })).toBeTruthy();
  });

  // The words of where the answers are prepared stand only while no answer is prepared here: a chat of this saga that works is
  // named and led to instead, and a turn the window awaits in another saga is prepared here too.
  it('«разом» while a chat of this saga works: that chat, no word of where they are prepared', async () => {
    const api = await theAccount({ accountTurnRefusal: { code: 'quota.active_turns' } });
    const working = await api.createChatSession(projectId, { scope: 'project', sourceView: 'published', title: 'Северный суд' },
      { idempotencyKey: createIdempotencyKey('createChatSession') });
    const list = api.listChatSessions.bind(api);
    vi.spyOn(api, 'listChatSessions').mockImplementation(async (...args) => {
      const page = await list(...args);
      return { ...page, data: page.data.map((session) => (session.id === working.id ? { ...session, latestTurnStatus: 'running' as const } : session)) };
    });
    await openChat(api);
    await ask();
    const block = await within(await theChat()).findByRole('alert', { name: /^Сейчас готов/u });
    expect(await within(block).findByRole('button', { name: '«Северный суд»' })).toBeTruthy();
    expect(block.textContent).not.toContain(elsewhereWords);
  });

  it('«разом» while the window awaits a turn of another saga: no word of where they are prepared', async () => {
    watchTurn({ chatId: 'chat-of-another-saga', jobId: 'job-of-another-saga', projectId: 'project-another-saga' });
    const api = await theAccount({ accountTurnRefusal: { code: 'quota.active_turns' } });
    await openChat(api);
    await ask();
    const block = await within(await theChat()).findByRole('alert', { name: /^Сейчас готов/u });
    expect(await within(block).findByRole('link', { name: 'Тарифы' })).toBeTruthy();
    expect(block.textContent).not.toContain(elsewhereWords);
  });

  it('the count over the field is the server\'s, as on the site', async () => {
    const api = await theAccount();
    vi.spyOn(api, 'getAccountQuota').mockImplementation(async () => {
      const quota = await (await import('../../api/mock')).createMockApiClient().getAccountQuota();
      return { ...quota, chatTurns: { used: 28, limit: 30 } };
    });
    await openChat(api);
    expect(await within(await theChat()).findByText('Осталось 2 из 30 вопросов на сегодня. Новые — после 00:00 по Москве.')).toBeTruthy();
  });
});

// AW4.2: every refusal of the start — its words of Data and its way out; the second sentence of the return to the count only by
// `mayReturn`; none of them is «Слишком много вопросов подряд».
describe('a question of the helper of the account refused at its start', () => {
  it.each([
    ['ai.account_unreachable', true, 'Нет связи с сервером CanonKeeper — вопрос не отправлен и остался в поле. Если он успел засчитаться, сервер сам вернёт его в остаток в течение пяти минут', null],
    ['ai.account_unreachable', false, 'Нет связи с сервером CanonKeeper — вопрос не отправлен и остался в поле', null],
    ['ai.daily_fence', undefined, 'Сегодня помощник из приложения больше не отвечает: превышен суточный предел его работы. Вопрос остался в поле — задайте его после 00:00 по Москве', null],
    ['ai.app_unfit', undefined, 'Эта версия CanonKeeper устарела для сервера — вопрос не отправлен', 'Открыть обновления'],
    ['app.request_too_large', undefined, 'Этот разговор стал слишком длинным для помощника CanonKeeper — вопрос не отправлен', 'Новый чат'],
  ] as const)('%s (mayReturn %s)', async (code, mayReturn, words, way) => {
    const api = await theAccount({ accountTurnRefusal: { code, mayReturn } });
    await openChat(api);
    const field = await ask();
    const chat = await theChat();
    const said = await within(chat).findByText(words);
    expect(chat.textContent).not.toMatch(/Слишком много вопросов подряд|Спросить снова можно через/u);
    expect(field.value).toBe(question);
    // The field sends the question again with its «Спросить» (board 44·45): the block has no second retry.
    expect(within(chat).queryByRole('button', { name: 'Спросить снова' })).toBeNull();
    const block = said.closest('[role="alert"]') as HTMLElement;
    // No title on the screen: the reader of the screen names the block by what became of the question.
    expect(block.getAttribute('aria-label')).toBe('Вопрос не отправлен');
    if (way) expect(within(block).getByRole('button', { name: way })).toBeTruthy();
    if (way === 'Открыть обновления') {
      fireEvent.click(within(block).getByRole('button', { name: way }));
      expect(await screen.findByText('/settings?part=updates')).toBeTruthy();
    }
  });

  // The link broke on the site at the moment of the question: the server refuses it 409 helper.not_connected. The edition reads
  // the link, the settings of the helper and the status of the saga again and says the one reason of the first part with its
  // way out (specs/2026-11-15-desk-account-link-web.md AW3.4); «Спросить» says the same reason.
  it('helper.not_connected: the link broke at the moment of the question — the one reason with «Связать снова»', async () => {
    const api = await theAccount();
    await openChat(api);
    const relinked = vi.spyOn(api, 'startAccountLink');
    const read = vi.spyOn(api, 'getAccount');
    await theField();
    const before = read.mock.calls.length;
    api.setScenario({ ...api.getScenario(), accountBroken: true });
    const field = await ask();
    const chat = await theChat();
    const block = await within(chat).findByRole('alert', { name: 'Вопрос не отправлен' });
    await waitFor(() => expect(block.textContent).toContain('Помощник не подключён: связь с аккаунтом прервана. Вопрос остался в поле'));
    expect(read.mock.calls.length).toBeGreaterThan(before);
    expect(chat.textContent).not.toContain('Помощник ещё не подключён');
    expect(field.value).toBe(question);
    // «Спросить» says the same reason, read from the same answers.
    const send = within(chat).getByRole('button', { name: 'Спросить' });
    await waitFor(() => expect(reasonOf(send)).toBe('Помощник не подключён: связь с аккаунтом прервана'));
    fireEvent.click(within(block).getByRole('button', { name: 'Связать снова' }));
    // «Связать снова» of the lost link of the helper of the account brings that helper back (decision 15.11).
    await waitFor(() => expect(relinked).toHaveBeenCalledWith({ chooseHelper: true }));
  });

  it('follows the value of the server: `mayReturn` adds the sentence of the return', () => {
    const refusal = (mayReturn: boolean) => refusedWordsOf('ai.account_unreachable', 'start', mayReturn)?.text;
    expect(refusal(true)).not.toBe(refusal(false));
    expect(refusal(true)?.startsWith(refusal(false)!)).toBe(true);
  });
});

// AW4.2: a correction takes the same refusals in its own word; while the answer goes «Уточнить» and «Остановить» stand.
describe('a correction of the helper of the account refused', () => {
  it.each([
    ['ai.account_unreachable', true, 'Нет связи с сервером CanonKeeper — уточнение не отправлено и осталось в поле. Если оно успело засчитаться, сервер сам вернёт его в остаток в течение пяти минут'],
    ['ai.account_unreachable', false, 'Нет связи с сервером CanonKeeper — уточнение не отправлено и осталось в поле'],
    ['ai.daily_fence', undefined, 'Сегодня помощник из приложения больше не отвечает: превышен суточный предел его работы. Уточнение осталось в поле — задайте его после 00:00 по Москве'],
    ['ai.app_unfit', undefined, 'Эта версия CanonKeeper устарела для сервера — уточнение не отправлено'],
    ['app.request_too_large', undefined, 'Этот разговор стал слишком длинным для помощника CanonKeeper — уточнение не отправлено'],
  ] as const)('%s (mayReturn %s)', async (code, mayReturn, words) => {
    const api = await theAccount();
    await openChat(api);
    await ask();
    const correction = await within(await theChat()).findByRole('textbox', { name: 'Уточнение' });
    api.setScenario({ ...api.getScenario(), accountTurnRefusal: { code, mayReturn } });
    fireEvent.change(correction, { target: { value: 'Только про вторую книгу' } });
    const send = within(await theChat()).getByRole('button', { name: 'Уточнить' });
    await waitFor(() => expect(send.getAttribute('aria-disabled')).not.toBe('true'));
    fireEvent.click(send);
    const chat = await theChat();
    const said = await within(chat).findByText(words);
    expect(said.closest('[role="alert"]')?.getAttribute('aria-label')).toBe('Уточнение не отправлено');
    expect(chat.textContent).not.toMatch(/Слишком много уточнений подряд/u);
    expect((within(chat).getByRole('textbox', { name: 'Уточнение' }) as HTMLTextAreaElement).value).toBe('Только про вторую книгу');
    expect(within(chat).getByRole('button', { name: 'Остановить' })).toBeTruthy();
  });

  it('helper.not_connected: the link broke while the answer went — the one reason, the correction stays in the field', async () => {
    const api = await theAccount();
    await openChat(api);
    await ask();
    const correction = await within(await theChat()).findByRole('textbox', { name: 'Уточнение' });
    api.setScenario({ ...api.getScenario(), accountBroken: true });
    fireEvent.change(correction, { target: { value: 'Только про вторую книгу' } });
    const send = within(await theChat()).getByRole('button', { name: 'Уточнить' });
    await waitFor(() => expect(send.getAttribute('aria-disabled')).not.toBe('true'));
    fireEvent.click(send);
    const block = await within(await theChat()).findByRole('alert', { name: 'Уточнение не отправлено' });
    await waitFor(() => expect(block.textContent).toContain('Помощник не подключён: связь с аккаунтом прервана. Уточнение осталось в поле'));
    expect(within(block).getByRole('button', { name: 'Связать снова' })).toBeTruthy();
    expect((await theChat()).textContent).not.toContain('Помощник ещё не подключён');
  });
});

/** «Чат» of the app with a chat whose last turn failed with the code and the count of the scenario. */
async function failedTurn(code: string, returned: 'yes' | 'no' | 'pending' | 'none' | undefined) {
  const api = await theAccount();
  const chat = await api.createChatSession(projectId, { scope: 'project', sourceView: 'published', title: 'Застава' },
    { idempotencyKey: createIdempotencyKey('createChatSession') });
  const turn = await api.createChatTurn(chat.id, { body: question }, { idempotencyKey: createIdempotencyKey('createChatTurn') });
  api.setScenario({ ...api.getScenario(), turnFailure: code, questionReturned: returned });
  vi.spyOn(api, 'getAgentTimeline').mockResolvedValue({
    eof: true, needsReconcile: false, nextCursor: null,
    events: [{ cursor: 'tl_1', id: 'failed-question', jobId: turn.jobId, kind: 'user', logicalSection: 0, role: 'main', text: question }],
  } as unknown as Awaited<ReturnType<Api['getAgentTimeline']>>);
  renderChat(api, createAppRoute('/chat', { projectId, chatId: chat.id }));
  return { api, block: await within(await theChat()).findByRole('alert', {}, { timeout: 10_000 }) };
}

// AW4.3, AW4.4: every reason of a failed turn — its words of Data and its way out; the block under the question is the same at
// any count, with one line of the count only by `questionReturned`.
describe('a failed turn of the helper of the account', () => {
  it.each([
    ['model.daily_fence', 'Сегодня помощник из приложения больше не отвечает: превышен суточный предел его работы. Завтра он снова ответит', null],
    ['model.account_unlinked', 'Связь с аккаунтом прервана', 'Связать снова'],
    ['model.permit_ended', 'Вопрос ждал слишком долго, и сервер закрыл его', 'Повторить вопрос'],
    ['model.site_unavailable', 'Помощник на сервере сейчас не отвечает. Попробуйте позже', 'Повторить вопрос'],
    ['model.app_unfit', 'Эта версия CanonKeeper устарела для сервера', 'Открыть обновления'],
    ['model.thread_too_large', 'Этот разговор стал слишком длинным для помощника CanonKeeper', 'Новый чат'],
    ['model.site_unreachable', 'Нет связи с сервером CanonKeeper', 'Повторить вопрос'],
    ['model.session_ended', 'Ответ прервался: CanonKeeper был перезапущен', 'Повторить вопрос'],
    ['quota.chat_turns_daily', 'Пока вопрос ждал, вопросы и уточнения на сегодня закончились', 'Тарифы'],
    ['quota.active_turns', 'Пока вопрос ждал, место заняли другие ваши вопросы: разом помощник готовит не больше \\d+ ответов\\. Спросите снова, когда один из них будет готов', 'Повторить вопрос'],
    // A code of the site keeps the words of the core, but where they speak of the site or lose their advice without the line of
    // the count of the core.
    ['model.server_overloaded', 'Помощник сейчас перегружен', 'Повторить вопрос'],
    ['model.usage_limit_exceeded', 'Сервис перегружен\\. Повторите через несколько минут', 'Повторить вопрос'],
    ['model.turn_interrupted', 'Ответ прервался\\. Спросите ещё раз', 'Повторить вопрос'],
  ] as const)('%s', async (code, title, way) => {
    const { block } = await failedTurn(code, 'yes');
    expect(block.getAttribute('aria-label')).toMatch(new RegExp(`^${title}`, 'u'));
    expect(block.textContent).toContain('Этот вопрос не засчитан');
    const ways = within(block).queryAllByRole(way === 'Тарифы' ? 'link' : 'button').map((each) => each.textContent);
    if (way) await waitFor(() => expect(within(block).getAllByRole(way === 'Тарифы' ? 'link' : 'button').map((each) => each.textContent)).toContain(way));
    else expect(ways.filter((name) => name !== 'Написать нам')).toEqual([]);
    expect(block.textContent).not.toMatch(/Вопрос не засчитан — повторите/u);
  });

  it.each([
    ['yes', 'Этот вопрос не засчитан'],
    ['pending', 'Засчитан ли этот вопрос, решит сервер в течение нескольких минут'],
    ['no', null],
    ['none', null],
  ] as const)('says the count %s only by the sign of the server, under the same block with «Повторить вопрос»', async (returned, line) => {
    const { block } = await failedTurn('model.site_unreachable', returned);
    expect(block.getAttribute('aria-label')).toBe('Нет связи с сервером CanonKeeper');
    await waitFor(() => expect(within(block).getByRole('button', { name: 'Повторить вопрос' })).toBeTruthy());
    for (const words of Object.values(countWords)) {
      if (words === line) expect(block.textContent).toContain(words);
      else expect(block.textContent).not.toContain(words);
    }
    expect(block.textContent).not.toMatch(/не засчитан\./u);
  });

  // m4, m7 of the review: the advice of the shared limit of the service stands in the words of the block at any count, and an
  // interrupted answer of the app says nothing of an update of the service.
  it.each([['yes'], ['pending'], ['no'], ['none']] as const)('model.usage_limit_exceeded at the count %s keeps its advice', async (returned) => {
    const { block } = await failedTurn('model.usage_limit_exceeded', returned);
    expect(block.getAttribute('aria-label')).toBe('Сервис перегружен. Повторите через несколько минут');
    await waitFor(() => expect(within(block).getByRole('button', { name: 'Повторить вопрос' })).toBeTruthy());
  });

  it('model.turn_interrupted of the app says nothing of an update of the service', async () => {
    const { block } = await failedTurn('model.turn_interrupted', 'no');
    expect(block.textContent).not.toMatch(/обновляли сервис/u);
  });

  // The block of the failed turn stands while that turn is the last failed one of the feed: a new question refused at its start
  // is its own block over the field, and the count of the failed one is still by the sign of the server (AW4.3, AW4.4).
  it('keeps the block of the failed turn when its question asked again is refused at the start', async () => {
    const { api, block } = await failedTurn('model.site_unreachable', 'pending');
    await waitFor(() => expect(block.textContent).toContain(countWords.pending));
    api.setScenario({ ...api.getScenario(), accountTurnRefusal: { code: 'ai.account_unreachable', mayReturn: true } });
    fireEvent.click(within(block).getByRole('button', { name: 'Повторить вопрос' }));
    // The question of the failed turn is in the field: the field sends it again with «Спросить снова».
    const send = within(await theChat()).getByRole('button', { name: 'Спросить снова' });
    await waitFor(() => expect(send.getAttribute('aria-disabled')).not.toBe('true'));
    fireEvent.click(send);
    const chat = await theChat();
    const refused = (await within(chat).findByText(/^Нет связи с сервером CanonKeeper — вопрос не отправлен и остался в поле/u)).closest('[role="alert"]') as HTMLElement;
    const failed = within(chat).getByRole('alert', { name: 'Нет связи с сервером CanonKeeper' });
    expect(failed).not.toBe(refused);
    expect(failed.textContent).toContain(countWords.pending);
    expect(within(failed).getByRole('button', { name: 'Повторить вопрос' })).toBeTruthy();
    expect(chat.textContent).not.toMatch(/не засчитан|Ответ прервался/u);
    expect(refused.textContent).not.toContain(countWords.pending);
    // The refusal stands under the question of the failed turn and its block.
    expect(failed.compareDocumentPosition(refused) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it('«Связать снова» starts a link, «Открыть обновления» opens the part «Обновления»', async () => {
    const unlinked = await failedTurn('model.account_unlinked', 'no');
    const started = vi.spyOn(unlinked.api, 'startAccountLink');
    fireEvent.click(within(unlinked.block).getByRole('button', { name: 'Связать снова' }));
    await waitFor(() => expect(started).toHaveBeenCalledWith({ chooseHelper: true }));
    cleanup();
    const unfit = await failedTurn('model.app_unfit', 'no');
    fireEvent.click(within(unfit.block).getByRole('button', { name: 'Открыть обновления' }));
    expect(await screen.findByText('/settings?part=updates')).toBeTruthy();
  });
});

// Decision of the coordinator 15.11: «Спросить» is closed by the reason «помощник не подключён» whenever the answers of the settings
// of the helper and of the link say so — also in a read saga, where the status says nothing of a waiting — and the line over the
// field says the same reason with its button before any question; until the answers are read, «Спросить» is as it was.
describe('a read saga with the helper not connected', () => {
  /** The status of a saga whose every chapter is read: no preparation, no reading — what the server says without a helper. */
  const readSaga = { published: { exists: true, context: { state: 'ready', currentBook: null, retryableFailedBook: null } }, steps: [] } as unknown as IndexingStatus;

  async function sendOf() {
    const field = await theField();
    fireEvent.change(field, { target: { value: question } });
    return within(await theChat()).getByRole('button', { name: 'Спросить' });
  }

  it('the helper of the account with a broken link — the reason at «Спросить» and «Связать снова» before a question', async () => {
    const api = await theAccount();
    api.setScenario({ ...api.getScenario(), accountBroken: true });
    vi.spyOn(api, 'getIndexingStatus').mockResolvedValue(readSaga);
    const started = vi.spyOn(api, 'startAccountLink');
    const created = vi.spyOn(api, 'createChatTurn');
    renderChat(api);
    const send = await sendOf();
    await waitFor(() => expect(reasonOf(send)).toBe('Помощник не подключён: связь с аккаунтом прервана'));
    expect(send.getAttribute('aria-disabled')).toBe('true');
    const note = await within(await theChat()).findByRole('note', { name: 'Помощник не подключён' });
    expect(note.textContent).toContain('Помощник не подключён: связь с аккаунтом прервана');
    fireEvent.click(within(note).getByRole('button', { name: 'Связать снова' }));
    await waitFor(() => expect(started).toHaveBeenCalledWith({ chooseHelper: true }));
    expect(created).not.toHaveBeenCalled();
  });

  it('no helper chosen — «Подключить помощника» opens the window of the choice', async () => {
    const api = createMockApiClient();
    api.setScenario({ preset: 'normal', actorRole: 'owner' });
    vi.spyOn(api, 'getIndexingStatus').mockResolvedValue(readSaga);
    renderChat(api);
    const send = await sendOf();
    await waitFor(() => expect(reasonOf(send)).toBe('Помощник ещё не подключён. Читать книги и искать по ним можно уже сейчас'));
    const note = await within(await theChat()).findByRole('note', { name: 'Помощник ещё не подключён' });
    fireEvent.click(within(note).getByRole('button', { name: 'Подключить помощника' }));
    expect(await screen.findByRole('dialog', { name: 'Подключить помощника' })).toBeTruthy();
  });

  it('the helper connected — «Спросить» is open', async () => {
    const api = await theAccount();
    vi.spyOn(api, 'getIndexingStatus').mockResolvedValue(readSaga);
    const read = vi.spyOn(api, 'getAccount');
    renderChat(api);
    const send = await sendOf();
    await waitFor(() => expect(read).toHaveBeenCalled());
    await waitFor(() => expect(send.getAttribute('aria-disabled')).not.toBe('true'));
    expect(reasonOf(send)).toBeNull();
    expect(within(await theChat()).queryByRole('note', { name: /^Помощник (ещё )?не подключён/u })).toBeNull();
  });

  it('until the settings and the link are read — «Спросить» as it was, no reason flickers', async () => {
    const api = await theAccount();
    api.setScenario({ ...api.getScenario(), accountBroken: true });
    vi.spyOn(api, 'getIndexingStatus').mockResolvedValue(readSaga);
    vi.spyOn(api, 'getAccount').mockImplementation(() => new Promise(() => undefined));
    renderChat(api);
    const send = await sendOf();
    await waitFor(() => expect(send.getAttribute('aria-disabled')).not.toBe('true'));
    await new Promise((resolve) => setTimeout(resolve, 300));
    expect(reasonOf(send)).toBeNull();
    expect(within(await theChat()).queryByRole('note', { name: 'Помощник не подключён' })).toBeNull();
  });
});

// m3 of the review: «разом» took the place of a question that waited — the limit of the server when it is known, else no number.
describe('a failed turn whose place the other questions took', () => {
  const quota = (limit: number) => ({ activeTurns: { used: limit, limit } }) as unknown as Parameters<typeof turnWordsOf>[1];

  it.each([
    [2, 'Пока вопрос ждал, место заняли другие ваши вопросы: разом помощник готовит не больше 2 ответов. Спросите снова, когда один из них будет готов'],
    [1, 'Пока вопрос ждал, место заняли другие ваши вопросы: разом помощник готовит не больше 1 ответа. Спросите снова, когда он будет готов'],
  ] as const)('the limit %d of the server', (limit, words) => {
    expect(turnWordsOf('quota.active_turns', quota(limit))).toEqual({ title: words, way: 'ask_again' });
  });

  it('without the limit of the server — without a number', () => {
    expect(turnWordsOf('quota.active_turns', undefined)).toEqual({
      title: 'Пока вопрос ждал, место заняли другие ваши вопросы. Спросите снова, когда один из ответов будет готов', way: 'ask_again',
    });
  });
});

// (б) the table of the words of the app against the lists of the contract: no code of a refusal or a failure without its words.
describe('the words of the refusals and the failures of the app against the contract', () => {
  const contract = JSON.parse(readFileSync(join(process.cwd(), 'contracts/openapi.desktop.json'), 'utf8')) as {
    components: { schemas: { Job: { properties: { error: { description: string } } }; Problem: { properties: { mayReturn: { description: string } } } } };
  };

  it('names every code of a failed turn of the account and of a restart', () => {
    const listed = contract.components.schemas.Job.properties.error.description;
    const ofTheAccount = listed.slice(listed.indexOf('«Аккаунт CanonKeeper»'));
    const codes = [...new Set([...ofTheAccount.matchAll(/(?:model|quota)\.[a-z_]+/gu)].map((match) => match[0])), 'model.session_ended'];
    expect(codes.length).toBeGreaterThanOrEqual(10);
    for (const code of codes) expect(turnWordsOf(code, undefined), code).not.toBeNull();
    for (const code of Object.keys(turnWords)) expect(listed, code).toContain(code);
  });

  it('names every refusal of the start the contract lists, but the helper not connected (the one reason of the edition)', () => {
    const listed = contract.components.schemas.Problem.properties.mayReturn.description;
    const codes = [...new Set([...listed.matchAll(/(?:ai|app|helper)\.[a-z]+_[a-z_]+/gu)].map((match) => match[0]))];
    expect(codes).toEqual(expect.arrayContaining(['ai.account_unreachable', 'ai.daily_fence', 'ai.app_unfit', 'app.request_too_large', 'helper.not_connected']));
    for (const code of codes.filter((each) => each !== 'helper.not_connected')) {
      for (const operation of ['start', 'steer'] as const) expect(refusedWordsOf(code, operation, true), `${code} ${operation}`).not.toBeNull();
    }
    expect(refusedWordsOf('helper.not_connected', 'start', false)).toBeNull();
  });

  it('declares 503 with mayReturn at the start of a question, a question with a chapter, a correction and an upload of materials', () => {
    const paths = (JSON.parse(readFileSync(join(process.cwd(), 'contracts/openapi.desktop.json'), 'utf8')) as {
      paths: Record<string, Record<string, { operationId?: string; responses?: Record<string, unknown> }>>;
    }).paths;
    const operations = Object.values(paths).flatMap((item) => Object.values(item));
    for (const id of ['createChatTurn', 'createChatTurnWithChapter', 'steerChatTurn', 'startAuthorMaterialsImport']) {
      expect(operations.find((operation) => operation.operationId === id)?.responses?.['503'], id).toBeTruthy();
    }
  });

  it('says nothing the dictionary hides where the words stand', () => {
    const sayings = [
      ...Object.values(turnWords).map((words) => words.title), turnWordsOf('quota.chat_turns_daily', undefined)!.title, ...Object.values(countWords),
      turnWordsOf('quota.active_turns', undefined)!.title,
      ...['ai.account_unreachable', 'ai.daily_fence', 'ai.app_unfit', 'app.request_too_large'].flatMap((code) => (['start', 'steer'] as const)
        .map((operation) => refusedWordsOf(code, operation, true)!.text)),
      'Ответы на ваши вопросы готовятся на сайте или в другом приложении этого аккаунта',
    ];
    for (const saying of sayings) expect(hiddenIn(saying, 'src/editions/desktop/chat-words.ts').map((word) => word.says), saying).toEqual([]);
  });
});
