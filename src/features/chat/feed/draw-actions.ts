import type { Places } from '../../../shared/text/place';

/**
 * The drawing tools of the chat (illustrations UC1, UC2): draw, look at a drawing, set a reference. Only saga.image is
 * left since the drawing workshop; the captions of the others stay so that the old chats read
 * (specs/2026-11-02-image-workshop.md IW7.2).
 */
export type DrawTool = 'draw' | 'image' | 'reference';

export const drawTools: readonly DrawTool[] = ['draw', 'image', 'reference'];

/** The tools of the helper of the drawing workshop (specs/2026-11-02-image-workshop.md IW4.3). */
export type WorkshopTool = 'state' | 'describe' | 'edit' | 'look';

export const workshopTools: readonly WorkshopTool[] = ['state', 'describe', 'edit', 'look'];

/** What a drawing action shows: its drawing's number once the tool told it, and what is drawn. */
export interface DrawActionFacts {
  tool: DrawTool | WorkshopTool;
  /** The region an edit of the helper changes, in parts of the drawing (IW3.4). */
  region?: { x: number; y: number; w: number; h: number };
  /** How many edit candidates the edit asked for (IW3.5). */
  readings?: number;
  /** The number K of the drawing, from the arguments or the first line of the result. */
  image?: number;
  /** The numbers of the variants saga.draw started, from the first line of its result (R21.9). */
  variants?: number[];
  subjects: string[];
  chapter?: string;
}

export interface DrawDescription {
  /** What the helper did, in the past tense (specs/2026-11-10-ux4-chat-waiting.md CW1.3). */
  label: string;
  /** What the helper does now. */
  activity: string;
  detail?: string;
  drawing: DrawActionFacts;
}

/**
 * Describes saga.draw, saga.image and saga.reference by their arguments and the first line of the result (R1.6) in the
 * voice of the helper: «Рисую: Мирон у костра» — «Нарисовал: Мирон у костра», «Смотрю рисунок», «Поставил пример:
 * Мирон · с главы: Книга 7, гл. 300». The chapter a reference holds from is named by its place (`places` of the event).
 */
export function describeDrawAction(tool: DrawTool, args: Record<string, unknown> | undefined, summary: string | undefined, places: Places = {}): DrawDescription {
  const subjects = strings(args?.subjects);
  const chapter = text(args?.chapter);
  const image = integer(args?.image) ?? integer(args?.base_image) ?? summaryImage(summary);
  const facts: DrawActionFacts = { tool, subjects, ...(chapter ? { chapter } : {}), ...(image ? { image } : {}) };
  const detail = summaryDetail(summary);
  if (tool === 'image') {
    // The number of a drawing is a word of the device: the card beside the line shows which drawing it is
    // (specs/2026-11-12-ux12-drawings.md DW4.2).
    const what = 'рисунок';
    // R21.17: the variant the author named in words is chosen, as with the button.
    const [label, activity] = args?.choose === true ? ['Выбрал вариант', 'Выбираю вариант'] : [`Посмотрел ${what}`, `Смотрю ${what}`];
    return withDetail({ label, activity, drawing: facts }, detail);
  }
  if (tool === 'reference') {
    const subject = text(args?.subject) ?? subjects[0] ?? '';
    const from = text(args?.from_chapter);
    const where = !from ? ' · с начала' : places[from] ? ` · с главы: ${places[from]}` : '';
    const [label, activity] = args?.remove === true ? [`Снял пример: ${subject}${where}`, `Снимаю пример: ${subject}${where}`]
      : [`Поставил пример: ${subject}${where}`, `Ставлю пример: ${subject}${where}`];
    return { label, activity, drawing: { ...facts, subjects: subject ? [subject] : subjects, image: integer(args?.image) ?? facts.image } };
  }
  const base = integer(args?.base_image);
  // A new drawing is named by the caption the agent gave it (R8.17); an edit by what it changes.
  const caption = base ? undefined : text(args?.title);
  const scene = caption ?? (shorten(firstSentence(text(args?.prompt) ?? ''), 64) || subjects.join(', ') || 'рисунок');
  const [label, activity] = base && args?.region ? [`Поправил часть рисунка: ${scene}`, `Правлю часть рисунка: ${scene}`]
    : base ? [`Поправил рисунок: ${scene}`, `Правлю рисунок: ${scene}`] : [`Нарисовал: ${scene}`, `Рисую: ${scene}`];
  const variants = summaryVariants(summary);
  return withDetail({
    label, activity,
    // The base of an edit is not the drawing of this action once it names its variants.
    drawing: { ...facts, image: summaryImage(summary) ?? (base && args?.region ? base : undefined), ...(variants ? { variants } : {}) },
  }, detail);
}

/**
 * Describes drawing.state, drawing.describe, drawing.edit and drawing.look of the helper of «Рисование» by their
 * arguments and the first line of the result: «Смотрю рисунок», «Правлю описание», «Поправил рисунок: два рога».
 */
export function describeWorkshopAction(tool: WorkshopTool, args: Record<string, unknown> | undefined, summary: string | undefined): DrawDescription | Omit<DrawDescription, 'drawing'> {
  const line = summary?.trim();
  if (tool === 'state') return { label: 'Посмотрел рисунок', activity: 'Смотрю рисунок' };
  if (tool === 'describe') return { label: 'Поправил описание', activity: 'Правлю описание', drawing: { tool, subjects: strings(args?.subjects) } };
  if (tool === 'look') {
    const number = integer(args?.image);
    const what = 'рисунок';
    const [label, activity] = args?.check === true ? [`Сверил ${what} с книгой`, `Сверяю ${what} с книгой`] : [`Посмотрел ${what}`, `Смотрю ${what}`];
    return { label, activity, drawing: { tool, subjects: [], ...(number ? { image: number } : {}) } };
  }
  const instruction = text(args?.instruction) ?? '';
  const readings = Array.isArray(args?.readings) ? args.readings.length : 0;
  const region = regionOf(args?.region);
  const [label, activity] = readings > 1
    ? [`Предложил варианты правки (${readings}): ${shorten(firstSentence(instruction), 56)}`, `Предлагаю варианты правки (${readings}): ${shorten(firstSentence(instruction), 56)}`]
    : [`Поправил рисунок: ${shorten(firstSentence(instruction), 64)}`, `Правлю рисунок: ${shorten(firstSentence(instruction), 64)}`];
  const ready = line ? /^Рисунок (\d+): правка (\d+) готова/u.exec(line) : null;
  const drawing = line ? /^Правка (\d+) рисунка (\d+) рисуется/u.exec(line) : null;
  const image = ready ? Number(ready[1]) : drawing ? Number(drawing[2]) : undefined;
  return withDetail({
    label, activity,
    drawing: { tool, subjects: [], ...(image ? { image } : {}), ...(region ? { region } : {}), ...(readings > 1 ? { readings } : {}) },
  }, ready ? 'правка готова' : drawing ? 'правка рисуется' : undefined);
}

/**
 * What the painter said when it refused («Модель изображений отказалась рисовать рисунок 44 по своим правилам:
 * жестокость. Смягчи указание…»): the reason is the author's, the advice to the model is not.
 */
export function painterRefusal(summary: string | undefined) {
  const reason = summary ? /отказалась[^:]*по своим правилам:\s*([^.]+)/u.exec(summary)?.[1]?.trim() : undefined;
  return reason ? `помощник не стал рисовать по правилам сервиса: ${reason}` : undefined;
}

function regionOf(value: unknown) {
  if (!value || typeof value !== 'object') return undefined;
  const { x, y, w, h } = value as Record<string, unknown>;
  return [x, y, w, h].every((part) => typeof part === 'number') ? { x: x as number, y: y as number, w: w as number, h: h as number } : undefined;
}

/** «Рисунок 42 готов: …», «Рисунок 42 рисуется …», «Рисунок 42: правка области 1 готова — …». */
function summaryImage(summary: string | undefined) {
  const match = summary ? /^Рисунок (\d+)\b/u.exec(summary.trim()) : null;
  return match ? Number(match[1]) : undefined;
}

/** «Варианты рисунка: 12, 13, 14.» */
function summaryVariants(summary: string | undefined) {
  const match = summary ? /^Варианты рисунка: (\d+(?:, \d+)*)\./u.exec(summary.trim()) : null;
  return match ? match[1]!.split(', ').map(Number) : undefined;
}

function summaryDetail(summary: string | undefined) {
  const line = summary?.trim();
  if (!line) return undefined;
  if (summaryVariants(line)) return 'варианты для выбора';
  const ready = /^Рисунок (\d+) готов/u.exec(line);
  if (ready) return 'рисунок готов';
  const drawing = /^Рисунок (\d+) рисуется/u.exec(line);
  if (drawing) return 'рисунок рисуется';
  const layer = /^Рисунок (\d+): правка области (\d+) готова/u.exec(line);
  if (layer) return 'правка рисунка готова';
  if (/^Рисунок \d+ выбран автором/u.test(line)) return 'вариант выбран';
  return undefined;
}

function withDetail<T extends Omit<DrawDescription, 'drawing'>>(described: T, detail: string | undefined): T {
  return detail ? { ...described, detail } : described;
}

function firstSentence(value: string) {
  const sentence = /^(.+?[.!?…])(\s|$)/u.exec(value);
  return (sentence?.[1] ?? value).replace(/[.!?…]+$/u, '');
}

function shorten(value: string, limit: number) {
  if (value.length <= limit) return value;
  const space = value.slice(0, limit + 1).lastIndexOf(' ');
  return `${value.slice(0, space > limit * 0.6 ? space : limit).trimEnd()}…`;
}

function text(value: unknown) {
  return typeof value === 'string' && value.trim() ? value.trim() : undefined;
}

function strings(value: unknown) {
  return Array.isArray(value) ? value.filter((item): item is string => typeof item === 'string' && item.trim() !== '') : [];
}

function integer(value: unknown) {
  return typeof value === 'number' && Number.isInteger(value) && value > 0 ? value : undefined;
}
