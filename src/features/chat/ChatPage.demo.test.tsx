import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../api/mock';
import { projectId } from '../../api/mock/repository/fixtures';
import { createAppRoute } from '../../shared/navigation/app-route';
import { InShell } from '../../app/shell/AppShell.test-helpers';
import { ChatPage } from './ChatPage';
import { ChatSlotProvider, type ChatSlot } from './chat-slot';

beforeEach(() => { vi.stubGlobal('innerWidth', 1440); vi.stubGlobal('innerHeight', 900); });
afterEach(() => vi.unstubAllGlobals());

/** The questions of the demo saga as a build gives them to the chat. */
const slot: ChatSlot = {
  demoExamples: ({ onPick }) => (
    <section aria-label="Вопросы примера">
      <button onClick={() => onPick('Чем кончилась игра бабушки?')} type="button">Чем кончилась игра бабушки?</button>
    </section>
  ),
};

function renderChat(api: ReturnType<typeof createMockApiClient>, sagaId: string, value: ChatSlot | null) {
  const route = createAppRoute('/chat', { projectId: sagaId });
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { mutations: { retry: false }, queries: { retry: false } } })}>
      <ChatSlotProvider value={value}>
        <InShell api={api} route={route}><ChatPage api={api} navigate={vi.fn()} route={route} /></InShell>
      </ChatSlotProvider>
    </QueryClientProvider>,
  );
}

// specs/2026-11-09-demo-saga.md DS4.1, DS4.3.
describe('ChatPage and the demo saga', () => {
  it('begins an empty chat of the demo saga with the questions of the build and marks the saga', async () => {
    const api = createMockApiClient();
    const saga = await api.openDemoSaga();
    const turn = vi.spyOn(api, 'createChatTurn');
    renderChat(api, saga.id, slot);

    const questions = await screen.findByRole('region', { name: 'Вопросы примера' });
    expect(screen.queryByRole('region', { name: 'Спросите о своей саге' })).toBeNull();
    // A question goes into the field; the author sends it himself.
    fireEvent.click(within(questions).getByRole('button', { name: 'Чем кончилась игра бабушки?' }));
    expect((screen.getByRole('textbox', { name: 'Вопрос' }) as HTMLTextAreaElement).value).toBe('Чем кончилась игра бабушки?');
    // The field asks about the example, not about a saga of the author.
    expect(screen.getByRole('textbox', { name: 'Вопрос' }).getAttribute('placeholder')).toBe('Спросите сагу-пример…');
    expect(turn).not.toHaveBeenCalled();

    // The mark stands by the name of the saga in the top bar of the shell (board 14·06).
    const name = screen.getByRole('button', { name: `Сага: ${saga.title}` });
    expect(within(screen.getByRole('banner')).getByText('Пример')).toBeTruthy();
    // And in the menu of the saga: the demo saga carries the mark, a saga of the author does not.
    fireEvent.click(name);
    const menu = await screen.findByRole('menu', { name: 'Выбор саги' });
    expect(within(within(menu).getByRole('menuitem', { name: `${saga.title} Пример` })).getByText('Пример')).toBeTruthy();
    expect(within(menu).getByRole('menuitem', { name: 'Хроники Белого порта' })).toBeTruthy();
  });

  it('keeps the general examples and no mark in a saga of the author', async () => {
    const api = createMockApiClient();
    await api.openDemoSaga();
    renderChat(api, projectId, slot);
    expect(await screen.findByRole('region', { name: 'Спросите о своей саге' })).toBeTruthy();
    expect(screen.getByRole('textbox', { name: 'Вопрос' }).getAttribute('placeholder')).toBe('Спросите о своей саге…');
    expect(screen.queryByRole('region', { name: 'Вопросы примера' })).toBeNull();
    expect(screen.queryByText('Пример')).toBeNull();
  });

  it('shows the general examples in the demo saga of a build without its questions', async () => {
    const api = createMockApiClient();
    const saga = await api.openDemoSaga();
    renderChat(api, saga.id, null);
    expect(await screen.findByRole('region', { name: 'Спросите о своей саге' })).toBeTruthy();
  });
});
