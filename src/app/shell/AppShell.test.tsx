import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../api/mock';
import { projectId } from '../../api/mock/repository/fixtures';
import { createAppRoute, type AppRoute } from '../../shared/navigation/app-route';
import { InShell } from './AppShell.test-helpers';

// specs/2026-11-10-ux2-shell-core.md UC1, UC3 (S3.1, S3.3): the saga of the shell, its menu and the change of the saga
// from every kind of page.

const client = () => new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });

function renderShell(api: ReturnType<typeof createMockApiClient>, route: AppRoute) {
  const navigate = vi.fn();
  render(<QueryClientProvider client={client()}>
    <InShell api={api} navigate={navigate} route={route}><h1>Содержимое</h1></InShell>
  </QueryClientProvider>);
  return navigate;
}

async function chooseOther(api: ReturnType<typeof createMockApiClient>, route: AppRoute) {
  const other = await api.createProject({ title: 'Песни южных островов' });
  const navigate = renderShell(api, route);
  const saga = await screen.findByRole('button', { name: 'Сага: Хроники Белого порта' });
  expect(saga.closest('header')).toBeTruthy();
  fireEvent.click(saga);
  const choice = await screen.findByRole('menu', { name: 'Выбор саги' });
  expect(within(choice).getByRole('menuitem', { name: 'Хроники Белого порта' }).getAttribute('aria-current')).toBe('true');
  fireEvent.click(within(choice).getByRole('menuitem', { name: 'Песни южных островов' }));
  await waitFor(() => expect(navigate).toHaveBeenCalled());
  expect(screen.queryByRole('menu', { name: 'Выбор саги' })).toBeNull();
  return { navigate, other };
}

describe('the saga of the shell', () => {
  it('names the saga of the address and marks the section of the page', async () => {
    renderShell(createMockApiClient(), createAppRoute('/memory', { projectId }));
    expect(await screen.findByRole('button', { name: 'Сага: Хроники Белого порта' })).toBeTruthy();
    // «Что помнит помощник» is a section of the group «Сага» (S2.1), not a page under the map.
    const nav = screen.getByRole('navigation', { name: 'Разделы' });
    expect(within(nav).getByRole('button', { name: 'Что помнит помощник' }).getAttribute('aria-current')).toBe('page');
    expect(within(nav).getByRole('button', { name: 'Карта' }).getAttribute('aria-current')).toBeNull();
    // The top bar names the saga, not the section: the menu and the heading of the page name it (ux2-polish PL1.1).
    expect(screen.getByRole('banner').textContent).not.toContain('Что помнит помощник');
    expect(screen.getByRole('main', { name: 'Что помнит помощник' })).toBeTruthy();
  });

  // specs/2026-11-10-ux6-drawings-section.md DS1.1: «Рисунки» is the one section of the drawings, its own lists included.
  it('marks «Рисунки» on its page and on «Мои картинки»', async () => {
    renderShell(createMockApiClient(), createAppRoute('/drawing', { projectId, view: 'mine' }));
    await screen.findByRole('button', { name: 'Сага: Хроники Белого порта' });
    expect(screen.getByRole('button', { name: 'Рисунки' }).getAttribute('aria-current')).toBe('page');
    expect(screen.getByRole('banner').textContent).not.toContain('Рисунки');
  });

  it('keeps the last saga of the author on a page without a saga in the address', async () => {
    renderShell(createMockApiClient(), createAppRoute('/tariff'));
    expect(await screen.findByRole('button', { name: 'Сага: Хроники Белого порта' })).toBeTruthy();
    expect(screen.getByRole('navigation', { name: 'Разделы' }).querySelector('[aria-current]')).toBeNull();
  });
});

describe('the change of the saga (S3.3)', () => {
  it('keeps the author in the same section of the saga chosen', async () => {
    const { navigate, other } = await chooseOther(createMockApiClient(), createAppRoute('/memory', { projectId }));
    expect(navigate).toHaveBeenCalledWith('/memory', { projectId: other.id });
  });

  it('opens the list of «Письмо» from a workplace of a chapter', async () => {
    const { navigate, other } = await chooseOther(createMockApiClient(), createAppRoute('/writing', { projectId, draft: 'draft-1' }));
    expect(navigate).toHaveBeenCalledWith('/writing', { projectId: other.id });
  });

  it('opens the list of «Рисунки» from a workplace of a drawing', async () => {
    const { navigate, other } = await chooseOther(createMockApiClient(), createAppRoute('/drawing', { projectId, drawing: 'drawing-1' }));
    expect(navigate).toHaveBeenCalledWith('/drawing', { projectId: other.id });
  });

  it('opens the shelf of the saga chosen from the reader', async () => {
    const { navigate, other } = await chooseOther(createMockApiClient(), createAppRoute('/manuscript/books', { projectId, bookId: 'book-02', mode: 'read', chapterId: 'chapter-12' }));
    expect(navigate).toHaveBeenCalledWith('/manuscript/books', { projectId: other.id });
  });

  it('keeps the notes open in the saga chosen', async () => {
    const { navigate, other } = await chooseOther(createMockApiClient(), createAppRoute('/notes', { projectId }));
    expect(navigate).toHaveBeenCalledWith('/notes', expect.objectContaining({ returnTo: `/manuscript/books?projectId=${other.id}` }));
  });

  it('opens the shelf of the saga chosen from a page without a saga', async () => {
    const { navigate, other } = await chooseOther(createMockApiClient(), createAppRoute('/tariff'));
    expect(navigate).toHaveBeenCalledWith('/manuscript/books', { projectId: other.id });
  });
});

describe('the sections of the shell', () => {
  it('opens a section of the same saga, and the shelf with the book and the chapter read', async () => {
    const navigate = renderShell(createMockApiClient(), createAppRoute('/manuscript/books', { projectId, bookId: 'book-02', mode: 'read', chapterId: 'chapter-12' }));
    await screen.findByRole('button', { name: 'Сага: Хроники Белого порта' });
    const nav = screen.getByRole('navigation', { name: 'Разделы' });
    // The reader is «Книги» (S2.2).
    expect(within(nav).getByRole('button', { name: 'Книги' }).getAttribute('aria-current')).toBe('page');
    fireEvent.click(within(nav).getByRole('button', { name: 'Книги' }));
    expect(navigate).toHaveBeenLastCalledWith('/manuscript/books', { projectId, bookId: 'book-02', chapterId: 'chapter-12' });
    fireEvent.click(within(nav).getByRole('button', { name: 'Письмо' }));
    expect(navigate).toHaveBeenLastCalledWith('/writing', { projectId });
    fireEvent.click(within(nav).getByRole('button', { name: 'Заметки' }));
    expect(navigate).toHaveBeenLastCalledWith('/notes', expect.objectContaining({ returnTo: expect.stringContaining('/manuscript/books') }));
  });

  it('opens «Недавно удалённые» over the page and gives the focus back to its item', async () => {
    renderShell(createMockApiClient(), createAppRoute('/saga', { projectId }));
    await screen.findByRole('button', { name: 'Сага: Хроники Белого порта' });
    const item = screen.getByRole('button', { name: 'Недавно удалённые' });
    fireEvent.click(item);
    const dialog = await screen.findByRole('dialog', { name: 'Недавно удалённые' });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Закрыть недавно удалённые' }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Недавно удалённые' })).toBeNull());
    expect(document.activeElement).toBe(item);
  });

  it('has no column and names the first saga on the shelf of an author without a saga', async () => {
    const api = createMockApiClient();
    api.setScenario({ actorRole: 'owner', preset: 'empty-lists' });
    renderShell(api, createAppRoute('/manuscript/books'));
    await waitFor(() => expect(screen.getByRole('banner').textContent).toContain('Первая сага'));
    expect(screen.queryByRole('navigation', { name: 'Разделы' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Поиск по книгам' })).toBeNull();
  });

  // specs/2026-11-10-ux3-first-screen.md FS1.1: a page of a saga without a saga is the first screen, not a dead end.
  it('leads any page of a saga to the first screen while the author has no saga', async () => {
    const api = createMockApiClient();
    api.setScenario({ actorRole: 'owner', preset: 'empty-lists' });
    for (const path of ['/chat', '/writing', '/saga', '/notes']) {
      const navigate = renderShell(api, createAppRoute(path, { projectId: 'project-gone' }));
      await waitFor(() => expect(navigate).toHaveBeenCalledWith('/manuscript/books', undefined, { replace: true }));
      expect(screen.queryByText('Содержимое')).toBeNull();
      expect(screen.queryByRole('navigation', { name: 'Разделы' })).toBeNull();
      expect(screen.getByRole('banner').textContent).toContain('Первая сага');
      cleanup();
    }
  });

  // FS1.4, FS2.1: «Поговорить» or a proofreading of the first screen founded «Моя сага»; the screen says it and renames it.
  it('names a saga the author did not name and opens «Настроить сагу» to rename it', async () => {
    const api = createMockApiClient();
    const saga = await api.createProject({ title: 'Моя сага' });
    renderShell(api, createAppRoute('/writing', { projectId: saga.id }));
    const line = await screen.findByRole('note', { name: 'Сага без вашего названия' });
    expect(line.textContent).toContain('Сага «Моя сага» создана — назовите её по-своему');
    fireEvent.click(within(line).getByRole('button', { name: 'Переименовать' }));
    expect(await screen.findByRole('dialog', { name: 'Настроить сагу' })).toBeTruthy();
  });

  it('says nothing of the name a saga with books or a name of the author has', async () => {
    renderShell(createMockApiClient(), createAppRoute('/writing', { projectId }));
    await screen.findByRole('button', { name: 'Сага: Хроники Белого порта' });
    expect(screen.queryByRole('note', { name: 'Сага без вашего названия' })).toBeNull();
  });

  it('says to a reader of the saga who changes it', async () => {
    const api = createMockApiClient();
    api.setScenario({ actorRole: 'viewer', preset: 'normal' });
    renderShell(api, createAppRoute('/manuscript/books', { projectId }));
    expect(await screen.findByRole('note', { name: 'Вы читаете эту сагу' })).toBeTruthy();
  });

  it('opens «Поиск по книгам» over the page of the saga', async () => {
    renderShell(createMockApiClient(), createAppRoute('/chat', { projectId }));
    fireEvent.click(await screen.findByRole('button', { name: 'Поиск по книгам' }));
    expect(await screen.findByRole('dialog', { name: 'Поиск по книгам' })).toBeTruthy();
  });
});

// specs/2026-11-10-ux2-patterns-states.md PS1.2, UC4: the width of the content and «← К …» are the shell's.
describe('the content of the shell', () => {
  it('stands «Тариф» in the width of reading and the shelf in the whole width', async () => {
    renderShell(createMockApiClient(), createAppRoute('/tariff'));
    await screen.findByRole('button', { name: 'Сага: Хроники Белого порта' });
    expect(screen.getByRole('main').querySelector('[data-content-width]')!.getAttribute('data-content-width')).toBe('reading');
  });

  it('offers the way back to the chat the author came from, a step of the history', async () => {
    const route = { ...createAppRoute('/manuscript/books', { projectId, bookId: 'book-02', chapterId: 'chapter-12', mode: 'read' }), from: { section: 'chat' as const, name: 'Кто такой Сава?' } };
    const navigate = renderShell(createMockApiClient(), route);
    const line = await screen.findByRole('navigation', { name: 'Возврат' });
    fireEvent.click(within(line).getByRole('button', { name: 'К чату «Кто такой Сава?»' }));
    expect(navigate).toHaveBeenLastCalledWith('/manuscript/books', undefined, { back: true });
  });

  it('has no way back on a page opened from the menu or by its address', async () => {
    renderShell(createMockApiClient(), createAppRoute('/manuscript/books', { projectId, bookId: 'book-02', chapterId: 'chapter-12', mode: 'read' }));
    await screen.findByRole('button', { name: 'Сага: Хроники Белого порта' });
    expect(screen.queryByRole('navigation', { name: 'Возврат' })).toBeNull();
  });
});
