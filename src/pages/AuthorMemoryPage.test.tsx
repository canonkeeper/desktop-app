import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../api/mock';
import { projectId } from '../api/mock/repository/fixtures';
import { createAppRoute } from '../shared/navigation/app-route';
import { InShell } from '../app/shell/AppShell.test-helpers';
import { AuthorMemoryPage } from './AuthorMemoryPage';

afterEach(cleanup);

function renderPage(api: ReturnType<typeof createMockApiClient>, query: Record<string, string> = {}) {
  const navigate = vi.fn();
  const route = createAppRoute('/memory', query);
  render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
    <InShell api={api} navigate={navigate} route={route}><AuthorMemoryPage api={api} navigate={navigate} route={route} /></InShell>
  </QueryClientProvider>);
  return navigate;
}

function editor(preset: 'normal' | 'empty-lists' = 'normal') {
  const api = createMockApiClient();
  api.setScenario({ actorRole: 'editor', preset });
  return api;
}

describe('AuthorMemoryPage (R4.5)', () => {
  // specs/2026-11-10-ux2-shell.md S2.1: «Что помнит помощник» is a section of the menu in the group «Сага».
  it('is a section of the menu, and an editor may write', async () => {
    renderPage(editor());
    expect(await screen.findByRole('button', { name: 'Записать самому' })).toBeTruthy();
    expect(screen.queryAllByRole('button', { name: 'Память' })).toHaveLength(0);
    const sections = screen.getByRole('navigation', { name: 'Разделы' });
    expect(within(sections).getByRole('button', { name: 'Что помнит помощник' }).getAttribute('aria-current')).toBe('page');
  });

  // specs/2026-11-11-ux9-shelf-chat.md SC2.4 (W-31, W-24): the width of the shell, no column of its own; the filter and the
  // button say the author's words.
  it('takes the width of the shell and says «Что это», «Всё», «Записать самому»', async () => {
    renderPage(editor());
    const heading = await screen.findByRole('heading', { level: 1, name: 'Что помнит помощник' });
    const page = heading.closest('header')!.parentElement!;
    expect(getComputedStyle(page).maxWidth).toMatch(/^(none|)$/u);
    expect(await screen.findByRole('button', { name: /^Что это/u })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Записать самому' })).toBeTruthy();
  });

  // specs/2026-11-10-ux2-memory-page.md MP1.1: one heading, one line of what the helper does with it, two parts in the address.
  it('is one page with two parts, the open one in the address', async () => {
    const navigate = renderPage(editor());
    expect(await screen.findByRole('heading', { level: 1, name: 'Что помнит помощник' })).toBeTruthy();
    expect(await screen.findByText('Помощник учитывает это в ответах и при письме глав. Заметки он не читает.')).toBeTruthy();
    const parts = screen.getByRole('tablist', { name: 'Части страницы' });
    const own = within(parts).getByRole('tab', { name: 'Вы просили помнить' });
    const materials = within(parts).getByRole('tab', { name: 'Ваши материалы' });
    expect(own.getAttribute('aria-selected')).toBe('true');
    expect(screen.getByRole('tabpanel', { name: 'Вы просили помнить' })).toBeTruthy();

    // The arrows go along the tabs; the focus stays on the tab, the saga stays in the address.
    own.focus();
    fireEvent.keyDown(own, { key: 'ArrowRight' });
    expect(document.activeElement).toBe(materials);
    expect(navigate).toHaveBeenLastCalledWith('/memory', { projectId, part: 'materials' }, { replace: true });
    fireEvent.click(own);
    expect(navigate).toHaveBeenLastCalledWith('/memory', { projectId }, { replace: true });
  });

  it('opens the part «Ваши материалы» by its address', async () => {
    renderPage(editor(), { projectId, part: 'materials' });
    const materials = await screen.findByRole('tab', { name: 'Ваши материалы' });
    expect(materials.getAttribute('aria-selected')).toBe('true');
    const part = screen.getByRole('tabpanel', { name: 'Ваши материалы' });
    expect(await within(part).findByText('Пока записей нет')).toBeTruthy();
    // The part of the records keeps its tools; the materials have their own «Записать самому» (MR2.2).
    expect(screen.queryByRole('searchbox', { name: 'Найти в записях' })).toBeNull();
    expect(within(part).getByRole('button', { name: 'Записать самому' })).toBeTruthy();
  });

  // MP1.2: an empty part says what comes here and leads to the chat.
  it('says what the empty part of the records is for and opens the chat', async () => {
    const navigate = renderPage(editor());
    expect(await screen.findByText('Пока ничего нет')).toBeTruthy();
    expect(screen.getByText(/Скажите помощнику в чате: «запомни…»/u)).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Открыть чат' }));
    expect(navigate).toHaveBeenCalledWith('/chat', { projectId });
  });

  it('a viewer reads only', async () => {
    const api = createMockApiClient();
    api.setScenario({ actorRole: 'viewer', preset: 'normal' });
    renderPage(api);
    expect(await screen.findByText('Записи появятся, когда владелец саги их добавит.')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Записать самому' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Открыть чат' })).toBeNull();
  });
});
