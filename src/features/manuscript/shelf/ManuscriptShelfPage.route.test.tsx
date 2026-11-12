import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../../api/mock';
import { bookId, draftBookId, foreignProjectId, projectId } from '../../../api/mock/repository/fixtures';
import { manuscriptQueryKeys, type ImportQueue, type ProjectJob } from '../../../entities/manuscript/api';
import { parseRoute } from '../../../shared/navigation/app-route';
import { FakeWebSocket } from '../../../test/FakeWebSocket';
import { InShell } from '../../../app/shell/AppShell.test-helpers';
import { ManuscriptShelfPage } from './ManuscriptShelfPage';
import { rememberReadingPlace } from '../reader/reading-place';
import { ToastsProvider } from '../../../ui/feedback/Toast';

type ShelfApi = ReturnType<typeof createMockApiClient>;

beforeEach(() => {
  vi.stubGlobal('WebSocket', FakeWebSocket);
});

afterEach(() => {
  FakeWebSocket.reset();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe('ManuscriptShelfPage route state', () => {
  it('shows a route-scoped unavailable state for an explicit unknown project id', async () => {
    let listBooks: ReturnType<typeof vi.spyOn> | undefined;
    renderShelf('?projectId=project-missing&bookId=book-02&sourceView=published', (api) => {
      listBooks = vi.spyOn(api, 'listBooks');
    });

    expect(await screen.findByRole('heading', { name: 'Сага не найдена' })).toBeTruthy();

    expect(screen.queryAllByRole('article')).toHaveLength(0);
    expect(screen.queryByText('Проект загружается')).toBeNull();
    expect(screen.queryByText(/^Сага ·/)).toBeNull();
    expect(screen.queryByText(/У вас пока нет доступных проектов/)).toBeNull();
    expect(screen.queryByRole('heading', { name: 'Создайте проект' })).toBeNull();
    expect(listBooks).not.toHaveBeenCalled();
  });

  // R2.1.3: a failed shelf names what failed, offers a retry and the support code, and no «загружается».
  it('shows a failed shelf with a retry and the support code', async () => {
    renderShelf(`?projectId=${projectId}`, (api) => api.setScenario({ actorRole: 'editor', preset: 'request-error' }));

    const alert = await screen.findByRole('alert');
    expect(within(alert).getByText('Не открылись книги саги')).toBeTruthy();
    expect(within(alert).getByRole('button', { name: 'Повторить' })).toBeTruthy();
    expect(within(alert).getByText('mock-listProjects')).toBeTruthy();
    expect(within(alert).getByRole('link', { name: 'Написать нам' })).toBeTruthy();
    expect(screen.queryByText('Проект загружается')).toBeNull();
    expect(screen.queryByText(/^Сага ·/)).toBeNull();
  });

  // specs/2026-11-11-ux9-shelf-chat.md SC1.9 (W-7G): a calm block, no alarm, with the sagas of the author to open.
  it('offers the sagas of the author in a calm block without exposing the raw route', async () => {
    const view = renderShelf('?projectId=project-missing&bookId=book-02');

    expect(await screen.findByRole('heading', { name: 'Сага не найдена' })).toBeTruthy();
    const missing = screen.getByRole('region', { name: 'Сага не найдена' });
    expect(screen.queryByRole('alert')).toBeNull();
    expect(screen.queryByText(/project-missing/)).toBeNull();

    fireEvent.click(within(within(missing).getByRole('list', { name: 'Ваши саги' })).getByRole('button', { name: 'Хроники Белого порта' }));
    expect(view.navigate).toHaveBeenCalledWith('/manuscript/books', { projectId });
  });

  // specs/2026-11-11-ux7-shelf.md SF1.2: an old address with `bookId` (or `bookMenu`) opens the shelf as usual — no chosen
  // book, no frame, no menu, whatever book it names.
  it.each([`&bookId=${bookId}&bookMenu=open`, '&bookId=book-missing'])('opens the shelf as usual for an old address %s', async (old) => {
    renderShelf(`?projectId=${projectId}${old}&sourceView=draft`);

    const books = await screen.findAllByRole('article');
    expect(books.length).toBeGreaterThan(1);
    expect(books.every((book) => book.getAttribute('aria-current') === null)).toBe(true);
    expect(screen.queryByRole('menu', { name: 'Действия книги' })).toBeNull();
    expect(screen.queryByText('Книга недоступна или не найдена')).toBeNull();
    expect(screen.getByRole('button', { name: 'Добавить книги' })).toBeTruthy();
  });

  it('keeps shelf actions for a valid project without an explicit book', async () => {
    renderShelf(`?projectId=${projectId}&sourceView=draft`);

    await screen.findByRole('article', { name: 'Книга III. Северный суд' });
    // The shelf is named «Книги» by the word of the menu and says in one line what is in the saga (PS1.1, PS1.3).
    expect(screen.getByRole('heading', { level: 1, name: 'Книги' })).toBeTruthy();
    expect(screen.getByText(/^\d+ книг[аи]? · /u)).toBeTruthy();

    expect(screen.queryByRole('button', { name: 'Новая книга' })).toBeNull();
    // The common button in its outline look (specs/2026-11-10-ux2-patterns-refusals.md PR1.1); the whole page takes a
    // file, there is no zone under the books (specs/2026-11-11-ux7-upload.md UP1.1).
    const upload = screen.getByRole('button', { name: 'Добавить книги' });
    expect(upload.getAttribute('data-variant')).toBe('outline');
    expect(screen.queryByText('Перетащите FB2, EPUB, Word (.docx), RTF, ODT или текст (.txt, .md)')).toBeNull();
  });

  it('hides manuscript mutations from a viewer while keeping reading available', async () => {
    renderShelf(`?projectId=${projectId}&sourceView=draft`, (api) => {
      api.setScenario({ actorRole: 'viewer', preset: 'normal' });
    });

    const book = await screen.findByRole('article', { name: 'Книга III. Северный суд' });

    expect(screen.queryByRole('button', { name: /Загрузить FB2/ })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Новая книга' })).toBeNull();
    expect(screen.queryByRole('button', { name: /Удалить сагу/u })).toBeNull();
    expect(within(book).getByRole('link', { name: 'Читать «Книга III. Северный суд»' })).toBeTruthy();
    // What a viewer may do with the saga itself says «Настроить сагу» of the shell (src/app/shell/AppShell.saga.test.tsx).
  });

  it('opens the window of the cover of the exact book of the menu, and the menu changes no address', async () => {
    const view = renderShelf(`?projectId=${projectId}&sourceView=draft`);
    const book = await screen.findByRole('article', { name: 'Книга II. Карта приливов' });

    fireEvent.click(within(book).getByRole('button', { name: 'Действия книги Книга II. Карта приливов' }));
    const menu = await screen.findByRole('menu', { name: 'Действия книги' });
    expect(view.navigate).not.toHaveBeenCalled();
    fireEvent.click(within(menu).getByRole('menuitem', { name: 'Обложка…' }));

    expect(await screen.findByRole('dialog', { name: 'Обложка «Книга II. Карта приливов»' })).toBeTruthy();
    expect(view.navigate).not.toHaveBeenCalled();
  });

  // SF1.1: the card opens the book at the chapter this browser read last; «Читать с начала» — at its first chapter.
  it('opens a book at its place of reading from the card and from its start from the menu', async () => {
    rememberReadingPlace(bookId, 'chapter-13');
    const view = renderShelf(`?projectId=${projectId}&sourceView=draft`);
    const book = await screen.findByRole('article', { name: 'Книга II. Карта приливов' });
    const link = within(book).getByRole('link', { name: 'Читать «Книга II. Карта приливов»' });
    expect(link.getAttribute('href')).toBe(`/manuscript/books?projectId=${projectId}&bookId=${bookId}&mode=read&chapterId=chapter-13`);
    fireEvent.click(link);
    expect(view.navigate).toHaveBeenLastCalledWith('/manuscript/books', { projectId, bookId, mode: 'read', chapterId: 'chapter-13' });

    fireEvent.click(within(book).getByRole('button', { name: 'Действия книги Книга II. Карта приливов' }));
    fireEvent.click(within(await screen.findByRole('menu', { name: 'Действия книги' })).getByRole('menuitem', { name: 'Читать с начала' }));
    expect(view.navigate).toHaveBeenLastCalledWith('/manuscript/books', { projectId, bookId, mode: 'read', chapterId: undefined });
    window.localStorage.clear();
  });

  // SF3.1: «Переименовать» — the title in place; the line of the result gives the old one back by the same operation.
  it('renames a book in place and gives the old title back by «Отменить»', async () => {
    let updateBook: ReturnType<typeof vi.spyOn> | undefined;
    renderShelf(`?projectId=${projectId}&sourceView=draft`, (api) => { updateBook = vi.spyOn(api, 'updateBook'); });
    const book = await screen.findByRole('article', { name: 'Книга II. Карта приливов' });
    fireEvent.click(within(book).getByRole('button', { name: 'Действия книги Книга II. Карта приливов' }));
    fireEvent.click(within(await screen.findByRole('menu', { name: 'Действия книги' })).getByRole('menuitem', { name: 'Переименовать' }));

    const field = await within(book).findByRole('textbox', { name: 'Название книги «Книга II. Карта приливов»' });
    fireEvent.change(field, { target: { value: 'Карта приливов' } });
    fireEvent.keyDown(field, { key: 'Enter' });
    await waitFor(() => expect(updateBook).toHaveBeenCalledWith(bookId, { title: 'Карта приливов' }));
    const line = (await screen.findByText('Книга переименована')).closest('[role="status"]') as HTMLElement;
    expect(within(line).getByText('Было: «Книга II. Карта приливов»')).toBeTruthy();
    expect(await screen.findByRole('link', { name: 'Читать «Карта приливов»' })).toBeTruthy();

    fireEvent.click(within(line).getByRole('button', { name: 'Отменить' }));
    await waitFor(() => expect(updateBook).toHaveBeenLastCalledWith(bookId, { title: 'Книга II. Карта приливов' }));
    expect(await screen.findByRole('link', { name: 'Читать «Книга II. Карта приливов»' })).toBeTruthy();
    expect(await screen.findByText('Название возвращено: «Книга II. Карта приливов»')).toBeTruthy();
  });

  // SF2.1, row 1: a book a new file rewrites says so, and its update and deletion wait with their reason.
  it('says a book is updated from a file and holds its update and deletion with their reason', async () => {
    renderShelf(`?projectId=${projectId}&sourceView=draft`, (api) => {
      vi.spyOn(api, 'listProjectJobs').mockResolvedValue({
        data: [{ ...importJob('job-update-book', 'running'), subject: { type: 'import', id: 'job-update-book', projectId, bookId, importMode: 'update_book' } }],
        meta: { hasMore: false, nextCursor: null },
      } as never);
    });
    const book = await screen.findByRole('article', { name: 'Книга II. Карта приливов' });
    expect(await within(book).findByText('Обновляется из файла')).toBeTruthy();
    fireEvent.click(within(book).getByRole('button', { name: 'Действия книги Книга II. Карта приливов' }));
    const menu = await screen.findByRole('menu', { name: 'Действия книги' });
    expect(within(menu).getByRole('menuitem', { name: /Новая версия из файла…/u }).getAttribute('aria-disabled')).toBe('true');
    expect(within(menu).getByRole('menuitem', { name: 'Удалить книгу…' }).getAttribute('aria-disabled')).toBe('true');
    expect(within(menu).getByText('Пока книга обновляется из файла, удалить её нельзя.')).toBeTruthy();
  });

  it('keeps the shelf and import queue visible when the jobs feed fails', async () => {
    renderShelf(`?projectId=${projectId}&sourceView=draft`, (api) => {
      vi.spyOn(api, 'listProjectJobs').mockRejectedValue(new Error('jobs unavailable'));
      vi.spyOn(api, 'listImportQueues').mockResolvedValue({ data: [runningQueue(draftBookId)] } as never);
    });

    expect(await screen.findByRole('article', { name: 'Книга III. Северный суд' })).toBeTruthy();
    // The upload goes on: «Добавить книги» waits for its end and says so (specs/2026-11-11-ux7-upload.md UP3.5).
    await waitFor(() => expect(screen.getByRole('button', { name: 'Добавить книги' }).getAttribute('aria-disabled')).toBe('true'));
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(screen.queryByText('Не удалось загрузить рукопись.')).toBeNull();
  });

  // specs/2026-11-11-ux7-upload.md UP3.2: a volume that did not read waits at its place with its reason, «Заменить файл»
  // and «Пропустить»; the page sends nothing of its own, and «Пропустить» is the one request.
  it('shows a volume that waits as its card with its two ways on', async () => {
    let createImportQueue: ReturnType<typeof vi.spyOn> | undefined;
    let startImportQueue: ReturnType<typeof vi.spyOn> | undefined;
    let cancelImportQueue: ReturnType<typeof vi.spyOn> | undefined;
    let skipImportQueueItem: ReturnType<typeof vi.spyOn> | undefined;
    const waiting = runningQueue(draftBookId);
    waiting.items = [...waiting.items, {
      ...waiting.items[0]!, id: 'queue-route-item-2', clientItemId: 'client-queue-route-item-2', position: 2,
      sourceFileName: 'Том 5.epub', bookId: null, importJobId: null, processingStatus: 'failed', preflightStatus: 'invalid',
      preflightError: { code: 'invalid_epub_container', message: 'EPUB container is invalid.' }, failedStep: 'preflight',
    }];
    renderShelf(`?projectId=${projectId}&sourceView=draft`, (api) => {
      vi.spyOn(api, 'listImportQueues').mockResolvedValue({ data: [waiting] } as never);
      createImportQueue = vi.spyOn(api, 'createImportQueue');
      startImportQueue = vi.spyOn(api, 'startImportQueue');
      cancelImportQueue = vi.spyOn(api, 'cancelImportQueue');
      skipImportQueueItem = vi.spyOn(api, 'skipImportQueueItem').mockResolvedValue({ ...waiting, status: 'succeeded' } as never);
    });

    const card = await screen.findByRole('article', { name: 'Том 5.epub' });
    expect(within(card).getByText('Не загрузилась')).toBeTruthy();
    expect(within(card).getByText(/^EPUB не читается/u)).toBeTruthy();
    expect(within(card).getByLabelText('Заменить файл «Том 5.epub»')).toBeTruthy();
    const line = screen.getByRole('region', { name: 'Загрузка книг' });
    expect(line.textContent).toContain('«Том 5» ждёт вашего решения');
    fireEvent.click(within(card).getByRole('button', { name: 'Пропустить' }));

    await waitFor(() => expect(skipImportQueueItem).toHaveBeenCalledWith(projectId, 'queue-route', 'queue-route-item-2'));
    expect(createImportQueue).not.toHaveBeenCalled();
    expect(startImportQueue).not.toHaveBeenCalled();
    expect(cancelImportQueue).not.toHaveBeenCalled();
  });

  // specs/2026-11-10-ux1-shelf.md UXS3.6, UXS3.7: a failed upload found after a reload does not jump out as a red
  // window; the shelf names it and the author opens it.
  // specs/2026-11-10-ux1-shelf.md UXS4.1: the only saga, deleted, is found again from the empty shelf.
  // The window is the shell's (specs/2026-11-10-ux2-shell-core.md SC2.3): the shelf opens it, there is no other.
  it('offers the deleted sagas on a shelf without a saga, with only their section', async () => {
    renderShelf('', (api) => {
      api.setScenario({ actorRole: 'owner', preset: 'normal' });
      void api.deleteProject('project-white-port');
    }, true);

    fireEvent.click(await screen.findByRole('button', { name: 'Недавно удалённые: 1 сага' }));

    const dialog = await screen.findByRole('dialog', { name: 'Недавно удалённые' });
    expect(await within(dialog).findByRole('heading', { name: 'Саги' })).toBeTruthy();
    expect(within(dialog).queryByRole('heading', { name: 'Книги' })).toBeNull();
    expect(await within(dialog).findByRole('button', { name: 'Вернуть: Сага «Хроники Белого порта»' })).toBeTruthy();
  });

  // A queue the earlier rule stopped on a volume is history (specs/2026-11-11-ux7-upload.md UP3.2): no card, no line,
  // and the upload is open.
  it('keeps a queue stopped by the earlier rule as history', async () => {
    renderShelf(`?projectId=${projectId}&sourceView=draft`, (api) => {
      vi.spyOn(api, 'listImportQueues').mockResolvedValue({ data: [failedQueue(draftBookId, 'job-failed-reload')] } as never);
    });

    await screen.findByRole('article', { name: 'Книга III. Северный суд' });
    expect(screen.queryByRole('region', { name: 'Загрузка книг' })).toBeNull();
    expect(screen.queryByRole('dialog')).toBeNull();
    await waitFor(() => expect(screen.getByRole('button', { name: 'Добавить книги' }).getAttribute('aria-disabled')).toBeNull());
  });

  it('reconciles Job and indexing status when the upload stops', async () => {
    const jobId = 'job-queue-current';
    const failedJob = importJob(jobId, 'failed');
    const runningJob = { ...importJob(jobId, 'running'), canCancel: true } as ProjectJob;
    let terminal = false;
    let getIndexingStatus: ReturnType<typeof vi.spyOn> | undefined;
    const view = renderShelf(`?projectId=${projectId}&sourceView=draft`, (api) => {
      vi.spyOn(api, 'listImportQueues').mockImplementation(async () => ({
        data: [terminal ? { ...parsingQueue(jobId), status: 'canceled', version: 6 } : parsingQueue(jobId)],
      }) as never);
      const loadJob = api.getJob.bind(api);
      vi.spyOn(api, 'getJob').mockImplementation((id) => (
        id === jobId ? Promise.resolve(terminal ? failedJob : runningJob) : loadJob(id)
      ));
      getIndexingStatus = vi.spyOn(api, 'getIndexingStatus').mockImplementation(async (_id, options = {}) => {
        const status = indexingStatusForJob(terminal ? failedJob : runningJob, 'import');
        return options.bookId ? status as never : { ...status, steps: [] } as never;
      });
    });

    // specs/2026-11-10-ux3-saga-state.md SS2.1: while the file of the book is parsed the line names it, without a count.
    const line = await screen.findByRole('region', { name: 'Чтение саги' });
    await waitFor(() => expect(getIndexingStatus?.mock.calls.length).toBeGreaterThanOrEqual(1));
    expect(within(line).getByText('Читаю файл «Том 4.epub»… · спросить можно будет через пару минут')).toBeTruthy();
    getIndexingStatus?.mockClear();

    terminal = true;
    await act(async () => {
      await view.queryClient.refetchQueries({
        queryKey: manuscriptQueryKeys.importQueues(projectId),
        exact: true,
      });
    });

    await waitFor(() => expect(screen.queryByRole('region', { name: 'Загрузка книг' })).toBeNull());
    await waitFor(() => expect(getIndexingStatus).toHaveBeenCalled());
    // The reading stopped: the same line says so and goes on with «Продолжить»; there is no stop of the author.
    await waitFor(() => expect(screen.getByRole('region', { name: 'Чтение саги' }).getAttribute('data-tone')).toBe('stalled'));
    const stalled = screen.getByRole('region', { name: 'Чтение саги' });
    expect(within(stalled).queryByRole('button', { name: /Остановить|Отменить/u })).toBeNull();
    expect(within(stalled).getByRole('button', { name: 'Дочитать' })).toBeTruthy();
  });

  // specs/2026-11-11-ux7-upload.md UP2.1, UP2.3: one new file goes at once; two and more stay local in «Порядок томов»
  // until «Загрузить N книг». Either way the one path is the queue of the server.
  it.each([
    ['one file', ['Северный ветер.epub']],
    ['many files', ['Том 5.epub', 'Том 6.fb2']],
  ])('sends %s by the one path of the queue', async (_label, fileNames) => {
    let createImportQueue: ReturnType<typeof vi.spyOn> | undefined;
    let uploadImportQueueItem: ReturnType<typeof vi.spyOn> | undefined;
    let startImportQueue: ReturnType<typeof vi.spyOn> | undefined;
    let importBookFile: ReturnType<typeof vi.spyOn> | undefined;
    renderShelf(`?projectId=${projectId}&sourceView=draft`, (api) => {
      vi.spyOn(api, 'listImportQueues').mockResolvedValue({ data: [] } as never);
      // This scenario starts idle; the default fixture includes a running text-index Job.
      const getIndexingStatus = api.getIndexingStatus;
      vi.spyOn(api, 'getIndexingStatus').mockImplementation(async (...args) => ({
        ...await getIndexingStatus(...args), steps: [],
      }));
      createImportQueue = vi.spyOn(api, 'createImportQueue');
      uploadImportQueueItem = vi.spyOn(api, 'uploadImportQueueItem');
      startImportQueue = vi.spyOn(api, 'startImportQueue');
      importBookFile = vi.spyOn(api, 'importBookFile');
    });

    await screen.findByRole('article', { name: 'Книга III. Северный суд' });
    const input = screen.getByLabelText('Файлы книг: FB2, EPUB, Word (.docx), RTF, ODT или текст (.txt, .md)');
    expect((input as HTMLInputElement).multiple).toBe(true);
    fireEvent.change(input, {
      target: {
        files: fileNames.map((name) => new File(
          [name],
          name,
          { type: name.endsWith('.epub') ? 'application/epub+zip' : 'application/x-fictionbook+xml' },
        )),
      },
    });

    if (fileNames.length > 1) {
      const dialog = await screen.findByRole('dialog', { name: 'Порядок томов' });
      expect(within(dialog).queryByRole('spinbutton')).toBeNull();
      fileNames.forEach((name) => expect(within(dialog).getByText(name)).toBeTruthy());
      expect(createImportQueue).not.toHaveBeenCalled();
      fireEvent.click(within(dialog).getByRole('button', { name: 'Загрузить 2 книги' }));
    } else {
      expect(screen.queryByRole('dialog')).toBeNull();
    }

    await waitFor(() => expect(createImportQueue).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(uploadImportQueueItem).toHaveBeenCalledTimes(fileNames.length));
    await waitFor(() => expect(startImportQueue).toHaveBeenCalledTimes(1));
    expect(importBookFile).not.toHaveBeenCalled();
  });

  it('renders the first imported book from the authoritative shelf cache update', async () => {
    let booksVisible = false;
    const waitingQueue = runningQueue(draftBookId);
    waitingQueue.items = waitingQueue.items.map((item) => ({
      ...item,
      bookId: null,
      processingStatus: 'importing',
    }));
    const view = renderShelf(`?projectId=${projectId}&sourceView=draft`, (api) => {
      const listBooks = api.listBooks.bind(api);
      vi.spyOn(api, 'listBooks').mockImplementation((nextProjectId) => (
        booksVisible
          ? listBooks(nextProjectId)
          : Promise.resolve({ data: [], meta: { hasMore: false, nextCursor: null } })
      ));
      vi.spyOn(api, 'listImportQueues').mockResolvedValue({ data: [waitingQueue] } as never);
    });
    // The volume on its way is a card of the empty shelf (specs/2026-11-11-ux7-upload.md UP3.1).
    expect(await screen.findByRole('article', { name: 'Том 4.epub' })).toBeTruthy();
    expect(screen.queryByText('В саге пока нет книг')).toBeNull();

    booksVisible = true;
    const books = (await view.api.listBooks(projectId)).data;
    act(() => view.queryClient.setQueryData(
      manuscriptQueryKeys.shelf({ projectId }),
      (current: object | undefined) => current ? { ...current, books } : current,
    ));

    expect(await screen.findByRole('article', { name: 'Книга III. Северный суд' })).toBeTruthy();
  });

  // UC2.1: «Перейти к книгам» opens the shelf without `?projectId`; a finished queue still shows its book there.
  it('shows the book of a finished queue on the shelf route without a project id', async () => {
    let finished = false;
    const view = renderShelf('', (api) => {
      const listBooks = api.listBooks.bind(api);
      vi.spyOn(api, 'listBooks').mockImplementation((nextProjectId) => (
        finished ? listBooks(nextProjectId) : Promise.resolve({ data: [], meta: { hasMore: false, nextCursor: null } })
      ));
      vi.spyOn(api, 'listProjectJobs').mockResolvedValue({ data: [], meta: { hasMore: false, nextCursor: null } } as never);
      vi.spyOn(api, 'listImportQueues').mockImplementation(async () => ({
        data: [finished ? { ...runningQueue(draftBookId), status: 'succeeded', version: 5, currentItemId: null } : runningQueue(draftBookId)],
      }) as never);
    });
    await screen.findByText('В саге пока нет книг');

    finished = true;
    await act(async () => {
      await view.queryClient.refetchQueries({ queryKey: manuscriptQueryKeys.importQueues(projectId), exact: true });
    });

    expect(await screen.findByRole('article', { name: 'Книга III. Северный суд' })).toBeTruthy();
  });

  // specs/2026-11-10-ux3-saga-state.md SS2.2, SS2.3 (amends UC3 of specs/2026-11-08-deletion-stops-processing.md): the
  // work left by a deleted book is not the author's — no block «Обработка саги», no stop; the deletion stops it itself.
  it('shows no work of a deleted book and no stop of the author', async () => {
    const running = { ...cardsJob('job-cards-orphan', 'running', 'published'), canCancel: true, progress: 0.25 } as ProjectJob;
    let cancelJob: ReturnType<typeof vi.spyOn> | undefined;
    renderShelf(`?projectId=${projectId}`, (api) => {
      vi.spyOn(api, 'listBooks').mockResolvedValue({ data: [], meta: { hasMore: false, nextCursor: null } } as never);
      vi.spyOn(api, 'getIndexingStatus').mockResolvedValue({
        published: { exists: true, context: { state: 'ready', currentBook: null, retryableFailedBook: null } },
        steps: [],
      } as never);
      vi.spyOn(api, 'listProjectJobs').mockResolvedValue({ data: [running], meta: { hasMore: false, nextCursor: null } } as never);
      cancelJob = vi.spyOn(api, 'cancelJob');
    });

    await screen.findByRole('region', { name: 'Книги саги' });
    await waitFor(() => expect(screen.queryByText('Проверяем подготовку…')).toBeNull());
    expect(screen.queryByRole('region', { name: 'Обработка саги' })).toBeNull();
    expect(screen.queryByRole('region', { name: 'Чтение саги' })).toBeNull();
    expect(screen.queryByRole('button', { name: /Остановить/u })).toBeNull();
    expect(cancelJob).not.toHaveBeenCalled();
  });

  it('hides the previous project while a changed project route hydrates', async () => {
    let holdProjectList = false;
    let releaseProjectList: (() => void) | undefined;
    const view = renderShelf(`?projectId=${projectId}&sourceView=draft`, (api) => {
      const listProjects = api.listProjects.bind(api);
      vi.spyOn(api, 'listProjects').mockImplementation(async () => {
        if (holdProjectList) {
          holdProjectList = false;
          await new Promise<void>((resolve) => {
            releaseProjectList = resolve;
          });
        }
        return listProjects();
      });
    });

    await screen.findByRole('article', { name: 'Книга III. Северный суд' });
    expect(screen.getByText(/^\d+ книг[аи]? · /u)).toBeTruthy();

    holdProjectList = true;
    view.rerenderWithRoute(`?projectId=${foreignProjectId}&sourceView=draft`);

    expect(screen.queryAllByRole('article')).toHaveLength(0);
    expect(screen.queryByText(/^\d+ книг[аи]? · /u)).toBeNull();

    await waitFor(() => expect(releaseProjectList).toBeTypeOf('function'));
    await act(async () => releaseProjectList?.());
    expect(await screen.findByRole('heading', { name: 'Сага не найдена' })).toBeTruthy();
  });
});

function renderShelf(search: string, configureApi?: (api: ShelfApi) => void, shell = false) {
  const api = createMockApiClient();
  api.setScenario({ actorRole: 'editor', preset: 'normal' });
  vi.spyOn(api, 'getAgentTimeline').mockResolvedValue({ events: [], eof: true, nextCursor: null } as never);
  configureApi?.(api);
  const navigate = vi.fn();
  const queryClient = new QueryClient({
    defaultOptions: { mutations: { retry: false }, queries: { retry: false } },
  });
  const page = (nextSearch: string) => {
    const route = parseRoute(`http://localhost/manuscript/books${nextSearch}`);
    const shelf = <ManuscriptShelfPage api={api} navigate={navigate} route={route} />;
    return (
      <QueryClientProvider client={queryClient}><ToastsProvider>
        {shell ? <InShell api={api} navigate={navigate} route={route}>{shelf}</InShell> : shelf}
      </ToastsProvider></QueryClientProvider>
    );
  };
  const result = render(page(search));

  return {
    api,
    navigate,
    queryClient,
    rerenderWithRoute: (nextSearch: string) => result.rerender(page(nextSearch)),
  };
}

function indexingStatusForJob(job: ProjectJob, kind: 'import' | 'cards') {
  const active = job.status === 'queued' || job.status === 'running' || job.status === 'canceling';
  // Import publishes the book at once, so both text versions are indexed together.
  const envelope = {
    exists: true,
    context: active
      ? {
        state: 'processing',
        currentBook: { bookId: draftBookId, position: 1, total: 1 },
        retryableFailedBook: null,
      }
      : {
        state: 'partial',
        currentBook: null,
        retryableFailedBook: { bookId: draftBookId },
      },
  };
  return {
    draft: envelope,
    published: envelope,
    // The server counts the reading while the text is in work (SS1.1): here the file is still parsed.
    ...(active ? {
      preparation: {
        remainingSeconds: null, letterPromised: false, chaptersRead: 0, chaptersTotal: 0, booksRead: 0, booksTotal: 0,
        currentBookId: null, askable: false, books: [],
      },
    } : {}),
    steps: [{
      bookId: kind === 'import' ? draftBookId : undefined,
      canCancel: job.canCancel,
      canRetry: job.status === 'failed',
      jobId: job.id,
      kind,
      progress: null,
      sourceView: kind === 'cards' ? 'published' : undefined,
      status: job.status,
    }],
  };
}

function importJob(id: string, status: ProjectJob['status'], resultBookId: string | null = null): ProjectJob {
  return {
    id,
    kind: 'import',
    status,
    progress: null,
    phase: null,
    subject: { type: 'import', id, projectId },
    result: resultBookId ? {
      type: 'import', bookId: resultBookId, chapterCount: 2, paragraphCount: 3,
      importMode: 'new_book', sourceFileName: 'Том 4.epub',
    } : null,
    error: status === 'failed'
      ? { type: 'about:blank', title: 'Job failed', status: 500, code: 'worker.handler_failed', detail: 'Worker failed.', requestId: '' }
      : null,
    canCancel: status === 'running' || status === 'queued',
    expiresAt: null,
    createdAt: '2026-08-20T10:00:00.000Z',
    updatedAt: '2026-08-20T10:01:00.000Z',
    links: { self: `/api/v1/jobs/${id}`, cancel: null, timeline: null },
  } as ProjectJob;
}

function cardsJob(id: string, status: ProjectJob['status'], sourceView: 'draft' | 'published' = 'draft'): ProjectJob {
  return {
    id,
    kind: 'chapter_cards',
    status,
    progress: null,
    phase: null,
    subject: { type: 'chapter_cards', projectId, sourceView },
    result: null,
    error: null,
    canCancel: false,
    expiresAt: null,
    createdAt: '2026-08-12T18:00:00.000Z',
    updatedAt: '2026-08-12T18:01:00.000Z',
    links: { self: `/api/v1/jobs/${id}`, cancel: null, timeline: null },
  } as ProjectJob;
}

function runningQueue(currentBookId: string, importJobId = 'job-import-route'): ImportQueue {
  return {
    id: 'queue-route',
    projectId,
    status: 'running',
    version: 4,
    intendedItemCount: 1,
    currentItemId: 'queue-route-item',
    createdAt: '2026-08-20T10:00:00.000Z',
    updatedAt: '2026-08-20T10:00:00.000Z',
    startedAt: '2026-08-20T10:00:00.000Z',
    completedAt: null,
    items: [{
      id: 'queue-route-item',
      clientItemId: 'client-queue-route-item',
      position: 1,
      sourceFileName: 'Том 4.epub',
      mimeType: 'application/epub+zip',
      sizeBytes: 24,
      preflightStatus: 'ready',
      preflightError: null,
      processingStatus: 'indexing',
      failureClass: null,
      failedStep: null,
      importJobId,
      bookId: currentBookId,
      updatedAt: '2026-08-20T10:00:00.000Z',
      links: { status: `/projects/${projectId}/books/${currentBookId}?sourceView=draft` },
    }],
    links: { self: `/projects/${projectId}/import-queues/queue-route`, cancel: null },
  };
}

/** A queue whose one volume the server reads now: its book is not on the shelf yet. */
function parsingQueue(importJobId: string): ImportQueue {
  const queue = runningQueue('', importJobId);
  return { ...queue, items: queue.items.map((item) => ({ ...item, bookId: null, processingStatus: 'importing', links: { status: null } })) };
}

function failedQueue(currentBookId: string, importJobId: string): ImportQueue {
  const queue = runningQueue(currentBookId, importJobId);
  return {
    ...queue,
    status: 'failed',
    version: queue.version + 1,
    updatedAt: '2026-08-20T10:02:00.000Z',
    completedAt: '2026-08-20T10:02:00.000Z',
    items: queue.items.map((item) => ({
      ...item,
      processingStatus: 'failed',
      failureClass: 'unexpected_deterministic_failure',
      failedStep: 'import',
      updatedAt: '2026-08-20T10:02:00.000Z',
    })),
  };
}
