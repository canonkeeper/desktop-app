import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { useState, type ReactNode } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../api/mock';
import { ApiStatusError } from '../../shared/api';
import { ToastsProvider } from '../../ui/feedback/Toast';
import { useToasts, type ToastInput } from '../../ui/feedback/useToastQueue';
import { DrawingReturnProvider, type DrawingReturn } from './drawing-return';
import { RecentlyDeleted } from './RecentlyDeleted';

type Api = ReturnType<typeof createMockApiClient>;

function renderWindow(api: Api, options: {
  drawings?: DrawingReturn | null; notify?: (toast: ToastInput) => number; onOpen?: () => void; onRevertBook?: () => void;
} = {}) {
  const queryClient = new QueryClient({ defaultOptions: { mutations: { retry: false }, queries: { retry: false } } });
  const notify = options.notify ?? vi.fn(() => 1);
  const drawings = options.drawings === undefined
    ? { restore: async (projectId: string, drawingId: string) => { await api.restoreDrawing(projectId, drawingId); } }
    : options.drawings;
  const wrap = (children: ReactNode) => drawings ? <DrawingReturnProvider value={drawings}>{children}</DrawingReturnProvider> : children;
  render(
    <QueryClientProvider client={queryClient}>
      {wrap(<RecentlyDeleted api={api} notify={notify} onClose={vi.fn()} onOpen={options.onOpen ?? vi.fn()}
        onRevertBook={options.onRevertBook ?? vi.fn()} revertAsking={null} />)}
    </QueryClientProvider>,
  );
  return { notify };
}

/** A saga of another name deleted, a book, chapters of two books and a drawing of the saga of the mock deleted. */
async function deleteEverything(api: Api) {
  const other = await api.createProject({ title: 'Морская' });
  await api.deleteProject(other.id);
  const [first, second, third] = (await api.listBooks('project-white-port')).data;
  if (!first || !second || !third) throw new Error('The mock saga needs three books.');
  for (const book of [first, second]) {
    const [chapter] = (await api.listChapters(book.id)).data;
    await api.deleteChapter(chapter!.id);
  }
  await api.deleteBook(third.id);
  const [drawing] = (await api.listDrawings('project-white-port')).data;
  await api.deleteDrawing('project-white-port', drawing!.id);
  return { books: [first, second, third], drawing: drawing! };
}

/** The first book of the saga of the mock updated from a file: its update waits 7 days in «Недавно удалённые». */
async function updateFirstBook(api: Api) {
  const [book] = (await api.listBooks('project-white-port')).data;
  const started = await api.importBookFile('project-white-port', {
    file: new File(['<FictionBook>том</FictionBook>'], 'tom.fb2', { type: 'application/x-fictionbook+xml' }),
    options: { importMode: 'update_book', bookId: book!.id },
  }, { idempotencyKey: 'idem-update-first-book' });
  await api.getJob(started.job.id);
  return book!;
}

function quotaRefusalError() {
  return new ApiStatusError(409, {
    code: 'quota.project_books', detail: 'over', requestId: 'request_test', status: 409, title: 'Conflict', type: 'about:blank',
  }, new Response(null, { status: 409 }));
}

describe('RecentlyDeleted', () => {
  // The mock deletes at its own moment (2026-06-16T05:00Z); the window reads the terms three hours later.
  beforeEach(() => {
    vi.useFakeTimers({ toFake: ['Date'] });
    vi.setSystemTime(new Date('2026-06-16T08:00:00.000Z'));
  });
  afterEach(() => vi.useRealTimers());

  // specs/2026-11-11-ux7-trash.md TB1.1: one list over every saga — the groups, what each row is and where it was, the term.
  it('lists a deleted saga, a book, chapters of two books and a drawing with their places and terms', async () => {
    const api = createMockApiClient();
    const { books } = await deleteEverything(api);
    renderWindow(api);

    for (const heading of ['Саги', 'Книги', 'Главы', 'Рисунки']) {
      expect(await screen.findByRole('heading', { name: heading })).toBeTruthy();
    }
    const sagas = screen.getByRole('region', { name: 'Саги' });
    expect(within(sagas).getByText('Сага «Морская»')).toBeTruthy();
    const book = within(screen.getByRole('region', { name: 'Книги' })).getByText(`Книга «${books[2]!.title}»`);
    expect(book.parentElement!.textContent).toMatch(/Сага «.+» · удалена сегодня в \d\d:\d\d · исчезнет через 7 дней/u);
    const chapters = within(screen.getByRole('region', { name: 'Главы' })).getAllByRole('listitem');
    // The books of the chapters are named by their places among the living books (specs/2026-11-11-ux7-reorder.md RK1.6).
    expect(chapters.map((row) => row.textContent?.match(/Книга (\S+) «/u)?.[1]).sort()).toEqual(['1', '2']);
    expect(within(screen.getByRole('region', { name: 'Рисунки' })).getByText(/^Рисунок «/u)).toBeTruthy();
    expect(document.body.textContent).not.toMatch(/восстанов|источник|материалы автора/iu);
  });

  // TB1.3: «Вернуть» answers with the line of the root and «Открыть»; the row leaves the list.
  it('returns a drawing with the line «… возвращён · Открыть»', async () => {
    const api = createMockApiClient();
    const { drawing } = await deleteEverything(api);
    const onOpen = vi.fn();
    const { notify } = renderWindow(api, { onOpen });
    const drawings = await screen.findByRole('region', { name: 'Рисунки' });
    fireEvent.click(within(drawings).getByRole('button', { name: /^Вернуть: Рисунок/u }));

    await waitFor(() => expect(notify).toHaveBeenCalledWith(expect.objectContaining({ message: expect.stringMatching(/^Рисунок «.+» возвращён$/u), tone: 'success' })));
    await waitFor(() => expect(screen.queryByRole('region', { name: 'Рисунки' })).toBeNull());
    const line = vi.mocked(notify).mock.calls[0]![0];
    render(<>{line.action}</>);
    fireEvent.click(screen.getByRole('button', { name: 'Открыть' }));
    expect(onOpen).toHaveBeenCalledWith({ path: '/drawing', query: { projectId: 'project-white-port', drawing: drawing.id } });
    expect((await api.getDrawing('project-white-port', drawing.id)).id).toBe(drawing.id);
  });

  // TB1.3: a return over the limit of the tier says why in the one block of refusals, and the row stays.
  it('keeps a book over the limit of the saga in the list under the block of the refusal', async () => {
    const api = createMockApiClient();
    const { books } = await deleteEverything(api);
    vi.spyOn(api, 'restoreBook').mockRejectedValue(quotaRefusalError());
    const { notify } = renderWindow(api);
    const section = await screen.findByRole('region', { name: 'Книги' });
    fireEvent.click(within(section).getByRole('button', { name: /^Вернуть: Книга/u }));

    expect(await screen.findByText(`Книга «${books[2]!.title}» не вернулась. Она подождёт здесь, пока не выйдет срок.`)).toBeTruthy();
    expect(within(screen.getByRole('region', { name: 'Книги' })).getByRole('button', { name: /^Вернуть: Книга/u })).toBeTruthy();
    expect(notify).not.toHaveBeenCalled();
  });

  // specs/2026-11-12-ux10-book-revert.md BR3.3: a book updated from a file waits with its term, «Вернуть…» asks the one question.
  it('lists the update of a book with «Вернуть…» to the version before it', async () => {
    const api = createMockApiClient();
    const book = await updateFirstBook(api);
    await deleteEverything(api);
    const onRevertBook = vi.fn();
    renderWindow(api, { onRevertBook });
    const updates = await screen.findByRole('region', { name: 'Обновления книг' });
    // Board 32·31: the group stands before «Рисунки».
    expect(screen.getAllByRole('heading', { level: 3 }).map((heading) => heading.textContent))
      .toEqual(['Саги', 'Книги', 'Главы', 'Обновления книг', 'Рисунки']);
    const row = within(updates).getByText(`Книга «${book.title}» обновлена из файла`);
    expect(row.parentElement!.textContent).toMatch(/Сага «.+» · сегодня в \d\d:\d\d · можно вернуть прежнюю версию до 23 июня/u);
    const press = within(updates).getByRole('button', { name: `Вернуть прежнюю версию: Книга «${book.title}» обновлена из файла` });
    expect(press.textContent).toBe('Вернуть…');
    fireEvent.click(press);
    expect(onRevertBook).toHaveBeenCalledWith(expect.objectContaining({ id: book.id, title: book.title }));
  });

  // TB1.2, TB1.4: without a saga the window lists the sagas; empty — the one empty block.
  it('shows only what there is: the sagas of an author without one, the empty block when nothing is deleted', async () => {
    const empty = createMockApiClient();
    renderWindow(empty);
    expect(await screen.findByText('Здесь будет то, что вы удалите')).toBeTruthy();

    const api = createMockApiClient();
    await api.deleteProject('project-white-port');
    renderWindow(api);
    expect(await screen.findByRole('heading', { name: 'Саги' })).toBeTruthy();
    expect(screen.queryByRole('heading', { name: 'Книги' })).toBeNull();
  });

  // A race: the row was returned or purged by someone else — the list is read again.
  it('reads the list again after a conflict and says it', async () => {
    const api = createMockApiClient();
    await deleteEverything(api);
    vi.spyOn(api, 'restoreChapter').mockRejectedValue(new ApiStatusError(409, {
      code: 'conflict', detail: 'raced', requestId: 'request_test', status: 409, title: 'Conflict', type: 'about:blank',
    }, new Response(null, { status: 409 })));
    const list = vi.spyOn(api, 'listRecentlyDeleted');
    renderWindow(api);
    const section = await screen.findByRole('region', { name: 'Главы' });
    fireEvent.click(within(section).getAllByRole('button', { name: /^Вернуть: Глава/u })[0]!);

    expect(await screen.findByText('Уже возвращено или удалено другим участником.')).toBeTruthy();
    await waitFor(() => expect(list.mock.calls.length).toBeGreaterThanOrEqual(2));
  });
});

// specs/2026-11-11-ux2-dictionary.md, Decision log (the passes of TEST): the line «… удалена · Вернуть» of a deletion made
// before the window opened does not stand inside it beside the same row with its own «Вернуть»; the line of a return made
// in the window does.
describe('RecentlyDeleted and the lines of the root', () => {
  it('takes in only the lines of what is done in the window', async () => {
    const api = createMockApiClient();
    const { books } = await deleteEverything(api);
    function Page() {
      const toasts = useToasts();
      const [open, setOpen] = useState(false);
      return (
        <>
          <button onClick={() => toasts.push({ action: <button type="button">Вернуть</button>, message: `Книга «${books[2]!.title}» удалена`, tone: 'neutral' })} type="button">Delete</button>
          <button onClick={() => setOpen(true)} type="button">Open</button>
          {open ? <RecentlyDeleted api={api} notify={toasts.push} onClose={vi.fn()} onOpen={vi.fn()} onRevertBook={vi.fn()} revertAsking={null} /> : null}
        </>
      );
    }
    const queryClient = new QueryClient({ defaultOptions: { mutations: { retry: false }, queries: { retry: false } } });
    render(<QueryClientProvider client={queryClient}><ToastsProvider><Page /></ToastsProvider></QueryClientProvider>);
    fireEvent.click(screen.getByRole('button', { name: 'Delete' }));
    expect(screen.getByText(`Книга «${books[2]!.title}» удалена`)).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Open' }));
    const window = await screen.findByRole('dialog', { name: 'Недавно удалённые' });
    const section = await within(window).findByRole('region', { name: 'Книги' });
    expect(within(window).queryByText(`Книга «${books[2]!.title}» удалена`)).toBeNull();
    fireEvent.click(within(section).getByRole('button', { name: /^Вернуть: Книга/u }));
    expect(await within(window).findByText(`Книга «${books[2]!.title}» возвращена`)).toBeTruthy();
  });
});
