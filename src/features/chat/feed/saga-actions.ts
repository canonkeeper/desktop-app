import { paragraphsLabel, type Places } from '../../../shared/text/place';
import { describeDraftAction, isDraftTool, momentWords } from './draft-actions';
import type { ActionDescription, ActionKind, ActionStatus, SagaToolCall } from './action-types';
import { describeDrawAction, describeWorkshopAction, drawTools, painterRefusal, workshopTools, type DrawTool, type WorkshopTool } from './draw-actions';

export type { ActionDescription, ActionKind, ActionStatus, SagaToolCall } from './action-types';

type SagaKind = Exclude<ActionKind, 'other' | 'draw'>;

/** The line of a call without arguments: [done, running]. */
const plainWords: Record<SagaKind, [string, string]> = {
  search: ['Искал в книгах', 'Ищу в книгах'],
  read: ['Прочитал главу', 'Читаю главу'],
  chapters: ['Посмотрел оглавление', 'Смотрю оглавление'],
  glossary: ['Посмотрел, что известно об именах', 'Смотрю, что известно об именах'],
  entity: ['Собрал всё о герое', 'Собираю всё о герое'],
  timeline: ['Посмотрел, когда что было', 'Смотрю, когда что было'],
  threads: ['Посмотрел сюжетные линии', 'Смотрю сюжетные линии'],
  changes: ['Посмотрел, что менялось', 'Слежу, как менялось'],
  scan: ['Перечитал подряд', 'Перечитываю подряд'],
  // «Что помнит помощник» has one name (specs/2026-11-10-ux2-memory-page.md MP1.4): the line says what the helper does with it.
  remember: ['Запомнил', 'Запоминаю'],
  memory: ['Сверился с тем, что вы просили помнить', 'Сверяюсь с тем, что вы просили помнить'],
};

/** A reading of the saga that did not work: the helper goes on, so the line says only that (CW1.4, ux1-names N2.4). */
const triedAnotherWay = 'не получилось, пробую иначе';
const chapterNotFound = 'Не нашёл такую главу — ищу иначе';

/**
 * Describes a call of the saga tools by its arguments, the first line of its result (the timeline summary) and the places
 * of the chapters its arguments name (`places` of the event: the server writes them beside the keys, so a chapter has its
 * name while it is still read and its key is never shown — specs/2026-11-10-ux1-names.md N2). One table for the chat and
 * the helpers of «Письмо» and «Рисование» (CW1.1, CW1.3); the first line of a result is read for numbers only, it is
 * written for the model.
 */
export function describeSagaAction(tool: SagaToolCall, status: ActionStatus, places: Places = {}): ActionDescription {
  const failed = status === 'error';
  if (tool.namespace === 'saga' && (drawTools as readonly string[]).includes(tool.name)) {
    const described = describeDrawAction(tool.name as DrawTool, tool.arguments, failed ? undefined : tool.summary, places);
    return withOptional({ kind: 'draw', label: described.label, activity: described.activity, drawing: described.drawing }, { detail: failed ? refusal(tool) : described.detail });
  }
  if (tool.namespace === 'drawing' && (workshopTools as readonly string[]).includes(tool.name)) {
    const described = describeWorkshopAction(tool.name as WorkshopTool, tool.arguments, failed ? undefined : tool.summary);
    return withOptional({ kind: 'draw', label: described.label, activity: described.activity, ...('drawing' in described ? { drawing: described.drawing } : {}) },
      { detail: failed ? refusal(tool) : described.detail });
  }
  if (isDraftTool(tool)) return describeDraftAction(tool, status, triedAnotherWay);
  if (tool.namespace === 'saga' && tool.name === 'moment') {
    return withOptional({ kind: 'other', label: momentWords[0], activity: momentWords[1] }, { detail: failed ? triedAnotherWay : undefined });
  }
  if (tool.namespace === 'saga' && tool.name === 'attachment') return describeAttachmentRead(tool.arguments ?? {}, failed);
  const kind = tool.namespace === 'saga' && tool.name in plainWords ? tool.name as SagaKind : null;
  if (!kind) return withOptional({ kind: 'other', label: 'Поработал с текстом', activity: 'Работаю с текстом' }, { detail: failed ? triedAnotherWay : undefined });
  const args = tool.arguments;
  if (!args) {
    const [label, activity] = plainWords[kind];
    return withOptional({ kind, label, activity }, { detail: failed ? triedAnotherWay : undefined });
  }
  const described = describers[kind](args, failed ? undefined : tool.summary?.trim() || undefined, places);
  if (!failed) return { kind, ...described };
  // A step the helper corrected itself is an ordinary line: no message for the model, no source to open (N2.4).
  if (kind === 'read' && !places[text(args.chapter) ?? '']) return { kind, label: chapterNotFound, activity: chapterNotFound };
  return { kind, label: described.label, activity: described.activity, detail: triedAnotherWay };
}

/** A tool of a drawing that refused: the reason of the painter is the author's, any other message is for the model. */
function refusal(tool: SagaToolCall) {
  const reason = painterRefusal(tool.summary);
  return reason ? `${reason} — пробую иначе` : triedAnotherWay;
}

/**
 * The reading of the chapter of the author attached to the chat (specs/2026-11-07-chat-attach-chapter.md CA3.2): its
 * paragraphs open beside the chat as a source does.
 */
function describeAttachmentRead(args: Record<string, unknown>, failed: boolean): ActionDescription {
  const number = integer(args.attachment);
  const from = integer(args.from);
  const to = integer(args.to);
  const paragraphs = from && to ? paragraphsLabel(from, to) : from ? `с абзаца ${from}` : undefined;
  return withOptional({ kind: 'read', label: 'Прочитал вашу главу', activity: 'Читаю вашу главу' }, {
    detail: failed ? triedAnotherWay : paragraphs,
    source: number && !failed ? { chapterKey: `a${number}`, ...(from ? { from, to: to && to >= from ? to : from } : {}) } : undefined,
  });
}

type Described = Omit<ActionDescription, 'kind'>;

/** What the author asked the helper to remember, by its kind: «Ваши поправки — пока нет». */
const memoryKinds: Readonly<Record<string, string>> = {
  plan: 'Ваши планы', correction: 'Ваши поправки', style_rule: 'Ваши правила стиля', rejected: 'Что вы отвергли',
};

const describers: Record<SagaKind, (args: Record<string, unknown>, summary: string | undefined, places: Places) => Described> = {
  search(args, summary, places) {
    const queries = strings(args.queries);
    const offset = integer(args.offset);
    const where = join(range(args, places), books(args, 'в книге', 'в книгах'), offset ? `дальше с ${offset + 1}-го места` : undefined);
    const found = summary ? /^«(.+)» \([a-z]+\): (\d+) абзац\S* в (\d+) глав/u.exec(summary) : null;
    let detail: string | undefined;
    if (found) {
      const count = Number(found[2]);
      const chapters = Number(found[3]);
      const placesFound = count === 0 ? 'ничего не нашёл'
        : `${count} ${plural(count, ['место', 'места', 'мест'])} в ${chapters} ${chapters % 10 === 1 && chapters % 100 !== 11 ? 'главе' : 'главах'}`;
      detail = queries.length > 1 && count > 0 ? `«${found[1]}»: ${placesFound}` : placesFound;
    }
    return withOptional({ label: join(`Искал ${quoteList(queries)}`, where), activity: `Ищу в книгах: ${quoteList(queries, 2)}` }, { detail });
  },
  read(args, summary, places) {
    const chapterKey = text(args.chapter) ?? '';
    // The chapter is named by the server; a chapter the text lost is named by the title its reading returned.
    const place = places[chapterKey] ?? (summary ? /^(?:Том \d+, )?(.+?) — /u.exec(summary)?.[1] : undefined);
    const label = place ? `Прочитал: ${place}` : 'Прочитал главу';
    const activity = place ? `Читаю: ${place}` : 'Читаю главу';
    const read = summary ? /, абзацы ¶(\d+)–¶(\d+) из (\d+)/u.exec(summary) : null;
    if (read) {
      const from = Number(read[1]);
      const to = Number(read[2]);
      return { label, activity, detail: `${paragraphsLabel(from, to)} из ${read[3]}`, source: { chapterKey, from, to } };
    }
    const span = readSpan(args);
    return withOptional({ label, activity }, {
      detail: span ? paragraphsLabel(span.from, span.to) : undefined,
      source: { chapterKey, ...(span ?? {}) },
    });
  },
  chapters(args, summary, places) {
    const retold = args.detail === 'summary' || args.detail === 'card';
    const title = text(args.title);
    const counted = summary ? /^Глав: (\d+)/u.exec(summary) : null;
    const count = counted ? Number(counted[1]) : undefined;
    const where = join(range(args, places), books(args, 'книга', 'книги'), title ? `«${title}»` : undefined);
    const chapters = count === undefined ? undefined : `${count} ${plural(count, ['главы', 'глав', 'глав'])}`;
    if (retold) {
      return { label: join(`Просмотрел краткое содержание ${chapters ?? 'глав'}`, where), activity: 'Смотрю оглавление' };
    }
    return withOptional({ label: join('Посмотрел оглавление', where), activity: 'Смотрю оглавление' },
      { detail: count === undefined ? undefined : `${count} ${plural(count, ['глава', 'главы', 'глав'])}` });
  },
  glossary(args, summary) {
    const query = text(args.query);
    const types = strings(args.types);
    const counted = summary ? /^Найдено записей: (\d+)/u.exec(summary) : null;
    const detail = counted ? `${counted[1]} ${plural(Number(counted[1]), ['имя', 'имени', 'имён'])}` : undefined;
    if (query) return withOptional({ label: `Что известно о „${query}“`, activity: `Смотрю, что известно о „${query}“` }, { detail });
    if (types.length) return withOptional({ label: `Что известно об именах: ${types.join(', ')}`, activity: `Смотрю, что известно об именах: ${types.join(', ')}` }, { detail });
    return { label: plainWords.glossary[0], activity: plainWords.glossary[1] };
  },
  entity(args, _summary, places) {
    const name = text(args.name);
    const at = places[text(args.at) ?? ''];
    if (!name) return { label: plainWords.entity[0], activity: plainWords.entity[1] };
    return { label: join(`Собрал всё о „${name}“`, at ? `к главе: ${at}` : undefined), activity: `Собираю всё о „${name}“` };
  },
  timeline(args, _summary, places) {
    const query = text(args.query);
    return { label: join(plainWords.timeline[0], query ? `«${query}»` : undefined, range(args, places)), activity: plainWords.timeline[1] };
  },
  remember(args, summary) {
    const note = text(args.text);
    const quoted = note ? `«${shorten(note, 80)}»` : undefined;
    const label = quoted ? `Запомнил: ${quoted}` : 'Запомнил';
    const activity = quoted ? `Запоминаю: ${quoted}` : 'Запоминаю';
    if (note && summary?.startsWith('Запись сохранена') && typeof args.kind === 'string') {
      return { label, activity, remembered: { kind: args.kind, text: note } };
    }
    return { label, activity };
  },
  memory(args, summary) {
    const kind = typeof args.kind === 'string' ? memoryKinds[args.kind] : undefined;
    const query = text(args.query);
    const counted = summary ? /^Записей: (\d+)/u.exec(summary) : null;
    const total = counted ? Number(counted[1]) : 0;
    return withOptional({
      label: join(kind ?? plainWords.memory[0], query ? `«${query}»` : undefined),
      activity: plainWords.memory[1],
    }, { detail: !counted ? undefined : total === 0 ? 'пока нет' : `${total} ${plural(total, ['запись', 'записи', 'записей'])}` });
  },
  threads(args, summary) {
    const arcs = args.part === 'arcs';
    const who = text(args.who);
    const query = text(args.query);
    const kind = text(args.kind);
    const counted = summary ? /^(Нитей|Арок): (\d+)/u.exec(summary) : null;
    const total = counted ? Number(counted[2]) : 0;
    const detail = !counted ? (summary?.startsWith('Нитей и арок пока нет') ? 'ещё собираются' : undefined) : total === 0 ? 'не нашёл' : String(total);
    const label = arcs ? 'Главные линии саги'
      : join(`Сюжетные линии${who ? ` с „${who}“` : ''}${query ? ` про „${query}“` : ''}`, kind ? `вид: ${kind}` : undefined, args.level === 'all' ? 'вместе с линиями книг' : undefined);
    return withOptional({ label, activity: arcs ? 'Смотрю главные линии саги' : 'Смотрю сюжетные линии' }, { detail });
  },
  changes(args, summary, places) {
    const subject = text(args.subject) ?? '';
    const counted = summary ? /^Изменений: (\d+)/u.exec(summary) : null;
    const detail = counted
      ? `${counted[1]} ${plural(Number(counted[1]), ['перемена', 'перемены', 'перемен'])}`
      : summary?.startsWith('Изменений не найдено') ? 'перемен не нашёл' : undefined;
    return withOptional({ label: join(`Что менялось у „${subject}“`, range(args, places)), activity: `Слежу, как менялось: „${subject}“` }, { detail });
  },
  scan(args, summary, places) {
    const task = text(args.task);
    const sought = task ? `: «${shorten(task, 80)}»` : '';
    const found = summary ? /Найдено: (\d+)/u.exec(summary) : null;
    const count = found ? Number(found[1]) : undefined;
    return withOptional({
      label: join(`${plainWords.scan[0]}${sought}`, range(args, places), books(args, 'книга', 'книги')),
      activity: `${plainWords.scan[1]}${task ? `: «${shorten(task, 60)}»` : ''}`,
    }, { detail: count === undefined ? undefined : count === 0 ? 'ничего не нашёл' : `${count} ${plural(count, ['место', 'места', 'мест'])}` });
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

/** «с главы: Книга 1, гл. 1 „Туманный берег“ · до главы: Книга 2, гл. 16 „Журнал Савы“»: the chapters a call is bound to, by their places. */
function range(args: Record<string, unknown>, places: Places) {
  const from = places[text(args.from) ?? ''];
  const to = places[text(args.to) ?? ''];
  return join(from ? `с главы: ${from}` : undefined, to ? `до главы: ${to}` : undefined) || undefined;
}

function books(args: Record<string, unknown>, one: string, many: string) {
  const list = Array.isArray(args.books) ? args.books.filter((book): book is number => Number.isInteger(book)) : [];
  if (!list.length) return undefined;
  return list.length === 1 ? `${one} ${list[0]}` : `${many} ${list.join(', ')}`;
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
