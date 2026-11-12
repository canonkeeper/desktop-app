import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { useState } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../../api/mock';
import { createAppRoute, parseRoute } from '../../../shared/navigation/app-route';
import { manuscriptSourceDestination } from '../../../shared/navigation/manuscript-route';
import { InShell } from '../../../app/shell/AppShell.test-helpers';
import { ManuscriptReaderMode } from './ManuscriptReaderMode';

const sourceQuery = 'projectId=project-white-port&bookId=book-02&mode=read&chapterId=chapter-12';
const originalScroll = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'scrollIntoView');
const scrollIntoView = vi.fn();

function renderSource(search: string, configure?: (api: ReturnType<typeof createMockApiClient>) => void) {
  const api = createMockApiClient();
  configure?.(api);
  const navigate = vi.fn();
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  // In the shell of the root: «Поиск по книгам» is the shell's (specs/2026-11-10-ux2-shell-core.md SC1.7).
  function RoutedReader() {
    const [route, setRoute] = useState(parseRoute(`/manuscript/books?${sourceQuery}&${search}`));
    const go = (path: string, query?: Record<string, string | undefined>) => {
      navigate(path, query);
      setRoute(createAppRoute(path, query));
    };
    return <InShell api={api} navigate={go} route={route}><ManuscriptReaderMode api={api} navigate={go} route={route} /></InShell>;
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

  // specs/2026-11-10-ux7-reader.md RD3.4: a stale link opens the chapter at once, from its top, with one line over it.
  it('opens the chapter from its top for a paragraph of another revision, and no paragraph is lit or scrolled to', async () => {
    const { navigate } = renderSource('expectedRevision=1&paragraphId=p-12-03');
    expect(await screen.findByText(/^Глава изменилась после правки/u)).toBeTruthy();
    expect(await screen.findByRole('document')).toBeTruthy();
    expect(document.querySelector('[role="document"] mark')).toBeNull();
    expect(scrollIntoView).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Закрыть строку' }));
    expect(navigate).toHaveBeenLastCalledWith('/manuscript/books', expect.objectContaining({
      chapterId: 'chapter-12', paragraphId: undefined, expectedRevision: undefined,
    }));
    await waitFor(() => expect(screen.queryByText(/^Глава изменилась после правки/u)).toBeNull());
  });

  it('never substitutes the first paragraph for an unavailable explicit paragraph', async () => {
    renderSource('paragraphId=missing-paragraph');
    expect(await screen.findByText('Этого абзаца в главе больше нет — показываю главу с начала')).toBeTruthy();
    expect(document.querySelector('[role="document"] mark')).toBeNull();
    expect(scrollIntoView).not.toHaveBeenCalled();
  });

  it('opens the chapter from its top for an invalid expected revision', async () => {
    renderSource('expectedRevision=0&paragraphId=p-12-03');
    expect(await screen.findByText('Не получилось открыть место по ссылке — показываю начало главы')).toBeTruthy();
    expect(document.querySelector('[role="document"] mark')).toBeNull();
    expect(scrollIntoView).not.toHaveBeenCalled();
  });

  it('never renders draft bytes on a published source landing', async () => {
    renderSource('paragraphId=p-12-03', (api) => {
      const getChapter = api.getChapter.bind(api);
      vi.spyOn(api, 'getChapter').mockImplementation(async (...args) => ({ ...(await getChapter(...args)), contentVariant: 'draft' }));
    });
    expect(await screen.findByRole('alert', { name: 'Не открылась глава' })).toBeTruthy();
    expect(screen.queryByRole('document')).toBeNull();
    expect(scrollIntoView).not.toHaveBeenCalled();
  });

  it('reads the published text when an old link asks for the draft', async () => {
    const { api } = renderSource('sourceView=draft&expectedRevision=7&paragraphId=p-12-03', (client) => {
      vi.spyOn(client, 'getChapter');
    });
    expect(await screen.findByRole('document')).toBeTruthy();
    expect(screen.queryByRole('status', { name: /ссылк/u })).toBeNull();
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
    expect(screen.getByRole('heading', { level: 1, name: 'Глава 12. Белый порт' })).toBeTruthy();
    expect(screen.getByTitle('Книга II. Карта приливов').textContent).toContain('Книга 2 · Глава 12. Белый порт');
    expect(within(document).queryByRole('button', { pressed: true })).toBeNull();
    expect(api.getChapter).toHaveBeenCalledTimes(1);
    expect(scrollIntoView).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Следующая глава: глава 13' }));
    expect(navigate).toHaveBeenLastCalledWith('/manuscript/books', expect.objectContaining({
      chapterId: 'chapter-13', expectedRevision: undefined, paragraphId: undefined,
    }));
  });

  it('lands on the server search anchor and keeps its revision through local panel navigation', async () => {
    const { api, navigate } = renderSource('paragraphId=p-12-02');
    const result = (await api.searchProject('project-white-port', 'Мара', 'published')).data.find((item) => item.kind === 'chapter')!;
    await screen.findByRole('document');
    fireEvent.click(screen.getByRole('button', { name: 'Поиск по книгам' }));
    const search = await screen.findByRole('dialog', { name: 'Поиск по книгам' });
    fireEvent.change(within(search).getByLabelText('Запрос поиска'), { target: { value: 'Мара' } });
    fireEvent.click(within(search).getByRole('button', { name: 'Найти' }));
    fireEvent.click((await within(search).findAllByRole('button', { name: 'Открыть абзац' }))[0]!);
    expect(navigate).toHaveBeenLastCalledWith('/manuscript/books', manuscriptSourceDestination(result.locator).query);
    await waitFor(() => expect(document.querySelector('[data-paragraph-id="p-12-03"] mark')).toBeTruthy());
    fireEvent.click(screen.getByRole('button', { name: 'Содержание' }));
    expect(navigate).toHaveBeenLastCalledWith('/manuscript/books', expect.objectContaining({
      paragraphId: 'p-12-03', expectedRevision: String(result.locator.sourceRevision), contents: 'chapters',
    }));
  });

  it('honours a server paragraph anchor that names a persisted heading', async () => {
    renderSource('expectedRevision=7&paragraphId=p-12-01');
    await waitFor(() => expect(document.querySelector('h1[data-paragraph-id="p-12-01"] mark')?.textContent).toBe('Глава 12. Белый порт'));
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
    fireEvent.click(screen.getByRole('button', { name: 'Поиск по книгам' }));
    const search = await screen.findByRole('dialog', { name: 'Поиск по книгам' });
    fireEvent.change(within(search).getByLabelText('Запрос поиска'), { target: { value: 'Белый порт' } });
    fireEvent.click(within(search).getByRole('button', { name: 'Найти' }));
    fireEvent.click(await within(search).findByRole('button', { name: 'Открыть абзац' }));
    expect(navigate).toHaveBeenLastCalledWith('/manuscript/books', manuscriptSourceDestination(result.locator).query);
    // The chapter of the link is not in the book: the first chapter opens from its top, nothing of the old place lit.
    expect(await screen.findByText('Главы, на которую вела ссылка, в книге больше нет — показываю первую главу')).toBeTruthy();
    expect(document.querySelector('[role="document"] mark')).toBeNull();
  });
});
