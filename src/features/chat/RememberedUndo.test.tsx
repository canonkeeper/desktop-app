import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { createMockApiClient } from '../../api/mock';
import type { FeedAction, FeedTurn } from './feed/feed-model';
import { describeSagaAction } from './feed/saga-actions';
import { ChatMemoryProvider, RememberedUndo } from './RememberedUndo';

afterEach(cleanup);

const projectId = 'project-white-port';
const jobId = 'job-remember-1';

function remembered(text: string): FeedAction {
  return {
    key: 'action-1', status: 'done',
    description: describeSagaAction({ namespace: 'saga', name: 'remember', arguments: { kind: 'correction', text }, summary: 'Запись сохранена в памяти автора: поправка.' }, 'done'),
  };
}

const turn = { key: 'turn-1', question: { id: 'q', jobId, cursor: 1, kind: 'message', logicalSection: 1, role: 'user', text: 'Ей двадцать' } } as unknown as FeedTurn;

function renderUndo(api: ReturnType<typeof createMockApiClient>, action: FeedAction, canEdit = true) {
  render(<QueryClientProvider client={new QueryClient()}>
    <ChatMemoryProvider value={{ api, projectId, canEdit }}><RememberedUndo action={action} turn={turn} /></ChatMemoryProvider>
  </QueryClientProvider>);
}

// specs/2026-11-02-writer-workshop.md, Decision log 03.11: what the chat remembered in a turn is undone from its line.
describe('RememberedUndo', () => {
  it('removes the record its turn remembered', async () => {
    const api = createMockApiClient();
    api.setScenario({ actorRole: 'editor', preset: 'normal' });
    await api.agentRemember(projectId, jobId, { kind: 'correction', text: 'Кире двадцать лет' });
    await api.agentRemember(projectId, 'job-other', { kind: 'correction', text: 'Кире двадцать лет' });
    renderUndo(api, remembered('Кире двадцать лет'));
    fireEvent.click(screen.getByRole('button', { name: 'Отменить' }));
    expect(await screen.findByText('Отменено: запись снята.')).toBeTruthy();
    expect((await api.listAuthorMemory(projectId)).data).toHaveLength(1);
    expect((await api.listAuthorMemory(projectId, { jobId })).data).toHaveLength(0);
  });

  it('says a record is gone and shows nothing to a reader', async () => {
    const api = createMockApiClient();
    renderUndo(api, remembered('Такой записи нет'));
    fireEvent.click(screen.getByRole('button', { name: 'Отменить' }));
    expect(await screen.findByText('Этой записи уже нет.')).toBeTruthy();
    cleanup();
    renderUndo(api, remembered('Кире двадцать лет'), false);
    expect(screen.queryByRole('button', { name: 'Отменить' })).toBeNull();
  });
});
