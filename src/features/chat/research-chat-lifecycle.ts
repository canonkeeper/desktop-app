import type { QueryClient } from '@tanstack/react-query';
import { isApiStatusFailure } from '../../entities/api-errors';
import {
  chatQueryKeys,
  createIdempotencyKey,
  type ChatJob,
  type ChatScope,
} from '../../entities/chat/api';
import { manuscriptQueryKeys } from '../../entities/manuscript/api';
import type { ChapterFileInput } from '../../entities/chat/api';

export interface PendingTask {
  body: string;
  /** The chapter attached to the question and the key of its intent (specs/2026-11-07-chat-attach-chapter.md CA7.5). */
  chapter?: ChapterFileInput;
  chapterKey?: string;
  chatId: string;
  createdSession: boolean;
  sessionIdempotencyKey: string;
  turnIdempotencyKey: string;
}

export interface CreatedChatTurn {
  chatId: string;
  status: ChatJob['status'];
  turnId: string;
}

type CreateTurn = (input: {
  body: string;
  chapter?: ChapterFileInput;
  chatId: string;
  idempotencyKey: string;
}) => Promise<{ job: ChatJob }>;

type ReadJob = (jobId: string) => Promise<ChatJob>;

/** A retry of the same question with the same chapter keeps its keys; another question or chapter is a new one. */
export function pendingTaskFor(current: PendingTask | null, body: string, chatId: string, chapter?: { input: ChapterFileInput; key: string }) {
  if (current?.body === body && current.chapterKey === chapter?.key) return current;
  return {
    body,
    ...(chapter ? { chapter: chapter.input, chapterKey: chapter.key } : {}),
    chatId,
    createdSession: false,
    sessionIdempotencyKey: createIdempotencyKey('createChatSession'),
    turnIdempotencyKey: createIdempotencyKey('createChatTurn'),
  } satisfies PendingTask;
}

export async function createPendingTurn(
  createTurn: CreateTurn,
  task: PendingTask,
  attempt = 0,
): Promise<ChatJob> {
  try {
    const response = await createTurn({
      chatId: task.chatId,
      body: task.body,
      ...(task.chapter ? { chapter: task.chapter } : {}),
      idempotencyKey: task.turnIdempotencyKey,
    });
    return response.job;
  } catch (error) {
    if (task.createdSession && attempt < 2 && isTransientCreatedResourceFailure(error)) {
      await waitForCreatedResource();
      return createPendingTurn(createTurn, task, attempt + 1);
    }
    throw error;
  }
}

export function emptyChatMessage(selectedChatId: string, canCreate: boolean, sessionCount: number) {
  if (selectedChatId) return undefined;
  if (canCreate) return 'Задайте первый вопрос — чат появится в списке чатов.';
  return sessionCount > 0
    ? 'Выберите чат слева, чтобы продолжить.'
    : 'Пока нет доступных чатов.';
}

export function retryAfterSeconds(error: unknown): number | null {
  if (!isApiStatusFailure(error) || error.status !== 429
    || !('response' in error) || !(error.response instanceof Response)) return null;
  const value = error.response.headers.get('Retry-After');
  if (!value || !/^\d+$/.test(value)) return null;
  return Number(value);
}

export function formatRetryAfterSeconds(seconds: number) {
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, '0')}`;
}

export async function reconcileCanceledJob(readJob: ReadJob, jobId: string) {
  for (let attempt = 0; attempt <= 15; attempt += 1) {
    const job = await readJob(jobId);
    if (job.id !== jobId) return null;
    if (['succeeded', 'failed', 'canceled'].includes(job.status)) return job;
    if (attempt === 15) return null;
    await new Promise((resolve) => setTimeout(resolve, 2_000));
  }
  return null;
}

function isTransientCreatedResourceFailure(error: unknown) {
  return isApiStatusFailure(error)
    && (error.status === 404 || (error.status === 409 && error.problem.code === 'conflict'));
}

function waitForCreatedResource() {
  return new Promise((resolve) => setTimeout(resolve, 100));
}

/** A turn that ended or stopped: its chat, the chat list and its Job are read again. */
export function invalidateEndedTurn(queryClient: QueryClient, scope: ChatScope, chatId: string, jobId: string) {
  return Promise.all([
    ...(chatId ? [
      queryClient.invalidateQueries({ queryKey: chatQueryKeys.activeTurn(scope, chatId), exact: true }),
      queryClient.invalidateQueries({ queryKey: chatQueryKeys.session(scope, chatId), exact: true }),
    ] : []),
    queryClient.invalidateQueries({ queryKey: chatQueryKeys.sessions(scope), exact: true }),
    queryClient.invalidateQueries({ queryKey: manuscriptQueryKeys.job(jobId), exact: true }),
  ]);
}
