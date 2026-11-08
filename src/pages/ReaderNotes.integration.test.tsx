import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { useState } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../api/mock';
import { useReaderPreferencesStore } from '../entities/manuscript/display-preferences-store';
import { createAppRoute, parseRoute } from '../shared/navigation/app-route';
import { NotesProvider } from '../features/notes/NotesProvider';
import { ManuscriptReaderMode } from '../features/manuscript/reader/ManuscriptReaderMode';

vi.mock('@tanstack/react-router', () => ({ useBlocker: vi.fn() }));

function renderReader(search = '', viewer = false) {
  const api = createMockApiClient();
  api.setScenario({ preset: 'normal', actorRole: viewer ? 'viewer' : 'editor' });
  const createNote = vi.spyOn(api, 'createPersonalNote');
  const navigate = vi.fn();
  const client = new QueryClient({ defaultOptions: { mutations: { retry: false }, queries: { retry: false } } });
  function Reader() {
    const [route, setRoute] = useState(parseRoute(`/manuscript/books?projectId=project-white-port&bookId=book-02&mode=read&chapterId=chapter-12&${search}`));
    const go = (path: string, query?: Record<string, string | undefined>) => {
      navigate(path, query);
      setRoute(createAppRoute(path, query));
    };
    return <NotesProvider api={api} accountId="user-mira" navigate={go} route={route}>
      <ManuscriptReaderMode api={api} navigate={go} onLogout={vi.fn()} route={route} userName="Мира Волкова" />
    </NotesProvider>;
  }
  render(<QueryClientProvider client={client}><Reader /></QueryClientProvider>);
  return { api, createNote, navigate };
}

describe('Reader with unified Notes', () => {
  beforeEach(() => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1440 });
    localStorage.clear();
    useReaderPreferencesStore.getState().resetReaderPreferences();
  });

  it('opens common Notes over the same Reader and keeps appearance controls on return', async () => {
    const { navigate } = renderReader();
    const text = await screen.findByRole('document');
    const controls = screen.getAllByLabelText('Настройки чтения')[0]!;
    fireEvent.click(within(controls).getByRole('button', { name: 'Размер текста' }));
    fireEvent.click(within(controls).getByRole('radio', { name: 'Сепия' }));
    fireEvent.click(screen.getAllByRole('button', { name: 'Заметки' })[0]!);
    const panel = await screen.findByRole('dialog', { name: 'Заметки' });
    expect(await within(panel).findByRole('option', { name: /Проверить, кто первым назвал порт/ })).toBeTruthy();
    expect(text.isConnected).toBe(true);
    expect(navigate).not.toHaveBeenCalled();
    fireEvent.click(within(panel).getByRole('button', { name: 'Закрыть заметки' }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Заметки' })).toBeNull());
    expect(screen.getByRole('document')).toBe(text);
    expect(controls.isConnected).toBe(true);
    expect(document.querySelector('[data-display-size="large"][data-display-theme="sepia"]')).toBeTruthy();
    fireEvent.click(within(controls).getByRole('button', { name: 'Режим фокуса' }));
    expect(navigate).toHaveBeenLastCalledWith('/manuscript/books', expect.objectContaining({ focus: '1' }));
    fireEvent.click(within(controls).getByRole('button', { name: 'Выйти из режима фокуса' }));
    expect(navigate).toHaveBeenLastCalledWith('/manuscript/books', expect.objectContaining({ focus: undefined }));
  });

  it.each([false, true])('captures native text in a personal draft without moving the Reader, viewer=%s', async (viewer) => {
    const { createNote, navigate } = renderReader('', viewer);
    const body = await screen.findByRole('document');
    const paragraph = body.querySelector<HTMLElement>('p[data-paragraph-id]')!;
    const text = document.createTreeWalker(paragraph, NodeFilter.SHOW_TEXT).nextNode()!;
    const range = document.createRange();
    range.setStart(text, 0); range.setEnd(text, 18);
    Object.defineProperty(range, 'getBoundingClientRect', { value: () => new DOMRect(80, 240, 160, 40) });
    const selection = window.getSelection()!;
    selection.removeAllRanges(); selection.addRange(range);
    const quote = selection.toString();
    fireEvent.pointerUp(paragraph);
    expect(screen.queryByText(/Выбран фрагмент/)).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'В заметки' }));
    expect((await screen.findByLabelText('Текст заметки') as HTMLTextAreaElement).value).toBe(quote);
    expect(body.isConnected).toBe(true);
    expect(navigate).not.toHaveBeenCalled();
    expect(createNote).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Сохранить' }));
    await screen.findByText('Заметка сохранена');
    expect(createNote).toHaveBeenCalledWith(expect.objectContaining({ body: quote, source: {
      kind: 'chapter', quote: paragraph.textContent,
      locator: expect.objectContaining({ paragraphId: paragraph.dataset.paragraphId, targetView: 'published' }),
    } }), expect.anything());
  });

  it('ignores legacy note panels without adding history or losing the exact source', async () => {
    const { navigate } = renderReader('pane=notes&notes=open&note=edit&annotationId=annotation-1&paragraphId=p-12-03&expectedRevision=7');
    await screen.findByRole('document');
    expect(document.querySelector('[data-paragraph-id="p-12-03"] mark')).toBeTruthy();
    expect(screen.queryByRole('dialog', { name: 'Заметки' })).toBeNull();
    expect(screen.queryByLabelText('Текст заметки')).toBeNull();
    expect(navigate).not.toHaveBeenCalled();
    fireEvent.click(within(screen.getAllByLabelText('Настройки чтения')[0]!).getByRole('button', { name: 'Режим фокуса' }));
    const target = navigate.mock.calls.at(-1)![1];
    expect(target).toMatchObject({ paragraphId: 'p-12-03', expectedRevision: '7', focus: '1' });
    for (const key of ['pane', 'note', 'notes', 'annotationId']) expect(target[key]).toBeUndefined();
  });

  it('keeps an invalid legacy source visible instead of editing or choosing the first paragraph', async () => {
    renderReader('pane=notes&note=edit&annotationId=missing&paragraphId=missing');
    expect(await screen.findByText('Фрагмент больше не найден')).toBeTruthy();
    expect(screen.queryByRole('document')).toBeNull();
    expect(screen.queryByLabelText('Текст заметки')).toBeNull();
  });

  it('opens the same Notes from mobile and exposes appearance/focus through View', async () => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 390 });
    renderReader();
    const text = await screen.findByRole('document');
    const actions = within(screen.getByLabelText('Мобильные действия чтения'));
    fireEvent.click(actions.getByText('Заметки'));
    const notes = await screen.findByRole('dialog', { name: 'Заметки' });
    fireEvent.click(within(notes).getByRole('button', { name: 'Закрыть заметки' }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Заметки' })).toBeNull());
    fireEvent.click(actions.getByText('Вид'));
    const appearance = within(await screen.findByRole('dialog', { name: 'Вид' }));
    fireEvent.click(appearance.getByRole('radio', { name: 'Крупный' }));
    fireEvent.click(appearance.getByRole('radio', { name: 'Тёмная' }));
    fireEvent.click(appearance.getByRole('button', { name: 'Режим фокуса' }));
    expect(appearance.getByRole('button', { name: 'Выйти из режима фокуса' })).toBeTruthy();
    expect(text.isConnected).toBe(true);
    expect(document.querySelector('[data-display-size="large"][data-display-theme="dark"][data-focus]')).toBeTruthy();
  });
});
