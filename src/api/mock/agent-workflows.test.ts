import { describe, expect, it } from 'vitest';
import { createMockApiClient } from './repository';
import {
  agentWorkflowOperationIds,
  agentWorkflowScenario,
  isProtectedOperation,
} from './scenarios';
import { runAgentWorkflowScenario } from '../../test/agent-workflow-scenario';
import { createIdempotencyKey } from '../../shared/api';

const fixture = agentWorkflowScenario;

describe('Phase 11 mock workflows', () => {
  it('serves the exact Job timeline in replayable pages', async () => {
    const api = createMockApiClient();
    const first = await api.getAgentTimeline(fixture.projectId, 'canon-text-index-job-1');
    expect(first.events).toHaveLength(2);
    expect((first as unknown as { eof: boolean }).eof).toBe(false);

    const second = await api.getAgentTimeline(fixture.projectId, 'canon-text-index-job-1', {
      cursor: String((first as unknown as { nextCursor: string }).nextCursor),
    });
    expect(second.events[0]).toMatchObject({ jobId: 'canon-text-index-job-1' });
    expect((second as unknown as { eof: boolean }).eof).toBe(true);
    await expect(api.getAgentTimeline(fixture.projectId, 'canon-text-index-job-1', { cursor: 'bad-cursor' }))
      .rejects.toMatchObject({ status: 400, code: 'invalid_cursor' });
  });

  it.each(['draft', 'published'] as const)('cites a %s chapter key that resolves in the chat', async (sourceView) => {
    const api = createMockApiClient();
    const chat = await api.createChatSession(fixture.projectId, { ...fixture.chat.session, sourceView }, {
      idempotencyKey: createIdempotencyKey('createChatSession'),
    });
    const turn = await api.createChatTurn(chat.id, fixture.chat.turn, {
      idempotencyKey: createIdempotencyKey('createChatTurn'),
    });
    const first = await api.getAgentTimeline(fixture.projectId, turn.job.id);
    const second = await api.getAgentTimeline(fixture.projectId, turn.job.id, { cursor: String(first.nextCursor) });
    const assistant = [...first.events, ...second.events].find((event) => event.kind === 'assistant');

    expect(assistant?.text).toContain('(source:b2c12/3)');
    await expect(api.resolveChatChapterKey(chat.id, 'b2c12')).resolves.toMatchObject({
      chapterKey: 'b2c12', bookId: fixture.publishedSource.bookId, chapterId: fixture.publishedSource.chapterId,
    });
    await expect(api.resolveChatChapterKey(chat.id, '999')).rejects.toMatchObject({ status: 404 });
  });

  it.each(['draft', 'published'] as const)('removes the %s Chat timeline with its deleted book', async (sourceView) => {
    const api = createMockApiClient();
    const chat = await api.createChatSession(fixture.projectId, { scope: 'project', sourceView, title: 'Research' }, {
      idempotencyKey: createIdempotencyKey('createChatSession'),
    });
    const turn = await api.createChatTurn(chat.id, fixture.chat.turn, { idempotencyKey: createIdempotencyKey('createChatTurn') });
    await api.deleteBook(fixture.publishedSource.bookId);
    await expect(api.getJob(turn.job.id)).rejects.toMatchObject({ status: 404 });
    await expect(api.getAgentTimeline(fixture.projectId, turn.job.id)).rejects.toMatchObject({ status: 404 });
  });

  it('keeps a saved assignment until the exact existing Start action commits', async () => {
    const api = createMockApiClient();
    const body = 'Проверь первую главу';
    const chat = await api.createChatSession(fixture.projectId, {
      ...fixture.chat.session,
      savedAssignment: `  ${body}  `,
    }, { idempotencyKey: createIdempotencyKey('createChatSession') });

    expect(chat.savedAssignment).toEqual({ body, availableActions: ['start'] });
    await expect(api.createChatTurn(chat.id, { body: 'Другая задача' }, {
      idempotencyKey: createIdempotencyKey('createChatTurn'),
    })).rejects.toMatchObject({ status: 409 });
    await expect(api.getChatSession(chat.id)).resolves.toMatchObject({ savedAssignment: { body } });

    const started = await api.createChatTurn(chat.id, { body }, {
      idempotencyKey: createIdempotencyKey('createChatTurn'),
    });
    await expect(api.getChatSession(chat.id)).resolves.toMatchObject({
      latestTurnJobId: started.job.id,
      savedAssignment: null,
    });
  });

  it('проводит общий сценарий Chat с удалением и восстановлением книги', async () => {
    const api = createMockApiClient();
    api.setScenario({ preset: 'normal', actorRole: fixture.actorRole });

    const result = await runAgentWorkflowScenario(api, 'memory');

    expect(result).toMatchObject(fixture.expected);
    expect(result.deletion.replayedChatId).toBe(result.deletion.oldChatId);
    expect(result.deletion.restoredChatId).toBe(result.deletion.oldChatId);
    expect(result.deletion.jobsAfterReplay).toBe(result.deletion.jobsBeforeReplay);
    expect(result.deletion.freshChatTurnId).not.toBe(result.deletion.oldChatTurnId);
    expect(result.deletion.freshChatJobId).not.toBe(result.deletion.oldChatJobId);
  });

  it('повторяет актуальный terminal Job без новой строки', async () => {
    const api = createMockApiClient();
    const options = { idempotencyKey: 'idem-direct-chat-terminal-replay' };
    const chat = await api.createChatSession(fixture.projectId, fixture.chat.session, { idempotencyKey: 'idem-direct-replay-chat' });
    const started = await api.createChatTurn(chat.id, fixture.chat.turn, options);
    const terminal = await api.getJob(started.job.id);
    const jobsBeforeReplay = (await api.listProjectJobs(fixture.projectId)).data.length;

    const replayed = await api.createChatTurn(chat.id, fixture.chat.turn, options);

    expect(replayed.jobId).toBe(started.jobId);
    expect(replayed.job).toEqual(terminal);
    expect((await api.listProjectJobs(fixture.projectId)).data).toHaveLength(jobsBeforeReplay);
  });

  it('защищает все Agent operationIds до поиска ресурса', async () => {
    const api = createMockApiClient();
    await api.logout();
    const key = { idempotencyKey: 'idem-direct-agent-auth' };
    const calls: Record<(typeof agentWorkflowOperationIds)[number], () => Promise<unknown>> = {
      listChatSessions: () => api.listChatSessions(fixture.projectId, {}),
      createChatSession: () => api.createChatSession(fixture.projectId, fixture.chat.session, key),
      getChatSession: () => api.getChatSession('missing-chat'),
      resolveChatChapterKey: () => api.resolveChatChapterKey('missing-chat', '12'),
      deleteChatSession: () => api.deleteChatSession('missing-chat'),
      createChatTurn: () => api.createChatTurn('missing-chat', fixture.chat.turn, key),
      createChatTurnWithChapter: () => api.createChatTurnWithChapter('missing-chat', { body: 'Ложится ли на канон?', text: 'Глава.' }, key),
      getChatAttachment: () => api.getChatAttachment('missing-chat', 1),
      steerChatTurn: () => api.steerChatTurn('missing-chat', 'missing-turn', fixture.chat.turn, key),
    };

    expect(agentWorkflowOperationIds.every(isProtectedOperation)).toBe(true);
    for (const operationId of agentWorkflowOperationIds) {
      await expect(calls[operationId]()).rejects.toMatchObject({ operationId, status: 401 });
    }
  });

  it('не восстанавливает workflow после удаления chapter или project', async () => {
    const api = createMockApiClient();
    const unaffectedChat = await api.createChatSession(
      fixture.projectId,
      fixture.chat.session,
      { idempotencyKey: 'idem-direct-unaffected-chat' },
    );
    const unaffectedTurn = await api.createChatTurn(
      unaffectedChat.id,
      fixture.chat.turn,
      { idempotencyKey: 'idem-direct-unaffected-turn' },
    );
    if (unaffectedTurn.job.subject.type !== 'research_chat_turn') throw new Error('Unexpected Chat result.');

    await api.deleteChapter(fixture.draftSource.chapterId);
    await api.restoreChapter(fixture.draftSource.chapterId);
    await expect(api.getChatSession(unaffectedChat.id)).resolves.toMatchObject({ id: unaffectedChat.id });
    await expect(api.getJob(unaffectedTurn.job.id)).resolves.toMatchObject({
      id: unaffectedTurn.job.id,
      subject: { type: 'research_chat_turn', turnId: unaffectedTurn.job.subject.turnId },
    });

    api.reset();
    const chapterChat = await api.createChatSession(
      fixture.projectId,
      fixture.chat.session,
      { idempotencyKey: 'idem-direct-chapter-chat' },
    );
    const chapterTurn = await api.createChatTurn(
      chapterChat.id,
      fixture.chat.turn,
      { idempotencyKey: 'idem-direct-chapter-turn' },
    );
    if (chapterTurn.job.subject.type !== 'research_chat_turn') throw new Error('Unexpected Chat result.');
    await api.deleteChapter(fixture.publishedSource.chapterId);
    await api.restoreChapter(fixture.publishedSource.chapterId);
    await expect(api.getChatSession(chapterChat.id)).resolves.toMatchObject({ id: chapterChat.id });
    await expect(api.getJob(chapterTurn.job.id)).rejects.toMatchObject({ status: 404 });
    await expect(api.createChatSession(
      fixture.projectId,
      fixture.chat.session,
      { idempotencyKey: 'idem-direct-chapter-chat' },
    )).resolves.toMatchObject({ id: chapterChat.id });
    await expect(api.createChatTurn(
      chapterChat.id,
      fixture.chat.turn,
      { idempotencyKey: 'idem-direct-chapter-turn' },
    )).rejects.toMatchObject({ status: 409, code: 'conflict' });

    api.reset();
    const chat = await api.createChatSession(fixture.projectId, fixture.chat.session, { idempotencyKey: 'idem-direct-project-chat' });
    const turn = await api.createChatTurn(chat.id, fixture.chat.turn, { idempotencyKey: 'idem-direct-project-turn' });
    if (turn.job.subject.type !== 'research_chat_turn') throw new Error('Unexpected Chat result.');
    await api.deleteProject(fixture.projectId);
    await api.restoreProject(fixture.projectId);
    await expect(api.getChatSession(chat.id)).resolves.toMatchObject({ id: chat.id });
    await expect(api.getJob(turn.job.id)).rejects.toMatchObject({ status: 404 });
    await expect(api.createChatSession(
      fixture.projectId,
      fixture.chat.session,
      { idempotencyKey: 'idem-direct-project-chat' },
    )).resolves.toMatchObject({ id: chat.id });
    await expect(api.createChatTurn(
      chat.id,
      fixture.chat.turn,
      { idempotencyKey: 'idem-direct-project-turn' },
    )).rejects.toMatchObject({ status: 409, code: 'conflict' });
    await expect(api.getBook(fixture.publishedSource.bookId)).resolves.toMatchObject({ id: fixture.publishedSource.bookId });
  });
});
