import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import '../../../styles/reading-themes.css';
import { useState } from 'react';
import { ApiStatusError, NetworkApiError, type ApiProblem } from '../../../shared/api';
import { createMockApiClient } from '../../../api/mock';
import {
  MANUSCRIPT_DISPLAY_PREFERENCES_STORAGE_KEY,
  readReaderPreferences,
  useReaderPreferencesStore,
} from '../../../entities/manuscript/display-preferences-store';
import { createAppRoute, parseRoute } from '../../../shared/navigation/app-route';
import { ManuscriptReaderMode } from './ManuscriptReaderMode';

type ReaderTestApi = ReturnType<typeof createMockApiClient>;

function makeRoute(search = '?mode=read&chapterId=chapter-12', path = '/manuscript/books') {
  return parseRoute(new URL(`http://localhost${path}${search}`));
}

function renderReader(search = '', configureApi?: (api: ReaderTestApi) => void, path?: string) {
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

  function RoutedReader() {
    const [route, setRoute] = useState(makeRoute(search, path));
    return <ManuscriptReaderMode api={api} navigate={(destination, query) => {
      navigate(destination, query);
      setRoute(createAppRoute(destination, query));
    }} onLogout={onLogout} route={route} userName="Мира Волкова" />;
  }
  const view = render(
    <QueryClientProvider client={queryClient}>
      <RoutedReader />
    </QueryClientProvider>,
  );

  return { api, navigate, onLogout, queryClient, ...view };
}

function statusError(status: number, title: string, requestId = 'req-reader-test') {
  return new ApiStatusError(
    status,
    { type: 'about:blank', title, status, code: `reader_${status}`, requestId } as ApiProblem,
    new Response(null, { status }),
  );
}

describe('ManuscriptReaderMode', () => {
  beforeEach(() => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1440 });
    localStorage.clear();
    useReaderPreferencesStore.getState().resetReaderPreferences();
  });

  // specs/2026-11-05-voice-full-access.md A8.7: an editor opens the chapter in «Письмо»; a reader has no such button.
  it('opens the chapter in «Письмо» from «Править главу», for an editor only', async () => {
    const { api, navigate } = renderReader();
    const open = vi.spyOn(api, 'openChapterEdit');
    fireEvent.click(await screen.findByRole('button', { name: 'Править главу «Белый порт»' }));
    await waitFor(() => expect(open).toHaveBeenCalledWith('project-white-port', 'chapter-12'));
    const draft = await open.mock.results[0]!.value;
    await waitFor(() => expect(navigate).toHaveBeenLastCalledWith('/writing', { projectId: 'project-white-port', draft: draft.id }));
  });

  it('gives a reader no «Править главу»', async () => {
    renderReader('', (api) => api.setScenario({ preset: 'normal', actorRole: 'viewer' }));
    expect(await screen.findByRole('heading', { level: 1, name: /Белый порт/u })).toBeTruthy();
    expect(screen.queryByRole('button', { name: /Править главу/u })).toBeNull();
  });

  it('marks Reader active and offers no Editor shortcut', async () => {
    renderReader('?mode=read&projectId=project-white-port&bookId=book-02&chapterId=chapter-12');
    await screen.findAllByRole('heading', { name: 'Глава 12. Белый порт' });
    const sidebar = screen.getByRole('complementary', { name: 'Навигация саги' });
    expect(within(sidebar).getByRole('button', { name: 'Чтение' }).getAttribute('aria-current')).toBe('page');
    expect(within(sidebar).queryByRole('button', { name: 'Редактор' })).toBeNull();
  });

  it('opens unified Notes from the sidebar without reopening a legacy editor', async () => {
    const { navigate } = renderReader('?mode=read&projectId=project-white-port&bookId=book-02&chapterId=chapter-12&pane=notes&paragraphId=p-12-03');
    await screen.findByRole('document');
    const sidebar = within(screen.getByRole('complementary', { name: 'Навигация саги' }));
    fireEvent.click(sidebar.getByRole('button', { name: 'Заметки' }));
    expect(navigate).toHaveBeenCalledWith('/notes', undefined);
    expect(screen.queryByLabelText('Текст заметки')).toBeNull();
  });

  it('renders the exact reader locator and preserves project/book context across chapter navigation', async () => {
    const { api, navigate } = renderReader('?projectId=project-white-port&bookId=book-02&paragraphId=p-12-03', (client) => {
      vi.spyOn(client, 'getChapter');
    });

    expect((await screen.findAllByRole('heading', { name: 'Глава 12. Белый порт' })).length).toBeGreaterThan(0);
    await waitFor(() =>
      expect(document.querySelector('[data-paragraph-id="p-12-03"] mark')?.textContent).toBe('Мара первой заметила дым с северной пристани.'),
    );
    expect(vi.mocked(api.getChapter).mock.calls.every(([, contentVariant]) => contentVariant === 'published')).toBe(true);

    fireEvent.click(screen.getByRole('button', { name: 'Следующая глава' }));

    expect(navigate).toHaveBeenCalledWith(
      '/manuscript/books',
      expect.objectContaining({
        bookId: 'book-02',
        chapterId: 'chapter-13',
        mode: 'read',
        projectId: 'project-white-port',
      }),
    );
  });

  it('keeps desktop focus mode in the URL while the project sidebar becomes a narrow column of named sections', async () => {
    const { navigate } = renderReader('?projectId=project-white-port&bookId=book-02&chapterId=chapter-12&focus=1');

    await screen.findByRole('heading', { level: 1, name: 'Глава 12. Белый порт' });
    const sidebar = screen.getByRole('complementary', { name: 'Навигация саги' });
    expect(within(sidebar).getByRole('button', { name: 'Мои книги' }).textContent).toBe('Мои книги');
    fireEvent.click(within(screen.getAllByLabelText('Настройки чтения')[0]!).getByRole('button', { name: 'Выйти из режима фокуса' }));
    expect(navigate).toHaveBeenLastCalledWith('/manuscript/books', expect.objectContaining({ focus: undefined }));
    fireEvent.click(within(await screen.findByLabelText('Настройки чтения')).getByRole('button', { name: 'Режим фокуса' }));
    fireEvent.click(screen.getByRole('button', { name: 'Следующая глава' }));
    expect(navigate).toHaveBeenLastCalledWith('/manuscript/books', expect.objectContaining({ focus: '1' }));
  });

  it('scrolls to a requested paragraph after the async source chapter is ready', async () => {
    const api = createMockApiClient();
    const getChapter = api.getChapter.bind(api);
    let releaseChapter!: () => void;
    const chapterGate = new Promise<void>((resolve) => { releaseChapter = resolve; });
    vi.spyOn(api, 'getChapter').mockImplementation(async (...args) => {
      await chapterGate;
      return getChapter(...args);
    });
    const scrollIntoView = vi.fn();
    const previousScrollIntoView = HTMLElement.prototype.scrollIntoView;
    Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', { configurable: true, value: scrollIntoView });
    const navigate = vi.fn();
    const queryClient = new QueryClient({ defaultOptions: { mutations: { retry: false }, queries: { retry: false } } });

    render(
      <QueryClientProvider client={queryClient}>
        <ManuscriptReaderMode api={api} navigate={navigate} onLogout={vi.fn()} route={makeRoute('?mode=read&chapterId=chapter-12&paragraphId=p-12-03')} userName="Мира Волкова" />
      </QueryClientProvider>,
    );
    expect(await screen.findByRole('status', { name: 'Загружаем главу' })).toBeTruthy();
    releaseChapter();

    await screen.findByText('Мара первой заметила дым с северной пристани.');
    await waitFor(() => expect(scrollIntoView).toHaveBeenCalledWith({ block: 'center' }));
    if (previousScrollIntoView) {
      Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', { configurable: true, value: previousScrollIntoView });
    } else {
      delete (HTMLElement.prototype as Partial<HTMLElement>).scrollIntoView;
    }
  });

  it('reads only chapters with a published revision', async () => {
    const draftOnly = renderReader('?mode=read&chapterId=chapter-16', (api) => {
      vi.spyOn(api, 'getChapter');
    });

    expect(await screen.findByRole('heading', { level: 1, name: 'Глава 12. Белый порт' })).toBeTruthy();
    expect(screen.getByRole('alert').textContent).toContain('Открыта другая глава');
    expect(screen.queryByText('16. Возвращение к записи')).toBeNull();
    expect(vi.mocked(draftOnly.api.getChapter)).toHaveBeenCalledWith('chapter-12', 'published');
    expect(vi.mocked(draftOnly.api.getChapter)).not.toHaveBeenCalledWith('chapter-16', 'published');
    draftOnly.unmount();

    const stalePublished = renderReader('?mode=read&chapterId=chapter-12', (api) => {
      const listChapters = api.listChapters.bind(api);
      vi.spyOn(api, 'listChapters').mockImplementation(async (...args) => {
        const response = await listChapters(...args);
        return {
          ...response,
          data: response.data.map((chapter) => chapter.id === 'chapter-12' ? { ...chapter, status: 'stale' } : chapter),
        };
      });
      vi.spyOn(api, 'getChapter');
    });

    expect(await screen.findByRole('heading', { level: 1, name: 'Глава 12. Белый порт' })).toBeTruthy();
    expect(vi.mocked(stalePublished.api.getChapter)).toHaveBeenCalledWith('chapter-12', 'published');
  });

  it('открывает Chat из reader navigation без возврата Agent actions', async () => {
    const { navigate } = renderReader('?projectId=project-white-port&bookId=book-02&paragraphId=p-12-03');

    expect((await screen.findAllByRole('heading', { name: 'Глава 12. Белый порт' })).length).toBeGreaterThan(0);
    fireEvent.click(screen.getByRole('button', { name: 'Чат' }));
    expect(navigate).toHaveBeenCalledWith('/chat', { projectId: 'project-white-port' });
    expect(screen.queryByRole('button', { name: 'Агент' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Проверить агентом' })).toBeNull();
  });

  it('returns from a chapter to the books shelf through global navigation', async () => {
    const { navigate } = renderReader('?projectId=project-white-port&bookId=book-02&paragraphId=p-12-03');

    expect((await screen.findAllByRole('heading', { name: 'Глава 12. Белый порт' })).length).toBeGreaterThan(0);
    fireEvent.click(within(screen.getByRole('navigation', { name: 'Разделы саги' })).getByRole('button', { name: 'Мои книги' }));

    expect(navigate).toHaveBeenCalledWith('/manuscript/books', {
      bookId: 'book-02',
      projectId: 'project-white-port',
    });
  });

  it('shows canonical forbidden fallback and routes back to books', async () => {
    const { navigate } = renderReader('', (client) => {
      vi.spyOn(client, 'getChapter').mockRejectedValueOnce(statusError(403, 'Нет доступа к этой главе.'));
    });

    expect(await screen.findByRole('heading', { name: 'Нет доступа к книге' })).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'На полку' }));

    expect(navigate).toHaveBeenCalledWith('/manuscript/books', expect.objectContaining({ projectId: 'project-white-port' }));
  });

  it('renders chapter loading as skeleton lines', async () => {
    let release!: () => void;
    const gate = new Promise<void>((resolve) => { release = resolve; });
    const view = renderReader('', (api) => {
      const getChapter = api.getChapter.bind(api);
      vi.spyOn(api, 'getChapter').mockImplementation(async (...args) => {
        await gate;
        return getChapter(...args);
      });
    });

    const loading = await screen.findByRole('status', { name: 'Загружаем главу' });
    expect(loading.querySelectorAll('[aria-hidden="true"]').length).toBeGreaterThanOrEqual(3);
    release();
    expect(await screen.findByRole('heading', { level: 1, name: 'Глава 12. Белый порт' })).toBeTruthy();
    view.unmount();
  });

  it('shows reader loading, empty, 401, 500 and network states without route scenarios', async () => {
    const emptyView = renderReader('', (api) => {
      vi.spyOn(api, 'listChapters').mockResolvedValueOnce({ data: [], meta: { hasMore: false, nextCursor: null } });
    });
    expect(await screen.findByRole('heading', { name: 'Нет доступных глав' })).toBeTruthy();
    emptyView.unmount();

    const unauthorizedView = renderReader('', (api) => {
      vi.spyOn(api, 'getChapter').mockRejectedValueOnce(statusError(401, 'Unauthorized'));
    });
    expect(await screen.findByText('Время входа истекло. Войдите снова — вернём вас к главе.')).toBeTruthy();
    unauthorizedView.unmount();

    const serverView = renderReader('', (api) => {
      vi.spyOn(api, 'getChapter').mockRejectedValueOnce(statusError(500, 'Server error'));
    });
    expect(await screen.findByText(/Сервис временно недоступен\. Попробуйте снова\./)).toBeTruthy();
    expect(within(screen.getByRole('alert')).getByText('Не удалось открыть главу')).toBeTruthy();
    expect(screen.getByText('req-reader-test').closest('span')?.textContent).toBe('Код для поддержки: req-reader-test');
    expect(decodeURIComponent(within(screen.getByRole('alert')).getByRole('link', { name: 'Написать в поддержку' }).getAttribute('href') ?? ''))
      .toContain('Canon Keeper: ошибка req-reader-test');
    fireEvent.click(screen.getByRole('button', { name: 'Повторить' }));
    expect(await screen.findByRole('heading', { level: 1, name: 'Глава 12. Белый порт' })).toBeTruthy();
    serverView.unmount();

    const networkView = renderReader('', (api) => {
      vi.spyOn(api, 'getChapter').mockRejectedValueOnce(new NetworkApiError(new Error('offline')));
    });
    expect(await screen.findByText('Не удалось подключиться к серверу. Проверьте соединение и попробуйте снова.')).toBeTruthy();
    networkView.unmount();
  });

  it('persists reader preferences through the allowlisted localStorage payload', async () => {
    const firstView = renderReader('?paragraphId=p-12-03');

    expect((await screen.findAllByRole('heading', { name: 'Глава 12. Белый порт' })).length).toBeGreaterThan(0);
    const rail = within(screen.getByRole('complementary', { name: 'Панель чтения' }));
    expect.soft(getComputedStyle(rail.getByLabelText('Размер текста')).background).toBe('rgb(241, 241, 239)');
    const increase = getComputedStyle(rail.getByRole('button', { name: 'Увеличить текст' }));
    expect.soft(increase.background).toBe('var(--ck-card)');
    expect.soft(increase.boxShadow).toBe('var(--ck-shadow-sm)');
    expect.soft(getComputedStyle(rail.getByRole('button', { name: 'Сепия' })).background).toBe('rgb(244, 236, 216)');
    expect.soft(getComputedStyle(rail.getByRole('button', { name: 'Тёмная' })).background).toBe('rgb(46, 46, 46)');
    fireEvent.click(screen.getAllByRole('button', { name: 'Сепия' })[0]!);
    fireEvent.click(screen.getByRole('button', { name: 'Размер текста' }));

    await waitFor(() => expect(document.querySelector('[data-display-theme="sepia"]')).toBeTruthy());
    expect(document.querySelector('[data-display-size="large"]')).toBeTruthy();
    const readerText = screen.getByLabelText('Текст главы');
    expect(getComputedStyle(readerText).getPropertyValue('--ck-reader')).toBe('#faf3e2');
    expect(getComputedStyle(readerText).getPropertyValue('--ck-muted-foreground')).toBe('#6A5C45');
    expect(getComputedStyle(readerText).getPropertyValue('--ck-subtle-foreground')).toBe('#6F6450');

    fireEvent.click(rail.getByRole('button', { name: 'Тёмная' }));
    await waitFor(() => expect(document.querySelector('[data-display-theme="dark"]')).toBeTruthy());
    expect(getComputedStyle(readerText).getPropertyValue('--ck-subtle-foreground')).toBe('#9A9AA3');
    expect(getComputedStyle(readerText).getPropertyValue('--ck-link')).toBe('#A5B4FC');
    expect(getComputedStyle(readerText).getPropertyValue('--ck-destructive-text')).toBe('#F87171');

    const stored = localStorage.getItem(MANUSCRIPT_DISPLAY_PREFERENCES_STORAGE_KEY);
    expect(stored).toBeTruthy();
    expect(JSON.parse(stored!)).toEqual({ version: 1, displaySize: 'large', displayTheme: 'dark' });
    expect(stored).not.toMatch(/ck_session|auth|session|draft|manuscript|chat|project|password/i);

    firstView.unmount();
    useReaderPreferencesStore.setState({ displaySize: 'normal', displayTheme: 'light' });
    renderReader('?paragraphId=p-12-03');

    expect((await screen.findAllByRole('heading', { name: 'Глава 12. Белый порт' })).length).toBeGreaterThan(0);
    await waitFor(() => expect(document.querySelector('[data-display-theme="dark"]')).toBeTruthy());
    expect(document.querySelector('[data-display-size="large"]')).toBeTruthy();
  });

  it('falls back to defaults when preference storage denies reads', () => {
    const deniedStorage = {
      getItem() {
        throw new DOMException('Denied', 'SecurityError');
      },
      removeItem() {},
      setItem() {},
    };

    expect(readReaderPreferences(deniedStorage)).toEqual({ displaySize: 'normal', displayTheme: 'light' });
  });

  it('survives corrupted or denied preference storage', () => {
    localStorage.setItem(MANUSCRIPT_DISPLAY_PREFERENCES_STORAGE_KEY, '{bad-json');
    useReaderPreferencesStore.getState().hydrateReaderPreferences();
    expect(useReaderPreferencesStore.getState()).toMatchObject({ displaySize: 'normal', displayTheme: 'light' });

    const setItem = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new DOMException('Denied');
    });

    expect(() => useReaderPreferencesStore.getState().setDisplayTheme('dark')).not.toThrow();
    expect(useReaderPreferencesStore.getState().displayTheme).toBe('dark');
    setItem.mockRestore();
  });

  it('opens mobile chapters and project search without leaving the reader surface', async () => {
    const { navigate } = renderReader('?chapters=open&paragraphId=p-12-03');

    const chaptersDialog = await screen.findByRole('dialog', { name: 'Главы' });
    expect(getComputedStyle(chaptersDialog.closest('[data-overlay-scrim]')!).background).toBe('var(--ck-scrim)');
    const mobileWorkspace = [...document.styleSheets].flatMap((sheet) => [...sheet.cssRules])
      .filter((rule): rule is CSSMediaRule => rule instanceof CSSMediaRule && rule.conditionText === '(max-width: 760px)')
      .flatMap((rule) => [...rule.cssRules])
      .find((rule): rule is CSSStyleRule => rule instanceof CSSStyleRule && document.querySelector('main')!.matches(rule.selectorText));
    expect.soft(mobileWorkspace?.style.background).toBe('var(--ck-reader-pane)');
    fireEvent.click(within(chaptersDialog).getByRole('button', { name: /Белый порт/ }));
    expect(navigate).toHaveBeenCalledWith(
      '/manuscript/books',
      expect.objectContaining({ chapterId: 'chapter-12', mode: 'read', projectId: 'project-white-port' }),
    );
    await screen.findByRole('document');
    expect(screen.queryByRole('dialog', { name: 'Главы' })).toBeNull();
    expect(screen.getByRole('button', { name: 'Чат' })).toBeTruthy();

    fireEvent.click(screen.getByRole('button', { name: 'Глобальный поиск' }));
    fireEvent.change(screen.getByLabelText('Запрос поиска'), { target: { value: 'Мара' } });
    fireEvent.click(screen.getByRole('button', { name: 'Найти' }));
    fireEvent.click((await within(screen.getByRole('dialog', { name: 'Поиск по книге' })).findAllByRole('button', { name: 'Открыть абзац' }))[0]!);

    expect(navigate).toHaveBeenCalledWith(
      '/manuscript/books',
      expect.objectContaining({ chapterId: 'chapter-12', mode: 'read', paragraphId: 'p-12-03' }),
    );
  });

  it('opens mobile display controls in the View sheet instead of changing size inline', async () => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 390 });
    const { navigate } = renderReader('?paragraphId=p-12-03');

    await screen.findByRole('heading', { level: 1, name: 'Глава 12. Белый порт' });
    fireEvent.click(screen.getByRole('button', { name: 'Вид', hidden: true }));

    const sheet = await screen.findByRole('dialog', { name: 'Вид' });
    expect(within(sheet).getByRole('radiogroup', { name: 'Размер текста' })).toBeTruthy();
    expect(within(sheet).getByRole('radiogroup', { name: 'Тема чтения' })).toBeTruthy();
    expect(navigate).toHaveBeenLastCalledWith('/manuscript/books', expect.objectContaining({ view: 'open' }));

    fireEvent.click(within(sheet).getByRole('radio', { name: 'Крупный' }));
    fireEvent.click(within(sheet).getByRole('radio', { name: 'Сепия' }));
    expect(document.querySelector('[data-display-size="large"][data-display-theme="sepia"]')).toBeTruthy();
  });

  it('does not open a retired mobile notes panel from a legacy link', async () => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 390 });
    const { navigate } = renderReader('?notes=open&pane=notes&paragraphId=p-12-03');
    await screen.findByRole('document');
    expect(screen.queryByRole('dialog', { name: 'Заметки' })).toBeNull();
    expect(document.querySelector('[data-paragraph-id="p-12-03"] mark')).toBeTruthy();
    expect(navigate).not.toHaveBeenCalled();
  });

  it('clears menu=open before opening the mobile project picker', async () => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 390 });
    const { navigate } = renderReader('?projectId=project-white-port&bookId=book-02&menu=open&paragraphId=p-12-03');
    await screen.findByRole('heading', { level: 1, name: 'Глава 12. Белый порт' });
    const menu = await screen.findByRole('dialog', { name: 'Меню чтения' });
    expect(getComputedStyle(menu.closest('[data-overlay-scrim]')!).background).toBe('var(--ck-scrim)');

    fireEvent.click(within(menu).getByRole('button', { name: /Проект|Хроники Белого порта/ }));

    expect(navigate).toHaveBeenLastCalledWith('/manuscript/books', expect.objectContaining({ menu: undefined }));
    expect(await screen.findByRole('menu', { name: 'Выбор саги' })).toBeTruthy();
  });

  it('closes search and opens the chapter source without a notes panel', async () => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 834 });
    const { api, navigate } = renderReader('?pane=notes');
    const response = await api.searchProject('project-white-port', 'порт', 'published');
    vi.spyOn(api, 'searchProject').mockResolvedValue(response);
    await screen.findAllByRole('heading', { name: 'Глава 12. Белый порт' });
    fireEvent.click(screen.getByRole('button', { name: 'Глобальный поиск' }));
    const searchDialog = await screen.findByRole('dialog', { name: 'Поиск по книге' });
    fireEvent.change(within(searchDialog).getByLabelText('Запрос поиска'), { target: { value: 'порт' } });
    fireEvent.click(within(searchDialog).getByRole('button', { name: 'Найти' }));
    const chapterResult = await within(searchDialog).findByText('Белый порт');
    fireEvent.click(chapterResult.closest('article')!.querySelector<HTMLButtonElement>('button')!);

    expect(navigate).toHaveBeenCalledWith('/manuscript/books', expect.objectContaining({
      projectId: 'project-white-port',
      bookId: 'book-02',
      chapterId: 'chapter-12',
      mode: 'read',
      expectedRevision: '7',
      paragraphId: response.data[0]?.locator.sourceAnchor.locator.paragraphId,
    }));
    expect(screen.queryByRole('dialog', { name: 'Поиск по книге' })).toBeNull();
    expect(screen.queryByRole('dialog', { name: 'Заметки' })).toBeNull();
  });

  it('offers a viewer selection only to book search and copy', async () => {
    const { api } = renderReader('', (client) => {
      client.setScenario({ preset: 'normal', actorRole: 'viewer' });
      vi.spyOn(client, 'searchProject');
    });
    const paragraph = await readerParagraphButton('p-12-03');

    selectReaderText(paragraph);

    expect(screen.getByRole('button', { name: 'Найти в книге' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Копировать' })).toBeTruthy();
    expect(screen.queryByRole('button', { name: /Канон|Пунктуация|Сократить/ })).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Найти в книге' }));

    const searchDialog = await screen.findByRole('dialog', { name: 'Поиск по книге' });
    expect(screen.queryByRole('button', { name: 'Копировать' })).toBeNull();
    expect((within(searchDialog).getByLabelText('Запрос поиска') as HTMLInputElement).value).toBe(paragraph.textContent);
    await waitFor(() => expect(api.searchProject).toHaveBeenCalledWith(
      'project-white-port',
      paragraph.textContent,
      'published',
      'all',
      { filters: { bookId: 'book-02' } },
    ));
  });

  it('copies a viewer selection and reports it through a neutral toast', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } });
    renderReader('', (api) => api.setScenario({ preset: 'normal', actorRole: 'viewer' }));
    const paragraph = await readerParagraphButton('p-12-03');

    selectReaderText(paragraph);
    fireEvent.click(screen.getByRole('button', { name: 'Копировать' }));

    await waitFor(() => expect(writeText).toHaveBeenCalledWith(paragraph.textContent));
    expect((await screen.findByRole('status')).textContent).toContain('Фрагмент скопирован');
  });

  it('shows a mobile chapter title without a repeated exact ordinal', async () => {
    renderReader('?chapters=open&paragraphId=p-12-03', (api) => {
      const listChapters = api.listChapters.bind(api);
      vi.spyOn(api, 'listChapters').mockImplementation(async (...args) => {
        const response = await listChapters(...args);
        return {
          ...response,
          data: response.data.map((chapter) => chapter.id === 'chapter-12' ? { ...chapter, title: 'Глава 12. Белый порт' } : chapter),
        };
      });
    });

    expect(await screen.findByRole('button', { name: '12 Белый порт' })).toBeTruthy();
  });

  it('keeps a failed search query and request ID available for an exact retry', async () => {
    const { api } = renderReader();
    const response = await api.searchProject('project-white-port', 'Белый порт', 'published');
    vi.spyOn(api, 'searchProject')
      .mockRejectedValueOnce(statusError(503, 'Search unavailable', 'req-reader-search'))
      .mockResolvedValueOnce(response);

    await screen.findAllByRole('heading', { name: 'Глава 12. Белый порт' });
    fireEvent.click(screen.getByRole('button', { name: 'Глобальный поиск' }));
    const searchDialog = await screen.findByRole('dialog', { name: 'Поиск по книге' });
    fireEvent.change(within(searchDialog).getByLabelText('Запрос поиска'), { target: { value: 'Белый порт' } });
    fireEvent.click(within(searchDialog).getByRole('button', { name: 'Найти' }));

    expect(await within(searchDialog).findByText(/req-reader-search/)).toBeTruthy();
    expect((within(searchDialog).getByLabelText('Запрос поиска') as HTMLInputElement).value).toBe('Белый порт');
    fireEvent.click(within(searchDialog).getByRole('button', { name: 'Повторить поиск' }));
    expect((await within(searchDialog).findAllByRole('button', { name: 'Открыть абзац' })).length).toBeGreaterThan(0);
    fireEvent.click(within(searchDialog).getByRole('button', { name: 'Очистить поиск' }));
    expect(within(searchDialog).queryAllByRole('button', { name: 'Открыть абзац' })).toHaveLength(0);
    expect(within(searchDialog).getByText(/Введите название, номер главы/)).toBeTruthy();
  });

  it('uses the existing search mutation for exact book and project scopes', async () => {
    const { api } = renderReader();
    const searchProject = vi.spyOn(api, 'searchProject');

    await screen.findAllByRole('heading', { name: 'Глава 12. Белый порт' });
    fireEvent.click(screen.getByRole('button', { name: 'Глобальный поиск' }));
    const searchDialog = await screen.findByRole('dialog', { name: 'Поиск по книге' });
    const scope = within(searchDialog).getByRole('radiogroup', { name: 'Область поиска по тексту' });
    fireEvent.change(within(searchDialog).getByLabelText('Запрос поиска'), { target: { value: 'Белый порт' } });
    fireEvent.click(within(searchDialog).getByRole('button', { name: 'Найти' }));

    await waitFor(() => expect(searchProject).toHaveBeenCalledWith(
      'project-white-port',
      'Белый порт',
      'published',
      'all',
      { filters: { bookId: 'book-02' } },
    ));

    fireEvent.click(within(scope).getByRole('radio', { name: 'Вся сага' }));
    await waitFor(() => expect(searchProject).toHaveBeenLastCalledWith(
      'project-white-port',
      'Белый порт',
      'published',
      'all',
      undefined,
    ));
  });

  it('does not offer Canon Correction to a project viewer', async () => {
    renderReader('', (api) => api.setScenario({ preset: 'normal', actorRole: 'viewer' }));

    await screen.findAllByRole('heading', { name: 'Глава 12. Белый порт' });
    fireEvent.click(screen.getByRole('button', { name: 'Глобальный поиск' }));
    fireEvent.change(screen.getByLabelText('Запрос поиска'), { target: { value: 'порт' } });
    fireEvent.click(screen.getByRole('button', { name: 'Найти' }));

    expect(await screen.findByRole('button', { name: 'Открыть абзац' })).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Исправить канон' })).toBeNull();

  });

  it('keeps project switch failures inside the reader surface, but clears local logout on failure', async () => {
    const { api, onLogout } = renderReader('?projectId=project-white-port&bookId=book-02&paragraphId=p-12-03');

    expect((await screen.findAllByRole('heading', { name: 'Глава 12. Белый порт' })).length).toBeGreaterThan(0);
    vi.spyOn(api, 'getProject').mockRejectedValueOnce(new Error('Не удалось сменить сагу.'));
    fireEvent.click(screen.getByRole('button', { name: /^(?!Открыть меню).*Хроники Белого порта/u }));
    const projectMenu = await screen.findByRole('menu', { name: 'Выбор саги' });
    fireEvent.click(within(projectMenu).getByRole('menuitem', { name: /^(?!Открыть меню).*Хроники Белого порта/u }));

    expect(await screen.findByText('Не удалось сменить сагу.')).toBeTruthy();

    vi.spyOn(api, 'logout').mockRejectedValueOnce(new Error('Не удалось выйти.'));
    fireEvent.click(screen.getByRole('button', { name: 'Аккаунт' }));
    const accountMenu = await screen.findByRole('menu', { name: 'Меню аккаунта' });
    fireEvent.click(within(accountMenu).getByRole('menuitem', { name: 'Выйти' }));

    await waitFor(() => expect(onLogout).toHaveBeenCalledTimes(1));
  });

  it('opens a selected project at its shelf without retaining the previous Reader locator', async () => {
    const { navigate } = renderReader('?projectId=project-white-port&bookId=book-02&paragraphId=p-12-03');

    await screen.findAllByRole('heading', { name: 'Глава 12. Белый порт' });
    fireEvent.click(screen.getByRole('button', { name: /^(?!Открыть меню).*Хроники Белого порта/u }));
    const projectMenu = await screen.findByRole('menu', { name: 'Выбор саги' });
    fireEvent.click(within(projectMenu).getByRole('menuitem', { name: /^(?!Открыть меню).*Хроники Белого порта/u }));

    await waitFor(() => expect(navigate).toHaveBeenCalledWith('/manuscript/books', { projectId: 'project-white-port' }));
  });
});

async function readerParagraphButton(paragraphId: string) {
  await screen.findByRole('heading', { level: 1, name: 'Глава 12. Белый порт' });
  const paragraph = document.querySelector<HTMLElement>(`[data-paragraph-id="${paragraphId}"]`);
  if (!paragraph) throw new Error(`Reader paragraph ${paragraphId} is missing.`);
  return paragraph;
}

function selectReaderText(element: HTMLElement) {
  const range = document.createRange();
  range.selectNodeContents(element);
  Object.defineProperty(range, 'getBoundingClientRect', {
    value: () => ({ bottom: 220, height: 24, left: 300, right: 560, top: 196, width: 260, x: 300, y: 196, toJSON: () => ({}) }),
  });
  const selection = window.getSelection();
  if (!selection) throw new Error('Selection API is unavailable.');
  selection.removeAllRanges();
  selection.addRange(range);
  fireEvent.pointerUp(element);
}
