// SPDX-License-Identifier: AGPL-3.0-only
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { Suspense, useCallback, useState } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../api/mock';
import { projectId } from '../../api/mock/repository/fixtures';
import { normalScenario } from '../../api/mock/scenarios';
import { InShell } from '../../app/shell/AppShell.test-helpers';
import { createIdempotencyKey } from '../../shared/api';
import { createAppRoute, type AppRoute, type RouteNavigate } from '../../shared/navigation/app-route';
import { reasonOf } from '../../test/disabled-reason';
import { FakeWebSocket } from '../../test/FakeWebSocket';
import { ToastsProvider } from '../../ui/feedback/Toast';
import { helperReasonOf } from './account/helper-reason';
import { noShell, shellOfTheWindow } from './settings/settings.test-helpers';
import { release, updateAt } from './updates/update-test-helpers';
import { edition } from '.';

vi.mock('@tanstack/react-router', async (actual) => ({ ...await actual<object>(), useBlocker: vi.fn(() => ({ status: 'idle' })) }));

type Api = ReturnType<typeof createMockApiClient>;

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
  FakeWebSocket.reset();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

const notConnected = 'Помощник ещё не подключён. Читать книги и искать по ним можно уже сейчас';
const broken = 'Помощник не подключён: связь с аккаунтом прервана';
const waiting = 'Помощник ещё не подключён: связь ждёт подтверждения в браузере';

// specs/2026-11-15-desk-account-link-web.md AW3.4: one function of the edition says why the helper is not connected, by the
// source of the settings and the state of the link.
describe('the one reason «помощник не подключён»', () => {
  it('says the broken link of the account, the waiting, the account not linked or none chosen, and «Свой ИИ» as before', () => {
    const notYet = 'Помощник ещё не подключён';
    expect(helperReasonOf('account', 'broken')).toEqual({ title: broken, label: 'Помощник не подключён', action: 'relink' });
    expect(helperReasonOf('account', 'waiting')).toEqual({ title: waiting, label: notYet, action: 'linkWindow' });
    expect(helperReasonOf('none', 'waiting')).toEqual({ title: waiting, label: notYet, action: 'linkWindow' });
    for (const state of ['none', 'failed'] as const) expect(helperReasonOf('account', state)).toEqual({ title: notConnected, label: notYet, action: 'connect' });
    expect(helperReasonOf('none', 'none')).toEqual({ title: notConnected, label: notYet, action: 'connect' });
    expect(helperReasonOf('own', 'broken')).toEqual({ title: notConnected, label: notYet, action: 'connect' });
  });
});

type ChatPageInput = Parameters<NonNullable<typeof edition.pages.chat>['render']>[0];

/**
 * The chat of the app as the root of the web puts it (src/App.tsx): the page of the chat of the edition in the shell under what
 * the account of the edition wraps the pages in — the update of the app with the one slot of the chat, the link with an account,
 * the slot of the line of the reading.
 */
function ChatOfTheApp({ api, start }: { api: Api; start: AppRoute }) {
  const [route, setRoute] = useState(start);
  const navigate = useCallback<RouteNavigate>((path, query = {}) => setRoute(createAppRoute(path, query)), []);
  const account = edition.useAccount({ api, navigate, route } as unknown as Parameters<typeof edition.useAccount>[0]);
  const user = { id: 'user-local' } as ChatPageInput['user'];
  return account.wrapPages(
    <InShell api={api} navigate={navigate} route={route}>
      {route.path === '/chat'
        ? <Suspense fallback={null}>{edition.pages.chat!.render({ api, navigate, route, user })}</Suspense>
        : <p>{`${route.path}?part=${route.query.get('part') ?? ''}`}</p>}
    </InShell>,
  );
}

function renderChat(api: Api, start = createAppRoute('/chat', { projectId, new: '1' })) {
  return render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { mutations: { retry: false }, queries: { retry: false } } })}>
      <ToastsProvider><ChatOfTheApp api={api} start={start} /></ToastsProvider>
    </QueryClientProvider>,
  );
}

/** The mock of the saga of the chat with a helper that is not connected. */
function theSaga() {
  const api = createMockApiClient();
  api.setScenario({ actorRole: 'owner', preset: 'normal', helperMissing: true });
  return api;
}

/**
 * The line over the field and the reason at «Спросить»: the same words; the line is named by its words — a broken link is not
 * «ещё не подключён».
 */
async function lineAndField(words: string) {
  const chat = await screen.findByRole('region', { name: 'Исследовательский чат' }, { timeout: 5000 });
  const note = await within(chat).findByRole('note', { name: words === broken ? 'Помощник не подключён' : 'Помощник ещё не подключён' });
  await waitFor(() => expect(within(note).getByText(words)).toBeTruthy());
  const ask = within(chat).getByRole('button', { name: 'Спросить' });
  expect(ask.getAttribute('aria-disabled')).toBe('true');
  // The field says the same reason at «Спросить».
  await waitFor(() => expect(reasonOf(ask)).toBe(words));
  return note;
}

describe('the line of the reading and the field of the chat say one reason', () => {
  it('the helper of the account and a broken link — «Связать снова» starts a link to choose it', async () => {
    const api = theSaga();
    api.setScenario({ ...api.getScenario(), accountLinked: true });
    await api.updateAiSettings({ source: 'account' });
    api.setScenario({ ...api.getScenario(), accountBroken: true });
    const started = vi.spyOn(api, 'startAccountLink');
    renderChat(api);
    const note = await lineAndField(broken);
    fireEvent.click(within(note).getByRole('button', { name: 'Связать снова' }));
    await waitFor(() => expect(started).toHaveBeenCalledWith({ chooseHelper: true }));
    expect(await screen.findByRole('dialog', { name: 'Подтвердите связь в браузере: там открылась страница CanonKeeper' })).toBeTruthy();
  });

  it('a link that waits — «Открыть окно связи» shows its window', async () => {
    const api = theSaga();
    await api.startAccountLink({ chooseHelper: true });
    renderChat(api);
    const note = await lineAndField(waiting);
    fireEvent.click(within(note).getByRole('button', { name: 'Открыть окно связи' }));
    expect(await screen.findByRole('dialog', { name: 'Подтвердите связь в браузере: там открылась страница CanonKeeper' })).toBeTruthy();
  });

  it('the helper of the account not linked — «Подключить помощника» opens the window of the choice', async () => {
    const api = theSaga();
    api.setScenario({ ...api.getScenario(), accountLinked: true });
    await api.updateAiSettings({ source: 'account' });
    await api.unlinkAccount();
    renderChat(api);
    const note = await lineAndField(notConnected);
    fireEvent.click(within(note).getByRole('button', { name: 'Подключить помощника' }));
    expect(await screen.findByRole('dialog', { name: 'Подключить помощника' })).toBeTruthy();
  });

  it('«Свой ИИ» not connected — the words it had', async () => {
    const api = theSaga();
    await api.updateAiSettings({ source: 'own', own: { preset: 'deepseek', chatModel: 'deepseek-chat', cardsModel: 'deepseek-chat' } });
    renderChat(api);
    const note = await lineAndField(notConnected);
    expect(within(note).getByRole('button', { name: 'Подключить помощника' })).toBeTruthy();
  });
});

const question = 'Где стоит застава у перевала?';

/** The words of «Спросить» of the field of the chat once a question is in it. */
async function askReason() {
  const chat = await screen.findByRole('region', { name: 'Исследовательский чат' }, { timeout: 5000 });
  fireEvent.change(within(chat).getByRole('textbox', { name: 'Вопрос' }), { target: { value: question } });
  return within(chat).getByRole('button', { name: 'Спросить' });
}

// AW3.4 with specs/2026-11-15-desk-updates-web.md UW3.4 and specs/2026-11-15-desk-interrupted-turn.md IT1.4: the app has one
// slot of the chat — the update of the app puts it around the pages with every field of the chat of the app; a slot that drops
// a field or a second slot under it would hide the reason of the helper, the reason of the update or the words of a question
// a restart interrupted.
describe('the one slot of the chat of the app, through the pages of the edition', () => {
  it('brings the reason of the helper, the reason of the update and the words of an interrupted question to the chat', async () => {
    // The helper of the account with a broken link, then with a link that waits.
    const brokenLink = theSaga();
    brokenLink.setScenario({ ...brokenLink.getScenario(), accountLinked: true });
    await brokenLink.updateAiSettings({ source: 'account' });
    brokenLink.setScenario({ ...brokenLink.getScenario(), accountBroken: true });
    const first = renderChat(brokenLink);
    const relink = await askReason();
    await waitFor(() => expect(reasonOf(relink)).toBe(broken));
    first.unmount();

    const waits = theSaga();
    await waits.startAccountLink({ chooseHelper: true });
    const second = renderChat(waits);
    const linkWindow = await askReason();
    await waitFor(() => expect(reasonOf(linkWindow)).toBe(waiting));
    second.unmount();

    // The helper connected and the app installs an update: the reason of the update.
    const installs = createMockApiClient();
    vi.spyOn(installs, 'getAppUpdate').mockResolvedValue(updateAt({ phase: 'installing', found: null, available: release }));
    const third = renderChat(installs);
    const closed = await askReason();
    await waitFor(() => expect(reasonOf(closed)).toBe('Ставлю обновление — спросите после перезапуска'));
    expect(closed.getAttribute('aria-disabled')).toBe('true');
    third.unmount();

    // A question a restart of the app interrupted: the words of the app for the failed turn.
    const interrupted = createMockApiClient();
    const chat = await interrupted.createChatSession(projectId, { scope: 'project', sourceView: 'published', title: 'Застава' },
      { idempotencyKey: createIdempotencyKey('createChatSession') });
    const turn = await interrupted.createChatTurn(chat.id, { body: question }, { idempotencyKey: createIdempotencyKey('createChatTurn') });
    interrupted.setScenario({ ...normalScenario, turnFailure: 'model.session_ended', questionReturned: 'pending' });
    vi.spyOn(interrupted, 'getAgentTimeline').mockResolvedValue({
      eof: true, needsReconcile: false, nextCursor: null,
      events: [{ cursor: 'tl_1', id: 'interrupted-question', jobId: turn.jobId, kind: 'user', logicalSection: 0, role: 'main', text: question }],
    } as unknown as Awaited<ReturnType<typeof interrupted.getAgentTimeline>>);
    renderChat(interrupted, createAppRoute('/chat', { projectId, chatId: chat.id }));
    const block = await screen.findByRole('alert', {}, { timeout: 10_000 });
    expect(block.getAttribute('aria-label')).toBe('Ответ прервался: CanonKeeper был перезапущен');
  });
});
