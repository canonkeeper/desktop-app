import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import type { ReactNode } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../api/mock';
import { projectId } from '../../api/mock/repository/fixtures';
import type { AgentTimelineEvent } from '../../shared/api';
import { ToastsProvider } from '../../ui/feedback/Toast';
import { TalkActionProvider } from '../../ui/navigation/TopBarStrip';
import { SagaSectionsProvider, type SagaSection } from '../../ui/saga-sections';
import type { CanonicalAgentTimelineEvent } from './feed/types';
import { ResearchChatConversation } from './ResearchChatConversation';
import { WhatNext, type WhatNextRoutes } from './WhatNext';

// specs/2026-11-10-ux3-first-screen.md FS3.1, FS3.2: «Что дальше» under the first answer of the saga.

afterEach(() => vi.restoreAllMocks());

const client = () => new QueryClient({ defaultOptions: { queries: { retry: false } } });

function event(cursor: number, kind: AgentTimelineEvent['kind'], fields: Partial<AgentTimelineEvent> = {}) {
  return { cursor: `tl_${cursor}`, id: `event-${cursor}`, jobId: 'job-chat', kind, logicalSection: 0, role: 'main', ...fields } as CanonicalAgentTimelineEvent;
}

const firstTurn = [
  event(1, 'user', { providerTurnId: 'turn-1', text: 'Кто такая Мара?' }),
  event(2, 'assistant', { providerTurnId: 'turn-1', providerItemId: 'item-1', phase: 'final_answer', text: 'Мара — дочь смотрителя маяка.' }),
  event(3, 'turn_outcome', { providerTurnId: 'turn-1', outcome: 'completed' } as Partial<AgentTimelineEvent>),
];
const secondTurn = [
  event(4, 'user', { providerTurnId: 'turn-2', text: 'А её отец?' }),
  event(5, 'assistant', { providerTurnId: 'turn-2', providerItemId: 'item-2', phase: 'final_answer', text: 'Смотритель маяка.' }),
  event(6, 'turn_outcome', { providerTurnId: 'turn-2', outcome: 'completed' } as Partial<AgentTimelineEvent>),
];

function conversation(events: readonly CanonicalAgentTimelineEvent[], extra: Partial<Parameters<typeof ResearchChatConversation>[0]> = {}) {
  // The buttons under an answer say what they did in the one area of the toasts of the root (AA1.4).
  return (
    <ToastsProvider><ResearchChatConversation chatId="chat-1" connection="connected" events={events} firstAnswerNext={<p>Строка «Что дальше»</p>} jobStatus={null}
      loading={false} onOpenSource={vi.fn()} {...extra} /></ToastsProvider>
  );
}

describe('the place of «Что дальше» (FS3.1, FS3.2)', () => {
  it('stands under the first answer, outside its text', async () => {
    render(<QueryClientProvider client={client()}>{conversation(firstTurn)}</QueryClientProvider>);
    const line = await screen.findByText('Строка «Что дальше»');
    const answer = line.closest('[data-answer]');
    expect(answer).toBeTruthy();
    expect(answer!.querySelector('[data-assistant-content]')!.contains(line)).toBe(false);
  });

  it('is gone with the second question, and while a question goes', async () => {
    const view = render(<QueryClientProvider client={client()}>{conversation([...firstTurn, ...secondTurn])}</QueryClientProvider>);
    await screen.findByText('Смотритель маяка.');
    expect(screen.queryByText('Строка «Что дальше»')).toBeNull();
    view.rerender(<QueryClientProvider client={client()}>{conversation(firstTurn, { pendingQuestion: 'А её отец?' })}</QueryClientProvider>);
    expect(screen.queryByText('Строка «Что дальше»')).toBeNull();
    view.rerender(<QueryClientProvider client={client()}>{conversation(firstTurn, { jobStatus: 'running' })}</QueryClientProvider>);
    expect(screen.queryByText('Строка «Что дальше»')).toBeNull();
  });
});

function renderNext({ canEdit = true, sections, talk = vi.fn() }: { canEdit?: boolean; sections?: readonly SagaSection[]; talk?: (() => void) | null }) {
  const api = createMockApiClient();
  const routes: WhatNextRoutes = { openMap: vi.fn(), draw: vi.fn(), write: vi.fn() };
  const useTalk = () => talk;
  const wrap = (node: ReactNode) => sections ? <SagaSectionsProvider value={sections}>{node}</SagaSectionsProvider> : node;
  render(
    <QueryClientProvider client={client()}>
      <TalkActionProvider value={useTalk}>
        {wrap(<WhatNext api={api} canEdit={canEdit} projectId={projectId} routes={routes} sourceView="published" />)}
      </TalkActionProvider>
    </QueryClientProvider>,
  );
  return { api, routes, talk };
}

describe('«Что дальше» (FS3.1)', () => {
  it('offers the map, the portrait of the hero, a chapter and the voice to an editor', async () => {
    const { api, routes, talk } = renderNext({});
    const map = await api.getSagaMap(projectId, { sourceView: 'published' });
    const hero = map.nodes.find((node) => node.key === map.hero)!.name;
    const group = screen.getByRole('group', { name: 'Что дальше' });
    expect(within(group).getAllByRole('button').map((button) => button.textContent)).toEqual([
      'Посмотреть карту саги', 'Нарисовать героя', 'Написать главу', 'Поговорить',
    ]);
    fireEvent.click(within(group).getByRole('button', { name: 'Посмотреть карту саги' }));
    expect(routes.openMap).toHaveBeenCalled();
    // The hero of the map goes into «Что нарисовать?»; nothing is drawn by the press.
    await waitFor(() => {
      fireEvent.click(within(group).getByRole('button', { name: 'Нарисовать героя' }));
      expect(routes.draw).toHaveBeenLastCalledWith(`Портрет: ${hero}`);
    });
    fireEvent.click(within(group).getByRole('button', { name: 'Написать главу' }));
    expect(routes.write).toHaveBeenCalled();
    fireEvent.click(within(group).getByRole('button', { name: 'Поговорить' }));
    expect(talk).toHaveBeenCalled();
  });

  it('leaves the field empty for a saga without a hero yet', () => {
    const { api, routes } = renderNext({});
    vi.spyOn(api, 'getSagaMap').mockImplementation(() => new Promise<never>(() => undefined));
    fireEvent.click(screen.getByRole('button', { name: 'Нарисовать героя' }));
    expect(routes.draw).toHaveBeenCalledWith('');
  });

  it('gives a reader of the saga the map only', () => {
    renderNext({ canEdit: false });
    expect(screen.getAllByRole('button').map((button) => button.textContent)).toEqual(['Посмотреть карту саги']);
  });

  it('has no «Поговорить» without the voice, and no section the build has not', () => {
    renderNext({ talk: null, sections: ['manuscript', 'saga', 'memory', 'chat', 'notes', 'deleted'] });
    expect(screen.getAllByRole('button').map((button) => button.textContent)).toEqual(['Посмотреть карту саги']);
  });
});
