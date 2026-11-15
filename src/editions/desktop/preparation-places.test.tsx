// SPDX-License-Identifier: AGPL-3.0-only
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { useCallback, useState, type ReactNode } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../api/mock';
import { projectId } from '../../api/mock/repository/fixtures';
import { InShell } from '../../app/shell/AppShell.test-helpers';
import { manuscriptQueryKeys, type IndexingStatus } from '../../entities/manuscript/api';
import { PreparationViewProvider } from '../../entities/manuscript/preparation-slot';
import { ChatPage } from '../../features/chat/ChatPage';
import { ManuscriptShelfPage } from '../../features/manuscript/shelf/ManuscriptShelfPage';
import { SagaMap } from '../../features/saga/SagaMap';
import type { CanonKeeperApiClient } from '../../shared/api';
import { createAppRoute, parseRoute, type RouteNavigate } from '../../shared/navigation/app-route';
import { FakeWebSocket } from '../../test/FakeWebSocket';
import { ToastsProvider } from '../../ui/feedback/Toast';
import { AccountLinkProvider } from './account/AccountLink';
import { ReadingClientProvider } from './reading/reading-api';
import { useReadingView } from './reading/ReadingView';
import { HelperPart } from './settings/helper/HelperPart';

vi.mock('@tanstack/react-router', async (actual) => ({ ...await actual<object>(), useBlocker: vi.fn(() => ({ status: 'idle' })) }));
// WebGL is not in jsdom: the canvas of the map is not what is checked here.
vi.mock('../../features/saga/MapCanvas', () => ({ MapCanvas: () => <div data-testid="saga-map-canvas" /> }));

type Api = ReturnType<typeof createMockApiClient>;

beforeEach(() => {
  vi.stubGlobal('WebSocket', FakeWebSocket);
  vi.stubGlobal('innerWidth', 1440);
  vi.stubGlobal('innerHeight', 900);
  vi.stubGlobal('matchMedia', (query: string) => ({ addEventListener: () => undefined, matches: false, media: query, removeEventListener: () => undefined }));
});

afterEach(() => {
  cleanup();
  FakeWebSocket.reset();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

const line = 'Помощник ещё не подключён. Читать книги и искать по ним можно уже сейчас';

const waitingHelper: IndexingStatus = {
  published: { exists: true, context: { state: 'ready', currentBook: null, retryableFailedBook: null } }, steps: [],
  preparation: {
    remainingSeconds: null, letterPromised: false, chaptersRead: 0, chaptersTotal: 4, booksRead: 0, booksTotal: 1,
    currentBookId: 'book-01', askable: true, books: [], waiting: 'helper',
  },
} as IndexingStatus;

/** The pages of the app with its slot, the link with an account and the client of its reading, or of the site without them. */
function edition(app: boolean, children: ReactNode, api?: Api) {
  if (!app) return children;
  // The link of the edition says its end in the one area of the toasts of the root, as App puts it over the pages.
  return (
    <ToastsProvider>
      <AccountLinkProvider api={(api ?? null) as never}>
        <ReadingClientProvider value={(api ?? null) as never}>
          <PreparationViewProvider value={useReadingView}>{children}</PreparationViewProvider>
        </ReadingClientProvider>
      </AccountLinkProvider>
    </ToastsProvider>
  );
}

/** «Подключить помощника» opens the window of the choice (specs/2026-11-15-desk-account-link-web.md AW3.3); «Настроить». */
function setUpOwn() {
  const window = screen.getByRole('dialog', { name: 'Подключить помощника' });
  fireEvent.click(within(within(window).getByRole('region', { name: 'Свой ИИ' })).getByRole('button', { name: 'Настроить' }));
}

function client() {
  return new QueryClient({ defaultOptions: { mutations: { retry: false }, queries: { retry: false } } });
}

function ChatWithAddress({ api }: { api: Api }) {
  const [route, setRoute] = useState(() => createAppRoute('/chat', { projectId, new: '1' }));
  const navigate = useCallback<RouteNavigate>((path, query = {}) => setRoute(createAppRoute(path, query)), []);
  return (
    <InShell api={api} navigate={navigate} route={route}>
      {route.path === '/chat' ? <ChatPage api={api} navigate={navigate} route={route} /> : <p>{`${route.path}?part=${route.query.get('part') ?? ''}`}</p>}
    </InShell>
  );
}

// specs/2026-11-13-desk-helper-web.md HW3.1, HW3.2 (board 44·3): the line of a reading that waits for the helper.
describe('the line of the reading leads to the helper in the app', () => {
  it.each([[true], [false]])('on the shelf (the app: %s)', async (app) => {
    const api = createMockApiClient();
    api.setScenario({ actorRole: 'editor', preset: 'normal' });
    vi.spyOn(api, 'getIndexingStatus').mockResolvedValue(waitingHelper);
    const linkRead = vi.spyOn(api, 'getAccount');
    const navigate = vi.fn();
    render(
      <QueryClientProvider client={client()}><ToastsProvider>
        {edition(app, <ManuscriptShelfPage api={api} navigate={navigate} route={parseRoute(`http://localhost/manuscript/books?projectId=${projectId}`)} />, api)}
      </ToastsProvider></QueryClientProvider>,
    );
    const reading = await screen.findByRole('region', { name: 'Чтение саги' });
    await waitFor(() => expect(within(reading).getAllByText(line).length).toBeGreaterThan(0));
    if (!app) {
      // The site: the line as it was, nothing to press; the link with an account of the app is not asked.
      expect(within(reading).queryByRole('button')).toBeNull();
      expect(linkRead).not.toHaveBeenCalled();
      return;
    }
    // The app reads the link to say the reason — the spy above is where the site would be caught asking.
    expect(linkRead).toHaveBeenCalled();
    fireEvent.click(within(reading).getByRole('button', { name: 'Подключить помощника' }));
    setUpOwn();
    expect(navigate).toHaveBeenCalledWith('/settings', { part: 'helper' });
  });

  it.each([[true], [false]])('on the empty map (the app: %s)', async (app) => {
    const api = createMockApiClient();
    vi.spyOn(api, 'getSagaMap').mockResolvedValue({ revision: 'r', coverage: { chapters: 3, withCards: 0 }, chapters: [], hero: null, nodesTotal: 0, nodes: [], links: [] });
    vi.spyOn(api, 'getIndexingStatus').mockResolvedValue(waitingHelper);
    const linkRead = vi.spyOn(api, 'getAccount');
    const navigate = vi.fn();
    render(
      <QueryClientProvider client={client()}><ToastsProvider>
        {edition(app, <SagaMap api={api} canEdit navigate={navigate} projectId={projectId} route={createAppRoute('/saga', { projectId })} sourceView="draft" />, api)}
      </ToastsProvider></QueryClientProvider>,
    );
    expect(await screen.findByText(line)).toBeTruthy();
    if (!app) {
      fireEvent.click(screen.getByRole('button', { name: 'К книгам' }));
      expect(navigate).toHaveBeenCalledWith('/manuscript/books', { projectId });
      expect(screen.queryByRole('button', { name: 'Подключить помощника' })).toBeNull();
      expect(linkRead).not.toHaveBeenCalled();
      return;
    }
    // The map waits for the helper, not for the books: its one button leads to him.
    expect(screen.queryByRole('button', { name: 'К книгам' })).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Подключить помощника' }));
    setUpOwn();
    expect(navigate).toHaveBeenCalledWith('/settings', { part: 'helper' });
  });

  it('in the chat, and the field still says why «Спросить» waits', async () => {
    const api = createMockApiClient();
    api.setScenario({ actorRole: 'owner', preset: 'normal', helperMissing: true });
    render(<QueryClientProvider client={client()}>{edition(true, <ChatWithAddress api={api} />, api)}</QueryClientProvider>);
    const chat = await screen.findByRole('region', { name: 'Исследовательский чат' }, { timeout: 5000 });
    const note = await within(chat).findByRole('note', { name: 'Помощник ещё не подключён' });
    expect(within(note).getByText(line)).toBeTruthy();
    expect(within(chat).getByRole('button', { name: 'Спросить' }).getAttribute('aria-disabled')).toBe('true');
    fireEvent.click(within(note).getByRole('button', { name: 'Подключить помощника' }));
    setUpOwn();
    expect(await screen.findByText('/settings?part=helper')).toBeTruthy();
  });

  // specs/2026-11-15-desk-account-link-web.md AW3.4: the site has no slot — the line and the reason at «Спросить» as before,
  // and nothing of the link with an account of the app is asked.
  it('in the chat of the site: the line and the reason as they were, the link of the app not asked', async () => {
    const api = createMockApiClient();
    api.setScenario({ actorRole: 'owner', preset: 'normal', helperMissing: true });
    const linkRead = vi.spyOn(api, 'getAccount');
    const settingsRead = vi.spyOn(api, 'getAiSettings');
    render(<QueryClientProvider client={client()}><ToastsProvider>{edition(false, <ChatWithAddress api={api} />)}</ToastsProvider></QueryClientProvider>);
    const chat = await screen.findByRole('region', { name: 'Исследовательский чат' }, { timeout: 5000 });
    const note = await within(chat).findByRole('note', { name: 'Помощник ещё не подключён' });
    expect(within(note).getByText(line)).toBeTruthy();
    expect(within(note).queryByRole('button')).toBeNull();
    fireEvent.change(within(chat).getByRole('textbox', { name: 'Вопрос' }), { target: { value: 'Кто остановил лампу маяка?' } });
    await waitFor(() => expect(within(chat).getByRole('button', { name: 'Спросить' }).getAttribute('aria-description')).toBe(line));
    expect(linkRead).not.toHaveBeenCalled();
    expect(settingsRead).not.toHaveBeenCalled();
  });

  it('changes in the chat once the helper is connected in «Настройки», without a reload', async () => {
    const api = createMockApiClient();
    api.setScenario({ actorRole: 'owner', preset: 'normal', helperMissing: true });
    // The text of the saga is ready and a reading that waits is no work: nothing reads the status again by itself.
    const read = api.getIndexingStatus.bind(api);
    vi.spyOn(api, 'getIndexingStatus').mockImplementation(async (...input: Parameters<Api['getIndexingStatus']>) => {
      const status = await read(...input);
      return { ...status, steps: [], published: waitingHelper.published, ...(status.draft ? { draft: waitingHelper.published } : {}) };
    });
    render(
      <QueryClientProvider client={client()}>
        {edition(true, <><ChatWithAddress api={api} /><ToastsProvider><HelperPart api={api as unknown as CanonKeeperApiClient} /></ToastsProvider></>, api)}
      </QueryClientProvider>,
    );
    const chat = await screen.findByRole('region', { name: 'Исследовательский чат' }, { timeout: 5000 });
    await within(chat).findByRole('note', { name: 'Помощник ещё не подключён' });
    // The local model with its models, saved on the page.
    fireEvent.click(screen.getByRole('button', { name: /^Провайдер/u }));
    fireEvent.click(within(screen.getByRole('listbox')).getByRole('option', { name: 'Локальная модель (Ollama)' }));
    fireEvent.change(screen.getByLabelText('Модель для чата'), { target: { value: 'gemma4' } });
    fireEvent.change(screen.getByLabelText('Модель для чтения книг'), { target: { value: 'gemma4' } });
    fireEvent.click(screen.getByRole('button', { name: 'Сохранить' }));
    // The status is read again: the reading now waits for the consent — «Прочитать книги…» in place of «Подключить помощника»
    // (specs/2026-11-13-desk-consent-web.md RW1.1) — and the field takes the question.
    const ready = await within(chat).findByRole('note', { name: 'Помощник готов прочитать книги' }, { timeout: 5000 });
    expect(within(ready).getAllByRole('button').map((button) => button.textContent)).toEqual(['Прочитать книги…']);
    fireEvent.change(within(chat).getByRole('textbox', { name: 'Вопрос' }), { target: { value: 'Кто остановил лампу маяка?' } });
    await waitFor(() => expect(within(chat).getByRole('button', { name: 'Спросить' }).getAttribute('aria-disabled')).not.toBe('true'));
    expect(within(chat).queryByRole('note', { name: 'Помощник ещё не подключён' })).toBeNull();
  });
});

/** The status of a saga of the app whose reading by «Свой ИИ» stands in a state of RW1.1. */
function readingStatus(reading: Partial<NonNullable<IndexingStatus['reading']>>, waiting?: 'consent' | 'paused'): IndexingStatus {
  return {
    ...waitingHelper,
    preparation: { ...waitingHelper.preparation!, chaptersRead: 1, chaptersTotal: 4, ...(waiting ? { waiting } : { waiting: undefined }) },
    reading: {
      state: 'reading', reason: null, pause: null, chaptersRead: 1, chaptersTotal: 4, namesWait: false, threadsWait: false, unread: null, unreadRest: null,
      spent: 240_000_000, approximate: true, stopAt: 1_000_000_000, step: 1_000_000_000, unit: 'RUB', remainingSeconds: 600, ...reading,
    },
  } as IndexingStatus;
}

const plain = (text: string | null | undefined) => (text ?? '').replace(/\u00a0/gu, ' ');

// specs/2026-11-13-desk-consent-web.md RW1.2: one line and the same buttons on the shelf, in the chat and on the map — one
// block of the edition in one slot; «Задать вопрос» stays where it is (the shelf).
describe('the line of the reading by «Свой ИИ» is one on the shelf, in the chat and on the map', () => {
  const words = 'Помощник читает книги: 1 из 4 глав · осталось около 10 минут · потрачено ≈ 240 из 1 000 ₽ · спросить можно уже сейчас';

  it('on the shelf: the words, «Изменить сумму…», «Пауза» and the shelf asks', async () => {
    const api = createMockApiClient();
    api.setScenario({ actorRole: 'owner', preset: 'normal' });
    vi.spyOn(api, 'getIndexingStatus').mockResolvedValue(readingStatus({}));
    render(
      <QueryClientProvider client={client()}><ToastsProvider>
        {edition(true, <ManuscriptShelfPage api={api} navigate={vi.fn()} route={parseRoute(`http://localhost/manuscript/books?projectId=${projectId}`)} />, api)}
      </ToastsProvider></QueryClientProvider>,
    );
    const line = await screen.findByRole('region', { name: 'Чтение саги' });
    await waitFor(() => expect(plain(line.textContent)).toContain(words));
    expect(within(line).getAllByRole('button').map((button) => button.textContent)).toEqual(['Изменить сумму…', 'Пауза', 'Задать вопрос']);
  });

  it('in the chat: the same words and buttons over the field', async () => {
    const api = createMockApiClient();
    api.setScenario({ actorRole: 'owner', preset: 'normal' });
    vi.spyOn(api, 'getIndexingStatus').mockResolvedValue(readingStatus({}));
    render(<QueryClientProvider client={client()}>{edition(true, <ChatWithAddress api={api} />, api)}</QueryClientProvider>);
    const chat = await screen.findByRole('region', { name: 'Исследовательский чат' }, { timeout: 5000 });
    const note = await within(chat).findByRole('note', { name: 'Помощник читает книги' });
    expect(plain(note.textContent)).toContain(words);
    expect(within(note).getAllByRole('button').map((button) => button.textContent)).toEqual(['Изменить сумму…', 'Пауза']);
  });

  it('on the empty map: the same words and buttons in place of «К книгам»', async () => {
    const api = createMockApiClient();
    vi.spyOn(api, 'getSagaMap').mockResolvedValue({ revision: 'r', coverage: { chapters: 4, withCards: 0 }, chapters: [], hero: null, nodesTotal: 0, nodes: [], links: [] });
    vi.spyOn(api, 'getIndexingStatus').mockResolvedValue(readingStatus({}));
    render(
      <QueryClientProvider client={client()}><ToastsProvider>
        {edition(true, <SagaMap api={api} canEdit navigate={vi.fn()} projectId={projectId} route={createAppRoute('/saga', { projectId })} sourceView="draft" />, api)}
      </ToastsProvider></QueryClientProvider>,
    );
    await waitFor(() => expect(plain(document.body.textContent)).toContain(words));
    expect(screen.queryByRole('button', { name: 'К книгам' })).toBeNull();
    expect(screen.getByRole('button', { name: 'Изменить сумму…' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Пауза' })).toBeTruthy();
  });

  // Board 44·28 «Не прочитались»: the end of the reading stands as the shelf says it — «Книги прочитаны — можно спрашивать»
  // with its question — and under it a line of its own for the chapters that did not read, with «Повторить».
  it('on the shelf, done but for chapters that did not read: the open line of the end, and under it the chapters with «Повторить»', async () => {
    const api = createMockApiClient();
    api.setScenario({ actorRole: 'owner', preset: 'normal' });
    const ready: IndexingStatus['published'] = { exists: true, context: { state: 'ready', currentBook: null, retryableFailedBook: null } };
    vi.spyOn(api, 'getIndexingStatus').mockResolvedValue({
      ...readingStatus({ state: 'done', chaptersRead: 4, remainingSeconds: null, unread: { chapters: 3, reason: 'cut_answer' } }),
      published: ready, preparation: undefined, sagaReady: { at: new Date().toISOString(), books: 13, chapters: 612 },
    } as IndexingStatus);
    render(
      <QueryClientProvider client={client()}><ToastsProvider>
        {edition(true, <ManuscriptShelfPage api={api} navigate={vi.fn()} route={parseRoute(`http://localhost/manuscript/books?projectId=${projectId}`)} />, api)}
      </ToastsProvider></QueryClientProvider>,
    );
    const end = await screen.findByRole('region', { name: 'Чтение саги' });
    await waitFor(() => expect(plain(end.textContent)).toContain('Книги прочитаны — можно спрашивать'));
    expect(plain(end.textContent)).toContain('13 книг, 612 глав');
    expect(within(end).getAllByRole('button').map((button) => button.textContent)).toEqual([expect.stringMatching(/^Задать (первый )?вопрос$/u)]);
    // The line of the failure is named by its beginning (specs/2026-11-15-desk-reading-rest.md, Data).
    const unread = screen.getByRole('region', { name: '3 главы не прочитались' });
    expect(plain(unread.textContent)).toContain('3 главы не прочитались: ответ помощника оборвался — увеличьте «Предел ответа»');
    expect(within(unread).getAllByRole('button').map((button) => button.textContent)).toEqual(['Открыть настройки помощника', 'Повторить']);
  });

  // specs/2026-11-15-desk-reading-rest.md RR2.5: while the line of the reading of the app says what is brought together, the
  // shelf does not say «Дособираю…» of the gathering of the story lines under it; the site, without the slot, says it as
  // before.
  it.each([[true], [false]])('on the shelf, the story lines gathered: «Дособираю…» only without the line of the reading (the app: %s)', async (app) => {
    const api = createMockApiClient();
    api.setScenario({ actorRole: 'owner', preset: 'normal' });
    vi.spyOn(api, 'getIndexingStatus').mockResolvedValue(readingStatus({
      state: 'reading', chaptersRead: 4, chaptersTotal: 4, namesWait: true, threadsWait: true, remainingSeconds: null,
    }));
    const gathering = {
      id: 'job-saga-layer', kind: 'saga_layer', status: 'running', progress: 0.4, phase: null,
      subject: { type: 'saga_layer', projectId, sourceView: 'published' }, result: null, error: null, canCancel: true,
      expiresAt: null, createdAt: '2026-11-15T08:00:00.000Z', updatedAt: '2026-11-15T08:01:00.000Z',
      links: { self: '/api/v1/jobs/job-saga-layer', cancel: null, timeline: null },
    };
    vi.spyOn(api, 'listProjectJobs').mockResolvedValue({ data: [gathering], meta: { hasMore: false, nextCursor: null } } as never);
    render(
      <QueryClientProvider client={client()}><ToastsProvider>
        {edition(app, <ManuscriptShelfPage api={api} navigate={vi.fn()} route={parseRoute(`http://localhost/manuscript/books?projectId=${projectId}`)} />, api)}
      </ToastsProvider></QueryClientProvider>,
    );
    const quiet = 'Дособираю сюжетные линии — спрашивать и читать это не мешает';
    if (!app) {
      expect(await screen.findByText(quiet)).toBeTruthy();
      return;
    }
    const reading = await screen.findByRole('region', { name: 'Чтение саги' });
    await waitFor(() => expect(plain(reading.textContent)).toContain('Главы прочитаны — помощник сводит имена героев и сюжетные нити'));
    await waitFor(() => expect(api.listProjectJobs).toHaveBeenCalled());
    expect(screen.queryByText(quiet)).toBeNull();
  });

  it('opens the window of the consent from the chat by the same button as on the shelf', async () => {
    const api = createMockApiClient();
    api.setScenario({ actorRole: 'owner', preset: 'normal' });
    vi.spyOn(api, 'getIndexingStatus').mockResolvedValue(readingStatus({ state: 'waiting_consent', spent: 0, stopAt: null, step: null }, 'consent'));
    vi.spyOn(api, 'getReadingEstimate').mockResolvedValue({
      chapters: 3, inputTokens: 90_000, outputTokens: 30_000, seconds: 240, amount: 4_000_000, unit: 'RUB', layerShare: 0.2,
      host: 'api.deepseek.com', local: false, kind: 'rough', assumptions: [], suggestedStop: 100_000_000, sampleChapters: 3,
      chapterEdit: { inputTokens: 1, outputTokens: 1, amount: 1 }, namesWait: false, threadsWait: false,
    });
    render(<QueryClientProvider client={client()}>{edition(true, <ChatWithAddress api={api} />, api)}</QueryClientProvider>);
    const chat = await screen.findByRole('region', { name: 'Исследовательский чат' }, { timeout: 5000 });
    const note = await within(chat).findByRole('note', { name: 'Помощник готов прочитать книги' });
    expect(note.textContent).toContain('Помощник готов прочитать книги: 3 главы');
    fireEvent.click(within(note).getByRole('button', { name: 'Прочитать книги…' }));
    expect(await screen.findByRole('dialog', { name: 'Прочитать книги своим ИИ?' })).toBeTruthy();
  });
});

// specs/2026-11-13-desk-consent-web.md RW1.6: one phrase is not on the screen twice. Under the line of the reading the end of
// an upload says only what came and «Как разобралась книга»; without that line (the saga read) it says the helper read it.
describe('the end of an upload over the line of the reading of the app', () => {
  const at = new Date().toISOString();
  const ended = {
    id: 'queue-end', projectId, status: 'succeeded', version: 6, intendedItemCount: 1, currentItemId: null,
    createdAt: at, updatedAt: at, startedAt: at, completedAt: at, links: { self: '/queue-end', cancel: null },
    items: [{
      id: 'queue-end-item', clientItemId: 'client-end', position: 1, sourceFileName: 'Северный суд.fb2', mimeType: null, sizeBytes: 4,
      preflightStatus: 'ready', preflightError: null, processingStatus: 'succeeded', failureClass: null, failedStep: null,
      importJobId: 'job-end', bookId: 'book-03', updatedAt: at, links: { status: null },
    }],
  } as const;
  const read: IndexingStatus['published'] = { exists: true, context: { state: 'ready', currentBook: null, retryableFailedBook: null } };

  async function shelfWith(status: IndexingStatus, helperChosen = false) {
    const api = createMockApiClient();
    api.setScenario({ actorRole: 'owner', preset: 'normal' });
    // Without a helper chosen a read saga says that the helper is not connected (decision 15.11, account-words-web).
    if (helperChosen) await api.updateAiSettings({ source: 'own', own: { preset: 'deepseek', chatModel: 'deepseek-chat', cardsModel: 'deepseek-chat' } });
    vi.spyOn(api, 'getIndexingStatus').mockResolvedValue(status);
    vi.spyOn(api, 'listImportQueues').mockResolvedValue({ data: [structuredClone(ended) as never] });
    render(
      <QueryClientProvider client={client()}><ToastsProvider>
        {edition(true, <ManuscriptShelfPage api={api} navigate={vi.fn()} route={parseRoute(`http://localhost/manuscript/books?projectId=${projectId}`)} />, api)}
      </ToastsProvider></QueryClientProvider>,
    );
    const end = await screen.findByRole('region', { name: 'Загрузка книг' }, { timeout: 5000 });
    return end;
  }

  it.each([
    ['waits for the helper', waitingHelper, line],
    ['waits for the consent', readingStatus({ state: 'waiting_consent', spent: 0, stopAt: null, step: null }, 'consent'), 'Помощник готов прочитать книги: 3 главы'],
    ['goes', readingStatus({}), 'Помощник читает книги: 1 из 4 глав'],
    ['stands paused', readingStatus({ state: 'paused', pause: 'author' }, 'paused'), 'Чтение на паузе по вашей просьбе: прочитано 1 из 4 глав'],
  ])('the reading %s: its phrase once, the end says what came', async (_state, status, phrase) => {
    const end = await shelfWith(status);
    expect(end.textContent).toMatch(/^Книга загружена: \d+ глав/u);
    expect(within(end).getAllByRole('button').map((button) => button.textContent)).toEqual(['Как разобралась книга']);
    const reading = screen.getByRole('region', { name: 'Чтение саги' });
    await waitFor(() => expect(plain(reading.textContent)).toContain(phrase));
    // What the eye reads: the announcement of a line for the screen reader is not on the screen.
    const seen = document.body.cloneNode(true) as HTMLElement;
    seen.querySelectorAll('[aria-live]').forEach((node) => node.remove());
    expect(plain(seen.textContent).split(phrase)).toHaveLength(2);
    expect(end.textContent).not.toContain('можно');
  });

  // The toast of an end this tab saw is said on the shelf, over the line of the reading: it says only what came too.
  it('the toast of an end this tab saw over the line of the reading says only what came', async () => {
    const api = createMockApiClient();
    api.setScenario({ actorRole: 'owner', preset: 'normal' });
    vi.spyOn(api, 'getIndexingStatus').mockResolvedValue(waitingHelper);
    let finished = false;
    const running = { ...structuredClone(ended), status: 'running', completedAt: null, items: [{ ...structuredClone(ended.items[0]), processingStatus: 'running' }] };
    vi.spyOn(api, 'listImportQueues').mockImplementation(async () => ({ data: [(finished ? structuredClone(ended) : running) as never] }));
    const queries = client();
    render(
      <QueryClientProvider client={queries}><ToastsProvider>
        {edition(true, <ManuscriptShelfPage api={api} navigate={vi.fn()} route={parseRoute(`http://localhost/manuscript/books?projectId=${projectId}`)} />, api)}
      </ToastsProvider></QueryClientProvider>,
    );
    const reading = await screen.findByRole('region', { name: 'Чтение саги' });
    await waitFor(() => expect(plain(reading.textContent)).toContain(line));
    finished = true;
    await act(async () => {
      await queries.refetchQueries({ queryKey: manuscriptQueryKeys.importQueues(projectId), exact: true });
    });
    const toast = await waitFor(() => screen.getAllByRole('status').find((each) => /^Книга загружена: \d+ глав/u.test(each.textContent ?? ''))!, { timeout: 5000 });
    expect(toast.textContent).not.toContain(line);
    const seen = document.body.cloneNode(true) as HTMLElement;
    seen.querySelectorAll('[aria-live]').forEach((node) => node.remove());
    expect(plain(seen.textContent).split(line)).toHaveLength(2);
  });

  it('the saga read, no line of the reading: the end says the helper read it', async () => {
    const end = await shelfWith({ ...waitingHelper, published: read, draft: read, preparation: undefined } as IndexingStatus, true);
    await waitFor(() => expect(end.textContent).toContain('Помощник прочитал её — можно спрашивать'));
    expect(screen.queryByRole('region', { name: 'Чтение саги' })).toBeNull();
  });
});
