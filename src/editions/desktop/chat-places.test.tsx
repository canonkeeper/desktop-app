// SPDX-License-Identifier: AGPL-3.0-only
import { cleanup, fireEvent, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { projectId } from '../../api/mock/repository/fixtures';
import type { MockScenarioControl } from '../../api/mock/scenarios';
import { createIdempotencyKey } from '../../shared/api';
import { createAppRoute } from '../../shared/navigation/app-route';
import { hiddenIn } from '../../shared/copy/dictionary';
import { FakeWebSocket } from '../../test/FakeWebSocket';
import { forgetAllTurns, watchTurn } from '../../features/chat/answer-ready';
import { noShell, shellOfTheWindow } from './settings/settings.test-helpers';
import { placesWords } from './chat-words';
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

// Data «слова» of specs/2026-11-15-desk-turns-here.md, word for word.
const otherSaga = 'Ответ на ваш вопрос готовится в другой саге.';
const otherSagas = 'Ответы на ваши вопросы готовятся в других сагах.';
const held = 'Сервер ещё считает недавний вопрос идущим — его место освободится само за несколько минут.';
const elsewhere = 'Ответы на ваши вопросы готовятся на сайте или в другом приложении этого аккаунта';
const placeLines = [otherSaga, otherSagas, held, elsewhere];

type Places = NonNullable<MockScenarioControl['accountPlaces']>;

/** The app linked with its helper chosen, «разом» refusing the next question, the places as the server counts them. */
async function refusedAtOnce(places?: Places) {
  return theAccount({ accountTurnRefusal: { code: 'quota.active_turns' }, ...(places ? { accountPlaces: places } : {}) });
}

async function openChat(api: Api) {
  const chat = await api.createChatSession(projectId, { scope: 'project', sourceView: 'published', title: 'Застава' },
    { idempotencyKey: createIdempotencyKey('createChatSession') });
  renderChat(api, createAppRoute('/chat', { projectId, chatId: chat.id }));
}

/** The question asked and refused «разом»: the block and the lines of the places it says, in their order. */
async function askRefused() {
  fireEvent.change(await theField(), { target: { value: 'Где стоит застава у перевала?' } });
  fireEvent.click(within(await theChat()).getByRole('button', { name: 'Спросить' }));
  const block = await within(await theChat()).findByRole('alert', { name: /^Сейчас готов/u });
  // The quota read again after the refusal names the places; the lines stand once it came.
  await vi.waitFor(() => expect(block.querySelector('p')).toBeTruthy());
  return block;
}

function linesOf(block: HTMLElement) {
  return [...block.querySelectorAll('p')].map((paragraph) => paragraph.textContent ?? '').filter((text) => placeLines.includes(text));
}

// specs/2026-11-15-desk-turns-here.md TH3.1–TH3.3: the block of «разом» says who holds the places by the numbers of the server,
// each line on its own condition, in the order of Data; the page compares nothing of them itself.
describe('«разом» of the helper of the account says who holds the places', () => {
  it.each([
    ['an answer in another saga', { answering: 1, held: 0, elsewhere: 0 }, [otherSaga]],
    ['answers in other sagas', { answering: 2, held: 0, elsewhere: 0 }, [otherSagas]],
    ['a place the server frees by itself', { answering: 0, held: 1, elsewhere: 0 }, [held]],
    ['the site or another app', { answering: 0, held: 0, elsewhere: 2 }, [elsewhere]],
    ['another saga and the site at once', { answering: 1, held: 0, elsewhere: 1 }, [otherSaga, elsewhere]],
    ['all three', { answering: 3, held: 1, elsewhere: 1 }, [otherSagas, held, elsewhere]],
    ['three naughts — the numbers changed meanwhile', { answering: 0, held: 0, elsewhere: 0 }, []],
  ] as const)('%s', async (_name, places, lines) => {
    const api = await refusedAtOnce(places);
    await openChat(api);
    const block = await askRefused();
    await vi.waitFor(() => expect(linesOf(block)).toEqual(lines));
    // The block itself stays: the words of «разом», «Тарифы».
    expect(within(block).getByRole('link', { name: 'Тарифы' })).toBeTruthy();
  });

  // A chat of this saga that works is named and led to; «другая сага» counts the answers beyond the chats named.
  it.each([
    ['one answer more than the chats named', 2, [otherSaga]],
    ['as many answers as the chats named', 1, []],
  ] as const)('a chat of this saga works: %s', async (_name, answering, lines) => {
    const api = await refusedAtOnce({ answering, held: 0, elsewhere: 0 });
    const working = await api.createChatSession(projectId, { scope: 'project', sourceView: 'published', title: 'Северный суд' },
      { idempotencyKey: createIdempotencyKey('createChatSession') });
    const list = api.listChatSessions.bind(api);
    vi.spyOn(api, 'listChatSessions').mockImplementation(async (...args) => {
      const page = await list(...args);
      return { ...page, data: page.data.map((session) => (session.id === working.id ? { ...session, latestTurnStatus: 'running' as const } : session)) };
    });
    await openChat(api);
    const block = await askRefused();
    expect(await within(block).findByRole('button', { name: '«Северный суд»' })).toBeTruthy();
    await vi.waitFor(() => expect(linesOf(block)).toEqual(lines));
  });

  // The page guesses nothing once the server counted: a turn the window awaits hides no line of the server.
  it('a turn the window awaits changes nothing of what the server counted', async () => {
    watchTurn({ chatId: 'chat-of-another-saga', jobId: 'job-of-another-saga', projectId: 'project-another-saga' });
    const api = await refusedAtOnce({ answering: 0, held: 0, elsewhere: 1 });
    await openChat(api);
    const block = await askRefused();
    await vi.waitFor(() => expect(linesOf(block)).toEqual([elsewhere]));
  });

  // TH3.4: a server without the places — the rule of the page as before: the line where they are prepared while the block named
  // no chat and the window awaits no turn (the tests of chat-account-words.test.tsx keep the other two cases).
  it('without the places of the server, the rule of the page as before', async () => {
    const api = await refusedAtOnce();
    await openChat(api);
    const block = await askRefused();
    await vi.waitFor(() => expect(linesOf(block)).toEqual([elsewhere]));
  });

  it('the words are those of Data and the dictionary of the page hides none of them', () => {
    expect(placesWords).toEqual({ otherSaga, otherSagas, held, elsewhere });
    for (const words of placeLines) expect(hiddenIn(words, 'src/editions/desktop/chat-words.ts').map((word) => word.says), words).toEqual([]);
  });
});
