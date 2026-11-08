import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { useState } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../../api/mock';
import { createAppRoute, parseRoute } from '../../../shared/navigation/app-route';
import { manuscriptSourceDestination } from '../../../shared/navigation/manuscript-route';
import { ManuscriptReaderMode } from './ManuscriptReaderMode';

const sourceQuery = 'projectId=project-white-port&bookId=book-02&mode=read&chapterId=chapter-12';
const originalScroll = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'scrollIntoView');
const scrollIntoView = vi.fn();

function renderSource(search: string, configure?: (api: ReturnType<typeof createMockApiClient>) => void) {
  const api = createMockApiClient();
  configure?.(api);
  const navigate = vi.fn();
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  function RoutedReader() {
    const [route, setRoute] = useState(parseRoute(`/manuscript/books?${sourceQuery}&${search}`));
    return <ManuscriptReaderMode api={api} navigate={(path, query) => {
      navigate(path, query);
      setRoute(createAppRoute(path, query));
    }} onLogout={vi.fn()} route={route} userName="Мира" />;
  }
  render(<QueryClientProvider client={queryClient}><RoutedReader /></QueryClientProvider>);
  return { api, navigate, queryClient };
}

describe('Reader exact source landing', () => {
  beforeEach(() => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1440 });
    Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', { configurable: true, value: scrollIntoView });
    scrollIntoView.mockClear();
  });
  afterEach(() => {
    if (originalScroll) Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', originalScroll);
    else delete (HTMLElement.prototype as Partial<HTMLElement>).scrollIntoView;
    vi.restoreAllMocks();
  });

  it('does not select or scroll a paragraph from a different revision and opens current only explicitly', async () => {
    const { navigate } = renderSource('expectedRevision=1&paragraphId=p-12-03');
    expect(await screen.findByText('Источник изменился')).toBeTruthy();
    expect(document.querySelector('[data-paragraph-id]')).toBeNull();
    expect(scrollIntoView).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Открыть актуальную' }));
    expect(navigate).toHaveBeenLastCalledWith('/manuscript/books', expect.objectContaining({
      chapterId: 'chapter-12', paragraphId: undefined, expectedRevision: undefined,
    }));
    expect(await screen.findByRole('document')).toBeTruthy();
    expect(screen.queryByText('Источник изменился')).toBeNull();
  });

  it('never substitutes the first paragraph for an unavailable explicit paragraph', async () => {
    renderSource('paragraphId=missing-paragraph');
    expect(await screen.findByText('Фрагмент больше не найден')).toBeTruthy();
    expect(document.querySelector('[data-paragraph-id]')).toBeNull();
    expect(scrollIntoView).not.toHaveBeenCalled();
  });

  it('does not treat an invalid expected revision as ordinary chapter navigation', async () => {
    renderSource('expectedRevision=0&paragraphId=p-12-03');
    expect(await screen.findByText('Ссылка на источник недействительна')).toBeTruthy();
    expect(document.querySelector('[data-paragraph-id]')).toBeNull();
    expect(scrollIntoView).not.toHaveBeenCalled();
  });

  it('never renders draft bytes on a published source landing', async () => {
    renderSource('paragraphId=p-12-03', (api) => {
      const getChapter = api.getChapter.bind(api);
      vi.spyOn(api, 'getChapter').mockImplementation(async (...args) => ({ ...(await getChapter(...args)), contentVariant: 'draft' }));
    });
    expect(await screen.findByText('Источник изменился')).toBeTruthy();
    expect(screen.queryByRole('document')).toBeNull();
    expect(scrollIntoView).not.toHaveBeenCalled();
  });

  it('reads the published text when an old link asks for the draft', async () => {
    const { api } = renderSource('sourceView=draft&expectedRevision=7&paragraphId=p-12-03', (client) => {
      vi.spyOn(client, 'getChapter');
    });
    expect(await screen.findByRole('document')).toBeTruthy();
    expect(screen.queryByText('Источник недоступен')).toBeNull();
    expect(api.getChapter).toHaveBeenCalledWith('chapter-12', 'published');
    expect(api.getChapter).not.toHaveBeenCalledWith('chapter-12', 'draft');
  });

  it('uses the same loaded chapter for verification and rendering, preserving chapter anchors without a fabricated paragraph', async () => {
    const fixture = createMockApiClient();
    const chapter = await fixture.getChapter('chapter-12', 'published');
    const { api, navigate } = renderSource(`expectedRevision=${chapter.revision}`, (client) => {
      vi.spyOn(client, 'getChapter').mockResolvedValueOnce(chapter).mockResolvedValue({ ...chapter, revision: chapter.revision + 1 });
    });
    const document = await screen.findByRole('document');
    expect(screen.getByRole('heading', { level: 2, name: 'Белый порт' })).toBeTruthy();
    expect(within(screen.getByRole('navigation', { name: 'Хлебные крошки' }))
      .getByRole('button', { name: 'Книга II. Карта приливов' })).toBeTruthy();
    expect(within(document).queryByRole('button', { pressed: true })).toBeNull();
    expect(api.getChapter).toHaveBeenCalledTimes(1);
    expect(scrollIntoView).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Следующая глава' }));
    expect(navigate).toHaveBeenLastCalledWith('/manuscript/books', expect.objectContaining({
      chapterId: 'chapter-13', expectedRevision: undefined, paragraphId: undefined,
    }));
  });

  it('lands on the server search anchor and keeps its revision through local panel navigation', async () => {
    const { api, navigate } = renderSource('paragraphId=p-12-02');
    const result = (await api.searchProject('project-white-port', 'Мара', 'published')).data.find((item) => item.kind === 'chapter')!;
    await screen.findByRole('document');
    fireEvent.click(screen.getByRole('button', { name: 'Глобальный поиск' }));
    const search = await screen.findByRole('dialog', { name: 'Поиск по книге' });
    fireEvent.change(within(search).getByLabelText('Запрос поиска'), { target: { value: 'Мара' } });
    fireEvent.click(within(search).getByRole('button', { name: 'Найти' }));
    fireEvent.click((await within(search).findAllByRole('button', { name: 'Открыть абзац' }))[0]!);
    expect(navigate).toHaveBeenLastCalledWith('/manuscript/books', manuscriptSourceDestination(result.locator).query);
    await waitFor(() => expect(document.querySelector('[data-paragraph-id="p-12-03"] mark')).toBeTruthy());
    fireEvent.click(within(screen.getAllByLabelText('Настройки чтения')[0]!).getByRole('button', { name: 'Режим фокуса' }));
    expect(navigate).toHaveBeenLastCalledWith('/manuscript/books', expect.objectContaining({
      paragraphId: 'p-12-03', expectedRevision: String(result.locator.sourceRevision),
    }));
  });

  it('honours a server paragraph anchor that names a persisted heading', async () => {
    renderSource('expectedRevision=7&paragraphId=p-12-01');
    await waitFor(() => expect(document.querySelector('[data-paragraph-id="p-12-01"] mark')?.textContent).toBe('Глава 12. Белый порт'));
    expect(scrollIntoView).toHaveBeenCalledWith({ block: 'center' });
    expect(document.querySelector('[data-paragraph-id="p-12-02"] mark')).toBeNull();
  });

  it('keeps a cross-chapter search destination exact instead of applying the current paragraph', async () => {
    const { api, navigate } = renderSource('paragraphId=p-12-03');
    const response = await api.searchProject('project-white-port', 'Белый порт', 'published');
    const source = response.data.find((result) => result.kind === 'chapter')!;
    const result = { ...source, locator: { ...source.locator, chapterId: 'chapter-13', sourceRef: 'chapter-13',
      sourceAnchor: { ...source.locator.sourceAnchor, kind: 'paragraph' as const,
        locator: { ...source.locator.sourceAnchor.locator, chapterId: 'chapter-13', paragraphId: 'p-13-04' } },
    } };
    vi.spyOn(api, 'searchProject').mockResolvedValue({ ...response, data: [result] });
    await screen.findByRole('document');
    fireEvent.click(screen.getByRole('button', { name: 'Глобальный поиск' }));
    const search = await screen.findByRole('dialog', { name: 'Поиск по книге' });
    fireEvent.change(within(search).getByLabelText('Запрос поиска'), { target: { value: 'Белый порт' } });
    fireEvent.click(within(search).getByRole('button', { name: 'Найти' }));
    fireEvent.click(await within(search).findByRole('button', { name: 'Открыть абзац' }));
    expect(navigate).toHaveBeenLastCalledWith('/manuscript/books', manuscriptSourceDestination(result.locator).query);
    expect(await screen.findByText('Источник недоступен')).toBeTruthy();
    expect(document.querySelector('[data-paragraph-id="p-12-03"]')).toBeNull();
  });
});
