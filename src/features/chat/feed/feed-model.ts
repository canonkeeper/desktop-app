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
}

type ToolEvent = CanonicalAgentTimelineEvent & { kind: 'tool_request' | 'tool_result'; tool: NonNullable<CanonicalAgentTimelineEvent['tool']> };

/**
 * Builds the turns of a chat from its timeline in server order. Reasoning, plans and subagent
 * activity give no rows: the author reads the agent remarks, the actions after each remark and
 * the answer. A finished turn answers with its last remark; a live or unfinished one only with a
 * remark that reads like an answer and has no actions after it. `ended` is how the Job of the
 * last turn ended without success: a turn a restart cut before its native outcome is unfinished.
 */
export function buildFeedTurns(events: readonly CanonicalAgentTimelineEvent[], active: boolean, ended?: 'failed' | 'canceled'): FeedTurn[] {
  const groups: CanonicalAgentTimelineEvent[][] = [];
  for (const event of events) {
    if (event.kind === 'user' || groups.length === 0) groups.push([]);
    groups.at(-1)!.push(event);
  }
  return groups.map((group, index) => index === groups.length - 1 ? buildTurn(group, active, ended) : buildTurn(group, false));
}

function buildTurn(events: readonly CanonicalAgentTimelineEvent[], lastOfActiveJob: boolean, ended?: 'failed' | 'canceled'): FeedTurn {
  const question = events.find((event) => event.kind === 'user');
  const outcome = lastMatching(events, (event) => event.kind === 'turn_outcome');
  const live = lastOfActiveJob && !outcome;
  const unfinished = outcome?.outcome === 'failed' || outcome?.outcome === 'interrupted' ? outcome.outcome
    : !outcome && ended ? (ended === 'canceled' ? 'interrupted' : 'failed') : undefined;
  const work = events.filter((event) => event.kind === 'assistant' || event.kind === 'reasoning' || event.kind === 'steer' || isToolEvent(event));
  const answer = turnAnswer(work, !live && !unfinished);
  const lateSteers = !live && answer ? work.slice(work.indexOf(answer) + 1).filter((event) => event.kind === 'steer') : [];
  // The remark a live turn is on stays out of the steps, in the place its answer will take.
  const tail = live && !answer ? openRemark(work) : undefined;

  const steps: FeedStep[] = [];
  const actions = new Map<string, FeedAction>();
  for (const event of work) {
    if (event.kind === 'assistant') {
      if (event !== answer && event !== tail) steps.push({ key: eventKey(event), event, actions: [] });
      continue;
    }
    if (event.kind === 'steer') {
      if (!lateSteers.includes(event)) steps.push({ key: eventKey(event), steer: event, actions: [] });
      continue;
    }
    if (!isToolEvent(event)) continue;
    const key = event.providerItemId ?? event.id;
    const action: FeedAction = { key, status: toolStatus(event), description: describeSagaAction(event.tool, toolStatus(event)) };
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
  };
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

/** The steps of the agent; the corrections of the author are counted apart. */
export function agentStepCount(turn: FeedTurn) {
  return turn.steps.filter((step) => !step.steer).length;
}

export function steerCount(turn: FeedTurn) {
  return turn.steps.filter((step) => step.steer).length;
}

export function actionCount(turn: FeedTurn) {
  return turn.steps.reduce((count, step) => count + step.actions.length, 0);
}

/** What a live turn is doing now, in the present tense. */
/**
 * What the live turn is doing now. A question still waiting in the queue shows its place instead
 * of «Начинает работу» (specs/2026-10-28-writer-quotas-and-fair-queue.md R10.4).
 */
export function feedStatus(turn: FeedTurn, queuePosition?: number | null) {
  if (turn.activity === 'starting' && typeof queuePosition === 'number') return queueStatus(queuePosition);
  if (turn.activity === 'acting' && turn.current) return turn.current.description.activity;
  if (turn.activity === 'writing') return 'Пишет ответ';
  if (turn.activity === 'starting') return 'Начинает работу';
  return turn.steps.length || turn.tail ? 'Обдумывает следующий шаг' : 'Обдумывает вопрос';
}

export function queueStatus(position: number) {
  if (position <= 0) return 'В очереди: ваш вопрос следующий';
  return `В очереди: перед вами ${position} ${pluralize(position, 'вопрос', 'вопроса', 'вопросов')}`;
}
