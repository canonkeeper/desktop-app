import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../../api/mock';
import { projectId } from '../../../api/mock/repository/fixtures';
import { ApiStatusError } from '../../../shared/api';
import type { ToastInput } from '../../../ui/feedback/useToastQueue';
import { useBookRevert } from './BookRevert';

type Api = ReturnType<typeof createMockApiClient>;

function Harness({ api, book, push, onReturned }: { api: Api; book: { id: string; title: string }; push: (toast: ToastInput) => number; onReturned: () => void }) {
  const revert = useBookRevert({ api, onReturned, toasts: { push } });
  return <>
    <button disabled={revert.asking === book.id} onClick={() => revert.ask({ bookId: book.id, title: book.title })} type="button">Вернуть прежнюю версию…</button>
    {revert.element}
  </>;
}

async function updatedBook(api: Api) {
  const [book] = (await api.listBooks(projectId)).data;
  const started = await api.importBookFile(projectId, {
    file: new File(['<FictionBook>том</FictionBook>'], 'tom.fb2', { type: 'application/x-fictionbook+xml' }),
    options: { importMode: 'update_book', bookId: book!.id },
  }, { idempotencyKey: 'idem-revert-harness' });
  await api.getJob(started.job.id);
  return book!;
}

function renderHarness(api: Api, book: { id: string; title: string }) {
  const push = vi.fn((_toast: ToastInput) => 1);
  const onReturned = vi.fn();
  const client = new QueryClient({ defaultOptions: { mutations: { retry: false }, queries: { retry: false } } });
  render(<QueryClientProvider client={client}><Harness api={api} book={book} onReturned={onReturned} push={push} /></QueryClientProvider>);
  return { onReturned, push };
}

function problem(status: number, code: string) {
  return new ApiStatusError(status, { code, detail: code, requestId: 'request_test', status, title: 'Refused', type: 'about:blank' }, new Response(null, { status }));
}

// specs/2026-11-12-ux10-book-revert.md BR3.2: the one question says the words of the preview of the server, the return carries
// its fingerprint, and the line of the root says it is done.
describe('the return of a book to the version before its update', () => {
  it('asks in the words of the preview and returns the book', async () => {
    const api = createMockApiClient();
    const book = await updatedBook(api);
    const revert = vi.spyOn(api, 'revertBookUpdate');
    // As the server answers: once the book is returned, the preview of its update is gone.
    const read = api.previewBookUpdateRevert.bind(api);
    vi.spyOn(api, 'previewBookUpdateRevert').mockImplementation(async (bookId) => {
      if (revert.mock.calls.length > 0) throw problem(404, 'book.update_gone');
      return read(bookId);
    });
    // The rest that is read anew after the return answers later than the preview, as on a real network: the question
    // is still open when «gone» comes.
    const quota = api.getAccountQuota.bind(api);
    vi.spyOn(api, 'getAccountQuota').mockImplementation(async () => {
      if (revert.mock.calls.length > 0) await new Promise((resolve) => { setTimeout(resolve, 60); });
      return quota();
    });
    const { onReturned, push } = renderHarness(api, book);
    fireEvent.click(screen.getByRole('button', { name: 'Вернуть прежнюю версию…' }));
    const question = await screen.findByRole('dialog', { name: `Вернуть «${book.title}» к версии до обновления?` });
    expect(question.textContent).toContain('Вернутся: текст 1 главы.');
    expect(question.textContent).toContain('Помощник перечитает возвращённые главы.');
    const action = within(question).getByRole('button', { name: 'Вернуть версию до обновления' });
    // The voice presses it only after the author's «да» (BR2.5).
    expect(action.getAttribute('data-voice-consent')).toBe('yes');
    fireEvent.click(action);
    await waitFor(() => expect(push).toHaveBeenCalledWith(expect.objectContaining({
      message: `Книга «${book.title}» возвращена к версии до обновления`, tone: 'success',
    })));
    expect(revert).toHaveBeenCalledWith(book.id, { fingerprint: expect.stringMatching(/^[0-9a-f]{32}$/u) });
    expect(onReturned).toHaveBeenCalledWith(book.id);
    expect(screen.queryByRole('dialog')).toBeNull();
    // The walk of TEST 12.11: the books were read again before the question closed, the preview of an update already
    // returned answered «gone», and «Вернуть прежнюю версию уже нельзя…» stood beside the line of the success.
    expect(push.mock.calls.map(([toast]) => toast.message)).toEqual([`Книга «${book.title}» возвращена к версии до обновления`]);
    expect((await api.getBook(book.id)).update).toBeUndefined();
  });

  it('asks again after a return: the next update of the book has its own question', async () => {
    const api = createMockApiClient();
    const book = await updatedBook(api);
    const { push } = renderHarness(api, book);
    fireEvent.click(screen.getByRole('button', { name: 'Вернуть прежнюю версию…' }));
    fireEvent.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'Вернуть версию до обновления' }));
    await waitFor(() => expect(push).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());

    const again = await api.importBookFile(projectId, {
      file: new File(['<FictionBook>том второй</FictionBook>'], 'tom.fb2', { type: 'application/x-fictionbook+xml' }),
      options: { importMode: 'update_book', bookId: book.id },
    }, { idempotencyKey: 'idem-revert-harness-again' });
    await api.getJob(again.job.id);
    fireEvent.click(screen.getByRole('button', { name: 'Вернуть прежнюю версию…' }));
    expect(await screen.findByRole('dialog', { name: `Вернуть «${book.title}» к версии до обновления?` })).toBeTruthy();
  });

  it('keeps the book and shows the block of the tier when the text does not fit', async () => {
    const api = createMockApiClient();
    const book = await updatedBook(api);
    vi.spyOn(api, 'revertBookUpdate').mockRejectedValue(problem(409, 'quota.project_characters'));
    const { push } = renderHarness(api, book);
    fireEvent.click(screen.getByRole('button', { name: 'Вернуть прежнюю версию…' }));
    const question = await screen.findByRole('dialog', { name: `Вернуть «${book.title}» к версии до обновления?` });
    fireEvent.click(within(question).getByRole('button', { name: 'Вернуть версию до обновления' }));
    expect(await within(question).findByText('Книга осталась как сейчас.')).toBeTruthy();
    expect(push).not.toHaveBeenCalled();
  });

  it('reads the preview again when the book changed, and closes when the update is gone', async () => {
    const api = createMockApiClient();
    const book = await updatedBook(api);
    const preview = vi.spyOn(api, 'previewBookUpdateRevert');
    const revert = vi.spyOn(api, 'revertBookUpdate').mockRejectedValueOnce(problem(409, 'book.update_changed'));
    const { push } = renderHarness(api, book);
    fireEvent.click(screen.getByRole('button', { name: 'Вернуть прежнюю версию…' }));
    const question = await screen.findByRole('dialog', { name: `Вернуть «${book.title}» к версии до обновления?` });
    fireEvent.click(within(question).getByRole('button', { name: 'Вернуть версию до обновления' }));
    expect(await within(question).findByText('Книга изменилась, пока вы читали, — посмотрите, что вернётся теперь.')).toBeTruthy();
    await waitFor(() => expect(preview.mock.calls.length).toBeGreaterThanOrEqual(2));

    revert.mockRejectedValueOnce(problem(404, 'book.update_gone'));
    await waitFor(() => expect(within(question).getByRole('button', { name: 'Вернуть версию до обновления' }).hasAttribute('disabled')).toBe(false));
    fireEvent.click(within(question).getByRole('button', { name: 'Вернуть версию до обновления' }));
    await waitFor(() => expect(push).toHaveBeenCalledWith(expect.objectContaining({
      message: 'Вернуть прежнюю версию уже нельзя: прошло 7 дней или книгу уже вернули.',
    })));
    expect(screen.queryByRole('dialog')).toBeNull();
  });
});
