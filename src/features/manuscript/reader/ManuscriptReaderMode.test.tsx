import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import '../../../styles/tokens.css';
import '../../../styles/reading-themes.css';
import { useState } from 'react';
import { ApiStatusError, NetworkApiError, type ApiProblem } from '../../../shared/api';
import { useSiteThemeStore } from '../../../shared/theme/site-theme';
import { createMockApiClient } from '../../../api/mock';
import {
  MANUSCRIPT_DISPLAY_PREFERENCES_STORAGE_KEY,
  readReaderPreferences,
  useReaderPreferencesStore,
} from '../../../entities/manuscript/display-preferences-store';
import { createAppRoute, parseRoute } from '../../../shared/navigation/app-route';
import { InShell } from '../../../app/shell/AppShell.test-helpers';
import { ManuscriptReaderMode } from './ManuscriptReaderMode';
import { readingPlaceOf, rememberReadingPlace } from './reading-place';
import { ToastsProvider } from '../../../ui/feedback/Toast';
import { NotesContext, type NotesActions } from '../../../shared/notes/notes-context';

type ReaderTestApi = ReturnType<typeof createMockApiClient>;
const chapterQuery = '?mode=read&projectId=project-white-port&bookId=book-02&chapterId=chapter-12';

function renderReader(search = chapterQuery, configureApi?: (api: ReaderTestApi) => void) {
  const api = createMockApiClient();
  configureApi?.(api);
  const navigate = vi.fn();
  const queryClient = new QueryClient({ defaultOptions: { mutations: { retry: false }, queries: { retry: false } } });
  // The reader in the shell of the root: the top bar, the sections and «Поиск по книгам» are the shell's (SC1.1, SC1.7).
  function RoutedReader() {
    const [route, setRoute] = useState(parseRoute(`http://localhost/manuscript/books${search}`));
    const go = (destination: string, query?: Record<string, string | undefined>, options?: object) => {
      navigate(destination, query, options);
      if (destination === '/manuscript/books') setRoute(createAppRoute(destination, query));
    };
    // The notes of the root: «Заметка» of a selection asks them for a new note (the window is src/features/notes).
    return <NotesContext.Provider value={notes}>
      <InShell api={api} navigate={go} route={route}><ManuscriptReaderMode api={api} navigate={go} route={route} /></InShell>
    </NotesContext.Provider>;
  }
  const view = render(<QueryClientProvider client={queryClient}><ToastsProvider><RoutedReader /></ToastsProvider></QueryClientProvider>);
  return { api, navigate, queryClient, ...view };
}

// The note beside the text is the window of src/features/notes; here its stand-in names the note it shows.
const notes: NotesActions = { open: vi.fn(), openWithNavigation: vi.fn(), capture: vi.fn(),
  renderNote: vi.fn(({ noteId }: { noteId: string }) => <section aria-label="Заметка рядом с текстом">{noteId}</section>) };

function statusError(status: number, title: string, requestId = 'req-reader-test') {
  return new ApiStatusError(status, { type: 'about:blank', title, status, code: `reader_${status}`, requestId } as ApiProblem, new Response(null, { status }));
}

async function heading() {
  return screen.findByRole('heading', { level: 1, name: 'Глава 12. Белый порт' });
}

function lastQuery(navigate: ReturnType<typeof vi.fn>) {
  return navigate.mock.calls.at(-1)![1] as Record<string, string | undefined>;
}

function select(element: HTMLElement) {
  const range = document.createRange();
  range.selectNodeContents(element);
  Object.defineProperty(range, 'getBoundingClientRect', {
    value: () => ({ bottom: 220, height: 24, left: 300, right: 560, top: 196, width: 260, x: 300, y: 196, toJSON: () => ({}) }),
  });
  const selection = window.getSelection()!;
  selection.removeAllRanges();
  selection.addRange(range);
  fireEvent.pointerUp(element);
}

beforeEach(() => {
  Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1440 });
  Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', { configurable: true, value: vi.fn() });
  localStorage.clear();
  sessionStorage.clear();
  useReaderPreferencesStore.getState().resetReaderPreferences();
});

afterEach(() => {
  window.getSelection()?.removeAllRanges();
  vi.restoreAllMocks();
});

// specs/2026-11-10-ux7-reader.md UC1: a screen for the text.
describe('the reader as a screen for the text', () => {
  it('keeps one line of the reader and one heading over the text, with nothing else above it', async () => {
    renderReader();
    expect(await heading()).toBeTruthy();
    expect(screen.getAllByRole('heading', { level: 1 })).toHaveLength(1);
    const line = document.querySelector<HTMLElement>('[data-reader-bar]')!;
    expect(within(line).getByText('Книга 2 · Глава 12. Белый порт')).toBeTruthy();
    for (const name of ['К книгам', 'Найти', 'Аа', 'Содержание', 'Ещё']) expect(within(line).getByRole('button', { name })).toBeTruthy();
    expect(within(line).getByRole('progressbar', { name: 'Прочитано в главе' })).toBeTruthy();
    // RD1.2: no crumbs, no words and minutes over the text, no row of the look, no «Править главу» row, no fixed bar.
    expect(screen.queryByRole('navigation', { name: 'Хлебные крошки' })).toBeNull();
    expect(screen.queryByText(/слов · \d+ мин/u)).toBeNull();
    expect(screen.queryByRole('button', { name: /Режим фокуса|Размер текста/u })).toBeNull();
    expect(screen.queryByRole('button', { name: /Править главу/u })).toBeNull();
    const turn = screen.getByRole('navigation', { name: 'Навигация по главам' });
    expect(turn.closest('article')).toBe(document.querySelector('[data-reader-target]'));
    expect(turn.textContent).toContain('12 из 48');
  });

  it('lights no paragraph without a link, and the one a link names', async () => {
    renderReader();
    await screen.findByRole('document');
    expect(document.querySelector('[role="document"] mark')).toBeNull();
  });

  it('lights the paragraph a link names', async () => {
    renderReader(`${chapterQuery}&paragraphId=p-12-03`);
    await waitFor(() => expect(document.querySelector('[data-paragraph-id="p-12-03"] mark')?.textContent).toBe('Мара первой заметила дым с северной пристани.'));
  });

  it('hides the line while the text scrolls down and brings it back up and at the top', async () => {
    renderReader();
    await screen.findByRole('document');
    const scroller = document.querySelector<HTMLElement>('[data-reader-target]')!.closest('section')!.parentElement!;
    const head = document.querySelector('[data-reader-bar]')!.closest('header')!;
    const scrollTo = async (top: number) => {
      scroller.scrollTop = top;
      fireEvent.scroll(scroller);
      await act(() => new Promise((resolve) => requestAnimationFrame(() => resolve(undefined))));
    };
    await scrollTo(300);
    expect(head.hasAttribute('data-hidden')).toBe(true);
    await scrollTo(200);
    expect(head.hasAttribute('data-hidden')).toBe(false);
    await scrollTo(400);
    await scrollTo(10);
    expect(head.hasAttribute('data-hidden')).toBe(false);
  });

  it('steps to the next chapter at the end of the text and by Alt+→, keeping the look and the mode', async () => {
    const { navigate } = renderReader(`${chapterQuery}&focus=1&illustrated=1`);
    await heading();
    fireEvent.click(screen.getByRole('button', { name: 'Следующая глава: глава 13' }));
    expect(lastQuery(navigate)).toMatchObject({ chapterId: 'chapter-13', focus: '1', illustrated: '1', mode: 'read', paragraphId: undefined });
  });

  it('steps to the previous chapter by Alt+←', async () => {
    const { navigate } = renderReader(`${chapterQuery}&focus=1`);
    await heading();
    fireEvent.keyDown(document.body, { key: 'ArrowLeft', altKey: true });
    expect(lastQuery(navigate)).toMatchObject({ chapterId: 'chapter-11', focus: '1' });
  });

  it('keeps the step at an end of the book and says why', async () => {
    renderReader(chapterQuery, (api) => {
      const getChapter = api.getChapter.bind(api);
      vi.spyOn(api, 'getChapter').mockImplementation(async (...args) => {
        const chapter = await getChapter(...args);
        return { ...chapter, navigation: { ...chapter.navigation, previous: null, previousChapterId: null } };
      });
    });
    await heading();
    const previous = screen.getByRole('button', { name: 'Предыдущая глава' });
    expect(previous.getAttribute('aria-disabled')).toBe('true');
    fireEvent.click(previous);
    expect(screen.getByText('Это первая глава книги.')).toBeTruthy();
  });

  it('opens «Найти» by «/» and closes it and the panel by Esc', async () => {
    const { navigate } = renderReader(`${chapterQuery}&contents=chapters`);
    await heading();
    fireEvent.keyDown(document.body, { key: '/' });
    const field = await screen.findByRole('searchbox', { name: 'Найти в книге' });
    await waitFor(() => expect(document.activeElement).toBe(field));
    fireEvent.keyDown(field, { key: 'Escape' });
    expect(screen.queryByRole('searchbox', { name: 'Найти в книге' })).toBeNull();
    expect(screen.getByRole('complementary', { name: 'Содержание' })).toBeTruthy();
    fireEvent.keyDown(document.body, { key: 'Escape' });
    expect(lastQuery(navigate).contents).toBeUndefined();
  });

  it('puts the focus of «К содержимому» on the text of the chapter', async () => {
    renderReader();
    const title = await heading();
    expect(title.hasAttribute('data-page-heading')).toBe(true);
    expect(title.closest('[data-reader-target]')).toBeTruthy();
  });
});

// specs/2026-11-10-ux7-reader.md RD1.4, RD1.5: «Аа».
describe('«Аа»', () => {
  it('steps the size in seven steps, sets the background and the width of the column, and keeps them', async () => {
    const { navigate } = renderReader();
    await heading();
    fireEvent.click(within(document.querySelector<HTMLElement>('[data-reader-bar]')!).getByRole('button', { name: 'Аа' }));
    expect(lastQuery(navigate)).toMatchObject({ view: 'open' });
    const look = await screen.findByRole('dialog', { name: 'Вид текста' });
    expect(look.textContent).toContain('18 px');
    expect(look.textContent).toContain('2 из 7');
    fireEvent.click(within(look).getByRole('button', { name: 'А+ крупнее' }));
    expect(look.textContent).toContain('20 px');
    const workspace = document.querySelector<HTMLElement>('[data-display-size]')!;
    expect(workspace.dataset.displaySize).toBe('20');
    expect(workspace.style.getPropertyValue('--reader-text-size')).toBe('20px');
    for (let step = 0; step < 6; step += 1) fireEvent.click(within(look).getByRole('button', { name: 'А+ крупнее' }));
    expect(look.textContent).toContain('32 px');
    expect(look.textContent).toContain('7 из 7');
    expect(within(look).getByRole('button', { name: 'А+ крупнее' }).getAttribute('aria-disabled')).toBe('true');
    fireEvent.click(within(look).getByRole('radio', { name: 'Тёмный' }));
    fireEvent.click(within(look).getByRole('radio', { name: 'Широкая' }));
    expect(workspace.dataset.displayTheme).toBe('dark');
    expect(workspace.getAttribute('data-reading-theme')).toBe('dark');
    expect(workspace.style.getPropertyValue('--reader-column')).toBe('820px');
    // RD1.5: the theme paints the whole reader — its line and the field of the text — not only the column.
    expect(getComputedStyle(workspace).getPropertyValue('--ck-reader')).toBe('#232326');
    expect(document.querySelector('[data-reader-bar]')!.closest('[data-reading-theme]')).toBe(workspace);
    expect(JSON.parse(localStorage.getItem(MANUSCRIPT_DISPLAY_PREFERENCES_STORAGE_KEY)!)).toEqual({ version: 3, textSize: 32, displayTheme: 'dark', columnWidth: 'wide' });
    fireEvent.click(within(look).getByRole('switch', { name: 'Только текст' }));
    expect(lastQuery(navigate)).toMatchObject({ focus: '1', view: undefined });
  });

  // specs/2026-11-12-ux11-dark-core.md TH1.3, TH2.3: «Фон текста» follows the theme of the site until the author chooses it.
  it('reads on the background of the site until «Фон текста» is chosen, and keeps a chosen one on a dark site', async () => {
    useSiteThemeStore.setState({ choice: 'dark', theme: 'dark' });
    try {
      renderReader();
      await heading();
      const workspace = document.querySelector<HTMLElement>('[data-display-size]')!;
      expect(workspace.getAttribute('data-reading-theme')).toBe('dark');
      fireEvent.click(within(document.querySelector<HTMLElement>('[data-reader-bar]')!).getByRole('button', { name: 'Аа' }));
      const look = await screen.findByRole('dialog', { name: 'Вид текста' });
      expect(look.textContent).toContain('Фон текста');
      const background = within(look).getByRole('radiogroup', { name: 'Фон текста' });
      expect(within(background).getByRole('radio', { name: 'Тёмный' }).getAttribute('aria-checked')).toBe('true');
      // A dark site: no «Сделать тёмным весь сайт».
      expect(within(look).queryByRole('button', { name: 'Сделать тёмным весь сайт' })).toBeNull();
      fireEvent.click(within(background).getByRole('radio', { name: 'Светлый' }));
      expect(workspace.getAttribute('data-reading-theme')).toBe('light');
      expect(useReaderPreferencesStore.getState().displayTheme).toBe('light');
    } finally {
      useSiteThemeStore.setState({ choice: 'light', theme: 'light' });
    }
  });

  it('turns the two sizes of before into their nearest steps and survives a broken or denied storage', () => {
    localStorage.setItem(MANUSCRIPT_DISPLAY_PREFERENCES_STORAGE_KEY, JSON.stringify({ version: 1, displaySize: 'large', displayTheme: 'sepia' }));
    expect(readReaderPreferences()).toEqual({ textSize: 20, displayTheme: 'sepia', columnWidth: 'normal' });
    localStorage.setItem(MANUSCRIPT_DISPLAY_PREFERENCES_STORAGE_KEY, JSON.stringify({ version: 1, displaySize: 'normal', displayTheme: 'dark' }));
    expect(readReaderPreferences()).toEqual({ textSize: 18, displayTheme: 'dark', columnWidth: 'normal' });
    // A light background of before the theme of the site was the default, no choice against a dark site: the sheet follows it.
    localStorage.setItem(MANUSCRIPT_DISPLAY_PREFERENCES_STORAGE_KEY, JSON.stringify({ version: 2, textSize: 23, displayTheme: 'light', columnWidth: 'wide' }));
    expect(readReaderPreferences()).toEqual({ textSize: 23, displayTheme: null, columnWidth: 'wide' });
    localStorage.setItem(MANUSCRIPT_DISPLAY_PREFERENCES_STORAGE_KEY, JSON.stringify({ version: 2, textSize: 23, displayTheme: 'sepia', columnWidth: 'wide' }));
    expect(readReaderPreferences()).toEqual({ textSize: 23, displayTheme: 'sepia', columnWidth: 'wide' });
    localStorage.setItem(MANUSCRIPT_DISPLAY_PREFERENCES_STORAGE_KEY, '{bad-json');
    expect(readReaderPreferences()).toEqual({ textSize: 18, displayTheme: null, columnWidth: 'normal' });
    expect(readReaderPreferences({ getItem() { throw new DOMException('Denied', 'SecurityError'); }, removeItem() {}, setItem() {} }))
      .toEqual({ textSize: 18, displayTheme: null, columnWidth: 'normal' });
    const setItem = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new DOMException('Denied'); });
    expect(() => useReaderPreferencesStore.getState().setDisplayTheme('dark')).not.toThrow();
    expect(useReaderPreferencesStore.getState().displayTheme).toBe('dark');
    setItem.mockRestore();
  });

  it('keeps the look and the place after a reload', async () => {
    useReaderPreferencesStore.getState().stepTextSize(1);
    useReaderPreferencesStore.getState().setDisplayTheme('sepia');
    const first = renderReader();
    await heading();
    first.unmount();
    useReaderPreferencesStore.setState({ textSize: 18, displayTheme: 'light' });
    renderReader();
    await heading();
    await waitFor(() => expect(document.querySelector('[data-display-size="20"][data-display-theme="sepia"]')).toBeTruthy());
  });
});

// specs/2026-11-10-ux7-reader.md RD1.6: «Только текст».
describe('«Только текст»', () => {
  it('leaves the text and a thin line with the title, the shell steps aside, and Esc brings it back', async () => {
    const { navigate } = renderReader(`${chapterQuery}&focus=1`);
    await heading();
    const shell = document.querySelector<HTMLElement>('[data-shell]')!;
    expect(shell.hasAttribute('data-bare')).toBe(true);
    expect(document.querySelector('[data-focus]')).toBeTruthy();
    // A touch at the top edge brings the top bar back; one on the text takes it away.
    const touch = (clientY: number) => act(() => { shell.dispatchEvent(new MouseEvent('pointerdown', { bubbles: true, clientX: 600, clientY })); });
    touch(4);
    expect(shell.hasAttribute('data-revealed')).toBe(true);
    touch(400);
    expect(shell.hasAttribute('data-revealed')).toBe(false);
    fireEvent.keyDown(document.body, { key: 'Escape' });
    expect(lastQuery(navigate).focus).toBeUndefined();
  });
});

// specs/2026-11-10-ux7-reader.md RD1.7, RD1.8: «Содержание» and «⋯».
describe('«Содержание» and «⋯»', () => {
  it('opens the panel with its tabs in the address and marks the chapter read', async () => {
    const { navigate } = renderReader();
    await heading();
    fireEvent.click(within(document.querySelector<HTMLElement>('[data-reader-bar]')!).getByRole('button', { name: 'Содержание' }));
    expect(lastQuery(navigate)).toMatchObject({ contents: 'chapters' });
    const panel = await screen.findByRole('complementary', { name: 'Содержание' });
    const tabs = within(panel).getAllByRole('tab').map((tab) => tab.textContent);
    expect(tabs).toEqual(['Главы', 'Заметки', 'Рисунки']);
    const current = within(panel).getByRole('button', { current: 'page' });
    expect(current.textContent).toContain('Белый порт');
    expect(current.textContent).toMatch(/слов · 14 мин/u);
    fireEvent.click(within(panel).getByRole('tab', { name: 'Заметки' }));
    expect(lastQuery(navigate)).toMatchObject({ contents: 'notes' });
  });

  it('opens «Главы» from an old link with «chapters=open»', async () => {
    renderReader(`${chapterQuery}&chapters=open`);
    expect(await screen.findByRole('complementary', { name: 'Содержание' })).toBeTruthy();
  });

  it('opens the chapter in «Письмо» from «⋯» → «Править главу», for an editor only', async () => {
    const { api, navigate } = renderReader();
    const open = vi.spyOn(api, 'openChapterEdit');
    await heading();
    fireEvent.click(screen.getByRole('button', { name: 'Ещё' }));
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Править главу' }));
    await waitFor(() => expect(open).toHaveBeenCalledWith('project-white-port', 'chapter-12'));
    const draft = await open.mock.results[0]!.value;
    // The edit offers «← К главе в книге» back here (specs/2026-11-11-ux9-writing-a.md WA4.2).
    await waitFor(() => expect(navigate).toHaveBeenLastCalledWith('/writing', { projectId: 'project-white-port', draft: draft.id }, { from: { section: 'manuscript' } }));
    expect(screen.queryByRole('menuitemcheckbox')).toBeNull();
  });

  it('gives a reader of the saga only what he may: no «Править главу» and no download', async () => {
    renderReader(chapterQuery, (api) => api.setScenario({ preset: 'normal', actorRole: 'viewer' }));
    await heading();
    expect(screen.queryByRole('button', { name: 'Ещё' })).toBeNull();
    expect(screen.getByRole('note', { name: 'Вы читаете эту сагу' })).toBeTruthy();
  });

  it('gives the demo saga no «Править главу»', async () => {
    renderReader(chapterQuery, (api) => {
      const listProjects = api.listProjects.bind(api) as (...options: unknown[]) => Promise<{ data: object[] }>;
      vi.spyOn(api, 'listProjects').mockImplementation((async (...options: unknown[]) => {
        const page = await listProjects(...options);
        return { ...page, data: page.data.map((project) => ({ ...project, demo: true })) };
      }) as never);
      const getProject = api.getProject.bind(api);
      vi.spyOn(api, 'getProject').mockImplementation(async (id) => ({ ...(await getProject(id)), demo: true }));
    });
    await heading();
    fireEvent.click(screen.getByRole('button', { name: 'Ещё' }));
    expect(screen.queryByRole('menuitem', { name: 'Править главу' })).toBeNull();
    expect(screen.getByRole('menuitem', { name: 'Скачать книгу…' })).toBeTruthy();
  });
});

// specs/2026-11-10-ux7-reader.md UC2: «Найти» in the book.
describe('«Найти» in the book', () => {
  it('marks every place in the chapter, says «N из M» and steps by Enter', async () => {
    renderReader(`${chapterQuery}&find=${encodeURIComponent('на')}`);
    await heading();
    const status = await screen.findByText(/^1 из \d+$/u);
    const total = Number(status.textContent!.split(' из ')[1]);
    expect(total).toBeGreaterThan(1);
    expect(document.querySelectorAll('[role="document"] mark')).toHaveLength(total);
    expect(document.querySelectorAll('[data-find-current]')).toHaveLength(1);
    fireEvent.keyDown(screen.getByRole('searchbox', { name: 'Найти в книге' }), { key: 'Enter' });
    expect(await screen.findByText(`2 из ${total}`)).toBeTruthy();
    fireEvent.keyDown(screen.getByRole('searchbox', { name: 'Найти в книге' }), { key: 'Enter', shiftKey: true });
    expect(await screen.findByText(`1 из ${total}`)).toBeTruthy();
  });

  // specs/2026-11-11-ux9-shelf-chat.md SC2.2 (П-15): on a phone the tools of the reader say their word under the icon.
  it('says the words of «Найти» and of the steps between the places under their icons on a phone', async () => {
    vi.stubGlobal('matchMedia', vi.fn((query: string) => ({ matches: /max-width: 7(60|67)px/u.test(query), addEventListener: vi.fn(), removeEventListener: vi.fn() })));
    renderReader(`${chapterQuery}&find=${encodeURIComponent('на')}`);
    await heading();
    const line = document.querySelector<HTMLElement>('[data-reader-bar]')!;
    const find = within(line).getByRole('button', { name: 'Найти' });
    expect(find.getAttribute('data-caption')).toBe('true');
    expect(find.textContent).toBe('Найти');
    await screen.findByText(/^1 из \d+$/u);
    expect(screen.getByRole('button', { name: 'Предыдущее место' }).textContent).toBe('Назад');
    expect(screen.getByRole('button', { name: 'Следующее место' }).textContent).toBe('Дальше');
    vi.unstubAllGlobals();
  });

  it('lists every place in «Найдено» by its place in words', async () => {
    const { navigate } = renderReader(`${chapterQuery}&find=${encodeURIComponent('Мара')}`);
    await heading();
    await screen.findByText('1 из 1');
    fireEvent.click(screen.getByRole('button', { name: 'Все места' }));
    expect(lastQuery(navigate)).toMatchObject({ contents: 'found', find: 'Мара' });
    const panel = await screen.findByRole('complementary', { name: 'Содержание' });
    const place = within(panel).getByRole('button', { current: true });
    expect(place.textContent).toContain('гл. 12 «Белый порт», абзац 3');
    expect(place.querySelector('mark')?.textContent).toBe('Мара');
  });

  it('opens a place in another chapter on its paragraph and keeps the search', async () => {
    const { navigate } = renderReader(`${chapterQuery}&find=${encodeURIComponent('Мара')}`, (api) => {
      const listChapters = api.listChapters.bind(api);
      vi.spyOn(api, 'listChapters').mockImplementation((async (...args: Parameters<typeof api.listChapters>) => {
        const list = await listChapters(...args);
        const current = list.data.find((item) => item.id === 'chapter-12')!;
        return { ...list, data: [...list.data, { ...current, id: 'chapter-13', title: 'Голоса под водой', order: 13, displayNumber: '13' }] };
      }) as never);
      const getChapter = api.getChapter.bind(api);
      vi.spyOn(api, 'getChapter').mockImplementation(async (id, view) => {
        if (id !== 'chapter-13') return getChapter(id, view);
        const chapter = await getChapter('chapter-12', view);
        return { ...chapter, id: 'chapter-13', title: 'Голоса под водой', paragraphs: chapter.paragraphs.map((item) => ({ ...item, id: item.id.replace('12', '13') })) };
      });
      const search = api.searchProject.bind(api);
      vi.spyOn(api, 'searchProject').mockImplementation(async (...args) => {
        const found = await search(...args);
        const first = found.data[0]!;
        return { ...found, data: [first, { ...first, id: 'search-chapter-13', locator: { ...first.locator, chapterId: 'chapter-13', sourceRef: 'chapter-13' } }] };
      });
    });
    await heading();
    await screen.findByText('1 из 2');
    fireEvent.keyDown(screen.getByRole('searchbox', { name: 'Найти в книге' }), { key: 'Enter' });
    await waitFor(() => expect(lastQuery(navigate)).toMatchObject({ chapterId: 'chapter-13', paragraphId: 'p-13-03', find: 'Мара' }));
  });

  it('says nothing is in the book and searches the whole saga with the same words', async () => {
    renderReader(`${chapterQuery}&find=${encodeURIComponent('зеркальный маяк')}`);
    await heading();
    expect(await screen.findByText('В этой книге такого нет')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Искать во всех книгах саги' }));
    const dialog = await screen.findByRole('dialog', { name: 'Поиск по книгам' });
    expect((within(dialog).getByLabelText('Запрос поиска') as HTMLInputElement).value).toBe('зеркальный маяк');
  });

  it('puts the words of a selection into the bar of the book, not into the search of the saga', async () => {
    const { navigate } = renderReader();
    await screen.findByRole('document');
    select(document.querySelector<HTMLElement>('[data-paragraph-id="p-12-03"]')!);
    fireEvent.click(within(screen.getByRole('toolbar', { name: 'Действия с выделением' })).getByRole('button', { name: 'Найти' }));
    expect((await screen.findByRole('searchbox', { name: 'Найти в книге' }) as HTMLInputElement).value).toBe('Мара первой заметила дым с северной пристани.');
    expect(lastQuery(navigate)).toMatchObject({ find: 'Мара первой заметила дым с северной пристани.' });
    expect(screen.queryByRole('dialog', { name: 'Поиск по книгам' })).toBeNull();
  });
});

// specs/2026-11-10-ux7-reader.md UC3.
describe('the selection, the margin and the links', () => {
  it('offers an editor «Спросить · Заметка · Найти · Копировать» in one row and asks the chat with the quote and its place', async () => {
    const { navigate } = renderReader();
    await screen.findByRole('document');
    select(document.querySelector<HTMLElement>('[data-paragraph-id="p-12-03"]')!);
    const menu = screen.getByRole('toolbar', { name: 'Действия с выделением' });
    expect(within(menu).getAllByRole('button').map((button) => button.textContent)).toEqual(['Спросить', 'Заметка', 'Найти', 'Копировать']);
    fireEvent.click(within(menu).getByRole('button', { name: 'Спросить' }));
    expect(navigate).toHaveBeenLastCalledWith('/chat', { projectId: 'project-white-port' }, {
      chatQuestion: '«Мара первой заметила дым с северной пристани.» (Книга 2, гл. 12 «Белый порт», абзац 3)\n\n',
      from: { section: 'manuscript', name: 'Белый порт' },
    });
  });

  it('offers a reader of the saga «Заметка · Найти · Копировать» and copies with a line of the area of the root', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } });
    renderReader(chapterQuery, (api) => api.setScenario({ preset: 'normal', actorRole: 'viewer' }));
    await screen.findByRole('document');
    const paragraph = document.querySelector<HTMLElement>('[data-paragraph-id="p-12-03"]')!;
    select(paragraph);
    const menu = screen.getByRole('toolbar', { name: 'Действия с выделением' });
    expect(within(menu).getAllByRole('button').map((button) => button.textContent)).toEqual(['Заметка', 'Найти', 'Копировать']);
    fireEvent.click(within(menu).getByRole('button', { name: 'Копировать' }));
    await waitFor(() => expect(writeText).toHaveBeenCalledWith(paragraph.textContent));
    expect((await screen.findByRole('status')).textContent).toContain('Текст скопирован');
  });

  it('opens the chapter at once with one line over the text for a link that went stale', async () => {
    const { navigate } = renderReader(`${chapterQuery}&expectedRevision=1&paragraphId=p-12-03`);
    expect(await screen.findByText('Глава изменилась после правки — показываю с начала. Абзац, на который вела ссылка, мог сдвинуться')).toBeTruthy();
    expect(screen.getByRole('document')).toBeTruthy();
    expect(document.querySelector('[role="document"] mark')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Закрыть строку' }));
    expect(lastQuery(navigate)).toMatchObject({ chapterId: 'chapter-12', paragraphId: undefined, expectedRevision: undefined });
  });

  it.each([
    ['paragraphId=missing-paragraph', 'Этого абзаца в главе больше нет — показываю главу с начала'],
    ['expectedRevision=0&paragraphId=p-12-03', 'Не получилось открыть место по ссылке — показываю начало главы'],
  ])('says why a link opens the chapter from its top: %s', async (search, words) => {
    renderReader(`${chapterQuery}&${search}`);
    expect(await screen.findByText(words)).toBeTruthy();
    expect(screen.getByRole('document')).toBeTruthy();
  });

  it('opens the first chapter for a link to a chapter that is not published', async () => {
    renderReader('?mode=read&projectId=project-white-port&bookId=book-02&chapterId=chapter-16');
    expect(await heading()).toBeTruthy();
    expect(screen.getByText('Главы, на которую вела ссылка, в книге больше нет — показываю первую главу')).toBeTruthy();
  });

  // specs/2026-11-11-ux7-shelf.md SF1.1: the reader keeps the chapter of the book the author read last; the card opens it.
  it('remembers the chapter it opens as the place of its book', async () => {
    renderReader();
    expect(await heading()).toBeTruthy();
    expect(readingPlaceOf('book-02')).toBe('chapter-12');
  });

  it('opens the first chapter quietly when the chapter of the place is gone, and keeps that chapter as the place', async () => {
    rememberReadingPlace('book-02', 'chapter-16');
    renderReader('?mode=read&projectId=project-white-port&bookId=book-02&chapterId=chapter-16');
    expect(await heading()).toBeTruthy();
    expect(screen.queryByText('Главы, на которую вела ссылка, в книге больше нет — показываю первую главу')).toBeNull();
    expect(readingPlaceOf('book-02')).toBe('chapter-12');
  });

  it('never shows the text of another version of the chapter', async () => {
    renderReader(`${chapterQuery}&paragraphId=p-12-03`, (api) => {
      const getChapter = api.getChapter.bind(api);
      vi.spyOn(api, 'getChapter').mockImplementation(async (...args) => ({ ...(await getChapter(...args)), contentVariant: 'draft' }));
    });
    expect(await screen.findByRole('alert', { name: 'Не открылась глава' })).toBeTruthy();
    expect(screen.queryByRole('document')).toBeNull();
  });

  it('says a book has no chapters, brings the deleted ones back and leads «К книгам»', async () => {
    const { navigate } = renderReader(chapterQuery, (api) => {
      vi.spyOn(api, 'listChapters').mockResolvedValueOnce({ data: [], meta: { hasMore: false, nextCursor: null } });
    });
    expect(await screen.findByText('В этой книге пока нет глав', { selector: 'p' })).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Вернуть удалённые главы' }));
    expect(await screen.findByRole('dialog', { name: /Недавно удалённые/u })).toBeTruthy();
    fireEvent.keyDown(document.activeElement!, { key: 'Escape' });
    fireEvent.click(screen.getByRole('button', { name: 'К книгам' }));
    expect(lastQuery(navigate)).toMatchObject({ projectId: 'project-white-port' });
  });

  it('shows the one block of errors without access and leads «К книгам»', async () => {
    const { navigate } = renderReader(chapterQuery, (client) => {
      vi.spyOn(client, 'getChapter').mockRejectedValueOnce(statusError(403, 'Нет доступа к этой главе.'));
    });
    const block = await screen.findByRole('alert', { name: 'Нет доступа к книге' });
    fireEvent.click(within(block).getByRole('button', { name: 'К книгам' }));
    expect(navigate).toHaveBeenCalledWith('/manuscript/books', expect.objectContaining({ projectId: 'project-white-port' }), undefined);
  });

  it('shows loading, 401, 500 and network failures', async () => {
    const unauthorized = renderReader(chapterQuery, (api) => vi.spyOn(api, 'getChapter').mockRejectedValueOnce(statusError(401, 'Unauthorized')));
    expect(await screen.findByText(/^Время входа истекло\. Войдите снова — вернём вас к главе\./u)).toBeTruthy();
    unauthorized.unmount();
    const server = renderReader(chapterQuery, (api) => vi.spyOn(api, 'getChapter').mockRejectedValueOnce(statusError(500, 'Server error')));
    expect(await screen.findByText(/Это сбой на нашей стороне, попробуйте через минуту\./u)).toBeTruthy();
    expect(screen.getByText('req-reader-test').closest('span')?.textContent).toBe('Код для поддержки: req-reader-test');
    fireEvent.click(screen.getByRole('button', { name: 'Повторить' }));
    expect(await heading()).toBeTruthy();
    server.unmount();
    renderReader(chapterQuery, (api) => vi.spyOn(api, 'getChapter').mockRejectedValueOnce(new NetworkApiError(new Error('offline'))));
    expect(await screen.findByText(/^Нет связи с сервером\. Проверьте интернет\./u)).toBeTruthy();
  });

  it('opens the very note of a sign on the margin beside the text; ← in it goes back to the notes of the chapter', async () => {
    const { navigate } = renderReader();
    await screen.findByRole('document');
    const sign = await screen.findByRole('button', { name: /^Заметка: Проверить, кто первым назвал порт/u });
    fireEvent.click(sign);
    // The open note is in the address (specs/2026-11-11-ux7-notes.md NT2.3).
    expect(lastQuery(navigate)).toMatchObject({ contents: 'notes', note: 'note-white-port-12' });
    const panel = await screen.findByRole('complementary', { name: 'Содержание' });
    expect(within(panel).getByRole('region', { name: 'Заметка рядом с текстом' }).textContent).toBe('note-white-port-12');
    const call = vi.mocked(notes.renderNote!).mock.calls.at(-1)![0];
    act(() => call.onBack());
    expect(lastQuery(navigate)).toMatchObject({ contents: 'notes', note: undefined });
    // A row of the tab opens the note too and brings its paragraph into view (jsdom has no CSS.escape).
    vi.stubGlobal('CSS', { escape: (value: string) => value });
    try {
      fireEvent.click(await within(panel).findByRole('button', { name: /Проверить, кто первым назвал порт/u }));
    } finally { vi.unstubAllGlobals(); }
    expect(lastQuery(navigate)).toMatchObject({ contents: 'notes', note: 'note-white-port-12' });
  });

  it('opens a link of a note at its paragraph with the note beside the text', async () => {
    renderReader(`${chapterQuery}&contents=notes&note=note-white-port-12`);
    const panel = await screen.findByRole('complementary', { name: 'Содержание' });
    expect(within(panel).getByRole('region', { name: 'Заметка рядом с текстом' }).textContent).toBe('note-white-port-12');
  });
});

// The reader is «Книги» of the sections; the change of the saga and signing out are the shell's (AppShell.test.tsx).
it('marks «Книги» active and keeps the reader in the narrow column', async () => {
  renderReader();
  await heading();
  const sections = screen.getByRole('navigation', { name: 'Разделы' });
  expect(within(sections).getByRole('button', { name: 'Книги' }).getAttribute('aria-current')).toBe('page');
  expect(document.querySelector('[data-shell]')?.getAttribute('data-column')).toBe('rail');
});
