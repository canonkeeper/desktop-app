import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../api/mock';
import { projectId } from '../../api/mock/repository/fixtures';
import { normalScenario } from '../../api/mock/scenarios';
import { ApiStatusError, createIdempotencyKey, type AgentTimelineEvent } from '../../shared/api';
import { FakeWebSocket } from '../../test/FakeWebSocket';
import { ResearchChatPanel } from './ResearchChatPanel';

const question = 'Сколько лет Мире в главе 12?';

beforeEach(() => {
  vi.stubGlobal('innerWidth', 1440);
  vi.stubGlobal('innerHeight', 900);
  FakeWebSocket.reset();
  vi.stubGlobal('WebSocket', FakeWebSocket);
});

afterEach(() => vi.unstubAllGlobals());

function event(cursor: number, kind: AgentTimelineEvent['kind'], fields: Partial<AgentTimelineEvent> = {}): AgentTimelineEvent {
  return { cursor: `tl_${cursor}`, id: `failure-event-${cursor}`, jobId: 'job-chat', kind, logicalSection: 0, role: 'main', ...fields };
}

/** A chat whose last turn failed with `code`: the mock Job fails, the timeline has the question. */
async function renderFailedTurn(code: string, outcome: 'failed' | 'interrupted' = 'failed') {
  const api = createMockApiClient();
  const chat = await api.createChatSession(projectId, { scope: 'project', sourceView: 'published', title: 'Возраст Миры' },
    { idempotencyKey: createIdempotencyKey('createChatSession') });
  const turn = await api.createChatTurn(chat.id, { body: question }, { idempotencyKey: createIdempotencyKey('createChatTurn') });
  api.setScenario({ ...normalScenario, turnFailure: code });
  vi.spyOn(api, 'getAgentTimeline').mockResolvedValue({
    eof: true, needsReconcile: false, nextCursor: null,
    events: [
      event(1, 'user', { jobId: turn.jobId, text: question }),
      event(2, 'assistant', { jobId: turn.jobId, providerTurnId: 'turn-1', providerItemId: 'item-2', text: 'Ищу Миру в главе 12.' }),
      event(3, 'turn_outcome', { jobId: turn.jobId, outcome }),
    ],
  } as unknown as Awaited<ReturnType<typeof api.getAgentTimeline>>);
  const createTurn = vi.spyOn(api, 'createChatTurn');
  const onSelectChatId = vi.fn();
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { mutations: { retry: false }, queries: { retry: false } } })}>
      <ResearchChatPanel api={api} canCreate onOpenSource={vi.fn()} onSelectChatId={onSelectChatId} projectId={projectId}
        selectedChatId={chat.id} sourceView="published" />
    </QueryClientProvider>,
  );
  const block = await screen.findByRole('alert');
  return { block, createTurn, jobId: turn.jobId, onSelectChatId };
}

function letterOf(link: HTMLElement) {
  return decodeURIComponent(link.getAttribute('href') ?? '');
}

describe('Research Chat turn failures (UC4.2)', () => {
  it('a turn whose first timeline read fails once shows no broken connection (feed spec, 30.10)', async () => {
    const api = createMockApiClient();
    const chat = await api.createChatSession(projectId, { scope: 'project', sourceView: 'published', title: 'Возраст Миры' },
      { idempotencyKey: createIdempotencyKey('createChatSession') });
    await api.createChatTurn(chat.id, { body: question }, { idempotencyKey: createIdempotencyKey('createChatTurn') });
    const getJob = api.getJob.bind(api);
    vi.spyOn(api, 'getJob').mockImplementation(async (id) => ({ ...await getJob(id), status: 'running' }));
    const read = vi.spyOn(api, 'getAgentTimeline')
      .mockRejectedValueOnce(new ApiStatusError(503, {
        type: 'about:blank', title: 'Timeline', status: 503, code: 'agent_timeline.unavailable', requestId: 'req-early',
      }, new Response(null, { status: 503 })))
      .mockResolvedValue({ eof: true, needsReconcile: false, nextCursor: null, events: [] } as unknown as Awaited<ReturnType<typeof api.getAgentTimeline>>);
    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { mutations: { retry: false }, queries: { retry: false } } })}>
        <ResearchChatPanel api={api} canCreate onOpenSource={vi.fn()} onSelectChatId={vi.fn()} projectId={projectId}
          selectedChatId={chat.id} sourceView="published" />
      </QueryClientProvider>,
    );

    await waitFor(() => expect(read).toHaveBeenCalledTimes(1));
    await new Promise((resolve) => setTimeout(resolve, 200));
    expect(screen.queryByText(/Связь прервалась/u)).toBeNull();
    await waitFor(() => expect(FakeWebSocket.instances).toHaveLength(1), { timeout: 3_000 });
    expect(read).toHaveBeenCalledTimes(2);
    expect(screen.queryByText(/Связь прервалась|Связь потеряна/u)).toBeNull();
    expect(screen.queryByRole('alert')).toBeNull();
  });

  it.each([
    ['model.server_overloaded', 'Модель перегружена', 'Подождите пару минут и спросите снова — вопрос сохранён.'],
    ['model.http_connection_failed', 'Нет связи с моделью', 'Связь с сервисом модели прервалась. Спросите снова через несколько минут.'],
    ['model.turn_failed', 'Не удалось получить ответ', 'Спросите снова. Если ошибка повторится — напишите в поддержку, код уже в письме.'],
    ['worker.handler_failed', 'Не удалось получить ответ', 'Спросите снова. Если ошибка повторится — напишите в поддержку, код уже в письме.'],
    ['runtime.client_start_failed', 'Не удалось получить ответ', 'Спросите снова. Если ошибка повторится — напишите в поддержку, код уже в письме.'],
  ])('%s: one block with its title, what to do, the code and a letter; one «Спросить снова»', async (code, title, hint) => {
    const { block, createTurn, jobId } = await renderFailedTurn(code);

    expect(within(block).getByText(title)).toBeTruthy();
    expect(within(block).getByText(hint)).toBeTruthy();
    expect(within(block).getByText(`mock-${jobId}`).closest('span')?.textContent).toBe(`Код для поддержки: mock-${jobId}`);
    expect(letterOf(within(block).getByRole('link', { name: 'Написать в поддержку' }))).toContain(`Canon Keeper: ошибка mock-${jobId}`);
    expect(screen.getAllByRole('alert')).toHaveLength(1);
    expect(screen.getByText('Ответ не получен.')).toBeTruthy();
    expect(document.body.textContent).not.toMatch(/Не удалось выполнить задачу|Агент не завершил/u);
    expect(screen.queryByRole('button', { name: 'Повторить' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Новая попытка' })).toBeNull();

    await waitFor(() => expect(screen.getByRole('textbox', { name: 'Вопрос' })).toHaveProperty('value', question));
    const askAgain = screen.getAllByRole('button', { name: 'Спросить снова' });
    expect(askAgain).toHaveLength(1);
    fireEvent.click(askAgain[0]!);
    await waitFor(() => expect(createTurn).toHaveBeenCalledWith(expect.any(String), { body: question }, { idempotencyKey: expect.any(String) }));
  });

  it('an interrupted turn says the answer stopped and offers to ask again', async () => {
    const { block } = await renderFailedTurn('model.turn_interrupted', 'interrupted');

    expect(within(block).getByText('Ответ прерван')).toBeTruthy();
    expect(within(block).getByText('Ответ остановили или сервис перезапускался. Спросите снова — вопрос сохранён.')).toBeTruthy();
    expect(screen.getByText('Ответ остановлен.')).toBeTruthy();
    expect(await screen.findByRole('button', { name: 'Спросить снова' })).toBeTruthy();
  });

  it('the shared model limit keeps the question in the field without an immediate retry', async () => {
    const { block } = await renderFailedTurn('model.usage_limit_exceeded');

    expect(within(block).getByText('Модель сейчас недоступна')).toBeTruthy();
    expect(within(block).getByText(/Исчерпан общий лимит обращений к модели/u)).toBeTruthy();
    expect(within(block).queryByRole('button')).toBeNull();
    await waitFor(() => expect(screen.getByRole('textbox', { name: 'Вопрос' })).toHaveProperty('value', question));
    expect(screen.queryByRole('button', { name: /Спросить снова|Повторить/u })).toBeNull();
  });

  it('a chat too long for the model leads to a new chat', async () => {
    const { block, onSelectChatId } = await renderFailedTurn('model.context_window_exceeded');

    expect(within(block).getByText('Чат стал слишком длинным')).toBeTruthy();
    expect(within(block).getByText(/Начните новый чат — книги и прежние ответы никуда не денутся/u)).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Спросить снова' })).toBeNull();
    fireEvent.click(within(block).getByRole('button', { name: 'Новый чат' }));
    expect(onSelectChatId).toHaveBeenCalledWith('');
  });

  it('a question the service did not take stays in the field with one «Спросить снова»', async () => {
    const api = createMockApiClient();
    const createTurn = vi.spyOn(api, 'createChatTurn').mockRejectedValueOnce(new ApiStatusError(503, {
      type: 'about:blank', title: 'Service Unavailable', status: 503, code: 'service_unavailable', requestId: 'send-503',
    }, new Response(null, { status: 503 })));
    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { mutations: { retry: false }, queries: { retry: false } } })}>
        <ResearchChatPanel api={api} canCreate onOpenSource={vi.fn()} onSelectChatId={vi.fn()} projectId={projectId}
          selectedChatId="" sourceView="published" />
      </QueryClientProvider>,
    );
    fireEvent.change(await screen.findByRole('textbox', { name: 'Вопрос' }), { target: { value: question } });
    fireEvent.click(screen.getByRole('button', { name: 'Спросить' }));

    const block = await screen.findByRole('alert');
    expect(within(block).getByText('Вопрос не отправлен')).toBeTruthy();
    expect(within(block).getByText('Сервис временно недоступен. Вопрос сохранён — отправьте его ещё раз через минуту.')).toBeTruthy();
    expect(within(block).getByText('send-503')).toBeTruthy();
    expect(screen.getByRole('textbox', { name: 'Вопрос' })).toHaveProperty('value', question);
    expect(screen.queryByRole('button', { name: 'Повторить' })).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Спросить снова' }));
    await waitFor(() => expect(createTurn).toHaveBeenCalledTimes(2));
  });
});
