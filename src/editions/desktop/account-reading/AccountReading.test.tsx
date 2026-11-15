// SPDX-License-Identifier: AGPL-3.0-only
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import type { ReactNode } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../../api/mock';
import { projectId } from '../../../api/mock/repository/fixtures';
import type { MockScenarioControl } from '../../../api/mock/scenarios';
import { manuscriptQueryKeys, useIndexingStatusQuery, type IndexingStatus } from '../../../entities/manuscript/api';
import { usePreparationView } from '../../../entities/manuscript/preparation-slot';
import { ChatPage } from '../../../features/chat/ChatPage';
import { ManuscriptShelfPage } from '../../../features/manuscript/shelf/ManuscriptShelfPage';
import { SagaMap } from '../../../features/saga/SagaMap';
import { ApiStatusError } from '../../../shared/api';
import { createAppRoute, parseRoute, type RouteNavigate } from '../../../shared/navigation/app-route';
import { FakeWebSocket } from '../../../test/FakeWebSocket';
import { ToastsProvider } from '../../../ui/feedback/Toast';
import { noShell, shellOfTheWindow } from '../settings/settings.test-helpers';
import { edition } from '..';

vi.mock('@tanstack/react-router', async (actual) => ({ ...await actual<object>(), useBlocker: vi.fn(() => ({ status: 'idle' })) }));
// WebGL is not in jsdom: the canvas of the map is not what is checked here.
vi.mock('../../../features/saga/MapCanvas', () => ({ MapCanvas: () => <div data-testid="saga-map-canvas" /> }));

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

const plain = (text: string | null | undefined) => (text ?? '').replace(/\u00a0/gu, ' ');
/** The words of «Свой ИИ» the reading through the account never says (AW5.1). */
const ownWords = /провайдер|токен|потрачен|сумм|цен[аыу]|проб[аеу]|своим ИИ/iu;

/** The mock app linked with an account, its helper chosen, in the scenario of the test. */
async function theAccount(scenario: Partial<MockScenarioControl> = {}) {
  const api = createMockApiClient();
  api.setScenario({ preset: 'normal', actorRole: 'owner', accountLinked: true });
  await api.updateAiSettings({ source: 'account' });
  api.setScenario({ ...api.getScenario(), ...scenario });
  return api;
}

function client() {
  return new QueryClient({ defaultOptions: { mutations: { retry: false }, queries: { retry: false } } });
}

/** A page under what the account of the edition wraps the pages in (src/App.tsx): the one slot of the line of the reading. */
function inTheApp(api: Api, page: ReactNode, navigate: RouteNavigate = vi.fn(), queries = client()) {
  function Pages() {
    const account = edition.useAccount({ api, navigate, route: createAppRoute('/manuscript/books', { projectId }) } as unknown as Parameters<typeof edition.useAccount>[0]);
    return <>{account.wrapPages(page)}</>;
  }
  render(<QueryClientProvider client={queries}><ToastsProvider><Pages /></ToastsProvider></QueryClientProvider>);
  return queries;
}

/** A place of the line: the status of the saga by React Query, the lines and the windows of the slot of the edition. */
function Place({ api, navigate = vi.fn() }: { api: Api; navigate?: RouteNavigate }) {
  const status = useIndexingStatusQuery(api, projectId).data;
  const view = usePreparationView({ navigate, projectId, status });
  return (
    <div aria-label="Место строки" role="region">
      {view?.lines.map((line) => <section aria-label={line.label} key={line.label}><p>{line.title}</p>{line.actions}</section>)}
      {view?.windows}
    </div>
  );
}

async function theLine(name: string) {
  return screen.findByRole('region', { name }, { timeout: 5000 });
}

/** «Прочитать книги…» of the line: the window of the consent of the account. */
async function theWindow(api: Api) {
  inTheApp(api, <Place api={api} />);
  fireEvent.click(within(await theLine('Помощник готов прочитать книги')).getByRole('button', { name: 'Прочитать книги…' }));
  return screen.findByRole('dialog', { name: 'Прочитать книги с помощником CanonKeeper?' });
}

// specs/2026-11-15-desk-account-words-web.md AW5.2, AW5.6 (board 44·44): the window of the consent of the account.
describe('the window of the consent of the account', () => {
  it('says what the helper does, the characters of the tier with the time and what was used, and gives the consent without a sum', async () => {
    const api = await theAccount();
    const write = vi.spyOn(api, 'setReadingConsent');
    const sample = vi.spyOn(api, 'getReadingSample');
    const window = await theWindow(api);
    await waitFor(() => expect(plain(window.textContent)).toContain('Помощник прочитает 612 глав и узнает, кто есть кто, что произошло и где это в книге. '
      + 'Текст книг пройдёт через сервер CanonKeeper к помощнику. Книги, которые вы добавите позже, помощник прочитает так же — остановить можно кнопкой «Пауза»'));
    // 6 120 000 characters up — 6,2; 3 200 000 used down — 3,2.
    expect(plain(window.textContent)).toContain('Это займёт до 6,2 млн знаков тарифа · около 11 часов. За 30 дней использовано 3,2 из 40 млн');
    expect(within(window).queryByRole('textbox')).toBeNull();
    expect(plain(window.textContent)).not.toMatch(ownWords);
    expect(within(window).getAllByRole('button').map((button) => button.textContent)).toEqual(['', 'Не сейчас', 'Прочитать книги']);
    fireEvent.click(within(window).getByRole('button', { name: 'Прочитать книги' }));
    await waitFor(() => expect(write).toHaveBeenCalledWith(projectId, { state: 'allowed' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    const toast = await screen.findByText('Помощник читает книги');
    expect(toast.closest('[role="status"]')?.textContent).toContain('Остановить чтение можно кнопкой «Пауза»');
    expect(sample).not.toHaveBeenCalled();
  });

  it.each([
    ['the characters end first', { readingLimits: { characters: { used: 37_960_000, limit: 40_000_000 }, work: { used: 0, limit: 320_000_000 } } },
      'За 30 дней использовано 37,9 из 40 млн знаков тарифа, а чтение займёт до 6,2 млн. Помощник прочитает, сколько поместится'],
    ['the work ends first', { readingLimits: { characters: { used: 0, limit: 40_000_000 }, work: { used: 319_500_000, limit: 320_000_000 } } },
      'На тарифе не хватит работы помощника на все книги за 30 дней. Помощник прочитает, сколько поместится'],
  ] as const)('%s — the numbers of the server and «Тарифы»', async (_case, scenario, words) => {
    const window = await theWindow(await theAccount(scenario));
    const note = await within(window).findByRole('note');
    expect(plain(note.textContent)).toContain(words);
    expect(await within(note).findByRole('link', { name: 'Тарифы' })).toHaveProperty('href', 'https://canonkeeper.example.test/tariffs');
  });

  it('without the limits of the site — the volume alone', async () => {
    const window = await theWindow(await theAccount({ readingLimits: null }));
    await waitFor(() => expect(plain(window.textContent)).toContain('Это займёт до 6,2 млн знаков тарифа · около 11 часов'));
    expect(plain(window.textContent)).not.toContain('использовано');
    expect(within(window).queryByRole('note')).toBeNull();
  });

  it.each([
    [{ namesWait: true, threadsWait: true }, 'имена героев и сюжетные нити', 'Свести имена и нити'],
    [{ namesWait: true }, 'имена героев', 'Свести имена'],
    [{ threadsWait: true }, 'сюжетные нити', 'Свести нити'],
  ] as const)('only the names and the threads left: %o', async (left, words, button) => {
    const again = await theWindow(await theAccount({ readingEstimateLeft: { chapters: 0, ...left } }));
    await waitFor(() => expect(plain(again.textContent)).toContain(`Главы прочитаны. Помощнику осталось свести ${words}. Текст пройдёт через сервер CanonKeeper к помощнику`));
    expect(plain(again.textContent)).not.toContain('знаков тарифа');
    expect(within(again).getByRole('button', { name: button })).toBeTruthy();
  });

  it.each([
    ['the work ends first', { characters: { used: 0, limit: 40_000_000 }, work: { used: 319_500_000, limit: 320_000_000 } }],
    ['the characters are spent', { characters: { used: 40_000_000, limit: 40_000_000 }, work: { used: 319_500_000, limit: 320_000_000 } }],
  ] as const)('only the names left and %s: no word of reading the books, only what is left to bring together', async (_case, readingLimits) => {
    const window = await theWindow(await theAccount({ readingEstimateLeft: { chapters: 0, namesWait: true }, readingLimits }));
    await waitFor(() => expect(plain(window.textContent)).toContain('Главы прочитаны. Помощнику осталось свести имена героев. Текст пройдёт через сервер CanonKeeper к помощнику'));
    expect(within(window).queryByRole('note')).toBeNull();
    expect(plain(window.textContent)).not.toMatch(/прочитает|все книги|знаков тарифа/u);
    expect(within(window).getAllByRole('button').map((button) => button.textContent)).toEqual(['', 'Не сейчас', 'Свести имена']);
  });

  it('nothing to read — «Закрыть»', async () => {
    const window = await theWindow(await theAccount({ readingEstimateLeft: { chapters: 0 } }));
    await waitFor(() => expect(plain(window.textContent)).toContain('Читать нечего: все главы прочитаны, имена героев и сюжетные нити сведены'));
    expect(within(window).getAllByRole('button').map((button) => button.textContent)).toEqual(['', 'Закрыть']);
  });

  it('the link broke while the window was open: «Связь с аккаунтом прервана» with «Связать снова»', async () => {
    const api = await theAccount();
    vi.spyOn(api, 'setReadingConsent').mockRejectedValue(new ApiStatusError(409, {
      type: 'about:blank', title: 'Conflict', status: 409, code: 'helper.not_connected', requestId: 'req-consent',
    }, new Response(null, { status: 409 })));
    const started = vi.spyOn(api, 'startAccountLink');
    const window = await theWindow(api);
    fireEvent.click(await within(window).findByRole('button', { name: 'Прочитать книги' }));
    const refused = await within(window).findByRole('alert', { name: 'Связь с аккаунтом прервана' });
    fireEvent.click(within(refused).getByRole('button', { name: 'Связать снова' }));
    // The lost link of the helper of the account: «Связать снова» brings that helper back (decision 15.11).
    await waitFor(() => expect(started).toHaveBeenCalledWith({ chooseHelper: true }));
  });
});

/** The status of the saga the server answers now: a reading of «Свой ИИ», then of the account. */
function statusOf(reading: Partial<NonNullable<IndexingStatus['reading']>>, waiting?: 'consent' | 'paused'): IndexingStatus {
  return {
    published: { exists: true, context: { state: 'ready', currentBook: null, retryableFailedBook: null } }, steps: [],
    preparation: {
      remainingSeconds: null, letterPromised: false, chaptersRead: 1, chaptersTotal: 4, booksRead: 0, booksTotal: 1,
      currentBookId: 'book-01', askable: true, books: [], ...(waiting ? { waiting } : {}),
    },
    reading: {
      state: 'reading', source: 'account', reason: null, pause: null, chaptersRead: 1, chaptersTotal: 4, namesWait: false, threadsWait: false,
      unread: null, unreadRest: null, spent: 0, approximate: false, stopAt: null, step: null, unit: 'tokens', remainingSeconds: 600,
      limits: { characters: { used: 40_000_000, limit: 40_000_000 }, work: { used: 0, limit: 320_000_000 } }, ...reading,
    },
  } as IndexingStatus;
}

const charactersPause = 'Чтение на паузе: за 30 дней использовано 40 из 40 млн знаков — столько знаков текста даёт ваш тариф. Прочитано 1 из 4 глав. Продолжится само, когда знаки освободятся';

// AW5.1, AW5.5: the one slot gives the block by `reading.source`; the hooks of both run at every render.
describe('the one slot of the line of the reading of the app', () => {
  it('gives the line of the account by the source the server says, never a sum or a sample', async () => {
    const api = await theAccount();
    vi.spyOn(api, 'getIndexingStatus').mockResolvedValue(statusOf({ state: 'paused', pause: 'characters' }, 'paused'));
    const write = vi.spyOn(api, 'setReadingConsent');
    inTheApp(api, <Place api={api} />);
    const line = await theLine('Чтение на паузе');
    await waitFor(() => expect(plain(line.textContent)).toContain(charactersPause));
    expect(await within(line).findByRole('link', { name: 'Тарифы' })).toBeTruthy();
    expect(within(line).queryByRole('button', { name: 'Изменить сумму…' })).toBeNull();
    fireEvent.click(within(line).getByRole('button', { name: 'Повторить' }));
    await waitFor(() => expect(write).toHaveBeenCalledWith(projectId, { state: 'allowed' }));
  });

  it('says nothing of its own before the status is read: the open words stand', async () => {
    const api = await theAccount();
    vi.spyOn(api, 'getIndexingStatus').mockImplementation(() => new Promise(() => undefined));
    inTheApp(api, <Place api={api} />);
    const place = await theLine('Место строки');
    expect(place.textContent).toBe('');
  });

  it('changes the block on an open page: the window of the other source closes, and the block of the account asks no sample', async () => {
    const api = await theAccount();
    let status = statusOf({ state: 'waiting_consent', source: 'own', unit: 'RUB', limits: undefined }, 'consent');
    vi.spyOn(api, 'getIndexingStatus').mockImplementation(async () => status);
    vi.spyOn(api, 'getReadingEstimate').mockResolvedValue({
      chapters: 3, inputTokens: 90_000, outputTokens: 30_000, seconds: 240, amount: 4_000_000, unit: 'RUB', layerShare: 0.2,
      host: 'api.deepseek.com', local: false, kind: 'rough', assumptions: [], suggestedStop: 100_000_000, sampleChapters: 3,
      chapterEdit: { inputTokens: 1, outputTokens: 1, amount: 1 }, namesWait: false, threadsWait: false,
    });
    const sample = vi.spyOn(api, 'getReadingSample');
    const queries = inTheApp(api, <Place api={api} />);
    fireEvent.click(within(await theLine('Помощник готов прочитать книги')).getByRole('button', { name: 'Прочитать книги…' }));
    expect(await screen.findByRole('dialog', { name: 'Прочитать книги своим ИИ?' })).toBeTruthy();
    await waitFor(() => expect(sample).toHaveBeenCalled());
    const asked = sample.mock.calls.length;
    status = statusOf({ state: 'waiting_consent', chaptersRead: 1 }, 'consent');
    await act(async () => {
      await queries.refetchQueries({ queryKey: manuscriptQueryKeys.indexingStatus(projectId) });
    });
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Прочитать книги своим ИИ?' })).toBeNull());
    expect(plain((await theLine('Помощник готов прочитать книги')).textContent)).toContain('Помощник готов прочитать книги: 3 главы');
    fireEvent.click(within(await theLine('Помощник готов прочитать книги')).getByRole('button', { name: 'Прочитать книги…' }));
    expect(await screen.findByRole('dialog', { name: 'Прочитать книги с помощником CanonKeeper?' })).toBeTruthy();
    expect(sample.mock.calls.length).toBe(asked);
    // Back to «Свой ИИ»: the window of the account closes, and the window of «Свой ИИ» closed before does not come back.
    status = statusOf({ state: 'waiting_consent', source: 'own', unit: 'RUB', limits: undefined }, 'consent');
    await act(async () => {
      await queries.refetchQueries({ queryKey: manuscriptQueryKeys.indexingStatus(projectId) });
    });
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Прочитать книги с помощником CanonKeeper?' })).toBeNull());
    expect(screen.queryByRole('dialog', { name: 'Прочитать книги своим ИИ?' })).toBeNull();
    // And to the account again: its window closed before does not come back either.
    status = statusOf({ state: 'waiting_consent', chaptersRead: 1 }, 'consent');
    await act(async () => {
      await queries.refetchQueries({ queryKey: manuscriptQueryKeys.indexingStatus(projectId) });
    });
    await waitFor(() => expect(plain((screen.getByRole('region', { name: 'Помощник готов прочитать книги' })).textContent)).toContain('Помощник готов прочитать книги: 3 главы'));
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  // AW5.1, AW5.4: the block of «Свой ИИ» keeps its words through the very slot the edition puts on its pages — what did not come
  // together, the reading going, the pause of the author and a provider that does not answer.
  describe('the block of «Свой ИИ» through the slot of the edition', () => {
    /** The mock app with «Свой ИИ» (no account), the status of the server a reading of «Свой ИИ». */
    function theOwn(reading: Partial<NonNullable<IndexingStatus['reading']>>, waiting?: 'paused') {
      const api = createMockApiClient();
      api.setScenario({ preset: 'normal', actorRole: 'owner' });
      vi.spyOn(api, 'getIndexingStatus').mockResolvedValue(statusOf({ source: 'own', unit: 'RUB', limits: undefined, ...reading }, waiting));
      return api;
    }

    it('what did not come together: the reason of «Свой ИИ» with «Открыть настройки помощника» and «Повторить»', async () => {
      const api = theOwn({ state: 'done', chaptersRead: 4, unreadRest: { names: 'refused', threads: null } });
      const navigate = vi.fn();
      inTheApp(api, <Place api={api} navigate={navigate} />, navigate);
      const line = await theLine('Имена героев не сведены');
      expect(plain(line.textContent)).toContain('Имена героев не сведены: провайдер не принял запрос — попробуйте другую модель для чтения книг');
      expect(within(line).getAllByRole('button').map((button) => button.textContent)).toEqual(['Открыть настройки помощника', 'Повторить']);
      fireEvent.click(within(line).getByRole('button', { name: 'Открыть настройки помощника' }));
      expect(navigate).toHaveBeenCalledWith('/settings', { part: 'helper' });
    });

    it('going: what was spent, «Изменить сумму…» and «Пауза»', async () => {
      const api = theOwn({ state: 'reading', spent: 240_000_000, stopAt: 1_000_000_000 });
      inTheApp(api, <Place api={api} />);
      const line = await theLine('Помощник читает книги');
      expect(plain(line.textContent)).toContain('Помощник читает книги: 1 из 4 глав · осталось около 10 минут · потрачено 240 из 1 000 ₽ · спросить можно уже сейчас');
      expect(within(line).getAllByRole('button').map((button) => button.textContent)).toEqual(['Изменить сумму…', 'Пауза']);
    });

    it('the pause of the author: «Продолжить»', async () => {
      const api = theOwn({ state: 'paused', pause: 'author' }, 'paused');
      inTheApp(api, <Place api={api} />);
      const line = await theLine('Чтение на паузе');
      expect(plain(line.textContent)).toContain('Чтение на паузе по вашей просьбе: прочитано 1 из 4 глав');
      expect(within(line).getAllByRole('button').map((button) => button.textContent)).toEqual(['Продолжить']);
    });

    it('a provider that does not answer: «Повторить»', async () => {
      const api = theOwn({ state: 'paused', pause: 'no_answer' }, 'paused');
      const write = vi.spyOn(api, 'setReadingConsent');
      inTheApp(api, <Place api={api} />);
      const line = await theLine('Чтение на паузе');
      expect(plain(line.textContent)).toContain('Чтение на паузе: провайдер не отвечает. Прочитано 1 из 4 глав');
      fireEvent.click(within(line).getByRole('button', { name: 'Повторить' }));
      await waitFor(() => expect(write).toHaveBeenCalled());
    });
  });
});

// AW5.3, AW5.4 with specs/2026-11-13-desk-consent-web.md RW1.2, RW1.6: the lines of the account are one on the shelf, in the chat
// and on the empty map; the end of an upload under them says only what came.
describe('the line of the account on the shelf, in the chat and on the map', () => {
  function account(status: IndexingStatus) {
    return theAccount().then((api) => {
      vi.spyOn(api, 'getIndexingStatus').mockResolvedValue(status);
      return api;
    });
  }

  it('on the shelf', async () => {
    const api = await account(statusOf({ state: 'paused', pause: 'characters' }, 'paused'));
    inTheApp(api, <ManuscriptShelfPage api={api} navigate={vi.fn()} route={parseRoute(`http://localhost/manuscript/books?projectId=${projectId}`)} />);
    const reading = await theLine('Чтение саги');
    await waitFor(() => expect(plain(reading.textContent)).toContain(charactersPause));
    expect(await within(reading).findByRole('link', { name: 'Тарифы' })).toBeTruthy();
    expect(within(reading).getByRole('button', { name: 'Повторить' })).toBeTruthy();
  });

  it('in the chat, a note over the field', async () => {
    const api = await account(statusOf({ state: 'waiting_consent', reason: 'provider_changed' }, 'consent'));
    inTheApp(api, <ChatPage api={api} navigate={vi.fn()} route={createAppRoute('/chat', { projectId, new: '1' })} />);
    const chat = await screen.findByRole('region', { name: 'Исследовательский чат' }, { timeout: 5000 });
    const note = await within(chat).findByRole('note', { name: 'Помощник готов прочитать книги' });
    expect(plain(note.textContent)).toContain('Книги теперь читает помощник CanonKeeper. Помощник готов прочитать книги: 3 главы');
    expect(within(note).getByRole('button', { name: 'Прочитать книги…' })).toBeTruthy();
  });

  it('on the empty map, in place of «К книгам»', async () => {
    const api = await account(statusOf({}));
    vi.spyOn(api, 'getSagaMap').mockResolvedValue({ revision: 'r', coverage: { chapters: 3, withCards: 0 }, chapters: [], hero: null, nodesTotal: 0, nodes: [], links: [] });
    inTheApp(api, <SagaMap api={api} canEdit navigate={vi.fn()} projectId={projectId} route={createAppRoute('/saga', { projectId })} sourceView="draft" />);
    await waitFor(() => expect(plain(document.body.textContent)).toContain(
      'Помощник читает книги: 1 из 4 глав · осталось около 10 минут · на тарифе за 30 дней использовано 40 из 40 млн знаков · спросить можно уже сейчас',
    ));
    expect(screen.getByRole('button', { name: 'Пауза' })).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'К книгам' })).toBeNull();
  });

  it('the end of an upload under the line of the account says only what came', async () => {
    const at = new Date().toISOString();
    const api = await account(statusOf({ state: 'paused', pause: 'characters' }, 'paused'));
    vi.spyOn(api, 'listImportQueues').mockResolvedValue({ data: [{
      id: 'queue-end', projectId, status: 'succeeded', version: 6, intendedItemCount: 1, currentItemId: null,
      createdAt: at, updatedAt: at, startedAt: at, completedAt: at, links: { self: '/queue-end', cancel: null },
      items: [{
        id: 'queue-end-item', clientItemId: 'client-end', position: 1, sourceFileName: 'Северный суд.fb2', mimeType: null, sizeBytes: 4,
        preflightStatus: 'ready', preflightError: null, processingStatus: 'succeeded', failureClass: null, failedStep: null,
        importJobId: 'job-end', bookId: 'book-03', updatedAt: at, links: { status: null },
      }],
    } as never] });
    inTheApp(api, <ManuscriptShelfPage api={api} navigate={vi.fn()} route={parseRoute(`http://localhost/manuscript/books?projectId=${projectId}`)} />);
    const end = await theLine('Загрузка книг');
    const reading = await theLine('Чтение саги');
    await waitFor(() => expect(plain(reading.textContent)).toContain(charactersPause));
    expect(end.textContent).toMatch(/^Книга загружена: \d+ глав/u);
    expect(end.textContent).not.toContain('можно');
    const seen = document.body.cloneNode(true) as HTMLElement;
    seen.querySelectorAll('[aria-live]').forEach((node) => node.remove());
    expect(plain(seen.textContent).split(charactersPause)).toHaveLength(2);
  });
});
