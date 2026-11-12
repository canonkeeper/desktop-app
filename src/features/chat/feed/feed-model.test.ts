import { describe, expect, it } from 'vitest';
import { buildFeedTurns, feedStatus, lastLines, workSummary } from './feed-model';
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
      [null, ['Что известно о „мать“', 'Искал «мать Мирона»']],
      ['Поиск захватил ругательства. Сначала найду имя матери в словаре.', ['Что известно о „Мать Мирона“']],
      ['Нашёл словарную карточку. Теперь прочитаю ключевые главы.', ['Прочитал: Глава 600. Лаборатория']],
    ]);
    // CW2.7: the fold counts the chapters read and the searches, not the calls.
    expect(workSummary(turn!)).toEqual(['прочитал 1 главу, искал 1 раз']);
  });

  it('keeps a running turn open and names what the agent is doing now', () => {
    // specs/2026-11-10-ux1-names.md N2.1: the server names the chapter beside its key, so the turn names it while it reads.
    const reading = { ...tool('read', { chapter: '593', around: 41, radius: 25 }, undefined, 'inProgress'), places: { 593: 'Книга 12, гл. 593 „Информация – сила“' } };
    const events = [user('Как менялись отношения Мирона и Борислава?'), reasoning(), say('Ищу линию Мирона и Борислава.'), reading];
    const [turn] = buildFeedTurns(events, true);
    expect(turn).toMatchObject({ live: true, activity: 'acting', current: { status: 'running', description: { activity: 'Читаю: Книга 12, гл. 593 „Информация – сила“' } } });
    expect(turn!.answer).toBeUndefined();
    expect(feedStatus(turn!)).toBe('Читаю: Книга 12, гл. 593 „Информация – сила“');
  });

  it('shows a long last remark without actions after it as the answer being written', () => {
    const writing = buildFeedTurns([user('Вопрос'), say('Ищу.'), tool('search', { queries: ['Любава'] }, '«Любава» (any): 3 абзаца в 2 главах'), say(answer)], true)[0]!;
    expect(writing).toMatchObject({ live: true, activity: 'writing', answer: { text: answer } });
    expect(writing.steps).toHaveLength(1);
    expect(feedStatus(writing)).toBe('Пишу ответ');

    const thinking = buildFeedTurns([user('Вопрос'), say('Ищу.'), tool('search', { queries: ['Любава'] }, '«Любава» (any): 3 абзаца в 2 главах'), say('Нашёл три места, проверю их.')], true)[0]!;
    expect(thinking).toMatchObject({ live: true, activity: 'thinking' });
    expect(thinking.answer).toBeUndefined();
    expect(thinking.steps.map((step) => step.event?.text ?? null)).toEqual(['Ищу.']);
    expect(thinking.tail).toMatchObject({ text: 'Нашёл три места, проверю их.' });
    expect(feedStatus(thinking)).toBe('Думаю, где искать дальше');
    expect(feedStatus(buildFeedTurns([user('Вопрос')], true)[0]!)).toBe('Начинаю');
    expect(feedStatus(buildFeedTurns([user('Вопрос'), reasoning()], true)[0]!)).toBe('Думаю над вопросом');
    // Ч-30: a stopped turn says so at once.
    expect(feedStatus(writing, null, true)).toBe('Останавливаю…');
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
    // N2.4: the step that did not work says so in the words of the author, without the message for the model.
    expect(turns[1]!.steps.map((step) => [step.event?.text, step.actions.map((action) => [action.status, action.description.label, action.description.detail])])).toEqual([
      ['Читаю главу.', [['error', 'Не нашёл такую главу — ищу иначе', undefined]]],
    ]);

    const interrupted = buildFeedTurns([user('Вопрос'), say('Ищу.'), say(answer), outcome('interrupted')], false)[0]!;
    expect(interrupted).toMatchObject({ answer: { text: answer }, outcome: { outcome: 'interrupted' } });
  });

  it('merges a request with its result and shows tools of the previous assistant by name', () => {
    const request = tool('search', { queries: ['Шорох'] }, undefined, 'inProgress');
    const result = { ...tool('search', { queries: ['Шорох'] }, '«Шорох» (any): 56 абзацев в 23 главах'), providerItemId: request.providerItemId };
    const [turn] = buildFeedTurns([user('Вопрос'), request, result, event('tool_result', { tool: { namespace: 'source', name: 'read', status: 'completed', success: true }, outcome: 'completed' })], true);
    expect(turn!.steps[0]!.actions.map((action) => [action.status, action.description.label, action.description.detail ?? null])).toEqual([
      ['done', 'Искал «Шорох»', '56 мест в 23 главах'],
      ['done', 'Поработал с текстом', null],
    ]);
    expect(turn!.activity).toBe('thinking');
  });
});

// quota spec R10.4; specs/2026-11-10-ux4-chat-waiting.md CW2.5: the place in the queue, no time in minutes.
describe('queue status', () => {
  it('shows the place of a waiting question until work starts', () => {
    const starting = buildFeedTurns([user('Вопрос')], true)[0]!;
    expect(feedStatus(starting, 0)).toBe('Ваш вопрос следующий — начну, когда освободится помощник');
    expect(feedStatus(starting, 1)).toBe('Перед вашим вопросом ещё 1 — начну, когда освободится помощник');
    expect(feedStatus(starting, 4)).toBe('Перед вашим вопросом ещё 4 — начну, когда освободится помощник');
    expect(feedStatus(starting, null)).toBe('Начинаю');
    expect(feedStatus(buildFeedTurns([user('Вопрос'), reasoning()], true)[0]!, 3)).toBe('Думаю над вопросом');
  });
});

// CW2.3: a running turn shows its last three lines and how many stand before them.
describe('the last lines of a running turn', () => {
  it('cuts the steps to the last three lines — a remark, a correction or an action each', () => {
    const events = [
      user('Вопрос'),
      say('Ищу.'), tool('search', { queries: ['а'] }, '«а» (any): 1 абзац в 1 главе'), tool('search', { queries: ['б'] }, '«б» (any): 1 абзац в 1 главе'),
      say('Читаю.'), tool('read', { chapter: '1' }), tool('read', { chapter: '2' }, undefined, 'inProgress'),
    ];
    const [turn] = buildFeedTurns(events, true);
    const tail = lastLines(turn!.steps, 3);
    expect(tail.hidden).toBe(3);
    expect(tail.steps.map((step) => [step.event?.text ?? null, step.actions.length])).toEqual([['Читаю.', 2]]);
    const four = lastLines(turn!.steps, 4);
    expect(four.steps.map((step) => [step.event?.text ?? null, step.actions.length])).toEqual([[null, 1], ['Читаю.', 2]]);
    expect(lastLines(turn!.steps, 10)).toEqual({ hidden: 0, steps: turn!.steps });
  });

  it('counts the work of a finished turn in its words', () => {
    const [turn] = buildFeedTurns([
      user('Вопрос'), say('Ищу.'),
      tool('search', { queries: ['а'] }, '«а» (any): 1 абзац в 1 главе'), tool('scan', { task: 'все места' }, 'Найдено: 2'),
      tool('read', { chapter: '1' }, 'Том 1, Глава 1. А — гл. 1, абзацы ¶1–¶4 из 4'), tool('read', { chapter: '1' }, 'Том 1, Глава 1. А — гл. 1, абзацы ¶1–¶2 из 4'),
      tool('read', { chapter: '2' }, 'Том 1, Глава 2. Б — гл. 2, абзацы ¶1–¶4 из 4'), tool('glossary', { query: 'а' }, 'Найдено записей: 1'),
      event('steer', { text: 'Короче' }), say(answer), outcome('completed'),
    ], false);
    expect(workSummary(turn!)).toEqual(['прочитал 2 главы, искал 2 раза', '1 ваше уточнение']);
    const [quiet] = buildFeedTurns([user('Вопрос'), say('Думаю.'), tool('glossary', { query: 'а' }, 'Найдено записей: 1'), say(answer), outcome('completed')], false);
    expect(workSummary(quiet!)).toEqual([]);
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
    expect(feedStatus(turn!)).toBe('Думаю, где искать дальше');
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

// specs/2026-11-11-ux4-after-answer.md AA1.2: the continuations the helper ends an answer with are buttons under it.
describe('the continuations of an answer', () => {
  const body = 'Сава — брат Мары [гл. 3 ¶2](source:3/2).\n\nОн ушёл из дома в главе 5.';
  const block = (...items: string[]) => `${body}\n\nДальше можно спросить:\n${items.map((item) => `- ${item}`).join('\n')}`;
  const finished = (text: string, options = { continuations: true }) => buildFeedTurns([user('Кто такой Сава?'), say(text), outcome('completed')], false, undefined, options)[0]!;

  it.each([
    [['А что знает Сава о пожаре?']],
    [['А что знает Сава о пожаре?', 'Сверь это с моей главой 17']],
    [['А что знает Сава о пожаре?', 'Сверь это с моей главой 17', 'Когда Сава вернулся домой?']],
  ])('splits the exact block of %j from the answer', (items) => {
    const turn = finished(block(...items));
    expect(turn.answer?.text).toBe(body);
    expect(turn.continuations).toEqual(items);
  });

  it('takes a numbered list and a blank line after the heading', () => {
    const turn = finished(`${body}\n\nДальше можно спросить:\n\n1. А что знает Сава?\n2. Где он сейчас?\n`);
    expect(turn.answer?.text).toBe(body);
    expect(turn.continuations).toEqual(['А что знает Сава?', 'Где он сейчас?']);
  });

  it.each([
    ['four items', block('Один?', 'Два?', 'Три?', 'Четыре?')],
    ['text after the list', `${block('А что знает Сава?')}\n\nЕсли нужно, сверю с главой.`],
    ['another heading', `${body}\n\nМожно спросить дальше:\n- А что знает Сава?`],
    ['a bold heading', `${body}\n\n**Дальше можно спросить:**\n- А что знает Сава?`],
    ['a long item', block(`А что знает Сава о ${'пожаре '.repeat(20)}?`)],
    ['a link in an item', block('Покажи [гл. 3](source:3)')],
    ['no item', `${body}\n\nДальше можно спросить:`],
    ['nothing but the block', 'Дальше можно спросить:\n- А что знает Сава?'],
  ])('keeps the answer whole with %s', (_name, text) => {
    const turn = finished(text);
    expect(turn.answer?.text).toBe(text);
    expect(turn.continuations).toBeUndefined();
  });

  it('does not split the answer of a helper of «Письмо» or «Рисование», a live or an unfinished turn', () => {
    const text = block('А что знает Сава?');
    expect(finished(text, { continuations: false }).answer?.text).toBe(text);
    expect(buildFeedTurns([user('Кто такой Сава?'), say(text)], false, undefined).at(0)?.continuations).toBeUndefined();
    const live = buildFeedTurns([user('Кто такой Сава?'), say(text)], true, undefined, { continuations: true })[0]!;
    expect(live.answer?.text).toBe(text);
    expect(live.continuations).toBeUndefined();
    const failed = buildFeedTurns([user('Кто такой Сава?'), say(text), outcome('failed')], false, undefined, { continuations: true })[0]!;
    expect(failed.continuations).toBeUndefined();
  });

  it('keeps the answer of an unchanged event the same object, so its text is not drawn anew', () => {
    const events = [user('Кто такой Сава?'), say(block('А что знает Сава?')), outcome('completed')];
    const first = buildFeedTurns(events, false, undefined, { continuations: true })[0]!;
    const second = buildFeedTurns([...events, user('Ещё')], false, undefined, { continuations: true })[0]!;
    expect(second.answer).toBe(first.answer);
    expect(second.continuations).toBe(first.continuations);
  });
});

// AA4.5 (Ч-53): the chapter of the author is named in the work of the turn by its title.
describe('the chapter of the author in the work', () => {
  it('reads «Читаю вашу главу „Брод“» while it reads and «Прочитал вашу главу „Брод“» once done', () => {
    const question = event('user', { text: 'Ложится ли на канон?', attachment: { number: 1, title: 'Брод', words: 2400 } });
    const reading = tool('attachment', { attachment: 1, from: 1, to: 12 }, undefined, 'inProgress');
    const [live] = buildFeedTurns([question, reading], true);
    expect(live!.current?.description.activity).toBe('Читаю вашу главу „Брод“');
    const [done] = buildFeedTurns([question, { ...reading, kind: 'tool_result', outcome: 'completed', tool: { ...reading.tool!, status: 'completed', success: true } }, say('Ложится.'), outcome('completed')], false);
    expect(done!.steps[0]!.actions[0]!.description.label).toBe('Прочитал вашу главу „Брод“');
  });
});
