import { z } from 'zod';
import { operationManifest } from './generated/operation-manifest';
import type {
  AgentTimelineEvent,
  AgentTimelineContext,
  AgentTimelineDelta,
  AgentTimelineSnapshot,
  AgentTimelineStreamEnvelope,
  CanonKeeperApiClient,
} from './contracts';

export type { AgentTimelineEvent, AgentTimelineContext, AgentTimelineDelta, AgentTimelineSnapshot, AgentTimelineStreamEnvelope };

export type TimelineCursor = string;
export type TimelineApiClient = Pick<CanonKeeperApiClient, 'getAgentTimeline'>;

export type TimelinePage = AgentTimelineSnapshot;

const cursorSchema = z.string().regex(/^tl_[A-Za-z0-9_-]+$/u);
const eventSchema = z.object({
  id: z.string().min(1),
  cursor: cursorSchema,
  kind: z.enum(['user', 'steer', 'reasoning', 'assistant', 'tool_request', 'tool_result', 'plan', 'subagent', 'turn_outcome']),
  agentPath: z.string().min(1).optional(),
  agentThreadId: z.string().min(1).optional(),
  jobId: z.string().min(1),
  role: z.enum(['main', 'completeness', 'source_evidence', 'saga_history']),
  providerTurnId: z.string().optional(),
  providerItemId: z.string().optional(),
  logicalSection: z.number().int().nonnegative(),
  text: z.string().optional(),
  // The gallery images attached to a question (illustrations R15.9).
  images: z.array(z.number().int().positive()).max(4).optional(),
  // A correction sent into the running turn (specs/2026-10-30-chat-steering.md R3.1).
  firstPhoto: z.number().int().positive().optional(),
  steerId: z.string().min(1).optional(),
  // The chapter of the author attached to a question (specs/2026-11-07-chat-attach-chapter.md CA4.1).
  attachment: z.object({
    number: z.number().int().min(1).max(999),
    title: z.string(),
    words: z.number().int().positive(),
  }).strict().optional(),
  // A button of the page sent the question (specs/2026-11-11-ux9-writing-b.md WB3.3).
  button: z.boolean().optional(),
  phase: z.string().optional(),
  tool: z.object({
    namespace: z.string().optional(),
    name: z.string().min(1),
    arguments: z.record(z.string(), z.unknown()).optional(),
    status: z.string().optional(),
    success: z.boolean().optional(),
    durationMs: z.number().int().nonnegative().optional(),
    // Counted in characters like JSON Schema maxLength and the backend, not UTF-16 units.
    summary: z.string().refine((text) => [...text].length <= 240).optional(),
  }).strict().optional(),
  // The places of the chapter keys the event shows, as the author reads them (specs/2026-11-10-ux1-names.md N2.1).
  places: z.record(z.string(), z.string()).optional(),
  outcome: z.enum(['in_progress', 'completed', 'failed', 'interrupted']).optional(),
}).strict();

const snapshotSchema = z.object({
  events: z.array(eventSchema),
  eof: z.boolean(),
  nextCursor: cursorSchema.nullable().optional(),
  needsReconcile: z.boolean(),
}).strict();

const deltaSchema = z.object({
  id: z.string().min(1),
  kind: z.enum(['assistant', 'reasoning']),
  jobId: z.string().min(1),
  role: z.enum(['main', 'completeness', 'source_evidence', 'saga_history']),
  providerTurnId: z.string().min(1),
  providerItemId: z.string().min(1),
  logicalSection: z.number().int().nonnegative(),
  text: z.string(),
}).strict();

const contextSchema = z.object({
  contextWindow: z.number().int().positive().optional(),
  providerTurnId: z.string().min(1),
  role: z.enum(['main', 'completeness', 'source_evidence', 'saga_history']),
  usedTokens: z.number().int().nonnegative().optional(),
}).strict();

export const agentTimelineStreamEnvelopeSchema = z.discriminatedUnion('type', [
  z.object({ type: z.literal('snapshot'), cursor: cursorSchema.optional(), snapshot: snapshotSchema }).strict(),
  z.object({ type: z.literal('event'), cursor: cursorSchema, event: eventSchema }).strict(),
  z.object({ type: z.literal('delta'), cursor: cursorSchema, delta: deltaSchema }).strict(),
  z.object({ type: z.literal('context'), context: contextSchema }).strict(),
  z.object({ type: z.literal('reconnecting'), cursor: cursorSchema.optional(), message: z.string().optional() }).strict(),
  z.object({ type: z.literal('error'), cursor: cursorSchema.optional(), message: z.string() }).strict(),
]);

export type ParsedAgentTimelineEnvelope = z.infer<typeof agentTimelineStreamEnvelopeSchema>;

export function parseAgentTimelineStreamEnvelope(value: string) {
  try {
    return agentTimelineStreamEnvelopeSchema.safeParse(JSON.parse(value));
  } catch {
    return agentTimelineStreamEnvelopeSchema.safeParse(null);
  }
}

export function timelinePageIsEof(page: TimelinePage) {
  if (page.eof === undefined) return false;
  const nextCursor = timelinePageNextCursor(page);
  if (page.eof && nextCursor !== null) {
    throw new Error('Timeline page cannot carry nextCursor after EOF.');
  }
  if (!page.eof && nextCursor === null) {
    throw new Error('Timeline page must carry nextCursor before EOF.');
  }
  return page.eof;
}

export function timelinePageNextCursor(page: TimelinePage): TimelineCursor | null {
  const cursor = page.nextCursor;
  return cursor === undefined || cursor === null ? null : cursor;
}

export function agentTimelineWebSocketUrl(
  projectId: string,
  jobId: string,
  baseUrl = '/api/v1',
  locationHref = globalThis.location.href,
) {
  const apiBase = new URL(`${baseUrl.replace(/\/$/u, '')}/`, locationHref);
  const route = operationManifest.streamAgentTimeline.path
    .replace('{projectId}', encodeURIComponent(projectId))
    .replace('{jobId}', encodeURIComponent(jobId));
  const url = new URL(route.replace(/^\//u, ''), apiBase);
  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:';
  return url.toString();
}

export const streamAgentTimelineUrl = agentTimelineWebSocketUrl;

export interface OpenAgentTimelineStreamOptions {
  baseUrl?: string;
  createSocket?: (url: string) => WebSocket;
  locationHref?: string;
}

export function openAgentTimelineStream(
  projectId: string,
  jobId: string,
  options: OpenAgentTimelineStreamOptions = {},
) {
  const url = agentTimelineWebSocketUrl(projectId, jobId, options.baseUrl, options.locationHref);
  return (options.createSocket ?? ((streamUrl: string) => new WebSocket(streamUrl)))(url);
}
