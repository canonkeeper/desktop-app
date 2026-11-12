import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../api/mock';
import { projectId } from '../../api/mock/repository/fixtures';
import { ApiStatusError, type AgentTimelineEvent } from '../../shared/api';
import { FakeWebSocket } from '../../test/FakeWebSocket';
import { ResearchChatPanel } from './ResearchChatPanel';
import { ToastsProvider } from '../../ui/feedback/Toast';

beforeEach(() => {
  vi.stubGlobal('innerWidth', 1440);
  vi.stubGlobal('innerHeight', 900);
  FakeWebSocket.reset();
  vi.stubGlobal('WebSocket', FakeWebSocket);
});

afterEach(() => vi.unstubAllGlobals());

function timelineEvent(
  jobId: string,
  cursor: number,
  kind: AgentTimelineEvent['kind'],
  fields: Partial<AgentTimelineEvent> = {},
): AgentTimelineEvent {
  return {
    cursor: `tl_${cursor}`,
    id: `authority-event-${cursor}`,
    jobId,
    kind,
    logicalSection: 0,
    role: 'main',
    ...fields,
  };
}

async function sessionWithTurn(api: ReturnType<typeof createMockApiClient>) {
  const session = await api.createChatSession(projectId, {
    scope: 'project', sourceView: 'published', title: 'Authority',
  }, { idempotencyKey: 'idem-authority-session' });
  const turn = await api.createChatTurn(session.id, { body: 'Вопрос' }, {
    idempotencyKey: 'idem-authority-turn',
  });
  return { jobId: turn.jobId, session };
}

function renderPanel(
  api: ReturnType<typeof createMockApiClient>,
  selectedChatId: string,
  canCreate = true,
  queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } }),
) {
  const view = render(
    <QueryClientProvider client={queryClient}><ToastsProvider>
      <ResearchChatPanel
        api={api}
        canCreate={canCreate}
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

async function socketFor(jobId: string, index = 0) {
  await waitFor(() => expect(
    FakeWebSocket.instances.filter((socket) => socket.url.includes(`/jobs/${jobId}/`)).length,
  ).toBeGreaterThan(index));
  return FakeWebSocket.instances.filter((socket) => socket.url.includes(`/jobs/${jobId}/`))[index]!;
}

describe('Research Chat authority', () => {
  it('polls a restored active Job until terminal even without a timeline outcome', async () => {
    const api = createMockApiClient();
    const { jobId, session } = await sessionWithTurn(api);
    const text = 'Проверяю **источник** <b>главы</b>.';
    vi.spyOn(api, 'getAgentTimeline').mockResolvedValue({
      events: [timelineEvent(jobId, 1, 'assistant', { phase: 'commentary', text })],
      eof: true, needsReconcile: false,
    });
    const getJob = api.getJob.bind(api);
    let reads = 0;
    vi.spyOn(api, 'getJob').mockImplementation(async (id) => ({ ...await getJob(id),
      status: reads++ === 0 ? 'running' : 'succeeded', canCancel: reads === 1,
    }));
    renderPanel(api, session.id);
    await screen.findByRole('button', { name: 'Остановить' });
    const live = await screen.findByRole('region', { name: 'Готовим ответ' });
    await waitFor(() => expect(live.textContent).toContain('Думаю, где искать дальше'));
    const remark = (await screen.findByText('источник')).closest('[data-activity-kind="assistant"]')!;
    expect(remark.querySelector('strong')?.textContent).toBe('источник');
    expect(remark.querySelector('b')).toBeNull();

    await waitFor(() => expect(screen.queryByRole('button', { name: 'Остановить' })).toBeNull(), { timeout: 3_500 });
    expect(screen.queryByRole('region', { name: 'Готовим ответ' })).toBeNull();
    expect(screen.getByRole('textbox', { name: 'Вопрос' })).toHaveProperty('disabled', false);
  });

  it('drops the live work of the previous chat immediately when switching sessions', async () => {
    const api = createMockApiClient();
    const { jobId, session } = await sessionWithTurn(api);
    const second = await api.createChatSession(projectId, {
      scope: 'project', sourceView: 'published', title: 'Другой чат',
    }, { idempotencyKey: 'idem-focus-second-session' });
    const getJob = api.getJob.bind(api);
    vi.spyOn(api, 'getJob').mockImplementation(async (id) => ({ ...await getJob(id), status: 'running', canCancel: true }));
    vi.spyOn(api, 'getAgentTimeline').mockResolvedValue({
      events: [timelineEvent(jobId, 1, 'assistant', { phase: 'commentary', text: 'Проверяю первую главу.' })],
      eof: true, needsReconcile: false,
    });
    const { queryClient, view } = renderPanel(api, session.id);
    await screen.findByRole('region', { name: 'Готовим ответ' });

    view.rerender(<QueryClientProvider client={queryClient}><ToastsProvider>
      <ResearchChatPanel api={api} canCreate onOpenSource={vi.fn()} onSelectChatId={vi.fn()}
        projectId={projectId} selectedChatId={second.id} sourceView="published" />
    </ToastsProvider></QueryClientProvider>);

    expect(screen.queryByRole('region', { name: 'Готовим ответ' })).toBeNull();
  });

  it('removes mutations after cancel 403 even if the exact Job has already succeeded', async () => {
    const api = createMockApiClient();
    const { session } = await sessionWithTurn(api);
    const getJob = api.getJob.bind(api);
    let canceled = false;
    vi.spyOn(api, 'getJob').mockImplementation(async (id) => ({ ...await getJob(id), status: canceled ? 'succeeded' : 'running', canCancel: !canceled }));
    vi.spyOn(api, 'cancelJob').mockImplementation(async () => {
      canceled = true;
      throw new ApiStatusError(403, { type: 'about:blank', status: 403, title: 'Forbidden', code: 'forbidden', requestId: 'cancel-authority-loss' }, new Response(null, { status: 403 }));
    });
    renderPanel(api, session.id);
    fireEvent.click(await screen.findByRole('button', { name: 'Остановить' }));

    await waitFor(() => expect(screen.queryByRole('textbox', { name: 'Вопрос' })).toBeNull());
    expect(screen.getByRole('button', { name: session.title })).toBeTruthy();
  });

  it('does not clear the pending Stop of a new chat when the previous Stop finishes', async () => {
    const api = createMockApiClient();
    const first = await sessionWithTurn(api);
    const second = await api.createChatSession(projectId, { scope: 'project', sourceView: 'published', title: 'Второй запуск' }, { idempotencyKey: 'idem-second-stop-session' });
    const secondTurn = await api.createChatTurn(second.id, { body: 'Второй вопрос' }, { idempotencyKey: 'idem-second-stop-turn' });
    const firstJob = await api.getJob(first.jobId);
    const secondJob = await api.getJob(secondTurn.jobId);
    vi.spyOn(api, 'getAgentTimeline').mockResolvedValue({ events: [], eof: true, needsReconcile: false });
    vi.spyOn(api, 'getJob').mockImplementation(async (id) => ({ ...(id === firstJob.id ? firstJob : secondJob), status: 'running', canCancel: true }));
    let finishFirst!: (job: typeof firstJob) => void;
    let finishSecond!: (job: typeof secondJob) => void;
    const cancel = vi.spyOn(api, 'cancelJob').mockImplementation((id) => new Promise((resolve) => {
      if (id === firstJob.id) finishFirst = resolve;
      else finishSecond = resolve;
    }));
    const { queryClient, view } = renderPanel(api, first.session.id);
    fireEvent.click(await screen.findByRole('button', { name: 'Остановить' }));
    await waitFor(() => expect(cancel).toHaveBeenCalledTimes(1));
    view.rerender(<QueryClientProvider client={queryClient}><ToastsProvider>
      <ResearchChatPanel api={api} canCreate onOpenSource={vi.fn()} onSelectChatId={vi.fn()}
        projectId={projectId} selectedChatId={second.id} sourceView="published" />
    </ToastsProvider></QueryClientProvider>);
    const stop = await screen.findByRole('button', { name: 'Остановить' });
    await waitFor(() => expect(stop).toHaveProperty('disabled', false));
    fireEvent.click(stop);
    await waitFor(() => expect(cancel).toHaveBeenCalledTimes(2));
    await act(async () => finishFirst({ ...firstJob, status: 'canceled', canCancel: false }));

    expect(screen.getByRole('button', { name: 'Остановить' })).toHaveProperty('disabled', true);
    await act(async () => finishSecond({ ...secondJob, status: 'canceled', canCancel: false }));
  });

  it('shows cancellation failure for a restored Job even before its user item arrives', async () => {
    const api = createMockApiClient();
    const { session } = await sessionWithTurn(api);
    vi.spyOn(api, 'getAgentTimeline').mockResolvedValue({ events: [], eof: true, needsReconcile: false });
    const getJob = api.getJob.bind(api);
    let cancelRequested = false;
    vi.spyOn(api, 'getJob').mockImplementation(async (jobId) => {
      if (cancelRequested) throw new Error('connection lost');
      return { ...await getJob(jobId), status: 'running', canCancel: true };
    });
    const cancel = vi.spyOn(api, 'cancelJob').mockImplementation(async () => {
      cancelRequested = true;
      throw new ApiStatusError(503, {
        type: 'about:blank', title: 'Unavailable', status: 503, code: 'service_unavailable', requestId: 'cancel-restored-job',
      }, new Response(null, { status: 503 }));
    });
    renderPanel(api, session.id);
    fireEvent.click(await screen.findByRole('button', { name: 'Остановить' }));

    expect(await screen.findByText('Не удалось остановить ответ. Попробуйте ещё раз.')).toBeTruthy();
    expect(screen.getByText('cancel-restored-job').closest('span')?.textContent).toBe('Код для поддержки: cancel-restored-job');
    fireEvent.click(screen.getByRole('button', { name: 'Повторить' }));
    await waitFor(() => expect(cancel).toHaveBeenCalledTimes(2));
  });

  it('removes mutation controls after turn creation returns 403 and keeps the session readable', async () => {
    const api = createMockApiClient();
    const session = await api.createChatSession(projectId, {
      scope: 'project', sourceView: 'published', title: 'Сохранённая история',
    }, { idempotencyKey: 'idem-authority-loss-session' });
    await api.createChatTurn(session.id, { body: 'Прежний вопрос' }, { idempotencyKey: 'idem-authority-loss-prior' });
    vi.spyOn(api, 'createChatTurn').mockRejectedValue(new ApiStatusError(403, {
      type: 'about:blank', title: 'Forbidden', status: 403, code: 'forbidden', requestId: 'turn-authority-loss',
    }, new Response(null, { status: 403 })));
    renderPanel(api, session.id);
    fireEvent.change(await screen.findByRole('textbox', { name: 'Вопрос' }), { target: { value: 'Новая задача' } });
    fireEvent.click(screen.getByRole('button', { name: 'Спросить' }));

    await waitFor(() => expect(screen.queryByRole('textbox', { name: 'Вопрос' })).toBeNull());
    expect(screen.queryByRole('button', { name: 'Новый чат' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Удалить чат' })).toBeNull();
    expect(screen.getByRole('button', { name: session.title })).toBeTruthy();
  });

  it.each([true, false])('does not announce a running agent in an empty chat (editor=%s)', async (canCreate) => {
    const api = createMockApiClient();
    const timeline = vi.spyOn(api, 'getAgentTimeline');
    renderPanel(api, '', canCreate);

    await screen.findByText(canCreate
      ? /Спросите о героях, местах и событиях книги/u
      : /В этой саге вы читатель: чаты ведёт автор/u);
    expect(screen.queryByText('Задача запущена')).toBeNull();
    expect(screen.queryByRole('region', { name: 'Готовим ответ' })).toBeNull();
    expect(timeline).not.toHaveBeenCalled();
  });

  it('keeps a viewer on the running Job live through assistant events and reconnecting without mutation controls', async () => {
    const api = createMockApiClient();
    const { jobId, session } = await sessionWithTurn(api);
    const getJob = api.getJob.bind(api);
    vi.spyOn(api, 'getJob').mockImplementation(async (id) => ({ ...await getJob(id), status: 'running', canCancel: false }));
    vi.spyOn(api, 'getAgentTimeline').mockResolvedValue({ events: [], eof: true, needsReconcile: false });
    renderPanel(api, session.id, false);
    const socket = await socketFor(jobId);
    await screen.findByRole('region', { name: 'Готовим ответ' });

    for (const cursor of [1, 2]) {
      const answer = timelineEvent(jobId, cursor, 'assistant', { text: `Фрагмент ${cursor}` });
      act(() => socket.message({ cursor: answer.cursor, event: answer, type: 'event' }));
      expect(await screen.findByText(answer.text!)).toBeTruthy();
      act(() => socket.message({ type: 'reconnecting', message: 'Восстанавливаем соединение.' }));
      // The stream recovers by itself: a quiet line under the status of the turn, no block and no button
      // (specs/2026-11-11-ux4-after-answer.md AA2.6, Ч-74).
      expect(await screen.findByText('Связь прервалась — восстанавливаю, ответ готовится')).toBeTruthy();
      expect(screen.queryByRole('alert')).toBeNull();
      expect(screen.queryByRole('button', { name: 'Повторить' })).toBeNull();
      expect(screen.getByRole('region', { name: 'Готовим ответ' })).toBeTruthy();
      expect(screen.queryByRole('textbox', { name: 'Вопрос' })).toBeNull();
      expect(screen.queryByRole('button', { name: 'Остановить' })).toBeNull();
      expect(screen.queryByRole('button', { name: 'Новый чат' })).toBeNull();
    }
    expect(screen.getByText('Фрагмент 1')).toBeTruthy();
  });

  it('keeps both question-answer pairs in server order after replay, reconnect and reload', async () => {
    const api = createMockApiClient();
    const { jobId, session } = await sessionWithTurn(api);
    const first = timelineEvent(jobId, 1, 'user', {
      text: 'Первый вопрос', providerTurnId: 'prior-turn', providerItemId: 'prior-user',
    });
    const second = timelineEvent(jobId, 2, 'assistant', {
      text: 'Первый ответ', providerTurnId: 'prior-turn', providerItemId: 'prior-answer',
    });
    const third = timelineEvent(jobId, 3, 'user', {
      text: 'Второй вопрос', providerTurnId: 'current-turn', providerItemId: 'current-user',
    });
    const fourth = timelineEvent(jobId, 4, 'assistant', {
      text: 'Второй ответ', providerTurnId: 'current-turn', providerItemId: 'current-answer',
    });
    const timeline = vi.spyOn(api, 'getAgentTimeline').mockImplementation(async (_project, _job, options = {}) => ({
      events: options.cursor === second.cursor ? [second, third, fourth] : [first],
      eof: true,
      nextCursor: null,
      needsReconcile: false,
    }));
    const rendered = renderPanel(api, session.id);
    const socket = await socketFor(jobId);
    act(() => socket.message({ cursor: second.cursor, event: second, type: 'event' }));
    act(() => socket.serverClose());

    await waitFor(() => expect(timeline).toHaveBeenCalledWith(
      projectId,
      jobId,
      expect.objectContaining({ cursor: second.cursor }),
    ), { timeout: 2_500 });
    const reconnected = await socketFor(jobId, 1);
    act(() => {
      reconnected.message({ cursor: third.cursor, event: third, type: 'event' });
      reconnected.message({ cursor: fourth.cursor, event: fourth, type: 'event' });
    });

    function expectConversation() {
      const nodes = ['Первый вопрос', 'Первый ответ', 'Второй вопрос', 'Второй ответ'].map((text) => {
        const matches = screen.getAllByText(text);
        expect(matches).toHaveLength(1);
        return matches[0]!;
      });
      expect(screen.getAllByRole('note', { name: 'Вопрос автора' }).map((node) => node.textContent))
        .toEqual(['Первый вопрос', 'Второй вопрос']);
      for (let index = 1; index < nodes.length; index += 1) {
        expect(nodes[index - 1]!.compareDocumentPosition(nodes[index]!) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
      }
    }

    expectConversation();
    rendered.view.unmount();
    FakeWebSocket.reset();
    timeline.mockResolvedValue({ events: [first, second, third, fourth], eof: true, nextCursor: null, needsReconcile: false });
    const getJob = api.getJob.bind(api);
    vi.spyOn(api, 'getJob').mockImplementation(async (id) => ({ ...await getJob(id), status: 'succeeded', canCancel: false }));
    const reopened = renderPanel(api, session.id);

    expect(reopened.queryClient).not.toBe(rendered.queryClient);
    await screen.findByText('Второй ответ');
    expectConversation();
  });

  it('commits one late assistant answer when terminal arrives first and keeps it after reload', async () => {
    const api = createMockApiClient();
    const { jobId, session } = await sessionWithTurn(api);
    const terminal = timelineEvent(jobId, 2, 'turn_outcome', { outcome: 'completed' });
    const answer = timelineEvent(jobId, 3, 'assistant', { text: 'Сохранённый ответ' });
    let persisted: AgentTimelineEvent[] = [];
    vi.spyOn(api, 'getAgentTimeline').mockImplementation(async () => ({
      events: persisted,
      eof: true,
      nextCursor: null,
      needsReconcile: false,
    }));
    const rendered = renderPanel(api, session.id);
    const socket = await socketFor(jobId);
    act(() => socket.message({ cursor: terminal.cursor, event: terminal, type: 'event' }));
    act(() => socket.message({ cursor: answer.cursor, event: answer, type: 'event' }));
    persisted = [terminal, answer];

    expect(await screen.findAllByText('Сохранённый ответ')).toHaveLength(1);
    rendered.view.unmount();
    FakeWebSocket.reset();
    renderPanel(api, session.id);

    expect(await screen.findAllByText('Сохранённый ответ')).toHaveLength(1);
  });

  it('removes mutation controls on authority loss without discarding the transcript', async () => {
    const api = createMockApiClient();
    const { jobId, session } = await sessionWithTurn(api);
    vi.spyOn(api, 'getAgentTimeline').mockResolvedValue({
      events: [timelineEvent(jobId, 1, 'assistant', { text: 'Ответ остаётся доступен' })],
      eof: true,
      nextCursor: null,
      needsReconcile: false,
    });
    const rendered = renderPanel(api, session.id);
    expect(await screen.findByText('Ответ остаётся доступен')).toBeTruthy();

    rendered.view.rerender(
      <QueryClientProvider client={rendered.queryClient}><ToastsProvider>
        <ResearchChatPanel
          api={api}
          canCreate={false}
          onOpenSource={vi.fn()}
          onSelectChatId={vi.fn()}
          projectId={projectId}
          selectedChatId={session.id}
          sourceView="published"
        />
      </ToastsProvider></QueryClientProvider>,
    );

    expect(screen.getByText('Ответ остаётся доступен')).toBeTruthy();
    expect(screen.queryByRole('textbox', { name: 'Вопрос' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Новый чат' })).toBeNull();
    expect(screen.queryByRole('button', { name: /Действия чата/u })).toBeNull();
  });

  it('treats a foreign-project session response as not found', async () => {
    const api = createMockApiClient();
    const session = await api.createChatSession(projectId, {
      scope: 'project', sourceView: 'published', title: 'Чужая область',
    }, { idempotencyKey: 'idem-foreign-scope-chat' });
    vi.spyOn(api, 'getChatSession').mockResolvedValue({ ...session, projectId: 'other-project' });
    renderPanel(api, session.id);

    expect(await screen.findByText('Чат не найден.')).toBeTruthy();
    expect(screen.getByRole('button', { name: session.title }).getAttribute('aria-current')).toBeNull();
    expect(screen.getByRole('button', { name: 'К списку чатов' })).toBeTruthy();
    expect(screen.queryByRole('textbox', { name: 'Вопрос' })).toBeNull();
  });

  it.each(['retry', 'switch'] as const)('keeps a pending submission failure across the old Job outcome until %s clears it', async (resolution) => {
    const api = createMockApiClient();
    const { jobId, session } = await sessionWithTurn(api);
    const other = await api.createChatSession(projectId, {
      scope: 'project', sourceView: 'published', title: 'Следующий чат',
    }, { idempotencyKey: 'idem-after-pending-failure' });
    const getJob = api.getJob.bind(api);
    vi.spyOn(api, 'getJob').mockImplementation(async (id) => ({ ...await getJob(id),
      status: id === jobId ? 'succeeded' : 'running', canCancel: id !== jobId,
    }));
    vi.spyOn(api, 'getAgentTimeline').mockResolvedValue({ events: [], eof: true, needsReconcile: false });
    const createTurn = vi.spyOn(api, 'createChatTurn').mockRejectedValueOnce(new TypeError('response lost'));
    const { queryClient, view } = renderPanel(api, session.id);
    const socket = await socketFor(jobId);
    const composer = screen.getByRole('textbox', { name: 'Вопрос' });
    await waitFor(() => expect(composer).toHaveProperty('disabled', false));
    fireEvent.change(composer, { target: { value: 'Новая задача' } });
    fireEvent.click(screen.getByRole('button', { name: 'Спросить' }));
    await screen.findByText('Вопрос не отправлен');

    const terminal = timelineEvent(jobId, 1, 'turn_outcome', { outcome: 'completed' });
    act(() => socket.message({ cursor: terminal.cursor, event: terminal, type: 'event' }));
    expect(screen.getByText('Вопрос не отправлен')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Спросить снова' })).toBeTruthy();
    expect(composer).toHaveProperty('value', 'Новая задача');

    if (resolution === 'retry') {
      fireEvent.click(screen.getByRole('button', { name: 'Спросить снова' }));
      await screen.findByRole('button', { name: 'Остановить' });
      expect(createTurn).toHaveBeenCalledTimes(2);
      expect(screen.getByRole('region', { name: 'Готовим ответ' })).toBeTruthy();
    } else {
      view.rerender(<QueryClientProvider client={queryClient}><ToastsProvider>
        <ResearchChatPanel api={api} canCreate onOpenSource={vi.fn()} onSelectChatId={vi.fn()}
          projectId={projectId} selectedChatId={other.id} sourceView="published" />
      </ToastsProvider></QueryClientProvider>);
      await waitFor(() => expect(screen.getByRole('textbox', { name: 'Вопрос' })).toHaveProperty('disabled', false));
    }
    expect(screen.queryByText('Вопрос не отправлен')).toBeNull();
    expect(screen.getByRole('textbox', { name: resolution === 'retry' ? 'Уточнение' : 'Вопрос' })).toHaveProperty('value', '');
  });

  it('describes saga tools by their arguments and never renders raw result content', async () => {
    const api = createMockApiClient();
    const { jobId, session } = await sessionWithTurn(api);
    renderPanel(api, session.id);
    const socket = await socketFor(jobId);
    const bare = timelineEvent(jobId, 1, 'tool_result', {
      tool: { name: 'search', namespace: 'saga', status: 'completed', success: true },
    });
    act(() => socket.message({ cursor: bare.cursor, event: bare, type: 'event' }));
    expect((await screen.findAllByText('Искал в книгах')).length).toBeGreaterThan(0);
    const described = {
      ...bare,
      cursor: 'tl_2',
      id: 'described-tool',
      providerItemId: 'described-tool',
      tool: { ...bare.tool, arguments: { queries: ['Шорох'] }, summary: '«Шорох» (any): 56 абзацев в 23 главах' },
    };
    act(() => socket.message({ cursor: described.cursor, event: described, type: 'event' }));
    expect((await screen.findByText('Искал «Шорох»')).closest('li')!.textContent).toBe('Искал «Шорох» — 56 мест в 23 главах');
    const raw = {
      ...bare,
      cursor: 'tl_3',
      id: 'raw-tool',
      tool: { ...bare.tool, arguments: '{"query":"private manuscript","sourceRef":"s7"}', content: [{ type: 'text', text: 'private result bytes' }] },
    };
    act(() => socket.message({ cursor: raw.cursor, event: raw, type: 'event' }));

    expect(document.body.textContent).not.toContain('saga.search');
    expect(document.body.textContent).not.toMatch(/private manuscript|private result bytes|s7/u);
  });
});
