import { useQueryClient } from '@tanstack/react-query';
import { useRef, useState } from 'react';
import { isApiStatusFailure } from '../../entities/api-errors';
import { chatQueryKeys, type ChatApiClient, type ChatJob, type ChatScope } from '../../entities/chat/api';
import { invalidateEndedTurn, reconcileCanceledJob } from './research-chat-lifecycle';

const terminalStatuses = ['succeeded', 'failed', 'canceled'];

interface ResearchChatStopOptions {
  /** The API part the stop needs. */
  api: Pick<ChatApiClient, 'cancelJob' | 'getJob'>;
  /** Whether what was started for this chat may still touch the page. */
  stillCurrent: (chatId: string, action: number) => boolean;
  action: () => number;
  scope: ChatScope;
  onAuthorityLost: () => void;
  onEnded: (jobId: string) => void;
  onProgress: (job: ChatJob) => void;
  onFailure: (error: unknown) => void;
  clearFailure: () => void;
  /** Gives the question of the stopped turn back to the field. */
  restoreQuestion: (task: string) => void;
}

/**
 * Stopping a chat turn: the cancel, the reconciliation of its Job and the question that goes back to
 * the field once the turn really stopped.
 */
export function useResearchChatStop(options: ResearchChatStopOptions) {
  const { action, api, clearFailure, onAuthorityLost, onEnded, onFailure, onProgress, restoreQuestion, scope, stillCurrent } = options;
  const queryClient = useQueryClient();
  const [stopping, setStopping] = useState(false);
  const recovery = useRef<{ jobId: string; task: string } | null>(null);

  async function stop(input: { canCancel: boolean; chatId: string; jobId: string; task: string }) {
    const { canCancel, chatId, jobId, task } = input;
    if (!jobId || !canCancel || stopping) return;
    const actionId = action();
    const current = () => stillCurrent(chatId, actionId);
    recovery.current = { jobId, task };
    setStopping(true);
    clearFailure();
    let cancelFailure: unknown;
    try {
      let cancelResponse: ChatJob | null = null;
      try {
        cancelResponse = await api.cancelJob(jobId) ?? null;
      } catch (error) {
        cancelFailure = error;
        if (current() && isApiStatusFailure(error) && error.status === 403) onAuthorityLost();
      }
      if (!current()) return;
      const outcome = cancelResponse?.id === jobId && terminalStatuses.includes(cancelResponse.status)
        ? cancelResponse
        : await reconcileCanceledJob((id) => api.getJob(id), jobId);
      if (!current()) return;
      if (!outcome) {
        if (recovery.current?.jobId === jobId) {
          recovery.current = null;
          onFailure(cancelFailure);
        }
        return;
      }
      queryClient.setQueryData(chatQueryKeys.activeTurn(scope, chatId), outcome);
      recovery.current = null;
      if (outcome.status === 'failed' || outcome.status === 'canceled') {
        onEnded(jobId);
        if (task) restoreQuestion(task);
      } else if (outcome.status === 'succeeded') {
        onEnded(jobId);
      } else {
        onProgress(outcome);
        if (cancelFailure) onFailure(cancelFailure);
      }
      await invalidateEndedTurn(queryClient, scope, chatId, jobId);
    } catch (error) {
      if (current() && recovery.current?.jobId === jobId) {
        recovery.current = null;
        onFailure(cancelFailure ?? error);
      }
    } finally {
      if (current()) setStopping(false);
    }
  }

  /** The native outcome of a turn: a stopped one gives its question back. */
  function settleOutcome(jobId: string, outcome: string | undefined) {
    if (recovery.current?.jobId !== jobId) return;
    setStopping(false);
    if (outcome === 'interrupted') restoreQuestion(recovery.current.task);
    recovery.current = null;
  }

  /** The Job of a turn read as finished before its native outcome came. */
  function settleJob(job: Pick<ChatJob, 'id' | 'status'>) {
    if (recovery.current?.jobId !== job.id) return;
    const { task } = recovery.current;
    recovery.current = null;
    if (job.status === 'failed' || job.status === 'canceled') restoreQuestion(task);
  }

  function reset() {
    recovery.current = null;
    setStopping(false);
  }

  return { reset, settleJob, settleOutcome, stop, stopping };
}
