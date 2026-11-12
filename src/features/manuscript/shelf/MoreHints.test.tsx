import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../../api/mock';
import { projectId } from '../../../api/mock/repository/fixtures';
import type { ChatSession } from '../../../entities/chat/api';
import { parseRoute } from '../../../shared/navigation/app-route';
import { FakeWebSocket } from '../../../test/FakeWebSocket';
import { ToastsProvider } from '../../../ui/feedback/Toast';
import { TalkActionProvider } from '../../../ui/navigation/TopBarStrip';
import { ManuscriptShelfPage } from './ManuscriptShelfPage';
import { forgetHiddenHints } from './more-hints';

// specs/2026-11-10-ux3-first-screen.md FS3.3, FS3.4: «Что ещё можно» on the shelf after the first answer, closed for good.

type ShelfApi = ReturnType<typeof createMockApiClient>;

beforeEach(() => {
  vi.stubGlobal('WebSocket', FakeWebSocket);
  window.localStorage.clear();
  forgetHiddenHints();
});
afterEach(() => {
  FakeWebSocket.reset();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

function chats(api: ShelfApi, sessions: Partial<ChatSession>[]) {
  vi.spyOn(api, 'listChatSessions').mockResolvedValue({ data: sessions, meta: { hasMore: false, nextCursor: null } } as never);
}

const answered: Partial<ChatSession> = { id: 'chat-1', latestTurnJobId: 'job-1', latestTurnStatus: 'succeeded' };

function renderShelf(configure: (api: ShelfApi) => void, role: 'editor' | 'viewer' = 'editor', talk: (() => void) | null = vi.fn()) {
  const api = createMockApiClient();
  api.setScenario({ actorRole: role, preset: 'normal' });
  vi.spyOn(api, 'getAgentTimeline').mockResolvedValue({ events: [], eof: true, nextCursor: null } as never);
  configure(api);
  const navigate = vi.fn();
  const useTalk = () => talk;
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <ToastsProvider>
        <TalkActionProvider value={useTalk}>
          <ManuscriptShelfPage api={api} navigate={navigate} route={parseRoute(`http://localhost/manuscript/books?projectId=${projectId}`)} />
        </TalkActionProvider>
      </ToastsProvider>
    </QueryClientProvider>,
  );
  return { navigate, talk };
}

describe('«Что ещё можно» (FS3.3)', () => {
  it('stands in place of the steps once the saga has answered, each line a way into a section', async () => {
    const { navigate, talk } = renderShelf((api) => chats(api, [answered]));
    const hints = await screen.findByRole('region', { name: 'Что ещё можно' });
    expect(screen.queryByRole('region', { name: 'До первого ответа' })).toBeNull();
    expect(within(hints).getAllByRole('listitem').map((row) => row.textContent)).toEqual([
      'Карта — кто с кем связан',
      'Рисунки — портрет героя по книге',
      'Письмо — следующая глава с помощником',
      'Вычитка — опечатки и неловкие места в вашей главе',
      'Поговорить — то же голосом',
    ]);
    fireEvent.click(within(hints).getByRole('button', { name: 'Карта — кто с кем связан' }));
    expect(navigate).toHaveBeenLastCalledWith('/saga', { projectId });
    fireEvent.click(within(hints).getByRole('button', { name: 'Вычитка — опечатки и неловкие места в вашей главе' }));
    expect(navigate).toHaveBeenLastCalledWith('/writing', { projectId });
    fireEvent.click(within(hints).getByRole('button', { name: 'Поговорить — то же голосом' }));
    expect(talk).toHaveBeenCalled();
  });

  it('closes for good for this author in this browser: only the mark is kept', async () => {
    renderShelf((api) => chats(api, [answered]));
    fireEvent.click(within(await screen.findByRole('region', { name: 'Что ещё можно' })).getByRole('button', { name: 'Скрыть подсказки' }));
    expect(screen.queryByRole('region', { name: 'Что ещё можно' })).toBeNull();
    expect(JSON.parse(window.localStorage.getItem('canonkeeper.more-hints.hidden') ?? '[]')).toHaveLength(1);

    // The page opened again — the row stays closed.
    cleanup();
    forgetHiddenHints();
    renderShelf((api) => chats(api, [answered]));
    await screen.findByRole('article', { name: 'Книга III. Северный суд' });
    await waitFor(() => expect(screen.queryByRole('region', { name: 'Что ещё можно' })).toBeNull());
  });

  it('waits for an answer, and gives a reader of the saga nothing to do', async () => {
    renderShelf((api) => chats(api, [{ id: 'chat-1', latestTurnJobId: 'job-1', latestTurnStatus: 'running' }]));
    await screen.findByRole('article', { name: 'Книга III. Северный суд' });
    expect(screen.queryByRole('region', { name: 'Что ещё можно' })).toBeNull();
    cleanup();
    renderShelf((api) => chats(api, [answered]), 'viewer');
    await screen.findByRole('article', { name: 'Книга III. Северный суд' });
    expect(screen.queryByRole('region', { name: 'Что ещё можно' })).toBeNull();
  });

  it('has no «Поговорить» without the voice', async () => {
    renderShelf((api) => chats(api, [answered]), 'editor', null);
    const hints = await screen.findByRole('region', { name: 'Что ещё можно' });
    expect(within(hints).queryByRole('button', { name: 'Поговорить — то же голосом' })).toBeNull();
  });
});

describe('one «Задать первый вопрос» on the screen (FS3.4)', () => {
  // specs/2026-11-10-ux3-saga-state.md SS2.5: while the saga is read, the line of the reading stands in the steps and its
  // button is the one question on the screen.
  it('is the button of the line of the reading in the steps; nothing repeats it', async () => {
    renderShelf((api) => chats(api, []));
    const steps = await screen.findByRole('region', { name: 'До первого ответа' });
    await within(steps).findByRole('region', { name: 'Чтение саги' });
    expect(screen.getAllByRole('button', { name: /Задать (первый )?вопрос/u })).toHaveLength(1);
  });
});
