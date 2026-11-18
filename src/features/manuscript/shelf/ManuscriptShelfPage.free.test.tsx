import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../../api/mock';
import { projectId } from '../../../api/mock/repository/fixtures';
import { MockApiError } from '../../../api/mock/scenarios';
import { parseRoute } from '../../../shared/navigation/app-route';
import { FakeWebSocket } from '../../../test/FakeWebSocket';
import { putOnFree } from '../../../test/illustration-api';
import { ToastsProvider } from '../../../ui/feedback/Toast';
import { ManuscriptShelfPage } from './ManuscriptShelfPage';

beforeEach(() => vi.stubGlobal('WebSocket', FakeWebSocket));
afterEach(() => {
  FakeWebSocket.reset();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

type Api = ReturnType<typeof createMockApiClient>;
const fileInput = 'Файлы книг: FB2, EPUB, Word (.docx), RTF, ODT или текст (.txt, .md)';
const epub = (name: string) => new File(['book'], name, { type: 'application/epub+zip' });

/** The author of the saga on «Бесплатно» (specs/2026-11-18-free-tier.md FT1): his books loaded once. */
async function renderFreeShelf(arrange?: (api: Api) => Promise<void> | void) {
  const api = createMockApiClient();
  api.setScenario({ actorRole: 'owner', preset: 'normal' });
  await putOnFree(api);
  await arrange?.(api);
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(<QueryClientProvider client={queryClient}><ToastsProvider>
    <ManuscriptShelfPage api={api} navigate={vi.fn()} route={parseRoute(`http://localhost/manuscript/books?projectId=${projectId}`)} />
  </ToastsProvider></QueryClientProvider>);
  await screen.findByRole('article', { name: 'Книга III. Северный суд' });
  return api;
}

/** The account has room for one more book: limits of the writer's own above his tier, fewer books loaded than that. */
function oneBookFits(api: Api) {
  const read = api.getAccountQuota.bind(api);
  vi.spyOn(api, 'getAccountQuota').mockImplementation(async () => {
    const quota = await read();
    const books = { used: quota.account.books.used, limit: quota.booksLoaded + 1 };
    return {
      ...quota, account: { ...quota.account, books },
      ownedProjects: quota.ownedProjects.map((project) => ({ ...project, sources: { ...project.sources, books: { ...project.sources.books, limit: 60 } } })),
    };
  });
}

// FT3.1: the shelf says a book loaded once before the author presses — «Добавить книги» and «Новая версия из файла…» stay
// in their places closed with their reasons, and the window says it before the files go.
describe('the shelf of a tier where a book is loaded once', () => {
  it('closes «Добавить книги» with its reason once the books of the account reached their limit', async () => {
    await renderFreeShelf();
    const add = await screen.findByRole('button', { name: 'Добавить книги' });
    await waitFor(() => expect(add.getAttribute('aria-disabled')).toBe('true'));
    fireEvent.click(add);
    expect(await screen.findByText('На вашем тарифе одна книга, и загружается она один раз')).toBeTruthy();
    // The button says it: no quiet line of the books of the saga beside it (board 30·53).
    expect(document.querySelector('[data-space-limit]')).toBeNull();
    expect(document.body.textContent).not.toContain('станет меньше');
  });

  it('closes «Новая версия из файла…» of a book with its reason', async () => {
    await renderFreeShelf();
    const book = screen.getByRole('article', { name: 'Книга III. Северный суд' });
    fireEvent.click(within(book).getByRole('button', { name: 'Действия книги Книга III. Северный суд' }));
    const menu = await screen.findByRole('menu', { name: 'Действия книги' });
    const item = within(menu).getByRole('menuitem', { name: /Новая версия из файла…/u });
    await waitFor(() => expect(item.getAttribute('aria-disabled')).toBe('true'));
    expect(item.textContent).toContain('На вашем тарифе книга загружается один раз');
  });

  it('opens the window for one file and says before it goes that the book will take no new version', async () => {
    const api = await renderFreeShelf(oneBookFits);
    const create = vi.spyOn(api, 'createImportQueue');
    await waitFor(() => expect(screen.getByRole('button', { name: 'Добавить книги' }).getAttribute('aria-disabled')).toBeNull());
    // A file named as a book of the shelf asks nothing about replacing it: a book here takes no new version.
    fireEvent.change(screen.getByLabelText(fileInput), { target: { files: [epub('Книга III. Северный суд.epub')] } });
    const dialog = await screen.findByRole('dialog', { name: 'Порядок томов' });
    const remarks = within(dialog).getByRole('list', { name: 'Замечания' });
    expect(within(remarks).getByText('На вашем тарифе книга загружается один раз: заменить её файлом потом нельзя')).toBeTruthy();
    expect(dialog.textContent).not.toContain('Новая версия из файла');
    expect(create).not.toHaveBeenCalled();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Загрузить книгу' }));
    await waitFor(() => expect(create).toHaveBeenCalledTimes(1));
  });

  // FT3.2 (board 35·30): a refusal the page could not foresee — another tab, a drop before the counters — is the one block of
  // the shelf by the code.
  it('says the refusal of the server as the block of the shelf', async () => {
    const api = await renderFreeShelf(oneBookFits);
    await waitFor(() => expect(screen.getByRole('button', { name: 'Добавить книги' }).getAttribute('aria-disabled')).toBeNull());
    vi.spyOn(api, 'createImportQueue').mockRejectedValue(new MockApiError(409, 'A book of the tier is loaded once.', 'createImportQueue', 'quota.book_reloads'));
    fireEvent.change(screen.getByLabelText(fileInput), { target: { files: [epub('Последний свет.epub')] } });
    const dialog = await screen.findByRole('dialog', { name: 'Порядок томов' });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Загрузить книгу' }));
    // The live books are below the limit of the account here: the words of the second case.
    expect(await screen.findByRole('alert', { name: 'Книга на вашем тарифе загружается один раз' })).toBeTruthy();
    expect(screen.getByText('Новую версию из файла и книгу вместо удалённой загрузить нельзя. Удалённую книгу можно вернуть в «Недавно удалённых» в течение 7 дней.')).toBeTruthy();
  });
});
