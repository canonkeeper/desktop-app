import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../api/mock';
import { InShell } from '../app/shell/AppShell.test-helpers';
import { createAppRoute, type AppRoute } from '../shared/navigation/app-route';
import { NotesContext } from '../shared/notes/notes-context';
import { NotesPage } from './NotesPage';

const workspaceProbe = vi.hoisted(() => vi.fn());
vi.mock('../features/notes/NotesWorkspace', () => ({
  NotesWorkspace: (props: { route: AppRoute; accountId: string }) => {
    workspaceProbe(props);
    return <section aria-label="Каталог заметок" />;
  },
}));

// The notes in the one shell (specs/2026-11-10-ux2-shell-core.md SC1.1, SC1.3): the saga of the place they were opened
// from is the saga of the top bar, the catalog stays account-wide.
async function renderNotes(query: Record<string, string> = {}, viewer = false, otherSaga?: string) {
  const api = createMockApiClient();
  if (viewer) api.setScenario({ actorRole: 'viewer', preset: 'normal' });
  const other = otherSaga ? await api.createProject({ title: otherSaga }) : null;
  const user = await api.getCurrentUser();
  const navigate = vi.fn();
  const capture = vi.fn();
  const onLogout = vi.fn();
  const route = createAppRoute('/notes', query);
  render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
    <NotesContext.Provider value={{ capture, open: vi.fn(), openWithNavigation: vi.fn() }}>
      <InShell api={api} navigate={navigate} onLogout={onLogout} route={route}>
        <NotesPage apiClient={api} navigate={navigate} route={route} user={user} />
      </InShell>
    </NotesContext.Provider>
  </QueryClientProvider>);
  return { api, capture, navigate, onLogout, other, route, user };
}

describe('NotesPage in the shell', () => {
  it('returns to the originating saga and chat without filtering account notes', async () => {
    const returnTo = '/chat?projectId=project-white-port&chatId=kept-chat';
    const { navigate, route } = await renderNotes({ returnTo: `${returnTo}&sourceView=published` });
    expect(await screen.findByRole('button', { name: 'Сага: Хроники Белого порта' })).toBeTruthy();
    expect(route.query.has('projectId')).toBe(false);
    expect(route.query.get('returnTo')).toBe(returnTo);
    fireEvent.click(within(screen.getByRole('navigation', { name: 'Разделы' })).getByRole('button', { name: 'Чат' }));
    expect(navigate).toHaveBeenCalledWith('/chat', { projectId: 'project-white-port', chatId: 'kept-chat' });
  });

  it('keeps the catalog account-wide and marks «Заметки» in the sections', async () => {
    const { navigate, route, user } = await renderNotes();
    await screen.findByRole('button', { name: 'Сага: Хроники Белого порта' });
    expect(workspaceProbe).toHaveBeenLastCalledWith(expect.objectContaining({ accountId: user.id, route }));
    expect(route.query.has('projectId')).toBe(false);
    expect(navigate).not.toHaveBeenCalled();
    const sections = screen.getByRole('navigation', { name: 'Разделы' });
    expect(within(sections).getByRole('button', { name: 'Заметки' }).getAttribute('aria-current')).toBe('page');
    expect(screen.queryByRole('button', { name: /Глобальный/u })).toBeNull();
    expect(screen.getByRole('main', { name: 'Заметки' }).querySelector('[aria-label="Каталог заметок"]')).toBeTruthy();
  });

  it('moves the notes to the saga chosen in the menu of the saga', async () => {
    const { navigate, other } = await renderNotes({}, false, 'Песни южных островов');
    fireEvent.click(await screen.findByRole('button', { name: 'Сага: Хроники Белого порта' }));
    const list = await screen.findByRole('menu', { name: 'Выбор саги' });
    fireEvent.click(await within(list).findByRole('menuitem', { name: 'Песни южных островов' }));
    await waitFor(() => expect(navigate).toHaveBeenCalledWith('/notes', { returnTo: `/manuscript/books?projectId=${other!.id}` }));
  });

  it('keeps the catalog and current project when selection fails', async () => {
    const { api, navigate } = await renderNotes({}, false, 'Песни южных островов');
    const saga = await screen.findByRole('button', { name: 'Сага: Хроники Белого порта' });
    vi.spyOn(api, 'getProject').mockRejectedValueOnce(new Error('Не удалось сменить сагу.'));
    fireEvent.click(saga);
    fireEvent.click(await within(await screen.findByRole('menu', { name: 'Выбор саги' })).findByRole('menuitem', { name: 'Песни южных островов' }));
    await screen.findByText('Не удалось сменить сагу.');
    expect(navigate).not.toHaveBeenCalled();
    expect(saga.textContent).toContain('Хроники Белого порта');
    expect(screen.getByRole('region', { name: 'Каталог заметок' })).toBeTruthy();
  });

  it('creates a note in place by «Заметка», opens «Заметки» from the sections and signs out from the account', async () => {
    const { capture, navigate, onLogout } = await renderNotes({ projectId: 'project-white-port', bookId: 'book-02' });
    await screen.findByRole('button', { name: 'Сага: Хроники Белого порта' });
    fireEvent.click(within(screen.getByRole('banner')).getByRole('button', { name: 'Заметка' }));
    expect(capture).toHaveBeenCalledWith({ body: '' });
    expect(navigate).not.toHaveBeenCalled();
    fireEvent.click(within(screen.getByRole('navigation', { name: 'Разделы' })).getByRole('button', { name: 'Заметки' }));
    expect(navigate).toHaveBeenCalledWith('/notes', expect.objectContaining({ returnTo: expect.any(String) }));
    fireEvent.click(screen.getByRole('button', { name: 'Аккаунт' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Выйти' }));
    expect(onLogout).toHaveBeenCalledOnce();
  });

  it('lets a viewer of the saga write his own note', async () => {
    const { capture } = await renderNotes({}, true);
    await screen.findByRole('note', { name: 'Вы читаете эту сагу' });
    expect(screen.queryByRole('button', { name: 'Редактор' })).toBeNull();
    fireEvent.click(within(screen.getByRole('banner')).getByRole('button', { name: 'Заметка' }));
    expect(capture).toHaveBeenCalledWith({ body: '' });
  });
});
