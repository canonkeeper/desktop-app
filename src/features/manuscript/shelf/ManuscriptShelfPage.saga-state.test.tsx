import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../../api/mock';
import { bookId, projectId } from '../../../api/mock/repository/fixtures';
import type { ChatSession } from '../../../entities/chat/api';
import type { IndexingStatus, ProjectJob } from '../../../entities/manuscript/api';
import { parseRoute } from '../../../shared/navigation/app-route';
import { FakeWebSocket } from '../../../test/FakeWebSocket';
import { ToastsProvider } from '../../../ui/feedback/Toast';
import { ManuscriptShelfPage } from './ManuscriptShelfPage';

type ShelfApi = ReturnType<typeof createMockApiClient>;

beforeEach(() => {
  vi.stubGlobal('WebSocket', FakeWebSocket);
});

afterEach(() => {
  FakeWebSocket.reset();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

const pastChat = { id: 'chat-old', createdAt: '2026-01-01T00:00:00.000Z' } as ChatSession;

function renderShelf(configure: (api: ShelfApi) => void, chats: () => ChatSession[] = () => [pastChat]) {
  const api = createMockApiClient();
  api.setScenario({ actorRole: 'editor', preset: 'normal' });
  vi.spyOn(api, 'listChatSessions').mockImplementation(async () => ({ data: chats(), meta: { hasMore: false, nextCursor: null } }) as never);
  configure(api);
  const navigate = vi.fn();
  const queryClient = new QueryClient({ defaultOptions: { mutations: { retry: false }, queries: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}><ToastsProvider>
      <ManuscriptShelfPage api={api} navigate={navigate} route={parseRoute(`http://localhost/manuscript/books?projectId=${projectId}`)} />
    </ToastsProvider></QueryClientProvider>,
  );
  return { api, navigate };
}

const ready = { exists: true as const, context: { state: 'ready' as const, currentBook: null, retryableFailedBook: null } };

function sagaLayer(status: ProjectJob['status']): ProjectJob {
  return {
    id: 'job-saga-layer', kind: 'saga_layer', status, progress: 0.4, phase: null,
    subject: { type: 'saga_layer', projectId, sourceView: 'published' }, result: null, error: null, canCancel: true,
    expiresAt: null, createdAt: '2026-08-12T18:00:00.000Z', updatedAt: '2026-08-12T18:01:00.000Z',
    links: { self: '/api/v1/jobs/job-saga-layer', cancel: null, timeline: null },
  } as ProjectJob;
}

// specs/2026-11-10-ux3-saga-state.md UC2 on the page of the shelf.
describe('the one line of the state of the saga on the shelf', () => {
  // SS2.1, SS2.2, SS2.6: one block over the books, the word of each book, nothing of the stages and no stop.
  it('says how far the helper reads, with the word of each book and no stages, blocks of work or stops', async () => {
    renderShelf(() => undefined);

    const line = await screen.findByRole('region', { name: 'Чтение саги' });
    expect(line.textContent).toMatch(/^Помощник читает книги: книга 2 из 2 · «Книга II\. Карта приливов»: 0 из 1 главы · обычно 10–30 минут · спросить можно уже сейчас/u);
    expect(within(line).getByText('Страницу можно закрыть — чтение продолжится')).toBeTruthy();
    expect(screen.getAllByRole('region', { name: 'Чтение саги' })).toHaveLength(1);
    const current = await screen.findByRole('article', { name: 'Книга II. Карта приливов' });
    expect(within(current).getByText('Читается · 0 из 1 главы')).toBeTruthy();
    // specs/2026-11-11-ux7-shelf.md SF2.1: a read book says so too — no book is without its word.
    await waitFor(() => expect(within(screen.getByRole('article', { name: 'Книга I. Туманный берег' })).getByText('Готова')).toBeTruthy());

    const page = screen.getByRole('region', { name: 'Книги саги' });
    expect(page.textContent).not.toMatch(/Показать этапы|этап \d|Обработка саги|карточк|очеред|импорт|обработк|поиск по тексту|нити и арки|Готовим «/iu);
    expect(screen.queryByRole('button', { name: /Остановить|Скрыть подготовку/u })).toBeNull();
    expect(screen.getAllByRole('button', { name: /^Задать (первый )?вопрос$/u })).toHaveLength(1);
  });

  // SS2.7: the line reaches its end without a reload — the status is read again while the reading goes on.
  it('reaches «прочитана» by itself and asks the question of a saga with chats', async () => {
    let done = false;
    const { navigate } = renderShelf((api) => {
      const read = api.getIndexingStatus.bind(api);
      vi.spyOn(api, 'getIndexingStatus').mockImplementation(async (id, options) => {
        const status = await read(id, options);
        if (!done || options?.bookId) return status;
        const { preparation: _reading, ...rest } = status;
        return { ...rest, published: ready, sagaReady: { at: '2026-11-11T10:00:00.000Z', books: 2, chapters: 2 } } satisfies IndexingStatus;
      });
    });

    await screen.findByText(/^Помощник читает книги/u);
    done = true;
    const line = await screen.findByRole('region', { name: 'Чтение саги' });
    await waitFor(() => expect(line.textContent).toMatch(/^Книги прочитаны — можно спрашивать/u), { timeout: 6_000 });
    expect(within(line).getByText('2 книги, 2 главы')).toBeTruthy();
    expect(screen.queryByText(/Читается/u)).toBeNull();
    fireEvent.click(within(line).getByRole('button', { name: 'Задать вопрос' }));
    expect(navigate).toHaveBeenCalledWith('/chat', { projectId });
  });

  // specs/2026-11-11-ux9-shelf-chat.md SC1.2 (W-82, П-22): a reader of the saga asks nothing — his main button reads.
  it('gives a reader of the saga «Читать» as its main button, not a question', async () => {
    const { navigate } = renderShelf((api) => {
      api.setScenario({ actorRole: 'viewer', preset: 'normal' });
      vi.spyOn(api, 'getIndexingStatus').mockResolvedValue({
        published: ready, steps: [], sagaReady: { at: '2026-11-11T10:00:00.000Z', books: 2, chapters: 2 },
      });
    }, () => []);

    const line = await screen.findByRole('region', { name: 'Чтение саги' });
    expect(within(line).queryByRole('button', { name: /Задать/u })).toBeNull();
    fireEvent.click(within(line).getByRole('button', { name: 'Читать' }));
    expect(navigate).toHaveBeenCalledWith('/manuscript/books', expect.objectContaining({ projectId, mode: 'read' }));
  });

  // SS2.1: the end stays until a question is asked after it — a chat started after the end takes the line away.
  it.each([
    ['before', [pastChat], true],
    ['after', [pastChat, { id: 'chat-new', createdAt: '2026-11-11T10:05:00.000Z' } as ChatSession], false],
  ])('says the end to a saga whose last chat started %s it only until then', async (_when, chats, told) => {
    renderShelf((api) => {
      vi.spyOn(api, 'getIndexingStatus').mockResolvedValue({
        published: ready, steps: [], sagaReady: { at: '2026-11-11T10:00:00.000Z', books: 1, chapters: 3 },
      });
    }, () => chats);

    await screen.findByRole('article', { name: 'Книга II. Карта приливов' });
    if (told) {
      const line = await screen.findByRole('region', { name: 'Чтение саги' });
      await waitFor(() => expect(line.textContent).toMatch(/^Книга прочитана — можно спрашивать1 книга, 3 главы/u));
    } else {
      await waitFor(() => expect(screen.queryByRole('region', { name: 'Чтение саги' })).toBeNull());
    }
  });

  // SS2.1, SS2.3; specs/2026-11-11-ux7-shelf.md SF2.1, SF2.4: a stopped reading names its book and goes on with «Дочитать» —
  // the same word in the line and on the book — the code for support beside it.
  it('names the book the reading stopped on and goes on with «Дочитать» from the line and from the book', async () => {
    const { api } = renderShelf((client) => {
      vi.spyOn(client, 'getIndexingStatus').mockImplementation(async (_id, options) => ({
        published: { exists: true, context: { state: 'partial', currentBook: null, retryableFailedBook: { bookId } } },
        steps: options?.bookId ? [
          { kind: 'text_index', status: 'succeeded', progress: 1, counters: { indexedChapters: 14, totalChapters: 14 } },
          { kind: 'cards', status: 'failed', progress: 0.36, jobId: 'job-cards-failed', counters: { indexedChapters: 5, totalChapters: 14 } },
        ] : [],
      }));
      vi.spyOn(client, 'startProjectIndexing');
    });

    const line = await screen.findByRole('region', { name: 'Чтение саги' });
    await waitFor(() => expect(line.textContent).toMatch(/^Не дочитал «Книга II\. Карта приливов»: 5 из 14 глав\. Чтение прервалось на нашей стороне\./u));
    expect(within(line).getByText('job-cards-failed')).toBeTruthy();
    const stopped = screen.getByRole('article', { name: 'Книга II. Карта приливов' });
    expect(within(stopped).getByText('Прочитана не полностью')).toBeTruthy();
    fireEvent.click(within(line).getByRole('button', { name: 'Дочитать' }));
    await waitFor(() => expect(api.startProjectIndexing).toHaveBeenCalledWith(projectId, { sourceView: 'published' }, expect.anything()));
    vi.mocked(api.startProjectIndexing).mockRejectedValueOnce(new Error('Сервер занят.'));
    fireEvent.click(within(stopped).getByRole('button', { name: 'Дочитать' }));
    await waitFor(() => expect(api.startProjectIndexing).toHaveBeenCalledTimes(2));
    // A refusal of «Дочитать» of the book is a line of the area of the toasts, with its reason.
    expect(await screen.findByText('Не удалось дочитать')).toBeTruthy();
  });

  // SS2.4: the story lines gathered after the reading are a quiet line under the block, without buttons.
  it('says quietly that the story lines are gathered, without a button', async () => {
    renderShelf((api) => {
      vi.spyOn(api, 'getIndexingStatus').mockResolvedValue({ published: ready, steps: [] });
      vi.spyOn(api, 'listProjectJobs').mockResolvedValue({ data: [sagaLayer('running')], meta: { hasMore: false, nextCursor: null } } as never);
    });

    const quiet = await screen.findByText('Дособираю сюжетные линии — спрашивать и читать это не мешает');
    expect(quiet.parentElement?.querySelector('button')).toBeFalsy();
    expect(screen.queryByRole('region', { name: 'Чтение саги' })).toBeNull();
    expect(screen.queryByRole('button', { name: /Остановить/u })).toBeNull();
  });
});
