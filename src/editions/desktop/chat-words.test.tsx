// SPDX-License-Identifier: AGPL-3.0-only
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, render, screen, waitFor, within } from '@testing-library/react';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { Suspense } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../api/mock';
import { projectId } from '../../api/mock/repository/fixtures';
import { normalScenario } from '../../api/mock/scenarios';
import { InShell } from '../../app/shell/AppShell.test-helpers';
import { createAppRoute } from '../../shared/navigation/app-route';
import { createIdempotencyKey } from '../../shared/api';
import { FakeWebSocket } from '../../test/FakeWebSocket';
import { ToastsProvider } from '../../ui/feedback/Toast';
import { turnWords } from './chat-words';
import { edition } from '.';

const question = 'Где стоит застава у перевала?';

beforeEach(() => {
  vi.stubGlobal('innerWidth', 1440);
  vi.stubGlobal('innerHeight', 900);
  vi.stubGlobal('WebSocket', FakeWebSocket);
});
afterEach(() => {
  cleanup();
  FakeWebSocket.reset();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

/** «Чат» of the app with a chat whose last turn failed with the code: the Job fails, the timeline has the question. */
async function renderFailedTurn(code: string) {
  const api = createMockApiClient();
  const chat = await api.createChatSession(projectId, { scope: 'project', sourceView: 'published', title: 'Застава' },
    { idempotencyKey: createIdempotencyKey('createChatSession') });
  const turn = await api.createChatTurn(chat.id, { body: question }, { idempotencyKey: createIdempotencyKey('createChatTurn') });
  api.setScenario({ ...normalScenario, turnFailure: code, questionReturned: 'pending' });
  vi.spyOn(api, 'getAgentTimeline').mockResolvedValue({
    eof: true, needsReconcile: false, nextCursor: null,
    events: [{ cursor: 'tl_1', id: 'interrupted-question', jobId: turn.jobId, kind: 'user', logicalSection: 0, role: 'main', text: question }],
  } as unknown as Awaited<ReturnType<typeof api.getAgentTimeline>>);
  const route = createAppRoute('/chat', { projectId, chatId: chat.id });
  const user = { id: 'user-local' } as Parameters<NonNullable<typeof edition.pages.chat>['render']>[0]['user'];
  const navigate = vi.fn();
  // The pages of the app go under what its account wraps them in, as the root of the web puts them.
  const account = edition.useAccount({ api, navigate, route } as unknown as Parameters<typeof edition.useAccount>[0]);
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { mutations: { retry: false }, queries: { retry: false } } })}><ToastsProvider>
      <InShell api={api} route={route}>
        {account.wrapPages(<Suspense fallback={null}>{edition.pages.chat!.render({ api, navigate, route, user })}</Suspense>)}
      </InShell>
    </ToastsProvider></QueryClientProvider>,
  );
  return screen.findByRole('alert', {}, { timeout: 10_000 });
}

// specs/2026-11-15-desk-interrupted-turn.md IT1.4 (board 44·45): a question a restart of the app interrupted says so in the
// words of the app, with the block's own «Повторить вопрос»; a code the app does not name keeps the words of the core, and a
// turn Codex interrupted has the app's own words (specs/2026-11-15-desk-account-words-web.md, Data «срыв хода»). The line of the
// count is not held here: it goes by questionReturned with the slice of the words of the account
// (specs/2026-11-13-desk-account-ai.md AI2.9 — «не засчитан» only for «да»). The words of the site for the code are the block of the core —
// ResearchChatPanel.failure.test.tsx.
describe('«Чат» of the desktop app: the words of a failed turn', () => {
  it('says a question a restart interrupted in the words of the app', async () => {
    const block = await renderFailedTurn('model.session_ended');
    expect(block.getAttribute('aria-label')).toBe('Ответ прервался: CanonKeeper был перезапущен');
    expect(within(block).getByText('Ответ прервался: CanonKeeper был перезапущен')).toBeTruthy();
    expect(block.textContent).not.toMatch(/Спросите снова|Мы обновляли сервис|модел|ИИ/u);
    await waitFor(() => expect(within(block).getByRole('button', { name: 'Повторить вопрос' })).toBeTruthy());
  });

  // specs/2026-11-14-desk-journal.md JR1.6, Data «срыв вопроса»: a question whose request the gateway did not send — its
  // record did not lie in the journal — says so, with the one «Повторить вопрос» of the app.
  it('says a question the journal did not let go in the words of the app', async () => {
    const block = await renderFailedTurn('journal.unavailable');
    const words = 'Запрос помощнику не отправлен: не удалось записать его в журнал отправок. Попробуйте ещё раз';
    expect(block.getAttribute('aria-label')).toBe(words);
    expect(within(block).getByText(words)).toBeTruthy();
    expect(block.textContent).not.toMatch(/Помощник не смог ответить|напишите нам|модел|ИИ/u);
    await waitFor(() => expect(within(block).getByRole('button', { name: 'Повторить вопрос' })).toBeTruthy());
  });

  it('keeps the words of the core for a code the app does not name', async () => {
    const block = await renderFailedTurn('model.server_overloaded');
    expect(block.getAttribute('aria-label')).toBe('Помощник сейчас перегружен');
  });

  it('says a turn Codex interrupted in the words of the app: no update of the service, no restart', async () => {
    const block = await renderFailedTurn('model.turn_interrupted');
    expect(block.getAttribute('aria-label')).toBe('Ответ прервался. Спросите ещё раз');
    expect(block.textContent).not.toMatch(/CanonKeeper был перезапущен|обновляли сервис/u);
  });

  it('names only codes the contract of the app lists for a failed turn', () => {
    const contract = JSON.parse(readFileSync(join(process.cwd(), 'contracts/openapi.desktop.json'), 'utf8')) as {
      components: { schemas: { Job: { properties: { error: { description: string } } } } };
    };
    const listed = contract.components.schemas.Job.properties.error.description;
    // The codes of the helper of the account have their words too (specs/2026-11-15-desk-account-words-web.md AW4.3).
    for (const code of Object.keys(turnWords)) expect(listed).toContain(`${code} (`);
    expect(Object.keys(turnWords)).toContain('model.session_ended');
    // A request the journal could not record is a code of the app too (specs/2026-11-14-desk-journal.md JR1.6).
    expect(Object.keys(turnWords)).toContain('journal.unavailable');
  });
});
