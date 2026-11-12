import type { ReactNode, RefObject } from 'react';
import type { PendingSteer } from '../../../entities/chat/steer';
import type { SourceCitation } from '../../../shared/navigation/chat-source';
import type { MarkdownImage, WorkshopLinkTarget } from '../../../ui/MarkdownContent';
import type { FeedAction, FeedTurn } from './feed-model';
import type { TurnStop } from './LiveStatus';
import type { AgentJobStatus, CanonicalAgentTimelineEvent } from './types';

/** What the page gives the feed of the chat and of the helpers of «Письмо» and «Рисование». */
export interface ChatFeedProps {
  className?: string;
  emptyMessage?: string;
  /** The failure block under the last turn: what happened, what to do and the support code. */
  error?: ReactNode;
  errorActions?: ReactNode;
  errorLabel?: string;
  events?: readonly CanonicalAgentTimelineEvent[];
  /** What stands under the answer of a turn, by the native turns of its events: the notes a workshop's helper wrote
   *  in it (specs/2026-11-02-writer-workshop.md W4.5). */
  belowAnswer?: (turnIds: readonly string[]) => ReactNode;
  /** What follows the last turn, such as the style choice of the first drawing (illustrations R8.6). */
  footer?: ReactNode;
  /** What stands before the first turn in the same scroll, such as the notes of a workshop's helper (W4.5). */
  header?: ReactNode;
  jobStatus?: AgentJobStatus | null;
  notice?: string;
  /** A quiet line under the status of the running turn: the stream broke and mends itself (AA2.6). */
  liveNote?: string | undefined;
  onOpenSource?: (source: SourceCitation, trigger: HTMLElement) => void;
  /** A question just sent whose turn has not reached the timeline yet. */
  pendingQuestion?: string;
  /** Corrections the server took that the timeline does not show yet (specs/2026-10-30-chat-steering.md R3.5). */
  pendingSteers?: readonly PendingSteer[];
  /** Puts a correction the end of the turn left unread into the question field (R3.6). */
  onAskSteer?: (text: string) => void;
  /** The place of the waiting question in the fair queue (getJob of a queued turn). */
  queuePosition?: number | null;
  /**
   * What stands under an answer: its buttons and, under the last one, the questions the helper offered at its end
   * (specs/2026-11-11-ux4-after-answer.md AA1.2).
   */
  renderAssistantActions?: (event: CanonicalAgentTimelineEvent, contentRef: RefObject<HTMLElement | null>, answer: { continuations: readonly string[] }) => ReactNode;
  renderSourceLink?: (event: CanonicalAgentTimelineEvent, source: SourceCitation, label: ReactNode) => ReactNode;
  /** Draws `![подпись](image:K)` of a remark or an answer: a drawing of the saga (R8.1). */
  renderImage?: (image: MarkdownImage) => ReactNode;
  /** Draws `drawing:new` and `drawing:K` of a remark or an answer with the question of its turn (image-workshop IW7.3). */
  renderWorkshopLink?: (target: WorkshopLinkTarget, label: ReactNode, question: string) => ReactNode;
  /** Draws the images attached to a question under it (illustrations R15.9). */
  renderQuestionImages?: (numbers: readonly number[]) => ReactNode;
  /** Draws the card of the chapter attached to a question under it (specs/2026-11-07-chat-attach-chapter.md CA4.2). */
  renderQuestionAttachment?: (attachment: NonNullable<CanonicalAgentTimelineEvent['attachment']>) => ReactNode;
  /** Draws the images of a correction with their «фото N» from `first` (R3.1). */
  renderSteerImages?: (numbers: readonly number[], first: number) => ReactNode;
  /** What a drawing action of a step shows under its line: the drawing as it develops (R8.1). */
  renderActionDrawing?: (action: FeedAction, live: boolean, turn: FeedTurn) => ReactNode;
  /**
   * What stands in the line «Запомнил: …» under the answer of a turn: its undo and the way to the records
   * (specs/2026-11-02-writer-workshop.md, Decision log 03.11; specs/2026-11-10-ux1-chat.md CH4).
   */
  renderActionRemembered?: (action: FeedAction, turn: FeedTurn) => ReactNode;
  /**
   * The line of a question a button of the page sent (`button` of the question), in place of the author's bubble: «Вы нажали
   * «Написать главу»» (specs/2026-11-11-ux9-writing-b.md WB3.3); without it a press shows as the question it sent.
   */
  pressedLine?: (text: string) => string;
  /** The question just sent that the timeline does not show yet came from a button of the page (WB3.3). */
  pendingPressed?: boolean;
  /** When the Job of the live turn was created, for the time it runs. */
  startedAt?: string;
  /** «Остановить» of the running turn in its status (specs/2026-11-10-ux4-chat-waiting.md CW2.4); a helper keeps its own at the field. */
  stop?: TurnStop;
  /** «Сообщить, когда будет готово» beside the time of a running turn: the chat watches its turns (CW2.6). */
  notify?: boolean;
  /** The feed of a helper of «Письмо» or «Рисование» (CW1.3): «Как я работал», a message instead of a question. */
  helper?: boolean;
  /** «Повторить вопрос» of a stopped or failed turn puts its question into the field (CW2.8). */
  onRepeatQuestion?: (text: string) => void;
  /** The block of the page for the end of the last turn — the failure of its Job with its code (CW2.8). */
  turnEnd?: ReactNode;
  viewportClassName?: string;
}
