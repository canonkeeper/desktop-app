import type { Job } from '../../../shared/api';
import { agentWorkflowScenario } from '../scenarios';
import { now } from './common';

/** Ends a running chat turn: it answers, or fails with `failureCode` like a refused model turn. */
export function completeAgentJob(job: Job, failureCode?: string) {
  if (job.status !== agentWorkflowScenario.jobTransition.start || job.subject.type !== 'research_chat_turn') return job;
  if (failureCode) {
    Object.assign(job, {
      status: 'failed',
      progress: 1,
      canCancel: false,
      error: { type: 'about:blank', title: 'Chat turn failed', status: 500, code: failureCode, requestId: `mock-${job.id}` },
      updatedAt: now,
    });
    return job;
  }

  const result: NonNullable<Job['result']> = { type: 'research_chat_turn', chatId: job.subject.chatId, turnId: job.subject.turnId };
  Object.assign(job, {
    status: agentWorkflowScenario.jobTransition.terminal,
    progress: 1,
    result,
    canCancel: false,
    updatedAt: now,
  });
  return job;
}
