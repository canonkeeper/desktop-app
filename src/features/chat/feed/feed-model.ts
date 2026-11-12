import { pluralize } from '../../../ui/copy';
import { describeSagaAction, type ActionDescription, type ActionStatus } from './saga-actions';
import type { CanonicalAgentTimelineEvent } from './types';

/** One tool call of a turn. */
export interface FeedAction {
  key: string;
  status: ActionStatus;
  description: ActionDescription;
}

/**
 * An agent remark or a correction of the author, with the actions after it; the first step of a
 * turn may have neither.
 */
export interface FeedStep {
  key: string;
  event?: CanonicalAgentTimelineEvent;
  steer?: CanonicalAgentTimelineEvent;
  actions: FeedAction[];
}

export type FeedActivity = 'starting' | 'thinking' | 'acting' | 'writing';

/** A question with the work of the agent and its answer. */
export interface FeedTurn {
  key: string;
  question?: CanonicalAgentTimelineEvent;
  steps: FeedStep[];
  answer?: CanonicalAgentTimelineEvent;
  /** The latest remark of a live turn with no actions after it yet; not a step until they come. */
  tail?: CanonicalAgentTimelineEvent;
  outcome?: CanonicalAgentTimelineEvent;
  /** How a turn ended without an answer: it failed or was stopped. */
  unfinished?: 'failed' | 'interrupted';
  /** The turn the agent is still working on. */
  live: boolean;
  activity: FeedActivity;
  /** The call a live turn is waiting for. */
  current?: FeedAction;
  /** Corrections the end of the turn outran: written after its answer, the model never read them. */
  lateSteers: CanonicalAgentTimelineEvent[];
  /** The native turns its events came from: what a workshop's helper wrote in them stands under its answer (W4.5). */
  turnIds: string[];
  /**
   * The questions the helper of the chat offered at the end of its answer, without the block in the answer
   * (specs/2026-11-11-ux4-after-answer.md AA1.2): the feed draws them as buttons under the answer.
   */
  continuations?: readonly string[];
}

/** What the feed reads out of the timeline: the continuations of an answer only in the feed of the chat. */
export interface FeedOptions {
  continuations?: boolean;
}

type ToolEvent = CanonicalAgentTimelineEvent & { kind: 'tool_request' | 'tool_result'; tool: NonNullable<CanonicalAgentTimelineEvent['tool']> };

/**
 * Builds the turns of a chat from its timeline in server order. Reasoning, plans and subagent
 * activity give no rows: the author reads the agent remarks, the actions after each remark and
 * the answer. A finished turn answers with its last remark; a live or unfinished one only with a
 * remark that reads like an answer and has no actions after it. `ended` is how the Job of the
 * last turn ended without success: a turn a restart cut before its native outcome is unfinished.
 */
export function buildFeedTurns(events: readonly CanonicalAgentTimelineEvent[], active: boolean, ended?: 'failed' | 'canceled', options: FeedOptions = {}): FeedTurn[] {
  const groups: CanonicalAgentTimelineEvent[][] = [];
  for (const event of events) {
    if (event.kind === 'user' || groups.length === 0) groups.push([]);
    groups.at(-1)!.push(event);
  }
  // The chapters the author attached to the questions of the chat, by their number: the work names them by title (AA4.5).
  const chapters = new Map(events.flatMap((event) => event.kind === 'user' && event.attachment?.title ? [[`a${event.attachment.number}`, event.attachment.title] as const] : []));
  const turnOptions = { ...options, chapters };
  return groups.map((group, index) => index === groups.length - 1 ? buildTurn(group, active, turnOptions, ended) : buildTurn(group, false, turnOptions));
}

function buildTurn(events: readonly CanonicalAgentTimelineEvent[], lastOfActiveJob: boolean, options: FeedOptions & { chapters: ReadonlyMap<string, string> }, ended?: 'failed' | 'canceled'): FeedTurn {
  const question = events.find((event) => event.kind === 'user');
  const outcome = lastMatching(events, (event) => event.kind === 'turn_outcome');
  const live = lastOfActiveJob && !outcome;
  const unfinished = outcome?.outcome === 'failed' || outcome?.outcome === 'interrupted' ? outcome.outcome
    : !outcome && ended ? (ended === 'canceled' ? 'interrupted' : 'failed') : undefined;
  const work = events.filter((event) => event.kind === 'assistant' || event.kind === 'reasoning' || event.kind === 'steer' || isToolEvent(event));
  const written = turnAnswer(work, !live && !unfinished);
  // The block of continuations belongs to the finished answer of the chat; anywhere else it stays text of the answer.
  const offered = written && options.continuations && !live && !unfinished ? continuationsOf(written) : null;
  const answer = offered?.answer ?? written;
  const lateSteers = !live && written ? work.slice(work.indexOf(written) + 1).filter((event) => event.kind === 'steer') : [];
  // The remark a live turn is on stays out of the steps, in the place its answer will take.
  const tail = live && !answer ? openRemark(work) : undefined;

  const steps: FeedStep[] = [];
  const actions = new Map<string, FeedAction>();
  for (const event of work) {
    if (event.kind === 'assistant') {
      if (event !== written && event !== tail) steps.push({ key: eventKey(event), event, actions: [] });
      continue;
    }
    if (event.kind === 'steer') {
      if (!lateSteers.includes(event)) steps.push({ key: eventKey(event), steer: event, actions: [] });
      continue;
    }
    if (!isToolEvent(event)) continue;
    const key = event.providerItemId ?? event.id;
    const action: FeedAction = { key, status: toolStatus(event), description: namedChapter(describeSagaAction(event.tool, toolStatus(event), event.places), options.chapters) };
    const known = actions.get(key);
    if (known) {
      Object.assign(known, action);
      continue;
    }
    actions.set(key, action);
    if (steps.length === 0) steps.push({ key: `${eventKey(event)}:start`, actions: [] });
    steps.at(-1)!.actions.push(action);
  }

  const last = work.at(-1);
  const current = live ? lastMatching([...actions.values()], (action) => action.status === 'running') : undefined;
  const activity: FeedActivity = !live ? 'thinking'
    : current ? 'acting'
      : answer && last === answer ? 'writing'
        : last ? 'thinking' : 'starting';
  return {
    key: question ? eventKey(question) : events[0] ? eventKey(events[0]) : 'empty',
    steps,
    live,
    activity,
    lateSteers,
    turnIds: [...new Set(events.flatMap((event) => event.providerTurnId ? [event.providerTurnId] : []))],
    ...(question ? { question } : {}),
    ...(answer ? { answer } : {}),
    ...(tail ? { tail } : {}),
    ...(outcome ? { outcome } : {}),
    ...(unfinished ? { unfinished } : {}),
    ...(current ? { current } : {}),
    ...(offered ? { continuations: offered.continuations } : {}),
  };
}

/** The line that opens the block of continuations, word for word (AA1.1). */
export const continuationsHeading = 'Дальше можно спросить:';
const continuationsAtMost = 3;
/** The skill asks for 120 signs; the feed takes a little more before the block stops being one (AA1.2). */
const continuationSigns = 140;
const listItem = /^(?:[-*+]|\d{1,2}[.)])\s+(\S.*)$/u;

// One split per remark: an unchanged answer keeps its object, so its markdown is not drawn anew on every delta.
const offeredByRemark = new WeakMap<CanonicalAgentTimelineEvent, { answer: CanonicalAgentTimelineEvent; continuations: readonly string[] } | null>();

/**
 * The block the helper ends an answer with when the talk has a natural next step (AA1.1, AA1.2): the line «Дальше можно
 * спросить:» and one to three items of a list after it, nothing after them. Any other form is no block: the text stays
 * the answer as it is.
 */
function continuationsOf(remark: CanonicalAgentTimelineEvent) {
  if (offeredByRemark.has(remark)) return offeredByRemark.get(remark)!;
  const split = splitContinuations(remark.text ?? '');
  const offered = split ? { answer: { ...remark, text: split.answer }, continuations: split.continuations } : null;
  offeredByRemark.set(remark, offered);
  return offered;
}

export function splitContinuations(text: string): { answer: string; continuations: string[] } | null {
  const lines = text.replace(/\s+$/u, '').split('\n').map((line) => line.trimEnd());
  const heading = lines.lastIndexOf(continuationsHeading);
  if (heading < 0) return null;
  let first = heading + 1;
  if (lines[first] === '') first += 1;
  const items = lines.slice(first).map((line) => listItem.exec(line)?.[1]?.trim());
  if (items.length === 0 || items.length > continuationsAtMost) return null;
  if (items.some((item) => !item || [...item].length > continuationSigns || item.includes(']('))) return null;
  const answer = lines.slice(0, heading).join('\n').trimEnd();
  return answer ? { answer, continuations: items as string[] } : null;
}

function turnAnswer(work: readonly CanonicalAgentTimelineEvent[], finished: boolean) {
  if (finished) return lastMatching(work, (event) => event.kind === 'assistant');
  const remark = openRemark(work);
  return remark && readsLikeAnswer(remark.text ?? '') ? remark : undefined;
}

/** The last remark when neither an action nor a correction came after it. */
function openRemark(work: readonly CanonicalAgentTimelineEvent[]) {
  const remark = lastMatching(work, (event) => event.kind === 'assistant');
  return remark && !work.slice(work.lastIndexOf(remark) + 1).some((event) => isToolEvent(event) || event.kind === 'steer') ? remark : undefined;
}

// Agent remarks between actions are one sentence (50–180 characters on TEST); answers run to
// hundreds of characters with paragraphs, headings or lists.
function readsLikeAnswer(text: string) {
  return text.length >= 280 || /\n\s*\n/u.test(text) || /^\s{0,3}(?:#{1,6}\s|[-*+]\s|\d+\.\s|\|)/mu.test(text);
}

/** «Читаю вашу главу „Брод“»: the reading of an attached chapter by the title the question carries (AA4.5, Ч-53). */
function namedChapter(description: ActionDescription, chapters: ReadonlyMap<string, string>): ActionDescription {
  const title = description.source ? chapters.get(description.source.chapterKey) : undefined;
  if (!title || !/вашу главу$/u.test(description.label)) return description;
  return { ...description, label: `${description.label} „${title}“`, activity: `${description.activity} „${title}“` };
}

function isToolEvent(event: CanonicalAgentTimelineEvent): event is ToolEvent {
  return (event.kind === 'tool_request' || event.kind === 'tool_result') && Boolean(event.tool);
}

function toolStatus(event: ToolEvent): ActionStatus {
  const { status, success } = event.tool;
  if (status === 'failed' || status === 'declined' || success === false || event.outcome === 'failed' || event.outcome === 'interrupted') return 'error';
  if (status === 'completed' || event.outcome === 'completed' || (event.kind === 'tool_result' && success === true)) return 'done';
  return 'running';
}

function lastMatching<T>(items: readonly T[], matches: (item: T) => boolean): T | undefined {
  for (let index = items.length - 1; index >= 0; index -= 1) {
    if (matches(items[index]!)) return items[index];
  }
  return undefined;
}

/** The React key of an event; a remark keeps it while it moves between step and answer draft. */
export function eventKey(event: CanonicalAgentTimelineEvent) {
  return `${String(event.cursor)}-${event.id}`;
}

export function steerCount(turn: FeedTurn) {
  return turn.steps.filter((step) => step.steer).length;
}

/**
 * The fold of a finished turn in the words of the work (specs/2026-11-10-ux4-chat-waiting.md CW2.7): how many chapters
 * the helper read and how often it searched — «прочитал 2 главы, искал 3 раза» — and the corrections of the author
 * apart; none of them — nothing but the title.
 */
export function workSummary(turn: FeedTurn) {
  const actions = turn.steps.flatMap((step) => step.actions).filter((action) => action.status === 'done');
  const chapters = new Set(actions.filter((action) => action.description.kind === 'read').map((action) => action.description.source?.chapterKey ?? action.key)).size;
  const searches = actions.filter((action) => action.description.kind === 'search' || action.description.kind === 'scan').length;
  const steers = steerCount(turn);
  const work = [
    chapters ? `прочитал ${chapters} ${pluralize(chapters, 'главу', 'главы', 'глав')}` : '',
    searches ? `искал ${searches} ${pluralize(searches, 'раз', 'раза', 'раз')}` : '',
  ].filter(Boolean).join(', ');
  return [work, steers ? `${steers} ${pluralize(steers, 'ваше уточнение', 'ваших уточнения', 'ваших уточнений')}` : ''].filter(Boolean);
}

/** One line of the work of a turn: a remark of the helper, a correction of the author or an action. */
function lineCount(step: FeedStep) {
  return (step.event || step.steer ? 1 : 0) + step.actions.length;
}

/**
 * The last lines of a running turn (CW2.3): the steps cut to the last `count` lines — a remark, a correction or an
 * action each — and how many lines stand before them.
 */
export function lastLines(steps: readonly FeedStep[], count: number): { hidden: number; steps: FeedStep[] } {
  const total = steps.reduce((sum, step) => sum + lineCount(step), 0);
  if (total <= count) return { hidden: 0, steps: [...steps] };
  const shown: FeedStep[] = [];
  let left = count;
  for (let index = steps.length - 1; index >= 0 && left > 0; index -= 1) {
    const step = steps[index]!;
    const actions = step.actions.slice(Math.max(0, step.actions.length - left));
    left -= actions.length;
    const head = left > 0 && (step.event || step.steer);
    if (head) left -= 1;
    shown.unshift({
      key: step.key, actions,
      ...(head && step.event ? { event: step.event } : {}),
      ...(head && step.steer ? { steer: step.steer } : {}),
    });
  }
  return { hidden: total - count, steps: shown };
}

/**
 * What the live turn is doing now, in the voice of the helper (CW1.1, table 4.1): its place in the queue while it waits
 * (specs/2026-10-28-writer-quotas-and-fair-queue.md R10.4; CW2.5), «Останавливаю…» once the author stopped it.
 */
export function feedStatus(turn: FeedTurn, queuePosition?: number | null, stopping = false) {
  if (stopping) return 'Останавливаю…';
  if (turn.activity === 'starting' && typeof queuePosition === 'number') return queueStatus(queuePosition);
  if (turn.activity === 'acting' && turn.current) return turn.current.description.activity;
  if (turn.activity === 'writing') return 'Пишу ответ';
  if (turn.activity === 'starting') return 'Начинаю';
  return turn.steps.length || turn.tail ? 'Думаю, где искать дальше' : 'Думаю над вопросом';
}

/**
 * The place of a waiting question by the questions before it (CW2.5): no time in minutes, the server does not count one,
 * and no word of the machinery (specs/2026-11-10-ux3-saga-state.md SS2.2).
 */
export function queueStatus(position: number) {
  if (position <= 0) return 'Ваш вопрос следующий — начну, когда освободится помощник';
  return `Перед вашим вопросом ещё ${position} — начну, когда освободится помощник`;
}
