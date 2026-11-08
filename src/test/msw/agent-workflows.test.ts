import { describe, expect, it } from 'vitest';
import { agentWorkflowScenario } from '../../api/mock/scenarios';
import { runAgentWorkflowScenario } from '../agent-workflow-scenario';
import { createCanonKeeperApiClient } from '../../shared/api';
import { createApiHandlers, mswApiBaseUrl } from './handlers';
import { mswServer } from './server';

const editorHeaders = {
  cookie: 'ck_session=mock-session',
  mock_actor_role: 'editor',
  origin: 'http://localhost:3000',
  'x-csrf-token': 'ck_msw_csrf_token_000000',
};
const cancelFixtureKeys = {
  draftChat: 'msw-opaque-chat',
  draftTurn: 'msw-opaque-turn',
  visibleChat: 'msw-visible-chat',
  visibleTurn: 'msw-visible-turn',
};

function createMswWorkflowApi() {
  return createCanonKeeperApiClient({
    baseUrl: mswApiBaseUrl,
    fetch: async (request) => {
      const headers = new Headers(request.headers);
      for (const [name, value] of Object.entries(editorHeaders)) headers.set(name, value);
      return fetch(request, { headers });
    },
  });
}

describe('MSW Phase 11 workflows', () => {
  it('проводит тот же сценарий Chat и deletion scrub через общий repository', async () => {
    mswServer.use(...createApiHandlers());

    const result = await runAgentWorkflowScenario(createMswWorkflowApi(), 'msw');

    expect(result).toMatchObject(agentWorkflowScenario.expected);
    expect(result.deletion.replayedChatId).toBe(result.deletion.oldChatId);
    expect(result.deletion.restoredChatId).toBe(result.deletion.oldChatId);
    expect(result.deletion.jobsAfterReplay).toBe(result.deletion.jobsBeforeReplay);
    expect(result.deletion.freshChatTurnId).not.toBe(result.deletion.oldChatTurnId);
    expect(result.deletion.freshChatJobId).not.toBe(result.deletion.oldChatJobId);
  });

  it('отменяет собственный скрытый draft Chat Job без раскрытия Job', async () => {
    mswServer.use(...createApiHandlers());
    const api = createMswWorkflowApi();
    const draftChat = await api.createChatSession(
      agentWorkflowScenario.projectId,
      { ...agentWorkflowScenario.chat.session, sourceView: 'draft' },
      { idempotencyKey: cancelFixtureKeys.draftChat },
    );
    const draftTurn = await api.createChatTurn(
      draftChat.id,
      agentWorkflowScenario.chat.turn,
      { idempotencyKey: cancelFixtureKeys.draftTurn },
    );

    const opaqueResponse = await fetch(`${mswApiBaseUrl}/jobs/${draftTurn.job.id}/cancel`, {
      method: 'POST',
      headers: { ...editorHeaders, mock_actor_role: 'viewer' },
    });

    expect(opaqueResponse.status).toBe(204);
    expect(opaqueResponse.headers.get('content-type')).toBeNull();
    await expect(opaqueResponse.text()).resolves.toBe('');
    await expect(api.getJob(draftTurn.job.id)).resolves.toMatchObject({ status: 'canceled', canCancel: false });

    const visibleChat = await api.createChatSession(
      agentWorkflowScenario.projectId,
      agentWorkflowScenario.chat.session,
      { idempotencyKey: cancelFixtureKeys.visibleChat },
    );
    const visibleTurn = await api.createChatTurn(
      visibleChat.id,
      agentWorkflowScenario.chat.turn,
      { idempotencyKey: cancelFixtureKeys.visibleTurn },
    );
    const visibleResponse = await fetch(`${mswApiBaseUrl}/jobs/${visibleTurn.job.id}/cancel`, {
      method: 'POST',
      headers: editorHeaders,
    });

    expect(visibleResponse.status).toBe(200);
    await expect(visibleResponse.json()).resolves.toMatchObject({ id: visibleTurn.job.id });
  });
});
