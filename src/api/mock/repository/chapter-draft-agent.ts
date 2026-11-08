import type { OperationRequest, Schema } from '../../../shared/api';
import { MockApiError } from '../scenarios';
import { addPlanVersion, plansOf } from './chapter-draft-plans';
import { keepVersion, versionsOf } from './chapter-draft-versions';

type Draft = Schema<'ChapterDraft'>;
type Note = Schema<'ChapterDraftNote'>;
type Plan = Schema<'ChapterDraftPlan'>;
type Scene = Schema<'ChapterDraftScene'>;
type Place = Schema<'ChapterDraftTextPlace'>;
type Start = OperationRequest<'startChapterDraftJob'>;

/**
 * The writing agent of the mock saga (specs/2026-11-06-writing-agent.md WA3–WA7; specs/2026-11-07-writing-screen.md
 * WS12): the plans a turn of the helper makes by the words of its button, and the Jobs of the writing that move a step
 * on every read of the draft, as background workers would. The prose is a fixed invented scene per card.
 */

const wordsOf = (text: string) => text.split(/\s+/u).filter((token) => token && !/^[―—–-]+$/u.test(token));
const phrase = (text: string, from: 'start' | 'end') => {
  const words = text.replace(/[⟦⟧]/gu, ' ').split(/\s+/u).filter(Boolean);
  return (from === 'start' ? words.slice(0, 6) : words.slice(-6)).join(' ');
};

/** Invented scenes of the mock saga (the owner's prototype of 05.11: Кира, Тарен, Ольм), by the title of a card. */
const prose: Record<string, string> = {
  'У ворот': 'Ночь у северных ворот пахла мокрым камнем и дымом из караулки. Кира подняла фонарь повыше и вгляделась в темноту под аркой — пусто.\n\nТарен обещал быть здесь к третьей страже. Она пришла раньше, как всегда, и теперь злилась на себя за это.',
  Стражник: 'Из караулки выглянул Ольм — немолодой, с седой щетиной и шрамом через губу. Он узнал её не сразу, а узнав, поморщился.\n\n— Если ты за мальчишкой из гильдии, то зря мёрзнешь, — сказал он. — Ушёл ещё днём. Через восточный выход, верхом и без провожатых.',
  'Обрывок письма': 'Она прошла вдоль стены, освещая камни факелом. У самого столба что-то белело в грязи: клочок бумаги, придавленный камнем.\n\nПоловина печати уцелела — серый воск, витая буква «Г». Слов почти не разобрать, только два, выведенные его рукой: «не ищи».',
  'Восточный выход': 'Кира обошла караулку и спустилась к восточному выходу, где ещё виднелись свежие следы.\n\nУ поворота кто-то сломал ветку и воткнул её в землю — знак, который они с Тареном придумали ещё детьми.',
  'В темноту': 'Кира погасила фонарь и шагнула в темноту.',
  Решение: 'Кира сложила обрывок и спрятала за пазуху. До утра оставалось часов пять. Ждать она не собиралась.',
};
const proseOf = (scene: Scene) => prose[scene.title] ?? `${scene.goal || scene.title}: Кира идёт дальше, не оглядываясь.`;

function card(number: number, title: string, goal: string, resistance: string, what: string, extra: Partial<Scene> = {}): Scene {
  return {
    number, title, state: 'planned', pov: 'Кира', whereWhen: 'северные ворота; ночь', participants: ['Кира'], goal, question: `${goal}?`,
    resistance, turns: [resistance], outcome: { type: 'yes_but', what }, value: { name: 'доверие', from: '+', to: '−' }, feelings: { main: 'тревога' },
    end: 'outcome', words: 700, ideaItems: [number], beats: [{ type: 'action', stimulus: resistance, reaction: 'Кира не отступает', atStake: goal }],
    ...extra,
  };
}

const understanding = (idea: string): Plan['understanding'] => ({
  lines: [
    'Кира ждёт Тарена у северных ворот ночью.',
    'Тарена нет; стражник Ольм говорит, что тот ушёл днём к болотам.',
    'У ворот Кира находит обрывок письма с печатью гильдии.',
    'Она решает идти следом одна, не дожидаясь утра.',
    'Глава кончается её решением — крючок к следующей.',
  ],
  pov: 'Кира', value: 'доверие к Тарену: + → −',
  threads: [{ thread: 'письмо гильдии', how: 'advances', milestone: 'печать на обрывке' }],
  keepVerbatim: [], avoid: ['не раскрывать, зачем Тарен ушёл'],
  ideaItems: idea.trim() ? idea.split(/(?<=[.!?])\s+/u).map((item) => item.trim()).filter(Boolean).slice(0, 8) : ['Кира у ворот'],
});

/** What the mock helper writes in a turn by the words of the button that opened it (WS12.1); null — nothing for the plan. */
export function helperPlan(draft: Draft, words: string, place: string | undefined, writtenSpans: ReadonlyMap<number, { from: string; to: string }>): Plan | null {
  const asked = words.trim();
  const latest = plansOf(draft).at(-1);
  const chapterCase = draft.case;
  if (/^(Написать главу|Дописать главу)/u.test(asked)) {
    const beginning = chapterCase === 'continue' && draft.text.trim();
    const scenes = beginning
      ? [card(1, 'Стражник', 'узнать, где Тарен', 'Ольм не хочет связываться с гильдией', 'Тарен ушёл к болотам'), card(2, 'Обрывок письма', 'найти след', 'темнота и грязь', 'он просит не искать')]
      : [card(1, 'У ворот', 'встретить Тарена', 'его нет, ночь', 'ждёт и злится на себя'), card(2, 'Стражник', 'узнать, где Тарен', 'Ольм не хочет связываться с гильдией', 'Тарен ушёл к болотам'),
        card(3, 'Обрывок письма', 'найти след', 'темнота и грязь', 'он просит не искать')];
    return addPlanVersion(draft, {
      origin: 'helper', reason: latest ? 'answers' : 'first', case: chapterCase,
      understanding: { ...understanding(draft.idea), ...(beginning ? { coverage: [{ item: 1, covered: true, paragraphs: '¶1', stoppedAt: 'Кира у ворот' }] } : {}) },
      scenes, marks: [],
    });
  }
  if (asked === 'Разобрать на сцены') {
    const paragraphs = draft.text.split(/\n+/u).map((line) => line.trim()).filter(Boolean);
    const scenes: Scene[] = [];
    let group: string[] = [];
    const flush = () => {
      if (!group.length) return;
      const number = scenes.length + 1;
      scenes.push(card(number, `Сцена ${number}`, 'дойти до конца сцены', 'то, что мешает в вашем тексте', 'так, как у вас', {
        state: 'author', span: { from: phrase(group[0]!, 'start'), to: phrase(group.at(-1)!, 'end') }, beats: [],
      }));
      group = [];
    };
    for (const line of paragraphs) {
      if (/^\[.*\]$/u.test(line)) {
        flush();
        const number = scenes.length + 1;
        scenes.push(card(number, 'Пропуск', 'заполнить пропуск', 'нет текста', line.slice(1, -1), { state: 'gap', fillAt: line }));
        continue;
      }
      group.push(line);
      if (group.length >= 3) flush();
    }
    flush();
    if (!scenes.length) return null;
    return addPlanVersion(draft, { origin: 'helper', reason: 'reverse', case: chapterCase, understanding: understanding(draft.idea), scenes, marks: [] });
  }
  if (asked.startsWith('Отсюда до конца') && place) {
    const working = draft.text;
    const at = working.indexOf(place);
    const before = at > 0 ? working.slice(0, at).trim() : '';
    const kept = before ? [card(1, 'Ваше начало', 'как у вас', 'как у вас', 'как у вас', { state: 'author', span: { from: phrase(before, 'start'), to: phrase(before, 'end') }, beats: [] })] : [];
    const next = kept.length;
    return addPlanVersion(draft, {
      origin: 'helper', reason: 'from_here', case: chapterCase, understanding: understanding(draft.idea),
      scenes: [...kept, card(next + 1, 'Восточный выход', 'найти, куда он ушёл', 'следы размыты', 'знак могли оставить и для чужих'), card(next + 2, 'В темноту', 'идти незаметно', 'страх болот', 'шагает в темноту')],
      marks: [],
    });
  }
  if (asked === 'Обновить план' && latest) {
    const scenes = latest.scenes.map((scene, index): Scene => {
      const span = scene.span ?? writtenSpans.get(scene.number);
      if (!span) return scene;
      return { ...scene, state: index === latest.scenes.length - 1 ? 'affected' : 'written', span };
    });
    scenes.push(card(scenes.length + 1, 'Решение', 'решить, что делать', 'ночь, болота, его просьба', 'идёт одна'));
    return addPlanVersion(draft, { origin: 'helper', reason: 'new_idea', case: chapterCase, understanding: understanding(draft.idea), scenes, marks: [] });
  }
  if (asked.startsWith('Добавь сцену') && latest) {
    const title = asked.replace(/^Добавь сцену[^:]*:\s*/u, '').slice(0, 60) || 'Новая сцена';
    return addPlanVersion(draft, {
      origin: 'helper', reason: 'author_words', words: asked, case: chapterCase, understanding: latest.understanding,
      scenes: [...latest.scenes, card(latest.scenes.length + 1, title, title, 'ночь', 'как просил автор')], marks: [],
    });
  }
  return null;
}

/** The questions a plan of the mock asks (WA3.5): one holds a scene, one the plan; the second has a default unlike the advice. */
export function planQuestions(): Array<Omit<Note, 'id' | 'number' | 'origin' | 'status' | 'createdAt' | 'updatedAt'>> {
  return [
    {
      kind: 'question', target: 'none', body: 'Как Тарен уехал? В замысле не сказано.', blocks: 'сцена 2', known: 'В гл. 11 он уезжал верхом (b1c1/4).',
      options: ['Верхом', 'Пешком, с поклажей'], effects: ['Ольм видел коня', 'следы ведут пешком'], recommend: 'А', fallback: 'А', refs: ['b1c1/4'],
    },
    {
      kind: 'question', target: 'none', body: 'Узнаёт ли Кира его почерк?', blocks: 'план', known: 'Росли вместе (b1c1/2).',
      options: ['Да, сразу', 'Нет, догадывается по печати'], effects: ['решение идти — сразу', 'сомнение до конца сцены'], recommend: 'А', fallback: 'Б', refs: ['b1c1/2'],
    },
  ];
}

/** Where the scenes of a run go (sagadraft ScenesBounds and SpanBounds): the text kept before them and after them. */
function boundsOf(working: string, chapterCase: Draft['case'], todo: readonly Scene[], body: Start) {
  const spanned = todo.filter((scene) => scene.span);
  if (!body.from && spanned.length && spanned.length === todo.length) {
    const start = working.indexOf(spanned[0]!.span!.from);
    const lastTo = spanned.at(-1)!.span!.to;
    const end = working.indexOf(lastTo, start) + lastTo.length;
    if (start < 0 || end < start) throw new MockApiError(400, 'The scene is not in the text.', 'startChapterDraftJob', 'request.validation');
    return { prefix: working.slice(0, start), suffix: working.slice(end) };
  }
  if (todo.length === 1 && todo[0]!.state === 'gap' && todo[0]!.fillAt) {
    const at = working.indexOf(todo[0]!.fillAt);
    if (at < 0) throw new MockApiError(400, 'The gap is not in the text.', 'startChapterDraftJob', 'request.validation');
    return { prefix: working.slice(0, at), suffix: working.slice(at + todo[0]!.fillAt.length) };
  }
  if (chapterCase === 'scratch' && !body.from) return { prefix: '', suffix: '' };
  let at = working.length;
  if (body.from) {
    const range = placeIn(working, body.from);
    if (!range) throw new MockApiError(400, 'The place is not in the text.', 'startChapterDraftJob', 'request.validation');
    at = range[1];
  }
  const prefix = working.slice(0, at).trimEnd();
  return { prefix: prefix ? `${prefix}\n\n` : '', suffix: body.tail === 'replace' ? '' : working.slice(at) };
}

/** A place of the working text, by its quote or by its first and last phrases. */
export function placeIn(text: string, place: Place): [number, number] | null {
  if (place.quote) {
    const at = text.indexOf(place.quote);
    return at < 0 ? null : [at, at + place.quote.length];
  }
  if (!place.from || !place.to) return null;
  const start = text.indexOf(place.from);
  const end = start < 0 ? -1 : text.indexOf(place.to, start);
  return start < 0 || end < 0 ? null : [start, end + place.to.length];
}

/** A run of the agent in the mock: what it writes and where, step by step. */
export interface AgentRun {
  mode: NonNullable<Start['mode']> | 'seams';
  todo: Scene[];
  prefix: string;
  suffix: string;
  written: string[];
  plan: number;
  place?: [number, number];
}

const runs = new WeakMap<Draft, AgentRun>();

/** startChapterDraftJob with a mode: the run is set up, the version before the writing kept (WA0.4). */
export function startAgentRun(draft: Draft, body: Start, jobId: string) {
  const working = draft.text;
  const mode = body.mode!;
  if (mode === 'scenes') {
    const plan = plansOf(draft).find((item) => item.number === body.plan);
    if (!plan) throw new MockApiError(404, 'No such plan.', 'startChapterDraftJob');
    const wanted = new Set(body.scenes ?? []);
    const todo = plan.scenes.filter((scene) => (wanted.size ? wanted.has(scene.number) : ['planned', 'affected', 'gap'].includes(scene.state)));
    if (!todo.length) throw new MockApiError(400, 'Nothing to write.', 'startChapterDraftJob', 'request.validation');
    const bounds = boundsOf(working, draft.case, todo, body);
    keepVersion(draft, 'before_write', working);
    runs.set(draft, { mode, todo, ...bounds, written: [], plan: plan.number });
  } else if (mode === 'selection') {
    const place = body.place ? placeIn(working, body.place) : null;
    if (!place) throw new MockApiError(400, 'The place is not in the text.', 'startChapterDraftJob', 'request.validation');
    runs.set(draft, { mode, todo: [], prefix: '', suffix: '', written: [], plan: 0, place });
  } else {
    runs.set(draft, { mode, todo: [], prefix: '', suffix: '', written: [], plan: 0 });
  }
  draft.job = { id: jobId, mode, status: 'queued', done: 0, total: 0 };
}

/** takeChapterDraftVersion of a piece checks the seams of what it put in (WA7.5). */
export function startSeams(draft: Draft, jobId: string) {
  runs.set(draft, { mode: 'seams', todo: [], prefix: '', suffix: '', written: [], plan: 0 });
  draft.job = { id: jobId, mode: 'seams', status: 'queued', done: 0, total: 0 };
}

type NewNote = Omit<Note, 'id' | 'number' | 'status' | 'createdAt' | 'updatedAt'>;

/** The findings of the check (WA6): one against the book with two quotes, one invented, one of style. */
function findings(text: string): NewNote[] {
  const found: NewNote[] = [];
  const scar = 'шрамом через губу';
  if (text.includes(scar)) {
    const book = '…шрам наискось через щёку';
    found.push({ kind: 'canon', target: 'draft', origin: 'job', level: 'blocking', quote: scar, body: 'В гл. 3 у Ольма шрам на щеке.', bookQuote: book, replacement: 'шрамом на щеке', refs: ['b1c1/8'] });
  }
  found.push({ kind: 'new', target: 'draft', origin: 'job', level: 'info', body: 'Седая щетина у Ольма в саге не описана: это придумано. Станет каноном только с главой.', refs: [] });
  // What changes inside the chapter without a cause (c83fb142): the later mention, the earlier named, the fix.
  const torch = 'освещая камни факелом';
  if (text.includes(torch) && text.includes('подняла фонарь')) {
    found.push({ kind: 'canon', target: 'draft', origin: 'job', level: 'warning', category: 'внутри главы', quote: torch,
      body: 'Раньше в главе у Киры фонарь: «подняла фонарь повыше».', replacement: 'освещая камни фонарём', refs: [] });
  }
  const repeated = /восточн\S* выход\S*/u.exec(text);
  // Layer A: a mark without a model and without a replacement (WA6.2).
  if (repeated) found.push({ kind: 'style', target: 'draft', origin: 'job', level: 'info', category: 'повтор', quote: repeated[0], body: '«Восточный» дважды в соседних абзацах.', refs: [] });
  return found;
}

/**
 * One step of the run of a draft on a read of it (WS12.2): the pack, a scene into the working text between the marks,
 * the check; a canceled run stops where it is, and what it wrote stays. Answers the notes it adds.
 */
export function advanceAgentRun(draft: Draft): NewNote[] {
  const job = draft.job;
  const run = runs.get(draft);
  if (!job || !run || !['queued', 'running', 'canceling'].includes(job.status)) return [];
  if (job.status === 'canceling') {
    job.status = 'canceled';
    return [];
  }
  if (job.status === 'queued') {
    Object.assign(job, { status: 'running', phase: 'pack', total: run.mode === 'scenes' ? run.todo.length : 1 });
    return [];
  }
  if (run.mode === 'scenes') {
    if (job.phase === 'pack') {
      job.phase = 'scenes';
      return [];
    }
    if (job.phase === 'scenes' && run.written.length < run.todo.length) {
      run.written.push(`⟦${proseOf(run.todo[run.written.length]!)}⟧`);
      draft.text = `${run.prefix}${run.written.join('\n\n')}${run.suffix ? `\n\n${run.suffix.replace(/^\s+/u, '')}` : ''}`;
      job.done = run.written.length;
      draft.revision += 1;
      if (run.written.length === run.todo.length) job.phase = 'check';
      return [];
    }
    keepVersion(draft, 'written', draft.text, {
      plan: run.plan, support: run.todo.map((scene, index) => ({ scene: scene.number, from: phrase(run.written[index] ?? '', 'start'), to: phrase(run.written[index] ?? '', 'end') })),
    });
    job.status = 'succeeded';
    return findings(run.written.join('\n\n'));
  }
  if (run.mode === 'selection') {
    const working = draft.text;
    const [start, end] = run.place!;
    const quote = working.slice(start, end);
    const group = `${draft.id}-group-${job.id}`;
    Object.assign(job, { status: 'succeeded', done: 1, phase: 'scenes' });
    const variants = [
      ['Короче и жёстче', 'Ворота стояли тёмные, только из караулки сочился дым. Кира подняла фонарь: под аркой — никого.'],
      ['Через запах и свет', 'У северных ворот пахло мокрым камнем и чужим дымом. Кира подняла фонарь, и свет лёг на пустую арку.'],
      ['С её злостью на себя', 'Тарен клялся прийти к третьей страже. Она, как всегда, явилась раньше и теперь не знала, на кого злится сильнее.'],
    ] as const;
    return [
      ...variants.map(([body, replacement]): NewNote => ({ kind: 'variant', target: 'draft', origin: 'job', quote, body, replacement, refs: [], variantGroup: group })),
      { kind: 'new', target: 'draft', origin: 'job', level: 'info', body: 'Чужой дым из караулки — придумано для варианта.', refs: [] },
    ];
  }
  if (run.mode === 'check') {
    if (job.phase === 'pack') {
      job.phase = 'check';
      return [];
    }
    Object.assign(job, { status: 'succeeded', done: 1 });
    return findings(draft.text);
  }
  Object.assign(job, { status: 'succeeded', done: 1, phase: 'notes' });
  return [];
}

/** The scenes the writing wrote, by the support of the written versions of a plan (WS4.2). */
export function writtenSpansOf(draft: Draft, plan: number) {
  const spans = new Map<number, { from: string; to: string }>();
  for (const version of versionsOf(draft)) {
    if (version.reason !== 'written' || version.plan !== plan) continue;
    for (const item of version.support) if (item.from && item.to) spans.set(item.scene, { from: item.from, to: item.to });
  }
  return spans;
}

export { wordsOf };
