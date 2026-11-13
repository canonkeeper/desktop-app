import { act, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ChatFeed } from './ChatFeed';
import type { CanonicalAgentTimelineEvent } from './types';

let cursor = 0;
function event(kind: CanonicalAgentTimelineEvent['kind'], extra: Partial<CanonicalAgentTimelineEvent> = {}): CanonicalAgentTimelineEvent {
  cursor += 1;
  return { cursor: `tl_${cursor}`, id: `item-${cursor}:0`, providerItemId: `item-${cursor}`, providerTurnId: 'turn-1', jobId: 'job-1', kind, logicalSection: 0, role: 'main', ...extra };
}
/** The place the server writes beside the key of the chapter (specs/2026-11-10-ux1-names.md N2.1). */
const place593 = 'Книга 12, гл. 593 „Информация – сила“';
const read = (chapter: string, summary?: string, status: 'running' | 'done' = 'done') => event(status === 'running' ? 'tool_request' : 'tool_result', {
  outcome: status === 'done' ? 'completed' : undefined,
  ...(chapter === '593' ? { places: { 593: place593 } } : {}),
  tool: { namespace: 'saga', name: 'read', arguments: { chapter, around: 41, radius: 25 }, summary, status: status === 'done' ? 'completed' : undefined, success: status === 'done' ? true : undefined },
});
const answer = 'Мать исчезла, когда Мирону было двенадцать.\n\nТекст прямо называет причину её ухода.';

afterEach(() => vi.useRealTimers());

describe('ChatFeed', () => {
  // specs/2026-11-02-writer-workshop.md W4.5: what a workshop's helper wrote in a turn stands right under
  // the answer of that turn, not above every turn.
  it('puts what a turn wrote under its answer, by the native turn of its events', () => {
    render(<ChatFeed
      belowAnswer={(turnIds) => <p>{`заметки хода ${turnIds.join(',')}`}</p>}
      events={[
        event('user', { text: 'Справка, пожалуйста.' }),
        event('assistant', { text: 'Справка готова.' }),
        event('turn_outcome', { outcome: 'completed' }),
        event('user', { text: 'Измени диалог.', providerTurnId: 'turn-2' }),
        event('assistant', { text: 'Подготовил два варианта — они ниже.', providerTurnId: 'turn-2' }),
        event('turn_outcome', { outcome: 'completed', providerTurnId: 'turn-2' }),
      ]}
      jobStatus="succeeded"
    />);
    const [first, second] = screen.getAllByRole('article', { name: 'Вопрос и ответ' });
    expect(within(first!).getByText('заметки хода turn-1')).toBeTruthy();
    const below = within(second!).getByText('заметки хода turn-2');
    const said = within(second!).getByText('Подготовил два варианта — они ниже.');
    expect(said.compareDocumentPosition(below) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it('folds the work of a finished turn behind one line and shows the answer with its actions', () => {
    const renderActions = vi.fn(() => <button type="button">В заметки</button>);
    render(<ChatFeed
      events={[
        event('user', { text: 'Что известно о матери?' }),
        event('reasoning', { text: 'The user asks about the mother.' }),
        event('assistant', { text: 'Ищу её в словаре.' }),
        read('593', 'Том 12, Глава 593. Информация – сила — гл. 593, абзацы ¶16–¶66 из 75'),
        event('assistant', { text: answer }),
        event('turn_outcome', { outcome: 'completed' }),
      ]}
      jobStatus="succeeded"
      renderAssistantActions={renderActions}
    />);

    // specs/2026-11-10-ux4-chat-waiting.md CW2.7: one folded line in the words of the work.
    const work = screen.getByText('Как я искал').closest('details')!;
    expect(work.open).toBe(false);
    expect(work.querySelector('summary')!.textContent).toBe('Как я искал · прочитал 1 главу');
    expect(within(work).getByText('Ищу её в словаре.')).toBeTruthy();
    expect(within(work).getByText(`Прочитал: ${place593}`).closest('li')!.textContent).toBe(`Прочитал: ${place593} — абзацы 16–66 из 75`);
    const answerSection = screen.getByText(/Мать исчезла/u).closest('[data-answer]')!;
    expect(answerSection.compareDocumentPosition(work) & Node.DOCUMENT_POSITION_PRECEDING).toBeTruthy();
    expect(renderActions).toHaveBeenCalledTimes(1);
    expect(screen.getAllByRole('button', { name: 'В заметки' })).toHaveLength(1);
    expect(document.body.textContent).not.toContain('The user asks');
    expect(screen.queryByText('Рассуждения')).toBeNull();
  });

  // CW2.1, CW2.2: the question stays pinned over the work; the status says what the helper does and how long such a turn
  // usually takes, and from the third minute that the question is a big one.
  it('pins the question and says in two lines what the helper does and how long it usually takes', () => {
    vi.useFakeTimers({ now: Date.parse('2026-10-13T12:00:42Z') });
    render(<ChatFeed
      events={[event('user', { text: 'Как менялись отношения?' }), event('assistant', { text: 'Ищу линию.' }), read('593', undefined, 'running')]}
      jobStatus="running"
      startedAt="2026-10-13T12:00:00Z"
    />);

    const live = screen.getByRole('region', { name: 'Готовим ответ' });
    expect(within(live).getByRole('note', { name: 'Вопрос автора' }).textContent).toBe('Как менялись отношения?');
    const lines = [...live.querySelectorAll('p')].slice(1).map((line) => line.textContent);
    expect(lines).toEqual([`Читаю: ${place593}`, '0:42 · обычно 1–2 минуты · можно уйти — ответ сохранится']);
    // Screen readers hear what the helper does, not the clock ticking every second.
    expect([...live.querySelectorAll('[aria-live]')].map((node) => node.textContent)).toEqual([`Читаю: ${place593}`]);
    // A running line says what the helper does now (CW1.1); no count of «действия».
    expect(document.querySelector('li[data-status="running"]')!.textContent).toBe(`Читаю: ${place593} — абзацы 16–66 (выполняется)`);
    expect(document.body.textContent).not.toMatch(/действи|Ход работы/u);

    act(() => vi.advanceTimersByTime(140_000));
    expect(live.textContent).toContain('3:02 · вопрос большой: такое занимает до 15 минут · можно уйти — ответ сохранится');
  });

  // The incident of 11.11: the models answered slowly, and for five minutes the line said «обычно 1–2 минуты» over «Начинаю».
  it('says the service answers slower than usual when the helper has not begun for a minute', () => {
    vi.useFakeTimers({ now: Date.parse('2026-10-13T12:00:20Z') });
    render(<ChatFeed events={[event('user', { text: 'Вопрос' })]} jobStatus="running" startedAt="2026-10-13T12:00:00Z" />);
    const live = screen.getByRole('region', { name: 'Готовим ответ' });
    expect(live.textContent).toContain('0:20 · обычно 1–2 минуты · можно уйти — ответ сохранится');

    act(() => vi.advanceTimersByTime(45_000));
    expect(live.textContent).toContain('1:05 · помощник ещё не начал: сервис отвечает медленнее обычного · можно уйти — ответ сохранится');
    expect(live.textContent).not.toContain('обычно 1–2 минуты');
  });

  it('keeps the usual words once the helper works, however long the first step took', () => {
    vi.useFakeTimers({ now: Date.parse('2026-10-13T12:01:30Z') });
    render(<ChatFeed
      events={[event('user', { text: 'Вопрос' }), event('assistant', { text: 'Ищу.' }), read('593', undefined, 'running')]}
      jobStatus="running"
      startedAt="2026-10-13T12:00:00Z"
    />);
    expect(screen.getByRole('region', { name: 'Готовим ответ' }).textContent).toContain('1:30 · обычно 1–2 минуты · можно уйти — ответ сохранится');
  });

  it('shows no time when the clock of the browser disagrees with the server', () => {
    vi.useFakeTimers({ now: Date.parse('2026-10-13T12:00:00Z') });
    const { rerender } = render(<ChatFeed events={[event('user', { text: 'Вопрос' })]} jobStatus="running" startedAt="2026-10-13T12:05:00Z" />);
    expect(screen.getByRole('region', { name: 'Готовим ответ' }).textContent).toContain('Начинаюобычно 1–2 минуты · можно уйти — ответ сохранится');
    rerender(<ChatFeed events={[event('user', { text: 'Вопрос' })]} jobStatus="running" startedAt="2026-10-11T12:00:00Z" />);
    expect(screen.getByRole('region', { name: 'Готовим ответ' }).textContent).not.toMatch(/\d+:\d\d/u);
  });

  it('folds a long question of a running turn to three lines', () => {
    const long = Array.from({ length: 5 }, (_, index) => `Строка ${index + 1} вопроса.`).join('\n');
    render(<ChatFeed events={[event('user', { text: long })]} jobStatus="running" />);
    const question = screen.getByRole('note', { name: 'Вопрос автора' });
    const text = within(question).getByText(/^Строка 1 вопроса/u);
    expect(text.className).toMatch(/folded/u);
    expect(text.dataset.lines).toBe('3');
    fireEvent.click(within(question).getByRole('button', { name: 'Показать полностью' }));
    expect(within(question).getByText(/^Строка 1 вопроса/u).className).not.toMatch(/folded/u);
  });

  // CW2.4: «Остановить» is a word in the status of the turn.
  it('stops the turn from its status and then says it stops', () => {
    const onStop = vi.fn();
    const events = [event('user', { text: 'Вопрос' }), event('assistant', { text: 'Ищу.' })];
    const { rerender } = render(<ChatFeed events={events} jobStatus="running" stop={{ canStop: true, stopping: false, onStop }} />);
    const live = screen.getByRole('region', { name: 'Готовим ответ' });
    fireEvent.click(within(live).getByRole('button', { name: 'Остановить' }));
    expect(onStop).toHaveBeenCalledOnce();
    rerender(<ChatFeed events={events} jobStatus="running" stop={{ canStop: true, stopping: true, onStop }} />);
    expect(live.querySelector('[aria-live]')!.textContent).toBe('Останавливаю…');
    expect(live.textContent).not.toContain('можно уйти');
    rerender(<ChatFeed events={events} jobStatus="running" />);
    expect(within(live).queryByRole('button', { name: 'Остановить' })).toBeNull();
  });

  // CW2.5: the place in the queue and no time in minutes the server does not count.
  it('says the place of a waiting question without a usual time', () => {
    render(<ChatFeed events={[]} jobStatus="queued" pendingQuestion="Где Шорох?" queuePosition={2} />);
    const live = screen.getByRole('region', { name: 'Готовим ответ' });
    expect(live.querySelector('[aria-live]')!.textContent).toBe('Перед вашим вопросом ещё 2 — начну, когда освободится помощник');
    expect(live.textContent).toContain('можно уйти — ответ сохранится');
    expect(live.textContent).not.toMatch(/обычно|минут/u);
  });

  // CW2.3: the last three lines and «… ещё N раньше», which opens the whole work.
  it('shows the last three lines of a running turn and opens the rest on the author\'s click', () => {
    const events = [
      event('user', { text: 'Вопрос' }),
      event('assistant', { text: 'Ищу линию.' }),
      ...['а', 'б', 'в', 'г'].map((query) => event('tool_result', { outcome: 'completed', tool: { namespace: 'saga', name: 'search', arguments: { queries: [query] }, status: 'completed', success: true } })),
      read('593', undefined, 'running'),
    ];
    render(<ChatFeed events={events} jobStatus="running" />);
    const lines = () => [...document.querySelectorAll('li[data-kind]')].map((line) => line.textContent);
    expect(lines()).toEqual(['Искал «в»', 'Искал «г»', `Читаю: ${place593} — абзацы 16–66 (выполняется)`]);
    expect(screen.queryByText('Ищу линию.')).toBeNull();
    const earlier = screen.getByRole('button', { name: '… ещё 3 раньше' });
    expect(earlier.getAttribute('aria-expanded')).toBe('false');
    fireEvent.click(earlier);
    expect(lines()).toHaveLength(5);
    expect(screen.getByText('Ищу линию.')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Свернуть начало' }));
    expect(lines()).toHaveLength(3);
  });

  it('offers nothing earlier while a running turn has three lines or fewer', () => {
    render(<ChatFeed events={[event('user', { text: 'Вопрос' }), event('assistant', { text: 'Ищу.' }), read('593', undefined, 'running')]} jobStatus="running" />);
    expect(screen.queryByRole('button', { name: /раньше/u })).toBeNull();
  });

  // CW2.6: «Сообщить, когда будет готово» stands beside the time where the page watches its turns.
  it('offers the notice of the browser only where the page watches the turn', () => {
    const { rerender } = render(<ChatFeed events={[event('user', { text: 'Вопрос' })]} jobStatus="running" />);
    expect(screen.queryByRole('button', { name: /Сообщить/u })).toBeNull();
    rerender(<ChatFeed events={[event('user', { text: 'Вопрос' })]} jobStatus="running" notify />);
    const live = screen.getByRole('region', { name: 'Готовим ответ' });
    const toggle = within(live).getByRole('button', { name: 'Сообщить, когда будет готово' });
    // It stands on the line of the time, beside «можно уйти».
    expect(toggle.closest('p')!.textContent).toContain('можно уйти — ответ сохранится');
  });

  it('opens the chapter a read returned beside the chat', () => {
    const onOpenSource = vi.fn();
    render(<ChatFeed
      events={[event('user', { text: 'Вопрос' }), event('assistant', { text: 'Читаю.' }), read('593', 'Том 12, Глава 593. Информация – сила — гл. 593, абзацы ¶16–¶66 из 75')]}
      jobStatus="running"
      onOpenSource={onOpenSource}
    />);

    fireEvent.click(screen.getByRole('button', { name: `Прочитал: ${place593}` }));
    expect(onOpenSource).toHaveBeenCalledWith({ chapterKey: '593', from: 16, to: 66 }, expect.any(HTMLButtonElement));
  });

  // specs/2026-11-10-ux1-names.md N2.4 (Ч-13): a step the helper corrected itself is an ordinary line — not an alarm,
  // not a link, and without the key of the chapter or the message written for the model.
  it('shows a step that did not work as an ordinary line without a key or a link', () => {
    const onOpenSource = vi.fn();
    render(<ChatFeed
      events={[
        event('user', { text: 'Что в последней главе?' }),
        event('assistant', { text: 'Читаю.' }),
        event('tool_result', {
          outcome: 'failed',
          tool: { namespace: 'saga', name: 'read', arguments: { chapter: 'b9c99' }, status: 'failed', success: false,
            summary: 'глава "b9c99" не найдена; используйте сквозной номер главы («481») или ключ из результатов search/chapters' },
        }),
        event('assistant', { text: answer }),
        event('turn_outcome', { outcome: 'completed' }),
      ]}
      jobStatus="succeeded"
      onOpenSource={onOpenSource}
    />);
    const row = screen.getByText('Не нашёл такую главу — ищу иначе').closest('li')!;
    expect(row.textContent).toBe('Не нашёл такую главу — ищу иначе (не получилось)');
    expect(within(row).queryByRole('button')).toBeNull();
    expect(document.body.textContent).not.toMatch(/b\d+c\d+|не найдена/u);
  });

  // N2.3: a link a button of the product wrote into the question reads as the place it names; the answer names its
  // citations by the places the server wrote beside their keys.
  it('reads the links of a question and of an answer as places of the book', () => {
    render(<ChatFeed
      events={[
        event('user', { text: 'Спросить об этом: [т. 2, «Белый порт» ¶3](source:b2c12/3) — кто там?', places: { b2c12: 'Книга 2, гл. 12 „Белый порт“' } }),
        event('assistant', { text: 'Там Мара [т. 2, «Белый порт» ¶3–4](source:b2c12/3-4).', places: { b2c12: 'Книга 2, гл. 12 „Белый порт“' } }),
        event('turn_outcome', { outcome: 'completed' }),
      ]}
      jobStatus="succeeded"
      renderSourceLink={(_event, _source, label) => <button type="button">{label}</button>}
    />);
    expect(screen.getByRole('note', { name: 'Вопрос автора' }).textContent).toBe('Спросить об этом: Книга 2, гл. 12 „Белый порт“, абзац 3 — кто там?');
    expect(screen.getByRole('button', { name: 'Книга 2, гл. 12 „Белый порт“, абзацы 3–4' })).toBeTruthy();
    expect(document.body.textContent).not.toMatch(/b\d+c\d+|¶|\]\(source:/u);
  });

  it('shows a question that has not reached the timeline yet as a starting turn', () => {
    const { rerender } = render(<ChatFeed events={[]} jobStatus="queued" pendingQuestion="Где впервые появляется Шорох?" />);
    expect(screen.getByRole('note', { name: 'Вопрос автора' }).textContent).toBe('Где впервые появляется Шорох?');
    expect(screen.getByRole('region', { name: 'Готовим ответ' }).textContent).toContain('Начинаю');

    rerender(<ChatFeed events={[]} jobStatus="succeeded" pendingQuestion="Где впервые появляется Шорох?" />);
    expect(screen.queryByRole('region', { name: 'Готовим ответ' })).toBeNull();
  });

  // The owner 04.11: the question just sent stands under the earlier turns. A turn without its end stays the live one:
  // a text that differs from the question sent must not take the running turn for a past one.
  it('never takes the running turn for a past one when the question sent reads differently', () => {
    render(<ChatFeed events={[event('user', { text: 'Вопрос как его прочитал сервер' }), event('assistant', { text: answer })]} jobStatus="running" pendingQuestion="Вопрос как его отправили" />);
    expect(screen.getAllByRole('note', { name: 'Вопрос автора' }).map((note) => note.textContent)).toEqual(['Вопрос как его прочитал сервер']);
  });

  it('keeps a question just sent under finished turns until its turn reaches the timeline', () => {
    render(<ChatFeed events={[event('user', { text: 'Прошлый вопрос' }), event('assistant', { text: answer }), event('turn_outcome', { outcome: 'completed' })]} jobStatus="queued" pendingQuestion="Новый вопрос" />);
    expect(screen.getAllByRole('note', { name: 'Вопрос автора' }).map((note) => note.textContent)).toEqual(['Прошлый вопрос', 'Новый вопрос']);
    expect(screen.getByRole('region', { name: 'Готовим ответ' }).textContent).toContain('Начинаю');
  });

  // The owner's snapshot 03.11: a long question of the author takes a few lines until the author opens it whole.
  it('folds a long question to a few lines and opens it whole on the author\'s click', () => {
    const long = Array.from({ length: 10 }, (_, index) => `Строка ${index + 1} вопроса.`).join('\n');
    render(<ChatFeed events={[event('user', { text: long }), event('assistant', { text: answer })]} jobStatus="succeeded" />);
    const question = screen.getByRole('note', { name: 'Вопрос автора' });
    expect(within(question).getByText(/^Строка 1 вопроса/u).className).toMatch(/folded/u);
    fireEvent.click(within(question).getByRole('button', { name: 'Показать полностью' }));
    expect(within(question).getByText(/^Строка 1 вопроса/u).className).not.toMatch(/folded/u);
    expect(within(question).getByRole('button', { name: 'Свернуть' })).toBeTruthy();
    render(<ChatFeed events={[event('user', { text: 'Где Шорох?' })]} jobStatus="succeeded" />);
    expect(screen.getAllByRole('note', { name: 'Вопрос автора' })[1]!.querySelector('button')).toBeNull();
  });

  // specs/2026-11-10-ux4-chat-waiting.md CW2.8: a stopped turn says what happened and the count as it is (a stop gives no
  // unit back, quota spec Q2.4); «Повторить вопрос» puts the question into the field. No alarm: the author stopped it.
  it('keeps the steps of a stopped turn open, says the question is counted and offers to repeat it', () => {
    const onRepeatQuestion = vi.fn();
    render(<ChatFeed
      events={[event('user', { text: 'Вопрос' }), event('assistant', { text: 'Читаю главу.' }), read('593'), event('turn_outcome', { outcome: 'interrupted' })]}
      jobStatus="canceled"
      onRepeatQuestion={onRepeatQuestion}
    />);

    expect(screen.getByText('Как я искал').closest('details')!.open).toBe(true);
    const end = screen.getByRole('status', { name: 'Ответ остановлен' });
    expect(end.textContent).toBe('Ответ остановленВы остановили ответ. Остановленный вопрос засчитан.Повторить вопрос');
    expect(screen.queryByRole('alert')).toBeNull();
    fireEvent.click(within(end).getByRole('button', { name: 'Повторить вопрос' }));
    expect(onRepeatQuestion).toHaveBeenCalledWith('Вопрос');
  });

  it('says how an earlier turn ended in a quiet line', () => {
    render(<ChatFeed
      events={[
        event('user', { text: 'Первый' }), event('assistant', { text: 'Читаю.' }), event('turn_outcome', { outcome: 'failed' }),
        event('user', { text: 'Второй' }), event('assistant', { text: answer }), event('turn_outcome', { outcome: 'completed' }),
      ]}
      jobStatus="succeeded"
    />);
    expect(screen.getByRole('status').textContent).toBe('Помощник не смог ответить на этот вопрос.');
    expect(screen.queryByRole('alert')).toBeNull();
  });

  // TEST 28.10: a turn cut by a restart kept «В заметки» under its partial step.
  it('offers «В заметки» only under an answer, not under a turn that got none', () => {
    const renderActions = vi.fn(() => <button type="button">В заметки</button>);
    const partial = 'Нашёл, где Мирон впервые встречает мать.\n\nСверяю следующую главу.';
    const question = event('user', { text: 'Что известно о матери?' });
    const remark = event('assistant', { text: partial });
    // The restart left the native turn without an outcome; only the failed Job says it ended.
    const { rerender } = render(<ChatFeed events={[question, remark]} jobStatus="failed" renderAssistantActions={renderActions} />);

    expect(screen.getByText(/Нашёл, где Мирон/u)).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'В заметки' })).toBeNull();
    // CW2.8: a helper without a block of its page says it with the common block of an error and the count.
    const failure = screen.getByRole('alert', { name: 'Помощник не смог ответить' });
    expect(failure.textContent).toContain('Ответ прервался. Этот вопрос не засчитан.');

    rerender(<ChatFeed
      events={[question, remark, event('turn_outcome', { outcome: 'failed', text: 'native research turn did not complete' })]}
      jobStatus="failed"
      renderAssistantActions={renderActions}
    />);
    expect(screen.queryByRole('button', { name: 'В заметки' })).toBeNull();
    expect(screen.getAllByRole('alert')).toHaveLength(1);
    expect(renderActions).not.toHaveBeenCalled();
  });
});

// specs/2026-11-10-ux4-chat-waiting.md CW1.3: the feeds of the helpers of «Письмо» and «Рисование» are the same feed in
// the same voice; a message to a helper is no question.
describe('ChatFeed of a helper', () => {
  const draft = (name: string, args: Record<string, unknown>, summary?: string) => event('tool_result', {
    outcome: 'completed', tool: { namespace: 'draft', name, arguments: args, status: 'completed', success: true, ...(summary ? { summary } : {}) },
  });
  it('folds the work behind «Как я работал» and speaks for the helper', () => {
    render(<ChatFeed helper jobStatus="succeeded" events={[
      event('user', { text: 'Сверь сцену с книгой.' }), event('assistant', { text: 'Сверяю.' }),
      draft('state', {}), draft('notes', { kind: 'canon', items: [] }, 'Записано расхождений: 2; автор видит их у мест в тексте.'),
      event('assistant', { text: answer }), event('turn_outcome', { outcome: 'completed' }),
    ]} />);
    const work = screen.getByText('Как я работал').closest('details')!;
    expect([...work.querySelectorAll('li[data-kind]')].map((line) => line.textContent)).toEqual(['Прочитал черновик', 'Сверил с книгой — 2 расхождения']);
    expect(document.body.textContent).not.toMatch(/Как я искал|Черновик|Сверка с каноном/u);
  });

  it('says a stopped message of a helper is counted, with no «Остановить» of its own in the feed', () => {
    const { rerender } = render(<ChatFeed helper jobStatus="running" events={[event('user', { text: 'Сверь.' })]} />);
    expect(screen.queryByRole('button', { name: 'Остановить' })).toBeNull();
    expect(screen.queryByRole('button', { name: /Сообщить/u })).toBeNull();
    rerender(<ChatFeed helper jobStatus="canceled" events={[event('user', { text: 'Сверь.' }), event('assistant', { text: 'Читаю.' }), event('turn_outcome', { outcome: 'interrupted' })]} />);
    expect(screen.getByRole('status', { name: 'Ответ остановлен' }).textContent).toContain('Остановленное сообщение засчитано.');
  });

  // specs/2026-11-13-desk-tidy.md TD2.3: a helper names a place of the book by a link, and its feed — the feed of the chat —
  // shows it as the place the server wrote beside the key, with the paragraphs in words. Its page opens no source, so the
  // place stands as words, without the hint of a broken link.
  it('names a place of the book in an answer of a helper by its place, not by «¶» and a key', () => {
    const whitePort = 'Книга 2, гл. 12 „Белый порт“';
    const { container } = render(<ChatFeed helper jobStatus="succeeded" events={[
      event('user', { text: 'Что ты знаешь из книги о героях главы?' }),
      event('assistant', { phase: 'final_answer', places: { b2c12: whitePort }, text: 'Мара и Ольм впервые встречаются в [«Белый порт», ¶1–3](source:b2c12/1-3).' }),
      event('turn_outcome', { outcome: 'completed' }),
    ]} />);
    const answerSection = container.querySelector('[data-answer]')!;
    expect(answerSection.textContent).toBe(`Мара и Ольм впервые встречаются в ${whitePort}, абзацы 1–3.`);
    expect(answerSection.textContent).not.toMatch(/¶|b\d+c\d+/u);
    expect(screen.queryByTitle('Место недоступно')).toBeNull();
  });
});

// specs/2026-10-30-chat-steering.md UC3.
// specs/2026-11-10-ux1-chat.md CH4: what the helper remembered is seen without opening the work of the turn.
describe('ChatFeed «Запомнил»', () => {
  const remember = (text: string, status: 'running' | 'done' = 'done') => event(status === 'running' ? 'tool_request' : 'tool_result', {
    outcome: status === 'done' ? 'completed' : undefined,
    tool: {
      namespace: 'saga', name: 'remember', arguments: { kind: 'correction', text },
      ...(status === 'done' ? { status: 'completed', success: true, summary: 'Запись сохранена в памяти автора: поправка.' } : {}),
    },
  });
  const undo = vi.fn(() => <button type="button">Отменить</button>);

  afterEach(() => undo.mockClear());

  it('stands under the answer of a finished turn, outside the folded work', () => {
    render(<ChatFeed
      events={[
        event('user', { text: 'Запомни: Мара не знает, кто поджёг склад.' }),
        event('assistant', { text: 'Записываю поправку.' }),
        remember('Мара не знает, кто поджёг склад'),
        event('assistant', { text: answer }),
        event('turn_outcome', { outcome: 'completed' }),
      ]}
      jobStatus="succeeded"
      renderActionRemembered={undo}
    />);
    const work = screen.getByText('Как я искал').closest('details')!;
    expect(work.open).toBe(false);
    const kept = screen.getByRole('list', { name: 'Помощник запомнил' });
    expect(work.contains(kept)).toBe(false);
    expect(within(kept).getByText('Запомнил: «Мара не знает, кто поджёг склад»')).toBeTruthy();
    const answerSection = screen.getByText(/Мать исчезла/u).closest('[data-answer]')!;
    expect(answerSection.compareDocumentPosition(kept) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    // One «Отменить» of the record: under the answer, not in the steps.
    expect(screen.getAllByRole('button', { name: 'Отменить' })).toHaveLength(1);
    expect(within(kept).getByRole('button', { name: 'Отменить' })).toBeTruthy();
    expect(within(work).getByText('Запомнил: «Мара не знает, кто поджёг склад»')).toBeTruthy();
  });

  it('stands under the steps of a turn that still works and names only what is kept', () => {
    render(<ChatFeed
      events={[
        event('user', { text: 'Запомни две вещи.' }),
        event('assistant', { text: 'Записываю.' }),
        remember('Кире двадцать лет'),
        remember('Сава левша', 'running'),
      ]}
      jobStatus="running"
      renderActionRemembered={undo}
    />);
    const kept = screen.getByRole('list', { name: 'Помощник запомнил' });
    expect(within(kept).getAllByRole('listitem')).toHaveLength(1);
    expect(within(kept).getByText('Запомнил: «Кире двадцать лет»')).toBeTruthy();
    const steps = screen.getByRole('region', { name: 'Готовим ответ' });
    expect(steps.contains(kept)).toBe(false);
    expect(steps.compareDocumentPosition(kept) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it('shows nothing under a turn that remembered nothing', () => {
    render(<ChatFeed events={[event('user', { text: 'Что известно о матери?' }), event('assistant', { text: answer }), event('turn_outcome', { outcome: 'completed' })]}
      jobStatus="succeeded" renderActionRemembered={undo} />);
    expect(screen.queryByRole('list', { name: 'Помощник запомнил' })).toBeNull();
    expect(undo).not.toHaveBeenCalled();
  });
});

describe('ChatFeed corrections', () => {
  it('keeps a correction inside the fold of a finished turn and counts it apart from the steps', () => {
    render(<ChatFeed
      events={[
        event('user', { text: 'Нарисуй обложку' }),
        event('assistant', { text: 'Читаю главу.' }),
        read('593', 'Том 12, Глава 593'),
        event('steer', { text: 'Без дракона', steerId: 'steer-1' }),
        event('assistant', { text: answer }),
        event('turn_outcome', { outcome: 'completed' }),
      ]}
      jobStatus="succeeded"
    />);
    expect(screen.getAllByRole('note', { name: 'Вопрос автора' })).toHaveLength(1);
    expect(screen.getByText('Как я искал').closest('summary')!.textContent).toBe('Как я искал · прочитал 1 главу · 1 ваше уточнение');
    const correction = screen.getByRole('note', { name: 'Уточнение автора', hidden: true });
    expect(correction.closest('details')).toBeTruthy();
    expect(within(correction).getByText('Без дракона')).toBeTruthy();
    expect(within(correction).queryByText('Будет учтено на следующем шаге')).toBeNull();
  });

  it('shows the images of a correction with the «фото N» it was sent with', () => {
    const renderSteerImages = vi.fn(() => <span>фото</span>);
    render(<ChatFeed
      events={[event('user', { text: 'Нарисуй обложку' }), event('steer', { text: 'Лицо с фото 3', images: [90], firstPhoto: 3 })]}
      jobStatus="running"
      renderSteerImages={renderSteerImages}
    />);
    expect(renderSteerImages).toHaveBeenCalledWith([90], 3);
  });

  it('never shows the native input of a correction: only what the server read out of it', () => {
    render(<ChatFeed events={[event('user', { text: 'Вопрос' }), event('steer', { text: 'Без дракона' })]} jobStatus="running" />);
    expect(screen.queryByText(/attachedPhotos|correction/u)).toBeNull();
  });

  it('shows what the server took under the live turn until the timeline has it', () => {
    render(<ChatFeed
      events={[event('user', { text: 'Нарисуй обложку' }), event('assistant', { text: 'Читаю главу.' })]}
      jobStatus="running"
      pendingSteers={[{ steerId: 'steer-1', text: 'Без дракона', images: [] }]}
    />);
    expect(within(screen.getByRole('note', { name: 'Уточнение автора' })).getByText('Будет учтено на следующем шаге')).toBeTruthy();
  });

  // specs/2026-11-10-ux4-chat-waiting.md CW2.7: it stands under the answer, outside the folded work.
  it('says a correction the end of the turn outran was not read and offers to ask it', () => {
    const onAskSteer = vi.fn();
    render(<ChatFeed
      events={[
        event('user', { text: 'Нарисуй обложку' }),
        event('assistant', { text: answer }),
        event('steer', { text: 'Без дракона' }),
        event('turn_outcome', { outcome: 'completed' }),
      ]}
      jobStatus="succeeded"
      onAskSteer={onAskSteer}
    />);
    const late = screen.getByRole('note', { name: 'Уточнение автора' });
    expect(late.closest('details')).toBeNull();
    expect(screen.getByText(/^Без дракона$/u).closest('[data-answer]')).toBeNull();
    // specs/2026-11-11-ux9-shelf-chat.md SC3.1 (Ч-26): it is counted, and asking it anew costs one more question of the day.
    expect(within(late).getByText('Не успел прочесть · уточнение засчитано. Задать его новым вопросом — ещё один вопрос из дневных.')).toBeTruthy();
    fireEvent.click(within(late).getByRole('button', { name: 'Задать новым вопросом' }));
    expect(onAskSteer).toHaveBeenCalledWith('Без дракона');
  });

  // specs/2026-11-02-image-workshop.md IW7.3: `drawing:new` of an answer leads with the question of its turn.
  it('gives a link into the drawing workshop the question of its turn', () => {
    const renderWorkshopLink = vi.fn((_target: 'new' | number, label: unknown) => <button type="button">{label as string}</button>);
    render(<ChatFeed events={[
      event('user', { text: 'Нарисуй Мару на причале' }),
      event('assistant', { text: 'Рисунки делаются в мастерской рисунков. [Открыть мастерскую рисунков](drawing:new)' }),
      event('turn_outcome', { outcome: 'completed' }),
    ]} jobStatus="succeeded" renderWorkshopLink={renderWorkshopLink} />);
    expect(screen.getByRole('button', { name: 'Открыть мастерскую рисунков' })).toBeTruthy();
    expect(renderWorkshopLink).toHaveBeenCalledWith('new', expect.anything(), 'Нарисуй Мару на причале');
  });

  // specs/2026-11-11-ux9-shelf-chat.md SC3.4 (W-5A): the link carries the scene the helper described — the field of a new
  // drawing gets it, not the question of the author.
  it('gives the field of a new drawing the scene the link of the answer carries', () => {
    const renderWorkshopLink = vi.fn((_target: 'new' | number, label: unknown) => <button type="button">{label as string}</button>);
    render(<ChatFeed events={[
      event('user', { text: 'Когда Мара получила кинжал и можно ли это нарисовать?' }),
      event('assistant', { text: 'Кинжал Мара получает в главе 12. [Нарисовать в «Рисунках»](drawing:new "Мара на причале Белого порта принимает кинжал от старика, рассвет")' }),
      event('turn_outcome', { outcome: 'completed' }),
    ]} jobStatus="succeeded" renderWorkshopLink={renderWorkshopLink} />);
    expect(screen.getByRole('button', { name: 'Нарисовать в «Рисунках»' })).toBeTruthy();
    expect(renderWorkshopLink).toHaveBeenCalledWith('new', expect.anything(), 'Мара на причале Белого порта принимает кинжал от старика, рассвет');
  });
});


// specs/2026-11-11-ux9-writing-b.md WB3.3: in the feed of a helper a question a button of the page sent is a line of the
// press, not the author's bubble; his own words stay his question.
describe('ChatFeed, the presses of a page', () => {
  it('shows a press as a line and the author\'s words as his question', () => {
    render(<ChatFeed
      events={[
        event('user', { text: 'Написать главу', button: true }),
        event('assistant', { text: 'План на листе.' }),
        event('turn_outcome', { outcome: 'completed' }),
        event('user', { text: 'Пусть стражник врёт', providerTurnId: 'turn-2' }),
        event('assistant', { text: 'Поправил план.', providerTurnId: 'turn-2' }),
        event('turn_outcome', { outcome: 'completed', providerTurnId: 'turn-2' }),
      ]}
      helper
      jobStatus="succeeded"
      pressedLine={(text) => `Вы нажали «${text}»`}
    />);
    const [pressed, typed] = screen.getAllByRole('article', { name: 'Вопрос и ответ' });
    expect(within(pressed!).getByRole('note', { name: 'Нажатие кнопки' }).textContent).toBe('Вы нажали «Написать главу»');
    expect(within(pressed!).queryByRole('note', { name: 'Вопрос автора' })).toBeNull();
    expect(within(typed!).getByRole('note', { name: 'Вопрос автора' }).textContent).toContain('Пусть стражник врёт');
  });
});
