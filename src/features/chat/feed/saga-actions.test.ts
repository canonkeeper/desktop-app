import { describe, expect, it } from 'vitest';
import { describeSagaAction } from './saga-actions';

const saga = (name: string, args: Record<string, unknown> | undefined, summary?: string) => ({ namespace: 'saga', name, arguments: args, summary });

describe('describeSagaAction', () => {
  // specs/2026-11-01-writer-assistant-index.md, R4.2: what the agent saves in the memory is visible in the feed.
  it('shows what the agent saves in the memory of the author and what it reads', () => {
    const remember = describeSagaAction(saga('remember', { kind: 'plan', text: 'Кира узнает правду' }, 'Запись сохранена в памяти автора: план автора.'), 'done');
    // specs/2026-11-02-writer-workshop.md, Decision log 03.11: what is kept reads «Запомнил: …» and can be undone.
    expect(remember).toMatchObject({ kind: 'remember', label: 'Запомнил: «Кира узнает правду»', remembered: { kind: 'plan', text: 'Кира узнает правду' } });
    expect(describeSagaAction(saga('remember', { kind: 'plan', text: 'т' }), 'running').activity).toBe('Записывает в память автора');
    const failed = describeSagaAction(saga('remember', { kind: 'plan', text: 'т' }, 'Автор этого хода может только читать память.'), 'error');
    expect(failed.detail).toBe('Автор этого хода может только читать память.');
    expect(describeSagaAction(saga('memory', {}, 'Записей: 3. Это слова автора.'), 'done')).toMatchObject({ kind: 'memory', label: 'Память автора', detail: '3 записи' });
    expect(describeSagaAction(saga('memory', { kind: 'correction', query: 'Кира' }, 'Записей: 0. Ничего не найдено.'), 'done'))
      .toMatchObject({ label: 'Память автора · поправка · «Кира»', detail: 'записей нет' });
  });

  it('names what a search looked for and how much it found', () => {
    expect(describeSagaAction(saga('search', { queries: ['Шорох'], order: 'story' }, '«Шорох» (any): 56 абзацев в 23 главах'), 'done')).toEqual({
      kind: 'search', label: 'Поиск «Шорох»', activity: 'Ищет «Шорох»', detail: '56 абзацев в 23 главах',
    });
    expect(describeSagaAction(saga('search', { queries: ['Ворон Шорох', 'Шороха', 'Шорох', 'Ворон', 'Птица'] }, '«Ворон Шорох» (any): 5922 абзацев в 593 главах'), 'done')).toMatchObject({
      label: 'Поиск «Ворон Шорох», «Шороха», «Шорох» и ещё 2',
      detail: '«Ворон Шорох»: 5922 абзаца в 593 главах',
    });
    expect(describeSagaAction(saga('search', { queries: ['8500'], from: '590' }, '«8500» (any): 0 абзацев в 0 главах'), 'done')).toMatchObject({
      label: 'Поиск «8500» · с гл. 590', detail: 'ничего не найдено',
    });
    expect(describeSagaAction(saga('search', { queries: ['мать'], books: [2, 3], offset: 30 }, '«мать» (any): 41 абзац в 21 главе'), 'done')).toMatchObject({
      label: 'Поиск «мать» · тома 2, 3 · дальше с 31-го', detail: '41 абзац в 21 главе',
    });
  });

  it('turns a read into the chapter title, the paragraphs and a source to open', () => {
    expect(describeSagaAction(saga('read', { chapter: '593', around: 41, radius: 25 },
      'Том 12, Глава 593. Информация – сила — гл. 593, абзацы ¶16–¶66 из 75; ссылка на абзац: [гл. 593 ¶16](source:593/16)'), 'done')).toEqual({
      kind: 'read', label: 'Глава 593. Информация – сила', activity: 'Читает главу 593', detail: '¶16–66 из 75 · том 12',
      source: { chapterKey: '593', from: 16, to: 66 },
    });
    expect(describeSagaAction(saga('read', { chapter: '484', from: 12, to: 40 }), 'running')).toEqual({
      kind: 'read', label: 'Глава 484', activity: 'Читает главу 484', detail: '¶12–40', source: { chapterKey: '484', from: 12, to: 40 },
    });
    expect(describeSagaAction(saga('read', { chapter: '7' }), 'running')).toMatchObject({ label: 'Глава 7', source: { chapterKey: '7' } });
  });

  it('describes the chapter list, glossary, changes and scan by their arguments', () => {
    expect(describeSagaAction(saga('chapters', { from: '581', to: '617', detail: 'card' }, 'Глав: 37'), 'done')).toMatchObject({
      kind: 'chapters', label: 'Список глав · гл. 581–617 · с карточками', activity: 'Смотрит список глав', detail: '37 глав',
    });
    expect(describeSagaAction(saga('chapters', { books: [2] }, 'Глав: 1'), 'done')).toMatchObject({ label: 'Список глав · том 2', detail: '1 глава' });
    expect(describeSagaAction(saga('glossary', { query: 'Нэлла' }, 'Найдено записей: 9'), 'done')).toMatchObject({
      kind: 'glossary', label: 'Словарь: «Нэлла»', activity: 'Ищет в словаре «Нэлла»', detail: '9 записей',
    });
    expect(describeSagaAction(saga('glossary', { types: ['город', 'место'] }, 'Найдено записей: 21'), 'done')).toMatchObject({
      label: 'Словарь: город, место', activity: 'Смотрит словарь', detail: '21 запись',
    });
    expect(describeSagaAction(saga('glossary', {}, 'Типы записей: 14'), 'done')).toEqual({ kind: 'glossary', label: 'Словарь саги', activity: 'Смотрит словарь' });
    expect(describeSagaAction(saga('changes', { subject: 'Шорох', from: '535', to: '617' }, 'Изменений: 14 (по порядку книги)'), 'done')).toMatchObject({
      kind: 'changes', label: 'Изменения: «Шорох» · гл. 535–617', activity: 'Прослеживает изменения «Шорох»', detail: '14 изменений',
    });
    expect(describeSagaAction(saga('changes', { subject: 'Любава' }, 'Изменений не найдено. Попробуйте другое имя или search по тексту.'), 'done')).toMatchObject({ detail: 'ничего не найдено' });
    expect(describeSagaAction(saga('threads', { who: 'Мирон', level: 'all' }, 'Нитей: 12 (важных 3); слой собран по гл. 640, глав новее: 1.'), 'done')).toMatchObject({
      kind: 'threads', label: 'Нити саги · «Мирон» · с нитями книг', activity: 'Смотрит нити саги', detail: '12 нитей',
    });
    expect(describeSagaAction(saga('threads', { part: 'arcs' }, 'Арок: 1; слой собран по гл. 640, глав новее: 0.'), 'done')).toMatchObject({ label: 'Арки саги', detail: '1 арка' });
    expect(describeSagaAction(saga('threads', { query: 'меч', kind: 'ружьё' }, 'Нитей: 0 (важных 0); слой собран по гл. 2, глав новее: 0.'), 'done')).toMatchObject({ label: 'Нити саги · «меч» · ружьё', detail: 'ничего не найдено' });
    expect(describeSagaAction(saga('threads', {}, 'Нитей и арок пока нет: слой саги ещё не собран.'), 'done')).toMatchObject({ detail: 'слой ещё не собран' });
    expect(describeSagaAction(saga('scan', { task: 'Все места, где герой упоминает отца, и сцены, где о нём говорят другие персонажи саги', from: '596' },
      'Просмотрены главы: гл. 596–601, гл. 604–616. Найдено: 2 (мест: 2). Порядок — по первому появлению.'), 'done')).toEqual({
      kind: 'scan', label: 'Просмотр текста: «Все места, где герой упоминает отца, и сцены, где о нём говорят другие персонажи…» · с гл. 596',
      activity: 'Просматривает текст целиком', detail: 'найдено 2',
    });
  });

  it('shows the message of a failed call and falls back to the tool name without arguments', () => {
    expect(describeSagaAction(saga('chapters', { from: '590', to: '620' }, 'глава "620" не найдена; используйте сквозной номер главы'), 'error')).toMatchObject({
      detail: 'глава "620" не найдена; используйте сквозной номер главы',
    });
    expect(describeSagaAction({ namespace: 'saga', name: 'search' }, 'done')).toEqual({ kind: 'search', label: 'Поиск в тексте саги', activity: 'Ищет в тексте саги' });
    expect(describeSagaAction({ namespace: 'source', name: 'read' }, 'done')).toEqual({ kind: 'other', label: 'Работа с текстом', activity: 'Работает с текстом' });
  });
});

// specs/2026-10-29-saga-illustrations.md R1.6: a drawing action reads «Рисует: …» by its arguments.
const campfireScene = 'Мирон у костра. Ночь, лес, ворон на ветке; лицо освещено огнём.';
const rainScene = 'Мирон идёт ночью под дождём к замку';
const technicalCampfire = 'Средний общий план, ракурс в три четверти, камера на высоте лица. Мирон у костра.';
const shorterHair = 'волосы до плеч';

describe('drawing actions', () => {
  it('names what is drawn and which drawing the tool answered with', () => {
    expect(describeSagaAction(saga('draw', {
      prompt: campfireScene, subjects: ['Мирон', 'Ворон'], chapter: '300', kind: 'scene',
    }, 'Рисунок 42 готов: A young man by a campfire'), 'done')).toEqual({
      kind: 'draw', label: 'Рисует: Мирон у костра', activity: 'Рисует: Мирон у костра', detail: 'рисунок 42 готов',
      drawing: { tool: 'draw', subjects: ['Мирон', 'Ворон'], chapter: '300', image: 42 },
    });
    expect(describeSagaAction(saga('draw', { prompt: rainScene, subjects: ['Мирон'] }), 'running')).toMatchObject({
      label: 'Рисует: Мирон идёт ночью под дождём к замку', drawing: { tool: 'draw', subjects: ['Мирон'] },
    });
    // R8.17: a new drawing is named by the caption the agent gave it, not by the technical description.
    expect(describeSagaAction(saga('draw', {
      prompt: technicalCampfire, title: 'Мирон у костра ночью', subjects: ['Мирон'],
    }), 'running')).toMatchObject({ label: 'Рисует: Мирон у костра ночью' });
    expect(describeSagaAction(saga('draw', { prompt: shorterHair, title: 'Мирон у костра ночью', base_image: 42 }), 'running')).toMatchObject({
      label: 'Правит рисунок 42: волосы до плеч',
    });
    expect(describeSagaAction(saga('draw', { prompt: 'зелёные глаза', base_image: 40, region: { x: 0.4, y: 0.2, w: 0.2, h: 0.1 } },
      'Рисунок 40: правка области 1 готова — зелёные глаза'), 'done')).toMatchObject({
      label: 'Правит область рисунка 40: зелёные глаза', detail: 'слой 1 рисунка 40 готов', drawing: { image: 40 },
    });
  });

  it('describes looking at a drawing, a reference and a refused drawing', () => {
    expect(describeSagaAction(saga('image', { image: 42 }, 'Рисунок 42 рисуется (обычно 40–120 с).'), 'done')).toMatchObject({
      label: 'Смотрит рисунок 42', detail: 'рисунок 42 рисуется', drawing: { tool: 'image', image: 42 },
    });
    // R21.17: the variant the author named in words is chosen by the agent, and the feed says so.
    expect(describeSagaAction(saga('image', { image: 90, choose: true },
      'Рисунок 90 выбран автором: варианты 89, 91 удалены. Теперь это обычный рисунок: правьте только его.'), 'done')).toMatchObject({
      label: 'Выбирает вариант: рисунок 90', detail: 'вариант выбран', drawing: { tool: 'image', image: 90 },
    });
    expect(describeSagaAction(saga('reference', { image: 42, subject: 'Мирон', from_chapter: '300' }), 'done')).toMatchObject({
      label: 'Образец: Мирон с гл. 300', drawing: { tool: 'reference', image: 42 },
    });
    expect(describeSagaAction(saga('draw', { prompt: 'Казнь на площади' },
      'Модель изображений отказалась рисовать рисунок 44 по своим правилам: жестокость.'), 'error')).toMatchObject({
      label: 'Рисует: Казнь на площади', detail: 'Модель изображений отказалась рисовать рисунок 44 по своим правилам: жестокость.',
    });
  });

  // specs/2026-11-02-writer-workshop.md W4.5: the feed of the helper of a draft names what each of its tools did.
  it('describes the tools of the helper of a draft', () => {
    const draft = (name: string, args: Record<string, unknown>, summary?: string) => ({ namespace: 'draft', name, arguments: args, summary });
    expect(describeSagaAction(draft('state', {}, 'Черновик «Кузница», ревизия 4. Место: книга «Том».'), 'done')).toMatchObject({ label: 'Черновик' });
    expect(describeSagaAction(draft('state', {}, 'Черновик'), 'done').detail).toBeUndefined();
    expect(describeSagaAction(draft('brief', { items: [] }, 'Справка записана: 5 пунктов.'), 'done'))
      .toMatchObject({ label: 'Справка к главе', detail: 'Справка записана: 5 пунктов.' });
    expect(describeSagaAction(draft('notes', { kind: 'canon', target: 'draft', items: [] }), 'running').activity).toBe('Сверяет с каноном');
    expect(describeSagaAction(draft('notes', { kind: 'question', target: 'draft', items: [] }), 'done').label).toBe('Вопросы автору');
    expect(describeSagaAction(draft('variants', { items: [] }), 'running').activity).toBe('Предлагает варианты сцены');
    expect(describeSagaAction(draft('continue', { text: 'сцены' }, 'Дописано 300 слов между метками.'), 'done'))
      .toMatchObject({ label: 'Дописано по замыслу', detail: 'Дописано 300 слов между метками.' });
  });
});

// specs/2026-11-07-chat-attach-chapter.md CA3.2: reading the chapter the author attached opens it beside the chat.
describe('the reading of an attached chapter', () => {
  it('names the attachment and its paragraphs and leads to its text', () => {
    expect(describeSagaAction({ namespace: 'saga', name: 'attachment', arguments: { attachment: 1 } }, 'done')).toEqual({
      kind: 'read', label: 'Глава автора, приложение 1', activity: 'Читает приложенную главу', source: { chapterKey: 'a1' },
    });
    expect(describeSagaAction({ namespace: 'saga', name: 'attachment', arguments: { attachment: 2, from: 41, to: 80 } }, 'done')).toMatchObject({
      detail: '¶41–80', source: { chapterKey: 'a2', from: 41, to: 80 },
    });
    expect(describeSagaAction({ namespace: 'saga', name: 'attachment', arguments: { from: 7 } }, 'done')).toEqual({
      kind: 'read', label: 'Глава автора', activity: 'Читает приложенную главу', detail: 'с ¶7',
    });
    expect(describeSagaAction({ namespace: 'saga', name: 'attachment', arguments: {}, summary: 'такого приложения в этом чате нет' }, 'error'))
      .toMatchObject({ detail: 'такого приложения в этом чате нет' });
  });
});
