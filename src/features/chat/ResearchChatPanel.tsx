import { useQueryClient } from '@tanstack/react-query';
import { type FormEvent, type ReactNode, useEffect, useMemo, useRef, useState } from 'react';
import { isApiStatusFailure } from '../../entities/api-errors';
import { invalidateAccountQuota, quotaFailureCode, useAccountQuota } from '../../entities/account-quota/api';
import { useCanonicalAgentTimeline } from '../../entities/agent-timeline/api';
import { type FirstQuestionsApi, useSagaFirstQuestionsQuery } from '../../entities/saga/api';
import {
  chatQueryKeys,
  type ChatApiClient,
  type ChatJob,
  type SourceView,
} from '../../entities/chat/api';
import type { AgentJobStatus, CanonicalAgentTimelineEvent } from './feed/types';
import { FilePlus2, Plus } from 'lucide-react';
import { useAccountSession } from '../../shared/session/account-session';
import { Button } from '../../ui/controls/Button';
import { Alert } from '../../ui/feedback/Alert';
import { useToasts } from '../../ui/feedback/useToastQueue';
import { Confirm } from '../../ui/overlays/Confirm';
import { refusalLine } from '../../entities/account-quota/refusal-words';
import { type AnswerAsk } from './AnswerActions';
import { askWhereQuestion, phraseAt } from './source-phrase';
import { ChatExamples } from './ChatExamples';
import { ResearchChatComposer } from './ResearchChatComposer';
import { useChatFieldNotes } from './ChatLimitNotice';
import { ChatActionsButton } from './ChatActionsMenu';
import { ChatDeleteDialog } from './ChatDeleteDialog';
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
import type { Blank } from './brackets';
import { questionSlot, useQuestionField } from './question-draft';
import { ChatChapterButton, ChatChapterStrip, type ChapterQuickActions } from './ChatChapterField';
import { chatQuotaHint } from './chat-quota';
import { useChatDrop } from './chat-drop';
import { ChatAttachmentHostProvider } from './ChatAttachmentCard';
import {
  conversationFailure,
  latestUserBody,
  outdatedChatMessage,
  useResearchChatFailure,
  useStreamLostLong,
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
export type ChatSurfaceApi = ChatApiClient & ChatMemoryHost['api'] & FirstQuestionsApi;

/** The chat without the proofreading of the site: the chapter at the field has no «Вычитать». */
function noProofreadChapter(): undefined {
  return undefined;
}

/** The chat without the drawings of the site: nothing around the conversation, no overlays. */
function noDrawings(_options: ChatDrawingsOptions) {
  return { provide: (conversation: ReactNode) => conversation, overlays: null };
}

interface ResearchChatPanelProps {
  /** Who writes here: the draft of a question is his own (specs/2026-11-10-ux1-chat.md CH1.6). */
  authorId?: string | undefined;
  /** The demo saga of the site: an empty chat begins with its questions (specs/2026-11-09-demo-saga.md DS4.3). */
  demo?: boolean;
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
  /**
   * Why no question goes now, as the server says it — the reading of the saga waits for the helper
   * (specs/2026-11-12-desk-window.md WN3.3): the field keeps the words, nothing is sent; questions are still put into it.
   */
  unavailable?: string | null;
  /** The draft «Вычитать и вернуть .docx» made of an attached chapter, in «Письмо» (specs/2026-11-07-chat-attach-chapter.md CA5.3). */
  onOpenDraft?: (draftId: string) => void;
  /** «Что помнит помощник» of the saga, for what a turn remembered (specs/2026-11-10-ux1-chat.md CH4.2). */
  memoryLink?: ChatMemoryHost['memoryLink'];
  /** «Что дальше» under the first answer of the saga (specs/2026-11-10-ux3-first-screen.md FS3.1): its one chat shows it. */
  whatNext?: ReactNode;
  projectId: string;
  selectedChatId: string;
  sourceView: SourceView;
  /**
   * The row of the panel the page keeps with its heading (specs/2026-11-11-ux2-polish.md PL2.1): «Новый чат», «Чаты» and
   * the actions of the open chat stand in it. Without it the panel keeps its own row of «Чаты».
   */
  row?: HTMLElement | null;
}

type CreatedTurnState = CreatedChatTurn & Pick<ChatJob, 'canCancel' | 'id'> & { task: string };
export function ResearchChatPanel({
  api,
  authorId,
  canCreate,
  demo = false,
  initialQuestion,
  illustrationRoutes,
  onOpenSource,
  onSelectChatId,
  onStartIndexing,
  onOpenDraft,
  memoryLink,
  preparation,
  projectId,
  selectedChatId,
  sourceView,
  sourceNavigation,
  row,
  unavailable = null,
  whatNext,
}: ResearchChatPanelProps) {
  // What the author typed is kept in the browser by chat until the server takes it (specs/2026-11-10-ux1-chat.md CH1).
  const field = useQuestionField({ projectId, authorId }, selectedChatId, initialQuestion);
  const [createdTurn, setCreatedTurn] = useState<CreatedTurnState | null>(null);
  const [mutationAuthorityLost, setMutationAuthorityLost] = useState(false);
  // «Переименовать» of the actions over the feed: the chat list takes it (CH2.2).
  const [renameRequest, setRenameRequest] = useState('');
  // A question of a button under an answer that waits for «Заменить набранное?» (AA1.3).
  const [replacing, setReplacing] = useState<string | null>(null);
  const questionField = useRef<HTMLTextAreaElement>(null);
  // The chapter at the field is kept by chat with the question, until the question goes (AA2.4, CA7).
  const chapter = useChatChapter(questionSlot(projectId, selectedChatId, authorId));
  const { clearPanelFailure, failure: panelFailure, retryDelaySeconds, showPanelFailure } = useResearchChatFailure(() => setMutationAuthorityLost(true));
  const pendingTask = useRef<PendingTask | null>(null);
  const activeAction = useRef(0);
  const expectedSelectedChatId = useRef<string | null>(null);
  const mounted = useRef(false);
  const observedSelectedChatId = useRef(selectedChatId);
  const queryClient = useQueryClient();
  const chatSlot = useChatSlot();
  const chatScope = { projectId, sourceView };
  const restoreQuestion = field.restore;
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
  const toasts = useToasts();
  const deleteFlow = useResearchChatDelete({
    api,
    isCurrentChat: () => mounted.current && observedSelectedChatId.current === selectedChatId,
    onAuthorityLost: () => setMutationAuthorityLost(true),
    onDeleted: (nextChatId) => {
      pendingTask.current = null;
      expectedSelectedChatId.current = nextChatId;
      onSelectChatId(nextChatId);
      field.sent(selectedChatId);
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
  const taskText = field.text ?? savedSession?.savedAssignment?.body ?? '';
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
  const queuePosition = recoveredTurnJob?.id === timelineJobId && recoveredTurnJob.status === 'queued' ? recoveredTurnJob.queuePosition : undefined;
  // A question in the queue has not started: the server takes its correction only once the helper works (CH5.1).
  const steerWaits = steerOpen && typeof queuePosition === 'number';
  const steer = useResearchChatSteer({
    active: steerOpen, api, chatId: readableChatId, events: timeline.events as readonly CanonicalAgentTimelineEvent[], turnId: runningTurnId,
    jobId: timelineJobId, scope: chatScope,
    action: () => activeAction.current,
    stillCurrent: (chatId, action) => actionIsCurrent(action) && observedSelectedChatId.current === chatId,
    clearFailure: () => clearPanelFailure(),
    onFailure: (error) => showPanelFailure(error, 'steer'),
    // What the author wrote while it was sent stays.
    onSent: (message) => field.corrected(readableChatId, message),
    onEnded: (jobId) => setCreatedTurn((current) => current?.id === jobId ? null : current),
  });
  const useDrawings = chatSlot?.drawings?.useDrawings ?? noDrawings;
  // «Вычитать» of the chapter at the field is the site's (AA4.1); the desktop has no proofreading.
  const useProofreadChapter = chatSlot?.proofread?.useProofreadChapter ?? noProofreadChapter;
  const proofreadChapter = useProofreadChapter({ projectId, canEdit: canCreate && !mutationAuthorityLost, openDraft: onOpenDraft }, chapter.chapter);
  const illustrations = useDrawings({
    scope: chatScope, canEdit: canCreate && !mutationAuthorityLost,
    ...illustrationRoutes, onOpenSource,
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

  // The field takes the draft of the chat the author goes to, not when the panel opens: a question brought from the
  // saga map waits there (saga map R5.6).
  const fieldChat = useRef(selectedChatId);
  useEffect(() => {
    stopFlow.reset();
    const changed = fieldChat.current !== selectedChatId;
    fieldChat.current = selectedChatId;
    const keepFirstTask = pendingTask.current?.chatId === selectedChatId;
    // The chapter at the field follows the chat by itself: it is kept with the question of each (AA2.4).
    if (!keepFirstTask && changed) field.open(selectedChatId);
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

  /**
   * A question from the field, with the chapter attached to it (specs/2026-11-07-chat-attach-chapter.md CA7) — or the
   * question of a ready action of the chapter (AA4.1), which leaves what the author typed in the field.
   */
  async function startTask(message: string, attached = chapter.chapter, fromField = true) {
    if (!message || busy || !canMutate || unavailable || savedStartUnavailable || chatOutdated || (selectedChatId && !readableChatId)) return;
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
        if (fromField || taskText.trim()) field.moved(chat.id, fromField ? message : taskText);
        if (attached) chapter.moved(questionSlot(projectId, chat.id, authorId));
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
      if (fromField) field.sent(submission.chatId);
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

  /** «Повторить вопрос» of a stopped or failed turn: the question stands in the field, the author sends it (CW2.8). */
  function repeatQuestion(text: string) {
    changeTask(text, []);
    questionField.current?.focus();
  }

  /** A question put into the field by a button: the focus stands at its end, nothing is sent (AA1.3, AA1.5). */
  function putQuestion(text: string) {
    changeTask(text, []);
    window.requestAnimationFrame(() => {
      const node = questionField.current;
      if (!node) return;
      node.focus();
      node.setSelectionRange(node.value.length, node.value.length);
    });
  }

  /** What the author typed is not lost: another question asks first (AA1.3). */
  function askFromAnswer(text: string) {
    const typed = taskText.trim();
    if (typed && typed !== text.trim()) {
      setReplacing(text);
      return;
    }
    putQuestion(text);
  }
  const askFromAnswerRef = useRef(askFromAnswer);
  askFromAnswerRef.current = askFromAnswer;

  /** What the author typed, or what a button put into the field with the blanks of its example (none for a whole question). */
  function changeTask(nextTask: string, blanks?: readonly Blank[]) {
    if (nextTask !== taskText && panelFailure) clearPanelFailure();
    field.change(nextTask, blanks);
  }

  function submitTask(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (steerWaits) return;
    void (steerOpen ? steer.send(taskText.trim()) : startTask(taskText.trim()));
  }

  const canMutate = canCreate && !mutationAuthorityLost;
  // Why a button under an answer cannot put a question now; it stays and says so (AA1.3).
  const answerAskUnavailable = jobIsActive ? 'Дождитесь ответа на идущий вопрос.'
    : accountQuota?.chatTurns && accountQuota.chatTurns.limit > 0 && accountQuota.chatTurns.used >= accountQuota.chatTurns.limit
      ? refusalLine('quota.chat_turns_daily', accountQuota) ?? null : null;
  // The page keeps one function for the buttons of every answer: typing in the field draws no answer anew.
  const answerAsk = useMemo<AnswerAsk | undefined>(() => canMutate && !chatOutdated
    ? { put: (text) => askFromAnswerRef.current(text), unavailable: answerAskUnavailable }
    : undefined, [answerAskUnavailable, canMutate, chatOutdated]);
  const memoryHost = useMemo<ChatMemoryHost>(() => ({ api, projectId, canEdit: canMutate, memoryLink }), [api, canMutate, memoryLink, projectId]);
  const attachmentHost = useMemo<ChatAttachmentHost>(() => ({
    api, projectId, chatId: readableChatId, canEdit: canMutate, openSource: onOpenSource, openDraft: onOpenDraft,
  }), [api, canMutate, onOpenDraft, onOpenSource, projectId, readableChatId]);
  const retryWaiting = typeof retryDelaySeconds === 'number' && retryDelaySeconds > 0;
  // The ready actions of the chapter at the field (AA4.1): two questions that go with it at once and its proofreading.
  const quickHint = chatQuotaHint(accountQuota, 'question');
  const chapterActions: ChapterQuickActions = {
    ask: { onAsk: (question) => void startTask(question, chapter.chapter, false), unavailable: unavailable ?? answerAskUnavailable },
    price: `«Сверить с книгой» и «Найти противоречия» — это вопрос чата${quickHint ? `: ${quickHint.charAt(0).toLowerCase()}${quickHint.slice(1)}` : '.'}${proofreadChapter ? ' «Вычитать» вопроса не тратит.' : ''}`,
    proofread: proofreadChapter,
  };
  // A refusal by a limit is one block above the field; it stands in advance once the day has nothing left (CH3.3, CH3.4).
  const fieldNotes = useChatFieldNotes({
    quota: accountQuota, refusedCode: panelFailure?.quotaCode ?? '', turnActive: jobIsActive, text: taskText,
    running: sessions.filter(chatIsWorking).map((session) => ({ id: session.id, title: session.title })),
    onOpenChat: (chatId) => selectChat(chatId),
    mode: steerWaits ? 'waiting' : steerOpen ? 'correction' : 'question',
  });
  const limitNotice = fieldNotes.limit;
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
  // A chapter dropped on the panel goes where «Приложить главу» puts it, while the field takes one (AA4.4).
  const emailUnverified = Boolean(useAccountSession()?.emailVerification);
  // A short break of the stream is a quiet line of the feed; a block only once it did not mend in a minute (AA2.6).
  const streamLostLong = useStreamLostLong(timeline.connection);
  const { failure, loadFailure, sendFailure } = conversationFailure({
    panelFailure, limitNamed: Boolean(limitNotice?.refused), selectedChatMissing, jobIsActive, connection: streamLostLong ? timeline.connection : 'connected', recoveredJob: recoveredTurnJob,
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
  // The questions with the names of the book (specs/2026-11-10-first-questions.md FQ1.5); the demo saga has its own.
  const drop = useChatDrop({
    enabled: canMutate && !chatOutdated && !selectedChatMissing && !jobIsActive && !busy && !retryWaiting && !savedStartUnavailable && !emailUnverified,
    onFile: chapter.attachFile,
  });
  const firstQuestions = useSagaFirstQuestionsQuery(api, projectId, sourceView, !selectedChatId && canMutate && Boolean(emptyMessage) && !demo).data?.questions;

  return (
    <section className={styles.panel} data-row={row !== undefined || undefined} aria-label="Исследовательский чат" {...drop.handlers}>
      {drop.dragging ? <div className={styles.dropOverlay}><FilePlus2 aria-hidden="true" size={28} /><strong>Отпустите, чтобы приложить главу</strong></div> : null}
      <ResearchChatLayout row={row} sourceOpen={Boolean(sourceNavigation?.target)} working={sessions.filter(chatIsWorking).length}
        actions={canMutate && deleteFlow.selectedTitle && !selectedChatMissing ? (showSessions) => <ChatActionsButton
          deleteUnavailable={turnIsActive ? 'Удалить чат можно, когда ответ будет готов или остановлен.' : null}
          disabled={startBusy || stoppingTurn}
          onDelete={() => deleteFlow.show()}
          onRename={() => { setRenameRequest(selectedChatId); showSessions(); }}
          title={deleteFlow.selectedTitle}
        /> : undefined}
        sessions={(onNavigate, newChatIn) => <ResearchChatSessions
          api={api}
          canCreate={canMutate}
          error={sessionsQuery.error}
          errorRequestId={chatErrorRequestId(sessionsQuery.error)}
          loading={sessionsQuery.isPending}
          newChatIn={newChatIn}
          onAuthorityLost={() => setMutationAuthorityLost(true)}
          onDelete={deleteFlow.show}
          onNotify={toasts.push}
          onRenameStarted={() => setRenameRequest('')}
          onRetry={() => void sessionsQuery.refetch()}
          onSelect={(chatId) => { selectChat(chatId); onNavigate(); }}
          onSessionGone={(chatId) => {
            if (!mounted.current || chatId !== observedSelectedChatId.current) return;
            pendingTask.current = null;
            expectedSelectedChatId.current = '';
            onSelectChatId('');
          }}
          onStartNew={() => { startNewChat(); onNavigate(); }}
          renameRequest={renameRequest}
          scope={chatScope}
          selectedChatId={selectedChatMissing ? '' : selectedChatId}
          sessions={sessions}
          startBusy={startBusy}
        />}>
        <ChatMemoryProvider value={memoryHost}>{illustrations.provide(<ChatAttachmentHostProvider value={attachmentHost}><ResearchChatConversation
          answerAsk={answerAsk}
          chatId={readableChatId}
          connection={timeline.connection}
          emptyMessage={emptyMessage}
          examples={!selectedChatId && canMutate && emptyMessage
            ? (demo ? chatSlot?.demoExamples?.({ field: questionField, onPick: (question) => changeTask(question, []) }) : null)
              ?? <ChatExamples field={questionField} message={emptyMessage} onPick={changeTask} questions={firstQuestions} typed={Boolean(taskText)} />
            : undefined}
          failure={failure}
          firstAnswerNext={sessions.length === 1 && sessions[0]?.id === readableChatId ? whatNext : undefined}
          events={timeline.events as readonly CanonicalAgentTimelineEvent[]}
          selectedSource={sourceNavigation?.target}
          sourcePane={sourceNavigation?.target && readableChatId ? <ResearchChatSourceInspector api={api} chatId={readableChatId} chatTitle={selectedSession?.title} navigation={sourceNavigation} projectId={projectId} sourceView={sourceView}
            onAskWhere={answerAsk && !answerAsk.unavailable ? () => {
              // The link the panel stands for is the one open beside the chat; after a reload it is found the same way.
              const link = document.querySelector<HTMLElement>('[data-chat-source][aria-expanded="true"]');
              const question = askWhereQuestion(link ? phraseAt(link) : '');
              sourceNavigation.close();
              askFromAnswer(question);
            } : undefined} /> : undefined}
          onOpenSource={onOpenSource}
          jobStatus={jobStatus as AgentJobStatus | null}
          startedAt={jobIsActive ? recoveredTurnJob?.createdAt : undefined}
          // «Остановить» stands in the status of the turn, the field keeps one action (specs/2026-11-10-ux4-chat-waiting.md CW2.4).
          stop={turnIsActive && canMutate ? { canStop: Boolean(activeTurnJobId && activeTurnCanCancel), stopping: stoppingTurn || jobStatus === 'canceling', onStop: () => void stopTurn() } : undefined}
          onRepeatQuestion={canMutate ? repeatQuestion : undefined}
          pendingQuestion={unseenQuestion?.task}
          pendingSteers={steer.waiting}
          onAskSteer={canMutate ? (text) => { changeTask(text, []); questionField.current?.focus(); } : undefined}
          preparation={preparation}
          queuePosition={queuePosition}
          loading={sessionPending}
          savedSession={savedSession}
          savedInField={canMutate && !chatOutdated}
          onStartNew={canMutate ? startNewChat : undefined}
          footer={chatOutdated ? (
            <Alert aria-label="Чат прежнего помощника" className={styles.outdatedNotice}>
              {outdatedChatMessage()}
              {canMutate ? <Button onClick={startNewChat} variant="primary"><Plus aria-hidden="true" size={16} />Новый чат</Button> : null}
            </Alert>
          ) : (readableChatId || !selectedChatId || sessionPending) && canMutate && !selectedChatMissing ? (
            <ResearchChatComposer
              above={fieldNotes.above}
              describedBy={fieldNotes.describedBy}
              active={turnIsActive}
              attachButton={({ compact, row }) => <ChatChapterButton chapter={chapter} compact={compact} disabled={busy || retryWaiting} row={row} />}
              chapterStrip={<ChatChapterStrip actions={chapterActions} chapter={chapter} onOpenBooks={onStartIndexing} />}
              chapterAttached={Boolean(chapter.chapter)}
              onLongPaste={chapter.offerPaste}
              blanks={field.text === null ? undefined : field.blanks}
              busy={busy}
              demo={demo}
              fieldRef={questionField}
              onTaskChange={(text) => changeTask(text)}
              onSubmit={submitTask}
              // The one «Спросить снова» of a failure lives here, with the question in the field (R4.2.3).
              recovery={Boolean(taskText) && !limitNotice && (failure ? failure.action === 'ask_again' : latestTurnFailed)}
              retryAfterSeconds={retryDelaySeconds}
              retryUnavailable={savedStartUnavailable}
              steering={steerOpen}
              steerSending={steer.sending}
              steerWaits={steerWaits}
              spent={limitNotice?.code === 'quota.chat_turns_daily'}
              stopping={stoppingTurn || jobStatus === 'canceling'}
              taskText={taskText}
              unavailable={unavailable}
            />
          ) : null}
          onRetry={retryFailure}
          onReturnToChats={selectedChatMissing ? () => selectChat('') : undefined}
          onStartIndexing={onStartIndexing}
        /></ChatAttachmentHostProvider>)}</ChatMemoryProvider>
      </ResearchChatLayout>
      <ChatDeleteDialog flow={deleteFlow} />
      {replacing !== null ? (
        <Confirm action="Заменить" consent="no" onCancel={() => setReplacing(null)} title="Заменить набранное?"
          onConfirm={() => { putQuestion(replacing); setReplacing(null); }}>
          {`В поле уже есть ваш текст. Вместо него встанет: «${replacing.trim()}»`}
        </Confirm>
      ) : null}
      {illustrations.overlays}
    </section>
  );
}
