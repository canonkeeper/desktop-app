import { fireEvent, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { createMockApiClient } from '../../../api/mock';
import type { ChatSession } from '../../../entities/chat/api';
import type { IndexingStep } from '../../../entities/manuscript/api';
import { renderCard } from './BookIndexingCard.test-helpers';

function withChats(sessions: ChatSession[]) {
  return (api: ReturnType<typeof createMockApiClient>) => {
    vi.spyOn(api, 'listChatSessions').mockResolvedValue({ data: sessions, meta: { hasMore: false, nextCursor: null } } as never);
  };
}

const readySteps: IndexingStep[] = [
  { kind: 'import', status: 'succeeded', progress: 1, counters: { totalChapters: 8 } },
  { kind: 'text_index', status: 'succeeded', progress: 1, counters: { indexedChapters: 8, totalChapters: 8 } },
  { kind: 'cards', status: 'succeeded', progress: 1, counters: { indexedChapters: 8, totalChapters: 8 } },
  { kind: 'ready', status: 'succeeded', progress: 1 },
];

const partialSteps: IndexingStep[] = [
  { kind: 'import', status: 'succeeded', progress: 1 },
  { kind: 'text_index', status: 'succeeded', progress: 1, counters: { indexedChapters: 8, totalChapters: 8 } },
  { kind: 'cards', status: 'pending', progress: 0.5, sourceView: 'draft', counters: { indexedChapters: 4, totalChapters: 8 } },
  { kind: 'ready', status: 'pending', progress: null },
];

describe('BookIndexingCard terminal outcomes', () => {
  it('shows a ready book with the chat capabilities it gained', async () => {
    renderCard({ indexingState: 'ready', steps: readySteps });

    expect(await screen.findByText('Книга готова для чата')).toBeTruthy();
    expect(screen.getAllByText('Поиск по тексту и карточки глав доступны чату.').length).toBeGreaterThan(0);
    expect(screen.getAllByText('Книга II. Карта приливов · 8 глав').length).toBeGreaterThan(0);
  });

  it('shows a partial book with its counters and restarts the whole text version', async () => {
    const { startProjectIndexing } = renderCard({
      indexingState: 'partial', retryableFailedBookId: 'book-02', sourceView: 'draft', steps: partialSteps,
    });

    expect(await screen.findByText('Книга подготовлена не полностью')).toBeTruthy();
    expect(screen.getAllByText('Поиск — по 8 из 8 глав, карточки — для 4 из 8.').length).toBeGreaterThan(0);
    fireEvent.click(screen.getByRole('button', { name: 'Повторить' }));

    await waitFor(() => expect(startProjectIndexing).toHaveBeenCalledWith(
      'project-white-port', { sourceView: 'draft' }, { idempotencyKey: expect.any(String) },
    ));
  });

  it('does not offer a restart to a viewer', async () => {
    renderCard({
      canEditManuscript: false, indexingState: 'partial', retryableFailedBookId: 'book-02', steps: partialSteps,
    });

    expect(await screen.findByText('Книга подготовлена не полностью')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Повторить' })).toBeNull();
  });

  it('shows a failed cards step and offers the restart the status allows', async () => {
    const { startProjectIndexing } = renderCard({
      indexingState: 'partial',
      retryableFailedBookId: 'book-02',
      steps: [
        { kind: 'import', status: 'succeeded', progress: 1 },
        { kind: 'text_index', status: 'succeeded', progress: 1 },
        { kind: 'cards', status: 'failed', progress: 0.75, canRetry: true, sourceView: 'published', jobId: 'job-cards' },
        { kind: 'ready', status: 'pending', progress: null },
      ],
    });

    expect(await screen.findByText('Не удалось подготовить книгу')).toBeTruthy();
    expect(screen.getAllByText('Составляем карточки глав · ошибка · можно повторить').length).toBeGreaterThan(0);
    fireEvent.click(screen.getByRole('button', { name: 'Повторить' }));

    await waitFor(() => expect(startProjectIndexing).toHaveBeenCalledWith(
      'project-white-port', { sourceView: 'published' }, { idempotencyKey: expect.any(String) },
    ));
  });

  it('shows a canceled import', async () => {
    renderCard({ steps: [{ kind: 'import', status: 'canceled', progress: null, jobId: 'job-import' }] });

    expect(await screen.findByText('Подготовка книги отменена')).toBeTruthy();
  });
});

// R2.2.3: the success and the partial result lead to a question in a new chat of the saga.
describe('BookIndexingCard question actions', () => {
  it.each([
    ['without a chat', [] as ChatSession[], 'Задать первый вопрос'],
    ['with a chat', [{ id: 'chat-1' } as ChatSession], 'Задать вопрос'],
  ])('offers the question of a ready book %s', async (_label, sessions, name) => {
    const { onAskQuestion } = renderCard({ apiSetup: withChats(sessions), indexingState: 'ready', steps: readySteps });

    fireEvent.click(await screen.findByRole('button', { name }));

    expect(onAskQuestion).toHaveBeenCalledTimes(1);
  });

  it('offers the question next to the restart of a partial book', async () => {
    renderCard({
      apiSetup: withChats([]), indexingState: 'partial', retryableFailedBookId: 'book-02', sourceView: 'draft', steps: partialSteps,
    });

    expect(await screen.findByRole('button', { name: 'Задать первый вопрос' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Повторить' })).toBeTruthy();
  });

  it('declines the ready chapters of the book', async () => {
    renderCard({
      indexingState: 'ready',
      steps: readySteps.map((step) => step.counters ? { ...step, counters: { ...step.counters, totalChapters: 22 } } : step),
    });

    expect((await screen.findAllByText('Книга II. Карта приливов · 22 главы')).length).toBeGreaterThan(0);
  });
});

describe('BookIndexingCard preparation estimate (R2.2.1, R2.2.4, R2.2.5)', () => {
  const startedAt = new Date(Date.now() - 10_000).toISOString();
  const cardsRunning: IndexingStep[] = [
    { kind: 'import', status: 'succeeded', progress: 1, counters: { totalChapters: 12 } },
    { kind: 'text_index', status: 'succeeded', progress: 1, counters: { indexedChapters: 12, totalChapters: 12 } },
    { kind: 'cards', status: 'running', progress: 0, startedAt, counters: { indexedChapters: 0, totalChapters: 12 } },
    { kind: 'ready', status: 'pending', progress: null },
  ];

  it('names the time left and offers to ask by the prepared part, without a success', async () => {
    const onAskQuestion = vi.fn();
    renderCard({ indexingState: 'processing', onAskQuestion, steps: cardsRunning });

    expect(await screen.findByText('Осталось ≈ 3 мин')).toBeTruthy();
    expect(screen.getByText(/Можно закрыть страницу — подготовка продолжится\. Когда вернётесь, здесь будет кнопка «Задать первый вопрос»\./)).toBeTruthy();
    expect(screen.queryByText('Книга готова для чата')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Можно спросить уже сейчас — ответ будет по готовой части' }));
    expect(onAskQuestion).toHaveBeenCalledTimes(1);
  });

  it('names the rest of the queue after the current book', async () => {
    renderCard({ currentBook: { bookId: 'book-02', position: 1, total: 3 }, indexingState: 'processing', steps: cardsRunning });

    expect(await screen.findByText('Эта книга — ещё ≈ 3 мин; дальше: 2 книги')).toBeTruthy();
  });

  it('waits for the chapters before an estimate', async () => {
    renderCard({
      indexingState: 'processing',
      steps: [
        { kind: 'import', status: 'running', progress: null },
        { kind: 'text_index', status: 'pending', progress: null },
        { kind: 'cards', status: 'pending', progress: null },
        { kind: 'ready', status: 'pending', progress: null },
      ],
    });

    expect(await screen.findByText('Оценка появится, когда главы будут разобраны')).toBeTruthy();
    expect(screen.queryByRole('button', { name: /Можно спросить уже сейчас/ })).toBeNull();
  });
});
