import { useQueryClient } from '@tanstack/react-query';
import { type FormEvent, type ReactNode, useEffect, useMemo, useRef, useState } from 'react';
import { isApiStatusFailure } from '../../entities/api-errors';
import { invalidateAccountQuota, quotaFailureCode, useAccountQuota } from '../../entities/account-quota/api';
import { useCanonicalAgentTimeline } from '../../entities/agent-timeline/api';
import {
  chatQueryKeys,
  type ChatApiClient,
  type ChatJob,
  type SourceView,
} from '../../entities/chat/api';
import type { AgentJobStatus, CanonicalAgentTimelineEvent } from './feed/types';
import { Plus } from 'lucide-react';
import { Button } from '../../ui/controls/Button';
import { Alert } from '../../ui/feedback/Alert';
import { ToastRegion } from '../../ui/feedback/Toast';
import { ChatExamples } from './ChatExamples';
import { ResearchChatComposer } from './ResearchChatComposer';
import { ChatQuotaHint } from './ChatQuotaHint';
import { chatQuotaHint, chatQuotaRefusal } from './chat-quota';
import { ResearchChatConversation } from './ResearchChatConversation';
import { ResearchChatSessions } from './ResearchChatSessions';
import { useResearchChatDelete } from './research-chat-delete';
import {
  type CreatedChatTurn,
  createPendingTurn,
  emptyChatMessage,
  invalidateEndedTurn,
  type PendingTask,
  pendingTaskFor,
} from './research-chat-lifecycle';
import { useResearchChatStop } from './research-chat-stop';
import { useResearchChatSteer } from './research-chat-steer';
import { useChatChapter } from './chat-chapter';
import { ChatChapterButton, ChatChapterStrip } from './ChatChapterField';
import { ChatAttachmentHostProvider } from './ChatAttachmentCard';
import {
  conversationFailure,
  latestUserBody,
  outdatedChatMessage,
  useResearchChatFailure,
} from './research-chat-presentation';
import {
  chatErrorRequestId,
  chatIsWorking,
  useActiveChatTurnQuery,
  useChatSessionQuery,
  useChatSessionsQuery,
  useCreateChatSessionMutation,
  useCreateChatTurnMutation,
} from './api';
import styles from './ResearchChatPanel.module.css';
import type { ChatSourceTarget } from '../../shared/navigation/chat-source';
import type { ChatSourceNavigation } from './use-chat-source-navigation';
import { ResearchChatSourceInspector } from './ResearchChatSourceInspector';
import { ResearchChatLayout } from './ResearchChatLayout';
import { watchTurn } from './answer-ready';
import { ChatMemoryProvider, type ChatMemoryHost } from './RememberedUndo';
import { useChatSlot, type ChatAttachmentHost, type ChatDrawingsOptions, type ChatIllustrationRoutes } from './chat-slot';

/** What the chat surface asks of the API: the chat and the undo of what it remembered. */
export type ChatSurfaceApi = ChatApiClient & ChatMemoryHost['api'];

/** The chat without the drawings of the site: nothing around the conversation, no overlays. */
function noDrawings(_options: ChatDrawingsOptions) {
  return { provide: (conversation: ReactNode) => conversation, overlays: null };
}

interface ResearchChatPanelProps {
  api: ChatSurfaceApi;
  canCreate: boolean;
  /** The lightbox the URL opens, the page of the feature and the gallery (illustrations R8.5, R11.4). */
  illustrationRoutes?: ChatIllustrationRoutes;
  onOpenSource: (source: ChatSourceTarget, trigger: HTMLElement) => void;
  sourceNavigation?: ChatSourceNavigation;
  onSelectChatId: (chatId: string) => void;
  onStartIndexing?: () => void;
  /** The question a new chat starts with in the field, from «Спросить о нём» of the saga map; the author sends it (saga map R5.6). */
  initialQuestion?: string;
  /** The saga preparation notice above the feed (UC2.3). */
  preparation?: ReactNode;
  /** The draft «Вычитать и вернуть .docx» made of an attached chapter, in «Письмо» (specs/2026-11-07-chat-attach-chapter.md CA5.3). */
  onOpenDraft?: (draftId: string) => void;
  projectId: string;
  selectedChatId: string;
  sourceView: SourceView;
  toolbar?: ReactNode;
}

type CreatedTurnState = CreatedChatTurn & Pick<ChatJob, 'canCancel' | 'id'> & { task: string };
export function ResearchChatPanel({
  api,
  canCreate,
  initialQuestion,
  illustrationRoutes,
  onOpenSource,
  onSelectChatId,
  onStartIndexing,
  onOpenDraft,
  preparation,
  projectId,
  selectedChatId,
  sourceView,
  sourceNavigation,
  toolbar,
}: ResearchChatPanelProps) {
  const [taskDraft, setTaskText] = useState<string | null>(initialQuestion ?? null);
  const [createdTurn, setCreatedTurn] = useState<CreatedTurnState | null>(null);
  const [mutationAuthorityLost, setMutationAuthorityLost] = useState(false);
  const questionField = useRef<HTMLTextAreaElement>(null);
  const chapter = useChatChapter();
  const { clearPanelFailure, failure: panelFailure, retryDelaySeconds, showPanelFailure } = useResearchChatFailure(() => setMutationAuthorityLost(true));
  const pendingTask = useRef<PendingTask | null>(null);
  const activeAction = useRef(0);
  const expectedSelectedChatId = useRef<string | null>(null);
  const mounted = useRef(false);
  const observedSelectedChatId = useRef(selectedChatId);
  const queryClient = useQueryClient();
  const chatSlot = useChatSlot();
  const chatScope = { projectId, sourceView };
  // The question comes back to the field only if the author has not written there (chat steering R4.4).
  const restoreQuestion = (task: string) => setTaskText((current) => current?.trim() ? current : task);
  const stopFlow = useResearchChatStop({
    action: () => activeAction.current,
    api,
    clearFailure: clearPanelFailure,
    onAuthorityLost: () => setMutationAuthorityLost(true),
    onEnded: (jobId) => setCreatedTurn((current) => current?.id === jobId ? null : current),
    onFailure: (error) => showPanelFailure(error, 'stop'),
    onProgress: (job) => setCreatedTurn((current) => current?.id === job.id ? { ...current, canCancel: job.canCancel, status: job.status } : current),
    restoreQuestion,
    scope: chatScope,
    stillCurrent: (chatId, action) => actionIsCurrent(action) && observedSelectedChatId.current === chatId,
  });
  const stoppingTurn = stopFlow.stopping;
  const sessionsQuery = useChatSessionsQuery(api, chatScope);
  const accountQuota = useAccountQuota(api, canCreate).data;
  const sessions = sessionsQuery.data?.data ?? [];
  const deleteFlow = useResearchChatDelete({
    api,
    isCurrentChat: () => mounted.current && observedSelectedChatId.current === selectedChatId,
    onAuthorityLost: () => setMutationAuthorityLost(true),
    onDeleted: (nextChatId) => {
      pendingTask.current = null;
      expectedSelectedChatId.current = nextChatId;
      onSelectChatId(nextChatId);
      setTaskText('');
      setCreatedTurn(null);
    },
    scope: chatScope,
    selectedChatId,
    sessions,
  });
  const ownedSelectedChat = Boolean(selectedChatId && (
    pendingTask.current?.chatId === selectedChatId || createdTurn?.chatId === selectedChatId
  ));
  const selectedSessionQuery = useChatSessionQuery(api, chatScope, selectedChatId, ownedSelectedChat);
  const selectedSession = selectedSessionQuery.data;
  const sessionPending = Boolean(selectedChatId) && selectedSessionQuery.isPending;
  // The chat list is one per project: a chat keeps the text version it was created on.
  const sessionMatchesScope = Boolean(selectedSession && selectedSession.projectId === projectId);
  const readableChatId = sessionMatchesScope || ownedSelectedChat ? selectedChatId : '';
  const createdTurnForChat = createdTurn?.chatId === readableChatId ? createdTurn : null;
  const activeTurnQuery = useActiveChatTurnQuery(api, chatScope, readableChatId);
  const recoveredTurnJob = activeTurnQuery.data;
  const jobStatus = createdTurnForChat?.status ?? recoveredTurnJob?.status;
  const jobIsActive = jobStatus === 'queued' || jobStatus === 'running' || jobStatus === 'canceling';
  const createSessionMutation = useCreateChatSessionMutation(api, chatScope);
  const createTurnMutation = useCreateChatTurnMutation(api, chatScope);
  const timelineJobId = createdTurnForChat?.id ?? recoveredTurnJob?.id ?? selectedSession?.latestTurnJobId ?? '';
  const savedSession = sessionMatchesScope && !timelineJobId && selectedSession?.savedAssignment ? selectedSession : undefined;
  const taskText = taskDraft ?? savedSession?.savedAssignment?.body ?? '';
  const savedStartUnavailable = Boolean(savedSession && !savedSession.savedAssignment?.availableActions.includes('start'));
  const chatOutdated = Boolean(sessionMatchesScope && selectedSession?.outdated);
  const timeline = useCanonicalAgentTimeline({
    api,
    jobId: timelineJobId,
    onTerminalEvent: (event) => {
      if (event.kind !== 'turn_outcome' || event.role !== 'main') return;
      if (!pendingTask.current) clearPanelFailure();
      stopFlow.settleOutcome(event.jobId, event.outcome);
      void invalidateEndedTurn(queryClient, chatScope, readableChatId, event.jobId);
      void invalidateAccountQuota(queryClient);
      chatSlot?.drawings?.afterTurn(queryClient);
      setCreatedTurn((current) => current?.id === event.jobId ? null : current);
    },
    projectId,
  });

  const runningTurnId = createdTurnForChat?.turnId ?? (recoveredTurnJob?.subject.type === 'research_chat_turn' ? recoveredTurnJob.subject.turnId : '');
  const unseenQuestion = jobIsActive && createdTurnForChat && createdTurnForChat.task !== latestUserBody(timeline.events) ? createdTurnForChat : undefined;
  // A turn that is being stopped takes no correction.
  const steerOpen = jobIsActive && jobStatus !== 'canceling' && !stoppingTurn && Boolean(runningTurnId) && canCreate && !mutationAuthorityLost;
  const steer = useResearchChatSteer({
    active: steerOpen, api, chatId: readableChatId, events: timeline.events as readonly CanonicalAgentTimelineEvent[], turnId: runningTurnId,
    jobId: timelineJobId, scope: chatScope,
    action: () => activeAction.current,
    stillCurrent: (chatId, action) => actionIsCurrent(action) && observedSelectedChatId.current === chatId,
    clearFailure: () => clearPanelFailure(),
    onFailure: (error) => showPanelFailure(error, 'steer'),
    // What the author wrote while it was sent stays.
    onSent: (message) => setTaskText((current) => current?.trim() === message ? '' : current),
    onEnded: (jobId) => setCreatedTurn((current) => current?.id === jobId ? null : current),
  });
  const useDrawings = chatSlot?.drawings?.useDrawings ?? noDrawings;
  const illustrations = useDrawings({
    scope: chatScope, canEdit: canCreate && !mutationAuthorityLost,
    ...illustrationRoutes, onOpenSource,
    toasts: deleteFlow.toasts,
  });

  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      activeAction.current += 1;
    };
  }, []);

  // A question brought from the saga map waits in the field for the author (saga map R5.6).
  useEffect(() => {
    if (initialQuestion) questionField.current?.focus();
  }, [initialQuestion]);

  useEffect(() => {
    if (observedSelectedChatId.current === selectedChatId) return;
    observedSelectedChatId.current = selectedChatId;
    if (expectedSelectedChatId.current === selectedChatId) {
      expectedSelectedChatId.current = null;
      return;
    }
    activeAction.current += 1;
  }, [selectedChatId]);

  // The field empties when the author goes to another chat, not when the panel opens: a question brought from the
  // saga map waits there (saga map R5.6).
  const fieldChat = useRef(selectedChatId);
  useEffect(() => {
    stopFlow.reset();
    const changed = fieldChat.current !== selectedChatId;
    fieldChat.current = selectedChatId;
    const keepFirstTask = pendingTask.current?.chatId === selectedChatId;
    if (!keepFirstTask && changed) {
      setTaskText(null);
      // The chapter at the field belongs to the question being written there (specs/2026-11-07-chat-attach-chapter.md CA7).
      chapter.remove();
    }
    setCreatedTurn((current) => current?.chatId === selectedChatId ? current : null);
    clearPanelFailure();
  }, [selectedChatId]);

  useEffect(() => {
    if (!canCreate) setMutationAuthorityLost(false);
  }, [canCreate]);

  // A sent question and a turn still running when the chat opens are awaited on every page (R6.1).
  const awaitedLabel = createdTurnForChat?.task ?? selectedSession?.title;
  useEffect(() => {
    if (jobIsActive && timelineJobId && readableChatId) {
      watchTurn({ chatId: readableChatId, jobId: timelineJobId, label: awaitedLabel, projectId });
    }
  }, [awaitedLabel, jobIsActive, projectId, readableChatId, timelineJobId]);

  const latestUserTask = latestUserBody(timeline.events);
  const latestTurnFailed = jobStatus === 'failed' || jobStatus === 'canceled';

  useEffect(() => {
    if (latestTurnFailed && latestUserTask) restoreQuestion(latestUserTask);
  }, [latestTurnFailed, latestUserTask]);

  useEffect(() => {
    if (stoppingTurn || !activeTurnQuery.isSuccess || !recoveredTurnJob ||
      createdTurnForChat?.id !== recoveredTurnJob.id ||
      ['queued', 'running', 'canceling'].includes(recoveredTurnJob.status)) return;
    stopFlow.settleJob(recoveredTurnJob);
    setCreatedTurn((current) => current?.id === recoveredTurnJob.id ? null : current);
  }, [activeTurnQuery.isSuccess, createdTurnForChat?.id, recoveredTurnJob, stoppingTurn]);

  function startNewChat() {
    pendingTask.current = null;
    setTaskText('');
    setCreatedTurn(null);
    clearPanelFailure();
    deleteFlow.dismiss();
    stopFlow.reset();
    if (selectedChatId) selectChat('');
  }

  function selectChat(chatId: string) {
    if (chatId === selectedChatId) return;
    pendingTask.current = null;
    expectedSelectedChatId.current = null;
    activeAction.current += 1;
    deleteFlow.dismiss();
    onSelectChatId(chatId);
  }

  function actionIsCurrent(actionId: number) {
    return mounted.current && activeAction.current === actionId;
  }

  /** A question from the field, with the chapter attached to it (specs/2026-11-07-chat-attach-chapter.md CA7). */
  async function startTask(message: string, attached = chapter.chapter) {
    if (!message || busy || !canMutate || savedStartUnavailable || chatOutdated || (selectedChatId && !readableChatId)) return;
    const actionId = ++activeAction.current;
    const submission = pendingTaskFor(pendingTask.current, message, readableChatId, attached ?? undefined);
    pendingTask.current = submission;
    clearPanelFailure();
    try {
      if (!submission.chatId) {
        const chat = await createSessionMutation.mutateAsync({
          title: Array.from(message.replace(/\s+/gu, ' ')).slice(0, 80).join(''),
          savedAssignment: message,
          idempotencyKey: submission.sessionIdempotencyKey,
        });
        if (!actionIsCurrent(actionId)) return;
        submission.chatId = chat.id;
        submission.createdSession = true;
        if (actionIsCurrent(actionId)) {
          expectedSelectedChatId.current = chat.id;
          onSelectChatId(chat.id);
        }
      }

      const job = await createPendingTurn(async (input) => {
        if (!actionIsCurrent(actionId)) throw new DOMException('Action stopped.', 'AbortError');
        return createTurnMutation.mutateAsync(input);
      }, submission);
      if (job.subject.type !== 'research_chat_turn') throw new Error('Unexpected Chat Job subject.');
      if (!actionIsCurrent(actionId)) return;
      if (pendingTask.current === submission) pendingTask.current = null;
      queryClient.setQueryData<ChatJob | null>(
        chatQueryKeys.activeTurn(chatScope, submission.chatId),
        job,
      );
      setTaskText('');
      if (submission.chapterKey) chapter.sent(submission.chapterKey);
      void invalidateAccountQuota(queryClient);
      setCreatedTurn({
        canCancel: job.canCancel,
        chatId: submission.chatId,
        id: job.id,
        status: job.status,
        task: submission.body,
        turnId: job.subject.turnId,
      });
    } catch (error) {
      if (actionIsCurrent(actionId) && !(error instanceof DOMException && error.name === 'AbortError')) {
        if (quotaFailureCode(error)) void invalidateAccountQuota(queryClient);
        showPanelFailure(error, 'start');
      }
    }
  }

  function changeTask(nextTask: string) {
    if (nextTask !== taskText && panelFailure) clearPanelFailure();
    setTaskText(nextTask);
  }

  function submitTask(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    void (steerOpen ? steer.send(taskText.trim()) : startTask(taskText.trim()));
  }

  const canMutate = canCreate && !mutationAuthorityLost;
  const memoryHost = useMemo<ChatMemoryHost>(() => ({ api, projectId, canEdit: canMutate }), [api, canMutate, projectId]);
  const attachmentHost = useMemo<ChatAttachmentHost>(() => ({
    api, projectId, chatId: readableChatId, canEdit: canMutate, openSource: onOpenSource, openDraft: onOpenDraft,
  }), [api, canMutate, onOpenDraft, onOpenSource, projectId, readableChatId]);
  const retryWaiting = typeof retryDelaySeconds === 'number' && retryDelaySeconds > 0;
  const quotaRefusal = panelFailure?.quotaCode ? chatQuotaRefusal(panelFailure.quotaCode, accountQuota) : '';
  const selectedChatMissing = Boolean(selectedChatId && (
    (selectedSessionQuery.isSuccess && !sessionMatchesScope)
    || (isApiStatusFailure(selectedSessionQuery.error) && selectedSessionQuery.error.status === 404)
  ));
  const turnIsActive = jobIsActive;
  const activeTurnJobId = timelineJobId;
  const activeTurnCanCancel = createdTurnForChat?.canCancel ?? recoveredTurnJob?.canCancel ?? false;

  function stopTurn() {
    return stopFlow.stop({
      canCancel: activeTurnCanCancel,
      chatId: readableChatId,
      jobId: activeTurnJobId,
      task: createdTurnForChat?.id === activeTurnJobId ? createdTurnForChat.task : latestUserTask ?? '',
    });
  }

  // Sending a question or deleting a chat holds a new chat back; a working turn does not (R1.1, R1.2).
  const startBusy = createSessionMutation.isPending
    || createTurnMutation.isPending
    || deleteFlow.isPending;
  const mutationBusy = startBusy
    || stoppingTurn
    || turnIsActive;
  const busy = sessionsQuery.isPending || sessionPending || mutationBusy;
  const { failure, loadFailure, sendFailure } = conversationFailure({
    panelFailure, quotaRefusal, selectedChatMissing, jobIsActive, connection: timeline.connection, recoveredJob: recoveredTurnJob,
    sessionError: selectedSessionQuery.error, sessionErrorRequestId: chatErrorRequestId(selectedSessionQuery.error),
  });
  const retryFailure = failure?.action !== 'retry' ? undefined
    : failure === sendFailure ? (canMutate && activeTurnCanCancel ? () => void stopTurn() : undefined)
      : failure === loadFailure ? () => void selectedSessionQuery.refetch() : timeline.retry;
  const emptyMessage = readableChatId
    ? timeline.connection === 'connecting' || timeline.events.length > 0
      ? undefined
      : 'Задайте первый вопрос о саге.'
    : emptyChatMessage(selectedChatId, canCreate, sessions.length);

  return (
    <section className={styles.panel} data-toolbar={Boolean(toolbar) || undefined} aria-label="Исследовательский чат">
      <ResearchChatLayout toolbar={toolbar} sourceOpen={Boolean(sourceNavigation?.target)} working={sessions.filter(chatIsWorking).length} sessions={(onNavigate) => <ResearchChatSessions
          api={api}
          busy={mutationBusy}
          canCreate={canMutate}
          error={sessionsQuery.error}
          errorRequestId={chatErrorRequestId(sessionsQuery.error)}
          loading={sessionsQuery.isPending}
          onAuthorityLost={() => setMutationAuthorityLost(true)}
          onDelete={deleteFlow.show}
          onNotify={deleteFlow.toasts.push}
          onRetry={() => void sessionsQuery.refetch()}
          onSelect={(chatId) => { selectChat(chatId); onNavigate(); }}
          onSessionGone={(chatId) => {
            if (!mounted.current || chatId !== observedSelectedChatId.current) return;
            pendingTask.current = null;
            expectedSelectedChatId.current = '';
            onSelectChatId('');
          }}
          onStartNew={() => { startNewChat(); onNavigate(); }}
          scope={chatScope}
          selectedChatId={selectedChatMissing ? '' : selectedChatId}
          sessions={sessions}
          startBusy={startBusy}
        />}>
        <ChatMemoryProvider value={memoryHost}>{illustrations.provide(<ChatAttachmentHostProvider value={attachmentHost}><ResearchChatConversation
          chatId={readableChatId}
          connection={timeline.connection}
          emptyMessage={emptyMessage}
          examples={!selectedChatId && canMutate && emptyMessage ? <ChatExamples field={questionField} message={emptyMessage} onPick={changeTask} /> : undefined}
          failure={failure}
          events={timeline.events as readonly CanonicalAgentTimelineEvent[]}
          selectedSource={sourceNavigation?.target}
          sourcePane={sourceNavigation?.target && readableChatId ? <ResearchChatSourceInspector api={api} chatId={readableChatId} navigation={sourceNavigation} projectId={projectId} sourceView={sourceView} /> : undefined}
          onOpenSource={onOpenSource}
          jobStatus={jobStatus as AgentJobStatus | null}
          startedAt={jobIsActive ? recoveredTurnJob?.createdAt : undefined}
          pendingQuestion={unseenQuestion?.task}
          pendingSteers={steer.waiting}
          onAskSteer={canMutate ? (text) => { changeTask(text); questionField.current?.focus(); } : undefined}
          preparation={preparation}
          queuePosition={recoveredTurnJob?.id === timelineJobId && recoveredTurnJob.status === 'queued' ? recoveredTurnJob.queuePosition : undefined}
          loading={sessionPending}
          savedSession={savedSession}
          savedBusy={busy || retryWaiting}
          onStartSaved={canMutate && !savedStartUnavailable ? () => void startTask(savedSession?.savedAssignment?.body ?? '') : undefined}
          onStartNew={canMutate ? startNewChat : undefined}
          footer={chatOutdated ? (
            <Alert aria-label="Чат прежнего помощника" className={styles.outdatedNotice}>
              {outdatedChatMessage()}
              {canMutate ? <Button onClick={startNewChat} variant="primary"><Plus aria-hidden="true" size={16} />Новый чат</Button> : null}
            </Alert>
          ) : (readableChatId || !selectedChatId || sessionPending) && canMutate && !selectedChatMissing ? (<>
            <ChatQuotaHint text={quotaRefusal ? '' : chatQuotaHint(accountQuota)} />
            <ResearchChatComposer
              active={turnIsActive}
              attachButton={({ compact, row }) => <ChatChapterButton chapter={chapter} compact={compact} disabled={busy || retryWaiting} row={row} />}
              chapterStrip={<ChatChapterStrip chapter={chapter} />}
              onLongPaste={chapter.offerPaste}
              busy={busy}
              canStop={Boolean(activeTurnJobId && activeTurnCanCancel)}
              canDelete={Boolean(readableChatId)}
              deleteConfirmationOpen={deleteFlow.open}
              deleteError={deleteFlow.error}
              deleteErrorCode={deleteFlow.errorCode}
              deleteTitle={deleteFlow.selectedTitle}
              deleting={deleteFlow.isPending}
              fieldRef={questionField}
              onCancelDelete={deleteFlow.dismiss}
              onConfirmDelete={() => void deleteFlow.confirm()}
              onDelete={deleteFlow.show}
              onStop={() => void stopTurn()}
              onTaskChange={changeTask}
              onSubmit={submitTask}
              // The one «Спросить снова» of a failure lives here, with the question in the field (R4.2.3).
              recovery={Boolean(taskText) && (failure ? failure.action === 'ask_again' : latestTurnFailed)}
              retryAfterSeconds={retryDelaySeconds}
              retryUnavailable={savedStartUnavailable}
              steering={steerOpen}
              steerSending={steer.sending}
              stopping={stoppingTurn}
              taskText={taskText}
            />
          </>) : null}
          onRetry={retryFailure}
          onReturnToChats={selectedChatMissing ? () => selectChat('') : undefined}
          onStartIndexing={onStartIndexing}
        /></ChatAttachmentHostProvider>)}</ChatMemoryProvider>
      </ResearchChatLayout>
      <ToastRegion onDismiss={deleteFlow.toasts.dismiss} queue={deleteFlow.toasts.queue} />
      {illustrations.overlays}
    </section>
  );
}
