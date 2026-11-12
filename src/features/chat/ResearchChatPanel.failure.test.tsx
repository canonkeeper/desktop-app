import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../api/mock';
import { projectId } from '../../api/mock/repository/fixtures';
import { normalScenario } from '../../api/mock/scenarios';
import { ApiStatusError, createIdempotencyKey, type AgentTimelineEvent } from '../../shared/api';
import { FakeWebSocket } from '../../test/FakeWebSocket';
import { ResearchChatPanel } from './ResearchChatPanel';
import { ToastsProvider } from '../../ui/feedback/Toast';

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
    <QueryClientProvider client={new QueryClient({ defaultOptions: { mutations: { retry: false }, queries: { retry: false } } })}><ToastsProvider>
      <ResearchChatPanel api={api} canCreate onOpenSource={vi.fn()} onSelectChatId={onSelectChatId} projectId={projectId}
        selectedChatId={chat.id} sourceView="published" />
    </ToastsProvider></QueryClientProvider>,
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
      <QueryClientProvider client={new QueryClient({ defaultOptions: { mutations: { retry: false }, queries: { retry: false } } })}><ToastsProvider>
        <ResearchChatPanel api={api} canCreate onOpenSource={vi.fn()} onSelectChatId={vi.fn()} projectId={projectId}
          selectedChatId={chat.id} sourceView="published" />
      </ToastsProvider></QueryClientProvider>,
    );

    await waitFor(() => expect(read).toHaveBeenCalledTimes(1));
    await new Promise((resolve) => setTimeout(resolve, 200));
    expect(screen.queryByText(/Связь прервалась/u)).toBeNull();
    await waitFor(() => expect(FakeWebSocket.instances).toHaveLength(1), { timeout: 3_000 });
    expect(read).toHaveBeenCalledTimes(2);
    expect(screen.queryByText(/Связь прервалась|Связь потеряна/u)).toBeNull();
    expect(screen.queryByRole('alert')).toBeNull();
  });

  // specs/2026-11-10-ux1-chat.md CH3.6; specs/2026-11-10-ux4-chat-waiting.md CW2.8: one block of the common look under the
  // question in the word «помощник», the count as it is (the server gave the unit back), «Повторить вопрос», the code and
  // a letter; no second line of the feed.
  it.each([
    ['model.server_overloaded', 'Помощник сейчас перегружен', 'Подождите пару минут и спросите снова — вопрос сохранён. Этот вопрос не засчитан.'],
    ['model.http_connection_failed', 'Нет связи с помощником', 'Связь с помощником прервалась. Спросите снова через несколько минут. Этот вопрос не засчитан.'],
    ['model.turn_failed', 'Помощник не смог ответить', 'Спросите снова. Если ошибка повторится, напишите нам: код ошибки приложим сами. Этот вопрос не засчитан.'],
    ['worker.handler_failed', 'Помощник не смог ответить', 'Спросите снова. Если ошибка повторится, напишите нам: код ошибки приложим сами. Этот вопрос не засчитан.'],
    ['runtime.client_start_failed', 'Помощник не смог ответить', 'Спросите снова. Если ошибка повторится, напишите нам: код ошибки приложим сами. Этот вопрос не засчитан.'],
  ])('%s: one block with its title, what to do, the count, the code and a letter', async (code, title, hint) => {
    const { block, createTurn, jobId } = await renderFailedTurn(code);

    expect(block.getAttribute('aria-label')).toBe(title);
    expect(within(block).getByText(hint)).toBeTruthy();
    expect(within(block).getByText(`mock-${jobId}`).closest('span')?.textContent).toBe(`Код для поддержки: mock-${jobId}`);
    expect(letterOf(within(block).getByRole('link', { name: 'Написать нам' }))).toContain(`CanonKeeper: ошибка mock-${jobId}`);
    expect(screen.getAllByRole('alert')).toHaveLength(1);
    // The block stands under the question of its turn; the feed says nothing a second time.
    const question = screen.getByRole('note', { name: 'Вопрос автора' });
    expect(question.compareDocumentPosition(block) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(document.body.textContent).not.toMatch(/Ответ не получен|Не удалось выполнить задачу|Агент не завершил|модел/u);
    expect(screen.queryByRole('button', { name: 'Повторить' })).toBeNull();

    await waitFor(() => expect(screen.getByRole('textbox', { name: 'Вопрос' })).toHaveProperty('value', question.textContent));
    // «Повторить вопрос» puts the question into the field; «Спросить снова» of the field sends it.
    fireEvent.change(screen.getByRole('textbox', { name: 'Вопрос' }), { target: { value: '' } });
    fireEvent.click(within(block).getByRole('button', { name: 'Повторить вопрос' }));
    expect(screen.getByRole('textbox', { name: 'Вопрос' })).toHaveProperty('value', question.textContent);
    expect(document.activeElement).toBe(screen.getByRole('textbox', { name: 'Вопрос' }));
    const askAgain = screen.getAllByRole('button', { name: 'Спросить снова' });
    expect(askAgain).toHaveLength(1);
    fireEvent.click(askAgain[0]!);
    await waitFor(() => expect(createTurn).toHaveBeenCalledWith(expect.any(String), { body: question.textContent }, { idempotencyKey: expect.any(String) }));
  });

  it('an interrupted turn says the service stopped it and offers to ask again', async () => {
    const { block } = await renderFailedTurn('model.turn_interrupted', 'interrupted');

    expect(block.getAttribute('aria-label')).toBe('Ответ прервался');
    expect(within(block).getByText('Мы обновляли сервис, и ответ остановился. Спросите снова — вопрос сохранён. Этот вопрос не засчитан.')).toBeTruthy();
    expect(screen.queryByText(/Вы остановили/u)).toBeNull();
    expect(await screen.findByRole('button', { name: 'Спросить снова' })).toBeTruthy();
  });

  // specs/2026-11-11-ux9-shelf-chat.md SC3.2 (Ч-32): the shared limit of the service says when to come back and gives
  // «Повторить вопрос», the question uncounted.
  it('the shared limit of the service says «Сервис перегружен», when to repeat, and gives «Повторить вопрос»', async () => {
    const { block } = await renderFailedTurn('model.usage_limit_exceeded');

    expect(block.getAttribute('aria-label')).toBe('Сервис перегружен');
    expect(within(block).getByText('Вопрос не засчитан — повторите через несколько минут.')).toBeTruthy();
    await waitFor(() => expect(screen.getByRole('textbox', { name: 'Вопрос' })).toHaveProperty('value', question));
    fireEvent.change(screen.getByRole('textbox', { name: 'Вопрос' }), { target: { value: '' } });
    fireEvent.click(within(block).getByRole('button', { name: 'Повторить вопрос' }));
    expect(screen.getByRole('textbox', { name: 'Вопрос' })).toHaveProperty('value', question);
  });

  it('a chat too long for the helper leads to a new chat', async () => {
    const { block, onSelectChatId } = await renderFailedTurn('model.context_window_exceeded');

    expect(block.getAttribute('aria-label')).toBe('Разговор стал слишком длинным для помощника');
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
      <QueryClientProvider client={new QueryClient({ defaultOptions: { mutations: { retry: false }, queries: { retry: false } } })}><ToastsProvider>
        <ResearchChatPanel api={api} canCreate onOpenSource={vi.fn()} onSelectChatId={vi.fn()} projectId={projectId}
          selectedChatId="" sourceView="published" />
      </ToastsProvider></QueryClientProvider>,
    );
    fireEvent.change(await screen.findByRole('textbox', { name: 'Вопрос' }), { target: { value: question } });
    fireEvent.click(screen.getByRole('button', { name: 'Спросить' }));

    const block = await screen.findByRole('alert');
    expect(within(block).getByText('Вопрос не отправлен')).toBeTruthy();
    // A question that never went was not counted at all: nothing to give back, nothing to say of it.
    expect(within(block).getByText('Сервис временно недоступен. Вопрос сохранён — отправьте его ещё раз через минуту.')).toBeTruthy();
    expect(block.textContent).not.toContain('не засчитан');
    expect(within(block).getByText('send-503')).toBeTruthy();
    expect(screen.getByRole('textbox', { name: 'Вопрос' })).toHaveProperty('value', question);
    expect(screen.queryByRole('button', { name: 'Повторить' })).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Спросить снова' }));
    await waitFor(() => expect(createTurn).toHaveBeenCalledTimes(2));
  });
});
