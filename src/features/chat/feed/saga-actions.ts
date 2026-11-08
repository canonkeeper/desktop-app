import type { SourceCitation } from '../../../shared/navigation/chat-source';
import { describeDrawAction, describeWorkshopAction, drawTools, workshopTools, type DrawActionFacts, type DrawTool, type WorkshopTool } from './draw-actions';

export type ActionKind = 'search' | 'read' | 'chapters' | 'glossary' | 'threads' | 'changes' | 'scan' | 'remember' | 'memory' | 'draw' | 'other';
export type ActionStatus = 'running' | 'done' | 'error';

/** One tool call as the author reads it: what was asked, what came back, where it leads. */
export interface ActionDescription {
  kind: ActionKind;
  /** What the agent did, e.g. «Поиск «Шорох»». */
  label: string;
  /** The same in the present tense for the status of a running turn, e.g. «Ищет «Шорох»». */
  activity: string;
  /** What came back, e.g. «56 абзацев в 23 главах», or the message of a failed call. */
  detail?: string;
  /** The text a read call returned, to open beside the chat. */
  source?: SourceCitation;
  /** A drawing tool: its drawing and what it draws (illustrations R1.6, R8.1). */
  drawing?: DrawActionFacts;
  /** What saga.remember kept, so the feed can undo it (specs/2026-11-02-writer-workshop.md, Decision log 03.11). */
  remembered?: { kind: string; text: string };
}

export interface SagaToolCall {
  namespace?: string;
  name: string;
  arguments?: Record<string, unknown>;
  summary?: string;
}

const fallbacks: Record<Exclude<ActionKind, 'other' | 'draw'>, [string, string]> = {
  search: ['Поиск в тексте саги', 'Ищет в тексте саги'],
  read: ['Чтение главы', 'Читает главу'],
  chapters: ['Список глав', 'Смотрит список глав'],
  glossary: ['Словарь саги', 'Смотрит словарь'],
  threads: ['Нити саги', 'Смотрит нити саги'],
  changes: ['Изменения по ходу саги', 'Прослеживает изменения'],
  scan: ['Просмотр текста', 'Просматривает текст целиком'],
  remember: ['Запись в память автора', 'Записывает в память автора'],
  memory: ['Память автора', 'Читает память автора'],
};

/** The tools of the helper of a chapter draft (specs/2026-11-02-writer-workshop.md W1.3, W4.5): what each does. */
const draftActions: Readonly<Record<string, [string, string]>> = {
  state: ['Черновик', 'Читает черновик'],
  brief: ['Справка к главе', 'Пишет справку к главе'],
  notes: ['Заметки к черновику', 'Пишет заметки к черновику'],
  variants: ['Варианты сцены', 'Предлагает варианты сцены'],
  replace: ['Заменено в тексте', 'Заменяет в тексте'],
  continue: ['Дописано по замыслу', 'Дописывает по замыслу'],
};

const noteActions: Readonly<Record<string, [string, string]>> = {
  canon: ['Сверка с каноном', 'Сверяет с каноном'],
  question: ['Вопросы автору', 'Записывает вопросы автору'],
};

/**
 * Describes a call of the saga tools by its arguments and the first line of its result (the
 * timeline summary). Tools of the previous assistant and calls without arguments get a plain
 * line by name.
 */
export function describeSagaAction(tool: SagaToolCall, status: ActionStatus): ActionDescription {
  if (tool.namespace === 'saga' && (drawTools as readonly string[]).includes(tool.name)) {
    const failure = status === 'error' ? tool.summary?.trim() || undefined : undefined;
    const described = describeDrawAction(tool.name as DrawTool, tool.arguments, failure ? undefined : tool.summary);
    return withOptional({ kind: 'draw', label: described.label, activity: described.activity, drawing: described.drawing }, { detail: failure ?? described.detail });
  }
  if (tool.namespace === 'drawing' && (workshopTools as readonly string[]).includes(tool.name)) {
    const failure = status === 'error' ? tool.summary?.trim() || undefined : undefined;
    const described = describeWorkshopAction(tool.name as WorkshopTool, tool.arguments, failure ? undefined : tool.summary);
    return withOptional({ kind: 'draw', label: described.label, activity: described.activity, ...('drawing' in described ? { drawing: described.drawing } : {}) }, { detail: failure ?? described.detail });
  }
  if (tool.namespace === 'draft' && tool.name in draftActions) {
    const notes = tool.name === 'notes' ? noteActions[String(tool.arguments?.kind)] : undefined;
    const [label, activity] = notes ?? draftActions[tool.name]!;
    const summary = tool.summary?.trim() || undefined;
    return withOptional({ kind: 'other', label, activity }, { detail: tool.name === 'state' && status !== 'error' ? undefined : summary });
  }
  if (tool.namespace === 'saga' && tool.name === 'attachment') return describeAttachmentRead(tool.arguments ?? {}, status === 'error' ? tool.summary?.trim() : undefined);
  const kind = tool.namespace === 'saga' && tool.name in fallbacks ? tool.name as Exclude<ActionKind, 'other' | 'draw'> : null;
  if (!kind) return { kind: 'other', label: 'Работа с текстом', activity: 'Работает с текстом' };
  const args = tool.arguments;
  const summary = tool.summary?.trim() || undefined;
  const failure = status === 'error' ? summary : undefined;
  if (!args) {
    const [label, activity] = fallbacks[kind];
    return withOptional({ kind, label, activity }, { detail: failure });
  }
  const described = describers[kind](args, status === 'error' ? undefined : summary);
  return withOptional({ kind, ...described, label: described.label }, { detail: failure ?? described.detail });
}

/**
 * The reading of the chapter of the author attached to the chat (specs/2026-11-07-chat-attach-chapter.md CA3.2): its
 * paragraphs open beside the chat as a source does.
 */
function describeAttachmentRead(args: Record<string, unknown>, failure?: string): ActionDescription {
  const number = integer(args.attachment);
  const from = integer(args.from);
  const to = integer(args.to);
  const paragraphs = from && to ? from === to ? `¶${from}` : `¶${from}–${to}` : from ? `с ¶${from}` : undefined;
  return withOptional({ kind: 'read', label: number ? `Глава автора, приложение ${number}` : 'Глава автора', activity: 'Читает приложенную главу' }, {
    detail: failure ?? paragraphs,
    source: number ? { chapterKey: `a${number}`, ...(from ? { from, to: to && to >= from ? to : from } : {}) } : undefined,
  });
}

type Described = Omit<ActionDescription, 'kind'>;

const memoryKinds: Readonly<Record<string, string>> = {
  plan: 'план', correction: 'поправка', style_rule: 'правило стиля', rejected: 'отвергнуто',
};

const describers: Record<Exclude<ActionKind, 'other' | 'draw'>, (args: Record<string, unknown>, summary?: string) => Described> = {
  search(args, summary) {
    const queries = strings(args.queries);
    const offset = integer(args.offset);
    const label = join(`Поиск ${quoteList(queries)}`, range(args), books(args), offset ? `дальше с ${offset + 1}-го` : undefined);
    const found = summary ? /^«(.+)» \([a-z]+\): (\d+) абзац\S* в (\d+) глав/u.exec(summary) : null;
    let detail: string | undefined;
    if (found) {
      const paragraphs = Number(found[2]);
      const chapters = Number(found[3]);
      const count = paragraphs === 0 ? 'ничего не найдено'
        : `${paragraphs} ${plural(paragraphs, ['абзац', 'абзаца', 'абзацев'])} в ${chapters} ${chapters % 10 === 1 && chapters % 100 !== 11 ? 'главе' : 'главах'}`;
      detail = queries.length > 1 && paragraphs > 0 ? `«${found[1]}»: ${count}` : count;
    }
    return withOptional({ label, activity: `Ищет ${quoteList(queries, 2)}` }, { detail });
  },
  read(args, summary) {
    const chapterKey = text(args.chapter) ?? '';
    const header = summary ? /^(?:Том (\d+), )?(.+?) — гл\. \S+, абзацы ¶(\d+)–¶(\d+) из (\d+)/u.exec(summary) : null;
    if (header) {
      const from = Number(header[3]);
      const to = Number(header[4]);
      const paragraphs = from === to ? `¶${from}` : `¶${from}–${to}`;
      return {
        label: header[2]!,
        activity: `Читает главу ${chapterKey}`,
        detail: join(`${paragraphs} из ${header[5]}`, header[1] ? `том ${header[1]}` : undefined),
        source: { chapterKey, from, to },
      };
    }
    const span = readSpan(args);
    return withOptional({ label: `Глава ${chapterKey}`, activity: `Читает главу ${chapterKey}` }, {
      detail: span ? span.from === span.to ? `¶${span.from}` : `¶${span.from}–${span.to}` : undefined,
      source: { chapterKey, ...(span ?? {}) },
    });
  },
  chapters(args, summary) {
    const detailMode = args.detail === 'summary' ? 'с пересказами' : args.detail === 'card' ? 'с карточками' : undefined;
    const title = text(args.title);
    const count = summary ? /^Глав: (\d+)/u.exec(summary) : null;
    return withOptional({
      label: join('Список глав', range(args), books(args), title ? `«${title}»` : undefined, detailMode),
      activity: 'Смотрит список глав',
    }, { detail: count ? `${count[1]} ${plural(Number(count[1]), ['глава', 'главы', 'глав'])}` : undefined });
  },
  glossary(args, summary) {
    const query = text(args.query);
    const types = strings(args.types);
    const count = summary ? /^Найдено записей: (\d+)/u.exec(summary) : null;
    return withOptional({
      label: query ? `Словарь: «${query}»` : types.length ? `Словарь: ${types.join(', ')}` : 'Словарь саги',
      activity: query ? `Ищет в словаре «${query}»` : 'Смотрит словарь',
    }, { detail: count ? `${count[1]} ${plural(Number(count[1]), ['запись', 'записи', 'записей'])}` : undefined });
  },
  remember(args, summary) {
    const kind = typeof args.kind === 'string' ? memoryKinds[args.kind] : undefined;
    const note = text(args.text);
    if (note && summary?.startsWith('Запись сохранена') && typeof args.kind === 'string') {
      return { label: `Запомнил: «${shorten(note, 80)}»`, activity: 'Записывает в память автора', remembered: { kind: args.kind, text: note } };
    }
    return {
      label: join('Запись в память автора', kind, note ? `«${shorten(note, 80)}»` : undefined),
      activity: 'Записывает в память автора',
    };
  },
  memory(args, summary) {
    const kind = typeof args.kind === 'string' ? memoryKinds[args.kind] : undefined;
    const query = text(args.query);
    const count = summary ? /^Записей: (\d+)/u.exec(summary) : null;
    const total = count ? Number(count[1]) : 0;
    return withOptional({
      label: join('Память автора', kind, query ? `«${query}»` : undefined),
      activity: 'Читает память автора',
    }, { detail: !count ? undefined : total === 0 ? 'записей нет' : `${total} ${plural(total, ['запись', 'записи', 'записей'])}` });
  },
  threads(args, summary) {
    const arcs = args.part === 'arcs';
    const what = arcs ? 'Арки саги' : 'Нити саги';
    const who = text(args.who);
    const query = text(args.query);
    const kind = text(args.kind);
    const count = summary ? /^(Нитей|Арок): (\d+)/u.exec(summary) : null;
    const total = count ? Number(count[2]) : 0;
    const detail = !count ? undefined
      : total === 0 ? 'ничего не найдено'
      : `${total} ${count[1] === 'Арок' ? plural(total, ['арка', 'арки', 'арок']) : plural(total, ['нить', 'нити', 'нитей'])}`;
    return withOptional({
      label: join(what, who ? `«${who}»` : undefined, query ? `«${query}»` : undefined, kind, args.level === 'all' ? 'с нитями книг' : undefined),
      activity: `Смотрит ${arcs ? 'арки' : 'нити'} саги`,
    }, { detail: detail ?? (summary?.startsWith('Нитей и арок пока нет') ? 'слой ещё не собран' : undefined) });
  },
  changes(args, summary) {
    const subject = text(args.subject) ?? '';
    const count = summary ? /^Изменений: (\d+)/u.exec(summary) : null;
    const detail = count
      ? `${count[1]} ${plural(Number(count[1]), ['изменение', 'изменения', 'изменений'])}`
      : summary?.startsWith('Изменений не найдено') ? 'ничего не найдено' : undefined;
    return withOptional({
      label: join(`Изменения: «${subject}»`, range(args)),
      activity: `Прослеживает изменения «${subject}»`,
    }, { detail });
  },
  scan(args, summary) {
    const task = text(args.task) ?? '';
    const found = summary ? /Найдено: (\d+)/u.exec(summary) : null;
    return withOptional({
      label: join(`Просмотр текста: «${shorten(task, 80)}»`, range(args), books(args)),
      activity: 'Просматривает текст целиком',
    }, { detail: found ? `найдено ${found[1]}` : undefined });
  },
};

function readSpan(args: Record<string, unknown>): { from: number; to: number } | null {
  const from = integer(args.from);
  const to = integer(args.to);
  if (from) return { from, to: to && to >= from ? to : from };
  const around = integer(args.around);
  if (!around) return null;
  const radius = integer(args.radius) ?? 4;
  return { from: Math.max(1, around - radius), to: around + radius };
}

function range(args: Record<string, unknown>) {
  const from = text(args.from);
  const to = text(args.to);
  if (from && to) return `гл. ${from}–${to}`;
  if (from) return `с гл. ${from}`;
  return to ? `до гл. ${to}` : undefined;
}

function books(args: Record<string, unknown>) {
  const list = Array.isArray(args.books) ? args.books.filter((book): book is number => Number.isInteger(book)) : [];
  if (!list.length) return undefined;
  return list.length === 1 ? `том ${list[0]}` : `тома ${list.join(', ')}`;
}

function quoteList(values: readonly string[], shown = 3) {
  if (!values.length) return '';
  const quoted = values.slice(0, shown).map((value) => `«${value}»`).join(', ');
  return values.length > shown ? `${quoted} и ещё ${values.length - shown}` : quoted;
}

function shorten(value: string, limit: number) {
  if (value.length <= limit) return value;
  const space = value.slice(0, limit + 1).lastIndexOf(' ');
  return `${value.slice(0, space > limit * 0.6 ? space : limit).trimEnd()}…`;
}

export function plural(count: number, forms: readonly [string, string, string]) {
  const lastTwo = count % 100;
  const last = count % 10;
  if (last === 1 && lastTwo !== 11) return forms[0];
  if (last >= 2 && last <= 4 && (lastTwo < 12 || lastTwo > 14)) return forms[1];
  return forms[2];
}

function join(...parts: Array<string | undefined>) {
  return parts.filter(Boolean).join(' · ');
}

function text(value: unknown) {
  return typeof value === 'string' && value.trim() ? value.trim() : undefined;
}

function strings(value: unknown) {
  return Array.isArray(value) ? value.filter((item): item is string => typeof item === 'string' && item.trim() !== '') : [];
}

function integer(value: unknown) {
  return typeof value === 'number' && Number.isInteger(value) && value >= 0 ? value : undefined;
}

function withOptional<T extends object>(base: T, optional: Partial<Pick<ActionDescription, 'detail' | 'source'>>): T & Partial<Pick<ActionDescription, 'detail' | 'source'>> {
  const result: T & Partial<Pick<ActionDescription, 'detail' | 'source'>> = { ...base };
  if (optional.detail !== undefined) result.detail = optional.detail;
  if (optional.source !== undefined) result.source = optional.source;
  return result;
}
