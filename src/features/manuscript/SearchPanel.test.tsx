import { act, fireEvent, render as renderView, renderHook, screen, waitFor, within } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../api/mock';
import type { Project, SearchResult } from '../../entities/manuscript/api';
import { useShelfSearch } from './shelf/useShelfSearch';
import { SearchPanel } from './SearchPanel';

const api = createMockApiClient();
api.setScenario({ actorRole: 'viewer', preset: 'normal' });
const searchResponse = await api.searchProject('project-white-port', 'порт', 'published');
const result: SearchResult | undefined = searchResponse.data.find((item) => item.kind === 'chapter');
if (!result) throw new Error('Chapter search fixture is missing.');
const chapterResult: SearchResult = result;

function render(element: React.ReactNode) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return renderView(element, { wrapper: ({ children }) => <QueryClientProvider client={client}>{children}</QueryClientProvider> });
}

function baseProps() {
  return {
    api: { searchChapters: vi.fn().mockResolvedValue({ data: [], meta: { hasMore: false } }) },
    onOpenChapter: vi.fn(),
    coverage: searchResponse.textCoverage,
    onClose: vi.fn(),
    onOpenResult: vi.fn(),
    onRunSearch: vi.fn(),
    results: [chapterResult],
    scope: 'book' as const,
    searchError: '',
    searchStatus: 'ready' as const,
    searchTerm: 'порт',
    setScope: vi.fn(),
    setSearchTerm: vi.fn(),
    sourceView: 'published' as const,
    state: 'open' as const,
    title: 'Поиск по книге',
  };
}

// specs/2026-11-11-ux9-shelf-chat.md SC2.3 (W-5I, W-62, П-19): the search finds as the words are typed, says nothing of
// how it is built, and names its keys only where there is a keyboard.
describe('SearchPanel as the words are typed', () => {
  it('searches a moment after the words change, without «Найти», and has no line of how it is built', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const props = baseProps();
    const view = render(<SearchPanel {...props} searchTerm="" results={[]} />);
    expect(screen.queryByText(/Область выше/u)).toBeNull();
    view.rerender(<SearchPanel {...props} searchTerm="пор" results={[]} />);
    view.rerender(<SearchPanel {...props} searchTerm="порт" results={[]} />);
    expect(props.onRunSearch).not.toHaveBeenCalled();
    await act(async () => { await vi.advanceTimersByTimeAsync(500); });
    expect(props.onRunSearch).toHaveBeenCalledTimes(1);
    await waitFor(() => expect(props.api.searchChapters).toHaveBeenCalledWith(expect.objectContaining({ query: 'порт' }), expect.anything()));
    vi.useRealTimers();
  });

  it.each([[true, 1], [false, 0]] as const)('names its keys where a keyboard is at hand (%s)', (keyboard, count) => {
    vi.stubGlobal('matchMedia', vi.fn((query: string) => ({ matches: keyboard && query === '(hover: hover) and (pointer: fine)', addEventListener: vi.fn(), removeEventListener: vi.fn() })));
    render(<SearchPanel {...baseProps()} />);
    expect(screen.queryAllByText(/Esc — закрыть/u)).toHaveLength(count);
    vi.unstubAllGlobals();
  });
});

describe('SearchPanel', () => {
  it('keeps a matching chapter from another saga visible when indexed text search fails', async () => {
    const props = baseProps();
    const locator = { projectId: 'other-saga', bookId: 'volume-9', chapterId: 'chapter-458', revision: 7, targetView: 'published' as const };
    props.api.searchChapters.mockResolvedValue({ data: [{ projectTitle: 'Другая сага', bookTitle: 'Том 9', chapterTitle: 'Глава 458. Создание навыка', chapterNumber: '458', locator }], meta: { hasMore: false } });
    render(<SearchPanel {...props} searchTerm="458" searchStatus="error" searchError="Текст ещё не проиндексирован" />);
    await screen.findByText('Глава 458. Создание навыка');
    expect(props.api.searchChapters).toHaveBeenCalledWith(expect.objectContaining({ query: '458', sourceView: 'published' }), expect.anything());
    fireEvent.click(screen.getByRole('button', { name: 'Открыть главу' }));
    expect(props.onOpenChapter).toHaveBeenCalledWith(locator);
    expect(screen.queryByRole('button', { name: 'Исправить канон' })).toBeNull();
  });
  it('hides stale results and counters while loading or failed', () => {
    const props = baseProps();
    const view = render(<SearchPanel {...props} />);

    view.rerender(<SearchPanel {...props} searchStatus="loading" />);
    expect(screen.queryByRole('button', { name: 'Открыть абзац' })).toBeNull();
    expect(screen.queryByText(/1 результат/)).toBeNull();

    view.rerender(<SearchPanel {...props} searchError="Сервис не ответил." searchRequestId="req_7f3a91" searchStatus="error" />);
    const alert = screen.getByRole('alert');

    expect(screen.queryByRole('button', { name: 'Открыть абзац' })).toBeNull();
    expect(alert.textContent?.indexOf('Сервис не ответил.')).toBeLessThan(alert.textContent?.lastIndexOf('Код для поддержки: req_7f3a91'));
    expect(decodeURIComponent(within(alert).getByRole('link', { name: 'Написать нам' }).getAttribute('href') ?? ''))
      .toContain('CanonKeeper: ошибка req_7f3a91');
    expect(within(alert).getByRole('button', { name: 'Повторить поиск' })).toBeTruthy();
  });

  it('loads subsequent chapter matches on scroll without pagination buttons', async () => {
    const props = baseProps();
    const chapter = (id: string) => ({ projectTitle: 'Сага', bookTitle: 'Книга', chapterTitle: `Глава ${id}`, chapterNumber: id,
      locator: { projectId: 'saga', bookId: 'book', chapterId: id, targetView: 'published' as const, revision: 1 } });
    props.api.searchChapters.mockResolvedValueOnce({ data: [chapter('458')], meta: { hasMore: true, nextCursor: 'next' } })
      .mockResolvedValueOnce({ data: [chapter('459')], meta: { hasMore: false } });
    render(<SearchPanel {...props} searchTerm="глава" results={[]} />);
    await screen.findByText('Глава 458 · Глава 458');
    fireEvent.scroll(screen.getByRole('region', { name: 'Результаты поиска' }));
    await screen.findByText('Глава 459 · Глава 459');
    expect(props.api.searchChapters).toHaveBeenLastCalledWith(expect.objectContaining({ cursor: 'next' }), expect.anything());
    expect(screen.getAllByRole('button', { name: 'Открыть главу' })).toHaveLength(2);
    expect(screen.queryByRole('button', { name: 'Ещё главы' })).toBeNull();
  });

  it('names the search scope without a text version and disables an unavailable book scope', () => {
    const view = render(<SearchPanel {...baseProps()} bookName="Северный суд" />);

    expect(screen.getByText('Книга · Северный суд')).toBeTruthy();
    expect(screen.queryByRole('button', { name: /Версия текста/ })).toBeNull();
    expect(screen.queryByText(/Черновик|Опубликован/)).toBeNull();

    view.rerender(<SearchPanel {...baseProps()} bookName="Северный суд" bookScopeDisabled scope="project" />);
    expect(screen.getAllByText('Вся сага').some((node) => node.tagName === 'P')).toBe(true);
    expect(screen.getByRole('radio', { name: 'Книга' })).toHaveProperty('disabled', true);
  });

  it('returns search to project scope when the selected book disappears', async () => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    const wrapper = ({ children }: { children: React.ReactNode }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    );
    const view = renderHook(
      () => useShelfSearch({
        api,
        book: null,
        navigate: vi.fn(),
        project: { id: 'project-white-port' } as Project,
        sourceView: 'published',
      }),
      { wrapper },
    );

    await act(async () => view.result.current.selectSearchScope('book'));

    await waitFor(() => expect(view.result.current.searchScope).toBe('project'));
  });
});
