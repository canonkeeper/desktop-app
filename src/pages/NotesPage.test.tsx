import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../api/mock';
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

async function renderNotes(query: Record<string, string> = {}, viewer = false) {
  const api = createMockApiClient();
  if (viewer) api.setScenario({ actorRole: 'viewer', preset: 'normal' });
  const user = await api.getCurrentUser();
  const navigate = vi.fn();
  const capture = vi.fn();
  const onLogout = vi.fn();
  const route = createAppRoute('/notes', query);
  render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
    <NotesContext.Provider value={{ capture, open: vi.fn(), openWithNavigation: vi.fn() }}>
      <NotesPage apiClient={api} navigate={navigate} onLogout={onLogout} route={route} user={user} />
    </NotesContext.Provider>
  </QueryClientProvider>);
  return { api, capture, navigate, onLogout, route, user };
}

describe('NotesPage shell', () => {
  it('returns to the originating saga and chat without filtering account notes', async () => {
    const returnTo = '/chat?projectId=project-white-port&chatId=kept-chat';
    const { navigate, route } = await renderNotes({ returnTo: `${returnTo}&sourceView=published` });
    const sidebar = screen.getByRole('complementary', { name: 'Навигация саги' });
    await waitFor(() => expect(within(sidebar).getByLabelText('Сага').textContent).toContain('Хроники Белого порта'));
    expect(route.query.has('projectId')).toBe(false);
    expect(route.query.get('returnTo')).toBe(returnTo);
    fireEvent.click(within(sidebar).getByRole('button', { name: 'Чат' }));
    expect(navigate).toHaveBeenCalledWith('/chat', { projectId: 'project-white-port', chatId: 'kept-chat' });
  });
  it('keeps the catalog account-wide while available sidebar projects load', async () => {
    const { navigate, route, user } = await renderNotes();
    fireEvent.click(screen.getByLabelText('Открыть меню'));
    const drawer = await screen.findByRole('dialog', { name: 'Навигация саги' });
    await within(drawer).findByLabelText('Сага');
    expect(within(drawer).getAllByLabelText('Сага')).toHaveLength(1);
    expect(within(drawer).queryByRole('button', { name: 'Хроники Белого порта' })).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Закрыть навигацию' }));
    expect(workspaceProbe).toHaveBeenLastCalledWith(expect.objectContaining({ accountId: user.id, route }));
    expect(route.query.has('projectId')).toBe(false);
    expect(navigate).not.toHaveBeenCalled();
    const sidebar = screen.getByRole('complementary', { name: 'Навигация саги' });
    expect(within(sidebar).getByRole('button', { name: 'Заметки' }).getAttribute('aria-current')).toBe('page');
    expect(screen.queryByRole('button', { name: 'Глобальный поиск' })).toBeNull();
  });

  it('opens only the project list from the desktop sidebar', async () => {
    const { navigate } = await renderNotes();
    fireEvent.click(await screen.findByLabelText('Сага'));
    expect(screen.queryByRole('dialog', { name: 'Навигация саги' })).toBeNull();
    const list = screen.getByRole('listbox', { name: 'Сага' });
    fireEvent.click(within(list).getByRole('option', { name: 'Хроники Белого порта' }));
    await waitFor(() => expect(navigate).toHaveBeenCalledWith('/notes', { returnTo: '/manuscript/books?projectId=project-white-port' }));
  });

  it('keeps the catalog and current project when selection fails', async () => {
    const { api, navigate } = await renderNotes();
    const selector = await screen.findByLabelText('Сага');
    vi.spyOn(api, 'getProject').mockRejectedValueOnce(new Error('Не удалось сменить сагу.'));
    fireEvent.click(selector);
    fireEvent.click(screen.getByRole('option', { name: 'Хроники Белого порта' }));
    await screen.findByText('Не удалось сменить сагу.');
    expect(navigate).not.toHaveBeenCalled();
    expect(selector.textContent).toContain('Хроники Белого порта');
    expect(screen.getByRole('region', { name: 'Каталог заметок' })).toBeTruthy();
  });

  it('creates in place, clears scope through Notes, and delegates logout', async () => {
    const { capture, navigate, onLogout } = await renderNotes({ projectId: 'project-white-port', bookId: 'book-02' });
    const sidebar = screen.getByRole('complementary', { name: 'Навигация саги' });
    fireEvent.click(within(sidebar).getByRole('button', { name: 'Создать заметку' }));
    expect(capture).toHaveBeenCalledWith({ body: '' });
    expect(navigate).not.toHaveBeenCalled();
    fireEvent.click(within(sidebar).getByRole('button', { name: 'Заметки' }));
    expect(navigate).toHaveBeenCalledWith('/notes');
    fireEvent.click(screen.getByRole('button', { name: 'Аккаунт' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Выйти' }));
    expect(onLogout).toHaveBeenCalledOnce();
  });

  it('keeps mobile actions available to a viewer and closes the drawer on create', async () => {
    const { capture } = await renderNotes({}, true);
    fireEvent.click(screen.getByLabelText('Открыть меню'));
    const drawer = await screen.findByRole('dialog', { name: 'Навигация саги' });
    await within(drawer).findByLabelText('Сага');
    expect(within(drawer).queryByRole('button', { name: 'Редактор' })).toBeNull();
    fireEvent.click(within(drawer).getByRole('button', { name: 'Создать заметку' }));
    expect(capture).toHaveBeenCalledWith({ body: '' });
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Навигация саги' })).toBeNull());
  });
});
