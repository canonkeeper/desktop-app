export type AgentTimelineEventKind =
  | 'user'
  | 'steer'
  | 'reasoning'
  | 'assistant'
  | 'tool_request'
  | 'tool_result'
  | 'plan'
  | 'subagent'
  | 'turn_outcome';

export type AgentTimelineRole = 'main' | 'completeness' | 'source_evidence' | 'saga_history';

/** One event of the chat timeline in server cursor order. */
export interface CanonicalAgentTimelineEvent {
  agentPath?: string;
  agentThreadId?: string;
  cursor: number | string;
  id: string;
  jobId: string;
  kind: AgentTimelineEventKind;
  logicalSection: number;
  outcome?: 'in_progress' | 'completed' | 'failed' | 'interrupted';
  phase?: string;
  providerItemId?: string;
  providerTurnId?: string;
  role: AgentTimelineRole;
  text?: string;
  /** The gallery images the author attached to a question (illustrations R15.9). */
  images?: readonly number[];
  /** A correction sent into the running turn: «фото N» of its first image and the id of the send. */
  firstPhoto?: number;
  steerId?: string;
  /** The chapter of the author attached to a question (specs/2026-11-07-chat-attach-chapter.md CA4.1). */
  attachment?: { number: number; title: string; words: number };
  tool?: {
    arguments?: Record<string, unknown>;
    durationMs?: number;
    name: string;
    namespace?: string;
    status?: string;
    success?: boolean;
    summary?: string;
  };
}

export type AgentJobStatus = 'queued' | 'running' | 'canceling' | 'succeeded' | 'failed' | 'canceled';
