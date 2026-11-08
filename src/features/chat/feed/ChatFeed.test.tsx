import { fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ChatFeed } from './ChatFeed';
import type { CanonicalAgentTimelineEvent } from './types';

let cursor = 0;
function event(kind: CanonicalAgentTimelineEvent['kind'], extra: Partial<CanonicalAgentTimelineEvent> = {}): CanonicalAgentTimelineEvent {
  cursor += 1;
  return { cursor: `tl_${cursor}`, id: `item-${cursor}:0`, providerItemId: `item-${cursor}`, providerTurnId: 'turn-1', jobId: 'job-1', kind, logicalSection: 0, role: 'main', ...extra };
}
const read = (chapter: string, summary?: string, status: 'running' | 'done' = 'done') => event(status === 'running' ? 'tool_request' : 'tool_result', {
  outcome: status === 'done' ? 'completed' : undefined,
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

    const work = screen.getByText('Ход работы').closest('details')!;
    expect(work.open).toBe(false);
    expect(work.querySelector('summary')!.textContent).toBe('Ход работы · 1 шаг · 1 действие');
    expect(within(work).getByText('Ищу её в словаре.')).toBeTruthy();
    expect(within(work).getByText('Глава 593. Информация – сила').closest('li')!.textContent).toBe('Глава 593. Информация – сила — ¶16–66 из 75 · том 12');
    const answerSection = screen.getByText(/Мать исчезла/u).closest('[data-answer]')!;
    expect(answerSection.compareDocumentPosition(work) & Node.DOCUMENT_POSITION_PRECEDING).toBeTruthy();
    expect(renderActions).toHaveBeenCalledTimes(1);
    expect(screen.getAllByRole('button', { name: 'В заметки' })).toHaveLength(1);
    expect(document.body.textContent).not.toContain('The user asks');
    expect(screen.queryByText('Рассуждения')).toBeNull();
  });

  it('shows what a live turn is doing now, for how long and how many actions it made', () => {
    vi.useFakeTimers({ now: Date.parse('2026-10-13T12:00:42Z') });
    render(<ChatFeed
      events={[event('user', { text: 'Как менялись отношения?' }), event('assistant', { text: 'Ищу линию.' }), read('593', undefined, 'running')]}
      jobStatus="running"
      startedAt="2026-10-13T12:00:00Z"
    />);

    const live = screen.getByRole('region', { name: 'Готовим ответ' });
    expect(within(live).getByText('Читает главу 593')).toBeTruthy();
    expect(live.textContent).toContain('0:42 · 1 действие');
    // Screen readers hear what the agent does, not the clock ticking every second.
    expect([...live.querySelectorAll('[aria-live]')].map((node) => node.textContent)).toEqual(['Читает главу 593']);
    expect(within(live).getByText('Глава 593').closest('li')!.textContent).toBe('Глава 593 — ¶16–66 (выполняется)');
    expect(screen.queryByText('Ход работы')).toBeNull();
  });

  it('folds the steps of a live turn while it works and keeps the remark it is writing', () => {
    const events = [event('user', { text: 'Как менялись отношения?' }), event('assistant', { text: 'Ищу линию.' }), read('593', 'Том 12, Глава 593. Информация – сила — гл. 593, абзацы ¶16–¶66 из 75')];
    const { rerender } = render(<ChatFeed events={events} jobStatus="running" />);
    const live = screen.getByRole('region', { name: 'Готовим ответ' });
    const fold = within(live).getByRole('button', { name: 'Свернуть ход работы' });
    expect(fold.getAttribute('aria-expanded')).toBe('true');
    const steps = document.getElementById(fold.getAttribute('aria-controls')!)!;
    expect(within(steps).getByText('Ищу линию.')).toBeTruthy();

    fireEvent.click(fold);
    expect(steps.hidden).toBe(true);
    expect(within(live).getByRole('button', { name: 'Развернуть ход работы' }).getAttribute('aria-expanded')).toBe('false');
    expect(within(live).getByText('1 действие')).toBeTruthy();

    // New steps arrive folded, while the remark the turn writes now stays in view.
    rerender(<ChatFeed events={[...events, event('assistant', { text: 'Сверяю вторую линию.' })]} jobStatus="running" />);
    expect(steps.hidden).toBe(true);
    expect(screen.getByText('Сверяю вторую линию.')).toBeTruthy();

    fireEvent.click(within(live).getByRole('button', { name: 'Развернуть ход работы' }));
    expect(steps.hidden).toBe(false);
  });

  it('offers no fold before a live turn has steps', () => {
    render(<ChatFeed events={[event('user', { text: 'Вопрос' })]} jobStatus="running" />);
    expect(screen.queryByRole('button', { name: 'Свернуть ход работы' })).toBeNull();
  });

  it('opens the chapter a read returned beside the chat', () => {
    const onOpenSource = vi.fn();
    render(<ChatFeed
      events={[event('user', { text: 'Вопрос' }), event('assistant', { text: 'Читаю.' }), read('593', 'Том 12, Глава 593. Информация – сила — гл. 593, абзацы ¶16–¶66 из 75')]}
      jobStatus="running"
      onOpenSource={onOpenSource}
    />);

    fireEvent.click(screen.getByRole('button', { name: 'Глава 593. Информация – сила' }));
    expect(onOpenSource).toHaveBeenCalledWith({ chapterKey: '593', from: 16, to: 66 }, expect.any(HTMLButtonElement));
  });

  it('shows a question that has not reached the timeline yet as a starting turn', () => {
    const { rerender } = render(<ChatFeed events={[]} jobStatus="queued" pendingQuestion="Где впервые появляется Шорох?" />);
    expect(screen.getByRole('note', { name: 'Вопрос автора' }).textContent).toBe('Где впервые появляется Шорох?');
    expect(screen.getByRole('region', { name: 'Готовим ответ' }).textContent).toContain('Начинает работу');

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
    expect(screen.getByRole('region', { name: 'Готовим ответ' }).textContent).toContain('Начинает работу');
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

  it('keeps the steps of a stopped turn open and says it stopped', () => {
    render(<ChatFeed
      events={[event('user', { text: 'Вопрос' }), event('assistant', { text: 'Читаю главу.' }), read('593'), event('turn_outcome', { outcome: 'interrupted' })]}
      jobStatus="canceled"
    />);

    expect(screen.getByText('Ход работы').closest('details')!.open).toBe(true);
    expect(screen.getByRole('status').textContent).toBe('Ответ остановлен.');
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
    expect(screen.getByRole('status').textContent).toBe('Ответ не получен.');

    rerender(<ChatFeed
      events={[question, remark, event('turn_outcome', { outcome: 'failed', text: 'native research turn did not complete' })]}
      jobStatus="failed"
      renderAssistantActions={renderActions}
    />);
    expect(screen.queryByRole('button', { name: 'В заметки' })).toBeNull();
    expect(screen.getByRole('status').textContent).toBe('Ответ не получен.');
    expect(renderActions).not.toHaveBeenCalled();
  });
});

// specs/2026-10-30-chat-steering.md UC3.
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
    expect(screen.getByText('· 1 шаг · 1 действие · 1 уточнение')).toBeTruthy();
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
    expect(within(late).getByText('Ответ закончился раньше, чем это уточнение было прочитано.')).toBeTruthy();
    fireEvent.click(within(late).getByRole('button', { name: 'Задать вопросом' }));
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
});

