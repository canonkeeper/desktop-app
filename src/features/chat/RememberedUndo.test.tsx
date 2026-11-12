import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../api/mock';
import type { FeedAction, FeedTurn } from './feed/feed-model';
import { describeSagaAction } from './feed/saga-actions';
import { ChatMemoryProvider, RememberedUndo } from './RememberedUndo';

afterEach(() => {
  cleanup();
  openMemory.mockClear();
});

const projectId = 'project-white-port';
const jobId = 'job-remember-1';

function remembered(text: string): FeedAction {
  return {
    key: 'action-1', status: 'done',
    description: describeSagaAction({ namespace: 'saga', name: 'remember', arguments: { kind: 'correction', text }, summary: 'Запись сохранена в памяти автора: поправка.' }, 'done'),
  };
}

const turn = { key: 'turn-1', question: { id: 'q', jobId, cursor: 1, kind: 'message', logicalSection: 1, role: 'user', text: 'Ей двадцать' } } as unknown as FeedTurn;

const openMemory = vi.fn();

function renderUndo(api: ReturnType<typeof createMockApiClient>, action: FeedAction, canEdit = true, link = true) {
  render(<QueryClientProvider client={new QueryClient()}>
    <ChatMemoryProvider value={{ api, projectId, canEdit, ...(link ? { memoryLink: { href: `/memory?projectId=${projectId}`, onOpen: openMemory } } : {}) }}>
      <RememberedUndo action={action} turn={turn} />
    </ChatMemoryProvider>
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
    expect(await screen.findByText('Забыл эту запись.')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Отменить' })).toBeNull();
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

  // specs/2026-11-10-ux1-chat.md CH4.2: the line leads to the page of what the helper remembers.
  it('leads to «Что помнит помощник» of the saga, for a reader too', () => {
    const api = createMockApiClient();
    renderUndo(api, remembered('Кире двадцать лет'), false);
    const link = screen.getByRole('link', { name: 'Что помнит помощник' });
    expect(link.getAttribute('href')).toBe(`/memory?projectId=${projectId}`);
    fireEvent.click(link);
    expect(openMemory).toHaveBeenCalledTimes(1);
    cleanup();
    // The helper of «Письмо» has no page to lead to from its feed.
    renderUndo(api, remembered('Кире двадцать лет'), true, false);
    expect(screen.queryByRole('link', { name: 'Что помнит помощник' })).toBeNull();
    expect(screen.getByRole('button', { name: 'Отменить' })).toBeTruthy();
  });
});
