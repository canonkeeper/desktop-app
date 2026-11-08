import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../api/mock';
import { createAppRoute } from '../../shared/navigation/app-route';
import { NotesContext } from '../../shared/notes/notes-context';
import { NotesWorkspace } from './NotesWorkspace';

vi.mock('@tanstack/react-router', () => ({ useBlocker: vi.fn() }));

describe('NotesWorkspace', () => {
  it('keeps an editable chapter note draft when search excludes it from the catalog', async () => {
    const api = createMockApiClient();
    const entry = (await api.listNotes()).data.find((item) => item.sourceKind === 'chapter')!;
    render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <NotesWorkspace api={api} accountId="user-mira" route={createAppRoute('/notes')} navigate={vi.fn()} />
    </QueryClientProvider>);
    fireEvent.click(await screen.findByRole('option', { name: (name) => name.includes(entry.preview) }));
    fireEvent.change(await screen.findByLabelText('Текст заметки'), { target: { value: 'Изменённый текст главы' } });
    fireEvent.change(screen.getByRole('searchbox'), { target: { value: 'никакого совпадения' } });
    await screen.findByText('Ничего не найдено');
    expect((screen.getByLabelText('Текст заметки') as HTMLTextAreaElement).value).toBe('Изменённый текст главы');
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
    render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <NotesWorkspace api={api} accountId="user-mira" route={createAppRoute('/notes')} navigate={vi.fn()} />
    </QueryClientProvider>);
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
      const { unmount } = render(<QueryClientProvider client={new QueryClient()}><NotesWorkspace api={api} accountId="user-mira" route={createAppRoute('/notes')} navigate={vi.fn()} /></QueryClientProvider>);
      fireEvent.click(await screen.findByRole('option', { name: /Проверка планшета/ }));
      const editor = await screen.findByRole('dialog', { name: 'Заметка' });
      const scrim = editor.closest<HTMLElement>('[data-overlay-scrim]')!;
      expect(Boolean(scrim.inert)).toBe(false);
      fireEvent.change(await screen.findByLabelText('Текст заметки'), { target: { value: 'Не терять' } });
      fireEvent.pointerDown(scrim);
      const confirm = await screen.findByRole('dialog', { name: 'Не сохранять?' });
      fireEvent.click(within(confirm).getByRole('button', { name: 'Назад' }));
      expect((screen.getByLabelText('Текст заметки') as HTMLTextAreaElement).value).toBe('Не терять');
      unmount();
    } finally { window.innerWidth = previousWidth; }
  });

  it('loads detail only on selection, retains a dirty form during search, and saves a title', async () => {
    const api = createMockApiClient();
    const note = await api.createPersonalNote({ title: 'Сверить письмо', body: 'Текст письма' }, { idempotencyKey: 'idem-notes-workspace' });
    const detail = vi.spyOn(api, 'getPersonalNote');
    render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <NotesContext.Provider value={{ open: vi.fn(), openWithNavigation: vi.fn(), capture: vi.fn() }}>
        <NotesWorkspace api={api} accountId="user-mira" route={createAppRoute('/notes')} navigate={vi.fn()} />
      </NotesContext.Provider>
    </QueryClientProvider>);
    const row = await screen.findByRole('option', { name: /Сверить письмо/ });
    expect(detail).not.toHaveBeenCalled();
    fireEvent.click(row);
    const editor = await screen.findByRole('dialog', { name: 'Заметка' });
    fireEvent.change(await within(editor).findByLabelText('Название · необязательно'), { target: { value: 'Уточнить дату' } });
    fireEvent.change(screen.getByRole('searchbox'), { target: { value: 'ничего подобного' } });
    await screen.findByText('Ничего не найдено');
    expect((within(editor).getByLabelText('Название · необязательно') as HTMLInputElement).value).toBe('Уточнить дату');
    fireEvent.click(within(editor).getByRole('button', { name: 'Сохранить' }));
    await screen.findByText('Заметка сохранена');
    expect((await api.getPersonalNote(note.id)).title).toBe('Уточнить дату');
    expect(screen.getByRole('dialog', { name: 'Заметка' })).toBeTruthy();
  });
});
