import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../api/mock';
import { projectId } from '../../api/mock/repository/fixtures';
import { createIdempotencyKey, type AgentTimelineEvent } from '../../shared/api';
import { createAppRoute } from '../../shared/navigation/app-route';
import { NotesContext } from '../../shared/notes/notes-context';
import { FakeWebSocket } from '../../test/FakeWebSocket';
import { ToastsProvider } from '../../ui/feedback/Toast';
import { forgetAllTurns } from './answer-ready';
import { ChatPage } from './ChatPage';
import { forgetQuestionDrafts } from './question-draft';

beforeEach(() => {
  vi.stubGlobal('innerWidth', 1440);
  vi.stubGlobal('innerHeight', 900);
  FakeWebSocket.reset();
  vi.stubGlobal('WebSocket', FakeWebSocket);
  window.localStorage.clear();
  forgetQuestionDrafts();
});
afterEach(() => {
  forgetAllTurns();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

const body = 'Сава — брат Мары, он ушёл из дома весной.';
const answer = `${body}\n\nДальше можно спросить:\n- А что знает Сава о пожаре?\n- Когда Сава вернулся домой?`;

/** A chat whose one question is answered: the Job succeeded, the timeline ends with the answer and its outcome. */
async function renderAnswered(text = answer, prepare?: (api: ReturnType<typeof createMockApiClient>) => void) {
  const api = createMockApiClient();
  prepare?.(api);
  const chat = await api.createChatSession(projectId, { scope: 'project', sourceView: 'published', title: 'Кто такой Сава' },
    { idempotencyKey: createIdempotencyKey('createChatSession') });
  const turn = await api.createChatTurn(chat.id, { body: 'Кто такой Сава?' }, { idempotencyKey: createIdempotencyKey('createChatTurn') });
  const getJob = api.getJob.bind(api);
  vi.spyOn(api, 'getJob').mockImplementation(async (jobId) => ({ ...(await getJob(jobId)), status: 'succeeded', canCancel: false }));
  const events: AgentTimelineEvent[] = [
    { id: 'question', cursor: 'tl_1', kind: 'user', role: 'main', logicalSection: 0, jobId: turn.jobId, providerTurnId: 'turn-1', text: 'Кто такой Сава?' },
    { id: 'answer', cursor: 'tl_2', kind: 'assistant', role: 'main', logicalSection: 0, jobId: turn.jobId, providerTurnId: 'turn-1', providerItemId: 'item-2', phase: 'final_answer', text },
    { id: 'outcome', cursor: 'tl_3', kind: 'turn_outcome', role: 'main', logicalSection: 0, jobId: turn.jobId, providerTurnId: 'turn-1', outcome: 'completed' },
  ];
  vi.spyOn(api, 'getAgentTimeline').mockResolvedValue({ events, eof: true, needsReconcile: false });
  const capture = vi.fn();
  const createTurn = vi.spyOn(api, 'createChatTurn');
  render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><ToastsProvider>
    <NotesContext.Provider value={{ capture, open: vi.fn(), openWithNavigation: vi.fn() }}>
      <ChatPage api={api} navigate={vi.fn()} route={createAppRoute('/chat', { projectId, chatId: chat.id })} />
    </NotesContext.Provider>
  </ToastsProvider></QueryClientProvider>);
  await screen.findByText(body);
  return { api, capture, createTurn };
}

const field = () => screen.getByRole('textbox', { name: 'Вопрос' }) as HTMLTextAreaElement;

// specs/2026-11-11-ux4-after-answer.md AA1.3: a press puts the question into the field and sends nothing.
describe('the continuations under an answer', () => {
  it('puts a question into the field with the focus there and sends nothing', async () => {
    const { createTurn } = await renderAnswered();
    const offered = await screen.findByRole('list', { name: 'Дальше можно спросить' });
    expect(within(offered).getAllByRole('button').map((button) => button.textContent)).toEqual(['А что знает Сава о пожаре?', 'Когда Сава вернулся домой?']);
    expect(screen.queryByText(/Дальше можно спросить:/u)).toBeNull();
    fireEvent.click(within(offered).getByRole('button', { name: 'Когда Сава вернулся домой?' }));
    await waitFor(() => expect(field().value).toBe('Когда Сава вернулся домой?'));
    await waitFor(() => expect(document.activeElement).toBe(field()));
    expect(createTurn).not.toHaveBeenCalled();
  });

  it('asks «Заменить набранное?» before it takes the place of what the author typed', async () => {
    await renderAnswered();
    fireEvent.change(await screen.findByRole('textbox', { name: 'Вопрос' }), { target: { value: 'Мой вопрос о Мирославе' } });
    fireEvent.click(await screen.findByRole('button', { name: 'А что знает Сава о пожаре?' }));
    const ask = await screen.findByRole('dialog', { name: 'Заменить набранное?' });
    fireEvent.click(within(ask).getByRole('button', { name: 'Отмена' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(field().value).toBe('Мой вопрос о Мирославе');
    fireEvent.click(screen.getByRole('button', { name: 'А что знает Сава о пожаре?' }));
    fireEvent.click(within(await screen.findByRole('dialog', { name: 'Заменить набранное?' })).getByRole('button', { name: 'Заменить' }));
    await waitFor(() => expect(field().value).toBe('А что знает Сава о пожаре?'));
  });

  it('stays and says why while the questions of the day are over', async () => {
    const { createTurn } = await renderAnswered(answer, (api) => {
      const quota = api.getAccountQuota.bind(api);
      vi.spyOn(api, 'getAccountQuota').mockImplementation(async () => {
        const read = await quota();
        return { ...read, chatTurns: { ...read.chatTurns, limit: 5, used: 5 } };
      });
    });
    const offered = await screen.findByRole('button', { name: 'А что знает Сава о пожаре?' });
    await waitFor(() => expect(offered.getAttribute('aria-disabled')).toBe('true'));
    fireEvent.click(offered);
    expect(field().value).toBe('');
    expect(await screen.findAllByText(/Вопросы и уточнения на сегодня закончились/u)).not.toHaveLength(0);
    expect(createTurn).not.toHaveBeenCalled();
  });
});

// AA1.4, AA1.5: «В заметки» and «Скопировать» take the answer without the block; «Неверно» starts a correction.
describe('the buttons of an answer', () => {
  it('takes the answer without its continuations into a note and the clipboard', async () => {
    const writeText = vi.fn(async () => undefined);
    vi.stubGlobal('navigator', { ...navigator, clipboard: { writeText } });
    const { capture } = await renderAnswered();
    const actions = screen.getByRole('group', { name: 'Действия с ответом' });
    fireEvent.click(within(actions).getByRole('button', { name: 'В заметки' }));
    expect(capture).toHaveBeenCalledTimes(1);
    const [[note]] = capture.mock.calls as unknown as [[{ body: string }]];
    expect(note.body).toBe(body);
    fireEvent.click(within(actions).getByRole('button', { name: 'Скопировать' }));
    await waitFor(() => expect(writeText).toHaveBeenCalledWith(body));
    expect(await screen.findByText('Ответ скопирован.')).toBeTruthy();
  });

  it('puts «Это неверно: » into the field with the caret after the colon and sends nothing', async () => {
    const { createTurn } = await renderAnswered();
    fireEvent.click(within(screen.getByRole('group', { name: 'Действия с ответом' })).getByRole('button', { name: 'Неверно' }));
    await waitFor(() => expect(field().value).toBe('Это неверно: '));
    await waitFor(() => expect(document.activeElement).toBe(field()));
    expect(field().selectionStart).toBe('Это неверно: '.length);
    expect(createTurn).not.toHaveBeenCalled();
  });
});

// AA1.6 (Ч-42): a link to a place the book does not have asks the helper where the phrase came from.
describe('a link of an answer to a place that is not there', () => {
  it('puts «Покажи точное место в тексте для: „…“» with the beginning of the phrase into the field', async () => {
    const api = createMockApiClient();
    const chat = await api.createChatSession(projectId, { scope: 'project', sourceView: 'published', title: 'Сава' },
      { idempotencyKey: createIdempotencyKey('createChatSession') });
    const turn = await api.createChatTurn(chat.id, { body: 'Кто такой Сава?' }, { idempotencyKey: createIdempotencyKey('createChatTurn') });
    const getJob = api.getJob.bind(api);
    vi.spyOn(api, 'getJob').mockImplementation(async (jobId) => ({ ...(await getJob(jobId)), status: 'succeeded', canCancel: false }));
    vi.spyOn(api, 'getAgentTimeline').mockResolvedValue({ eof: true, needsReconcile: false, events: [
      { id: 'q', cursor: 'tl_1', kind: 'user', role: 'main', logicalSection: 0, jobId: turn.jobId, text: 'Кто такой Сава?' },
      { id: 'a', cursor: 'tl_2', kind: 'assistant', role: 'main', logicalSection: 0, jobId: turn.jobId, providerTurnId: 't', providerItemId: 'i',
        text: 'Сава — брат Мары. Он ушёл из дома весной [гл. 99 ¶1](source:99/1), когда сгорел амбар.' },
      { id: 'o', cursor: 'tl_3', kind: 'turn_outcome', role: 'main', logicalSection: 0, jobId: turn.jobId, outcome: 'completed' },
    ] });
    const navigate = vi.fn();
    render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><ToastsProvider>
      <ChatPage api={api} navigate={navigate} route={createAppRoute('/chat', { projectId, chatId: chat.id, source: '99/1' })} />
    </ToastsProvider></QueryClientProvider>);
    fireEvent.click(await screen.findByRole('button', { name: 'Спросить, откуда это' }));
    await waitFor(() => expect(field().value).toBe('Покажи точное место в тексте для: „Он ушёл из дома весной“'));
    expect(navigate).toHaveBeenCalledWith('/chat', { projectId, chatId: chat.id }, { replace: true });
  });
});
