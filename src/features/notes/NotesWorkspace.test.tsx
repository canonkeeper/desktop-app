import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { useState } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../api/mock';
import { createAppRoute, type RouteNavigate } from '../../shared/navigation/app-route';
import { NotesContext } from '../../shared/notes/notes-context';
import { NotesWorkspace } from './NotesWorkspace';
import { ToastsProvider } from '../../ui/feedback/Toast';

vi.mock('@tanstack/react-router', () => ({ useBlocker: vi.fn(() => ({ status: 'idle' })) }));

/** The catalog at its address: a step of the page changes the address it reads, as the router does. */
function RoutedWorkspace({ api, navigate = vi.fn() }: { api: ReturnType<typeof createMockApiClient>; navigate?: RouteNavigate }) {
  const [route, setRoute] = useState(() => createAppRoute('/notes'));
  const go: RouteNavigate = (path, query, options) => {
    navigate(path, query, options);
    if (path === '/notes') setRoute(createAppRoute(path, query));
  };
  return <NotesWorkspace api={api} accountId="user-mira" route={route} navigate={go} />;
}

describe('NotesWorkspace', () => {
  it('keeps an editable chapter note draft when search excludes it from the catalog', async () => {
    const api = createMockApiClient();
    const entry = (await api.listNotes()).data.find((item) => item.sourceKind === 'chapter')!;
    render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><ToastsProvider>
      <RoutedWorkspace api={api} />
    </ToastsProvider></QueryClientProvider>);
    fireEvent.click(await screen.findByRole('option', { name: (name) => name.includes(entry.preview) }));
    fireEvent.change(await screen.findByLabelText('Ваша мысль'), { target: { value: 'Изменённый текст главы' } });
    fireEvent.change(screen.getByRole('searchbox'), { target: { value: 'никакого совпадения' } });
    // A search that found nothing is not an empty section (specs/2026-11-10-ux2-patterns-states.md PS2.3).
    await screen.findByText('Ничего не нашлось по «никакого совпадения»');
    expect(screen.getByRole('button', { name: 'Сбросить поиск' })).toBeTruthy();
    expect((screen.getByLabelText('Ваша мысль') as HTMLTextAreaElement).value).toBe('Изменённый текст главы');
    fireEvent.click(screen.getByRole('button', { name: 'Сохранить' }));
    await screen.findByText('Заметка сохранена');
    expect((await api.getPersonalNote(entry.id)).body).toBe('Изменённый текст главы');
  });
  it('declines the note count and the found notes (R5.3)', async () => {
    const api = createMockApiClient();
    const listNotes = api.listNotes.bind(api);
    vi.spyOn(api, 'listNotes').mockImplementation(async (options = {}) => {
      const page = await listNotes(options);
      return { ...page, meta: { ...page.meta, totalCount: options.query ? 1 : 22 } };
    });
    render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><ToastsProvider>
      <NotesWorkspace api={api} accountId="user-mira" route={createAppRoute('/notes')} navigate={vi.fn()} />
    </ToastsProvider></QueryClientProvider>);
    const heading = await screen.findByRole('heading', { name: 'Заметки' });
    const count = heading.nextElementSibling!;
    await waitFor(() => expect(count.textContent).toBe('22 заметки'));
    fireEvent.change(screen.getByRole('searchbox'), { target: { value: 'порт' } });
    await waitFor(() => expect(count.textContent).toBe('Найдена 1 заметка'));
  });

  it('keeps the tablet scrim interactive while the background is inert', async () => {
    const previousWidth = window.innerWidth;
    window.innerWidth = 834;
    try {
      const api = createMockApiClient();
      await api.createPersonalNote({ body: 'Проверка планшета' }, { idempotencyKey: 'idem-tablet-scrim' });
      const { unmount } = render(<QueryClientProvider client={new QueryClient()}><ToastsProvider><RoutedWorkspace api={api} /></ToastsProvider></QueryClientProvider>);
      fireEvent.click(await screen.findByRole('option', { name: /Проверка планшета/ }));
      const editor = await screen.findByRole('dialog', { name: 'Заметка' });
      const scrim = editor.closest<HTMLElement>('[data-overlay-scrim]')!;
      expect(Boolean(scrim.inert)).toBe(false);
      fireEvent.change(await screen.findByLabelText('Ваша мысль'), { target: { value: 'Не терять' } });
      fireEvent.pointerDown(scrim);
      const confirm = await screen.findByRole('dialog', { name: 'Не сохранять?' });
      fireEvent.click(within(confirm).getByRole('button', { name: 'Отмена' }));
      expect((screen.getByLabelText('Ваша мысль') as HTMLTextAreaElement).value).toBe('Не терять');
      unmount();
    } finally { window.innerWidth = previousWidth; }
  });

  it('loads detail only on selection, retains a dirty form during search, and saves a title', async () => {
    const api = createMockApiClient();
    const note = await api.createPersonalNote({ title: 'Сверить письмо', body: 'Текст письма' }, { idempotencyKey: 'idem-notes-workspace' });
    const detail = vi.spyOn(api, 'getPersonalNote');
    render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><ToastsProvider>
      <NotesContext.Provider value={{ open: vi.fn(), openWithNavigation: vi.fn(), capture: vi.fn() }}>
        <RoutedWorkspace api={api} />
      </NotesContext.Provider>
    </ToastsProvider></QueryClientProvider>);
    const row = await screen.findByRole('option', { name: /Сверить письмо/ });
    expect(detail).not.toHaveBeenCalled();
    fireEvent.click(row);
    const editor = await screen.findByRole('dialog', { name: 'Заметка' });
    fireEvent.change(await within(editor).findByLabelText('Название'), { target: { value: 'Уточнить дату' } });
    fireEvent.change(screen.getByRole('searchbox'), { target: { value: 'ничего подобного' } });
    await screen.findByText('Ничего не нашлось по «ничего подобного»');
    expect((within(editor).getByLabelText('Название') as HTMLInputElement).value).toBe('Уточнить дату');
    fireEvent.click(within(editor).getByRole('button', { name: 'Сохранить' }));
    await screen.findByText('Заметка сохранена');
    expect((await api.getPersonalNote(note.id)).title).toBe('Уточнить дату');
    expect(screen.getByRole('dialog', { name: 'Заметка' })).toBeTruthy();
  });

  it('keeps the open note in the address and goes to its place with the way back to it (specs/2026-11-11-ux7-notes.md NT2.1, NT2.2)', async () => {
    const api = createMockApiClient();
    const entry = (await api.listNotes()).data.find((item) => item.sourceKind === 'chapter')!;
    const detail = await api.getPersonalNote(entry.id);
    if (detail.source?.kind !== 'chapter') throw new Error('missing chapter note fixture');
    const navigate = vi.fn();
    render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><ToastsProvider>
      <NotesWorkspace api={api} accountId="user-mira" route={createAppRoute('/notes', { note: entry.id })} navigate={navigate} />
    </ToastsProvider></QueryClientProvider>);
    // A link to the note, a reload and «Назад» open it: the address names it.
    const editor = await screen.findByRole('dialog', { name: 'Заметка' });
    expect((await within(editor).findByLabelText('Ваша мысль') as HTMLTextAreaElement).value).toBe(detail.body);
    const place = await within(editor).findByRole('button', { name: `К месту в книге: ${entry.place}` });
    fireEvent.click(place);
    await waitFor(() => expect(navigate).toHaveBeenCalledWith('/manuscript/books', expect.objectContaining({
      chapterId: detail.source!.kind === 'chapter' ? detail.source!.locator.chapterId : '', contents: 'notes', note: entry.id,
    }), { from: { section: 'notes', name: 'Проверить, кто первым назвал порт Белым.' } }));
    // Another row opens another note in the address; the cross closes it there.
    fireEvent.click(within(editor).getByRole('button', { name: 'Закрыть заметку' }));
    expect(navigate).toHaveBeenLastCalledWith('/notes', expect.not.objectContaining({ note: expect.anything() }), { replace: true });
  });

  it('shows the notes of the open saga by default; «Откуда» and «Книга» in the words of the author, «Все саги» names the saga (NT3.1, NT3.2)', async () => {
    const api = createMockApiClient();
    const listNotes = vi.spyOn(api, 'listNotes');
    await api.createPersonalNote({ body: 'Своя мысль о порте' }, { idempotencyKey: 'idem-own-thought-filters' });
    const navigate = vi.fn();
    render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><ToastsProvider>
      <RoutedWorkspace api={api} navigate={navigate} />
    </ToastsProvider></QueryClientProvider>);
    await screen.findByRole('option', { name: /Своя мысль о порте/ });
    expect(listNotes).toHaveBeenLastCalledWith(expect.objectContaining({ projectId: 'project-white-port' }));
    expect(screen.queryByText(/Тип|Источник|Записи без источника/u)).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: /^Откуда/u }));
    expect(screen.getAllByRole('option', { name: /^(Все|Из книги|Из чата|Свои мысли)$/u }).map((option) => option.textContent)).toEqual(['Все', 'Из книги', 'Из чата', 'Свои мысли']);
    fireEvent.click(screen.getByRole('option', { name: 'Из книги' }));
    expect(navigate).toHaveBeenLastCalledWith('/notes', expect.objectContaining({ source: 'chapter' }), { replace: true });
    await waitFor(() => expect(listNotes).toHaveBeenLastCalledWith(expect.objectContaining({ source: 'chapter', projectId: 'project-white-port' })));
    fireEvent.click(screen.getByRole('button', { name: /^Книга/u }));
    const choices = screen.getAllByRole('option').filter((option) => option.closest('ul'));
    expect(choices[0]!.textContent).toBe('Все книги саги');
    expect(choices.at(-1)!.textContent).toBe('Все саги');
    fireEvent.click(choices.at(-1)!);
    expect(navigate).toHaveBeenLastCalledWith('/notes', expect.objectContaining({ sagas: 'all', bookId: undefined }), { replace: true });
    await waitFor(() => expect(listNotes).toHaveBeenLastCalledWith(expect.not.objectContaining({ projectId: expect.anything() })));
    // A note of a saga names it in «Все саги».
    expect(await screen.findByRole('option', { name: /Сага «Хроники Белого порта» · /u })).toBeTruthy();
  });
});
