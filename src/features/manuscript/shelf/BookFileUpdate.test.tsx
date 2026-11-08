import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../../api/mock';
import { bookId, projectId } from '../../../api/mock/repository/fixtures';
import { manuscriptQueryKeys, type Book, type ProjectJob } from '../../../entities/manuscript/api';
import { useBookFileUpdate } from './BookFileUpdate';

function Harness({ api, book, onNotice }: {
  api: ReturnType<typeof createMockApiClient>;
  book: Book;
  onNotice: (message: string) => void;
}) {
  const update = useBookFileUpdate({ api, canUpdate: true, onNotice, projectId });
  return <>
    <button onClick={() => update.choose(book)} type="button">Обновить из файла</button>
    {update.element}
  </>;
}

async function renderHarness() {
  const api = createMockApiClient();
  const book = (await api.listBooks(projectId)).data.find((item) => item.id === bookId)!;
  const onNotice = vi.fn();
  const importBookFile = vi.spyOn(api, 'importBookFile');
  const client = new QueryClient({ defaultOptions: { mutations: { retry: false }, queries: { retry: false } } });
  const view = render(<QueryClientProvider client={client}><Harness api={api} book={book} onNotice={onNotice} /></QueryClientProvider>);
  // The shelf has read the import constraints before the author opens a book menu.
  await waitFor(() => expect(client.getQueryState(manuscriptQueryKeys.importConstraints(projectId))?.status).toBe('success'));
  return { api, book, importBookFile, onNotice, view };
}

function chooseFile(view: ReturnType<typeof render>, name: string) {
  fireEvent.click(screen.getByRole('button', { name: 'Обновить из файла' }));
  const input = view.container.querySelector<HTMLInputElement>('input[type="file"]')!;
  expect(input.accept).toBe('.fb2,.epub,.docx');
  fireEvent.change(input, { target: { files: [new File(['<FictionBook>новый том</FictionBook>'], name, { type: 'application/x-fictionbook+xml' })] } });
}

type ImportResult = Extract<NonNullable<ProjectJob['result']>, { type: 'import' }>;

/** Fails the Jobs of one import mode with a code; the others go on as the mock runs them. */
function failJobs(api: ReturnType<typeof createMockApiClient>, mode: ImportResult['importMode'], code: string) {
  const getJob = api.getJob.bind(api);
  vi.spyOn(api, 'getJob').mockImplementation(async (jobId) => {
    const job = await getJob(jobId);
    if (job.result?.type !== 'import' || job.result.importMode !== mode) return job;
    return { ...job, status: 'failed', result: null, error: { type: 'about:blank', title: 'Job failed', status: 500, code, requestId: 'req-1' } };
  });
}

describe('updating a book from a file (specs/2026-10-27-update-book-from-file.md)', () => {
  // UC6: the file is read first; the window names what changes and where, then the update goes.
  it('shows what the file changes before the update, then updates the book in place', async () => {
    const { book, importBookFile, onNotice, view } = await renderHarness();

    chooseFile(view, 'том-2.fb2');
    const dialog = await screen.findByRole('dialog', { name: 'Обновить книгу из файла' });
    expect(importBookFile).toHaveBeenCalledWith(projectId, expect.objectContaining({
      options: { importMode: 'update_preview', bookId: book.id },
    }), expect.objectContaining({ idempotencyKey: expect.any(String) }));
    expect(await within(dialog).findByRole('heading', { name: `Что изменится в «${book.title}»` })).toBeTruthy();
    const summary = within(dialog).getByRole('list', { name: 'Что изменится' });
    expect(summary.textContent).toContain('Слова изменятся — в 1 главе');
    expect(dialog.textContent).toContain('Новые карточки нужны 1 главе — примерно 1 мин. Нити саги пересобирать не нужно.');
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
    await waitFor(() => expect(onNotice).toHaveBeenCalledWith(`Обновляем «${book.title}» из файла том-2.fb2.`));
    await waitFor(() => expect(onNotice).toHaveBeenLastCalledWith(expect.stringMatching(new RegExp(`^Книга «${book.title}» обновлена: глав изменено 1`))));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Обновить книгу из файла' })).toBeNull());
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
    ['import.unsupported_encoding', 'Файл в кодировке, которую мы не читаем. Сохраните его в UTF-8 или загрузите DOCX.'],
    ['invalid_docx_document', 'DOCX не читается. Откройте файл в Word и сохраните заново.'],
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
  it.each([
    ['invalid_docx_document', 'Не удалось обновить книгу из файла. DOCX не читается. Откройте файл в Word и сохраните заново.'],
    ['worker.handler_failed', 'Не удалось обновить книгу из файла. Повторите — если не получится, напишите в поддержку.'],
  ])('names the reason of a failed update (%s)', async (code, notice) => {
    const { api, onNotice, view } = await renderHarness();
    failJobs(api, 'update_book', code);

    chooseFile(view, 'том-2.fb2');
    fireEvent.click(await screen.findByRole('button', { name: 'Обновить книгу' }));

    await waitFor(() => expect(onNotice).toHaveBeenLastCalledWith(notice));
  });

  // R3.1.1: the same check as every import entry, before anything is sent.
  it('refuses a PDF with its reason and sends nothing', async () => {
    const { importBookFile, onNotice, view } = await renderHarness();

    fireEvent.click(screen.getByRole('button', { name: 'Обновить из файла' }));
    const input = view.container.querySelector<HTMLInputElement>('input[type="file"]')!;
    fireEvent.change(input, { target: { files: [new File(['%PDF'], 'rukopis.pdf', { type: 'application/pdf' })] } });

    expect(onNotice).toHaveBeenCalledWith('PDF не поддерживается: из него нельзя надёжно достать главы. Сохраните рукопись как DOCX (Word: Файл → Сохранить как → Документ Word) или загрузите FB2/EPUB.');
    expect(screen.queryByRole('dialog', { name: 'Обновить книгу из файла' })).toBeNull();
    expect(importBookFile).not.toHaveBeenCalled();
  });
});
