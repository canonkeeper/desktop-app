import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { useState } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../api/mock';
import { useReaderPreferencesStore } from '../entities/manuscript/display-preferences-store';
import { createAppRoute, parseRoute } from '../shared/navigation/app-route';
import { NotesProvider } from '../features/notes/NotesProvider';
import { ManuscriptReaderMode } from '../features/manuscript/reader/ManuscriptReaderMode';
import { InShell } from '../app/shell/AppShell.test-helpers';
import { ToastsProvider } from '../ui/feedback/Toast';

vi.mock('@tanstack/react-router', () => ({ useBlocker: vi.fn(() => ({ status: 'idle' })) }));

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
      <InShell api={api} navigate={go} route={route}><ManuscriptReaderMode api={api} navigate={go} route={route} /></InShell>
    </NotesProvider>;
  }
  render(<QueryClientProvider client={client}><ToastsProvider><Reader /></ToastsProvider></QueryClientProvider>);
  return { api, createNote, navigate };
}

describe('Reader with unified Notes', () => {
  beforeEach(() => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1440 });
    localStorage.clear();
    useReaderPreferencesStore.getState().resetReaderPreferences();
  });

  // «Заметка» of the top bar of the shell opens a new note over the reader; its list is one step back (S1.2).
  it('opens common Notes over the same Reader and keeps the look of the text on return', async () => {
    const { navigate } = renderReader('view=open');
    const text = await screen.findByRole('document');
    const look = await screen.findByRole('dialog', { name: 'Вид текста' });
    fireEvent.click(within(look).getByRole('button', { name: 'А+ крупнее' }));
    fireEvent.click(within(look).getByRole('radio', { name: 'Сепия' }));
    fireEvent.click(within(document.querySelector<HTMLElement>('[data-shell-top-bar]')!).getByRole('button', { name: 'Заметка' }));
    const panel = await screen.findByRole('dialog', { name: 'Заметки' });
    expect(await within(panel).findByLabelText('Ваша мысль')).toBeTruthy();
    fireEvent.click(within(panel).getByRole('button', { name: 'К списку заметок' }));
    expect(await within(panel).findByRole('option', { name: /Проверить, кто первым назвал порт/ })).toBeTruthy();
    expect(text.isConnected).toBe(true);
    expect(navigate).not.toHaveBeenCalled();
    fireEvent.click(within(panel).getByRole('button', { name: 'Закрыть заметки' }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Заметки' })).toBeNull());
    expect(screen.getByRole('document')).toBe(text);
    expect(document.querySelector('[data-display-size="20"][data-display-theme="sepia"]')).toBeTruthy();
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
    fireEvent.click(within(screen.getByRole('toolbar', { name: 'Действия с выделением' })).getByRole('button', { name: 'Заметка' }));
    // The selected words are the quote of the note; its thought starts empty (specs/2026-11-11-ux7-notes.md NT1.2).
    expect((await screen.findByLabelText('Ваша мысль') as HTMLTextAreaElement).value).toBe('');
    expect(screen.getByRole('blockquote', { name: 'Цитата' }).textContent).toBe(paragraph.textContent);
    expect(body.isConnected).toBe(true);
    expect(navigate).not.toHaveBeenCalled();
    expect(createNote).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Сохранить' }));
    // RD3.6: the note saved is a line of the one area of the root, not a bar over the page.
    await screen.findByText('Заметка сохранена');
    expect(quote.length).toBeGreaterThan(0);
    expect(createNote).toHaveBeenCalledWith(expect.objectContaining({ body: '', source: {
      kind: 'chapter', quote: paragraph.textContent,
      locator: expect.objectContaining({ paragraphId: paragraph.dataset.paragraphId, targetView: 'published' }),
    } }), expect.anything());
  });

  it('ignores legacy note panels without adding history or losing the exact source', async () => {
    const { navigate } = renderReader('pane=notes&notes=open&note=edit&annotationId=annotation-1&paragraphId=p-12-03&expectedRevision=7');
    await screen.findByRole('document');
    expect(document.querySelector('[data-paragraph-id="p-12-03"] mark')).toBeTruthy();
    expect(screen.queryByRole('dialog', { name: 'Заметки' })).toBeNull();
    expect(screen.queryByLabelText('Ваша мысль')).toBeNull();
    expect(navigate).not.toHaveBeenCalled();
    fireEvent.click(within(document.querySelector<HTMLElement>('[data-reader-bar]')!).getByRole('button', { name: 'Аа' }));
    const target = navigate.mock.calls.at(-1)![1];
    expect(target).toMatchObject({ paragraphId: 'p-12-03', expectedRevision: '7', view: 'open' });
    for (const key of ['pane', 'note', 'notes', 'annotationId']) expect(target[key]).toBeUndefined();
  });

  it('opens the chapter from its top for an invalid legacy source, without editing or lighting a paragraph', async () => {
    renderReader('pane=notes&note=edit&annotationId=missing&paragraphId=missing');
    expect(await screen.findByText('Этого абзаца в главе больше нет — показываю главу с начала')).toBeTruthy();
    expect(screen.getByRole('document')).toBeTruthy();
    expect(document.querySelector('[role="document"] mark')).toBeNull();
    expect(screen.queryByLabelText('Ваша мысль')).toBeNull();
  });
});
