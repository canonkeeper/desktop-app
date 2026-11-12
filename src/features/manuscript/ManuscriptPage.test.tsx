import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../api/mock';
import { ApiStatusError } from '../../shared/api';
import { parseRoute } from '../../shared/navigation/app-route';
import { FakeWebSocket } from '../../test/FakeWebSocket';
import { InShell } from '../../app/shell/AppShell.test-helpers';
import { ManuscriptPage } from './ManuscriptPage';
import { ToastsProvider } from '../../ui/feedback/Toast';

vi.mock('@tanstack/react-router', async () => ({
  ...await vi.importActual<typeof import('@tanstack/react-router')>('@tanstack/react-router'),
  useBlocker: vi.fn(() => ({ status: 'idle' })),
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

  // The shelf stands in the shell of the root (specs/2026-11-10-ux2-shell-core.md SC1.1).
  const view = (nextSearch: string) => {
    const route = makeRoute(nextSearch);
    const page = <ManuscriptPage api={api} navigate={navigate} route={route} />;
    return (
      <QueryClientProvider client={queryClient}><ToastsProvider>
        <InShell api={api} navigate={navigate} onLogout={onLogout} route={route}>{page}</InShell>
      </ToastsProvider></QueryClientProvider>
    );
  };
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

  // specs/2026-11-10-ux2-shell-core.md SC2.3: «Недавно удалённые» is an item of the sections, the window of the shell.
  it('opens one bounded recently deleted surface without preloading it', async () => {
    const { api } = renderManuscript();
    const listRecentlyDeleted = vi.spyOn(api, 'listRecentlyDeleted');

    await screen.findByRole('button', { name: 'Сага: Хроники Белого порта' });
    const trigger = within(screen.getByRole('navigation', { name: 'Разделы' })).getByRole('button', { name: 'Недавно удалённые' });
    expect(listRecentlyDeleted).not.toHaveBeenCalled();

    fireEvent.click(trigger);

    expect(await screen.findByRole('dialog', { name: 'Недавно удалённые' })).toBeTruthy();
    await waitFor(() => expect(listRecentlyDeleted).toHaveBeenCalledTimes(1));
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

  it('keeps «Книги» and «Чат» of the sections in the saga of the shelf, without «Чтение»', async () => {
    const { navigate } = renderManuscript('?projectId=project-white-port&bookId=book-02&sourceView=draft');
    await screen.findByText('Книга II. Карта приливов');
    const sections = within(screen.getByRole('navigation', { name: 'Разделы' }));
    expect(sections.getAllByRole('button').map((button) => button.getAttribute('aria-label') ?? button.textContent))
      .toEqual(['Книги', 'Карта', 'Что помнит помощник', 'Чат', 'Письмо', 'Рисунки', 'Заметки', 'Недавно удалённые']);
    const books = sections.getByRole('button', { name: 'Книги' });
    expect(books.getAttribute('aria-current')).toBe('page');
    expect(screen.getAllByRole('button', { name: 'Поиск по книгам' })).toHaveLength(1);

    fireEvent.click(sections.getByRole('button', { name: 'Чат' }));
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
    await waitFor(() => expect(api.getIndexingStatus).toHaveBeenCalledWith('project-white-port', expect.anything()));
    expect(vi.mocked(api.getIndexingStatus).mock.calls.some(([, options]) => options?.sourceView === 'draft')).toBe(false);
    expect(screen.queryByRole('button', { name: /^Версия текста/ })).toBeNull();
    expect(screen.queryByText(/Черновик|Опубликован/)).toBeNull();
  });

  it('opens the reader from book cards and keeps the anchored menu lightweight', async () => {
    const { api, navigate } = renderManuscript();
    const updateBook = vi.spyOn(api, 'updateBook');

    // specs/2026-11-11-ux7-shelf.md SF1.1: the card is one link; without a place of reading the reader opens the first chapter.
    fireEvent.click(await screen.findByRole('link', { name: 'Читать «Книга II. Карта приливов»' }));

    await waitFor(() =>
      expect(navigate).toHaveBeenCalledWith(
        '/manuscript/books',
        expect.objectContaining({ bookId: 'book-02', mode: 'read', projectId: 'project-white-port' }),
      ),
    );
    expect(navigate.mock.lastCall?.[1]).not.toHaveProperty('sourceView');
    expect(navigate.mock.lastCall?.[1]).toMatchObject({ chapterId: undefined });

    fireEvent.click(screen.getByRole('button', { name: 'Действия книги Книга II. Карта приливов' }));
    const menu = await screen.findByRole('menu', { name: 'Действия книги' });
    expect(menu).toBeTruthy();
    expect(document.body.style.overflow).toBe('');

    fireEvent.click(screen.getByRole('menuitem', { name: 'Обложка…' }));

    const coverEditor = await screen.findByRole('dialog', { name: 'Обложка «Книга II. Карта приливов»' });
    expect(updateBook).not.toHaveBeenCalled();
    fireEvent.click(within(coverEditor).getByRole('button', { name: 'Охра' }));
    fireEvent.click(within(coverEditor).getByRole('button', { name: 'Сохранить' }));
    expect(await screen.findByText('Обложка «Книга II. Карта приливов» сохранена')).toBeTruthy();
    expect(updateBook).toHaveBeenCalledWith('book-02', { coverColor: '#8a6500' });
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
    const exportBook = api.getBookExport.bind(api);
    const getBookExport = vi.spyOn(api, 'getBookExport');
    let release!: () => void;
    const held = new Promise<void>((resolve) => { release = resolve; });
    getBookExport.mockImplementation(async (...args) => { await held; return exportBook(...args); });

    fireEvent.click(await screen.findByRole('button', { name: 'Действия книги Книга II. Карта приливов' }));
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Скачать книгу…' }));
    // specs/2026-11-11-ux9-shelf-chat.md SC1.5: one window — the kind of the file, what it holds and what it does not.
    const window = await screen.findByRole('dialog', { name: 'Скачать книгу «Книга II. Карта приливов»' });
    const kinds = within(window).getByRole('group', { name: 'Какой файл скачать?' });
    expect(within(kinds).getAllByRole('radio').map((radio) => radio.closest('label')!.textContent)).toEqual([
      'Word (.docx) — для Word и Google Документов', 'EPUB — для читалок и телефона', 'FB2 — для читалок',
    ]);
    expect(within(window).getByRole('radio', { name: /^Word/u })).toHaveProperty('checked', true);
    expect(within(window).getByText(/В файле — главы по порядку, курсив и жирный, обложка\./u)).toBeTruthy();
    // specs/2026-11-12-ux10-book-pictures.md BP3.1: the drawings of the book go with it unless the author says otherwise.
    expect(await within(window).findByRole('radio', { name: /^С рисунками \(\d+\)$/u })).toHaveProperty('checked', true);
    fireEvent.click(within(window).getByRole('button', { name: 'Скачать' }));

    // While the file is made: one line of a long work, no term the server did not give (specs/2026-11-10-ux2-patterns-states.md PS3.3).
    const work = await screen.findByRole('region', { name: 'Готовим файл книги «Книга II. Карта приливов»' });
    expect(within(work).getByText('Готовим файл книги «Книга II. Карта приливов»')).toBeTruthy();
    expect(within(work).queryByRole('progressbar')).toBeNull();
    release();
    // The result is a line of the one area of the toasts (PA1.1).
    await waitFor(() => expect(screen.getByText('Скачано: Книга II. Карта приливов.docx').closest('[role="status"]')).toBeTruthy());
    expect(screen.queryByRole('region', { name: 'Готовим файл книги «Книга II. Карта приливов»' })).toBeNull();
    expect(getBookExport).toHaveBeenCalledWith('book-02', 'docx', { drawings: true });
    expect(click).toHaveBeenCalledTimes(1);
    expect(createObjectURL).toHaveBeenCalledTimes(1);
    click.mockRestore();
  });

  // specs/2026-11-12-ux10-book-pictures.md BP3.1, BP3.3: the number of the drawings comes from the server as the window
  // opens; under «С рисунками» the file says where they stand and that a file with them may be too large to give back;
  // «Без рисунков» asks the file without the parameter.
  it('offers the drawings of a book in its file, by the number of the server, and leaves them out when asked', async () => {
    const { api } = renderManuscript();
    const getBookExport = vi.spyOn(api, 'getBookExport');
    const counts = vi.spyOn(api, 'getBookDrawingCounts').mockResolvedValue({ data: [{ bookId: 'book-02', drawings: 2 }, { bookId: 'book-01', drawings: 5 }] });
    fireEvent.click(await screen.findByRole('button', { name: 'Действия книги Книга II. Карта приливов' }));
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Скачать книгу…' }));
    const window = await screen.findByRole('dialog', { name: 'Скачать книгу «Книга II. Карта приливов»' });
    const choice = await within(window).findByRole('group', { name: 'Рисунки' });
    expect(within(choice).getAllByRole('radio').map((radio) => radio.closest('label')!.textContent)).toEqual(['С рисунками (2)', 'Без рисунков']);
    expect(within(choice).getByRole('radio', { name: 'С рисунками (2)' })).toHaveProperty('checked', true);
    expect(within(window).getByText('Рисунки встанут там же, где в книге. Файл будет больше.')).toBeTruthy();
    expect(within(window).getByText('Для «Новой версии из файла» лучше скачать без рисунков: файл с рисунками может быть больше 50 МБ.')).toBeTruthy();
    expect(within(window).queryByText(/Рисунки в файл не входят/u)).toBeNull();
    expect(counts).toHaveBeenCalledWith('project-white-port', expect.anything());

    fireEvent.click(within(choice).getByRole('radio', { name: 'Без рисунков' }));
    expect(within(window).queryByText(/Рисунки встанут/u)).toBeNull();
    expect(within(window).queryByText(/50 МБ/u)).toBeNull();
    fireEvent.click(within(window).getByRole('button', { name: 'Скачать' }));
    await waitFor(() => expect(getBookExport).toHaveBeenCalledWith('book-02', 'docx'));
  });

  // BP3.1: a book where no drawing stands has no choice: the window says where drawings are put in a book.
  it('says where the drawings of a book are put when it has none', async () => {
    const { api } = renderManuscript();
    const getBookExport = vi.spyOn(api, 'getBookExport');
    vi.spyOn(api, 'getBookDrawingCounts').mockResolvedValue({ data: [{ bookId: 'book-01', drawings: 5 }] });
    fireEvent.click(await screen.findByRole('button', { name: 'Действия книги Книга II. Карта приливов' }));
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Скачать книгу…' }));
    const window = await screen.findByRole('dialog', { name: 'Скачать книгу «Книга II. Карта приливов»' });
    expect(await within(window).findByText('В этой книге нет рисунков — их ставят в «Рисунках»: «Поставить в книгу…».')).toBeTruthy();
    expect(within(window).queryByRole('group', { name: 'Рисунки' })).toBeNull();
    fireEvent.click(within(window).getByRole('button', { name: 'Скачать' }));
    await waitFor(() => expect(getBookExport).toHaveBeenCalledWith('book-02', 'docx'));
  });

  it('says why a file was not given and saves nothing', async () => {
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click');
    const { api } = renderManuscript();
    vi.spyOn(api, 'getBookExport').mockRejectedValueOnce(new ApiStatusError(422, {
      code: 'book_export.empty', detail: 'no text', requestId: 'req', status: 422, title: 'Unprocessable Entity', type: 'about:blank',
    }, new Response(null, { status: 422 })));

    fireEvent.click(await screen.findByRole('button', { name: 'Действия книги Книга II. Карта приливов' }));
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Скачать книгу…' }));
    const window = await screen.findByRole('dialog', { name: /^Скачать книгу/u });
    fireEvent.click(within(window).getByRole('radio', { name: /^FB2/u }));
    fireEvent.click(within(window).getByRole('button', { name: 'Скачать' }));

    expect(await screen.findByText('В этой книге пока нет текста: скачивать нечего.')).toBeTruthy();
    expect(click).not.toHaveBeenCalled();
    click.mockRestore();
  });

  it('closes the book action menu when clicking outside and exposes the read action without editing', async () => {
    renderManuscript();

    fireEvent.click(await screen.findByRole('button', { name: 'Действия книги Книга II. Карта приливов' }));
    const menu = await screen.findByRole('menu', { name: 'Действия книги' });
    // × of the menu is for a pointer: the menu holds only its items, Esc closes it (aria-required-children).
    expect(within(menu).getByRole('button', { name: 'Закрыть меню', hidden: true }).tabIndex).toBe(-1);
    expect(within(menu).getByRole('menuitem', { name: 'Читать с начала' })).toBeTruthy();
    expect(within(menu).queryByRole('menuitem', { name: 'Новая глава' })).toBeNull();
    expect(within(menu).queryByRole('menuitem', { name: 'Открыть редактор' })).toBeNull();
    fireEvent.click(within(menu).getByRole('button', { name: 'Закрыть меню', hidden: true }));
    await waitFor(() => expect(screen.queryByRole('menu', { name: 'Действия книги' })).toBeNull());

    fireEvent.click(screen.getByRole('button', { name: 'Действия книги Книга II. Карта приливов' }));
    const nextMenu = await screen.findByRole('menu', { name: 'Действия книги' });
    fireEvent.keyDown(nextMenu, { key: 'Escape' });
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
    const listRecentlyDeleted = vi.spyOn(api, 'listRecentlyDeleted');

    await screen.findByRole('link', { name: 'Читать «Книга II. Карта приливов»' });
    // A reader hears whose the books are (specs/2026-11-11-ux7-upload.md UP1.1); there is no zone and no file field.
    const add = screen.getByRole('button', { name: 'Добавить книги' });
    expect(add.getAttribute('aria-disabled')).toBe('true');
    expect(screen.queryByRole('button', { name: /Перетащите FB2/ })).toBeNull();
    expect(screen.queryByLabelText('Файлы книг: FB2, EPUB, Word (.docx), RTF, ODT или текст (.txt, .md)')).toBeNull();
    expect(screen.queryByRole('button', { name: 'Новая книга' })).toBeNull();
    // A reader of the saga returns only his own sagas from «Недавно удалённые»: the books of this one are not his to return.
    expect(listRecentlyDeleted).not.toHaveBeenCalled();
    fireEvent.click(within(screen.getByRole('navigation', { name: 'Разделы' })).getByRole('button', { name: 'Недавно удалённые' }));
    const deleted = await screen.findByRole('dialog', { name: 'Недавно удалённые' });
    expect(await within(deleted).findByText('Здесь будет то, что вы удалите')).toBeTruthy();
    expect(within(deleted).queryByRole('heading', { name: 'Книги' })).toBeNull();
    fireEvent.click(within(deleted).getByRole('button', { name: 'Закрыть недавно удалённые' }));

    fireEvent.click(screen.getByRole('link', { name: 'Читать «Книга II. Карта приливов»' }));
    await waitFor(() =>
      expect(navigate).toHaveBeenCalledWith(
        '/manuscript/books',
        expect.objectContaining({ bookId: 'book-02', mode: 'read', projectId: 'project-white-port' }),
      ),
    );

    fireEvent.click(screen.getByRole('button', { name: 'Действия книги Книга II. Карта приливов' }));
    const menu = await screen.findByRole('menu', { name: 'Действия книги' });
    expect(within(menu).getAllByRole('menuitem').map((item) => item.textContent)).toEqual(['Читать с начала']);
  });

  it('runs delete book and import transitions', async () => {
    const { api } = renderManuscript();
    const cancelImportQueue = vi.spyOn(api, 'cancelImportQueue');
    const uploadImportQueueItem = vi.spyOn(api, 'uploadImportQueueItem');

    await screen.findByText('Книга II. Карта приливов');
    expect(screen.queryByRole('button', { name: 'Новая книга' })).toBeNull();
    fireEvent.click(await screen.findByRole('button', { name: 'Действия книги Книга I. Туманный берег' }));
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Удалить книгу…' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Удалить книгу' }));

    // The line of the reversible deletion with «Вернуть» (specs/2026-11-10-ux2-patterns-actions.md PA3.3).
    const deleted = await screen.findByText(/Книга «Книга I\. Туманный берег» удалена/);
    expect(within(deleted.closest('[role="status"]') as HTMLElement).getByRole('button', { name: 'Вернуть' })).toBeTruthy();

    // One new file goes at once and stands on the shelf as its card (specs/2026-11-11-ux7-upload.md UP2.1, UP3.1).
    const importFile = new File(['<book/>'], 'Северный ветер.epub', { type: 'application/epub+zip' });
    fireEvent.change(screen.getByLabelText('Файлы книг: FB2, EPUB, Word (.docx), RTF, ODT или текст (.txt, .md)'), { target: { files: [importFile] } });
    await waitFor(() => expect(uploadImportQueueItem).toHaveBeenCalledTimes(1));
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(await screen.findByRole('article', { name: 'Северный ветер.epub' })).toBeTruthy();
    expect(cancelImportQueue).not.toHaveBeenCalled();
  });

  it.each([
    { browserMime: '', mimeKind: 'empty' },
    { browserMime: 'application/zip', mimeKind: 'generic' },
  ])('sends a DOCX manuscript with $mimeKind MIME by the queue', async ({ browserMime }) => {
    let importBookFileSpy!: ReturnType<typeof vi.spyOn>;
    let uploadSpy!: ReturnType<typeof vi.spyOn>;
    renderManuscript('', (client) => {
      const importBookFile = client.importBookFile.bind(client);
      importBookFileSpy = vi.spyOn(client, 'importBookFile').mockImplementation(importBookFile);
      uploadSpy = vi.spyOn(client, 'uploadImportQueueItem');
    });

    await screen.findByText('Книга II. Карта приливов');
    const input = screen.getByLabelText('Файлы книг: FB2, EPUB, Word (.docx), RTF, ODT или текст (.txt, .md)') as HTMLInputElement;
    // specs/2026-11-10-ux1-names.md N8.1 (L-19): the system window hides no file — an unfit one gets its reason.
    expect(input.accept).toBe('');
    const sourceFile = new File(['word-document'], 'том 5.docx', {
      lastModified: 1_723_654_321_000,
      type: browserMime,
    });

    fireEvent.change(input, {
      target: {
        files: [sourceFile],
      },
    });

    await waitFor(() => expect(uploadSpy).toHaveBeenCalledTimes(1));
    expect(importBookFileSpy).not.toHaveBeenCalled();
  });

  it('keeps import and delete failures inside the manuscript shelf', async () => {
    const { api } = renderManuscript();
    const cancelImportQueue = vi.spyOn(api, 'cancelImportQueue');
    const uploadImportQueueItem = vi.spyOn(api, 'uploadImportQueueItem');

    await screen.findByText('Книга II. Карта приливов');
    vi.spyOn(api, 'createImportQueue').mockRejectedValueOnce(new Error('Не удалось начать загрузку.'));
    const importBookFile = vi.spyOn(api, 'importBookFile');
    const failedImportFile = new File(['<book/>'], 'northern-court.epub', { type: 'application/epub+zip' });
    fireEvent.change(screen.getByLabelText('Файлы книг: FB2, EPUB, Word (.docx), RTF, ODT или текст (.txt, .md)'), { target: { files: [failedImportFile] } });

    // The refusal stands on the shelf; nothing was sent (specs/2026-11-11-ux7-upload.md UP2.1).
    expect(await screen.findByText(/^Не удалось начать загрузку/u)).toBeTruthy();
    expect(importBookFile).not.toHaveBeenCalled();
    expect(screen.getByText('Книга II. Карта приливов')).toBeTruthy();
    expect(cancelImportQueue).not.toHaveBeenCalled();
    expect(uploadImportQueueItem).not.toHaveBeenCalled();

    vi.spyOn(api, 'deleteBook').mockRejectedValueOnce(new Error('Не удалось удалить книгу.'));
    fireEvent.click(screen.getByRole('button', { name: 'Действия книги Книга II. Карта приливов' }));
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Удалить книгу…' }));
    const deleteDialog = await screen.findByRole('dialog', { name: 'Удалить книгу «Книга II. Карта приливов»?' });
    fireEvent.click(within(deleteDialog).getByRole('button', { name: 'Удалить книгу' }));

    // The refusal stays in the window (PA2.1).
    expect((await within(deleteDialog).findByRole('alert')).textContent).toBe('Не удалось удалить книгу.');
    expect(screen.getByRole('dialog', { name: 'Удалить книгу «Книга II. Карта приливов»?' })).toBeTruthy();
    expect(screen.getByText('Книга II. Карта приливов')).toBeTruthy();
  });

  it('keeps manuscript mutations unavailable until a project exists', async () => {
    renderManuscript('', (api) => {
      api.setScenario({ preset: 'normal', actorRole: 'non_member' });
    });

    // R2.1.1, specs/2026-11-10-ux3-first-screen.md FS1.2: without a saga the first screen asks to name one, says
    // «Добро пожаловать» once; the books come on the shelf of the saga.
    expect(await screen.findByRole('heading', { level: 1, name: 'С чего начнём?' })).toBeTruthy();
    expect(screen.getAllByText(/Добро пожаловать/u)).toHaveLength(1);
    expect(screen.queryByText(/Примите приглашение/)).toBeNull();
    expect(screen.queryByText('Проект загружается')).toBeNull();
    expect(screen.queryByText('В саге пока нет книг')).toBeNull();
    expect(screen.queryByRole('button', { name: /Перетащите FB2, EPUB, Word/ })).toBeNull();
    expect(screen.queryByLabelText('Файлы книг: FB2, EPUB, Word (.docx), RTF, ODT или текст (.txt, .md)')).toBeNull();
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
    expect(await screen.findByRole('heading', { level: 1, name: 'Книги' })).toBeTruthy();
    // The shelf of the new saga confirms it in «Три шага» and asks for the first book (R2.1.1).
    expect(await screen.findByText('Сага «Сага северного ветра» создана')).toBeTruthy();
    expect(screen.getByRole('heading', { name: 'Загрузите книги' })).toBeTruthy();
    await waitFor(() => expect(screen.getAllByRole('button', { name: 'Добавить книги' })[0]).toHaveProperty('disabled', false));
  });

  it('shows search fallbacks in context', async () => {
    const { api } = renderManuscript();

    await screen.findByText('Книга II. Карта приливов');

    vi.spyOn(api, 'searchProject').mockRejectedValueOnce(new Error('Поиск временно недоступен.'));

    fireEvent.click(screen.getByRole('button', { name: 'Поиск по книгам' }));
    fireEvent.change(screen.getByLabelText('Запрос поиска'), { target: { value: 'Белый порт' } });
    fireEvent.click(screen.getByRole('button', { name: 'Найти' }));

    expect(await screen.findByText('Поиск временно недоступен.')).toBeTruthy();
  });

  it('routes project selection and delegates logout to App', async () => {
    const { navigate, onLogout } = renderManuscript('', (client) => {
      void client.createProject({ title: 'Сага северного ветра' });
    });

    await screen.findByRole('link', { name: 'Читать «Книга II. Карта приливов»' });
    navigate.mockClear();
    fireEvent.click(screen.getByRole('button', { name: 'Сага: Хроники Белого порта' }));
    const projectMenu = await screen.findByRole('menu', { name: 'Выбор саги' });
    fireEvent.click(within(projectMenu).getByRole('menuitem', { name: 'Сага северного ветра' }));

    await waitFor(() => expect(navigate).toHaveBeenCalledWith('/manuscript/books', { projectId: expect.not.stringMatching(/^project-white-port$/u) }));

    fireEvent.click(screen.getByRole('button', { name: 'Аккаунт' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Выйти' }));

    await waitFor(() => expect(onLogout).toHaveBeenCalledTimes(1));
  });

  // specs/2026-11-10-ux1-shell.md SH3.2: the name of the saga in the top bar opens the same menu of the saga.
  it('opens the menu of the saga from its name in the top bar', async () => {
    const { navigate } = renderManuscript('', (client) => {
      void client.createProject({ title: 'Сага северного ветра' });
    });
    const saga = await screen.findByRole('button', { name: 'Сага: Хроники Белого порта' });
    expect(saga.closest('header')).toBeTruthy();
    fireEvent.click(saga);
    const projectMenu = await screen.findByRole('menu', { name: 'Выбор саги' });
    expect(within(projectMenu).getByRole('menuitem', { name: 'Новая сага' })).toBeTruthy();
    fireEvent.click(within(projectMenu).getByRole('menuitem', { name: 'Сага северного ветра' }));
    await waitFor(() => expect(navigate).toHaveBeenCalledWith('/manuscript/books', { projectId: expect.not.stringMatching(/^project-white-port$/u) }));
  });
});
