import { queryOptions } from '@tanstack/react-query';
import { createIdempotencyKey } from '../../../shared/api';
import type {
  CanonKeeperApiClient,
  ChapterFileInput,
  ChatAttachment,
  ChatAttachmentSummary,
  ChatChapterLocation,
  ChatSession,
  Schema,
  SourceView,
} from '../../../shared/api';

export type ChatApiClient = Pick<
  CanonKeeperApiClient,
  | 'cancelJob'
  | 'createChatSession'
  | 'createChatTurn'
  | 'createChatTurnWithChapter'
  | 'steerChatTurn'
  | 'deleteChatSession'
  | 'getAccountQuota'
  | 'getChapter'
  | 'getChatSession'
  | 'getChatAttachment'
  | 'getAgentTimeline'
  | 'getJob'
  | 'listChatSessions'
  | 'renameChatSession'
  | 'resolveChatChapterKey'
>;
export type { ChapterFileInput, ChatAttachment, ChatAttachmentSummary, ChatChapterLocation, ChatSession, SourceView };
export type ChatJob = Schema<'Job'>;
export type CanonicalSourceLocator = Schema<'CanonicalChapterSourceLocatorV1'>;
export type SourceNavigationAnchor = Schema<'SourceNavigationAnchor'>;
export { createIdempotencyKey };

export interface ChatScope {
  projectId: string;
  sourceView: SourceView;
}

function scopeKey(scope: ChatScope) {
  return [scope.projectId, scope.sourceView] as const;
}

export const chatQueryKeys = {
  all: ['canonkeeper', 'chat'] as const,
  sessions: (scope: ChatScope) => [
    ...chatQueryKeys.all,
    'sessions',
    ...scopeKey(scope),
  ] as const,
  session: (scope: ChatScope, chatId: string) => [...chatQueryKeys.all, 'session', ...scopeKey(scope), chatId] as const,
  activeTurn: (scope: ChatScope, chatId: string) => [...chatQueryKeys.all, 'active-turn', ...scopeKey(scope), chatId] as const,
  turnJob: (jobId: string) => [...chatQueryKeys.all, 'turn-job', jobId] as const,
  chapterKey: (chatId: string, chapterKey: string) => [...chatQueryKeys.all, 'chapter-key', chatId, chapterKey] as const,
  sourceChapter: (chapterId: string, sourceView: SourceView) => [...chatQueryKeys.all, 'source-chapter', chapterId, sourceView] as const,
  /** The chapter attached to a question of a chat (specs/2026-11-07-chat-attach-chapter.md CA4.3): written once, never changed. */
  attachment: (chatId: string, number: number) => [...chatQueryKeys.all, 'attachment', chatId, number] as const,
};

type ChatSessionList = Awaited<ReturnType<ChatApiClient['listChatSessions']>>;
type ChatSessionsClient = Pick<ChatApiClient, 'listChatSessions'>;

/** The whole chat list of a saga, read page by page under one key (R1.1 of parallel chats). */
export function chatSessionsQueryOptions(api: ChatSessionsClient, scope: ChatScope) {
  return queryOptions({
    queryKey: chatQueryKeys.sessions(scope),
    queryFn: async ({ meta, signal }): Promise<ChatSessionList> => {
      const chatApi = (meta as { chatApi: ChatSessionsClient }).chatApi;
      const data: ChatSession[] = [];
      let cursor: string | undefined;
      let page: ChatSessionList;
      do {
        page = await chatApi.listChatSessions(scope.projectId, { limit: 100, ...(cursor ? { cursor } : {}), signal });
        data.push(...page.data);
        cursor = page.meta.nextCursor ?? undefined;
      } while (cursor);
      return { data, meta: page.meta };
    },
    meta: { chatApi: api },
    enabled: scope.projectId.length > 0,
  });
}

/**
 * Whether the saga already has a chat (UC2.1 «Три шага», R2.2.3). It reads the chat list of the
 * saga under the key of that list, so creating or deleting a chat resets it everywhere; the shelf
 * selects only whether the list is empty.
 */
export function sagaHasChatQueryOptions(api: ChatSessionsClient, scope: ChatScope) {
  return queryOptions({
    ...chatSessionsQueryOptions(api, scope),
    select: (list: ChatSessionList) => list.data.length > 0,
  });
}

/**
 * Whether the saga has had an answer (specs/2026-11-10-ux3-first-screen.md FS3.3): a chat of it whose latest turn
 * succeeded, by the same list of the chats.
 */
export function sagaHasAnswerQueryOptions(api: ChatSessionsClient, scope: ChatScope) {
  return queryOptions({
    ...chatSessionsQueryOptions(api, scope),
    select: (list: ChatSessionList) => list.data.some((chat) => chat.latestTurnStatus === 'succeeded'),
  });
}
