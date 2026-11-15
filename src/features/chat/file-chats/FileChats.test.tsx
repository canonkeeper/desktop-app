import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { useState } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../../api/mock';
import { projectId } from '../../../api/mock/repository/fixtures';
import { InShell } from '../../../app/shell/AppShell.test-helpers';
import { bytesOf } from '../../../shared/files/zip';
import { createAppRoute, type RouteNavigate } from '../../../shared/navigation/app-route';
import { NotesContext, type NotesActions } from '../../../shared/notes/notes-context';
import { ChatPage } from '../ChatPage';
import { fileChatWords, turnEndWords } from './file-chat-words';

beforeEach(() => { vi.stubGlobal('innerWidth', 1440); vi.stubGlobal('innerHeight', 900); });
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

/** A saga opened from a saga file of the mock: one book and two chats the file brought (board 44·38). */
async function sagaFromFile(api: ReturnType<typeof createMockApiClient>) {
  const archive = await api.getSagaArchive(projectId);
  const opened = await api.openSagaArchive(new File([(await bytesOf(archive.blob)).slice()], archive.fileName), { idempotencyKey: 'idem-chat-saga-file' });
  const { data } = await api.listSagaTranscripts(opened.projectId);
  return { sagaId: opened.projectId, chats: data };
}

function renderChat(api: ReturnType<typeof createMockApiClient>, query: Record<string, string | undefined>, notes: NotesActions | null = null) {
  const navigate = vi.fn();
  const route = createAppRoute('/chat', query);
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { mutations: { retry: false }, queries: { retry: false } } })}>
      <NotesContext.Provider value={notes}>
        <InShell api={api} navigate={navigate} route={route}><ChatPage api={api} navigate={navigate} route={route} /></InShell>
      </NotesContext.Provider>
    </QueryClientProvider>,
  );
  return navigate;
}

// specs/2026-11-13-desk-sagafile-web.md SW3.1–SW3.3 (board 44·38).
describe('the chats of a saga file', () => {
  it('have no part in the list of a saga without them, and nothing of them is read', async () => {
    const api = createMockApiClient();
    const read = vi.spyOn(api, 'getSagaTranscript');
    const listed = vi.spyOn(api, 'listSagaTranscripts');
    renderChat(api, { projectId, new: '1' });
    const list = await screen.findByRole('complementary', { name: 'Сохранённые чаты' });
    await waitFor(() => expect(listed).toHaveBeenCalledWith(projectId, expect.anything()));
    await waitFor(() => expect(listed.mock.results[0]?.value).toBeDefined());
    expect(within(list).queryByRole('region', { name: fileChatWords.section })).toBeNull();
    expect(read).not.toHaveBeenCalled();
  });

  it('stand under the chats of the saga by their names and the day of their last question, and open by the address', async () => {
    const api = createMockApiClient();
    const { chats, sagaId } = await sagaFromFile(api);
    const navigate = renderChat(api, { projectId: sagaId, new: '1' });
    const part = await screen.findByRole('region', { name: fileChatWords.section });
    const rows = within(part).getAllByRole('button');
    expect(rows.map((row) => row.textContent)).toEqual(['Мара и северная пристань', 'Родословная Савы']);
    expect(part.textContent).toMatch(/12 окт/u);
    fireEvent.click(rows[0]!);
    expect(navigate).toHaveBeenCalledWith('/chat', { projectId: sagaId, fileChat: chats[0]!.id });
  });

  it('read as the feed of a chat without its field, with the line over it, the turns without an answer in words', async () => {
    const api = createMockApiClient();
    const { chats, sagaId } = await sagaFromFile(api);
    const navigate = renderChat(api, { projectId: sagaId, fileChat: chats[0]!.id });
    const notice = await screen.findByRole('note', { name: fileChatWords.notice });
    expect(notice.textContent).toContain('Этот чат пришёл из файла саги. Продолжить его нельзя — задайте вопрос в новом чате');
    expect(screen.queryByLabelText('Вопрос')).toBeNull();
    expect(screen.queryByRole('button', { name: 'Спросить' })).toBeNull();
    const panel = screen.getByRole('region', { name: 'Исследовательский чат' });
    await within(panel).findByText('Кто первым заметил дым в Белом порту?');
    expect(within(panel).getByText('К вопросу была приложена глава „Белый порт“ — в файл она не вошла')).toBeTruthy();
    expect(within(panel).getByText('Ответа нет: помощник был остановлен')).toBeTruthy();
    expect(within(panel).getByText('Ответа нет: помощник не смог ответить')).toBeTruthy();
    expect(within(panel).getByText('В этом чате было ещё 3 вопроса других участников — в файл они не вошли')).toBeTruthy();
    // SW3.3: nothing here asks the helper — no questions after the answer, no «Запомнить», no stop.
    expect(within(panel).queryByText('Дальше можно спросить:')).toBeNull();
    expect(within(panel).queryByRole('button', { name: /Запомнить|Остановить|В заметки/u })).toBeNull();
    // The place of the chapter in words, never its key or «¶».
    const links = within(panel).getAllByRole('button', { name: /Книга 1, гл\./u });
    expect(links.map((link) => link.textContent)).toEqual([
      'Книга 1, гл. 12 „Белый порт“, абзацы 3–5', 'Книга 1, гл. 14 „Колокол“, абзац 2', 'Книга 1, гл. 12 „Белый порт“, абзац 1',
    ]);
    expect(panel.textContent).not.toMatch(/¶|b\d+c\d+|source:/u);
    // «Новый чат» of the line: a new chat of the saga.
    fireEvent.click(within(notice).getByRole('button', { name: 'Новый чат' }));
    expect(navigate).toHaveBeenCalledWith('/chat', { projectId: sagaId, new: '1' });
  });

  it('opens a place of an answer in the book, at its paragraph, with the way back to the chat', async () => {
    const api = createMockApiClient();
    const { chats, sagaId } = await sagaFromFile(api);
    const navigate = renderChat(api, { projectId: sagaId, fileChat: chats[0]!.id });
    const link = await screen.findByRole('button', { name: 'Книга 1, гл. 12 „Белый порт“, абзацы 3–5' });
    fireEvent.click(link);
    await waitFor(() => expect(navigate).toHaveBeenCalledWith('/manuscript/books', expect.objectContaining({
      projectId: sagaId, mode: 'read', chapterId: expect.stringContaining('-chapter-12'), paragraphId: expect.stringContaining('-12-p-3'),
    }), { from: { section: 'chat', name: 'Мара и северная пристань' } }));
  });

  it('picked in «Чаты» of a phone, close the drawer as a chat of the saga does: the chat is in sight, not under the list', async () => {
    vi.stubGlobal('innerWidth', 390);
    vi.stubGlobal('innerHeight', 844);
    const api = createMockApiClient();
    const { chats, sagaId } = await sagaFromFile(api);
    function Routed() {
      const [route, setRoute] = useState(() => createAppRoute('/chat', { projectId: sagaId, new: '1' }));
      const navigate = ((path: string, query?: Record<string, string | undefined>) => setRoute(createAppRoute(path, query ?? {}))) as RouteNavigate;
      return <InShell api={api} navigate={navigate} route={route}><ChatPage api={api} navigate={navigate} route={route} /></InShell>;
    }
    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { mutations: { retry: false }, queries: { retry: false } } })}>
        <Routed />
      </QueryClientProvider>,
    );
    fireEvent.click(await screen.findByRole('button', { name: 'Чаты' }));
    const drawer = await screen.findByRole('dialog', { name: 'Чаты' });
    const part = await within(drawer).findByRole('region', { name: fileChatWords.section });
    fireEvent.click(within(part).getByRole('button', { name: chats[0]!.title }));
    await screen.findByRole('note', { name: fileChatWords.notice });
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Чаты' })).toBeNull());
  });

  it('take a note of a selection of an answer as the chat does, without a turn', async () => {
    const api = createMockApiClient();
    const { chats, sagaId } = await sagaFromFile(api);
    const capture = vi.fn();
    renderChat(api, { projectId: sagaId, fileChat: chats[1]!.id }, { capture, open: vi.fn(), openWithNavigation: vi.fn() });
    const answer = await screen.findByText('В книге об этом не сказано.');
    const range = document.createRange();
    range.selectNodeContents(answer);
    range.getBoundingClientRect = () => new DOMRect(10, 10, 100, 20);
    window.getSelection()!.removeAllRanges();
    window.getSelection()!.addRange(range);
    document.dispatchEvent(new Event('selectionchange'));
    fireEvent.click(await screen.findByRole('button', { name: 'В заметки' }));
    expect(capture).toHaveBeenCalledWith({ body: 'В книге об этом не сказано.' });
  });
});

describe('the words of a turn of a saga file without an answer', () => {
  it('say how it ended, the turn still going when the saga was saved too', () => {
    expect(turnEndWords({ outcome: 'answered', answer: 'Мара.' })).toBeNull();
    expect(turnEndWords({ outcome: 'interrupted' })).toBe('Ответа нет: помощник был остановлен');
    expect(turnEndWords({ outcome: 'failed' })).toBe('Ответа нет: помощник не смог ответить');
    expect(turnEndWords({ outcome: 'answered', answer: ' ' })).toBe('Ответа нет: помощник не смог ответить');
    expect(turnEndWords({ outcome: 'in_progress' })).toBe('Ответа нет: помощник ещё отвечал, когда сага сохранялась в файл');
    expect(fileChatWords.others(1)).toBe('В этом чате был ещё 1 вопрос других участников — в файл он не вошёл');
    expect(fileChatWords.others(5)).toBe('В этом чате было ещё 5 вопросов других участников — в файл они не вошли');
    expect(fileChatWords.others(0)).toBeNull();
  });
});
