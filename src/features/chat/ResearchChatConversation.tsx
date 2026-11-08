import { useCallback, useRef, type ReactNode, type RefObject } from 'react';
import { Play, Plus } from 'lucide-react';
import type { PendingSteer } from '../../entities/chat/steer';
import { ChatFeed } from './feed/ChatFeed';
import type { AgentJobStatus, CanonicalAgentTimelineEvent } from './feed/types';
import type { AgentTimelineConnection } from '../../entities/agent-timeline/api';
import type { ChatSession } from '../../entities/chat/api';
import { sameChatSource, type ChatSourceTarget, type SourceCitation } from '../../shared/navigation/chat-source';
import { formatRelativeTime } from '../../ui/copy';
import { SourceLink } from '../../ui/controls/SourceLink';
import { Button } from '../../ui/controls/Button';
import { Alert } from '../../ui/feedback/Alert';
import { Card } from '../../ui/feedback/Card';
import { Skeleton } from '../../ui/feedback/Skeleton';
import { SupportReference } from '../../ui/feedback/SupportReference';
import styles from './ResearchChatPanel.module.css';
import { ChatNoteAction } from './ChatNoteAction';
import type { ChatFailure } from './research-chat-presentation';
import type { FeedAction, FeedTurn } from './feed/feed-model';
import { RememberedUndo } from './RememberedUndo';
import { ChatAttachmentCard } from './ChatAttachmentCard';
import { useChatSlot } from './chat-slot';
import type { ChatAttachmentSummary } from '../../entities/chat/api';

interface ResearchChatConversationProps {
  chatId?: string;
  connection: AgentTimelineConnection;
  emptyMessage?: string;
  /** What a new chat shows instead of its empty message: examples of a first question (UC2.3). */
  examples?: ReactNode;
  failure?: ChatFailure | null;
  footer?: ReactNode;
  /** What follows the last turn of the feed: the style choice of the first drawing. */
  feedFooter?: ReactNode;
  events: readonly CanonicalAgentTimelineEvent[];
  sourcePane?: ReactNode;
  selectedSource?: ChatSourceTarget | null;
  jobStatus: AgentJobStatus | null;
  loading: boolean;
  onRetry?: () => void;
  onReturnToChats?: () => void;
  onOpenSource: (source: ChatSourceTarget, trigger: HTMLElement) => void;
  onStartNew?: () => void;
  onStartSaved?: () => void;
  onStartIndexing?: () => void;
  pendingQuestion?: string;
  /** Corrections the server took that the timeline does not show yet. */
  pendingSteers?: readonly PendingSteer[];
  onAskSteer?: (text: string) => void;
  /** The saga preparation notice above the feed (UC2.3). */
  preparation?: ReactNode;
  /** The place of the waiting question in the fair queue. */
  queuePosition?: number | null;
  savedSession?: ChatSession;
  /** When the Job of the running turn was created. */
  startedAt?: string;
  savedBusy?: boolean;
}

export function ResearchChatConversation({
  chatId = '',
  connection,
  emptyMessage,
  examples,
  failure,
  events,
  feedFooter,
  footer,
  jobStatus,
  loading,
  onRetry,
  onReturnToChats,
  onOpenSource,
  onStartNew,
  onStartSaved,
  onStartIndexing,
  onAskSteer,
  pendingQuestion,
  pendingSteers,
  preparation,
  queuePosition,
  savedSession,
  savedBusy,
  sourcePane,
  selectedSource,
  startedAt,
}: ResearchChatConversationProps) {
  // Stable renderers let unchanged remarks of a streaming turn skip re-rendering.
  const openSource = useRef(onOpenSource);
  openSource.current = onOpenSource;
  const renderSourceLink = useCallback((_event: CanonicalAgentTimelineEvent, citation: SourceCitation, label: ReactNode) => (
    <SourceLink aria-expanded={sameChatSource(selectedSource, citation)} title="Открыть источник рядом с чатом" onClick={(click) => {
      openSource.current({ chapterKey: citation.chapterKey, from: citation.from, to: citation.to }, click.currentTarget);
    }}>{label}</SourceLink>
  ), [selectedSource]);
  const renderAssistantActions = useCallback((event: CanonicalAgentTimelineEvent, contentRef: RefObject<HTMLElement | null>) => (
    <ChatNoteAction chatId={chatId} event={event} contentRef={contentRef} />
  ), [chatId]);
  // The drawings of the answers, of the questions and of the corrections are a block of the site (illustrations R8.1, R15.9).
  const drawings = useChatSlot()?.drawings;
  const renderActionRemembered = useCallback((action: FeedAction, turn: FeedTurn) => <RememberedUndo action={action} turn={turn} />, []);
  const renderQuestionAttachment = useCallback((attachment: ChatAttachmentSummary) => <ChatAttachmentCard attachment={attachment} />, []);
  const notice = connection === 'reconnecting' && !failure
    ? 'Связь прервалась — восстанавливаем. Ответ продолжает готовиться на сервере.'
    : connection === 'connecting' && events.length === 0 && jobStatus
      ? 'Подключаемся…'
      : undefined;
  // What happened, what to do, the way out and the support code — one block (R4.2.3).
  const error = failure ? <>
    {failure.title ? <strong className={styles.failureTitle}>{failure.title}</strong> : null}
    <p className={styles.failureText}>{failure.text}</p>
    <SupportReference code={failure.supportCode} />
  </> : undefined;
  const actions = [
    failure?.action === 'retry' && onRetry ? <Button key="retry" onClick={onRetry}>Повторить</Button> : null,
    failure?.action === 'new_chat' && onStartNew ? <Button key="new" onClick={onStartNew} variant="primary"><Plus aria-hidden="true" size={16} />Новый чат</Button> : null,
    failure?.action === 'books' && onStartIndexing ? <Button key="books" onClick={onStartIndexing} variant="primary">К книгам</Button> : null,
    onReturnToChats ? <Button key="chats" onClick={onReturnToChats} variant="outline">К списку чатов</Button> : null,
  ].filter(Boolean);
  const errorActions = actions.length ? <>{actions}</> : undefined;
  const errorLabel = failure?.title ?? 'Ответ не получен';

  return (
    <div className={styles.conversation} data-source={sourcePane ? '' : undefined}>
      <div className={styles.conversationMain}>
        <div className={styles.preparationSlot}>{preparation}</div>
        {loading ? <div aria-label="Загрузка чата" className={styles.emptyConversation} role="status">
          <div className={styles.loadingConversation}>
            <Skeleton style={{ width: '100%', height: 56 }} />
            <Skeleton style={{ width: 520, maxWidth: '100%', height: 96 }} />
            <Skeleton style={{ width: '100%', height: 140 }} />
          </div>
        </div> : jobStatus || events.length > 0 ? <ChatFeed
          emptyMessage={emptyMessage}
          error={error}
          errorLabel={errorLabel}
          errorActions={errorActions}
          events={events}
          footer={feedFooter}
          jobStatus={jobStatus}
          notice={notice}
          onOpenSource={(source, trigger) => onOpenSource({ chapterKey: source.chapterKey, from: source.from, to: source.to }, trigger)}
          onAskSteer={onAskSteer}
          pendingQuestion={pendingQuestion}
          pendingSteers={pendingSteers}
          queuePosition={queuePosition}
          renderActionDrawing={drawings?.renderActionDrawing}
          renderActionRemembered={renderActionRemembered}
          renderAssistantActions={renderAssistantActions}
          renderImage={drawings?.renderImage}
          renderQuestionImages={drawings?.renderQuestionImages}
          renderQuestionAttachment={renderQuestionAttachment}
          renderSourceLink={renderSourceLink}
          renderSteerImages={drawings?.renderSteerImages}
          renderWorkshopLink={drawings?.renderWorkshopLink}
          startedAt={startedAt}
        /> : (
          <div className={styles.emptyConversation}>
            {notice ? <p role="status">{notice}</p> : null}
            {savedSession?.savedAssignment ? (
              <Card
                aria-label="Сохранённый вопрос"
                className={styles.savedAssignment}
                role="region"
                title="ВОПРОС СОХРАНЁН · ЕЩЁ НЕ ОТПРАВЛЕН"
                actions={onStartSaved || onStartNew ? <>
                  {onStartSaved ? <Button disabled={savedBusy} onClick={onStartSaved} variant="primary"><Play aria-hidden="true" size={16} />Отправить</Button> : null}
                  {onStartNew ? <Button disabled={savedBusy} onClick={onStartNew} variant="outline"><Plus aria-hidden="true" size={16} />Новый чат</Button> : null}
                </> : undefined}
              >
                <p>{savedSession.savedAssignment.body}</p>
                <small>Чат создан {formatRelativeTime(savedSession.createdAt)}</small>
              </Card>
            ) : null}
            {error ? <Alert aria-label={errorLabel} className={styles.failure} tone="error">{error}<div className={styles.failureActions}>{errorActions}</div></Alert>
              : !savedSession && emptyMessage ? examples ?? <Alert>{emptyMessage}</Alert> : null}
          </div>
        )}
      </div>
      <div className={styles.conversationFooter}>{footer}</div>
      {sourcePane ? <div className={styles.sourcePane}>{sourcePane}</div> : null}
    </div>
  );
}
