import { Fragment, useCallback, useRef, type ReactNode, type RefObject } from 'react';
import { Plus } from 'lucide-react';
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
import { ErrorBlock } from '../../ui/feedback/ErrorBlock';
import { Card } from '../../ui/feedback/Card';
import { Skeleton } from '../../ui/feedback/Skeleton';
import { SupportReference } from '../../ui/feedback/SupportReference';
import styles from './ResearchChatPanel.module.css';
import { AnswerActions, type AnswerAsk } from './AnswerActions';
import { latestUserBody, type ChatFailure } from './research-chat-presentation';
import type { FeedAction, FeedTurn } from './feed/feed-model';
import type { TurnStop } from './feed/LiveStatus';
import { RememberedUndo } from './RememberedUndo';
import { ChatAttachmentCard } from './ChatAttachmentCard';
import { useChatSlot } from './chat-slot';
import type { ChatAttachmentSummary } from '../../entities/chat/api';

interface ResearchChatConversationProps {
  /** How the buttons under an answer put a question into the field (AA1.3); none for the reader of a saga. */
  answerAsk?: AnswerAsk | undefined;
  chatId?: string;
  connection: AgentTimelineConnection;
  emptyMessage?: string;
  /** What a new chat shows instead of its empty message: examples of a first question (UC2.3). */
  examples?: ReactNode;
  failure?: ChatFailure | null;
  /**
   * The block under the failed turn the edition says apart from `failure` (specs/2026-11-15-desk-account-words-web.md AW4.4):
   * it stands while that turn is the last failed one, a refused new question its own block. None — the block under the turn is
   * `failure` when it carries a count, as on the site.
   */
  turnFailure?: ChatFailure | null;
  footer?: ReactNode;
  /**
   * «Что дальше» of the first answer of the saga (specs/2026-11-10-ux3-first-screen.md FS3.1): the panel gives it to the one
   * chat of the saga; it stands under the buttons of the answer while the chat has that one question and no other goes.
   */
  firstAnswerNext?: ReactNode;
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
  /** The question of the chat not sent yet stands in the field: the page says so instead of showing it twice (AA2.8). */
  savedInField?: boolean;
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
  /** «Остановить» of the running turn, in its status (specs/2026-11-10-ux4-chat-waiting.md CW2.4). */
  stop?: TurnStop | undefined;
  /** «Повторить вопрос» of a stopped or failed turn: the question goes into the field (CW2.8). */
  onRepeatQuestion?: ((text: string) => void) | undefined;
}

export function ResearchChatConversation({
  answerAsk,
  chatId = '',
  connection,
  emptyMessage,
  examples,
  failure,
  turnFailure,
  events,
  feedFooter,
  firstAnswerNext,
  footer,
  jobStatus,
  loading,
  onRetry,
  onReturnToChats,
  onOpenSource,
  onStartNew,
  savedInField = false,
  onStartIndexing,
  onAskSteer,
  pendingQuestion,
  pendingSteers,
  preparation,
  queuePosition,
  savedSession,
  sourcePane,
  selectedSource,
  startedAt,
  stop,
  onRepeatQuestion,
}: ResearchChatConversationProps) {
  // Stable renderers let unchanged remarks of a streaming turn skip re-rendering.
  const openSource = useRef(onOpenSource);
  openSource.current = onOpenSource;
  const renderSourceLink = useCallback((_event: CanonicalAgentTimelineEvent, citation: SourceCitation, label: ReactNode) => (
    <SourceLink aria-expanded={sameChatSource(selectedSource, citation)} data-chat-source="" title="Открыть место рядом с чатом" onClick={(click) => {
      openSource.current({ chapterKey: citation.chapterKey, from: citation.from, to: citation.to }, click.currentTarget);
    }}>{label}</SourceLink>
  ), [selectedSource]);
  // The count is the server's: one question in the timeline of the one chat, no other on its way (FS3.1).
  const working = jobStatus === 'queued' || jobStatus === 'running' || jobStatus === 'canceling';
  const next = firstAnswerNext && !working && !pendingQuestion && events.filter((event) => event.kind === 'user').length === 1 ? firstAnswerNext : null;
  const renderAssistantActions = useCallback((event: CanonicalAgentTimelineEvent, contentRef: RefObject<HTMLElement | null>, answer: { continuations: readonly string[] }) => (
    <AnswerActions ask={answerAsk} chatId={chatId} contentRef={contentRef} continuations={answer.continuations} event={event} next={next} />
  ), [answerAsk, chatId, next]);
  // The drawings of the answers, of the questions and of the corrections are a block of the site (illustrations R8.1, R15.9).
  const drawings = useChatSlot()?.drawings;
  const renderActionRemembered = useCallback((action: FeedAction, turn: FeedTurn) => <RememberedUndo action={action} turn={turn} />, []);
  const renderQuestionAttachment = useCallback((attachment: ChatAttachmentSummary) => <ChatAttachmentCard attachment={attachment} />, []);
  // A short break of the stream: one quiet line under the status of the turn, no alarm (AA2.6, Ч-74).
  const lostQuietly = (connection === 'reconnecting' || connection === 'closed') && !failure && working;
  const notice = connection === 'connecting' && events.length === 0 && jobStatus ? 'Подключаемся…' : undefined;
  // The failure of the Job of the turn is one block under its question, of the common look: what happened in the word
  // «помощник», the count as it is, «Повторить вопрос», «Новый чат» or the way out of the edition, the code and «Написать нам»
  // (CW2.8). It is the same block without a line of the count (specs/2026-11-15-desk-account-words-web.md AW4.3).
  const latestQuestion = latestUserBody(events);
  const ended = turnFailure ?? (failure?.count !== undefined ? failure : null);
  const turnEnd = ended ? (
    <ErrorBlock title={ended.title ?? 'Помощник не смог ответить'} cause={ended.text} kept={ended.count}
      action={ended.action === 'ask_again' && onRepeatQuestion && latestQuestion ? { label: 'Повторить вопрос', onClick: () => onRepeatQuestion(latestQuestion) }
        : ended.action === 'new_chat' && onStartNew ? { label: 'Новый чат', onClick: onStartNew } : undefined}>
      {ended.way ? <div className={styles.failureActions}>{ended.way}</div> : null}
      <SupportReference code={ended.supportCode} />
    </ErrorBlock>
  ) : undefined;
  // What happened, what to do, the way out and the support code — one block (R4.2.3): a refusal, a read or the stream, never
  // the failed turn its block under the question says.
  const shown = failure === ended ? null : failure;
  const error = shown ? <>
    {shown.title ? <strong className={styles.failureTitle}>{shown.title}</strong> : null}
    <p className={styles.failureText}>{shown.reason ?? shown.text}</p>
    <SupportReference code={shown.supportCode} />
  </> : undefined;
  const actions = shown ? [
    shown.action === 'retry' && onRetry ? <Button key="retry" onClick={onRetry}>Повторить</Button> : null,
    shown.action === 'new_chat' && onStartNew ? <Button key="new" onClick={onStartNew} variant="primary"><Plus aria-hidden="true" size={16} />Новый чат</Button> : null,
    shown.action === 'books' && onStartIndexing ? <Button key="books" onClick={onStartIndexing} variant="primary">К книгам</Button> : null,
    shown.way ? <Fragment key="way">{shown.way}</Fragment> : null,
    onReturnToChats ? <Button key="chats" onClick={onReturnToChats} variant="outline">К списку чатов</Button> : null,
  ].filter(Boolean) : [];
  const errorActions = actions.length ? <>{actions}</> : undefined;
  const errorLabel = shown?.title ?? shown?.label ?? 'Ответ не получен';

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
          liveNote={lostQuietly ? 'Связь прервалась — восстанавливаю, ответ готовится' : undefined}
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
          notify
          turnEnd={turnEnd}
          {...(stop ? { stop } : {})}
          {...(onRepeatQuestion ? { onRepeatQuestion } : {})}
        /> : (
          <div className={styles.emptyConversation}>
            {notice ? <p role="status">{notice}</p> : null}
            {/* A question not sent stands once, in the field, with its one «Спросить» (AA2.8, Ч-33); the reader of the
                saga, who has no field, reads it here. */}
            {savedSession?.savedAssignment && savedInField ? (
              <p className={styles.savedNote} role="status">Вопрос ещё не отправлен — он стоит в поле.</p>
            ) : savedSession?.savedAssignment ? (
              <Card aria-label="Сохранённый вопрос" className={styles.savedAssignment} role="region" title="ВОПРОС СОХРАНЁН · ЕЩЁ НЕ ОТПРАВЛЕН">
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
