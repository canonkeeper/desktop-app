import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { createEvent, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../../api/mock';
import { projectId } from '../../../api/mock/repository/fixtures';
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

const ready = { exists: true as const, context: { state: 'ready' as const, currentBook: null, retryableFailedBook: null } };

function renderShelf(configure: (api: ShelfApi) => void = (api) => {
  vi.spyOn(api, 'getIndexingStatus').mockResolvedValue({ published: ready, steps: [] });
}) {
  const api = createMockApiClient();
  api.setScenario({ actorRole: 'editor', preset: 'normal' });
  vi.spyOn(api, 'listChatSessions').mockResolvedValue({ data: [], meta: { hasMore: false, nextCursor: null } } as never);
  configure(api);
  const update = vi.spyOn(api, 'updateBook');
  const queryClient = new QueryClient({ defaultOptions: { mutations: { retry: false }, queries: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}><ToastsProvider>
      <ManuscriptShelfPage api={api} navigate={vi.fn()} route={parseRoute(`http://localhost/manuscript/books?projectId=${projectId}`)} />
    </ToastsProvider></QueryClientProvider>,
  );
  return { api, update };
}

const titles = () => [...document.querySelectorAll('article[data-book-id]')].map((card) => card.getAttribute('aria-label'));

// specs/2026-11-11-ux7-reorder.md RK2: the author puts the volumes in his order.
describe('a move of a volume on the shelf', () => {
  // RK2.1: «⋯» → «Переставить…» → «В начало» — updateBook with the place; «Отменить» puts it back by the same operation.
  it('moves a book by the window of its place, and «Отменить» puts it back', async () => {
    const { update } = renderShelf();
    const shelf = ['Книга I. Туманный берег', 'Книга II. Карта приливов', 'Книга III. Северный суд', 'Книга IV. Последний свет'];
    await waitFor(() => expect(titles()).toEqual(shelf));
    const second = screen.getByRole('article', { name: 'Книга II. Карта приливов' });
    const secondId = second.getAttribute('data-book-id')!;
    const firstId = screen.getByRole('article', { name: 'Книга I. Туманный берег' }).getAttribute('data-book-id')!;
    fireEvent.click(within(second).getByRole('button', { name: 'Действия книги Книга II. Карта приливов' }));
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Переставить…' }));

    const window = await screen.findByRole('dialog', { name: 'Переставить «Книга II. Карта приливов»' });
    expect(within(window).getByText('Сейчас это книга 2. Куда её поставить?')).toBeTruthy();
    const here = within(window).getByRole('radio', { name: 'После «Книга I. Туманный берег» — сейчас здесь' });
    expect((here as HTMLInputElement).disabled).toBe(true);
    const move = within(window).getByRole('button', { name: /Переставить/u });
    fireEvent.click(move);
    expect(update).not.toHaveBeenCalled();
    expect(await screen.findByText('Выберите, куда поставить книгу.')).toBeTruthy();

    fireEvent.click(within(window).getByRole('radio', { name: 'В начало' }));
    fireEvent.click(move);
    await waitFor(() => expect(update).toHaveBeenCalledWith(secondId, { afterBookId: null }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: /Переставить/u })).toBeNull());
    await waitFor(() => expect(titles()).toEqual(['Книга II. Карта приливов', 'Книга I. Туманный берег', ...shelf.slice(2)]));
    expect(await screen.findByText('Книга переставлена')).toBeTruthy();
    expect(screen.getByText('«Книга II. Карта приливов» — теперь книга 1')).toBeTruthy();

    fireEvent.click(screen.getByRole('button', { name: 'Отменить' }));
    await waitFor(() => expect(update).toHaveBeenLastCalledWith(secondId, { afterBookId: firstId }));
    await waitFor(() => expect(titles()).toEqual(shelf));
    expect(await screen.findByText('Порядок возвращён')).toBeTruthy();
  });

  // RK2.1: a card taken by the mouse and let go over the second half of another lands after it.
  it('moves a book by a drag of its card', async () => {
    const { update } = renderShelf();
    await waitFor(() => expect(titles()).toHaveLength(4));
    const first = screen.getByRole('article', { name: 'Книга I. Туманный берег' });
    const second = screen.getByRole('article', { name: 'Книга II. Карта приливов' });
    vi.spyOn(second, 'getBoundingClientRect').mockReturnValue({ left: 0, top: 0, width: 200, height: 260, right: 200, bottom: 260 } as DOMRect);
    const data = new Map<string, string>();
    const dataTransfer = { setData: (type: string, value: string) => data.set(type, value), getData: (type: string) => data.get(type) ?? '', effectAllowed: '', dropEffect: '' };
    expect(first.getAttribute('draggable')).toBe('true');
    fireEvent.dragStart(first, { dataTransfer });
    const over = createEvent.dragOver(second, { dataTransfer });
    Object.defineProperty(over, 'clientX', { value: 150 });
    fireEvent(second, over);
    await waitFor(() => expect(second.getAttribute('data-drop')).toBe('after'));
    expect(first.hasAttribute('data-dragging')).toBe(true);
    const drop = createEvent.drop(second, { dataTransfer });
    Object.defineProperty(drop, 'clientX', { value: 150 });
    fireEvent(second, drop);
    await waitFor(() => expect(update).toHaveBeenCalledWith(first.getAttribute('data-book-id'), { afterBookId: second.getAttribute('data-book-id') }));
    expect(await screen.findByText('Книга переставлена')).toBeTruthy();
  });

  // RK2.2: while the helper reads the books, «Переставить…» stays dimmed with its reason, a taken card says it and stays.
  it('stands the volumes still while the books are read and says why', async () => {
    const { update } = renderShelf(() => undefined);
    await screen.findByText(/^Помощник читает книги/u);
    const second = screen.getByRole('article', { name: 'Книга II. Карта приливов' });
    fireEvent.click(within(second).getByRole('button', { name: 'Действия книги Книга II. Карта приливов' }));
    const item = await screen.findByRole('menuitem', { name: /Переставить…/u });
    expect(item.getAttribute('aria-disabled')).toBe('true');
    expect(item.textContent).toContain('Пока помощник читает книги, переставить нельзя.');
    fireEvent.click(item);
    expect(screen.queryByRole('dialog', { name: /Переставить/u })).toBeNull();

    fireEvent.keyDown(screen.getByRole('menu'), { key: 'Escape' });
    fireEvent.dragStart(second, { dataTransfer: { setData: vi.fn(), effectAllowed: '' } });
    expect(await screen.findByText('Пока помощник читает книги, переставить нельзя.')).toBeTruthy();
    expect(second.hasAttribute('data-dragging')).toBe(false);
    expect(update).not.toHaveBeenCalled();
  });

  // specs/2026-11-12-desk-window.md WN3.2: a reading that waits for the helper reads no book — the text is indexed and
  // nobody reads it — so the volumes move.
  it('moves the volumes while the reading waits for the helper', async () => {
    const { update } = renderShelf((api) => {
      vi.spyOn(api, 'getIndexingStatus').mockResolvedValue({
        published: ready, steps: [],
        preparation: {
          remainingSeconds: null, letterPromised: false, chaptersRead: 0, chaptersTotal: 4, booksRead: 0, booksTotal: 4,
          currentBookId: 'book-01', askable: true, books: [], waiting: 'helper',
        },
      });
    });
    const line = await screen.findByRole('region', { name: 'Чтение саги' });
    await waitFor(() => expect(line.textContent).toMatch(/^Помощник ещё не подключён\. Читать книги и искать по ним можно уже сейчас/u));
    const second = screen.getByRole('article', { name: 'Книга II. Карта приливов' });
    fireEvent.click(within(second).getByRole('button', { name: 'Действия книги Книга II. Карта приливов' }));
    const item = await screen.findByRole('menuitem', { name: 'Переставить…' });
    expect(item.getAttribute('aria-disabled')).not.toBe('true');
    fireEvent.click(item);
    const window = await screen.findByRole('dialog', { name: 'Переставить «Книга II. Карта приливов»' });
    fireEvent.click(within(window).getByRole('radio', { name: 'В начало' }));
    fireEvent.click(within(window).getByRole('button', { name: /Переставить/u }));
    await waitFor(() => expect(update).toHaveBeenCalledWith(second.getAttribute('data-book-id'), { afterBookId: null }));
  });

  // RK1.6: the first book goes to «Недавно удалённые» — the one after it is «Книга 1» on its card and in the window of the
  // move, though its stored order keeps the gap the deleted one comes back to.
  it('numbers the books by their places without gaps after a deletion', async () => {
    renderShelf(async (api) => {
      vi.spyOn(api, 'getIndexingStatus').mockResolvedValue({ published: ready, steps: [] });
      await api.deleteBook('book-01');
    });
    const second = await screen.findByRole('article', { name: 'Книга II. Карта приливов' });
    await waitFor(() => expect(second.textContent).toContain('Книга 1 · '));
    expect(screen.queryByRole('article', { name: 'Книга I. Туманный берег' })).toBeNull();
    fireEvent.click(within(second).getByRole('button', { name: 'Действия книги Книга II. Карта приливов' }));
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Переставить…' }));
    const window = await screen.findByRole('dialog', { name: 'Переставить «Книга II. Карта приливов»' });
    expect(within(window).getByText('Сейчас это книга 1. Куда её поставить?')).toBeTruthy();
  });
});
