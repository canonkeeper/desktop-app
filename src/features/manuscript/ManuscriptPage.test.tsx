import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../api/mock';
import { ApiStatusError } from '../../shared/api';
import { parseRoute } from '../../shared/navigation/app-route';
import { FakeWebSocket } from '../../test/FakeWebSocket';
import { ManuscriptPage } from './ManuscriptPage';

vi.mock('@tanstack/react-router', async () => ({
  ...await vi.importActual<typeof import('@tanstack/react-router')>('@tanstack/react-router'),
  useBlocker: vi.fn(),
}));

type ManuscriptTestApi = ReturnType<typeof createMockApiClient>;

function makeRoute(search = '') {
  return parseRoute(new URL(`http://localhost/manuscript/books${search}`));
}

function renderManuscript(search = '', configureApi?: (api: ManuscriptTestApi) => void) {
  const api = createMockApiClient();
  configureApi?.(api);
  const navigate = vi.fn();
  const onLogout = vi.fn();
  const queryClient = new QueryClient({
    defaultOptions: {
      mutations: { retry: false },
      queries: { retry: false },
    },
  });

  const view = (nextSearch: string) => (
    <QueryClientProvider client={queryClient}>
      <ManuscriptPage api={api} navigate={navigate} onLogout={onLogout} route={makeRoute(nextSearch)} userName="Мира Волкова" />
    </QueryClientProvider>
  );
  const renderResult = render(view(search));

  return { api, navigate, onLogout, queryClient, rerenderWithRoute: (nextSearch: string) => renderResult.rerender(view(nextSearch)) };
}

describe('ManuscriptPage', () => {
  beforeEach(() => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1440 });
    vi.stubGlobal('WebSocket', FakeWebSocket);
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => undefined);
  });

  afterEach(() => {
    FakeWebSocket.reset();
    vi.unstubAllGlobals();
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it('opens one bounded recently deleted surface without preloading it', async () => {
    const { api } = renderManuscript();
    const listProjects = vi.spyOn(api, 'listProjects');

    fireEvent.click(await screen.findByRole('button', { name: 'Хроники Белого порта' }));
    const trigger = await screen.findByRole('button', { name: 'Недавно удалённые' });
    expect(listProjects.mock.calls.some(([options]) => (options as { state?: string } | undefined)?.state === 'deleted')).toBe(false);

    fireEvent.click(trigger);

    expect(await screen.findByRole('dialog', { name: 'Недавно удалённые' })).toBeTruthy();
    await waitFor(() => expect(listProjects.mock.calls.some(([options]) => (options as { state?: string } | undefined)?.state === 'deleted')).toBe(true));
    fireEvent.click(screen.getByRole('button', { name: 'Закрыть недавно удалённые' }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Недавно удалённые' })).toBeNull());
    expect(document.activeElement).toBe(trigger);
  });

  it('открывает Chat из sidebar проекта без возврата Agent controls', async () => {
    const { navigate } = renderManuscript('?projectId=project-white-port&bookId=book-02');

    fireEvent.click(screen.getByRole('button', { name: 'Чат' }));
    expect(navigate).toHaveBeenCalledWith('/chat', { projectId: 'project-white-port' });

    await screen.findByText('Книга II. Карта приливов');
    expect(screen.queryByRole('button', { name: 'Обзор' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Материалы' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Читалка' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Агент' })).toBeNull();
  });

  it('tablet rail keeps Books and Chat in the project context without a text version', async () => {
    const { navigate } = renderManuscript('?projectId=project-white-port&bookId=book-02&sourceView=draft');
    await screen.findByText('Книга II. Карта приливов');
    const rail = within(screen.getByLabelText('Планшетная навигация'));
    expect(rail.getAllByRole('button', { hidden: true }).map((button) => button.getAttribute('aria-label')))
      .toEqual(['Мои книги', 'Чтение', 'Чат', 'Карта саги', 'Письмо', 'Рисование', 'Галерея новое', 'Заметки']);
    const books = rail.getByLabelText('Мои книги');
    expect(books.getAttribute('aria-current')).toBe('page');
    expect(screen.getAllByRole('button', { name: 'Глобальный поиск' })).toHaveLength(1);

    fireEvent.click(rail.getByLabelText('Чат'));
    expect(navigate).toHaveBeenLastCalledWith('/chat', { projectId: 'project-white-port' });
    fireEvent.click(books);
    expect(navigate).toHaveBeenLastCalledWith('/manuscript/books', { projectId: 'project-white-port', bookId: 'book-02' });
  });

  it.each(['owner', 'viewer'] as const)('reads the published text on the shelf of an %s without a version control', async (actorRole) => {
    const { api } = renderManuscript('?projectId=project-white-port&bookId=book-02&sourceView=draft', (client) => {
      client.setScenario({ preset: 'normal', actorRole });
      vi.spyOn(client, 'getIndexingStatus');
    });

    await screen.findByText('Книга II. Карта приливов');
    await waitFor(() => expect(api.getIndexingStatus).toHaveBeenCalledWith(
      'project-white-port',
      expect.objectContaining({ bookId: 'book-02', sourceView: 'published' }),
    ));
    expect(vi.mocked(api.getIndexingStatus).mock.calls.some(([, options]) => options?.sourceView === 'draft')).toBe(false);
    expect(screen.queryByRole('button', { name: /^Версия текста/ })).toBeNull();
    expect(screen.queryByText(/Черновик|Опубликован/)).toBeNull();
  });

  it('opens the reader from book cards and keeps the anchored menu lightweight', async () => {
    const { api, navigate } = renderManuscript();
    const updateBook = vi.spyOn(api, 'updateBook');

    fireEvent.click(await screen.findByRole('button', { name: 'Открыть чтение Книга II. Карта приливов' }));

    await waitFor(() =>
      expect(navigate).toHaveBeenCalledWith(
        '/manuscript/books',
        expect.objectContaining({
          bookId: 'book-02',
          chapterId: 'chapter-12',
          mode: 'read',
          projectId: 'project-white-port',
        }),
      ),
    );
    expect(navigate.mock.lastCall?.[1]).not.toHaveProperty('sourceView');

    fireEvent.click(screen.getByRole('button', { name: 'Действия книги Книга II. Карта приливов' }));
    const menu = await screen.findByRole('menu', { name: 'Действия книги' });
    expect(menu).toBeTruthy();
    expect(document.body.style.overflow).toBe('');

    fireEvent.click(screen.getByRole('menuitem', { name: 'Изменить обложку' }));

    const coverEditor = await screen.findByRole('dialog', { name: 'Обложка книги' });
    expect(updateBook).not.toHaveBeenCalled();
    fireEvent.click(within(coverEditor).getByRole('button', { name: 'Сохранить цвет' }));
    expect(await screen.findByText('Обложка книги обновлена')).toBeTruthy();
    expect(updateBook).toHaveBeenCalledWith('book-02', expect.objectContaining({ coverColor: expect.any(String) }));
    await waitFor(() => expect(document.body.style.overflow).toBe(''));
  });

  // specs/2026-11-05-book-export.md: the page says what has been downloaded, and a status is what a screen reader hears.
  it('saves the file of a book under its name and says so', async () => {
    const createObjectURL = vi.fn((_blob: Blob) => 'blob:book');
    Object.assign(URL, { createObjectURL, revokeObjectURL: vi.fn() });
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function saved(this: HTMLAnchorElement) {
      expect(this.download).toBe('Книга II. Карта приливов.docx');
      expect(this.href).toBe('blob:book');
    });
    const { api } = renderManuscript();
    const getBookExport = vi.spyOn(api, 'getBookExport');

    fireEvent.click(await screen.findByRole('button', { name: 'Действия книги Книга II. Карта приливов' }));
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Скачать в Word' }));

    await waitFor(() => expect(screen.getByText('Скачано: Книга II. Карта приливов.docx').getAttribute('role')).toBe('status'));
    expect(getBookExport).toHaveBeenCalledWith('book-02', 'docx');
    expect(click).toHaveBeenCalledTimes(1);
    expect(createObjectURL).toHaveBeenCalledTimes(1);
    click.mockRestore();
  });

  it('says why a file was not given and saves nothing', async () => {
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click');
    const { api } = renderManuscript();
    vi.spyOn(api, 'getBookExport').mockRejectedValueOnce(new ApiStatusError(422, {
      code: 'book_export.empty', detail: 'no text', requestId: 'req', status: 422, title: 'Unprocessable Entity', type: 'about:blank',
    }, new Response(null, { status: 422 })));

    fireEvent.click(await screen.findByRole('button', { name: 'Действия книги Книга II. Карта приливов' }));
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Скачать в FB2' }));

    expect(await screen.findByText('В этой книге пока нет текста: скачивать нечего.')).toBeTruthy();
    expect(click).not.toHaveBeenCalled();
    click.mockRestore();
  });

  it('closes the book action menu when clicking outside and exposes the read action without editing', async () => {
    renderManuscript();

    fireEvent.click(await screen.findByRole('button', { name: 'Действия книги Книга II. Карта приливов' }));
    const menu = await screen.findByRole('menu', { name: 'Действия книги' });
    expect(within(menu).getByRole('button', { name: 'Закрыть меню' })).toBeTruthy();
    expect(within(menu).getByRole('menuitem', { name: 'Открыть чтение' })).toBeTruthy();
    expect(within(menu).queryByRole('menuitem', { name: 'Новая глава' })).toBeNull();
    expect(within(menu).queryByRole('menuitem', { name: 'Открыть редактор' })).toBeNull();
    fireEvent.click(within(menu).getByRole('button', { name: 'Закрыть меню' }));
    await waitFor(() => expect(screen.queryByRole('menu', { name: 'Действия книги' })).toBeNull());

    fireEvent.click(screen.getByRole('button', { name: 'Действия книги Книга II. Карта приливов' }));
    const nextMenu = await screen.findByRole('menu', { name: 'Действия книги' });
    fireEvent.click(within(nextMenu).getByRole('button', { name: 'Закрыть меню' }));
    await waitFor(() => expect(screen.queryByRole('menu', { name: 'Действия книги' })).toBeNull());

    fireEvent.click(screen.getByRole('button', { name: 'Действия книги Книга II. Карта приливов' }));
    await screen.findByRole('menu', { name: 'Действия книги' });
    fireEvent.pointerDown(document.body);
    await waitFor(() => expect(screen.queryByRole('menu', { name: 'Действия книги' })).toBeNull());
  });

  it('keeps the action menu open when selecting a different book', async () => {
    renderManuscript();

    await screen.findByRole('button', { name: 'Действия книги Книга II. Карта приливов' });
    fireEvent.click(screen.getByRole('button', { name: 'Действия книги Книга I. Туманный берег' }));

    const menu = await screen.findByRole('menu', { name: 'Действия книги' });
    expect(within(menu).getByText('Книга I. Туманный берег')).toBeTruthy();
  });

  it('switches between shelf and reader and sends saved draft links to the reader', async () => {
    const { navigate, rerenderWithRoute } = renderManuscript();

    expect(await screen.findByText('Книга II. Карта приливов')).toBeTruthy();

    rerenderWithRoute('?mode=read&projectId=project-white-port&bookId=book-02&chapterId=chapter-12&paragraphId=p-12-03');
    expect(await screen.findByRole('heading', { level: 1, name: 'Глава 12. Белый порт' })).toBeTruthy();

    rerenderWithRoute('');
    expect(await screen.findByText('Книга III. Северный суд')).toBeTruthy();

    rerenderWithRoute('?mode=draft&projectId=project-white-port&bookId=book-02&chapterId=chapter-16');
    await waitFor(() => expect(navigate).toHaveBeenLastCalledWith('/manuscript/books', {
      mode: 'read', projectId: 'project-white-port', bookId: 'book-02', chapterId: 'chapter-16',
    }, { replace: true }));
  });

  it('keeps manuscript shelf read-only for project viewers', async () => {
    const { api, navigate } = renderManuscript('?bookId=book-02', (client) => {
      client.setScenario({ preset: 'normal', actorRole: 'viewer' });
    });
    const listProjects = vi.spyOn(api, 'listProjects');

    await screen.findByRole('button', { name: 'Открыть чтение Книга II. Карта приливов' });
    const importDropzone = screen.getByRole('button', { name: /Перетащите FB2/ });
    expect(importDropzone).toHaveProperty('disabled', true);
    expect(screen.queryByLabelText('Файлы книг: FB2, EPUB или DOCX')).toBeNull();
    expect(screen.queryByRole('button', { name: 'Новая книга' })).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Хроники Белого порта' }));
    const projectMenu = await screen.findByRole('dialog', { name: 'Выбор саги' });
    const deletedSourcesButton = within(projectMenu).getByRole('button', { name: 'Недавно удалённые' }) as HTMLButtonElement;
    expect(deletedSourcesButton.disabled).toBe(true);
    expect(listProjects.mock.calls.some(([options]) => (options as { state?: string } | undefined)?.state === 'deleted')).toBe(false);

    fireEvent.click(screen.getByRole('button', { name: 'Открыть чтение Книга II. Карта приливов' }));
    await waitFor(() =>
      expect(navigate).toHaveBeenCalledWith(
        '/manuscript/books',
        expect.objectContaining({ bookId: 'book-02', mode: 'read', projectId: 'project-white-port' }),
      ),
    );

    fireEvent.click(screen.getByRole('button', { name: 'Действия книги Книга II. Карта приливов' }));
    const menu = await screen.findByRole('menu', { name: 'Действия книги' });
    expect(within(menu).getByRole('menuitem', { name: 'Открыть чтение' })).toBeTruthy();
    expect(within(menu).queryByRole('menuitem', { name: 'Открыть редактор' })).toBeNull();
    expect(within(menu).queryByRole('menuitem', { name: 'Новая глава' })).toBeNull();
    expect(within(menu).queryByRole('menuitem', { name: 'Удалить книгу' })).toBeNull();
  });

  it('runs import and delete book transitions', async () => {
    const { api } = renderManuscript();
    const cancelImportQueue = vi.spyOn(api, 'cancelImportQueue');
    const uploadImportQueueItem = vi.spyOn(api, 'uploadImportQueueItem');

    await screen.findByText('Книга II. Карта приливов');
    const importFile = new File(['<book/>'], 'том 5.epub', { type: 'application/epub+zip' });
    fireEvent.change(screen.getByLabelText('Файлы книг: FB2, EPUB или DOCX'), { target: { files: [importFile] } });
    expect(await screen.findByRole('dialog', { name: 'Загрузка книг' })).toBeTruthy();
    expect(uploadImportQueueItem).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Загрузить' }));
    await waitFor(() => expect(uploadImportQueueItem).toHaveBeenCalledTimes(1));
    fireEvent.click(screen.getByRole('button', { name: 'Скрыть очередь' }));
    expect(cancelImportQueue).not.toHaveBeenCalled();
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Загрузка книг' })).toBeNull());
    expect(screen.queryByRole('button', { name: 'Новая книга' })).toBeNull();
    fireEvent.click(await screen.findByRole('button', { name: 'Действия книги Книга I. Туманный берег' }));
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Удалить книгу' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Удалить' }));

    expect(await screen.findByText(/Книга «Книга I\. Туманный берег» удалена/)).toBeTruthy();
  });

  it.each([
    { browserMime: '', mimeKind: 'empty' },
    { browserMime: 'application/zip', mimeKind: 'generic' },
  ])('opens a DOCX manuscript with $mimeKind MIME in the confirmation queue', async ({ browserMime }) => {
    let importBookFileSpy!: ReturnType<typeof vi.spyOn>;
    renderManuscript('', (client) => {
      const importBookFile = client.importBookFile.bind(client);
      importBookFileSpy = vi.spyOn(client, 'importBookFile').mockImplementation(importBookFile);
    });

    await screen.findByText('Книга II. Карта приливов');
    const input = screen.getByLabelText('Файлы книг: FB2, EPUB или DOCX') as HTMLInputElement;
    expect(input.accept).toBe('.fb2,.epub,.docx');
    const sourceFile = new File(['word-document'], 'том 5.docx', {
      lastModified: 1_723_654_321_000,
      type: browserMime,
    });

    fireEvent.change(input, {
      target: {
        files: [sourceFile],
      },
    });

    expect(await screen.findByRole('dialog', { name: 'Загрузка книг' })).toBeTruthy();
    expect(importBookFileSpy).not.toHaveBeenCalled();
  });

  it('keeps import and delete failures inside the manuscript shelf', async () => {
    const { api } = renderManuscript();
    const cancelImportQueue = vi.spyOn(api, 'cancelImportQueue');
    const uploadImportQueueItem = vi.spyOn(api, 'uploadImportQueueItem');

    await screen.findByText('Книга II. Карта приливов');
    vi.spyOn(api, 'createImportQueue').mockRejectedValueOnce(new Error('Не удалось создать очередь импорта.'));
    const importBookFile = vi.spyOn(api, 'importBookFile');
    const failedImportFile = new File(['<book/>'], 'northern-court.epub', { type: 'application/epub+zip' });
    fireEvent.change(screen.getByLabelText('Файлы книг: FB2, EPUB или DOCX'), { target: { files: [failedImportFile] } });

    expect(await screen.findByRole('dialog', { name: 'Загрузка книг' })).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Загрузить' }));
    expect(await screen.findByText('Не удалось создать очередь импорта.')).toBeTruthy();
    expect(importBookFile).not.toHaveBeenCalled();
    expect(screen.getByText('Книга II. Карта приливов')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Закрыть очередь' }));
    expect(cancelImportQueue).not.toHaveBeenCalled();
    expect(uploadImportQueueItem).not.toHaveBeenCalled();
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Загрузка книг' })).toBeNull());

    vi.spyOn(api, 'deleteBook').mockRejectedValueOnce(new Error('Не удалось удалить книгу.'));
    fireEvent.click(screen.getByRole('button', { name: 'Действия книги Книга II. Карта приливов' }));
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Удалить книгу' }));
    const deleteDialog = await screen.findByRole('dialog', { name: 'Удалить книгу' });
    fireEvent.click(within(deleteDialog).getByRole('button', { name: 'Удалить' }));

    expect(await screen.findByText('Не удалось удалить книгу.')).toBeTruthy();
    expect(screen.getByRole('dialog', { name: 'Удалить книгу' })).toBeTruthy();
    expect(screen.getByText('Книга II. Карта приливов')).toBeTruthy();
  });

  it('keeps manuscript mutations unavailable until a project exists', async () => {
    renderManuscript('', (api) => {
      api.setScenario({ preset: 'normal', actorRole: 'non_member' });
    });

    // R2.1.1: without a saga the shelf asks to name one; the books come in the next step.
    expect(await screen.findByRole('heading', { name: 'Добро пожаловать в Canon Keeper' })).toBeTruthy();
    expect(screen.getByRole('heading', { level: 1, name: 'Добро пожаловать' })).toBeTruthy();
    expect(screen.queryByText(/Примите приглашение/)).toBeNull();
    expect(screen.queryByText('Проект загружается')).toBeNull();
    expect(screen.queryByText('В саге пока нет книг')).toBeNull();
    expect(screen.queryByRole('button', { name: /Перетащите FB2, EPUB или DOCX/ })).toBeNull();
    expect(screen.queryByLabelText('Файлы книг: FB2, EPUB или DOCX')).toBeNull();
    expect(screen.queryByRole('button', { name: 'Новая книга' })).toBeNull();
  });

  it('lets a newly registered writer create and select their first project from the shelf', async () => {
    const { api, navigate, rerenderWithRoute } = renderManuscript('', (client) => {
      client.setScenario({ preset: 'normal', actorRole: 'non_member' });
    });
    const createProject = vi.spyOn(api, 'createProject');

    fireEvent.change(await screen.findByRole('textbox', { name: 'Название саги' }), { target: { value: 'Сага северного ветра' } });
    fireEvent.click(screen.getByRole('button', { name: 'Создать сагу' }));

    await waitFor(() => expect(createProject).toHaveBeenCalledWith({ title: 'Сага северного ветра' }));
    await waitFor(() => expect(navigate).toHaveBeenCalledWith('/manuscript/books', expect.objectContaining({ projectId: 'project-2' })));
    rerenderWithRoute('?projectId=project-2');
    expect(await screen.findByRole('heading', { name: 'Сага северного ветра' })).toBeTruthy();
    // The shelf of the new saga confirms it in «Три шага» and asks for the first book (R2.1.1).
    expect(await screen.findByText('Сага «Сага северного ветра» создана')).toBeTruthy();
    expect(screen.getByRole('heading', { name: 'Загрузите книги' })).toBeTruthy();
    await waitFor(() => expect(screen.getByRole('button', { name: 'Загрузить файл' })).toHaveProperty('disabled', false));
  });

  it('shows search fallbacks in context', async () => {
    const { api } = renderManuscript();

    await screen.findByText('Книга II. Карта приливов');

    vi.spyOn(api, 'searchProject').mockRejectedValueOnce(new Error('Поиск временно недоступен.'));

    fireEvent.click(screen.getByRole('button', { name: 'Глобальный поиск' }));
    fireEvent.change(screen.getByLabelText('Запрос поиска'), { target: { value: 'Белый порт' } });
    fireEvent.click(screen.getByRole('button', { name: 'Найти' }));

    expect(await screen.findByText('Поиск временно недоступен.')).toBeTruthy();
  });

  it('keeps book failures on the shelf, routes project selection and delegates logout to App', async () => {
    const { api, navigate, onLogout } = renderManuscript('', (client) => {
      const listProjects = client.listProjects.bind(client);
      vi.spyOn(client, 'listProjects').mockImplementation(async (...args) => {
        const response = await listProjects(...args);
        return { ...response, data: [...response.data, { ...response.data[0]!, id: 'project-north-wind', title: 'Сага северного ветра' }] };
      });
    });

    await screen.findByText('Книга II. Карта приливов');
    vi.spyOn(api, 'getBook').mockRejectedValueOnce(new Error('Не удалось открыть книгу.'));
    navigate.mockClear();
    fireEvent.click(screen.getByRole('button', { name: 'Открыть чтение Книга II. Карта приливов' }));

    expect(await screen.findByText('Не удалось открыть книгу.')).toBeTruthy();
    expect(navigate).not.toHaveBeenCalled();

    navigate.mockClear();
    fireEvent.click(screen.getByRole('button', { name: 'Хроники Белого порта' }));
    const projectMenu = await screen.findByRole('dialog', { name: 'Выбор саги' });
    expect(within(projectMenu).queryByRole('button', { name: 'Хроники Белого порта' })).toBeNull();
    fireEvent.click(within(projectMenu).getByRole('button', { name: 'Сага северного ветра' }));

    expect(navigate).toHaveBeenCalledWith('/manuscript/books', { projectId: 'project-north-wind' });

    fireEvent.click(screen.getByRole('button', { name: 'Аккаунт' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Выйти' }));

    await waitFor(() => expect(onLogout).toHaveBeenCalledTimes(1));
  });
});
