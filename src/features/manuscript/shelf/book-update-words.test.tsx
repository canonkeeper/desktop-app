import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, renderHook, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../../api/mock';
import { projectId } from '../../../api/mock/repository/fixtures';
import { bookStateOf } from '../../../entities/manuscript/preparation-copy';
import type { Book } from '../../../entities/manuscript/api';
import { ApiStatusError } from '../../../shared/api';
import type { ToastInput } from '../../../ui/feedback/useToastQueue';
import { revertQuestion, updateLine } from './book-revert-words';
import { useBookUpdateWords } from './book-update-words';
import { BookParseSummary } from './BookParseSummary';
import { BookUpdatePreview, type BookUpdatePreviewResult } from './BookUpdatePreview';
import { useBookRevert } from './BookRevert';
import { bookUpdateTitle } from '../../../app/shell/recently-deleted-words';
import { SagaSectionsProvider } from '../../../ui/saga-sections';

// specs/2026-11-15-desk-obsidian-book-web.md OU3.1: without the provider of an edition — the site — every line of the update of a
// book is the line of the site, to the sign; the other lines of the site keep the tests they had.
describe('the words of the update of a book without an edition of its own (the site)', () => {
  it('are the words of the site', () => {
    const { result } = renderHook(() => useBookUpdateWords());
    const words = result.current;
    expect(words.updating).toEqual({
      mark: 'Обновляется из файла',
      wait: 'Дождитесь, пока книга обновится из файла.',
      already: 'Книга уже обновляется из файла.',
      noDelete: 'Пока книга обновляется из файла, удалить её нельзя.',
      noRevert: 'Книга сейчас обновляется из файла — вернуть прежнюю версию можно будет после этого.',
    });
    expect(words.edits).toEqual({ made: 'Правки, сделанные на сайте', mark: 'есть правки с сайта' });
    expect(words.forecast).toBe(true);
    expect(updateLine({ at: new Date(2026, 10, 3, 9, 0).toISOString() }, Date.now(), words)).toBe('Обновлена из файла 3 ноября');
    expect(bookUpdateTitle({ title: 'Застава' } as never, words)).toBe('Книга «Застава» обновлена из файла');
    const chapter = (title: string) => ({ id: title, title });
    const preview = { bookId: 'b', bookTitle: 'Застава', fingerprint: 'f', text: [], place: [], returned: [], kept: [] } as never as Parameters<typeof revertQuestion>[0];
    expect(revertQuestion({ ...preview, removed: [chapter('Утро')] }, words).line).toBe('В «Недавно удалённые» уйдёт глава «Утро» — её добавил файл.');
    expect(revertQuestion({ ...preview, removed: [chapter('Утро'), chapter('Река')] }, words).line).toBe('Главы, которые добавил файл (2), уйдут в «Недавно удалённые».');
    expect(revertQuestion({ ...preview, removed: [] }, words).line).toBe('Всё, что изменил файл, уже поменяли после обновления — книга останется как сейчас.');
    expect(bookStateOf({ id: 'b', chapterCount: 3, status: 'ready' }, undefined, true, words.updating.mark)?.word).toBe('Обновляется из файла');
    expect(words.useFolderBooks(projectId).size).toBe(0);
    expect(words.useSourceLines()('job')).toBeNull();
  });

  it('name a book by its file in «Как разобралась книга»', async () => {
    const api = { listChapters: vi.fn(async () => ({ data: [], meta: { hasMore: false, nextCursor: null } })), deleteChapter: vi.fn(), restoreChapter: vi.fn() };
    render(<QueryClientProvider client={new QueryClient()}>
      <BookParseSummary api={api as never} books={[{ id: 'book-1', projectId, title: 'Том 5', chapterCount: 1, place: 1 } as Book]} onClose={vi.fn()}
        onRead={vi.fn()} onRename={vi.fn()} sourceNames={{ 'book-1': 'Том 5.md' }} toasts={{ push: vi.fn(), dismiss: vi.fn() }} />
    </QueryClientProvider>);
    expect(await screen.findByText('Книга названа по имени файла: «Том 5».')).toBeTruthy();
  });

  it('say what becomes of the notes and the drawings of the chapters a file takes away', () => {
    const result = {
      type: 'import', changedChapters: 0, removedChapters: 1, unchangedChapters: 2,
      preview: { chapters: [{ change: 'removed', order: 3, title: 'Ночной дозор' }], newCards: 0, threads: 0 },
    } as never as BookUpdatePreviewResult;
    const { unmount } = render(<BookUpdatePreview result={result} />);
    expect(screen.getByText('Заметки и рисунки уходящих глав сохранятся и вернутся вместе с главой из «Недавно удалённых».')).toBeTruthy();
    unmount();
    // A build without «Рисунки» does not name the drawings.
    render(<SagaSectionsProvider value={['manuscript', 'chat']}><BookUpdatePreview result={result} /></SagaSectionsProvider>);
    expect(screen.getByText('Заметки уходящих глав сохранятся и вернутся вместе с главой из «Недавно удалённых».')).toBeTruthy();
  });

  it('say a book being updated from its file at «Вернуть прежнюю версию…»', async () => {
    const api = createMockApiClient();
    const [book] = (await api.listBooks(projectId)).data;
    vi.spyOn(api, 'previewBookUpdateRevert').mockRejectedValue(new ApiStatusError(409, {
      code: 'book.busy', detail: 'busy', requestId: 'request_test', status: 409, title: 'Busy', type: 'about:blank',
    }, new Response(null, { status: 409 })));
    const push = vi.fn((_toast: ToastInput) => 1);
    function Harness() {
      const revert = useBookRevert({ api, toasts: { push } });
      return <><button onClick={() => revert.ask({ bookId: book!.id, title: book!.title })} type="button">Вернуть</button>{revert.element}</>;
    }
    render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><Harness /></QueryClientProvider>);
    fireEvent.click(within(document.body).getByRole('button', { name: 'Вернуть' }));
    await waitFor(() => expect(push).toHaveBeenCalledWith(expect.objectContaining({
      message: 'Книга сейчас обновляется из файла — вернуть прежнюю версию можно будет после этого.',
    })));
  });
});
