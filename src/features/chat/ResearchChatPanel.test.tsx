import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, renderHook, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../api/mock';
import { projectId } from '../../api/mock/repository/fixtures';
import { createIdempotencyKey, type AgentTimelineEvent } from '../../shared/api';
import { FakeWebSocket } from '../../test/FakeWebSocket';
import { forgetAllTurns, useWatchedTurns } from './answer-ready';
import { ResearchChatPanel } from './ResearchChatPanel';
import { ToastsProvider } from '../../ui/feedback/Toast';

beforeEach(() => {
  vi.stubGlobal('innerWidth', 1440);
  vi.stubGlobal('innerHeight', 900);
  FakeWebSocket.reset();
  vi.stubGlobal('WebSocket', FakeWebSocket);
});

afterEach(() => vi.unstubAllGlobals());

function event(
  cursor: number,
  kind: AgentTimelineEvent['kind'],
  fields: Partial<AgentTimelineEvent> = {},
): AgentTimelineEvent {
  return {
    cursor: `tl_${cursor}`,
    id: `chat-event-${cursor}`,
    jobId: 'job-chat',
    kind,
    logicalSection: 0,
    role: 'main',
    ...fields,
  };
}

async function chatWithTurn(api: ReturnType<typeof createMockApiClient>, title: string) {
  const chat = await api.createChatSession(projectId, {
    scope: 'project',
    sourceView: 'published',
    title,
  }, { idempotencyKey: createIdempotencyKey('createChatSession') });
  const turn = await api.createChatTurn(chat.id, {
    body: 'Проверь первую главу',
  }, { idempotencyKey: createIdempotencyKey('createChatTurn') });
  return { chat, jobId: turn.jobId };
}

function renderPanel(
  api: ReturnType<typeof createMockApiClient>,
  selectedChatId: string,
) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = render(
    <QueryClientProvider client={queryClient}><ToastsProvider>
      <ResearchChatPanel
        api={api}
        canCreate
        onOpenSource={vi.fn()}
        onSelectChatId={vi.fn()}
        projectId={projectId}
        selectedChatId={selectedChatId}
        sourceView="published"
      />
    </ToastsProvider></QueryClientProvider>,
  );
  return { queryClient, view };
}

async function currentSocket(index = 0) {
  await waitFor(() => expect(FakeWebSocket.instances.length).toBeGreaterThan(index));
  const socket = FakeWebSocket.instances[index];
  if (!socket) throw new Error('WebSocket was not opened.');
  return socket;
}

function snapshot(events: AgentTimelineEvent[]) {
  return {
    cursor: events.at(-1)?.cursor ?? 0,
    snapshot: { eof: true, events, needsReconcile: false, nextCursor: null },
    type: 'snapshot' as const,
  };
}

describe('ResearchChatPanel canonical timeline', () => {
  it('reuses the same session and turn keys after both create responses are lost', async () => {
    const api = createMockApiClient();
    const createSession = api.createChatSession.bind(api);
    const createTurn = api.createChatTurn.bind(api);
    const getSession = api.getChatSession.bind(api);
    const sessionKeys: string[] = [];
    const turnKeys: string[] = [];
    let loseSessionResponse = true;
    let loseTurnResponse = true;
    let loseTurnReconciliation = false;
    vi.spyOn(api, 'createChatSession').mockImplementation(async (...args) => {
      sessionKeys.push(args[2].idempotencyKey);
      const response = await createSession(...args);
      if (loseSessionResponse) {
        loseSessionResponse = false;
        throw new TypeError('session response lost');
      }
      return response;
    });
    vi.spyOn(api, 'createChatTurn').mockImplementation(async (...args) => {
      turnKeys.push(args[2].idempotencyKey);
      const response = await createTurn(...args);
      if (loseTurnResponse) {
        loseTurnResponse = false;
        loseTurnReconciliation = true;
        throw new TypeError('turn response lost');
      }
      return response;
    });
    vi.spyOn(api, 'getChatSession').mockImplementation(async (...args) => {
      if (loseTurnReconciliation) {
        loseTurnReconciliation = false;
        throw new TypeError('reconciliation response lost');
      }
      return getSession(...args);
    });
    renderPanel(api, '');
    const composer = await screen.findByRole('textbox', { name: 'Вопрос' });
    fireEvent.change(composer, { target: { value: 'Проверь первую главу' } });

    for (let attempt = 0; attempt < 3; attempt += 1) {
      const submit = screen.getByRole('button', { name: /Спросить/u });
      await waitFor(() => expect((submit as HTMLButtonElement).disabled).toBe(false));
      fireEvent.click(submit);
      await waitFor(() => expect(sessionKeys.length + turnKeys.length).toBeGreaterThan(attempt));
    }

    await waitFor(() => expect(turnKeys).toHaveLength(2));
    expect(new Set(sessionKeys).size).toBe(1);
    expect(new Set(turnKeys).size).toBe(1);
    expect((await api.listChatSessions(projectId, {})).data).toHaveLength(1);
    expect((await api.listProjectJobs(projectId, { kind: 'research_chat_turn' })).data).toHaveLength(1);
  });

  it('renders the question, folded failed action and answer in event order without reasoning or plan', async () => {
    const api = createMockApiClient();
    const { chat } = await chatWithTurn(api, 'canonical-order');
    renderPanel(api, chat.id);
    const socket = await currentSocket();
    const events = [
      event(1, 'user', { text: 'Как называется первая глава?' }),
      event(2, 'reasoning', { text: 'Сверяю источник.' }),
      event(3, 'assistant', { text: 'Проверяю **найденное** название.' }),
      event(4, 'tool_request', { providerItemId: 'read-1', tool: { name: 'read', namespace: 'saga', arguments: { chapter: '1' } } }),
      event(5, 'tool_result', {
        providerItemId: 'read-1',
        outcome: 'failed',
        tool: { name: 'read', namespace: 'saga', arguments: { chapter: '1' }, success: false, summary: 'глава "1" не найдена' },
      }),
      event(6, 'plan', { text: 'Внутренний план' }),
    ];

    act(() => socket.message(snapshot(events)));

    const question = await screen.findByRole('note', { name: 'Вопрос автора' });
    const remark = await screen.findByText('найденное');
    // specs/2026-11-10-ux1-names.md N2.4: the step the helper corrected is an ordinary line, without the message for the model.
    const action = screen.getByText('Не нашёл такую главу — ищу иначе').closest('li')!;
    expect(action.textContent).toBe('Не нашёл такую главу — ищу иначе (не получилось)');
    // The Job is not running, so the last remark answers the turn after its folded work.
    expect(remark.closest('[data-answer]')).not.toBeNull();
    expect(action.closest('details')?.textContent).toContain('Как я искал');
    expect(question.compareDocumentPosition(action) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(action.compareDocumentPosition(remark) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(screen.queryByRole('alert')).toBeNull();
    expect(screen.queryByRole('region', { name: 'План выполнения' })).toBeNull();
    expect(document.body.textContent).not.toContain('Внутренний план');
    expect(document.body.textContent).not.toContain('Сверяю источник.');
    expect(document.body.textContent).not.toContain('thread_id');
  });

  it('awaits a turn still running when its chat opens, on every page', async () => {
    forgetAllTurns();
    const api = createMockApiClient();
    const { chat, jobId } = await chatWithTurn(api, 'awaited-turn');
    const sourceGetJob = api.getJob.bind(api);
    vi.spyOn(api, 'getJob').mockImplementation(async (id) => ({ ...await sourceGetJob(id), canCancel: true, status: 'running' }));
    const watched = renderHook(() => useWatchedTurns());
    renderPanel(api, chat.id);
    await waitFor(() => expect(watched.result.current).toEqual([
      { chatId: chat.id, jobId, label: 'awaited-turn', projectId },
    ]));
  });

  it('uses the exact latest-turn Job and invalidates it after a terminal event', async () => {
    const api = createMockApiClient();
    const { chat, jobId } = await chatWithTurn(api, 'canonical-terminal');
    const retryTurn = vi.spyOn(api, 'createChatTurn');
    const sourceGetJob = api.getJob.bind(api);
    let terminal = false;
    const listProjectJobs = vi.spyOn(api, 'listProjectJobs');
    vi.spyOn(api, 'getJob').mockImplementation(async (id) => {
      const job = await sourceGetJob(id);
      return terminal && id === jobId ? { ...job, status: 'failed', canCancel: false } : job;
    });
    renderPanel(api, chat.id);
    const socket = await currentSocket();
    act(() => socket.message(snapshot([
      event(1, 'user', { jobId, text: 'Проверь первую главу' }),
    ])));

    terminal = true;
    act(() => socket.message({
      cursor: 'tl_4',
      event: event(4, 'turn_outcome', { id: 'terminal', jobId, outcome: 'failed', text: 'Задача завершилась с ошибкой.' }),
      type: 'event',
    }));

    expect(listProjectJobs).not.toHaveBeenCalled();
    // specs/2026-11-10-ux4-chat-waiting.md CW2.8: one block of the end of the turn, in the word «помощник».
    expect(await screen.findByText('Помощник не смог ответить')).toBeTruthy();
    expect(screen.queryByText(/Ответ не получен/u)).toBeNull();
    // The question returns to the field; its «Спросить снова» sends it (R4.2.3).
    expect(screen.queryByRole('button', { name: 'Повторить' })).toBeNull();
    fireEvent.click(await screen.findByRole('button', { name: 'Спросить снова' }));
    await waitFor(() => expect(retryTurn).toHaveBeenCalledWith(
      chat.id,
      { body: 'Проверь первую главу' },
      { idempotencyKey: expect.any(String) },
    ));
  });

  it('explains a turn refused because the chat runtime did not start', async () => {
    const api = createMockApiClient();
    const { chat, jobId } = await chatWithTurn(api, 'runtime-down');
    const sourceGetJob = api.getJob.bind(api);
    vi.spyOn(api, 'getJob').mockImplementation(async (id) => {
      const job = await sourceGetJob(id);
      return id === jobId
        ? {
          ...job, status: 'failed', canCancel: false,
          error: { type: 'about:blank', title: 'Job failed', status: 500, code: 'runtime.client_start_failed', requestId: 'runtime-down-1' },
        }
        : job;
    });
    renderPanel(api, chat.id);

    expect(await screen.findByText('Помощник не смог ответить')).toBeTruthy();
    expect(screen.getByText('runtime-down-1').closest('span')?.textContent).toBe('Код для поддержки: runtime-down-1');
  });

  it('reconciles a terminal turn when the cancel response is lost', async () => {
    const api = createMockApiClient();
    const chat = await api.createChatSession(projectId, {
      scope: 'project',
      sourceView: 'published',
      title: 'ambiguous-cancel',
    }, { idempotencyKey: createIdempotencyKey('createChatSession') });
    const getJob = api.getJob.bind(api);
    const cancelJob = api.cancelJob.bind(api);
    let cancelRequested = false;
    let postCancelReads = 0;
    vi.spyOn(api, 'getJob').mockImplementation(async (jobId) => {
      const job = await getJob(jobId);
      if (cancelRequested && postCancelReads++ === 0) {
        return { ...job, status: 'canceling', canCancel: true };
      }
      return job;
    });
    vi.spyOn(api, 'cancelJob').mockImplementation(async (jobId) => {
      await cancelJob(jobId);
      cancelRequested = true;
      throw new Error('connection lost');
    });
    renderPanel(api, chat.id);

    fireEvent.change(await screen.findByRole('textbox', { name: 'Вопрос' }), {
      target: { value: 'Проверь первую главу' },
    });
    const start = screen.getByRole('button', { name: 'Спросить' });
    await waitFor(() => expect((start as HTMLButtonElement).disabled).toBe(false));
    fireEvent.click(start);

    await pressStop(api);

    expect(await screen.findByRole('button', { name: 'Спросить снова' })).toBeTruthy();
    expect((screen.getByRole('button', { name: 'Новый чат' }) as HTMLButtonElement).disabled).toBe(false);
    expect(screen.queryByText(/Не удалось остановить ответ/u)).toBeNull();
  });

  it('does not restore the local task when accepted cancellation resolves as succeeded', async () => {
    const api = createMockApiClient();
    vi.spyOn(api, 'getAgentTimeline').mockResolvedValue({
      events: [],
      eof: true,
      nextCursor: null,
      needsReconcile: false,
    });
    const chat = await api.createChatSession(projectId, {
      scope: 'project',
      sourceView: 'published',
      title: 'successful-terminal-race',
    }, { idempotencyKey: createIdempotencyKey('createChatSession') });
    const cancelJob = api.cancelJob.bind(api);
    const getJob = api.getJob.bind(api);
    let cancelReturned = false;
    vi.spyOn(api, 'getJob').mockImplementation(async (jobId) => {
      const job = await getJob(jobId);
      if (!cancelReturned) return job;
      return { ...job, status: 'succeeded', canCancel: false };
    });
    vi.spyOn(api, 'cancelJob').mockImplementation(async (jobId) => {
      const job = await cancelJob(jobId);
      if (!job) throw new Error('Readable Chat cancel did not return a Job.');
      cancelReturned = true;
      return { ...job, status: 'canceling', canCancel: true };
    });
    renderPanel(api, chat.id);

    fireEvent.change(await screen.findByRole('textbox', { name: 'Вопрос' }), {
      target: { value: 'Проверь первую главу' },
    });
    const start = screen.getByRole('button', { name: 'Спросить' });
    await waitFor(() => expect((start as HTMLButtonElement).disabled).toBe(false));
    fireEvent.click(start);
    fireEvent.click(await screen.findByRole('button', { name: 'Остановить' }));

    await waitFor(
      () => expect(screen.queryByRole('button', { name: 'Остановить' })).toBeNull(),
    );
    expect(screen.queryByRole('button', { name: 'Спросить снова' })).toBeNull();
    expect((screen.getByRole('textbox', { name: 'Вопрос' }) as HTMLTextAreaElement).value).toBe('');
    expect((screen.getByRole('button', { name: 'Новый чат' }) as HTMLButtonElement).disabled).toBe(false);
  });

  it('reconciles an ordinary successful turn without a timeline job outcome', async () => {
    const api = createMockApiClient();
    vi.spyOn(api, 'getAgentTimeline').mockResolvedValue({
      events: [],
      eof: true,
      nextCursor: null,
      needsReconcile: false,
    });
    const chat = await api.createChatSession(projectId, {
      scope: 'project',
      sourceView: 'published',
      title: 'ordinary-success-query',
    }, { idempotencyKey: createIdempotencyKey('createChatSession') });
    const getJob = api.getJob.bind(api);
    let jobId = '';
    let activeReads = 0;
    vi.spyOn(api, 'getJob').mockImplementation(async (id) => {
      const job = await getJob(id);
      if (id !== jobId) return job;
      if (activeReads++ === 0) return { ...job, status: 'running', canCancel: true };
      return { ...job, status: 'succeeded', canCancel: false };
    });
    renderPanel(api, chat.id);

    fireEvent.change(await screen.findByRole('textbox', { name: 'Вопрос' }), {
      target: { value: 'Проверь первую главу' },
    });
    const start = screen.getByRole('button', { name: 'Спросить' });
    await waitFor(() => expect((start as HTMLButtonElement).disabled).toBe(false));
    fireEvent.click(start);
    await screen.findByRole('button', { name: 'Остановить' });
    const session = await api.getChatSession(chat.id);
    jobId = session.latestTurnJobId ?? '';
    if (!jobId) throw new Error('Chat turn Job was not created.');

    await waitFor(
      () => expect(screen.queryByRole('button', { name: 'Остановить' })).toBeNull(),
      { timeout: 6_000 },
    );
    expect(screen.queryByRole('button', { name: 'Спросить снова' })).toBeNull();
    expect((screen.getByRole('textbox', { name: 'Вопрос' }) as HTMLTextAreaElement).value).toBe('');
    expect((screen.getByRole('button', { name: 'Новый чат' }) as HTMLButtonElement).disabled).toBe(false);
  }, 10_000);

  it('does not restore a canceled task after switching chat during refetch', async () => {
    const api = createMockApiClient();
    const first = await api.createChatSession(projectId, {
      scope: 'project',
      sourceView: 'published',
      title: 'cancel-before-switch',
    }, { idempotencyKey: createIdempotencyKey('createChatSession') });
    const second = await api.createChatSession(projectId, {
      scope: 'project',
      sourceView: 'published',
      title: 'switch-target',
    }, { idempotencyKey: createIdempotencyKey('createChatSession') });
    const getJob = api.getJob.bind(api);
    const cancelJob = api.cancelJob.bind(api);
    let jobId = '';
    let cancelReturned = false;
    let releaseRead = () => {};
    let readWaiting = false;
    const readGate = new Promise<void>((resolve) => {
      releaseRead = resolve;
    });
    vi.spyOn(api, 'getJob').mockImplementation(async (id) => {
      const job = await getJob(id);
      if (cancelReturned) {
        readWaiting = true;
        await readGate;
      }
      return job;
    });
    vi.spyOn(api, 'cancelJob').mockImplementation(async (id) => {
      const job = await cancelJob(id);
      if (!job) throw new Error('Readable Chat cancel did not return a Job.');
      cancelReturned = true;
      return { ...job, status: 'canceling', canCancel: true };
    });
    const rendered = renderPanel(api, first.id);

    fireEvent.change(await screen.findByRole('textbox', { name: 'Вопрос' }), {
      target: { value: 'Задача первого чата' },
    });
    const start = screen.getByRole('button', { name: 'Спросить' });
    await waitFor(() => expect((start as HTMLButtonElement).disabled).toBe(false));
    fireEvent.click(start);
    await screen.findByRole('button', { name: 'Остановить' });
    const session = await api.getChatSession(first.id);
    jobId = session.latestTurnJobId ?? '';
    if (!jobId) throw new Error('Chat turn Job was not created.');
    fireEvent.click(screen.getByRole('button', { name: 'Остановить' }));
    await waitFor(() => expect(readWaiting).toBe(true));

    rendered.view.rerender(
      <QueryClientProvider client={rendered.queryClient}><ToastsProvider>
        <ResearchChatPanel
          api={api}
          canCreate
          onOpenSource={vi.fn()}
          onSelectChatId={vi.fn()}
          projectId={projectId}
          selectedChatId={second.id}
          sourceView="published"
        />
      </ToastsProvider></QueryClientProvider>,
    );
    const secondTask = await screen.findByRole('textbox', { name: 'Вопрос' });
    fireEvent.change(secondTask, { target: { value: 'Задача второго чата' } });
    await waitFor(() => expect((secondTask as HTMLTextAreaElement).value).toBe('Задача второго чата'));
    await act(async () => {
      releaseRead();
    });
    await waitFor(() => expect(
      (screen.getByRole('button', { name: 'Спросить' }) as HTMLButtonElement).disabled,
    ).toBe(false));

    expect((screen.getByRole('textbox', { name: 'Вопрос' }) as HTMLTextAreaElement).value)
      .toBe('Задача второго чата');
  });

  it('does not accept stale terminal data when cancel reconciliation fails', async () => {
    const api = createMockApiClient();
    const { chat } = await chatWithTurn(api, 'stale-cancel-reconciliation');
    const getJob = api.getJob.bind(api);
    let failReads = false;
    vi.spyOn(api, 'getJob').mockImplementation(async (jobId) => {
      if (failReads) throw new Error('read failed');
      return getJob(jobId);
    });
    vi.spyOn(api, 'cancelJob').mockImplementation(async () => {
      failReads = true;
      throw new Error('cancel failed');
    });
    renderPanel(api, chat.id);

    fireEvent.change(await screen.findByRole('textbox', { name: 'Вопрос' }), {
      target: { value: 'Проверь вторую главу' },
    });
    const start = screen.getByRole('button', { name: 'Спросить' });
    await waitFor(() => expect((start as HTMLButtonElement).disabled).toBe(false));
    fireEvent.click(start);
    await pressStop(api);

    expect(await screen.findByText('Не удалось остановить ответ. Попробуйте ещё раз.')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Остановить' })).toBeTruthy();
  });

  it('subscribes by each chat session latestTurnJobId when switching sessions', async () => {
    const api = createMockApiClient();
    const first = await chatWithTurn(api, 'first-session');
    const second = await chatWithTurn(api, 'second-session');
    const rendered = renderPanel(api, first.chat.id);
    const firstSocket = await currentSocket();
    expect(firstSocket.url).toContain(`/jobs/${first.jobId}/agent-timeline/stream`);

    rendered.view.rerender(
      <QueryClientProvider client={rendered.queryClient}><ToastsProvider>
        <ResearchChatPanel
          api={api}
          canCreate
          onOpenSource={vi.fn()}
          onSelectChatId={vi.fn()}
          projectId={projectId}
          selectedChatId={second.chat.id}
          sourceView="published"
        />
      </ToastsProvider></QueryClientProvider>,
    );
    const secondSocket = await currentSocket(1);
    expect(secondSocket.url).toContain(`/jobs/${second.jobId}/agent-timeline/stream`);
    expect(secondSocket.url).not.toContain(`/jobs/${first.jobId}/`);
  });
});

/**
 * «Остановить» stands in the status of the running turn (specs/2026-11-10-ux4-chat-waiting.md CW2.4): the turn of the
 * question just sent gives way to the turn of its first event, so the press takes the button the page shows now.
 */
async function pressStop(api: { cancelJob: unknown }) {
  const cancel = api.cancelJob as { mock: { calls: unknown[] } };
  await waitFor(() => {
    if (!cancel.mock.calls.length) fireEvent.click(screen.getByRole('button', { name: 'Остановить' }));
    expect(cancel.mock.calls.length).toBeGreaterThan(0);
  });
}
