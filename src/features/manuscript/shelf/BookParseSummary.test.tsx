import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { Book } from '../../../entities/manuscript/api';
import { BookParseSummary } from './BookParseSummary';
import { looksLikeNoChapter, namedByFile } from './parse-summary';

const book = (id: string, title: string, chapterCount: number, place: number) => ({
  id, projectId: 'project-1', title, chapterCount, place, wordCount: 1000,
}) as Book;

function chapters(titles: string[]) {
  return {
    data: titles.map((title, index) => ({ id: `chapter-${index + 1}`, title, bookId: 'book-1' })),
    meta: { hasMore: false, nextCursor: null },
  };
}

// specs/2026-11-11-ux7-upload.md UP4.2: how a book was read — its chapters, what looks like no chapter with «Убрать из
// книги», a book named by its file with «Переименовать»; the actions are the operations the shelf has.
describe('«Как разобралась книга»', () => {
  it('shows the chapters, offers to take a page that is no chapter out and gives it back', async () => {
    const api = {
      listChapters: vi.fn(async () => chapters(['1. Пролог', '2. Ледяная гавань', 'Об этом электронном издании'])),
      deleteChapter: vi.fn(async () => undefined),
      restoreChapter: vi.fn(async () => ({})),
    };
    const toasts = { push: vi.fn(), dismiss: vi.fn() };
    const onRename = vi.fn();
    render(<QueryClientProvider client={new QueryClient()}>
      <BookParseSummary api={api as never} books={[book('book-1', '03-severny-sud', 3, 3)]} onClose={vi.fn()} onRead={vi.fn()}
        onRename={onRename} sourceNames={{ 'book-1': '03-severny-sud.epub' }} toasts={toasts} />
    </QueryClientProvider>);
    const dialog = screen.getByRole('dialog', { name: 'Как разобралась книга «03-severny-sud»' });
    expect(await within(dialog).findByText('3 главы — так вы будете читать книгу и так её читает помощник.')).toBeTruthy();
    expect(within(within(dialog).getByRole('list', { name: 'Главы «03-severny-sud»' })).getAllByRole('listitem').map((item) => item.textContent))
      .toEqual(['1. Пролог', '2. Ледяная гавань', 'Об этом электронном издании']);
    expect(within(dialog).getByText('Похоже, это не глава: «Об этом электронном издании».')).toBeTruthy();

    fireEvent.click(within(dialog).getByRole('button', { name: 'Убрать из книги' }));
    await waitFor(() => expect(api.deleteChapter).toHaveBeenCalledWith('chapter-3'));
    await waitFor(() => expect(toasts.push).toHaveBeenCalledWith(expect.objectContaining({ message: '«Об этом электронном издании» убрана из книги' })));

    fireEvent.click(within(dialog).getByRole('button', { name: 'Переименовать' }));
    expect(onRename).toHaveBeenCalledWith(expect.objectContaining({ id: 'book-1' }));
  });

  it('lists the books of an upload first and opens one of them', async () => {
    const api = { listChapters: vi.fn(async () => chapters(['Глава 1'])), deleteChapter: vi.fn(), restoreChapter: vi.fn() };
    render(<QueryClientProvider client={new QueryClient()}>
      <BookParseSummary api={api as never} books={[book('book-1', 'Северный суд', 14, 3), book('book-2', 'Последний свет', 12, 4)]}
        onClose={vi.fn()} onRead={vi.fn()} onRename={vi.fn()} sourceNames={{}} toasts={{ push: vi.fn(), dismiss: vi.fn() }} />
    </QueryClientProvider>);
    const dialog = screen.getByRole('dialog', { name: 'Как разобрались книги' });
    const books = within(within(dialog).getByRole('list', { name: 'Книги загрузки' })).getAllByRole('listitem');
    expect(books.map((item) => item.firstChild?.textContent)).toEqual(['Книга 3 «Северный суд» · 14 глав', 'Книга 4 «Последний свет» · 12 глав']);
    fireEvent.click(within(books[1]!).getByRole('button', { name: 'Посмотреть' }));
    expect(screen.getByRole('dialog', { name: 'Как разобралась книга «Последний свет»' })).toBeTruthy();
  });

  it('knows the pages of a publisher and the names of files', () => {
    expect(['18. Об этом электронном издании', 'Аннотация', 'Оглавление', 'От издательства'].every(looksLikeNoChapter)).toBe(true);
    expect(['Пролог', 'Глава 3. Содержание письма', 'Эпилог'].some(looksLikeNoChapter)).toBe(false);
    expect(namedByFile('Том 5', 'Том 5.fb2')).toBe(true);
    expect(namedByFile('tom_5')).toBe(true);
    expect(namedByFile('03-severny-sud')).toBe(true);
    expect(namedByFile('Северный суд')).toBe(false);
  });
});
