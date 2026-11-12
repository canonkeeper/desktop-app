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
  /** A button of the page sent the question, not the author typing it (specs/2026-11-11-ux9-writing-b.md WB3.3). */
  button?: boolean;
  /**
   * The places of the chapters whose keys the event shows — the arguments of its tool, the source links of its text —
   * as the author reads them (specs/2026-11-10-ux1-names.md N2.1).
   */
  places?: Readonly<Record<string, string>>;
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
