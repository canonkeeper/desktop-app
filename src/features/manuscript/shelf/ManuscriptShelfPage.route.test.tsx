import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../../api/mock';
import { bookId, draftBookId, foreignProjectId, projectId } from '../../../api/mock/repository/fixtures';
import { manuscriptQueryKeys, type ImportQueue, type ProjectJob } from '../../../entities/manuscript/api';
import { parseRoute } from '../../../shared/navigation/app-route';
import { FakeWebSocket } from '../../../test/FakeWebSocket';
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
    expect(within(alert).getByText('Не удалось загрузить книги')).toBeTruthy();
    expect(within(alert).getByRole('button', { name: 'Повторить' })).toBeTruthy();
    expect(within(alert).getByText('mock-listProjects')).toBeTruthy();
    expect(within(alert).getByRole('link', { name: 'Написать в поддержку' })).toBeTruthy();
    expect(screen.queryByText('Проект загружается')).toBeNull();
    expect(screen.queryByText(/^Сага ·/)).toBeNull();
  });

  it('offers project-not-found recovery without exposing the raw route', async () => {
    const view = renderShelf('?projectId=project-missing&bookId=book-02');

    expect(await screen.findByRole('heading', { name: 'Сага не найдена' })).toBeTruthy();
    expect(screen.getByText('Сага недоступна или вы перешли по старой ссылке.')).toBeTruthy();
    expect(screen.queryByText(/project-missing/)).toBeNull();

    fireEvent.click(within(screen.getByRole('alert')).getByRole('button', { name: 'Мои книги' }));
    expect(view.navigate).toHaveBeenCalledWith('/manuscript/books', {});
  });

  it('creates a project from the selector when the route project is unavailable', async () => {
    let createProject: ReturnType<typeof vi.spyOn> | undefined;
    const view = renderShelf('?projectId=project-missing&sourceView=draft', (api) => {
      createProject = vi.spyOn(api, 'createProject');
    });

    await screen.findByRole('heading', { name: 'Сага не найдена' });
    fireEvent.click(screen.getByRole('button', { name: 'Сага не найдена' }));
    fireEvent.click(within(screen.getByRole('dialog', { name: 'Выбор саги' })).getByRole('button', { name: 'Новая сага' }));
    fireEvent.change(screen.getByRole('textbox', { name: 'Название саги' }), {
      target: { value: 'TEST Saga 1→12' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Создать сагу' }));

    await waitFor(() => expect(createProject).toHaveBeenCalledWith({ title: 'TEST Saga 1→12' }));
    await waitFor(() => expect(view.navigate).toHaveBeenCalledWith('/manuscript/books', {
      projectId: expect.any(String),
    }));
  });

  it('shows a route-scoped read-only state for an explicit unknown book id', async () => {
    renderShelf(`?projectId=${projectId}&bookId=book-missing&sourceView=draft`);

    expect(await screen.findByText('Книга недоступна или не найдена')).toBeTruthy();

    expect(screen.queryAllByRole('article')).toHaveLength(0);
    expect(screen.queryByRole('button', { name: 'Новая книга' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Загрузить FB2 / EPUB / DOCX' })).toBeNull();
  });

  it('keeps shelf actions for a valid project without an explicit book', async () => {
    renderShelf(`?projectId=${projectId}&sourceView=draft`);

    await screen.findByRole('article', { name: 'Книга III. Северный суд' });
    expect(screen.getByText(/^Сага ·/)).toBeTruthy();

    expect(screen.queryByRole('button', { name: 'Новая книга' })).toBeNull();
    const upload = screen.getByRole('button', { name: 'Загрузить файл' });
    const uploadBackgrounds = [...document.styleSheets].flatMap((sheet) => [...sheet.cssRules])
      .filter((rule): rule is CSSStyleRule => rule instanceof CSSStyleRule
        && [...upload.classList].some((name) => rule.selectorText === `.${name}`)
        && Boolean(rule.style.getPropertyValue('background')))
      .map((rule) => rule.style.getPropertyValue('background'));
    expect(uploadBackgrounds.at(-1)).toBe('transparent');
    expect(screen.getByText('Перетащите FB2, EPUB или DOCX')).toBeTruthy();
    expect(screen.getByText('Разделим на главы и подготовим к вопросам · до 50 МБ')).toBeTruthy();
  });

  it('keeps project deletion disabled for an editor', async () => {
    renderShelf(`?projectId=${projectId}&sourceView=draft`);

    await screen.findByRole('article', { name: 'Книга III. Северный суд' });
    fireEvent.click(screen.getByRole('button', { name: 'Хроники Белого порта' }));
    const deleteButton = within(screen.getByRole('dialog', { name: 'Выбор саги' }))
      .getByRole('button', { name: 'Удалить сагу' }) as HTMLButtonElement;

    expect(deleteButton.disabled).toBe(true);
    expect(deleteButton.title).toBe('Удалить сагу может её владелец или администратор.');
    fireEvent.click(deleteButton);
    expect(screen.queryByRole('dialog', { name: 'Удалить сагу' })).toBeNull();
  });

  it('hides manuscript mutations from a viewer while keeping reading available', async () => {
    renderShelf(`?projectId=${projectId}&sourceView=draft`, (api) => {
      api.setScenario({ actorRole: 'viewer', preset: 'normal' });
    });

    const book = await screen.findByRole('article', { name: 'Книга III. Северный суд' });

    expect(screen.queryByRole('button', { name: /Загрузить FB2/ })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Новая книга' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Удалить сагу' })).toBeNull();
    expect(within(book).getByRole('button', { name: 'Открыть чтение Книга III. Северный суд' })).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Хроники Белого порта' }));
    expect(within(screen.getByRole('dialog', { name: 'Выбор саги' }))
      .getByRole('button', { name: 'Удалить сагу' })).toHaveProperty('disabled', true);
  });

  it('opens the cover editor from the exact selected book action', async () => {
    renderShelf(`?projectId=${projectId}&bookId=${bookId}&sourceView=draft`);
    const selected = await screen.findByRole('article', { name: 'Книга II. Карта приливов' });

    fireEvent.click(within(selected).getByRole('button', { name: 'Действия книги Книга II. Карта приливов' }));
    const menu = await screen.findByRole('menu', { name: 'Действия книги' });
    fireEvent.click(within(menu).getByRole('menuitem', { name: 'Изменить обложку' }));

    const editor = await screen.findByRole('dialog', { name: 'Обложка книги' });
    expect(within(editor).getByText('Книга II. Карта приливов')).toBeTruthy();
  });

  it.each(['owner', 'admin'] as const)('allows a %s to delete the selected project only after confirmation', async (actorRole) => {
    let deleteProject: ReturnType<typeof vi.spyOn> | undefined;
    const view = renderShelf(`?projectId=${projectId}&sourceView=draft`, (api) => {
      api.setScenario({ actorRole, preset: 'normal' });
      deleteProject = vi.spyOn(api, 'deleteProject').mockResolvedValue();
    });

    await screen.findByRole('article', { name: 'Книга III. Северный суд' });
    fireEvent.click(screen.getByRole('button', { name: 'Хроники Белого порта' }));
    const deleteButton = within(screen.getByRole('dialog', { name: 'Выбор саги' }))
      .getByRole('button', { name: 'Удалить сагу' }) as HTMLButtonElement;
    expect(deleteButton.disabled).toBe(false);
    fireEvent.click(deleteButton);
    expect(await screen.findByRole('dialog', { name: 'Удалить сагу' })).toBeTruthy();
    expect(deleteProject).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole('button', { name: 'Удалить' }));

    await waitFor(() => expect(deleteProject).toHaveBeenCalledWith(projectId));
    await waitFor(() => expect(view.navigate).toHaveBeenCalledWith('/manuscript/books'));
  });

  it('renames the selected project from its role-aware menu', async () => {
    let updateProject: ReturnType<typeof vi.spyOn> | undefined;
    renderShelf(`?projectId=${projectId}&sourceView=draft`, (api) => {
      api.setScenario({ actorRole: 'owner', preset: 'normal' });
      updateProject = vi.spyOn(api, 'updateProject');
    });
    await screen.findByRole('article', { name: 'Книга III. Северный суд' });

    fireEvent.click(screen.getByRole('button', { name: 'Хроники Белого порта' }));
    const menu = screen.getByRole('dialog', { name: 'Выбор саги' });
    fireEvent.click(within(menu).getByRole('button', { name: 'Переименовать сагу' }));
    fireEvent.change(within(menu).getByRole('textbox', { name: 'Новое название саги' }), {
      target: { value: 'Новый Белый порт' },
    });
    fireEvent.click(within(menu).getByRole('button', { name: 'Сохранить название' }));

    await waitFor(() => expect(updateProject).toHaveBeenCalledWith(projectId, { title: 'Новый Белый порт' }));
  });

  it('creates another project from the project selector', async () => {
    let createProject: ReturnType<typeof vi.spyOn> | undefined;
    const view = renderShelf(`?projectId=${projectId}&sourceView=draft`, (api) => {
      createProject = vi.spyOn(api, 'createProject');
    });

    await screen.findByRole('article', { name: 'Книга III. Северный суд' });
    fireEvent.click(screen.getByRole('button', { name: 'Хроники Белого порта' }));
    fireEvent.click(within(screen.getByRole('dialog', { name: 'Выбор саги' })).getByRole('button', { name: 'Новая сага' }));

    fireEvent.change(screen.getByRole('textbox', { name: 'Название саги' }), {
      target: { value: 'Сага двенадцати томов' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Создать сагу' }));

    await waitFor(() => expect(createProject).toHaveBeenCalledWith({ title: 'Сага двенадцати томов' }));
    await waitFor(() => expect(view.navigate).toHaveBeenCalledWith('/manuscript/books', {
      projectId: expect.any(String),
    }));
    await waitFor(() => expect(screen.queryByText('Создайте проект')).toBeNull());
  });

  it('keeps the shelf and import queue visible when the jobs feed fails', async () => {
    renderShelf(`?projectId=${projectId}&sourceView=draft`, (api) => {
      vi.spyOn(api, 'listProjectJobs').mockRejectedValue(new Error('jobs unavailable'));
      vi.spyOn(api, 'listImportQueues').mockResolvedValue({ data: [runningQueue(draftBookId)] } as never);
    });

    expect(await screen.findByRole('article', { name: 'Книга III. Северный суд' })).toBeTruthy();
    expect(await screen.findByRole('dialog', { name: 'Загрузка книг' })).toBeTruthy();
    expect(screen.queryByText('Не удалось загрузить рукопись.')).toBeNull();
  });

  it('reopens a hidden active queue from the shelf header without queue mutations', async () => {
    let createImportQueue: ReturnType<typeof vi.spyOn> | undefined;
    let uploadImportQueueItem: ReturnType<typeof vi.spyOn> | undefined;
    let startImportQueue: ReturnType<typeof vi.spyOn> | undefined;
    let cancelImportQueue: ReturnType<typeof vi.spyOn> | undefined;
    renderShelf(`?projectId=${projectId}&sourceView=draft`, (api) => {
      vi.spyOn(api, 'listImportQueues').mockResolvedValue({ data: [runningQueue(draftBookId)] } as never);
      createImportQueue = vi.spyOn(api, 'createImportQueue');
      uploadImportQueueItem = vi.spyOn(api, 'uploadImportQueueItem');
      startImportQueue = vi.spyOn(api, 'startImportQueue');
      cancelImportQueue = vi.spyOn(api, 'cancelImportQueue');
    });

    const dialog = await screen.findByRole('dialog', { name: 'Загрузка книг' });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Скрыть очередь' }));

    expect(screen.queryByRole('dialog', { name: 'Загрузка книг' })).toBeNull();
    const reopen = await screen.findByRole('button', { name: 'Открыть очередь' });
    fireEvent.click(reopen);

    expect(await screen.findByRole('dialog', { name: 'Загрузка книг' })).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Открыть очередь' })).toBeNull();
    expect(createImportQueue).not.toHaveBeenCalled();
    expect(uploadImportQueueItem).not.toHaveBeenCalled();
    expect(startImportQueue).not.toHaveBeenCalled();
    expect(cancelImportQueue).not.toHaveBeenCalled();
  });

  it('reconciles Job and indexing status when a queue poll becomes failed', async () => {
    const jobId = 'job-queue-current';
    const failedJob = importJob(jobId, 'failed');
    const runningJob = { ...importJob(jobId, 'running'), canCancel: true } as ProjectJob;
    let terminal = false;
    let getJob: ReturnType<typeof vi.spyOn> | undefined;
    let getIndexingStatus: ReturnType<typeof vi.spyOn> | undefined;
    const view = renderShelf(`?projectId=${projectId}&bookId=${draftBookId}&sourceView=draft`, (api) => {
      vi.spyOn(api, 'listImportQueues').mockImplementation(async () => ({
        data: [terminal ? failedQueue(draftBookId, jobId) : runningQueue(draftBookId, jobId)],
      }) as never);
      const loadJob = api.getJob.bind(api);
      getJob = vi.spyOn(api, 'getJob').mockImplementation((id) => (
        id === jobId ? Promise.resolve(terminal ? failedJob : runningJob) : loadJob(id)
      ));
      getIndexingStatus = vi.spyOn(api, 'getIndexingStatus').mockImplementation(async (_id, options = {}) => {
        const status = indexingStatusForJob(terminal ? failedJob : runningJob, 'import');
        return options.bookId ? status as never : { ...status, steps: [] } as never;
      });
    });

    const card = await screen.findByRole('region', { name: 'Подготовка книги' });
    await waitFor(() => expect(getJob).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(getIndexingStatus?.mock.calls.length).toBeGreaterThanOrEqual(2));
    expect(within(card).getByText('Готовим «Том 4.epub» для чата')).toBeTruthy();
    getJob?.mockClear();
    getIndexingStatus?.mockClear();

    terminal = true;
    await act(async () => {
      await view.queryClient.refetchQueries({
        queryKey: manuscriptQueryKeys.importQueues(projectId),
        exact: true,
      });
    });

    const failedQueueDialog = await screen.findByRole('dialog', { name: 'Загрузка книг' });
    expect(within(failedQueueDialog).getByText('Очередь остановлена на книге №4')).toBeTruthy();
    expect(within(failedQueueDialog).getByRole('button', { name: 'Повторить' })).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Открыть очередь' })).toBeNull();
    await waitFor(() => expect(getJob).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(getIndexingStatus).toHaveBeenCalled());
    await waitFor(() => expect(card.getAttribute('data-tone')).toBe('failed'));
    expect(within(card).queryByRole('button', { name: 'Отменить обработку' })).toBeNull();
  });

  it.each([
    ['one file', ['Том 5.epub']],
    ['many files', ['Том 5.epub', 'Том 6.fb2']],
  ])('keeps %s local until transfer starts, then uses only the durable queue', async (_label, fileNames) => {
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
    const input = screen.getByLabelText('Файлы книг: FB2, EPUB или DOCX');
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

    const dialog = await screen.findByRole('dialog', { name: 'Загрузка книг' });
    expect(within(dialog).queryByRole('checkbox')).toBeNull();
    fileNames.forEach((name) => expect(screen.getByText(name)).toBeTruthy());
    expect(createImportQueue).not.toHaveBeenCalled();
    expect(uploadImportQueueItem).not.toHaveBeenCalled();
    expect(startImportQueue).not.toHaveBeenCalled();

    fireEvent.click(within(dialog).getByRole('button', { name: 'Загрузить' }));

    await waitFor(() => expect(createImportQueue).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(uploadImportQueueItem).toHaveBeenCalledTimes(fileNames.length));
    await waitFor(() => expect(startImportQueue).toHaveBeenCalledTimes(1));
    expect(importBookFile).not.toHaveBeenCalled();
  });

  it('selects the current queue book before a ready fallback', async () => {
    renderShelf(`?projectId=${projectId}&sourceView=draft`, (api) => {
      vi.spyOn(api, 'listImportQueues').mockResolvedValue({ data: [runningQueue(draftBookId)] } as never);
      vi.spyOn(api, 'getIndexingStatus').mockResolvedValue({
        draft: { exists: true, context: { state: 'processing', currentBook: { bookId }, retryableFailedBook: null } },
      } as never);
    });
    const article = await screen.findByRole('article', { name: 'Книга III. Северный суд' });
    await waitFor(() => expect(article.getAttribute('aria-current')).toBe('true'));
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
    await screen.findByText('В саге пока нет книг');
    const dialog = await screen.findByRole('dialog', { name: 'Загрузка книг' });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Скрыть очередь' }));

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

  it('keeps an explicit route book above queue and indexing defaults', async () => {
    renderShelf(`?projectId=${projectId}&bookId=${bookId}&sourceView=draft`, (api) => {
      vi.spyOn(api, 'listImportQueues').mockResolvedValue({ data: [runningQueue(draftBookId)] } as never);
      vi.spyOn(api, 'getIndexingStatus').mockResolvedValue({
        draft: { exists: true, context: { state: 'processing', currentBook: { bookId: draftBookId }, retryableFailedBook: null } },
      } as never);
    });

    const article = await screen.findByRole('article', { name: 'Книга II. Карта приливов' });
    await waitFor(() => expect(article.getAttribute('aria-current')).toBe('true'));
  });

  it('selects the indexing current book before an active Job book', async () => {
    renderShelf(`?projectId=${projectId}`, (api) => {
      vi.spyOn(api, 'getIndexingStatus').mockResolvedValue({
        published: { exists: true, context: { state: 'processing', currentBook: { bookId: draftBookId }, retryableFailedBook: null } },
      } as never);
      vi.spyOn(api, 'listProjectJobs').mockResolvedValue({
        data: [importJob('job-import-other', 'running', bookId)],
        meta: { hasMore: false, nextCursor: null },
      } as never);
    });

    const article = await screen.findByRole('article', { name: 'Книга III. Северный суд' });
    await waitFor(() => expect(article.getAttribute('aria-current')).toBe('true'));
  });

  it('selects the exact indexing-step Job book before the preferred ready book', async () => {
    const job = cardsJob('job-cards-route', 'running');
    renderShelf(`?projectId=${projectId}`, (api) => {
      vi.spyOn(api, 'getJob').mockResolvedValue(job);
      vi.spyOn(api, 'getIndexingStatus').mockResolvedValue({
        published: {
          exists: true,
          context: {
            state: 'processing',
            currentBook: { bookId: draftBookId, position: 1, total: 1 },
            retryableFailedBook: null,
          },
        },
        steps: [{ jobId: job.id, kind: 'cards', progress: null, sourceView: 'published', status: 'running' }],
      } as never);
    });

    const article = await screen.findByRole('article', { name: 'Книга III. Северный суд' });
    await waitFor(() => expect(article.getAttribute('aria-current')).toBe('true'));
  });

  it('opens an exact queue item status deep link', async () => {
    const queue = runningQueue(draftBookId);
    const view = renderShelf(`?projectId=${projectId}&sourceView=draft`, (api) => {
      vi.spyOn(api, 'listImportQueues').mockResolvedValue({ data: [queue] } as never);
    });

    const dialog = await screen.findByRole('dialog', { name: 'Загрузка книг' });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Подробнее: Книга III. Северный суд' }));

    expect(view.navigate).toHaveBeenLastCalledWith('/manuscript/books', expect.objectContaining({
      projectId,
      bookId: draftBookId,
    }));
    expect(view.navigate.mock.lastCall?.[1]).not.toHaveProperty('sourceView');
  });

  it('selects the exact book when the route changes on the mounted shelf', async () => {
    const view = renderShelf(`?projectId=${projectId}&bookId=${bookId}&sourceView=published`);

    const publishedBook = await screen.findByRole('article', { name: 'Книга II. Карта приливов' });
    await waitFor(() => expect(publishedBook.getAttribute('aria-current')).toBe('true'));

    view.rerenderWithRoute(`?projectId=${projectId}&bookId=${draftBookId}&sourceView=draft`);

    const draftBook = await screen.findByRole('article', { name: 'Книга III. Северный суд' });
    await waitFor(() => expect(draftBook.getAttribute('aria-current')).toBe('true'));
    expect(publishedBook.getAttribute('aria-current')).toBeNull();
  });

  it('hides the previous project while a changed project route hydrates', async () => {
    let holdProjectList = false;
    let releaseProjectList: (() => void) | undefined;
    const view = renderShelf(`?projectId=${projectId}&bookId=${draftBookId}&sourceView=draft`, (api) => {
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
    expect(screen.getAllByText('Хроники Белого порта').length).toBeGreaterThan(0);

    holdProjectList = true;
    view.rerenderWithRoute(`?projectId=${foreignProjectId}&sourceView=draft`);

    expect(screen.queryAllByRole('article')).toHaveLength(0);
    expect(screen.queryAllByText('Хроники Белого порта')).toHaveLength(0);
    expect(screen.queryByText(/^Сага ·/)).toBeNull();

    await waitFor(() => expect(releaseProjectList).toBeTypeOf('function'));
    await act(async () => releaseProjectList?.());
    expect(await screen.findByRole('heading', { name: 'Сага не найдена' })).toBeTruthy();
  });
});

describe('ManuscriptShelfPage book reading actions', () => {
  it('opens the first published chapter when the first chapter is draft', async () => {
    const view = renderShelf(`?projectId=${projectId}&bookId=${bookId}&bookMenu=open&sourceView=published`, (api) => {
      vi.spyOn(api, 'listChapters').mockResolvedValue({
        data: [
          { id: 'chapter-draft-first', title: 'Черновая глава', status: 'draft' },
          { id: 'chapter-published-second', title: 'Опубликованная глава', status: 'published' },
        ],
        meta: { hasMore: false, nextCursor: null },
      } as never);
    });

    await screen.findByRole('menu', { name: 'Действия книги' });
    view.navigate.mockClear();
    fireEvent.click(screen.getByRole('menuitem', { name: 'Открыть чтение' }));

    await waitFor(() => expect(view.navigate).toHaveBeenCalledWith('/manuscript/books', expect.objectContaining({
      bookId,
      chapterId: 'chapter-published-second',
      mode: 'read',
      projectId,
    })));
    expect(view.navigate).not.toHaveBeenCalledWith('/manuscript/books', expect.objectContaining({
      chapterId: 'chapter-draft-first',
      mode: 'read',
    }));
  });

  it('closes the menu without navigating when no chapter is published', async () => {
    const view = renderShelf(`?projectId=${projectId}&bookId=${bookId}&bookMenu=open&sourceView=published`, (api) => {
      vi.spyOn(api, 'listChapters').mockResolvedValue({
        data: [{ id: 'chapter-draft-only', title: 'Только черновая глава', status: 'draft' }],
        meta: { hasMore: false, nextCursor: null },
      } as never);
    });

    await screen.findByRole('menu', { name: 'Действия книги' });
    view.navigate.mockClear();
    fireEvent.click(screen.getByRole('menuitem', { name: 'Открыть чтение' }));

    await waitFor(() => expect(screen.queryByRole('menu', { name: 'Действия книги' })).toBeNull());
    expect(view.navigate).not.toHaveBeenCalledWith('/manuscript/books', expect.objectContaining({ mode: 'read' }));
    expect(await screen.findByText('В книге пока нет глав.')).toBeTruthy();
  });
});

function renderShelf(search: string, configureApi?: (api: ShelfApi) => void) {
  const api = createMockApiClient();
  api.setScenario({ actorRole: 'editor', preset: 'normal' });
  vi.spyOn(api, 'getAgentTimeline').mockResolvedValue({ events: [], eof: true, nextCursor: null } as never);
  configureApi?.(api);
  const navigate = vi.fn();
  const queryClient = new QueryClient({
    defaultOptions: { mutations: { retry: false }, queries: { retry: false } },
  });
  const page = (nextSearch: string) => (
    <QueryClientProvider client={queryClient}>
      <ManuscriptShelfPage
        api={api}
        navigate={navigate}
        onLogout={vi.fn()}
        route={parseRoute(`http://localhost/manuscript/books${nextSearch}`)}
        userName="Мира Волкова"
      />
    </QueryClientProvider>
  );
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
    nextBookOrder: 4,
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
      detectedBookOrder: 4,
      confirmedBookOrder: 4,
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
