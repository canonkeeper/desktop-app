import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../../api/mock';
import { projectId } from '../../../api/mock/repository/fixtures';
import type { AccountQuota } from '../../../entities/account-quota/api';
import type { ImportQueue } from '../../../entities/manuscript/api';
import { manuscriptQueryKeys } from '../../../entities/manuscript/api';
import { parseRoute } from '../../../shared/navigation/app-route';
import { FakeWebSocket } from '../../../test/FakeWebSocket';
import { ToastsProvider } from '../../../ui/feedback/Toast';
import { ManuscriptShelfPage } from './ManuscriptShelfPage';
import { ShelfSlotProvider } from './shelf-slot';

beforeEach(() => vi.stubGlobal('WebSocket', FakeWebSocket));
afterEach(() => {
  FakeWebSocket.reset();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

const fileInput = 'Файлы книг: FB2, EPUB, Word (.docx), RTF, ODT или текст (.txt, .md)';
const epub = (name: string) => new File(['book'], name, { type: 'application/epub+zip' });

async function renderShelf(arrange?: (api: ReturnType<typeof createMockApiClient>) => void, open = true) {
  const api = createMockApiClient();
  api.setScenario({ actorRole: 'editor', preset: 'normal' });
  arrange?.(api);
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(<QueryClientProvider client={queryClient}><ToastsProvider>
    <ManuscriptShelfPage api={api} navigate={vi.fn()} route={parseRoute(`http://localhost/manuscript/books?projectId=${projectId}`)} />
  </ToastsProvider></QueryClientProvider>);
  await screen.findByRole('article', { name: 'Книга III. Северный суд' });
  if (!open) return { api, queryClient };
  await waitFor(() => expect(screen.getByLabelText(fileInput)).toHaveProperty('disabled', false));
  // The constraints of the server are read: the page checks files only against them.
  await waitFor(() => expect(screen.getByRole('button', { name: 'Добавить книги' }).getAttribute('aria-disabled')).toBeNull());
  return { api, queryClient };
}

function drop(target: Element, files: File[]) {
  fireEvent.dragEnter(target, { dataTransfer: { files, types: ['Files'] } });
  fireEvent.dragOver(target, { dataTransfer: { files, types: ['Files'] } });
  fireEvent.drop(target, { dataTransfer: { files, types: ['Files'] } });
}

function choose(files: File[]) {
  fireEvent.change(screen.getByLabelText(fileInput), { target: { files } });
}

// specs/2026-11-11-ux7-upload.md UP1.1, UP1.2: the whole page takes a file, and a file of a foreign kind gets its reason
// while the others go.
describe('the page takes a file', () => {
  it('takes files dropped on a book card and on the heading; a foreign kind gets its reason, the others go', async () => {
    const { api } = await renderShelf();
    const create = vi.spyOn(api, 'createImportQueue');

    drop(screen.getByRole('article', { name: 'Книга III. Северный суд' }), [epub('Пламя.epub'), new File(['%PDF'], 'рукопись.pdf', { type: 'application/pdf' })]);
    const dialog = await screen.findByRole('dialog', { name: 'Порядок томов' });
    expect(within(dialog).getByText(/^«рукопись\.pdf» — PDF не поддерживается/u)).toBeTruthy();
    expect(within(dialog).getByText('Подошли 1 из 2')).toBeTruthy();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Загрузить книгу' }));
    await waitFor(() => expect(create).toHaveBeenCalledTimes(1));
    expect(create.mock.calls[0]![1].items.map((item) => item.sourceFileName)).toEqual(['Пламя.epub']);
    expect(await screen.findByRole('article', { name: 'Пламя.epub' })).toBeTruthy();
  });

  // The flake of the full check of 12.11: files dropped in the first moment of the page, before the server said what it
  // takes, were dropped silently. The page waits for the answer and then asks as usual.
  it('takes files dropped before the server said what it takes: waits for the answer, then opens the order', async () => {
    let answer: (() => void) | undefined;
    await renderShelf((api) => {
      const read = api.getImportConstraints.bind(api);
      vi.spyOn(api, 'getImportConstraints').mockImplementation(async (...args) => {
        await new Promise<void>((resolve) => { answer = resolve; });
        return read(...args);
      });
    }, false);
    await waitFor(() => expect(answer).toBeTruthy());

    drop(screen.getByRole('article', { name: 'Книга III. Северный суд' }), [epub('Пламя.epub'), epub('Лёд.epub')]);
    expect(screen.queryByRole('dialog', { name: 'Порядок томов' })).toBeNull();
    answer!();

    const dialog = await screen.findByRole('dialog', { name: 'Порядок томов' });
    expect(within(dialog).getAllByRole('listitem')).toHaveLength(2);
  });

  it('gives a foreign file dropped on the heading its reason on the shelf and sends nothing', async () => {
    const { api } = await renderShelf();
    const create = vi.spyOn(api, 'createImportQueue');
    drop(screen.getByRole('heading', { level: 1, name: 'Книги' }), [new File(['%PDF'], 'рукопись.pdf', { type: 'application/pdf' })]);
    const reasons = await screen.findByRole('alert', { name: 'Этот файл не подошёл' });
    expect(reasons.textContent).toContain('PDF не поддерживается');
    expect(within(reasons).getByRole('button', { name: 'Выбрать другой' })).toBeTruthy();
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(create).not.toHaveBeenCalled();
  });
});

// UP2.1, UP2.2: one new file goes at once; a file like a book of the shelf asks first, and «Заменить» opens «Что изменится».
describe('one file', () => {
  it('goes at once without a window, and its card stands on the shelf', async () => {
    const { api } = await renderShelf();
    const create = vi.spyOn(api, 'createImportQueue');
    const start = vi.spyOn(api, 'startImportQueue');
    choose([epub('Северный ветер.epub')]);
    await waitFor(() => expect(create).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(start).toHaveBeenCalledTimes(1));
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(await screen.findByRole('article', { name: 'Северный ветер.epub' })).toBeTruthy();
  });

  it('asks «заменить или добавить» for a file like a book of the shelf; «Заменить» opens what would change', async () => {
    const { api } = await renderShelf();
    const create = vi.spyOn(api, 'createImportQueue');
    const preview = vi.spyOn(api, 'importBookFile');
    choose([epub('Книга III. Северный суд.epub')]);
    const question = await screen.findByRole('dialog', { name: /^«Книга III\. Северный суд» уже есть в саге\. Заменить её этим файлом или добавить новой книгой\?$/u });
    expect(within(question).getByRole('button', { name: 'Добавить новой' })).toBeTruthy();
    fireEvent.click(within(question).getByRole('button', { name: 'Заменить' }));
    expect(await screen.findByRole('dialog', { name: 'Обновить книгу из файла' })).toBeTruthy();
    await waitFor(() => expect(preview).toHaveBeenCalledTimes(1));
    expect(preview.mock.calls[0]![1]).toMatchObject({ options: { importMode: 'update_preview' } });
    expect(create).not.toHaveBeenCalled();
  });

  // specs/2026-11-11-ux9-shelf-chat.md SC1.3 (L-51, Ф-2): a file dropped on a cover is asked about as that book, and a file
  // that begins as a book does is that book, whatever its name.
  it('asks about the book a file is dropped on, by its name', async () => {
    const { api } = await renderShelf();
    const create = vi.spyOn(api, 'createImportQueue');
    drop(screen.getByRole('article', { name: 'Книга III. Северный суд' }), [epub('рукопись после правки.epub')]);
    const question = await screen.findByRole('dialog', { name: 'Заменить «Книга III. Северный суд» этим файлом или добавить новой книгой?' });
    expect(within(question).getByRole('button', { name: 'Заменить' })).toBeTruthy();
    expect(within(question).getByRole('button', { name: 'Добавить новой' })).toBeTruthy();
    expect(create).not.toHaveBeenCalled();
  });

  it('asks about the book a file begins as, whatever the name of the file', async () => {
    const { api } = await renderShelf();
    const create = vi.spyOn(api, 'createImportQueue');
    const books = await api.listBooks(projectId);
    const third = books.data.find((book) => book.title === 'Книга III. Северный суд')!;
    expect(third.opening).toBeTruthy();
    choose([new File([`# Черновик\n\n${third.opening}`], 'черновик-12.md', { type: 'text/markdown' })]);
    expect(await screen.findByRole('dialog', { name: /^«Книга III\. Северный суд» уже есть в саге\./u })).toBeTruthy();
    expect(create).not.toHaveBeenCalled();
  });

  it('adds the file as a new book by «Добавить новой»', async () => {
    const { api } = await renderShelf();
    const create = vi.spyOn(api, 'createImportQueue');
    choose([epub('Книга III. Северный суд.epub')]);
    const question = await screen.findByRole('dialog', { name: /уже есть в саге/u });
    fireEvent.click(within(question).getByRole('button', { name: 'Добавить новой' }));
    await waitFor(() => expect(create).toHaveBeenCalledTimes(1));
  });
});

// UP1.3: what the tier takes is known before any transfer.
describe('what the saga takes', () => {
  it('asks to load the first two of five files where two fit, before any request of the upload', async () => {
    const { api } = await renderShelf((client) => {
      const read = client.getAccountQuota.bind(client);
      vi.spyOn(client, 'getAccountQuota').mockImplementation(async () => {
        const quota = await read();
        return {
          ...quota,
          ownedProjects: quota.ownedProjects.map((project) => project.projectId === projectId
            ? { ...project, sources: { ...project.sources, books: { used: 4, limit: 6 } } } : project),
        } as AccountQuota;
      });
    });
    const create = vi.spyOn(api, 'createImportQueue');
    choose(['Том 5.epub', 'Том 6.epub', 'Том 7.epub', 'Том 8.epub', 'Том 9.epub'].map(epub));
    const dialog = await screen.findByRole('dialog', { name: 'Порядок томов' });
    expect(within(dialog).getByText('В этой саге поместится 2 из 5 книг')).toBeTruthy();
    expect(create).not.toHaveBeenCalled();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Загрузить две' }));
    await waitFor(() => expect(create).toHaveBeenCalledTimes(1));
    expect(create.mock.calls[0]![1].items.map((item) => item.sourceFileName)).toEqual(['Том 5.epub', 'Том 6.epub']);
  });
});

// UP3.1, UP3.3, UP3.5: the upload goes on the shelf — a card of each volume and one line of the saga; «Остановить
// остальные» asks, keeps the books on the shelf and lets the upload go on later.
describe('the upload on the shelf', () => {
  it('shows the volumes as cards and one line, and stops the rest after the question', async () => {
    const { api } = await renderShelf();
    const cancel = vi.spyOn(api, 'cancelImportQueue');
    const create = vi.spyOn(api, 'createImportQueue');
    const start = vi.spyOn(api, 'startImportQueue');
    choose([epub('Том 5. Огни.epub'), epub('Том 6. Пепел.epub')]);
    fireEvent.click(within(await screen.findByRole('dialog', { name: 'Порядок томов' })).getByRole('button', { name: 'Загрузить 2 книги' }));
    // Both files are with the server and the upload goes by itself.
    await waitFor(() => expect(start).toHaveBeenCalledTimes(1));

    const line = await screen.findByRole('region', { name: 'Загрузка книг' });
    await waitFor(() => expect(line.textContent).toContain('Загружаю книги: 0 из 2 готово, идёт «Том 5. Огни»'));
    await waitFor(() => expect(screen.getByRole('article', { name: 'Том 5. Огни.epub' }).textContent).toMatch(/Загружается/u));
    expect(within(screen.getByRole('article', { name: 'Том 6. Пепел.epub' })).getByText('Ждёт загрузки')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Добавить книги' }).getAttribute('aria-disabled')).toBe('true');
    const page = screen.getByRole('region', { name: 'Книги саги' }).textContent!.toLowerCase();
    for (const word of ['очеред', 'импорт', 'передач', 'предпровер', 'автозапуск', 'текущий файл']) expect(page).not.toContain(word);

    fireEvent.click(within(line).getByRole('button', { name: 'Остановить остальные' }));
    const question = screen.getByRole('dialog', { name: 'Не загружать оставшиеся 2 книги?' });
    expect(within(question).getByText('Готовые книги останутся в саге.')).toBeTruthy();
    fireEvent.click(within(question).getByRole('button', { name: 'Не загружать' }));
    await waitFor(() => expect(cancel).toHaveBeenCalledTimes(1));
    const stopped = await screen.findByRole('region', { name: 'Загрузка книг' });
    await waitFor(() => expect(stopped.textContent).toContain('Загрузка остановлена: добавлено 0 из 2'));
    expect(screen.getByRole('article', { name: 'Книга III. Северный суд' })).toBeTruthy();

    fireEvent.click(within(stopped).getByRole('button', { name: 'Продолжить' }));
    await waitFor(() => expect(create).toHaveBeenCalledTimes(2));
    expect(create.mock.calls[1]![1].items.map((item) => item.sourceFileName)).toEqual(['Том 5. Огни.epub', 'Том 6. Пепел.epub']);
  });
});

// UP4.1, UP4.3: the end of an upload says the books and the chapters, and no «готова» while the saga is read.
describe('the end of an upload', () => {
  /** The one volume of an upload, going or ended, with the times of the server. */
  const queue = (status: ImportQueue['status'], at = new Date().toISOString()): ImportQueue => ({
    id: 'queue-end', projectId, status, version: status === 'succeeded' ? 6 : 5, intendedItemCount: 1, currentItemId: null,
    createdAt: at, updatedAt: at, startedAt: at, completedAt: status === 'succeeded' ? at : null,
    links: { self: '/queue-end', cancel: null },
    items: [{
      id: 'queue-end-item', clientItemId: 'client-end', position: 1, sourceFileName: 'Северный суд.fb2', mimeType: null, sizeBytes: 4,
      preflightStatus: 'ready', preflightError: null, processingStatus: status === 'succeeded' ? 'succeeded' : 'importing',
      failureClass: null, failedStep: null, importJobId: 'job-end', bookId: status === 'succeeded' ? 'book-03' : null,
      updatedAt: at, links: { status: null },
    }],
  });

  it('says how many books and chapters came and leads to how they were read', async () => {
    let finished = false;
    const { queryClient } = await renderShelf((client) => {
      vi.spyOn(client, 'listImportQueues').mockImplementation(async () => ({ data: [queue(finished ? 'succeeded' : 'running')] }));
    }, false);
    await screen.findByRole('article', { name: 'Северный суд.fb2' });

    finished = true;
    await act(async () => {
      await queryClient.refetchQueries({ queryKey: manuscriptQueryKeys.importQueues(projectId), exact: true });
    });

    // The line of the shared area and the line of the saga say the same words (UP4.1).
    await waitFor(() => expect(screen.getByRole('region', { name: 'Загрузка книг' }).textContent).toMatch(/^Книга загружена: \d+ глав/u));
    const status = await waitFor(() => screen.getAllByRole('status').find((each) => /^Книга загружена/u.test(each.textContent ?? '')
      && within(each).queryByRole('button', { name: 'Как разобралась книга' }))!);
    expect(status.textContent).not.toMatch(/готов/u);
    fireEvent.click(within(status).getByRole('button', { name: 'Как разобралась книга' }));
    expect(await screen.findByRole('dialog', { name: 'Как разобралась книга «Книга III. Северный суд»' })).toBeTruthy();
  });

  // Found by the e2e of the mock, whose clock stands months behind: the toast of an upload this tab saw going does not
  // hang on the clock of the server; only the line that waits to be looked at is of the last day.
  it('says the end of an upload this tab saw going whatever the clock of the server', async () => {
    let finished = false;
    const serverTime = '2026-06-16T05:00:00.000Z';
    const { queryClient } = await renderShelf((client) => {
      vi.spyOn(client, 'listImportQueues').mockImplementation(async () => ({ data: [queue(finished ? 'succeeded' : 'running', serverTime)] }));
    }, false);
    await screen.findByRole('article', { name: 'Северный суд.fb2' });

    finished = true;
    await act(async () => {
      await queryClient.refetchQueries({ queryKey: manuscriptQueryKeys.importQueues(projectId), exact: true });
    });

    await waitFor(() => expect(screen.getAllByRole('status').some((each) => /^Книга загружена: \d+ глав/u.test(each.textContent ?? ''))).toBe(true));
    expect(screen.queryByRole('region', { name: 'Загрузка книг' })).toBeNull();
  });

  // Found by the e2e: the queue of two volumes turned «succeeded» one read before the books of the shelf had its last
  // book, and the toast said «Книга загружена» of one. The end is said once the shelf has every book of the upload.
  it('says the end once the shelf has every book of the upload', async () => {
    let finished = false;
    let arrived = false;
    const two = (status: ImportQueue['status']): ImportQueue => {
      const one = queue(status);
      return {
        ...one, intendedItemCount: 2,
        items: [one.items[0]!, {
          ...one.items[0]!, id: 'queue-end-item-2', clientItemId: 'client-end-2', position: 2, sourceFileName: 'Поздний том.fb2',
          importJobId: 'job-end-2', bookId: status === 'succeeded' ? 'book-late' : null,
        }],
      };
    };
    const { queryClient } = await renderShelf((client) => {
      vi.spyOn(client, 'listImportQueues').mockImplementation(async () => ({ data: [two(finished ? 'succeeded' : 'running')] }));
      const listBooks = client.listBooks.bind(client);
      vi.spyOn(client, 'listBooks').mockImplementation(async (...args) => {
        const page = await listBooks(...args);
        if (!arrived) return page;
        const model = page.data.find((book) => book.id === 'book-03')!;
        return { ...page, data: [...page.data, { ...model, id: 'book-late', title: 'Поздний том', order: 99, place: page.data.length + 1 }] };
      });
    }, false);
    await screen.findByRole('article', { name: 'Поздний том.fb2' });

    finished = true;
    await act(async () => {
      await queryClient.refetchQueries({ queryKey: manuscriptQueryKeys.importQueues(projectId), exact: true });
    });
    // The page has read the end of the queue and its books; the toast would stand by now.
    await act(() => new Promise((resolve) => setTimeout(resolve, 300)));
    expect(screen.queryAllByRole('status').some((each) => /^Книга загружена/u.test(each.textContent ?? ''))).toBe(false);

    arrived = true;
    await act(async () => {
      await queryClient.refetchQueries();
    });
    await waitFor(() => expect(screen.getAllByRole('status').some((each) => /^Загружено 2 книги: \d+ глав/u.test(each.textContent ?? ''))).toBe(true));
    expect(screen.queryAllByRole('status').some((each) => /^Книга загружена/u.test(each.textContent ?? ''))).toBe(false);
  });
});

// specs/2026-11-11-ux9-shelf-chat.md SC1.7 (W-12): skills, places and characters of a table are not a book — the line at
// «Добавить книги» says where they go and leads there.
describe('the line of the materials at «Добавить книги»', () => {
  it('says that the tables of skills, places and heroes go to «Ваши материалы» and opens them', async () => {
    const api = createMockApiClient();
    api.setScenario({ actorRole: 'editor', preset: 'normal' });
    const navigate = vi.fn();
    render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><ToastsProvider>
      <ShelfSlotProvider value={{ materialsFromFiles: true }}>
        <ManuscriptShelfPage api={api} navigate={navigate} route={parseRoute(`http://localhost/manuscript/books?projectId=${projectId}`)} />
      </ShelfSlotProvider>
    </ToastsProvider></QueryClientProvider>);
    const line = await screen.findByText(/^Навыки, места и герои из таблиц — в/u);
    fireEvent.click(within(line).getByRole('button', { name: '«Ваши материалы»' }));
    expect(navigate).toHaveBeenCalledWith('/memory', { projectId, part: 'materials' });
  });
});
