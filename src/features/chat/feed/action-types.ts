import type { SourceCitation } from '../../../shared/navigation/chat-source';
import type { DrawActionFacts } from './draw-actions';

export type ActionKind = 'search' | 'read' | 'chapters' | 'glossary' | 'entity' | 'timeline' | 'threads' | 'changes' | 'scan' | 'remember' | 'memory' | 'draw' | 'other';
export type ActionStatus = 'running' | 'done' | 'error';

/**
 * One tool call as the author reads it, in the voice of the helper about the books of the author
 * (specs/2026-11-10-ux4-chat-waiting.md CW1.1): what it does now, what it did, what came back, where it leads.
 */
export interface ActionDescription {
  kind: ActionKind;
  /** What the helper did, in the past tense, for a done line: «Искал «пожар», «дым» · до главы: Книга 2, гл. 16 „Журнал Савы“». */
  label: string;
  /**
   * What the helper does, in the present tense, for a running line, the status of the turn and a line that did not work
   * («Читаю: … — не получилось, пробую иначе»): «Ищу в книгах: «пожар»».
   */
  activity: string;
  /** What came back — «14 мест в 6 главах» — or that a failed call is tried another way. */
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
