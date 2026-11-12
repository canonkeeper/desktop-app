import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { useLayoutEffect, useState } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../../api/mock';
import { bookId, projectId } from '../../../api/mock/repository/fixtures';
import { manuscriptQueryKeys, useManuscriptShelfQuery, type Book, type ProjectJob } from '../../../entities/manuscript/api';
import { AccountSessionContext } from '../../../shared/session/account-session';
import { useToastQueue, type ToastInput } from '../../../ui/feedback/useToastQueue';
import { useBookFileUpdate } from './BookFileUpdate';
import { BookUpdateChanges, useBookUpdateLines, type BookChanges } from './BookUpdateLines';

/**
 * The shelf with the shell around it: the line of the result is the shell's (specs/2026-11-11-ux7-trash.md TB4.1), which follows
 * the updates the server lists in work (specs/2026-11-12-ux10-book-revert.md BR3.4).
 */
function Harness({ api, book, lines, onNotice, onRevert }: {
  api: ReturnType<typeof createMockApiClient>;
  book: Book;
  lines: (toast: ToastInput) => number;
  onNotice: (message: string) => void;
  onRevert: () => void;
}) {
  const update = useBookFileUpdate({ api, canUpdate: true, onNotice, projectId });
  const shelf = useManuscriptShelfQuery(api, { projectId }).data;
  const [changes, setChanges] = useState<BookChanges | null>(null);
  useBookUpdateLines({ api, books: shelf?.books ?? [], jobs: shelf?.jobs ?? [], onChanges: setChanges, toasts: { push: lines } });
  const changed = shelf?.books.find((item) => item.id === changes?.bookId) ?? null;
  return <>
    <button onClick={() => update.choose(book)} type="button">Обновить из файла</button>
    {update.element}
    {changes ? (
      <BookUpdateChanges api={api} asking={false} book={changed} canRevert jobId={changed?.update?.jobId ?? changes.jobId}
        onClose={() => setChanges(null)} onRevert={onRevert} />
    ) : null}
  </>;
}

async function renderHarness() {
  const api = createMockApiClient();
  const book = (await api.listBooks(projectId)).data.find((item) => item.id === bookId)!;
  const onNotice = vi.fn();
  const lines = vi.fn((_toast: ToastInput) => 1);
  const onRevert = vi.fn();
  // «Написать нам» of the site: the window of the shell, which the session of the pages opens.
  const onOpenContact = vi.fn();
  const importBookFile = vi.spyOn(api, 'importBookFile');
  const client = new QueryClient({ defaultOptions: { mutations: { retry: false }, queries: { retry: false } } });
  const view = render(
    <QueryClientProvider client={client}><AccountSessionContext.Provider value={{ busy: false, onOpenContact }}>
      <Harness api={api} book={book} lines={lines} onNotice={onNotice} onRevert={onRevert} />
    </AccountSessionContext.Provider></QueryClientProvider>,
  );
  // The shelf has read the import constraints before the author opens a book menu.
  await waitFor(() => expect(client.getQueryState(manuscriptQueryKeys.importConstraints(projectId))?.status).toBe('success'));
  return { api, book, importBookFile, lines, onNotice, onOpenContact, onRevert, view };
}

/**
 * The shelf of the shell around the update (specs/2026-11-12-ux12-shell.md SH2.2): the line of the update in work stands
 * where the notices of the shelf stand, the line of its result comes in the area of the root; every frame is noted.
 */
function ShelfInTheShell({ api, book, frames }: {
  api: ReturnType<typeof createMockApiClient>;
  book: Book;
  frames: Array<{ working: boolean; result: boolean }>;
}) {
  const [notice, setNotice] = useState('');
  const update = useBookFileUpdate({ api, canUpdate: true, onNotice: setNotice, projectId });
  const shelf = useManuscriptShelfQuery(api, { projectId }).data;
  const toasts = useToastQueue();
  useBookUpdateLines({ api, books: shelf?.books ?? [], jobs: shelf?.jobs ?? [], onChanges: () => undefined, toasts });
  const line = notice || update.working || '';
  const result = toasts.queue.some((toast) => toast.message.startsWith(`Книга «${book.title}» обновлена`));
  useLayoutEffect(() => {
    frames.push({ working: line.startsWith('Обновляем'), result });
  });
  return <>
    <button onClick={() => update.choose(book)} type="button">Обновить из файла</button>
    {update.element}
    {line ? <p role="status">{line}</p> : null}
    {toasts.queue.map((toast) => <p key={toast.id}>{toast.message}</p>)}
  </>;
}

function chooseFile(view: ReturnType<typeof render>, name: string) {
  fireEvent.click(screen.getByRole('button', { name: 'Обновить из файла' }));
  const input = view.container.querySelector<HTMLInputElement>('input[type="file"]')!;
  // specs/2026-11-10-ux1-names.md N8.1 (L-19): the system window hides no file — an unfit one gets its reason.
  expect(input.accept).toBe('');
  fireEvent.change(input, { target: { files: [new File(['<FictionBook>новый том</FictionBook>'], name, { type: 'application/x-fictionbook+xml' })] } });
}

type ImportResult = Extract<NonNullable<ProjectJob['result']>, { type: 'import' }>;

/** Fails the Jobs of one import mode with a code; the others go on as the mock runs them. Gives what lets them run again. */
function failJobs(api: ReturnType<typeof createMockApiClient>, mode: ImportResult['importMode'], code: string) {
  const getJob = api.getJob.bind(api);
  const failed = new Set<string>();
  let failing = true;
  vi.spyOn(api, 'getJob').mockImplementation(async (jobId) => {
    const job = await getJob(jobId);
    if (job.result?.type !== 'import' || job.result.importMode !== mode || (!failing && !failed.has(jobId))) return job;
    failed.add(jobId);
    return { ...job, status: 'failed', result: null, error: { type: 'about:blank', title: 'Job failed', status: 500, code, requestId: 'req-1' } };
  });
  return () => {
    failing = false;
  };
}

describe('updating a book from a file (specs/2026-10-27-update-book-from-file.md)', () => {
  beforeEach(() => window.localStorage.clear());

  // UC6: the file is read first; the window names what changes and where, then the update goes.
  it('shows what the file changes before the update, then updates the book in place', async () => {
    const { book, importBookFile, lines, onNotice, onRevert, view } = await renderHarness();

    chooseFile(view, 'том-2.fb2');
    const dialog = await screen.findByRole('dialog', { name: 'Обновить книгу из файла' });
    expect(importBookFile).toHaveBeenCalledWith(projectId, expect.objectContaining({
      options: { importMode: 'update_preview', bookId: book.id },
    }), expect.objectContaining({ idempotencyKey: expect.any(String) }));
    expect(await within(dialog).findByRole('heading', { name: `Что изменится в «${book.title}»` })).toBeTruthy();
    const summary = within(dialog).getByRole('list', { name: 'Что изменится' });
    expect(summary.textContent).toContain('Слова изменятся — в 1 главе');
    expect(dialog.textContent).toContain('Помощник перечитает 1 главу — примерно 1 мин. Сюжетные линии собирать заново не нужно.');
    const chapter = within(dialog).getByRole('button', { name: /^1\. .+ — слова$/ });
    expect(chapter.getAttribute('aria-expanded')).toBe('false');
    fireEvent.click(chapter);
    expect(chapter.getAttribute('aria-expanded')).toBe('true');
    expect(dialog.textContent).toContain('Было:…отряд из пятнадцати человек вышел к реке…');
    expect(dialog.textContent).toContain('Стало:…отряд из тринадцати человек вышел к реке…');

    // R6.8: the voice presses «Обновить книгу» only after the author's «да».
    expect(within(dialog).getByRole('button', { name: 'Обновить книгу' }).getAttribute('data-voice-consent')).toBe('yes');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Обновить книгу' }));
    await waitFor(() => expect(importBookFile).toHaveBeenLastCalledWith(projectId, expect.objectContaining({
      options: { importMode: 'update_book', bookId: book.id },
    }), expect.objectContaining({ idempotencyKey: expect.any(String) })));
    expect(importBookFile.mock.calls[0]![2].idempotencyKey).not.toBe(importBookFile.mock.calls[1]![2].idempotencyKey);
    // The line of the update in work is the shelf's own, read from its Job (SH2.2): a former notice gives way to it.
    await waitFor(() => expect(onNotice).toHaveBeenCalledWith(''));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Обновить книгу из файла' })).toBeNull());
    // specs/2026-11-11-ux7-trash.md TB4.1: the line of the shell says what the update did and opens «Что изменилось».
    await waitFor(() => expect(lines).toHaveBeenCalledWith(expect.objectContaining({ message: `Книга «${book.title}» обновлена: изменена 1 глава` })));
    // One line for one update, though the list of work read before its end still names it.
    await new Promise((resolve) => setTimeout(resolve, 2_500));
    expect(lines.mock.calls.filter(([toast]) => toast.message.startsWith(`Книга «${book.title}» обновлена`))).toHaveLength(1);
    render(<>{lines.mock.calls.at(-1)![0].action}</>);
    fireEvent.click(screen.getByRole('button', { name: 'Что изменилось' }));
    const changes = await screen.findByRole('dialog', { name: `Что изменилось в «${book.title}»` });
    expect((await within(changes).findByRole('list', { name: 'Что изменилось' })).textContent).toContain('Слова изменились — в 1 главе');
    // specs/2026-11-12-ux10-book-revert.md BR3.1: the server keeps the update 7 days, and the window offers to return the book.
    expect(await within(changes).findByText('Прежнюю версию можно вернуть до 23 июня.')).toBeTruthy();
    expect(changes.textContent).toContain('Обновлена из файла');
    fireEvent.click(within(changes).getByRole('button', { name: 'Вернуть прежнюю версию…' }));
    expect(onRevert).toHaveBeenCalledTimes(1);
    // BR3.4: the browser keeps nothing of the update — the server owns it.
    expect(window.localStorage.length).toBe(0);
  });

  // specs/2026-11-11-ux9-shelf-chat.md SC1.4 (L-52): the window says what becomes of the notes and of a text written on the
  // site, by what the server does.
  it('says what becomes of the notes of a changed chapter and warns of the edits made on the site', async () => {
    const { api, view } = await renderHarness();
    const getJob = api.getJob.bind(api);
    vi.spyOn(api, 'getJob').mockImplementation(async (jobId) => {
      const job = await getJob(jobId);
      if (job.result?.type !== 'import' || job.result.importMode !== 'update_preview' || !job.result.preview) return job;
      const chapters = job.result.preview.chapters.map((chapter, index) => (index === 0 ? { ...chapter, editedOnSite: true as const } : chapter));
      return { ...job, result: { ...job.result, preview: { ...job.result.preview, chapters } } };
    });
    chooseFile(view, 'том-2.fb2');
    const dialog = await screen.findByRole('dialog', { name: 'Обновить книгу из файла' });
    await within(dialog).findByRole('list', { name: 'Что изменится' });
    expect(dialog.textContent).toContain('Заметки останутся у своих мест; если цитаты заметки в новом тексте нет, заметка сохранит её и откроет начало главы.');
    const warning = within(dialog).getByRole('alert');
    expect(warning.textContent).toMatch(/^Правки, сделанные на сайте в этих главах, заменятся текстом файла: «.+»\.$/u);
    expect(within(dialog).getByRole('button', { name: /^1\. .+ — слова, есть правки с сайта$/u })).toBeTruthy();
  });

  it('leaves the book alone when the author cancels after the preview', async () => {
    const { importBookFile, view } = await renderHarness();

    chooseFile(view, 'том-2.fb2');
    const dialog = await screen.findByRole('dialog', { name: 'Обновить книгу из файла' });
    await within(dialog).findByRole('button', { name: 'Обновить книгу' });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Отмена' }));

    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Обновить книгу из файла' })).toBeNull());
    expect(importBookFile).toHaveBeenCalledTimes(1);
    expect(importBookFile.mock.calls[0]![1].options).toEqual({ importMode: 'update_preview', bookId: expect.any(String) });
  });

  it('says so when the file changes nothing and offers no update', async () => {
    const { api, book, importBookFile, view } = await renderHarness();
    const getJob = api.getJob.bind(api);
    vi.spyOn(api, 'getJob').mockImplementation(async (jobId) => {
      const job = await getJob(jobId);
      if (job.result?.type !== 'import' || !job.result.preview) return job;
      return { ...job, result: { ...job.result, changedChapters: 0, unchangedChapters: job.result.chapterCount, preview: { newCards: 0, threads: false, chapters: [] } } };
    });

    chooseFile(view, 'том-2.fb2');
    const dialog = await screen.findByRole('dialog', { name: 'Обновить книгу из файла' });
    expect(await within(dialog).findByRole('heading', { name: 'В файле нет изменений' })).toBeTruthy();
    expect(dialog.textContent).toContain(`Текст «${book.title}» совпадает с файлом том-2.fb2. Обновлять нечего.`);
    expect(within(dialog).queryByRole('button', { name: 'Обновить книгу' })).toBeNull();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Закрыть' }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Обновить книгу из файла' })).toBeNull());
    expect(importBookFile).toHaveBeenCalledTimes(1);
  });

  // R3.4.3: a file the preview cannot read names its reason in the window, and nothing is updated.
  it.each([
    ['import.too_many_chapters', 'В файле больше 2000 глав — столько в одну книгу не поместить. Разделите его на тома.'],
    ['import.unsupported_encoding', 'Файл в кодировке, которую мы не читаем. Сохраните его в UTF-8 (в Блокноте: «Сохранить как» → «Кодировка: UTF-8») или загрузите Word (.docx).'],
    ['invalid_docx_document', 'Word (.docx) не читается. Откройте файл в Word и сохраните заново.'],
  ])('names why the file could not be read (%s)', async (code, reason) => {
    const { api, importBookFile, view } = await renderHarness();
    failJobs(api, 'update_preview', code);

    chooseFile(view, 'том-2.fb2');
    const dialog = await screen.findByRole('dialog', { name: 'Обновить книгу из файла' });
    expect(await within(dialog).findByRole('heading', { name: 'Не удалось прочитать файл' })).toBeTruthy();
    expect(dialog.textContent).toContain(reason);
    expect(within(dialog).queryByRole('button', { name: 'Обновить книгу' })).toBeNull();
    expect(importBookFile).toHaveBeenCalledTimes(1);
  });

  // R3.4.3: the same reasons as the import queue when the update itself fails.
  it('names the reason of a failed update the author fixes in the file', async () => {
    const { api, book, lines, view } = await renderHarness();
    failJobs(api, 'update_book', 'invalid_docx_document');

    chooseFile(view, 'том-2.fb2');
    fireEvent.click(await screen.findByRole('button', { name: 'Обновить книгу' }));

    await waitFor(() => expect(lines).toHaveBeenCalledWith({
      message: `Книга «${book.title}» не обновилась.`, detail: 'Word (.docx) не читается. Откройте файл в Word и сохраните заново.', tone: 'error',
    }));
  });

  // specs/2026-11-12-ux12-shell.md SH2.3: a failure on our side says how to repeat and carries «Написать нам» — the window of
  // the site, though the line stands in the area of the root, above the session of the pages.
  it('gives a failed update on our side «Написать нам» of the window, not words of support', async () => {
    const { api, book, lines, onOpenContact, view } = await renderHarness();
    failJobs(api, 'update_book', 'worker.handler_failed');

    chooseFile(view, 'том-2.fb2');
    fireEvent.click(await screen.findByRole('button', { name: 'Обновить книгу' }));

    await waitFor(() => expect(lines).toHaveBeenCalledWith(expect.objectContaining({ message: `Книга «${book.title}» не обновилась.`, tone: 'error' })));
    const failure = lines.mock.calls.map(([toast]) => toast).find((toast) => toast.message === `Книга «${book.title}» не обновилась.`)!;
    expect(failure.detail).toBe('Это сбой на нашей стороне — повторите: «Новая версия из файла…» в меню книги.');
    expect(failure.detail).not.toMatch(/поддержк/u);
    render(<>{failure.action}</>);
    fireEvent.click(screen.getByRole('button', { name: 'Написать нам' }));
    expect(onOpenContact).toHaveBeenCalledWith({ topic: 'bug', code: 'req-1', draft: `Книга «${book.title}» не обновилась из файла.` });
  });

  it('repeats the reading of the same file after a failure on our side and offers «Написать нам» beside it', async () => {
    const { api, book, importBookFile, onOpenContact, view } = await renderHarness();
    const unfail = failJobs(api, 'update_preview', 'worker.handler_failed');

    chooseFile(view, 'том-2.fb2');
    const dialog = await screen.findByRole('dialog', { name: 'Обновить книгу из файла' });
    expect(await within(dialog).findByRole('heading', { name: 'Не удалось прочитать файл' })).toBeTruthy();
    const failure = within(dialog).getByRole('alert');
    expect(failure.textContent).toContain('Это сбой на нашей стороне. Повторите, а если не поможет — напишите нам.');
    expect(dialog.textContent).not.toMatch(/поддержк/u);
    fireEvent.click(within(failure).getByRole('button', { name: 'Написать нам' }));
    expect(onOpenContact).toHaveBeenCalledWith({ topic: 'bug', code: 'req-1', draft: `Не удалось прочитать файл том-2.fb2 для книги «${book.title}».` });

    unfail();
    fireEvent.click(within(failure).getByRole('button', { name: 'Повторить' }));
    expect(await within(dialog).findByRole('heading', { name: `Что изменится в «${book.title}»` })).toBeTruthy();
    expect(importBookFile).toHaveBeenCalledTimes(2);
    expect(importBookFile.mock.calls[1]![1]).toMatchObject({ options: { importMode: 'update_preview', bookId: book.id } });
  });

  // specs/2026-11-12-ux12-shell.md SH2.2: the line of the result takes the place of the line of the update in work.
  it('shows the line of the update in work until it ends, never in a frame with the line of its result', async () => {
    const api = createMockApiClient();
    const book = (await api.listBooks(projectId)).data.find((item) => item.id === bookId)!;
    const frames: Array<{ working: boolean; result: boolean }> = [];
    const client = new QueryClient({ defaultOptions: { mutations: { retry: false }, queries: { retry: false } } });
    const view = render(<QueryClientProvider client={client}><ShelfInTheShell api={api} book={book} frames={frames} /></QueryClientProvider>);
    await waitFor(() => expect(client.getQueryState(manuscriptQueryKeys.importConstraints(projectId))?.status).toBe('success'));

    chooseFile(view, 'том-2.fb2');
    fireEvent.click(await screen.findByRole('button', { name: 'Обновить книгу' }));
    expect(await screen.findByText(`Обновляем «${book.title}» из файла том-2.fb2.`)).toBeTruthy();
    expect(await screen.findByText(`Книга «${book.title}» обновлена: изменена 1 глава`, {}, { timeout: 5_000 })).toBeTruthy();
    expect(screen.queryByText(/^Обновляем/u)).toBeNull();
    expect(frames.some((frame) => frame.working)).toBe(true);
    expect(frames.filter((frame) => frame.working && frame.result)).toEqual([]);
  });

  // R3.1.1: the same check as every import entry, before anything is sent.
  it('refuses a PDF with its reason and sends nothing', async () => {
    const { importBookFile, onNotice, view } = await renderHarness();

    fireEvent.click(screen.getByRole('button', { name: 'Обновить из файла' }));
    const input = view.container.querySelector<HTMLInputElement>('input[type="file"]')!;
    fireEvent.change(input, { target: { files: [new File(['%PDF'], 'rukopis.pdf', { type: 'application/pdf' })] } });

    expect(onNotice).toHaveBeenCalledWith('PDF не поддерживается: из него нельзя надёжно достать главы. Откройте рукопись в Word или LibreOffice → «Сохранить как» → Word (.docx), RTF или ODT.');
    expect(screen.queryByRole('dialog', { name: 'Обновить книгу из файла' })).toBeNull();
    expect(importBookFile).not.toHaveBeenCalled();
  });
});
