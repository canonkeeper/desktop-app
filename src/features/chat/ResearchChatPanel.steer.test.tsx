import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { useState } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../api/mock';
import { projectId } from '../../api/mock/repository/fixtures';
import { ApiStatusError, createIdempotencyKey, type AgentTimelineEvent } from '../../shared/api';
import { FakeWebSocket } from '../../test/FakeWebSocket';
import { forgetAllTurns } from './answer-ready';
import { ResearchChatPanel } from './ResearchChatPanel';
import { ToastsProvider } from '../../ui/feedback/Toast';

beforeEach(() => {
  vi.stubGlobal('innerWidth', 1440);
  vi.stubGlobal('innerHeight', 900);
  FakeWebSocket.reset();
  vi.stubGlobal('WebSocket', FakeWebSocket);
});

afterEach(() => {
  forgetAllTurns();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

type Api = ReturnType<typeof createMockApiClient>;

async function newChat(api: Api, title: string) {
  return api.createChatSession(projectId, { scope: 'project', sourceView: 'published', title }, { idempotencyKey: createIdempotencyKey('createChatSession') });
}

function renderPanel(api: Api, selectedChatId: string, panel: { illustrationRoutes?: Record<string, never> } = {}) {
  const client = new QueryClient({ defaultOptions: { mutations: { retry: false }, queries: { retry: false } } });
  let select: (chatId: string) => void = () => undefined;
  function SelectedChat() {
    const [chatId, setChatId] = useState(selectedChatId);
    select = setChatId;
    return <QueryClientProvider client={client}><ToastsProvider>
      <ResearchChatPanel api={api} canCreate {...panel} onOpenSource={vi.fn()} onSelectChatId={setChatId} projectId={projectId} selectedChatId={chatId} sourceView="published" />
    </ToastsProvider></QueryClientProvider>;
  }
  render(<SelectedChat />);
  return { select: (chatId: string) => act(() => select(chatId)) };
}

async function ask(question: string) {
  fireEvent.change(await screen.findByRole('textbox', { name: 'Вопрос' }), { target: { value: question } });
  const start = screen.getByRole('button', { name: 'Спросить' });
  await waitFor(() => expect(start).toHaveProperty('disabled', false));
  fireEvent.click(start);
  return screen.findByRole('textbox', { name: 'Уточнение' });
}

function conflict(code: string) {
  return new ApiStatusError(409, { type: 'about:blank', title: 'Conflict', status: 409, code, requestId: 'req-steer' }, new Response(null, { status: 409 }));
}

// specs/2026-10-30-chat-steering.md UC4.
describe('Research Chat corrections', () => {
  it('keeps the field open in a running turn; Enter sends a correction, not a question', async () => {
    const api = createMockApiClient();
    const chat = await newChat(api, 'Обложка');
    renderPanel(api, chat.id);
    const turn = vi.spyOn(api, 'createChatTurn');
    const steer = vi.spyOn(api, 'steerChatTurn');

    const field = await ask('Нарисуй обложку') as HTMLTextAreaElement;
    expect(field.disabled).toBe(false);
    expect(field.placeholder).toBe('Уточнить по ходу работы…');
    const send = screen.getByRole('button', { name: 'Уточнить' });
    expect(send.getAttribute('aria-disabled')).toBe('true');
    expect(screen.getByRole('button', { name: 'Остановить' })).toBeTruthy();
    expect(turn).toHaveBeenCalledTimes(1);

    fireEvent.change(field, { target: { value: '  Без дракона  ' } });
    fireEvent.keyDown(field, { key: 'Enter' });

    await waitFor(() => expect(steer).toHaveBeenCalledTimes(1));
    const [chatId, turnId, body, options] = steer.mock.calls[0]!;
    expect(chatId).toBe(chat.id);
    expect(turnId).toMatch(/^chat-turn-/u);
    expect(body).toEqual({ body: 'Без дракона' });
    expect(options.idempotencyKey).toBeTruthy();
    expect(turn).toHaveBeenCalledTimes(1);
    await waitFor(() => expect(field.value).toBe(''));
    expect(await screen.findByText('Без дракона')).toBeTruthy();
    expect(screen.getByText('Будет учтено на следующем шаге')).toBeTruthy();
  });

  it('keeps what the author wrote while the correction was on its way', async () => {
    const api = createMockApiClient();
    const chat = await newChat(api, 'Обложка');
    renderPanel(api, chat.id);
    const field = await ask('Нарисуй обложку') as HTMLTextAreaElement;
    let accept!: (value: { steerId: string }) => void;
    vi.spyOn(api, 'steerChatTurn').mockImplementationOnce(() => new Promise((resolve) => { accept = resolve; }));
    fireEvent.change(field, { target: { value: 'Без дракона' } });
    fireEvent.click(screen.getByRole('button', { name: 'Уточнить' }));
    await waitFor(() => expect(accept).toBeTruthy());
    fireEvent.change(field, { target: { value: 'И без замка' } });
    await act(async () => accept({ steerId: 'steer-1' }));
    expect(field.value).toBe('И без замка');
  });

  it('shows nothing as sent before the server answers and keeps the draft on a refusal', async () => {
    const api = createMockApiClient();
    const chat = await newChat(api, 'Обложка');
    renderPanel(api, chat.id);
    const field = await ask('Нарисуй обложку') as HTMLTextAreaElement;
    let refuse!: (error: unknown) => void;
    vi.spyOn(api, 'steerChatTurn').mockImplementationOnce(() => new Promise((_, reject) => { refuse = reject; }));

    fireEvent.change(field, { target: { value: 'Без дракона' } });
    fireEvent.click(screen.getByRole('button', { name: 'Уточнить' }));
    await waitFor(() => expect(refuse).toBeTruthy());
    expect(screen.queryByText('Будет учтено на следующем шаге')).toBeNull();

    await act(async () => refuse(conflict('chat.steer_retry')));
    expect(await screen.findByText(/Сейчас уточнение не принимается/u)).toBeTruthy();
    expect(field.value).toBe('Без дракона');
    expect(screen.queryByText('Будет учтено на следующем шаге')).toBeNull();
  });

  // specs/2026-11-09-quota-accounting.md Q2.3: a correction spends a question, and its refusal is the question's.
  it('refuses a correction over the questions of the day as a question, keeps the text and reads the quota again', async () => {
    const api = createMockApiClient();
    const chat = await newChat(api, 'Обложка');
    renderPanel(api, chat.id);
    const field = await ask('Нарисуй обложку') as HTMLTextAreaElement;
    const quota = vi.spyOn(api, 'getAccountQuota');
    const reads = quota.mock.calls.length;
    vi.spyOn(api, 'steerChatTurn').mockRejectedValueOnce(new ApiStatusError(
      429,
      { type: 'about:blank', title: 'Too Many Requests', status: 429, code: 'quota.chat_turns_daily', requestId: 'req-quota' },
      new Response(null, { status: 429, headers: { 'Retry-After': '3600' } }),
    ));

    fireEvent.change(field, { target: { value: 'Без дракона' } });
    fireEvent.click(screen.getByRole('button', { name: 'Уточнить' }));
    // specs/2026-11-10-ux1-chat.md CH3.3: one block above the field, in the words of both kinds of message.
    const block = await screen.findByRole('alert', { name: /^Вопросы и уточнения на сегодня закончились/u });
    expect(within(block).getByText('Новые — после 00:00 по Москве.')).toBeTruthy();
    expect(within(block).getByText('Ответ на ваш вопрос готовится и придёт. Текст уточнения остался в поле.')).toBeTruthy();
    expect(screen.getAllByRole('alert')).toHaveLength(1);
    expect(within(block).getByRole('link', { name: 'Написать нам' })).toBeTruthy();
    expect(field.value).toBe('Без дракона');
    expect(screen.queryByText(/Спросить снова можно через/u)).toBeNull();
    await waitFor(() => expect(quota.mock.calls.length).toBeGreaterThan(reads));
  });

  // specs/2026-11-10-ux1-chat.md CH5: a question in the queue has not started — the server would not take a correction.
  it('does not call for a correction while the question waits in the queue, and takes it once the helper works', async () => {
    const api = createMockApiClient();
    const chat = await newChat(api, 'Обложка');
    let queued = true;
    const getJob = api.getJob.bind(api);
    vi.spyOn(api, 'getJob').mockImplementation(async (id) => {
      const job = await getJob(id);
      return queued ? { ...job, status: 'queued', queuePosition: 2, canCancel: true } : { ...job, status: 'running', queuePosition: null, canCancel: true };
    });
    const steer = vi.spyOn(api, 'steerChatTurn');
    renderPanel(api, chat.id);

    const field = await ask('Нарисуй обложку') as HTMLTextAreaElement;
    expect(await screen.findByText('Уточнить можно будет, когда помощник начнёт.', {}, { timeout: 4_000 })).toBeTruthy();
    expect(field.disabled).toBe(false);
    expect(field.placeholder).toBe('Уточнение — когда помощник начнёт…');
    fireEvent.change(field, { target: { value: 'Без дракона' } });
    expect(screen.getByRole('button', { name: 'Уточнить' }).getAttribute('aria-disabled')).toBe('true');
    fireEvent.keyDown(field, { key: 'Enter' });
    fireEvent.submit(field.closest('form')!);
    expect(steer).not.toHaveBeenCalled();

    queued = false;
    expect(await screen.findByText(/^Уточнение к идущему ответу — считается как вопрос\./u, {}, { timeout: 4_000 })).toBeTruthy();
    expect(field.value).toBe('Без дракона');
    expect(field.placeholder).toBe('Уточнить по ходу работы…');
    fireEvent.click(screen.getByRole('button', { name: 'Уточнить' }));
    await waitFor(() => expect(steer).toHaveBeenCalledTimes(1));
    expect(steer.mock.calls[0]![2]).toEqual({ body: 'Без дракона' });
  }, 15_000);

  it('retries a refused correction with the same key and a changed text with a new one', async () => {
    const api = createMockApiClient();
    const chat = await newChat(api, 'Обложка');
    renderPanel(api, chat.id);
    const field = await ask('Нарисуй обложку') as HTMLTextAreaElement;
    const steer = vi.spyOn(api, 'steerChatTurn').mockRejectedValue(conflict('chat.steer_retry'));

    fireEvent.change(field, { target: { value: 'Без дракона' } });
    fireEvent.click(screen.getByRole('button', { name: 'Уточнить' }));
    await waitFor(() => expect(steer).toHaveBeenCalledTimes(1));
    await screen.findByText(/Сейчас уточнение не принимается/u);
    fireEvent.click(screen.getByRole('button', { name: 'Уточнить' }));
    await waitFor(() => expect(steer).toHaveBeenCalledTimes(2));
    expect(steer.mock.calls[1]![3].idempotencyKey).toBe(steer.mock.calls[0]![3].idempotencyKey);
    fireEvent.change(field, { target: { value: 'С драконом' } });
    fireEvent.click(await screen.findByRole('button', { name: 'Уточнить' }));
    await waitFor(() => expect(steer).toHaveBeenCalledTimes(3));
    expect(steer.mock.calls[2]![3].idempotencyKey).not.toBe(steer.mock.calls[0]![3].idempotencyKey);
  });

  it('after «the turn has ended» keeps the text and asks it as a new question with one action', async () => {
    const api = createMockApiClient();
    const chat = await newChat(api, 'Обложка');
    renderPanel(api, chat.id);
    const field = await ask('Нарисуй обложку') as HTMLTextAreaElement;
    const turn = vi.spyOn(api, 'createChatTurn');
    vi.spyOn(api, 'steerChatTurn').mockRejectedValueOnce(conflict('chat.turn_ended'));
    const job = (await api.listProjectJobs(projectId, { active: true })).data[0]!;
    vi.spyOn(api, 'getJob').mockImplementation(async () => ({ ...job, status: 'succeeded', canCancel: false }));

    fireEvent.change(field, { target: { value: 'Без дракона' } });
    fireEvent.click(screen.getByRole('button', { name: 'Уточнить' }));
    const again = await screen.findByRole('button', { name: /^Спросить/u });
    expect((screen.getByRole('textbox', { name: 'Вопрос' }) as HTMLTextAreaElement).value).toBe('Без дракона');
    await waitFor(() => expect(again).toHaveProperty('disabled', false));
    fireEvent.click(again);
    await waitFor(() => expect(turn).toHaveBeenCalledTimes(1));
    expect(turn.mock.calls[0]![1]).toMatchObject({ body: 'Без дракона' });
  });

  it('does not replace a draft with the question when the turn is stopped', async () => {
    const api = createMockApiClient();
    const chat = await newChat(api, 'Обложка');
    renderPanel(api, chat.id);
    const field = await ask('Нарисуй обложку') as HTMLTextAreaElement;
    fireEvent.change(field, { target: { value: 'Без дракона' } });
    fireEvent.click(screen.getByRole('button', { name: 'Остановить' }));
    const question = await screen.findByRole('textbox', { name: 'Вопрос' }) as HTMLTextAreaElement;
    await waitFor(() => expect(question.disabled).toBe(false));
    expect(question.value).toBe('Без дракона');
  });

  it('gives the empty field back the question when the turn is stopped, as before', async () => {
    const api = createMockApiClient();
    const chat = await newChat(api, 'Обложка');
    renderPanel(api, chat.id);
    await ask('Нарисуй обложку');
    fireEvent.click(screen.getByRole('button', { name: 'Остановить' }));
    const question = await screen.findByRole('textbox', { name: 'Вопрос' }) as HTMLTextAreaElement;
    await waitFor(() => expect(question.value).toBe('Нарисуй обложку'));
  });

  it('takes the wait down once the correction reaches the timeline, in its place among the steps', async () => {
    const api = createMockApiClient();
    const chat = await newChat(api, 'Обложка');
    renderPanel(api, chat.id);
    const field = await ask('Нарисуй обложку') as HTMLTextAreaElement;
    const steer = vi.spyOn(api, 'steerChatTurn').mockResolvedValue({ steerId: 'steer-1' });
    fireEvent.change(field, { target: { value: 'Без дракона' } });
    fireEvent.click(screen.getByRole('button', { name: 'Уточнить' }));
    await waitFor(() => expect(steer).toHaveBeenCalled());
    await screen.findByText('Будет учтено на следующем шаге');

    const socket = FakeWebSocket.instances.at(-1)!;
    const job = (await api.listProjectJobs(projectId, { active: true })).data[0]!;
    const row = (cursor: string, kind: AgentTimelineEvent['kind'], extra: Partial<AgentTimelineEvent>): AgentTimelineEvent => ({
      cursor, id: `${cursor}-${kind}`.padEnd(32, '0'), jobId: job.id, kind, logicalSection: 0, role: 'main', ...extra,
    });
    await act(async () => {
      socket.message({ type: 'snapshot', snapshot: { eof: true, needsReconcile: false, nextCursor: null, events: [
        row('tl_1', 'user', { text: 'Нарисуй обложку' }),
        row('tl_2', 'assistant', { text: 'Читаю главу.' }),
        row('tl_3', 'steer', { text: 'Без дракона', steerId: 'steer-1' }),
      ] } });
    });
    await waitFor(() => expect(screen.queryByText('Будет учтено на следующем шаге')).toBeNull());
    const correction = screen.getByRole('note', { name: 'Уточнение автора' });
    expect(within(correction).getByText('Без дракона')).toBeTruthy();
    expect(screen.getAllByRole('note', { name: 'Вопрос автора' })).toHaveLength(1);
  });

  it('keeps the draft and the wait of one chat out of another', async () => {
    const api = createMockApiClient();
    const first = await newChat(api, 'Первый');
    const second = await newChat(api, 'Второй');
    const { select } = renderPanel(api, first.id);
    const field = await ask('Нарисуй обложку') as HTMLTextAreaElement;
    vi.spyOn(api, 'steerChatTurn').mockResolvedValue({ steerId: 'steer-1' });
    fireEvent.change(field, { target: { value: 'Без дракона' } });
    fireEvent.click(screen.getByRole('button', { name: 'Уточнить' }));
    await screen.findByText('Будет учтено на следующем шаге');
    fireEvent.change(field, { target: { value: 'Черновик первого чата' } });

    select(second.id);
    const other = await screen.findByRole('textbox', { name: 'Вопрос' }) as HTMLTextAreaElement;
    await waitFor(() => expect(other.value).toBe(''));
    expect(screen.queryByText('Будет учтено на следующем шаге')).toBeNull();
    expect(screen.getByRole('button', { name: 'Спросить' })).toBeTruthy();
  });
});
