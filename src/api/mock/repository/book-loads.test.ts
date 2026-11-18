import { describe, expect, it } from 'vitest';
import { putOnFree } from '../../../test/illustration-api';
import { createMockApiClient } from '../index';
import { projectId } from './fixtures';

const epub = (name: string) => new File(['book'], name, { type: 'application/epub+zip' });

async function refusal(work: Promise<unknown>) {
  try {
    await work;
  } catch (error) {
    return (error as { code?: string }).code;
  }
  return 'passed';
}

// specs/2026-11-18-free-tier.md FT1: the mock keeps the journal of the books of an account and refuses as the fence of the
// server does — it is not kinder than the server.
describe('a book loaded once in the mock', () => {
  it('counts every book that came into the sagas, a deleted one too', async () => {
    const api = createMockApiClient();
    await putOnFree(api);
    const before = await api.getAccountQuota();
    expect(before).toMatchObject({ bookReloads: false });
    const { data: books } = await api.listBooks(projectId);
    await api.deleteBook(books[0]!.id);
    const after = await api.getAccountQuota();
    expect(after.booksLoaded).toBe(before.booksLoaded);
    expect(after.account.books.used).toBe(before.account.books.used - 1);
  });

  it('refuses a new version from a file, a book without text and a new book past the books of the account', async () => {
    const api = createMockApiClient();
    await putOnFree(api);
    const { data: books } = await api.listBooks(projectId);
    expect(await refusal(api.importBookFile(projectId, { file: epub('Новая версия.epub'), options: { importMode: 'update_preview', bookId: books[0]!.id } },
      { idempotencyKey: 'idem-update-preview' }))).toBe('quota.book_reloads');
    expect(await refusal(api.importBookFile(projectId, { file: epub('Новая версия.epub'), options: { importMode: 'update_book', bookId: books[0]!.id } },
      { idempotencyKey: 'idem-update-book' }))).toBe('quota.book_reloads');
    expect(await refusal(api.createBook(projectId, { title: 'Пустая' }))).toBe('quota.book_reloads');
    expect(await refusal(api.createImportQueue(projectId, { items: [{ clientItemId: 'one', position: 1, sourceFileName: 'Новая.epub' }] },
      { idempotencyKey: 'idem-queue' }))).toBe('quota.book_reloads');
  });

  it('lets a tier whose book loads again update a book from a file', async () => {
    const api = createMockApiClient();
    const { data: books } = await api.listBooks(projectId);
    expect(await refusal(api.importBookFile(projectId, { file: epub('Новая версия.epub'), options: { importMode: 'update_preview', bookId: books[0]!.id } },
      { idempotencyKey: 'idem-paid' }))).toBe('passed');
  });
});
