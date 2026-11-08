import { useQueryClient } from '@tanstack/react-query';
import { isApiStatusFailure } from '../../entities/api-errors';
import type { ChatApiClient, ChatScope } from '../../entities/chat/api';
import { useTurnSteer } from '../../entities/chat/steer';
import type { CanonicalAgentTimelineEvent } from './feed/types';
import { invalidateEndedTurn } from './research-chat-lifecycle';

interface ResearchChatSteerOptions {
  api: Pick<ChatApiClient, 'steerChatTurn'>;
  /** The turn takes corrections now: it runs, is not being stopped, and the author may write. */
  active: boolean;
  chatId: string;
  turnId: string;
  jobId: string;
  events: readonly CanonicalAgentTimelineEvent[];
  scope: ChatScope;
  /** Whether what was started for this chat may still touch the page. */
  stillCurrent: (chatId: string, action: number) => boolean;
  action: () => number;
  clearFailure: () => void;
  onFailure: (error: unknown) => void;
  /** The correction went: the field forgets it, unless the author wrote something else meanwhile. */
  onSent: (message: string) => void;
  /** The turn ended before it took the correction. */
  onEnded: (jobId: string) => void;
}

/**
 * The correction of the running turn (specs/2026-10-30-chat-steering.md UC1, UC4): the draft goes only on success, and a
 * turn that ended meanwhile is read again. Steering takes no chapter: a chapter goes with a question
 * (specs/2026-11-07-chat-attach-chapter.md CA7.4).
 */
export function useResearchChatSteer(options: ResearchChatSteerOptions) {
  const { action, active, api, chatId, clearFailure, events, jobId, onEnded, onFailure, onSent, scope, stillCurrent, turnId } = options;
  const queryClient = useQueryClient();
  const steer = useTurnSteer({ active, api, chatId, events, turnId });

  async function send(message: string) {
    if (!message || steer.sending) return;
    const actionId = action();
    const sentChat = chatId;
    const sentJob = jobId;
    clearFailure();
    try {
      await steer.send(message, []);
      if (stillCurrent(sentChat, actionId)) onSent(message);
    } catch (error) {
      if (!stillCurrent(sentChat, actionId)) return;
      onFailure(error);
      if (isApiStatusFailure(error) && error.problem.code === 'chat.turn_ended') {
        onEnded(sentJob);
        void invalidateEndedTurn(queryClient, scope, sentChat, sentJob);
      }
    }
  }

  return { send, sending: steer.sending, waiting: steer.waiting };
}
