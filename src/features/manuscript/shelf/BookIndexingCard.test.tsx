import { cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { IndexingStep } from '../../../entities/manuscript/api';
import { orderedSteps, renderCard } from './BookIndexingCard.test-helpers';

describe('BookIndexingCard', () => {
  it('requests the checklist for the exact book view', async () => {
    const { getIndexingStatus } = renderCard({ bookId: 'book-02', sourceView: 'draft' });

    await waitFor(() => expect(getIndexingStatus).toHaveBeenCalledWith('project-white-port', {
      bookId: 'book-02',
      signal: expect.any(AbortSignal),
      sourceView: 'draft',
    }));
  });

  it('does not show a project-wide text counter before the exact book is known', async () => {
    const { getIndexingStatus } = renderCard({
      bookId: null,
      bookPendingCreation: false,
      steps: [{ counters: { indexedChapters: 77, totalChapters: 154 }, kind: 'text_index', progress: 0.5, status: 'running' }],
    });

    await waitFor(() => expect(getIndexingStatus).not.toHaveBeenCalled());
    expect(screen.queryByText('Готово глав для поиска: 77 из 154.')).toBeNull();
    expect(screen.queryByRole('list', { name: 'Этапы подготовки' })).toBeNull();
  });

  it('shows the four steps of a book in order: import, text index, cards, ready', async () => {
    renderCard({ steps: orderedSteps, indexingState: 'processing' });

    const list = await screen.findByRole('list', { name: 'Этапы подготовки' });
    const items = within(list).getAllByRole('listitem');
    ['Импортируем главы', 'Готовим поиск по тексту', 'Составляем карточки глав', 'Готово для чата'].forEach((label, index) => {
      expect(within(items[index]!).getByText(label)).toBeTruthy();
    });
    expect(screen.getByRole('heading', { name: 'Готовим «Книга II. Карта приливов» для чата' })).toBeTruthy();
    expect(screen.getByText('Сейчас · этап 2 из 4')).toBeTruthy();
  });

  it('keeps the checklist collapsed until the reader asks for stages', async () => {
    renderCard({
      steps: orderedSteps.map((step) => step.kind === 'text_index' ? { ...step, progress: 6 / 14 } : step),
    });

    const summary = await screen.findByText(
      (_, element) => element?.tagName === 'SUMMARY' && Boolean(element.textContent?.includes('Показать этапы')),
    );
    const details = summary.closest('details');
    expect(summary.textContent).toContain('43%');
    expect(details?.open).toBe(false);

    fireEvent.click(summary);

    expect(details?.open).toBe(true);
    expect(screen.getByRole('list', { name: 'Этапы подготовки' }).children).toHaveLength(4);
  });

  it('shows the cards of a book with their chapter counters and progress', async () => {
    renderCard({
      indexingState: 'processing',
      steps: [
        { kind: 'import', status: 'succeeded', progress: 1 },
        { kind: 'text_index', status: 'succeeded', progress: 1, counters: { indexedChapters: 8, totalChapters: 8 } },
        { kind: 'cards', status: 'running', progress: 3 / 8, counters: { indexedChapters: 3, totalChapters: 8 }, jobId: 'job-cards' },
        { kind: 'ready', status: 'pending', progress: null },
      ],
    });

    expect(await screen.findByRole('heading', { name: 'Составляем карточки глав' })).toBeTruthy();
    expect(screen.getAllByText(/Карточки готовы для 3 из 8 глав\./).length).toBeGreaterThan(0);
    expect(screen.getByText('Сейчас · этап 3 из 4')).toBeTruthy();
    expect(screen.getByRole('progressbar', { name: 'Ход этапа «Составляем карточки глав»' })).toBeTruthy();
  });

  it('keeps a new queue import running when the previous ready projection arrives', async () => {
    renderCard({
      bookId: null,
      bookPendingCreation: true,
      bookTitle: 'Том 1.epub',
      books: [],
      indexingState: 'ready',
      sourceView: 'draft',
      steps: [{ kind: 'import', status: 'running', progress: null, jobId: 'job-import-running' }],
    });

    expect(await screen.findByRole('heading', { name: 'Готовим «Том 1.epub» для чата' })).toBeTruthy();
    expect(screen.getByRole('heading', { name: 'Импортируем главы' })).toBeTruthy();
    expect(screen.queryByText('Книга готова для чата')).toBeNull();
  });

  it('dismisses the card without canceling background processing', async () => {
    const onCancel = vi.fn();
    const { cancelJob, onDismiss } = renderCard({ canCancel: true, onCancel, steps: orderedSteps });

    fireEvent.click(await screen.findByRole('button', { name: 'Скрыть подготовку' }));

    expect(onDismiss).toHaveBeenCalledOnce();
    expect(onCancel).not.toHaveBeenCalled();
    expect(cancelJob).not.toHaveBeenCalled();
  });

  it('cancels processing only through the explicitly labeled action', async () => {
    const onCancel = vi.fn();
    const { onDismiss } = renderCard({ canCancel: true, onCancel, steps: orderedSteps });

    fireEvent.click(await screen.findByRole('button', { name: 'Отменить обработку' }));

    expect(onCancel).toHaveBeenCalledOnce();
    expect(onDismiss).not.toHaveBeenCalled();
  });

  it('does not offer cancellation without the current job capability', async () => {
    renderCard({ canCancel: false, onCancel: vi.fn(), steps: orderedSteps });

    await screen.findByRole('list', { name: 'Этапы подготовки' });
    expect(screen.queryByRole('button', { name: 'Отменить обработку' })).toBeNull();
  });

  it('shows clear copy for every public step status', async () => {
    const labels: Record<IndexingStep['status'], RegExp> = {
      pending: /Не начат/, queued: /В очереди/, running: /Сейчас/, canceling: /Останавливаем/,
      succeeded: /Готово/, partial: /Частично готово/, failed: /Ошибка/, canceled: /Отменено/,
    };
    for (const [status, label] of Object.entries(labels) as [IndexingStep['status'], RegExp][]) {
      renderCard({ steps: [{ kind: 'text_index', status, progress: null }] });
      const list = await screen.findByRole('list', { name: 'Этапы подготовки' });
      expect(within(list).getAllByText(label).length).toBeGreaterThan(0);
      cleanup();
    }
  });

  it('shows an accepted queued Job without implying a second start after reopening', async () => {
    renderCard({ steps: [{ kind: 'text_index', status: 'queued', progress: null, jobId: 'job-text' }] });

    expect(await screen.findByRole('heading', { name: 'Подготовка поставлена в очередь' })).toBeTruthy();
    expect(screen.getByText('Подготовка уже идёт — запускать её снова не нужно.')).toBeTruthy();
  });

  it('keeps the explicit book identity when aggregate processing points at another book', async () => {
    renderCard({
      bookId: 'book-02',
      books: [{ id: 'book-01', title: 'Книга I' }, { id: 'book-02', title: 'Книга II. Карта приливов' }],
      currentBook: { bookId: 'book-01', position: 1, total: 2 },
      indexingState: 'processing',
      steps: orderedSteps,
    });

    expect(await screen.findByRole('heading', { name: 'Готовим «Книга II. Карта приливов» для чата' })).toBeTruthy();
    expect(screen.getByText('Книга 1 из 2')).toBeTruthy();
  });

  it('shows only factual import counters supplied by the worker', async () => {
    renderCard({
      steps: [{
        kind: 'import', status: 'running', progress: null, importPhase: 'saving',
        counters: { bytesTransferred: 2048, bytesTotal: 2048, parsedChapters: 12, savedChapters: 5, totalChapters: 12 },
      }],
    });

    expect((await screen.findAllByText('Сохраняем книгу и главы · Получено 2 КБ из 2 КБ · разобрано 12/12 глав · сохранено 5/12 глав.')).length).toBeGreaterThan(0);
  });
});
