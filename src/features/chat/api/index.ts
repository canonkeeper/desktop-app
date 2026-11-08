import { queryOptions, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { isApiStatusFailure } from '../../../entities/api-errors';
import {
  type CanonicalSourceLocator,
  type ChapterFileInput,
  type ChatApiClient,
  chatQueryKeys,
  chatSessionsQueryOptions,
  type ChatScope,
  type ChatSession,
  type SourceView,
} from '../../../entities/chat/api';

const chatTurnReconciliationIntervalMs = 2_000;
const awaitedTurnIntervalMs = 4_000;
const workingChatsIntervalMs = 4_000;

/** The chat's latest turn is queued, running or canceling: the chat list marks it (R2.2). */
export function chatIsWorking(session: Pick<ChatSession, 'latestTurnStatus'>) {
  return session.latestTurnStatus === 'queued' || session.latestTurnStatus === 'running'
    || session.latestTurnStatus === 'canceling';
}

export function chatErrorRequestId(error: unknown) {
  return isApiStatusFailure(error) ? error.problem.requestId ?? '' : '';
}

function chatMeta(api: ChatApiClient) {
  return { chatApi: api };
}

function apiFromMeta(meta: unknown) {
  const value = meta as { chatApi?: ChatApiClient } | undefined;
  if (!value?.chatApi) throw new Error('Chat query is missing API metadata.');
  return value.chatApi;
}

export function useChatSessionsQuery(api: ChatApiClient, scope: ChatScope) {
  return useQuery({
    ...chatSessionsQueryOptions(api, scope),
    // While a chat works, the list refreshes itself so its mark goes away when the turn ends (R2.3).
    refetchInterval: (query) => query.state.data?.data.some(chatIsWorking) ? workingChatsIntervalMs : false,
  });
}

export function useChatSessionQuery(api: ChatApiClient, scope: ChatScope, chatId: string, reconcile = false) {
  return useQuery({
    queryKey: chatQueryKeys.session(scope, chatId),
    queryFn: ({ meta }) => apiFromMeta(meta).getChatSession(chatId),
    meta: chatMeta(api),
    enabled: chatId.length > 0,
    retry: (failureCount, error) => shouldRetryCreatedResourceRead(reconcile, failureCount, error),
    retryDelay: 100,
  });
}

function shouldRetryCreatedResourceRead(reconcile: boolean, failureCount: number, error: unknown) {
  return reconcile
    && isApiStatusFailure(error)
    && (error.status === 404 || error.status === 409)
    && failureCount < 3;
}

/** Resolves a cited chapter key in the chat's text version (UC4). */
export function useChatChapterKeyQuery(api: Pick<ChatApiClient, 'resolveChatChapterKey'>, chatId: string, chapterKey: string, enabled = true) {
  return useQuery({
    enabled,
    queryKey: chatQueryKeys.chapterKey(chatId, chapterKey),
    queryFn: ({ meta, signal }) => apiFromMeta(meta).resolveChatChapterKey(chatId, chapterKey, { signal }),
    meta: chatMeta(api as ChatApiClient),
    retry: false,
    staleTime: 60_000,
  });
}

/** The chapter attached to a question of the chat (CA4.3): written once, so it stays fresh. */
export function chatAttachmentQueryOptions(api: Pick<ChatApiClient, 'getChatAttachment'>, chatId: string, number: number) {
  return queryOptions({
    queryKey: chatQueryKeys.attachment(chatId, number),
    queryFn: ({ meta, signal }) => apiFromMeta(meta).getChatAttachment(chatId, number, { signal }),
    meta: chatMeta(api as ChatApiClient),
    retry: false,
    staleTime: Infinity,
  });
}

export function useChatAttachmentQuery(api: Pick<ChatApiClient, 'getChatAttachment'>, chatId: string, number: number | null) {
  return useQuery({ ...chatAttachmentQueryOptions(api, chatId, number ?? 0), enabled: Boolean(chatId) && number !== null });
}

/** Reads the cited chapter through the Reader chapter resource. */
export function useChatSourceChapterQuery(api: Pick<ChatApiClient, 'getChapter'>, chapterId: string | undefined, sourceView: SourceView) {
  return useQuery({
    queryKey: chatQueryKeys.sourceChapter(chapterId ?? '', sourceView),
    queryFn: ({ meta }) => apiFromMeta(meta).getChapter(chapterId!, sourceView),
    meta: chatMeta(api as ChatApiClient),
    enabled: Boolean(chapterId),
    retry: false,
    staleTime: 30_000,
  });
}

/** The Job of an awaited turn, polled on any page until it ends, also in a hidden tab. */
export function turnJobQueryOptions(api: Pick<ChatApiClient, 'getJob'>, jobId: string) {
  return queryOptions({
    queryKey: chatQueryKeys.turnJob(jobId),
    queryFn: ({ meta }) => apiFromMeta(meta).getJob(jobId),
    meta: { ...chatMeta(api as ChatApiClient), outageScope: 'local' },
    refetchInterval: (query) => ['succeeded', 'failed', 'canceled'].includes(query.state.data?.status ?? '')
      ? false
      : awaitedTurnIntervalMs,
    refetchIntervalInBackground: true,
  });
}

export function useActiveChatTurnQuery(
  api: ChatApiClient,
  scope: ChatScope,
  chatId: string,
) {
  return useQuery({
    queryKey: chatQueryKeys.activeTurn(scope, chatId),
    queryFn: ({ meta, signal }) => findActiveChatTurn(apiFromMeta(meta), scope, chatId, signal),
    meta: chatMeta(api),
    enabled: chatId.length > 0,
    refetchInterval: (query) => query.state.status === 'success'
      && query.state.data
      && ['queued', 'running', 'canceling'].includes(query.state.data.status)
      ? chatTurnReconciliationIntervalMs
      : false,
  });
}

export async function findActiveChatTurn(
  api: ChatApiClient,
  scope: ChatScope,
  chatId: string,
  signal?: AbortSignal,
) {
  void signal;
  const session = await api.getChatSession(chatId);
  if (session.projectId !== scope.projectId) return null;
  const jobId = session.latestTurnJobId;
  if (!jobId) return null;
  const job = await api.getJob(jobId);
  const subject = job.subject;
  if (subject.type !== 'research_chat_turn'
    || subject.projectId !== scope.projectId
    || subject.chatId !== chatId
    || subject.sourceView !== session.sourceView) {
    return null;
  }
  return job;
}

export function useCreateChatSessionMutation(api: ChatApiClient, scope: ChatScope) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: { title?: string; savedAssignment?: string; idempotencyKey: string }) => api.createChatSession(
      scope.projectId,
      { sourceView: scope.sourceView, scope: 'project', title: input.title, savedAssignment: input.savedAssignment },
      { idempotencyKey: input.idempotencyKey },
    ),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: chatQueryKeys.sessions(scope), exact: true }),
  });
}

export function useDeleteChatSessionMutation(api: ChatApiClient, scope: ChatScope) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (chatId: string) => api.deleteChatSession(chatId),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: chatQueryKeys.sessions(scope), exact: true }),
  });
}

export function useRenameChatSessionMutation(api: ChatApiClient, scope: ChatScope) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: { chatId: string; title: string }) => api.renameChatSession(
      input.chatId,
      { title: input.title },
    ),
    onSuccess: async (session) => {
      queryClient.setQueryData(chatQueryKeys.session(scope, session.id), session);
      queryClient.setQueryData<Awaited<ReturnType<ChatApiClient['listChatSessions']>>>(
        chatQueryKeys.sessions(scope),
        (current) => current
          ? { ...current, data: current.data.map((item) => item.id === session.id ? session : item) }
          : current,
      );
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: chatQueryKeys.sessions(scope), exact: true }),
        queryClient.invalidateQueries({ queryKey: chatQueryKeys.session(scope, session.id), exact: true }),
      ]);
    },
  });
}

export function useCreateChatTurnMutation(api: ChatApiClient, scope: ChatScope) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: {
      chatId: string; body: string; idempotencyKey: string; contextLocators?: CanonicalSourceLocator[]; references?: readonly number[];
      /** The chapter of the author attached to the question (specs/2026-11-07-chat-attach-chapter.md CA2). */
      chapter?: ChapterFileInput;
    }) => input.chapter
      ? api.createChatTurnWithChapter(input.chatId, { ...input.chapter, body: input.body }, { idempotencyKey: input.idempotencyKey })
      : api.createChatTurn(
        input.chatId,
        {
          body: input.body,
          ...(input.contextLocators?.length ? { contextLocators: input.contextLocators } : {}),
          ...(input.references?.length ? { references: [...input.references] } : {}),
        },
        { idempotencyKey: input.idempotencyKey },
      ),
    // The chat list marks the chat as working at once (R2.3).
    onSuccess: () => queryClient.invalidateQueries({ queryKey: chatQueryKeys.sessions(scope), exact: true }),
  });
}

