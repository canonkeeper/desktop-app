import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { useState } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../api/mock';
import { projectId } from '../../api/mock/repository/fixtures';
import { ApiStatusError, createIdempotencyKey, NetworkApiError } from '../../shared/api';
import { FakeWebSocket } from '../../test/FakeWebSocket';
import { forgetAllTurns } from './answer-ready';
import { questionSlot, readQuestionDraft } from './question-draft';
import { ResearchChatPanel } from './ResearchChatPanel';
import { ToastsProvider } from '../../ui/feedback/Toast';

beforeEach(() => {
  vi.stubGlobal('innerWidth', 1440);
  vi.stubGlobal('innerHeight', 900);
  FakeWebSocket.reset();
  vi.stubGlobal('WebSocket', FakeWebSocket);
});

afterEach(() => {
  forgetAllTurns();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

type Api = ReturnType<typeof createMockApiClient>;

async function newChat(api: Api, title: string) {
  return api.createChatSession(projectId, { scope: 'project', sourceView: 'published', title }, { idempotencyKey: createIdempotencyKey('createChatSession') });
}

/** The panel as the page opens it; `reopen` is the author coming back: another section, a reload, a new sign-in. */
function openPanel(api: Api, selectedChatId: string, authorId?: string) {
  let select: (chatId: string) => void = () => undefined;
  function SelectedChat({ first }: { first: string }) {
    const [chatId, setChatId] = useState(first);
    select = setChatId;
    return <QueryClientProvider client={new QueryClient({ defaultOptions: { mutations: { retry: false }, queries: { retry: false } } })}><ToastsProvider>
      <ResearchChatPanel api={api} authorId={authorId} canCreate onOpenSource={vi.fn()} onSelectChatId={setChatId} projectId={projectId} selectedChatId={chatId} sourceView="published" />
    </ToastsProvider></QueryClientProvider>;
  }
  render(<SelectedChat first={selectedChatId} />);
  return {
    select: (chatId: string) => act(() => select(chatId)),
    reopen(chatId: string, author = authorId) {
      cleanup();
      return openPanel(api, chatId, author);
    },
  };
}

const field = (name = 'Вопрос') => screen.findByRole('textbox', { name }) as Promise<HTMLTextAreaElement>;

function refusal(status: number, code: string) {
  return new ApiStatusError(status, { type: 'about:blank', title: 'Refused', status, code, requestId: 'req-draft' }, new Response(null, { status }));
}

// specs/2026-11-10-ux1-chat.md CH1: what the author typed in the question field is not lost.
describe('the question the author typed', () => {
  it('stands in the field of a new chat when the author comes back', async () => {
    const api = createMockApiClient();
    const panel = openPanel(api, '');
    fireEvent.change(await field(), { target: { value: 'Кто первым заметил дым?\nИ кто поднял тревогу?' } });
    panel.reopen('');
    expect((await field()).value).toBe('Кто первым заметил дым?\nИ кто поднял тревогу?');
  });

  // CH1.6: a shared saga in a shared browser — the draft of one author is not in the field of another.
  it('is the draft of its author alone', async () => {
    const api = createMockApiClient();
    const chat = await newChat(api, 'Общая сага');
    const panel = openPanel(api, chat.id, 'author-1');
    fireEvent.change(await field(), { target: { value: 'Вопрос первого автора' } });
    const other = panel.reopen(chat.id, 'author-2');
    expect((await field()).value).toBe('');
    other.reopen(chat.id, 'author-1');
    expect((await field()).value).toBe('Вопрос первого автора');
  });

  it('is kept by chat: another chat and «Новый чат» do not wipe it', async () => {
    const api = createMockApiClient();
    const first = await newChat(api, 'Пожар');
    const second = await newChat(api, 'Суд');
    const panel = openPanel(api, first.id);
    fireEvent.change(await field(), { target: { value: 'Про пожар' } });
    await panel.select(second.id);
    await waitFor(async () => expect((await field()).value).toBe(''));
    fireEvent.change(await field(), { target: { value: 'Про суд' } });
    fireEvent.click(screen.getByRole('button', { name: 'Новый чат' }));
    await waitFor(async () => expect((await field()).value).toBe(''));
    await panel.select(first.id);
    await waitFor(async () => expect((await field()).value).toBe('Про пожар'));
    panel.reopen(second.id);
    expect((await field()).value).toBe('Про суд');
  });

  it('is forgotten once the server took the question, and the new chat starts empty', async () => {
    const api = createMockApiClient();
    const panel = openPanel(api, '');
    fireEvent.change(await field(), { target: { value: 'Кто первым заметил дым?' } });
    expect(readQuestionDraft(questionSlot(projectId, ''))).toBe('Кто первым заметил дым?');
    fireEvent.click(screen.getByRole('button', { name: 'Спросить' }));
    await field('Уточнение');
    const chat = (await api.listChatSessions(projectId, {})).data[0]!;
    expect(readQuestionDraft(questionSlot(projectId, ''))).toBeNull();
    expect(readQuestionDraft(questionSlot(projectId, chat.id))).toBeNull();
    panel.reopen('');
    expect((await field()).value).toBe('');
  });

  it('stays in its chat when the server refused the question', async () => {
    const api = createMockApiClient();
    vi.spyOn(api, 'createChatTurn').mockRejectedValue(refusal(429, 'quota.chat_turns_daily'));
    const panel = openPanel(api, '');
    fireEvent.change(await field(), { target: { value: 'Кто первым заметил дым?' } });
    fireEvent.click(screen.getByRole('button', { name: 'Спросить' }));
    await waitFor(() => expect(api.createChatTurn).toHaveBeenCalled());
    const chat = (await api.listChatSessions(projectId, {})).data[0]!;
    await waitFor(async () => expect((await field()).disabled).toBe(false));
    expect((await field()).value).toBe('Кто первым заметил дым?');
    // The first question got its chat: the draft went there with it, and a new chat is empty (CH1.4).
    expect(readQuestionDraft(questionSlot(projectId, chat.id))).toBe('Кто первым заметил дым?');
    expect(readQuestionDraft(questionSlot(projectId, ''))).toBeNull();
    const again = panel.reopen(chat.id);
    expect((await field()).value).toBe('Кто первым заметил дым?');
    again.reopen('');
    expect((await field()).value).toBe('');
  });

  it('is forgotten once the server took the correction and kept when it did not', async () => {
    const api = createMockApiClient();
    const chat = await newChat(api, 'Обложка');
    const panel = openPanel(api, chat.id);
    fireEvent.change(await field(), { target: { value: 'Нарисуй обложку' } });
    fireEvent.click(screen.getByRole('button', { name: 'Спросить' }));
    const correction = await field('Уточнение');
    const steer = vi.spyOn(api, 'steerChatTurn').mockRejectedValueOnce(refusal(409, 'chat.steer_retry'));
    fireEvent.change(correction, { target: { value: 'Без дракона' } });
    fireEvent.click(screen.getByRole('button', { name: 'Уточнить' }));
    expect(await screen.findByText(/Сейчас уточнение не принимается/u)).toBeTruthy();
    expect(readQuestionDraft(questionSlot(projectId, chat.id))).toBe('Без дракона');
    fireEvent.click(screen.getByRole('button', { name: 'Уточнить' }));
    await waitFor(() => expect(steer).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(correction.value).toBe(''));
    expect(readQuestionDraft(questionSlot(projectId, chat.id))).toBeNull();
    void panel;
  });
});

// specs/2026-11-11-ux4-after-answer.md AA2.8 (Ч-34): no network is said apart from a failure of the service.
describe('a question the network did not carry', () => {
  it('says there is no connection and keeps the question in the field', async () => {
    const api = createMockApiClient();
    const chat = await newChat(api, 'Без сети');
    vi.spyOn(api, 'createChatTurn').mockRejectedValue(new NetworkApiError(new TypeError('Failed to fetch')));
    openPanel(api, chat.id);
    fireEvent.change(await field(), { target: { value: 'Кто первым заметил дым?' } });
    const ask = screen.getByRole('button', { name: 'Спросить' });
    await waitFor(() => expect(ask).toHaveProperty('disabled', false));
    fireEvent.click(ask);
    expect(await screen.findByText('Нет связи с сервером. Вопрос остался в поле.')).toBeTruthy();
    expect(screen.queryByText(/Сервис временно недоступен/u)).toBeNull();
    expect((await field()).value).toBe('Кто первым заметил дым?');
  });
});

// AA2.7 (Ч-36): «идут два ответа» names those questions and leads to their chats; the question stays in the field.
describe('a question over the answers being prepared', () => {
  it('names the questions being answered as ways to their chats', async () => {
    const api = createMockApiClient();
    const first = await newChat(api, 'Что Мара знает о пожаре');
    const second = await newChat(api, 'Сколько лет Саве');
    for (const chat of [first, second]) await api.createChatTurn(chat.id, { body: chat.title }, { idempotencyKey: createIdempotencyKey('createChatTurn') });
    const third = await newChat(api, 'Третий');
    vi.spyOn(api, 'createChatTurn').mockRejectedValue(refusal(429, 'quota.active_turns'));
    const panel = openPanel(api, third.id);
    fireEvent.change(await field(), { target: { value: 'Кто поднял тревогу?' } });
    const ask = screen.getByRole('button', { name: 'Спросить' });
    await waitFor(() => expect(ask).toHaveProperty('disabled', false));
    fireEvent.click(ask);
    const block = await screen.findByText('Готовятся ответы на вопросы:');
    const refused = block.closest('section')!;
    expect(within(refused).getByText('Ваш вопрос остался в поле.')).toBeTruthy();
    // The refusal ends by itself when an answer is ready: there is nothing to write to the support about.
    expect(within(refused).queryByText(/Написать (нам|в поддержку)/u)).toBeNull();
    fireEvent.click(within(refused).getByRole('button', { name: '«Сколько лет Саве»' }));
    // The chat of that question opens; the refused question waits in the field of its own chat.
    await waitFor(() => expect(document.querySelector(`[aria-current="page"][aria-label^="Сколько лет Саве"]`)).toBeTruthy());
    expect(readQuestionDraft(questionSlot(projectId, third.id))).toBe('Кто поднял тревогу?');
    void panel;
  });
});
