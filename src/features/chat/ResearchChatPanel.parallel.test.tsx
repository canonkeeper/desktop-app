import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { useState } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../api/mock';
import { projectId } from '../../api/mock/repository/fixtures';
import { createIdempotencyKey } from '../../shared/api';
import { FakeWebSocket } from '../../test/FakeWebSocket';
import { forgetAllTurns } from './answer-ready';
import { ResearchChatPanel } from './ResearchChatPanel';

beforeEach(() => {
  vi.stubGlobal('innerWidth', 1440);
  vi.stubGlobal('innerHeight', 900);
  FakeWebSocket.reset();
  vi.stubGlobal('WebSocket', FakeWebSocket);
});

afterEach(() => {
  forgetAllTurns();
  vi.unstubAllGlobals();
});

async function chat(api: ReturnType<typeof createMockApiClient>, title: string) {
  return api.createChatSession(projectId, {
    scope: 'project', sourceView: 'published', title,
  }, { idempotencyKey: createIdempotencyKey('createChatSession') });
}

function renderPanel(api: ReturnType<typeof createMockApiClient>, selectedChatId: string) {
  const client = new QueryClient({ defaultOptions: { mutations: { retry: false }, queries: { retry: false } } });
  function SelectedChat() {
    const [chatId, setChatId] = useState(selectedChatId);
    return <QueryClientProvider client={client}>
      <ResearchChatPanel
        api={api}
        canCreate
        onOpenSource={vi.fn()}
        onSelectChatId={setChatId}
        projectId={projectId}
        selectedChatId={chatId}
        sourceView="published"
      />
    </QueryClientProvider>;
  }
  render(<SelectedChat />);
}

async function ask(question: string) {
  fireEvent.change(await screen.findByRole('textbox', { name: 'Вопрос' }), { target: { value: question } });
  const start = screen.getByRole('button', { name: 'Спросить' });
  await waitFor(() => expect(start).toHaveProperty('disabled', false));
  fireEvent.click(start);
}

describe('Research Chat parallel chats', () => {
  it('keeps «Новый чат» open while a turn works and runs a second chat beside it', async () => {
    const api = createMockApiClient();
    const first = await chat(api, 'Кто такой Тарас Гнедой?');
    renderPanel(api, first.id);

    await ask('Кто такой Тарас Гнедой?');
    expect(await screen.findByRole('button', { name: 'Остановить' })).toBeTruthy();
    expect(screen.getByRole('textbox', { name: 'Уточнение' })).toHaveProperty('disabled', false);
    const newChat = screen.getByRole('button', { name: 'Новый чат' });
    expect(newChat).toHaveProperty('disabled', false);
    expect(await screen.findByRole('button', { name: 'Кто такой Тарас Гнедой?, идёт работа' })).toBeTruthy();

    fireEvent.click(newChat);
    await ask('Где впервые появляется Ворон?');

    expect(await screen.findByRole('button', { name: 'Где впервые появляется Ворон?, идёт работа' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Кто такой Тарас Гнедой?, идёт работа' })).toBeTruthy();
  });

  it('marks only the chats whose turn works', async () => {
    const api = createMockApiClient();
    const idle = await chat(api, 'Тихий чат');
    const working = await chat(api, 'Работающий чат');
    await api.createChatTurn(working.id, { body: 'Проверь первую главу' }, { idempotencyKey: createIdempotencyKey('createChatTurn') });
    renderPanel(api, idle.id);
    expect(await screen.findByRole('button', { name: 'Работающий чат, идёт работа' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Тихий чат' })).toBeTruthy();
  });

  it('shows on «Чаты» how many chats work while the list is hidden', async () => {
    vi.stubGlobal('innerWidth', 1024);
    const api = createMockApiClient();
    const idle = await chat(api, 'Тихий чат');
    for (const title of ['Первый работающий', 'Второй работающий']) {
      const working = await chat(api, title);
      await api.createChatTurn(working.id, { body: 'Проверь первую главу' }, { idempotencyKey: createIdempotencyKey('createChatTurn') });
    }
    renderPanel(api, idle.id);

    expect(await screen.findByRole('button', { name: 'Чаты, работают: 2' })).toBeTruthy();
  });
});
