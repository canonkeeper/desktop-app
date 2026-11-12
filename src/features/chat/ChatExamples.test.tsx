import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import type { ReactNode } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../api/mock';
import { projectId } from '../../api/mock/repository/fixtures';
import { sagaQueryKeys } from '../../entities/saga/api';
import { createIdempotencyKey } from '../../shared/api';
import { AccountSessionContext } from '../../shared/session/account-session';
import { FakeWebSocket } from '../../test/FakeWebSocket';
import { ResearchChatPanel } from './ResearchChatPanel';
import { LiveStatus, usualMinutes, waitLine } from './feed/LiveStatus';
import { forgetServerClock, noteServerDate } from '../../shared/clock/server-clock';
import type { FeedTurn } from './feed/feed-model';
import { ToastsProvider } from '../../ui/feedback/Toast';

const examples = [
  'Что [имя героя] знает о [событии] к главе [номер]?',
  'Где впервые упоминается [предмет или место]?',
  'Сколько лет [имя героя] в главе [номер] и не противоречит ли это раннему тексту?',
  'Перескажи, что происходило с [имя героя] в книге [номер].',
  'Не спорит ли с каноном эта сцена: [вставьте сцену]',
];
// The questions the mock asks of the map of «Хроники Белого порта» (specs/2026-11-10-first-questions.md FQ1.2).
const questions = [
  'Мара: кто это и что важного происходит с этим персонажем?',
  'Мара и Сава: что их связывает?',
  'Где впервые упоминается Белый порт и что там происходит?',
];
const more = 'Ещё примеры вопросов';

beforeEach(() => {
  vi.stubGlobal('innerWidth', 1440);
  vi.stubGlobal('innerHeight', 900);
  FakeWebSocket.reset();
  vi.stubGlobal('WebSocket', FakeWebSocket);
});

afterEach(() => vi.unstubAllGlobals());

function renderPanel(api: ReturnType<typeof createMockApiClient>, selectedChatId = '', canCreate = true, wrap = (panel: ReactNode) => panel) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}><ToastsProvider>
      {wrap(<ResearchChatPanel api={api} canCreate={canCreate} onOpenSource={vi.fn()} onSelectChatId={vi.fn()}
        projectId={projectId} selectedChatId={selectedChatId} sourceView="published" />)}
    </ToastsProvider></QueryClientProvider>,
  );
  return {
    /** The answer of the questions has reached the page: what is on the screen now is what it shows with them. */
    answered: async () => {
      await waitFor(() => expect(client.getQueryState(sagaQueryKeys.firstQuestions(projectId, 'published'))?.status).toBe('success'));
      await act(async () => undefined);
    },
  };
}

/** The answer of the first questions with these texts. */
function answerOf(texts: readonly string[], pending = false) {
  return { questions: texts.map((text) => ({ kind: 'hero' as const, text })), pending };
}

/** A saga whose map gives no three questions: the mock answers with what is given. */
function withQuestions(api: ReturnType<typeof createMockApiClient>, texts: readonly string[]) {
  return vi.spyOn(api, 'getSagaFirstQuestions').mockResolvedValue(answerOf(texts));
}

function buttonTexts(region: HTMLElement) {
  return within(region).getAllByRole('button').map((button) => button.textContent);
}

describe('ChatExamples (UC2.3)', () => {
  it('a new chat of a saga without a map offers five examples and what to expect; an example goes into the field, not to the model', async () => {
    const api = createMockApiClient();
    withQuestions(api, []);
    const createSession = vi.spyOn(api, 'createChatSession');
    const createTurn = vi.spyOn(api, 'createChatTurn');
    renderPanel(api);

    const empty = await screen.findByRole('region', { name: 'Спросите о своей саге' });
    expect(within(empty).getByRole('heading', { name: 'Спросите о своей саге' })).toBeTruthy();
    expect(within(empty).getByText('Спросите о героях, местах и событиях книги — отвечу со ссылками на главы')).toBeTruthy();
    expect(empty.textContent).not.toContain('в списке чатов');
    expect(buttonTexts(empty)).toEqual(examples);
    // specs/2026-11-11-ux9-shelf-chat.md SC3.3 (Ч-03): one term, the one the line of a running turn says.
    expect(within(empty).getByText(/^Отвечаю только по тексту ваших книг и даю ссылки на главы и абзацы\. Обычный вопрос — 1–2 минуты, обзор всей саги — до 15 минут\. Можно уйти с этой страницы — ответ сохранится в этом чате\.$/u)).toBeTruthy();
    expect(waitLine(30_000, { queued: false, helper: false })).toContain(`обычно ${usualMinutes}`);

    const field = screen.getByRole('textbox', { name: 'Вопрос' }) as HTMLTextAreaElement;
    expect(field.placeholder).toBe('Спросите о своей саге…');
    expect(screen.getByRole('button', { name: 'Спросить' })).toHaveProperty('disabled', true);
    await waitFor(() => expect(field.disabled).toBe(false));

    fireEvent.click(within(empty).getByRole('button', { name: examples[0] }));

    expect(field.value).toBe(examples[0]);
    expect(document.activeElement).toBe(field);
    expect(field.value.slice(field.selectionStart, field.selectionEnd)).toBe('[имя героя]');
    expect(screen.getByRole('button', { name: 'Спросить' })).toHaveProperty('disabled', false);
    expect(createSession).not.toHaveBeenCalled();
    expect(createTurn).not.toHaveBeenCalled();
  });

  // specs/2026-11-11-ux9-shelf-chat.md SC3.3 (Ч-01): an example with «[…]» does not go as it is — «Спросить» puts the cursor
  // into the first bracket and says to replace them; Tab steps from a bracket to the next one.
  it('sends no example with brackets, leads to the first bracket and steps between them by Tab', async () => {
    const api = createMockApiClient();
    withQuestions(api, []);
    const createTurn = vi.spyOn(api, 'createChatTurn');
    renderPanel(api);
    const empty = await screen.findByRole('region', { name: 'Спросите о своей саге' });
    const field = screen.getByRole('textbox', { name: 'Вопрос' }) as HTMLTextAreaElement;
    await waitFor(() => expect(field.disabled).toBe(false));
    fireEvent.click(within(empty).getByRole('button', { name: examples[0] }));
    const selected = () => field.value.slice(field.selectionStart, field.selectionEnd);

    fireEvent.keyDown(field, { key: 'Tab' });
    expect(selected()).toBe('[событии]');
    fireEvent.keyDown(field, { key: 'Tab' });
    expect(selected()).toBe('[номер]');
    fireEvent.keyDown(field, { key: 'Tab', shiftKey: true });
    expect(selected()).toBe('[событии]');

    const ask = screen.getByRole('button', { name: 'Спросить' });
    expect(ask.getAttribute('aria-disabled')).toBe('true');
    fireEvent.click(ask);
    expect(await screen.findByText('Замените слова в скобках своими')).toBeTruthy();
    expect(document.activeElement).toBe(field);
    expect(selected()).toBe('[имя героя]');
    fireEvent.keyDown(field, { key: 'Enter' });
    expect(createTurn).not.toHaveBeenCalled();

    fireEvent.change(field, { target: { value: 'Что Мара знает о кинжале к главе 12?' } });
    expect(screen.getByRole('button', { name: 'Спросить' }).getAttribute('aria-disabled')).toBeNull();
  });

  // specs/2026-11-12-ux12-shell.md SH1.1: only the words in brackets the example put hold the question.
  it('sends a question with brackets of the author’s own, and Tab is no stop at them', async () => {
    const api = createMockApiClient();
    withQuestions(api, []);
    const createSession = vi.spyOn(api, 'createChatSession');
    renderPanel(api);
    await screen.findByRole('region', { name: 'Спросите о своей саге' });
    const field = screen.getByRole('textbox', { name: 'Вопрос' }) as HTMLTextAreaElement;
    await waitFor(() => expect(field.disabled).toBe(false));

    fireEvent.change(field, { target: { value: 'Что значит сноска [1] в главе 3 и пометка [так в тексте]?' } });
    const ask = screen.getByRole('button', { name: 'Спросить' });
    expect(ask.getAttribute('aria-disabled')).toBeNull();
    // Tab leaves the field as anywhere: it is not kept for brackets the example did not put.
    expect(fireEvent.keyDown(field, { key: 'Tab' })).toBe(true);
    fireEvent.keyDown(field, { key: 'Enter' });
    await waitFor(() => expect(createSession).toHaveBeenCalledWith(projectId, expect.objectContaining({
      savedAssignment: 'Что значит сноска [1] в главе 3 и пометка [так в тексте]?',
    }), expect.anything()));
  });

  it('holds an example while one of its blanks stands as it was — after Ctrl+Z and after a reload too — and lets it go once all are replaced', async () => {
    const api = createMockApiClient();
    withQuestions(api, []);
    const createSession = vi.spyOn(api, 'createChatSession');
    renderPanel(api);
    const empty = await screen.findByRole('region', { name: 'Спросите о своей саге' });
    let field = screen.getByRole('textbox', { name: 'Вопрос' }) as HTMLTextAreaElement;
    await waitFor(() => expect(field.disabled).toBe(false));
    fireEvent.click(within(empty).getByRole('button', { name: examples[0] }));
    const selected = () => field.value.slice(field.selectionStart, field.selectionEnd);
    const closed = () => screen.getByRole('button', { name: 'Спросить' }).getAttribute('aria-disabled') === 'true';

    // Two blanks replaced, a footnote of the author's own added: the third blank still holds the question.
    const half = 'Что Мара знает о кинжале к главе [номер]? Сноска [1].';
    fireEvent.change(field, { target: { value: 'Что Мара знает о [событии] к главе [номер]?' } });
    fireEvent.change(field, { target: { value: 'Что Мара знает о кинжале к главе [номер]?' } });
    fireEvent.change(field, { target: { value: half } });
    expect(closed()).toBe(true);
    field.setSelectionRange(0, 0);
    fireEvent.keyDown(field, { key: 'Tab' });
    expect(selected()).toBe('[номер]');
    fireEvent.keyDown(field, { key: 'Tab' });
    expect(selected()).toBe('[номер]');

    // A reload keeps the question in the field and what of it is still the example's.
    cleanup();
    renderPanel(api);
    field = await screen.findByRole('textbox', { name: 'Вопрос' }) as HTMLTextAreaElement;
    await waitFor(() => expect(field.value).toBe(half));
    expect(closed()).toBe(true);

    fireEvent.change(field, { target: { value: 'Что Мара знает о кинжале к главе 12? Сноска [1].' } });
    expect(closed()).toBe(false);
    // Ctrl+Z brings the words of the example back, and with them the hold.
    fireEvent.change(field, { target: { value: half } });
    expect(closed()).toBe(true);
    fireEvent.change(field, { target: { value: 'Что Мара знает о кинжале к главе 12? Сноска [1].' } });
    fireEvent.keyDown(field, { key: 'Enter' });
    await waitFor(() => expect(createSession).toHaveBeenCalledTimes(1));
  });

  it('forgets the example erased whole: a question of the author’s own with brackets goes', async () => {
    const api = createMockApiClient();
    withQuestions(api, []);
    renderPanel(api);
    const empty = await screen.findByRole('region', { name: 'Спросите о своей саге' });
    const field = screen.getByRole('textbox', { name: 'Вопрос' }) as HTMLTextAreaElement;
    await waitFor(() => expect(field.disabled).toBe(false));
    fireEvent.click(within(empty).getByRole('button', { name: examples[1] }));
    fireEvent.change(field, { target: { value: '' } });
    fireEvent.change(field, { target: { value: 'Где [предмет или место] в сноске [2]?' } });
    expect(screen.getByRole('button', { name: 'Спросить' }).getAttribute('aria-disabled')).toBeNull();
  });

  it('on a phone the field asks for «Ваш вопрос…» and keeps «Спросить» as its name (R2.3.2)', async () => {
    vi.stubGlobal('matchMedia', vi.fn((query: string) => ({
      matches: query === '(max-width: 760px)', addEventListener: vi.fn(), removeEventListener: vi.fn(),
    })));
    renderPanel(createMockApiClient());

    expect((await screen.findByRole('textbox', { name: 'Вопрос' }) as HTMLTextAreaElement).placeholder).toBe('Ваш вопрос…');
    // specs/2026-11-11-ux9-shelf-chat.md SC2.2 (П-15): the button of an icon says its word under it on a phone.
    const ask = screen.getByRole('button', { name: 'Спросить' });
    expect(ask.textContent).toBe('Спросить');
    expect(ask.getAttribute('data-caption')).toBe('true');
  });

  it('a chat with a question shows no examples', async () => {
    const api = createMockApiClient();
    const chat = await api.createChatSession(projectId, { scope: 'project', sourceView: 'published', title: 'Мать Мирона' },
      { idempotencyKey: createIdempotencyKey('createChatSession') });
    await api.createChatTurn(chat.id, { body: 'Что известно о матери?' }, { idempotencyKey: createIdempotencyKey('createChatTurn') });
    renderPanel(api, chat.id);

    await screen.findByRole('textbox', { name: 'Вопрос' });
    expect(screen.queryByRole('region', { name: 'Спросите о своей саге' })).toBeNull();
    expect(screen.queryByRole('button', { name: examples[1] })).toBeNull();
    expect(screen.queryByRole('button', { name: questions[0] })).toBeNull();
  });

  it('a reader without questions keeps the former empty state and asks nothing of the map', async () => {
    const api = createMockApiClient();
    const asked = vi.spyOn(api, 'getSagaFirstQuestions');
    renderPanel(api, '', false);

    // specs/2026-11-11-ux9-shelf-chat.md SC1.2: the reader is told why the list is empty.
    expect(await screen.findByText('В этой саге вы читатель: чаты ведёт автор.')).toBeTruthy();
    expect(screen.queryByRole('region', { name: 'Спросите о своей саге' })).toBeNull();
    expect(asked).not.toHaveBeenCalled();
  });
});

// specs/2026-11-10-first-questions.md FQ1.5, FQ1.6.
describe('the three questions of the saga', () => {
  it('stand under the title as three buttons with the names of the book; the examples fold under «Ещё примеры вопросов»', async () => {
    const api = createMockApiClient();
    renderPanel(api);

    const empty = await screen.findByRole('region', { name: 'Спросите о своей саге' });
    await within(empty).findByRole('button', { name: questions[0] });
    expect(buttonTexts(empty)).toEqual([...questions, more]);
    const toggle = within(empty).getByRole('button', { name: more });
    expect(toggle.getAttribute('aria-expanded')).toBe('false');

    fireEvent.click(toggle);
    expect(toggle.getAttribute('aria-expanded')).toBe('true');
    expect(buttonTexts(empty)).toEqual([...questions, more, ...examples]);
    expect(document.getElementById(toggle.getAttribute('aria-controls') ?? '')?.textContent).toContain(examples[4]);

    // An example works as before: its first «[…]» is selected for typing over.
    const field = screen.getByRole('textbox', { name: 'Вопрос' }) as HTMLTextAreaElement;
    await waitFor(() => expect(field.disabled).toBe(false));
    fireEvent.click(within(empty).getByRole('button', { name: examples[1] }));
    expect(field.value.slice(field.selectionStart, field.selectionEnd)).toBe('[предмет или место]');

    fireEvent.click(toggle);
    expect(toggle.getAttribute('aria-expanded')).toBe('false');
    expect(within(empty).queryByRole('button', { name: examples[1] })).toBeNull();
  });

  it('a question goes into the field whole with the cursor at its end; nothing is sent and the count of the day stays', async () => {
    const api = createMockApiClient();
    const createSession = vi.spyOn(api, 'createChatSession');
    const createTurn = vi.spyOn(api, 'createChatTurn');
    renderPanel(api);

    const question = await screen.findByRole('button', { name: questions[1] });
    const field = screen.getByRole('textbox', { name: 'Вопрос' }) as HTMLTextAreaElement;
    await waitFor(() => expect(field.disabled).toBe(false));
    const quota = vi.spyOn(api, 'getAccountQuota');
    fireEvent.click(question);

    expect(field.value).toBe(questions[1]);
    expect(document.activeElement).toBe(field);
    expect([field.selectionStart, field.selectionEnd]).toEqual([questions[1]!.length, questions[1]!.length]);
    expect(screen.getByRole('button', { name: 'Спросить' })).toHaveProperty('disabled', false);
    expect(createSession).not.toHaveBeenCalled();
    expect(createTurn).not.toHaveBeenCalled();
    expect(quota).not.toHaveBeenCalled();
  });

  it('fewer than three questions keep the former screen without empty places', async () => {
    const api = createMockApiClient();
    withQuestions(api, [questions[0]!, questions[2]!]);
    const panel = renderPanel(api);

    const empty = await screen.findByRole('region', { name: 'Спросите о своей саге' });
    await panel.answered();
    expect(buttonTexts(empty)).toEqual(examples);
  });

  it('do not take the place of the examples the author has begun with', async () => {
    for (const begin of ['focus', 'type'] as const) {
      const api = createMockApiClient();
      let answer: (value: Awaited<ReturnType<typeof api.getSagaFirstQuestions>>) => void = () => undefined;
      vi.spyOn(api, 'getSagaFirstQuestions').mockImplementation(() => new Promise((resolve) => { answer = resolve; }));
      const panel = renderPanel(api);

      const empty = await screen.findByRole('region', { name: 'Спросите о своей саге' });
      expect(buttonTexts(empty)).toEqual(examples);
      const field = screen.getByRole('textbox', { name: 'Вопрос' }) as HTMLTextAreaElement;
      await waitFor(() => expect(field.disabled).toBe(false));
      if (begin === 'focus') within(empty).getByRole('button', { name: examples[2] }).focus();
      else fireEvent.change(field, { target: { value: 'Кто такая Мара' } });
      await act(async () => answer(answerOf(questions)));
      await panel.answered();

      expect(buttonTexts(empty)).toEqual(examples);
      document.body.innerHTML = '';
    }
  });

  it('are not shown to an author with an unverified address: he has no question field', async () => {
    const api = createMockApiClient();
    const panel = renderPanel(api, '', true, (children) => (
      <AccountSessionContext.Provider value={{ busy: false, emailVerification: { email: 'a@example.test', status: 'idle', canSend: true, onSend: vi.fn() } }}>
        {children}
      </AccountSessionContext.Provider>
    ));

    const empty = await screen.findByRole('region', { name: 'Спросите о своей саге' });
    await panel.answered();
    expect(within(empty).queryAllByRole('button')).toHaveLength(0);
  });

  it('leave the demo saga to its own questions', async () => {
    const api = createMockApiClient();
    const asked = vi.spyOn(api, 'getSagaFirstQuestions');
    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><ToastsProvider>
        <ResearchChatPanel api={api} canCreate demo onOpenSource={vi.fn()} onSelectChatId={vi.fn()}
          projectId={projectId} selectedChatId="" sourceView="published" />
      </ToastsProvider></QueryClientProvider>,
    );

    await screen.findByRole('textbox', { name: 'Вопрос' });
    await act(async () => undefined);
    expect(asked).not.toHaveBeenCalled();
    expect(screen.queryByRole('button', { name: questions[0] })).toBeNull();
  });
});

// specs/2026-11-10-first-questions.md FQ1.10: the author opened the chat right after the upload, or came by the letter «Сага
// готова» before the map — the chat asks again every 15 seconds while the map is still to come, at most 160 times (40
// minutes), and the questions take the place of the examples.
describe('a chat opened before the map', () => {
  beforeEach(() => vi.useFakeTimers({ shouldAdvanceTime: true }));
  afterEach(() => vi.useRealTimers());

  const pause = (milliseconds: number) => act(() => vi.advanceTimersByTimeAsync(milliseconds));

  it('asks again while the map is being built and stops once the questions are there', async () => {
    const api = createMockApiClient();
    const asked = vi.spyOn(api, 'getSagaFirstQuestions')
      .mockResolvedValueOnce(answerOf([], true))
      .mockResolvedValueOnce(answerOf([], true))
      .mockResolvedValue(answerOf(questions));
    renderPanel(api);

    const empty = await screen.findByRole('region', { name: 'Спросите о своей саге' });
    await waitFor(() => expect(asked).toHaveBeenCalledTimes(1));
    expect(buttonTexts(empty)).toEqual(examples);
    await pause(14_000);
    expect(asked).toHaveBeenCalledTimes(1);
    await pause(1_000);
    await waitFor(() => expect(asked).toHaveBeenCalledTimes(2));
    expect(buttonTexts(empty)).toEqual(examples);
    await pause(15_000);
    await waitFor(() => expect(buttonTexts(empty)).toEqual([...questions, more]));
    expect(asked).toHaveBeenCalledTimes(3);
    await pause(60_000);
    expect(asked).toHaveBeenCalledTimes(3);
  });

  it('asks at most 160 times more and then leaves the examples', async () => {
    const api = createMockApiClient();
    const asked = vi.spyOn(api, 'getSagaFirstQuestions').mockResolvedValue(answerOf([], true));
    renderPanel(api);

    await screen.findByRole('region', { name: 'Спросите о своей саге' });
    await waitFor(() => expect(asked).toHaveBeenCalledTimes(1));
    for (let repeat = 1; repeat <= 160; repeat += 1) {
      await pause(15_000);
      await waitFor(() => expect(asked).toHaveBeenCalledTimes(1 + repeat));
    }
    await pause(5 * 60_000);
    expect(asked).toHaveBeenCalledTimes(161);
  }, 60_000);

  it('does not ask again when the map is not being built', async () => {
    const api = createMockApiClient();
    const asked = withQuestions(api, []);
    renderPanel(api);

    const empty = await screen.findByRole('region', { name: 'Спросите о своей саге' });
    await waitFor(() => expect(asked).toHaveBeenCalledTimes(1));
    await pause(5 * 60_000);
    expect(asked).toHaveBeenCalledTimes(1);
    expect(buttonTexts(empty)).toEqual(examples);
  });

  it('does not put the questions in place of the examples the author has focused', async () => {
    const api = createMockApiClient();
    const asked = vi.spyOn(api, 'getSagaFirstQuestions').mockResolvedValueOnce(answerOf([], true)).mockResolvedValue(answerOf(questions));
    renderPanel(api);

    const empty = await screen.findByRole('region', { name: 'Спросите о своей саге' });
    await waitFor(() => expect(asked).toHaveBeenCalledTimes(1));
    within(empty).getByRole('button', { name: examples[0] }).focus();
    await pause(15_000);
    await waitFor(() => expect(asked).toHaveBeenCalledTimes(2));
    await pause(1_000);
    expect(buttonTexts(empty)).toEqual(examples);
    await pause(60_000);
    expect(asked).toHaveBeenCalledTimes(2);
  });
});

// specs/2026-11-11-ux9-shelf-chat.md SC3.8 (J-80): a clock of the device an hour ahead does not make the first second of a turn
// an hour long, nor a big question.
describe('the time of a running turn', () => {
  afterEach(() => forgetServerClock());

  it('counts from the time of the server, whatever the clock of the device', () => {
    const now = Date.now();
    // The server is an hour behind the device.
    noteServerDate(new Date(now - 3_600_000).toUTCString(), now, now);
    const startedAt = new Date(now - 3_600_000 - 2_000).toISOString();
    render(<LiveStatus startedAt={startedAt} turn={{ activity: 'thinking', steps: [], tail: null } as unknown as FeedTurn} />);
    const line = screen.getByText(/можно уйти — ответ сохранится/u);
    expect(line.textContent).toMatch(/^0:0\d · обычно 1–2 минуты/u);
  });
});
