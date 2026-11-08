import { agentWorkflowScenario } from '../api/mock/scenarios';
import { ApiStatusError, type CanonKeeperApiClient } from '../shared/api';

async function expectedApiError(run: () => Promise<unknown>) {
  try {
    await run();
  } catch (error) {
    if (error instanceof ApiStatusError) return { status: error.status, code: error.problem.code };
    throw error;
  }
  throw new Error('Expected API error.');
}

/** Один продуктовый сценарий выполняется без собственных данных HTTP-адаптеров. */
export async function runAgentWorkflowScenario(api: CanonKeeperApiClient, keyPrefix: string) {
  const key = (suffix: string) => `${keyPrefix}-${suffix}`;
  const fixture = agentWorkflowScenario;
  const chat = await api.createChatSession(fixture.projectId, fixture.chat.session, { idempotencyKey: key('chat') });
  const chatStart = await api.createChatTurn(chat.id, fixture.chat.turn, { idempotencyKey: key('turn') });
  if (chatStart.job.subject.type !== 'research_chat_turn') throw new Error('Unexpected Chat Job subject.');
  const chatTurnId = chatStart.job.subject.turnId;
  const chatJob = await api.getJob(chatStart.job.id);
  if (chatJob.subject.type !== 'research_chat_turn' || chatJob.subject.turnId !== chatTurnId) {
    throw new Error('Chat Job has an unexpected subject.');
  }

  await api.deleteBook(fixture.publishedSource.bookId);
  const deletedSource = {
    chat: await expectedApiError(() => api.createChatTurn(chat.id, fixture.chat.turn, { idempotencyKey: key('turn') })),
  };
  const restoredBook = await api.restoreBook(fixture.publishedSource.bookId);
  const replayedChat = await api.createChatSession(fixture.projectId, fixture.chat.session, { idempotencyKey: key('chat') });
  const restoredChat = await api.getChatSession(chat.id);
  const jobsBeforeReplay = (await api.listProjectJobs(fixture.projectId)).data.length;
  const replayConflicts = {
    chat: await expectedApiError(() => api.createChatTurn(chat.id, fixture.chat.turn, { idempotencyKey: key('turn') })),
  };
  const jobsAfterReplay = (await api.listProjectJobs(fixture.projectId)).data.length;
  const freshChatStart = await api.createChatTurn(chat.id, fixture.chat.turn, { idempotencyKey: key('fresh-turn') });
  if (freshChatStart.job.subject.type !== 'research_chat_turn') throw new Error('Unexpected fresh Chat Job subject.');
  const freshChatJob = await api.getJob(freshChatStart.job.id);
  const scrubbed = {
    chatJob: await expectedApiError(() => api.getJob(chatStart.job.id)),
  };

  return {
    jobs: {
      chat: { start: chatStart.job.status, terminal: chatJob.status },
    },
    deletion: {
      restoredBookId: restoredBook.id,
      deletedSource,
      replayConflicts,
      oldChatId: chat.id,
      replayedChatId: replayedChat.id,
      restoredChatId: restoredChat.id,
      jobsBeforeReplay,
      jobsAfterReplay,
      oldChatTurnId: chatTurnId,
      freshChatTurnId: freshChatStart.job.subject.turnId,
      oldChatJobId: chatStart.job.id,
      freshChatJobId: freshChatJob.id,
      scrubbed,
    },
  };
}
