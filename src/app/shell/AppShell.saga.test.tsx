import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../api/mock';
import { projectId } from '../../api/mock/repository/fixtures';
import { createAppRoute } from '../../shared/navigation/app-route';
import { InShell } from './AppShell.test-helpers';

// specs/2026-11-10-ux2-shell-core.md UC3 (S3.1, S3.2): one menu of the saga — «Ваши саги», «Новая сага», «Настроить сагу»
// — and the window of the settings of the saga by the role of the author. Moved here from the menu of the shelf.

type Api = ReturnType<typeof createMockApiClient>;

function renderSaga(api: Api, query: Record<string, string> = { projectId }) {
  const navigate = vi.fn();
  const toasts = { push: vi.fn(() => 7), dismiss: vi.fn() };
  render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })}>
    <InShell api={api} navigate={navigate} route={createAppRoute('/manuscript/books', query)} toasts={toasts}><h1>Книги</h1></InShell>
  </QueryClientProvider>);
  return { navigate, toasts };
}

async function openMenu(name = 'Хроники Белого порта') {
  fireEvent.click(await screen.findByRole('button', { name: `Сага: ${name}` }));
  return screen.findByRole('menu', { name: 'Выбор саги' });
}

async function openSettings() {
  fireEvent.click(within(await openMenu()).getByRole('menuitem', { name: 'Настроить сагу' }));
  return screen.findByRole('dialog', { name: 'Настроить сагу' });
}

describe('the menu of the saga', () => {
  it('lists «Ваши саги» with the current one marked and the example marked, then «Новая сага», «Скачать всю сагу…» and «Настроить сагу»', async () => {
    const api = createMockApiClient();
    await api.createProject({ title: 'Песни южных островов' });
    renderSaga(api);
    const menu = await openMenu();
    expect(within(menu).getByRole('group', { name: 'Ваши саги' })).toBeTruthy();
    const items = within(menu).getAllByRole('menuitem').map((item) => item.textContent);
    expect(items.slice(-3)).toEqual(['Новая сага', 'Скачать всю сагу…', 'Настроить сагу']);
    expect(within(menu).getByRole('menuitem', { name: 'Хроники Белого порта' }).getAttribute('aria-current')).toBe('true');
  });

  // specs/2026-11-11-ux9-shelf-chat.md SC1.5 (L-54, Ф-13): the whole saga in one archive, by the window of «Скачать книгу…».
  it('downloads the whole saga in one archive: every book with text, by the kind chosen', async () => {
    const api = createMockApiClient();
    api.setScenario({ actorRole: 'owner', preset: 'normal' });
    const getBookExport = vi.spyOn(api, 'getBookExport');
    const createObjectURL = vi.fn(() => 'blob:saga');
    vi.stubGlobal('URL', Object.assign(URL, { createObjectURL, revokeObjectURL: vi.fn() }));
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => undefined);
    const { toasts } = renderSaga(api);
    fireEvent.click(within(await openMenu()).getByRole('menuitem', { name: 'Скачать всю сагу…' }));
    const window = await screen.findByRole('dialog', { name: 'Скачать всю сагу «Хроники Белого порта»' });
    expect(within(window).getByText(/каждая своим файлом, все в одном архиве \.zip/u)).toBeTruthy();
    // specs/2026-11-12-ux10-book-pictures.md BP3.2: the same choice of the drawings, their number the sum of the books.
    const books = (await api.listBooks(projectId)).data.filter((book) => book.chapterCount > 0);
    const counted = (await api.getBookDrawingCounts(projectId)).data.filter((book) => books.some((each) => each.id === book.bookId));
    const sum = counted.reduce((total, book) => total + book.drawings, 0);
    expect(sum).toBeGreaterThan(0);
    expect(await within(window).findByRole('radio', { name: `С рисунками (${sum})` })).toHaveProperty('checked', true);
    fireEvent.click(within(window).getByRole('radio', { name: /^EPUB/u }));
    fireEvent.click(within(window).getByRole('button', { name: 'Скачать' }));
    await waitFor(() => expect(toasts.push).toHaveBeenCalledWith({ message: 'Скачано: Хроники Белого порта.zip', tone: 'success' }));
    // The choice goes to every book of the archive.
    expect(getBookExport.mock.calls).toEqual(books.map((book) => [book.id, 'epub', { drawings: true }]));
    expect(click).toHaveBeenCalledTimes(1);
    click.mockRestore();
    vi.unstubAllGlobals();
  });

  it('creates another saga from «Новая сага» and opens its shelf', async () => {
    const api = createMockApiClient();
    const createProject = vi.spyOn(api, 'createProject');
    const { navigate } = renderSaga(api);
    fireEvent.click(within(await openMenu()).getByRole('menuitem', { name: 'Новая сага' }));
    const dialog = await screen.findByRole('dialog', { name: 'Новая сага' });
    fireEvent.change(within(dialog).getByRole('textbox', { name: 'Название саги' }), { target: { value: 'Сага двенадцати томов' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Создать сагу' }));
    await waitFor(() => expect(createProject).toHaveBeenCalledWith({ title: 'Сага двенадцати томов' }));
    await waitFor(() => expect(navigate).toHaveBeenCalledWith('/manuscript/books', { projectId: expect.any(String) }));
    expect(screen.queryByRole('dialog', { name: 'Новая сага' })).toBeNull();
  });

  it('creates a saga from the menu when the address names a saga that is gone', async () => {
    const api = createMockApiClient();
    const createProject = vi.spyOn(api, 'createProject');
    const { navigate } = renderSaga(api, { projectId: 'project-missing' });
    fireEvent.click(within(await openMenu('Сага не найдена')).getByRole('menuitem', { name: 'Новая сага' }));
    fireEvent.change(screen.getByRole('textbox', { name: 'Название саги' }), { target: { value: 'TEST Saga 1→12' } });
    fireEvent.click(screen.getByRole('button', { name: 'Создать сагу' }));
    await waitFor(() => expect(createProject).toHaveBeenCalledWith({ title: 'TEST Saga 1→12' }));
    await waitFor(() => expect(navigate).toHaveBeenCalledWith('/manuscript/books', { projectId: expect.any(String) }));
  });
});

describe('«Настроить сагу»', () => {
  it('renames the saga by its window', async () => {
    const api = createMockApiClient();
    api.setScenario({ actorRole: 'owner', preset: 'normal' });
    const updateProject = vi.spyOn(api, 'updateProject');
    const { toasts } = renderSaga(api);
    const dialog = await openSettings();
    // The saga of the mock has an editor besides its owner: the role is said.
    expect(await within(dialog).findByText('владелец')).toBeTruthy();
    fireEvent.change(within(dialog).getByRole('textbox', { name: 'Имя саги' }), { target: { value: 'Новый Белый порт' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Сохранить' }));
    await waitFor(() => expect(updateProject).toHaveBeenCalledWith(projectId, { title: 'Новый Белый порт' }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Настроить сагу' })).toBeNull());
    expect(toasts.push).toHaveBeenCalledWith({ message: 'Сага переименована.', tone: 'success' });
  });

  // specs/2026-11-11-ux9-shelf-chat.md SC1.1 (L-12, W-83): an author alone in his saga is told no role.
  it('says no role to an owner alone in his saga, and his role once the saga has others', async () => {
    const api = createMockApiClient();
    api.setScenario({ actorRole: 'owner', preset: 'normal' });
    const members = await api.listProjectMembers(projectId);
    const listMembers = vi.spyOn(api, 'listProjectMembers').mockResolvedValue({ ...members, data: members.data.slice(0, 1) });
    renderSaga(api);
    const dialog = await openSettings();
    await waitFor(() => expect(listMembers).toHaveBeenCalledWith(projectId, { limit: 2 }));
    expect(within(dialog).getByRole('textbox', { name: 'Имя саги' })).toBeTruthy();
    expect(within(dialog).queryByText('Ваша роль')).toBeNull();
    expect(within(dialog).queryByText('владелец')).toBeNull();
  });

  it.each(['owner', 'admin'] as const)('lets a %s delete the saga last, after the window of the deletion, with «Вернуть»', async (actorRole) => {
    const api = createMockApiClient();
    api.setScenario({ actorRole, preset: 'normal' });
    const deleteProject = vi.spyOn(api, 'deleteProject').mockResolvedValue();
    const { navigate, toasts } = renderSaga(api);
    const dialog = await openSettings();
    const buttons = within(dialog).getAllByRole('button').map((button) => button.textContent);
    expect(buttons.indexOf('Удалить сагу…')).toBeGreaterThan(-1);
    expect(within(dialog).getByText('Можно вернуть 7 дней — в «Недавно удалённых».')).toBeTruthy();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Удалить сагу…' }));
    const confirm = await screen.findByRole('dialog', { name: 'Удалить сагу «Хроники Белого порта»?' });
    expect(deleteProject).not.toHaveBeenCalled();
    fireEvent.click(within(confirm).getByRole('button', { name: 'Удалить сагу' }));
    await waitFor(() => expect(deleteProject).toHaveBeenCalledWith(projectId));
    await waitFor(() => expect(navigate).toHaveBeenCalledWith('/manuscript/books'));
    expect(toasts.push).toHaveBeenCalledWith(expect.objectContaining({ message: 'Сага «Хроники Белого порта» удалена', persistent: true }));
  });

  it('says to an editor who changes the name and deletes the saga, without the actions', async () => {
    const api = createMockApiClient();
    api.setScenario({ actorRole: 'editor', preset: 'normal' });
    renderSaga(api);
    const dialog = await openSettings();
    expect(within(dialog).getByText('редактор')).toBeTruthy();
    expect(within(dialog).getByText('Менять имя саги и удалить её может владелец или администратор.')).toBeTruthy();
    expect(within(dialog).queryByRole('button', { name: 'Удалить сагу…' })).toBeNull();
    expect(within(dialog).queryByRole('textbox', { name: 'Имя саги' })).toBeNull();
  });

  it('says to a reader of the saga his role and who changes it, without the actions', async () => {
    const api = createMockApiClient();
    api.setScenario({ actorRole: 'viewer', preset: 'normal' });
    renderSaga(api);
    const dialog = await openSettings();
    expect(within(dialog).getByText('читатель')).toBeTruthy();
    expect(within(dialog).getByText('Менять имя, книги и главы может владелец.')).toBeTruthy();
    expect(within(dialog).queryByRole('button', { name: 'Сохранить' })).toBeNull();
    expect(within(dialog).queryByRole('button', { name: 'Удалить сагу…' })).toBeNull();
  });
});
