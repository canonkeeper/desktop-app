import { describe, expect, it } from 'vitest';
import { actionCount, buildFeedTurns, feedStatus } from './feed-model';
import type { CanonicalAgentTimelineEvent } from './types';

let cursor = 0;
function event(kind: CanonicalAgentTimelineEvent['kind'], extra: Partial<CanonicalAgentTimelineEvent> = {}): CanonicalAgentTimelineEvent {
  cursor += 1;
  return { cursor: `tl_${cursor}`, id: `item-${cursor}:0`, providerItemId: `item-${cursor}`, jobId: 'job-1', kind, logicalSection: 0, role: 'main', ...extra };
}
const user = (text: string) => event('user', { text });
const reasoning = () => event('reasoning', { text: 'The user asks about the mother.' });
const say = (text: string) => event('assistant', { text });
const tool = (name: string, args: Record<string, unknown>, summary?: string, status = 'completed') => event(status === 'inProgress' ? 'tool_request' : 'tool_result', {
  outcome: status === 'inProgress' ? undefined : status === 'failed' ? 'failed' : 'completed',
  tool: { namespace: 'saga', name, arguments: args, summary, status: status === 'inProgress' ? undefined : status, success: status === 'completed' ? true : status === 'failed' ? false : undefined },
});
const outcome = (value: CanonicalAgentTimelineEvent['outcome']) => event('turn_outcome', { outcome: value });
const answer = 'Собрал обе линии по тексту.\n\n## Что знают герои\n\nМать исчезла, когда Мирону было двенадцать [гл. 1 ¶5](source:1/5).';

describe('buildFeedTurns', () => {
  it('turns a finished turn into the question, steps of the agent remarks with their actions and the answer', () => {
    const events = [
      user('Что известно о матери Мирона?'),
      reasoning(),
      tool('glossary', { query: 'мать' }, 'Найдено записей: 4'),
      tool('search', { queries: ['мать Мирона'] }, '«мать Мирона» (any): 12 абзацев в 9 главах'),
      reasoning(),
      say('Поиск захватил ругательства. Сначала найду имя матери в словаре.'),
      tool('glossary', { query: 'Мать Мирона' }, 'Найдено записей: 1'),
      reasoning(),
      say('Нашёл словарную карточку. Теперь прочитаю ключевые главы.'),
      tool('read', { chapter: '600' }, 'Том 12, Глава 600. Лаборатория — гл. 600, абзацы ¶1–¶40 из 40; ссылка на абзац: [гл. 600 ¶1](source:600/1)'),
      reasoning(),
      say(answer),
      outcome('completed'),
    ];
    const [turn] = buildFeedTurns(events, false);
    expect(turn).toMatchObject({ live: false, question: { text: 'Что известно о матери Мирона?' }, answer: { text: answer }, outcome: { outcome: 'completed' } });
    expect(turn!.steps.map((step) => [step.event?.text ?? null, step.actions.map((action) => action.description.label)])).toEqual([
      [null, ['Словарь: «мать»', 'Поиск «мать Мирона»']],
      ['Поиск захватил ругательства. Сначала найду имя матери в словаре.', ['Словарь: «Мать Мирона»']],
      ['Нашёл словарную карточку. Теперь прочитаю ключевые главы.', ['Глава 600. Лаборатория']],
    ]);
    expect(actionCount(turn!)).toBe(4);
  });

  it('keeps a running turn open and names what the agent is doing now', () => {
    const events = [user('Как менялись отношения Мирона и Борислава?'), reasoning(), say('Ищу линию Мирона и Борислава.'), tool('read', { chapter: '593', around: 41, radius: 25 }, undefined, 'inProgress')];
    const [turn] = buildFeedTurns(events, true);
    expect(turn).toMatchObject({ live: true, activity: 'acting', current: { status: 'running', description: { activity: 'Читает главу 593' } } });
    expect(turn!.answer).toBeUndefined();
    expect(feedStatus(turn!)).toBe('Читает главу 593');
  });

  it('shows a long last remark without actions after it as the answer being written', () => {
    const writing = buildFeedTurns([user('Вопрос'), say('Ищу.'), tool('search', { queries: ['Любава'] }, '«Любава» (any): 3 абзаца в 2 главах'), say(answer)], true)[0]!;
    expect(writing).toMatchObject({ live: true, activity: 'writing', answer: { text: answer } });
    expect(writing.steps).toHaveLength(1);
    expect(feedStatus(writing)).toBe('Пишет ответ');

    const thinking = buildFeedTurns([user('Вопрос'), say('Ищу.'), tool('search', { queries: ['Любава'] }, '«Любава» (any): 3 абзаца в 2 главах'), say('Нашёл три места, проверю их.')], true)[0]!;
    expect(thinking).toMatchObject({ live: true, activity: 'thinking' });
    expect(thinking.answer).toBeUndefined();
    expect(thinking.steps.map((step) => step.event?.text ?? null)).toEqual(['Ищу.']);
    expect(thinking.tail).toMatchObject({ text: 'Нашёл три места, проверю их.' });
    expect(feedStatus(thinking)).toBe('Обдумывает следующий шаг');
    expect(feedStatus(buildFeedTurns([user('Вопрос')], true)[0]!)).toBe('Начинает работу');
    expect(feedStatus(buildFeedTurns([user('Вопрос'), reasoning()], true)[0]!)).toBe('Обдумывает вопрос');
  });

  it('splits a chat into turns and keeps steps of a failed turn', () => {
    const turns = buildFeedTurns([
      user('Первый вопрос'), say('Ищу.'), tool('search', { queries: ['Любава'] }, '«Любава» (any): 3 абзаца в 2 главах'), say(answer), outcome('completed'),
      user('Второй вопрос'), say('Читаю главу.'), tool('read', { chapter: '620' }, 'глава "620" не найдена', 'failed'), outcome('failed'),
    ], false);
    expect(turns).toHaveLength(2);
    expect(turns[0]).toMatchObject({ question: { text: 'Первый вопрос' }, answer: { text: answer } });
    expect(turns[1]).toMatchObject({ live: false, question: { text: 'Второй вопрос' }, outcome: { outcome: 'failed' } });
    expect(turns[1]!.answer).toBeUndefined();
    expect(turns[1]!.steps.map((step) => [step.event?.text, step.actions.map((action) => [action.status, action.description.detail])])).toEqual([
      ['Читаю главу.', [['error', 'глава "620" не найдена']]],
    ]);

    const interrupted = buildFeedTurns([user('Вопрос'), say('Ищу.'), say(answer), outcome('interrupted')], false)[0]!;
    expect(interrupted).toMatchObject({ answer: { text: answer }, outcome: { outcome: 'interrupted' } });
  });

  it('merges a request with its result and shows tools of the previous assistant by name', () => {
    const request = tool('search', { queries: ['Шорох'] }, undefined, 'inProgress');
    const result = { ...tool('search', { queries: ['Шорох'] }, '«Шорох» (any): 56 абзацев в 23 главах'), providerItemId: request.providerItemId };
    const [turn] = buildFeedTurns([user('Вопрос'), request, result, event('tool_result', { tool: { namespace: 'source', name: 'read', status: 'completed', success: true }, outcome: 'completed' })], true);
    expect(turn!.steps[0]!.actions.map((action) => [action.status, action.description.label, action.description.detail ?? null])).toEqual([
      ['done', 'Поиск «Шорох»', '56 абзацев в 23 главах'],
      ['done', 'Работа с текстом', null],
    ]);
    expect(turn!.activity).toBe('thinking');
  });
});

describe('queue status (quota spec R10.4)', () => {
  it('shows the place of a waiting question until work starts', () => {
    const starting = buildFeedTurns([user('Вопрос')], true)[0]!;
    expect(feedStatus(starting, 0)).toBe('В очереди: ваш вопрос следующий');
    expect(feedStatus(starting, 1)).toBe('В очереди: перед вами 1 вопрос');
    expect(feedStatus(starting, 2)).toBe('В очереди: перед вами 2 вопроса');
    expect(feedStatus(starting, 5)).toBe('В очереди: перед вами 5 вопросов');
    expect(feedStatus(starting, null)).toBe('Начинает работу');
    expect(feedStatus(buildFeedTurns([user('Вопрос'), reasoning()], true)[0]!, 3)).toBe('Обдумывает вопрос');
  });
});

// specs/2026-10-30-chat-steering.md R3.2–R3.4, R3.6: a correction is a row of the steps of its turn.
describe('a correction in a turn', () => {
  const steer = (text: string, extra: Partial<CanonicalAgentTimelineEvent> = {}) => event('steer', { text, ...extra });

  it('does not start a turn and does not become the question or the answer', () => {
    const events = [
      user('Нарисуй обложку'),
      say('Читаю главу.'),
      tool('read', { chapter: '1' }, 'Том 1, Глава 1'),
      steer('Без дракона'),
      tool('search', { queries: ['дракон'] }, 'Найдено'),
      say(answer),
      outcome('completed'),
    ];
    const turns = buildFeedTurns(events, false);
    expect(turns).toHaveLength(1);
    expect(turns[0]).toMatchObject({ question: { text: 'Нарисуй обложку' }, answer: { text: answer } });
    expect(turns[0]!.steps.map((step) => [step.event?.text ?? null, step.steer?.text ?? null, step.actions.length])).toEqual([
      ['Читаю главу.', null, 1],
      [null, 'Без дракона', 1],
    ]);
    expect(turns[0]!.lateSteers).toEqual([]);
  });

  it('keeps the remark before it among the steps while the turn is live', () => {
    const [turn] = buildFeedTurns([user('Нарисуй обложку'), say('Читаю главу.'), steer('Без дракона')], true);
    expect(turn!.tail).toBeUndefined();
    expect(turn!.steps.map((step) => step.event?.text ?? step.steer?.text)).toEqual(['Читаю главу.', 'Без дракона']);
    expect(feedStatus(turn!)).toBe('Обдумывает следующий шаг');
  });

  it('says a correction the end of the turn outran was not read', () => {
    const [turn] = buildFeedTurns([user('Нарисуй обложку'), say(answer), steer('Без дракона'), outcome('completed')], false);
    expect(turn!.answer?.text).toBe(answer);
    expect(turn!.steps).toEqual([]);
    expect(turn!.lateSteers.map((late) => late.text)).toEqual(['Без дракона']);
  });

  it('keeps a correction of a live turn after the last remark among its steps', () => {
    const [turn] = buildFeedTurns([user('Нарисуй обложку'), say('Читаю.'), steer('Без дракона')], true);
    expect(turn!.lateSteers).toEqual([]);
  });
});
