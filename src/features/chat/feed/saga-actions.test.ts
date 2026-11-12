import { describe, expect, it } from 'vitest';
import { describeSagaAction, type ActionStatus } from './saga-actions';

const saga = (name: string, args: Record<string, unknown> | undefined, summary?: string) => ({ namespace: 'saga', name, arguments: args, summary });

/** The places the server writes beside the keys of an event (specs/2026-11-10-ux1-names.md N2.1). */
const places = {
  590: 'Книга 12, гл. 590 „Долг“', 593: 'Книга 12, гл. 593 „Информация – сила“', 617: 'Книга 13, гл. 617 „Вершина“',
  b2c12: 'Книга 2, гл. 12 „Белый порт“', b2c16: 'Книга 2, гл. 16 „Журнал Савы“', b2c10: 'Книга 2, гл. 10 „Дым“',
  b1c1: 'Книга 1, гл. 1 „Туманный берег“',
};
/** What the author never reads in a line of the work (specs/2026-11-10-ux4-chat-waiting.md CW1.2). */
const deviceWords = /b\d+c\d+|¶|\]\(source:|действи|шаг|карточк|словар|(^|[^а-яё])нит[ьией]|слой|памят|просмотр текста|(^|\s)том(\s|$)|модел|Ищет|Читает|Смотрит|Работает|Поиск|Список глав/iu;

/** The line of a call while it runs and once it is done, as the feed shows it: the sentence and what came back. */
function lines(tool: ReturnType<typeof saga>, status: ActionStatus = 'done') {
  const described = describeSagaAction(tool, status, places);
  return { running: describeSagaAction(tool, 'running', places).activity, done: [described.label, described.detail].filter(Boolean).join(' — ') };
}

// specs/2026-11-10-ux4-chat-waiting.md CW1.1: the table of section 4.1 — a call, its line while it runs, its line once done.
describe('the lines of the work in the voice of the helper', () => {
  const table: Array<[string, ReturnType<typeof saga>, string, string]> = [
    ['search with a range', saga('search', { queries: ['пожар', 'дым'], to: 'b2c16' }, '«пожар» (any): 14 абзацев в 6 главах'),
      'Ищу в книгах: «пожар», «дым»', 'Искал «пожар», «дым» · до главы: Книга 2, гл. 16 „Журнал Савы“ — «пожар»: 14 мест в 6 главах'],
    ['search of one word', saga('search', { queries: ['Шорох'] }, '«Шорох» (any): 56 абзацев в 23 главах'), 'Ищу в книгах: «Шорох»', 'Искал «Шорох» — 56 мест в 23 главах'],
    ['search that found nothing', saga('search', { queries: ['8500'], from: '590' }, '«8500» (any): 0 абзацев в 0 главах'),
      'Ищу в книгах: «8500»', 'Искал «8500» · с главы: Книга 12, гл. 590 „Долг“ — ничего не нашёл'],
    ['search further in books', saga('search', { queries: ['мать'], books: [2, 3], offset: 30 }, '«мать» (any): 41 абзац в 21 главе'),
      'Ищу в книгах: «мать»', 'Искал «мать» · в книгах 2, 3 · дальше с 31-го места — 41 место в 21 главе'],
    ['read of a chapter', saga('read', { chapter: 'b2c12', from: 1, to: 8 },
      'Том 2, Белый порт — т. 2, «Белый порт» [b2c12], абзацы ¶1–¶8 из 40; ссылка на абзац: [т. 2, «Белый порт» ¶1](source:b2c12/1)'),
    'Читаю: Книга 2, гл. 12 „Белый порт“', 'Прочитал: Книга 2, гл. 12 „Белый порт“ — абзацы 1–8 из 40'],
    ['contents with summaries', saga('chapters', { from: 'b1c1', to: 'b2c16', detail: 'card' }, 'Глав: 28'),
      'Смотрю оглавление', 'Просмотрел краткое содержание 28 глав · с главы: Книга 1, гл. 1 „Туманный берег“ · до главы: Книга 2, гл. 16 „Журнал Савы“'],
    ['contents of a book', saga('chapters', { books: [2] }, 'Глав: 22'), 'Смотрю оглавление', 'Посмотрел оглавление · книга 2 — 22 главы'],
    ['names by a name', saga('glossary', { query: 'Мара' }, 'Найдено записей: 2'), 'Смотрю, что известно о „Мара“', 'Что известно о „Мара“ — 2 имени'],
    ['names by kinds', saga('glossary', { types: ['город', 'место'] }, 'Найдено записей: 21'),
      'Смотрю, что известно об именах: город, место', 'Что известно об именах: город, место — 21 имя'],
    ['names of the saga', saga('glossary', {}, 'Типы записей: 14'), 'Смотрю, что известно об именах', 'Посмотрел, что известно об именах'],
    ['a character', saga('entity', { name: 'Мирон', at: '593' }), 'Собираю всё о „Мирон“', 'Собрал всё о „Мирон“ · к главе: Книга 12, гл. 593 „Информация – сила“'],
    ['the time of the story', saga('timeline', { from: 'b2c10', to: 'b2c16', query: 'турнир' }),
      'Смотрю, когда что было', 'Посмотрел, когда что было · «турнир» · с главы: Книга 2, гл. 10 „Дым“ · до главы: Книга 2, гл. 16 „Журнал Савы“'],
    ['story lines of a hero', saga('threads', { who: 'Мара' }, 'Нитей: 3 (важных 1); слой собран по гл. 640, глав новее: 1.'),
      'Смотрю сюжетные линии', 'Сюжетные линии с „Мара“ — 3'],
    ['story lines not built yet', saga('threads', {}, 'Нитей и арок пока нет: слой саги ещё не собран.'), 'Смотрю сюжетные линии', 'Сюжетные линии — ещё собираются'],
    ['story lines of the books', saga('threads', { part: 'arcs' }, 'Арок: 1; слой собран по гл. 640, глав новее: 0.'), 'Смотрю главные линии саги', 'Главные линии саги — 1'],
    ['story lines found none', saga('threads', { query: 'меч', kind: 'ружьё', level: 'all' }, 'Нитей: 0 (важных 0); слой собран по гл. 2, глав новее: 0.'),
      'Смотрю сюжетные линии', 'Сюжетные линии про „меч“ · вид: ружьё · вместе с линиями книг — не нашёл'],
    ['changes', saga('changes', { subject: 'Мара', to: 'b2c16' }, 'Изменений: 5 (по порядку книги)'),
      'Слежу, как менялось: „Мара“', 'Что менялось у „Мара“ · до главы: Книга 2, гл. 16 „Журнал Савы“ — 5 перемен'],
    ['changes found none', saga('changes', { subject: 'Любава' }, 'Изменений не найдено. Попробуйте другое имя или search по тексту.'),
      'Слежу, как менялось: „Любава“', 'Что менялось у „Любава“ — перемен не нашёл'],
    ['a reading through', saga('scan', { task: 'кто видел начало пожара', from: 'b2c10', to: 'b2c16' }, 'Просмотрены главы: т. 2, «Белый порт» [b2c12]. Найдено: 3 (мест: 3).'),
      'Перечитываю подряд: «кто видел начало пожара»', 'Перечитал подряд: «кто видел начало пожара» · с главы: Книга 2, гл. 10 „Дым“ · до главы: Книга 2, гл. 16 „Журнал Савы“ — 3 места'],
    ['what the author asked to remember', saga('memory', { kind: 'correction' }, 'Записей: 0. Ничего не найдено.'),
      'Сверяюсь с тем, что вы просили помнить', 'Ваши поправки — пока нет'],
    ['all the records', saga('memory', {}, 'Записей: 3. Это слова автора.'), 'Сверяюсь с тем, что вы просили помнить', 'Сверился с тем, что вы просили помнить — 3 записи'],
    ['a record kept', saga('remember', { kind: 'plan', text: 'Кира узнает правду' }, 'Запись сохранена в памяти автора: план автора.'),
      'Запоминаю: «Кира узнает правду»', 'Запомнил: «Кира узнает правду»'],
    ['the chapter of the author', saga('attachment', { attachment: 1, from: 1, to: 3 }), 'Читаю вашу главу', 'Прочитал вашу главу — абзацы 1–3'],
  ];

  it.each(table)('%s', (_name, call, running, done) => {
    expect(lines(call)).toEqual({ running, done });
  });

  it('never shows a key, «¶», the name of a tool or a word of the device in any line', () => {
    const extra = [
      saga('read', { chapter: 'b9c99' }, 'глава "b9c99" не найдена; используйте сквозной номер главы («481») или ключ из результатов search/chapters'),
      saga('search', { queries: ['суд'], from: 'b1c1', to: 'b9c99' }, '«суд» (any): 3 абзаца в 2 главах'),
      saga('reference', { image: 42, subject: 'Мирон', from_chapter: 'b9c99' }),
      { namespace: 'drawing', name: 'describe', arguments: { chapter: 'b9c99' }, summary: '"b9c99" — не глава саги' },
      { namespace: 'draft', name: 'notes', arguments: { kind: 'canon' }, summary: 'Записано расхождений: 2; автор видит их у мест в тексте и решает сам. (гл. 14 ¶33, [b1c2])' },
      { namespace: 'draft', name: 'replace', arguments: { text: 'новый' }, summary: 'Заменено. Автор видит новый текст на месте сцены, а под твоим ответом — строку «Заменено».' },
      { namespace: 'saga', name: 'moment', arguments: {}, summary: 'Пакет на момент главы — то, что письмо знает о саге до места черновика' },
      { namespace: 'source', name: 'read', arguments: {} },
    ];
    for (const call of [...table.map((row) => row[1]), ...extra]) {
      for (const status of ['running', 'done', 'error'] as const) {
        const described = describeSagaAction(call, status, places);
        expect([described.label, described.activity, described.detail ?? ''].join(' | ')).not.toMatch(deviceWords);
      }
    }
  });

  it('turns a read into a source to open beside the chat, already while it reads', () => {
    expect(describeSagaAction(saga('read', { chapter: '593', around: 41, radius: 25 },
      'Том 12, Глава 593. Информация – сила — гл. 593, абзацы ¶16–¶66 из 75; ссылка на абзац: [гл. 593 ¶16](source:593/16)'), 'done', places)).toEqual({
      kind: 'read', label: 'Прочитал: Книга 12, гл. 593 „Информация – сила“', activity: 'Читаю: Книга 12, гл. 593 „Информация – сила“', detail: 'абзацы 16–66 из 75',
      source: { chapterKey: '593', from: 16, to: 66 },
    });
    expect(describeSagaAction(saga('read', { chapter: 'b2c12', from: 12, to: 40 }), 'running', places)).toEqual({
      kind: 'read', label: 'Прочитал: Книга 2, гл. 12 „Белый порт“', activity: 'Читаю: Книга 2, гл. 12 „Белый порт“', detail: 'абзацы 12–40',
      source: { chapterKey: 'b2c12', from: 12, to: 40 },
    });
    expect(describeSagaAction(saga('read', { chapter: 'b2c12', around: 7, radius: 0 }), 'running', places).detail).toBe('абзац 7');
    // A chapter the text has lost since is named by the title its reading returned, never by its key.
    expect(describeSagaAction(saga('read', { chapter: 'b3c7' },
      'Том 3, Суд — т. 3, «Суд» [b3c7], абзацы ¶1–¶9 из 9; ссылка на абзац: [т. 3, «Суд» ¶1](source:b3c7/1)'), 'done', places)).toMatchObject({
      label: 'Прочитал: Суд', activity: 'Читаю: Суд', detail: 'абзацы 1–9 из 9',
    });
    expect(describeSagaAction(saga('read', { chapter: 'b3c7' }), 'running', places)).toMatchObject({ label: 'Прочитал главу', activity: 'Читаю главу' });
    expect(describeSagaAction(saga('attachment', { attachment: 2, from: 41, to: 80 }), 'done')).toMatchObject({
      detail: 'абзацы 41–80', source: { chapterKey: 'a2', from: 41, to: 80 },
    });
    expect(describeSagaAction(saga('attachment', { from: 7 }), 'done')).toEqual({
      kind: 'read', label: 'Прочитал вашу главу', activity: 'Читаю вашу главу', detail: 'с абзаца 7',
    });
  });

  it('keeps what a turn remembered, to undo it under the answer', () => {
    expect(describeSagaAction(saga('remember', { kind: 'plan', text: 'Кира узнает правду' }, 'Запись сохранена в памяти автора: план автора.'), 'done'))
      .toMatchObject({ kind: 'remember', remembered: { kind: 'plan', text: 'Кира узнает правду' } });
    expect(describeSagaAction(saga('remember', { kind: 'plan', text: 'т' }), 'running').remembered).toBeUndefined();
  });

  // CW1.4 (N2.4, Ч-13): a step the helper corrected itself is an ordinary line — no message for the model, no source.
  it('says a failed call is tried another way', () => {
    expect(describeSagaAction(saga('chapters', { from: '590', to: '620' }, 'глава "620" не найдена; используйте сквозной номер главы'), 'error', places)).toEqual({
      kind: 'chapters', label: 'Посмотрел оглавление · с главы: Книга 12, гл. 590 „Долг“', activity: 'Смотрю оглавление', detail: 'не получилось, пробую иначе',
    });
    expect(describeSagaAction(saga('read', { chapter: 'b9c99' }, 'глава "b9c99" не найдена'), 'error', places)).toEqual({
      kind: 'read', label: 'Не нашёл такую главу — ищу иначе', activity: 'Не нашёл такую главу — ищу иначе',
    });
    expect(describeSagaAction(saga('read', { chapter: 'b2c12', from: 90 }, 'в главе b2c12 всего 40 абзацев'), 'error', places)).toEqual({
      kind: 'read', label: 'Прочитал: Книга 2, гл. 12 „Белый порт“', activity: 'Читаю: Книга 2, гл. 12 „Белый порт“', detail: 'не получилось, пробую иначе',
    });
    expect(describeSagaAction(saga('remember', { kind: 'plan', text: 'т' }, 'Автор этого хода может только читать память.'), 'error').detail).toBe('не получилось, пробую иначе');
    expect(describeSagaAction({ namespace: 'saga', name: 'search', summary: 'Чтение не удалось. Повторите вызов.' }, 'error').detail).toBe('не получилось, пробую иначе');
    expect(describeSagaAction({ namespace: 'saga', name: 'search' }, 'done')).toEqual({ kind: 'search', label: 'Искал в книгах', activity: 'Ищу в книгах' });
    expect(describeSagaAction({ namespace: 'source', name: 'read' }, 'done')).toEqual({ kind: 'other', label: 'Поработал с текстом', activity: 'Работаю с текстом' });
  });
});

// specs/2026-10-29-saga-illustrations.md R1.6; CW1.3: a drawing action in the voice of the helper.
const campfireScene = 'Мирон у костра. Ночь, лес, ворон на ветке; лицо освещено огнём.';
const technicalCampfire = 'Средний общий план, ракурс в три четверти, камера на высоте лица. Мирон у костра.';
const shorterHair = 'волосы до плеч';

describe('drawing actions', () => {
  it('names what is drawn and which drawing the tool answered with', () => {
    expect(describeSagaAction(saga('draw', {
      prompt: campfireScene, subjects: ['Мирон', 'Ворон'], chapter: '300', kind: 'scene',
    }, 'Рисунок 42 готов: A young man by a campfire'), 'done')).toEqual({
      kind: 'draw', label: 'Нарисовал: Мирон у костра', activity: 'Рисую: Мирон у костра', detail: 'рисунок готов',
      drawing: { tool: 'draw', subjects: ['Мирон', 'Ворон'], chapter: '300', image: 42 },
    });
    // R8.17: a new drawing is named by the caption the agent gave it, not by the technical description.
    expect(describeSagaAction(saga('draw', { prompt: technicalCampfire, title: 'Мирон у костра ночью', subjects: ['Мирон'] }), 'running'))
      .toMatchObject({ activity: 'Рисую: Мирон у костра ночью' });
    expect(describeSagaAction(saga('draw', { prompt: shorterHair, base_image: 42 }), 'running')).toMatchObject({ activity: `Правлю рисунок: ${shorterHair}` });
    expect(describeSagaAction(saga('draw', { prompt: 'зелёные глаза', base_image: 40, region: { x: 0.4, y: 0.2, w: 0.2, h: 0.1 } },
      'Рисунок 40: правка области 1 готова — зелёные глаза'), 'done')).toMatchObject({
      label: 'Поправил часть рисунка: зелёные глаза', detail: 'правка рисунка готова', drawing: { image: 40 },
    });
  });

  it('describes looking at a drawing, a reference and a refused drawing', () => {
    expect(describeSagaAction(saga('image', { image: 42 }, 'Рисунок 42 рисуется (обычно 40–120 с).'), 'done')).toMatchObject({
      label: 'Посмотрел рисунок', activity: 'Смотрю рисунок', detail: 'рисунок рисуется', drawing: { tool: 'image', image: 42 },
    });
    expect(describeSagaAction(saga('image', { image: 90, choose: true }, 'Рисунок 90 выбран автором: варианты 89, 91 удалены.'), 'done')).toMatchObject({
      label: 'Выбрал вариант', detail: 'вариант выбран',
    });
    expect(describeSagaAction(saga('reference', { image: 42, subject: 'Мирон', from_chapter: '590' }), 'done', places)).toMatchObject({
      label: 'Поставил пример: Мирон · с главы: Книга 12, гл. 590 „Долг“', drawing: { tool: 'reference', image: 42 },
    });
    expect(describeSagaAction(saga('reference', { image: 42, subject: 'Мирон' }), 'running').activity).toBe('Ставлю пример: Мирон · с начала');
    expect(describeSagaAction(saga('reference', { image: 42, subject: 'Мирон', from_chapter: 'b9c99', remove: true }), 'done', places).label).toBe('Снял пример: Мирон');
    // The reason of the painter is the author's; the advice to the model is not.
    expect(describeSagaAction(saga('draw', { prompt: 'Казнь на площади' },
      'Модель изображений отказалась рисовать рисунок 44 по своим правилам: жестокость. Смягчи указание и повтори.'), 'error')).toMatchObject({
      label: 'Нарисовал: Казнь на площади', detail: 'помощник не стал рисовать по правилам сервиса: жестокость — пробую иначе',
    });
  });

  it('speaks for the helper of the drawing workshop', () => {
    const drawing = (name: string, args: Record<string, unknown>, summary?: string) => ({ namespace: 'drawing', name, arguments: args, summary });
    expect(lines(drawing('state', {}))).toEqual({ running: 'Смотрю рисунок', done: 'Посмотрел рисунок' });
    expect(lines(drawing('describe', { subjects: ['Мара'] }, 'Описание рисунка записано: ревизия 3.'))).toEqual({ running: 'Правлю описание', done: 'Поправил описание' });
    expect(lines(drawing('look', { image: 3, check: true }))).toEqual({ running: 'Сверяю рисунок с книгой', done: 'Сверил рисунок с книгой' });
    expect(lines(drawing('look', { image: 3 }))).toEqual({ running: 'Смотрю рисунок', done: 'Посмотрел рисунок' });
    expect(lines(drawing('edit', { instruction: 'Рога короче. И свет теплее.' }, 'Рисунок 12: правка 2 готова — рога'))).toEqual({
      running: 'Правлю рисунок: Рога короче', done: 'Поправил рисунок: Рога короче — правка готова',
    });
    expect(lines(drawing('edit', { instruction: 'Рога короче', readings: ['а', 'б', 'в'] }, 'Правка 4 рисунка 12 рисуется'))).toEqual({
      running: 'Предлагаю варианты правки (3): Рога короче', done: 'Предложил варианты правки (3): Рога короче — правка рисуется',
    });
  });
});

// specs/2026-11-06-writing-agent.md; CW1.3: the helper of «Письмо» in the same voice.
describe('the helper of a chapter', () => {
  const draft = (name: string, args: Record<string, unknown>, summary?: string) => ({ namespace: 'draft', name, arguments: args, summary });
  const rows: Array<[string, ReturnType<typeof draft>, string, string]> = [
    ['the draft', draft('state', {}, 'Черновик «Кузница», ревизия 4.'), 'Читаю черновик', 'Прочитал черновик'],
    ['the canon', draft('notes', { kind: 'canon', items: [] }, 'Записано расхождений: 2; автор видит их у мест в тексте и решает сам.'), 'Сверяю с книгой', 'Сверил с книгой — 2 расхождения'],
    ['no difference', draft('notes', { kind: 'canon', items: [] }, 'Записано расхождений: 0; автор видит их у мест в тексте и решает сам.'), 'Сверяю с книгой', 'Сверил с книгой — расхождений нет'],
    ['the questions', draft('notes', { kind: 'question', items: [] }, 'Записано вопросов: 3; автор видит их у мест в тексте и решает сам.'), 'Пишу вопросы к главе', 'Записал вопросы к главе — 3 вопроса'],
    ['the notes', draft('notes', { items: [] }), 'Пишу заметки к главе', 'Записал заметки к главе'],
    ['a replacement', draft('replace', { text: 'новый' }, 'Заменено. Автор видит новый текст на месте сцены.'), 'Заменяю в тексте', 'Заменил в тексте'],
    ['the plan', draft('plan', { reason: 'first' }, 'Версия плана 3 записана: 5 сцен. Автор видит её и утверждает сам.'), 'Составляю план сцен', 'Составил план сцен — версия 3: 5 сцен'],
    ['a decision', draft('decide', { words: 'А' }), 'Записываю ваше решение', 'Записал ваше решение'],
    ['the scenes', draft('write', { mode: 'scenes', words: 'пиши' }), 'Начинаю писать сцены', 'Начал писать сцены'],
    ['variants of a place', draft('write', { mode: 'selection', words: 'перепиши' }), 'Предлагаю варианты', 'Предложил варианты'],
    ['the check', draft('write', { mode: 'check', words: 'проверь' }), 'Начинаю проверять главу по саге', 'Начал проверять главу по саге'],
    ['the moment of the chapter', { namespace: 'saga', name: 'moment', arguments: {}, summary: 'Пакет на момент главы' },
      'Собираю, что нужно знать к этой главе', 'Собрал, что нужно знать к этой главе'],
  ];
  it.each(rows)('%s', (_name, call, running, done) => {
    expect(lines(call)).toEqual({ running, done });
  });
});
