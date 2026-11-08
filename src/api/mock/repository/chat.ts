import type {
  ChapterFileInput,
  ChatAttachment,
  ChatChapterLocation,
  ChatSession,
  ListChatSessionsOptions,
  OperationRequest,
  OperationResponse,
  RequiredIdempotencyOptions,
} from '../../../shared/api';
import { clone, createJob, findById, now, paginateList, startResponse } from './common';
import type { MockRepositoryContext } from './context';
import { agentWorkflowScenario, MockApiError } from '../scenarios';
import { mockAsksToDraw } from './agent-timeline';
import { readChapterFile } from './chapter-draft-proofread';

/** The limit of an attached chapter, as the server holds it (specs/2026-11-07-chat-attach-chapter.md CA0.1). */
const attachmentSigns = 400_000;

/** The codes of the chat for the refusals of a chapter file (CA2.3). */
const attachmentCodes: Record<string, string> = {
  'chapter_draft.file_too_large': 'chat.attachment_too_large',
  'chapter_draft.file_format': 'chat.attachment_format',
  'chapter_draft.no_text': 'chat.attachment_empty',
};

export function createChatOperations(context: MockRepositoryContext) {
  function readableChat(chatId: string, operationId: 'getChatSession' | 'resolveChatChapterKey' | 'updateChatSession' | 'deleteChatSession' | 'createChatTurn' | 'createChatTurnWithChapter' | 'getChatAttachment' | 'steerChatTurn') {
    const chat = findById(context.state.chats, chatId, operationId);
    context.requireReadableSourceView(operationId, chat.projectId, chat.sourceView);
    return chat;
  }

  /** The chat as the server reads it: with the status of its latest turn's Job. */
  function visibleChat(chat: ChatSession): ChatSession {
    const job = context.state.jobs.find((item) => item.id === chat.latestTurnJobId);
    return clone(job ? { ...chat, latestTurnStatus: job.status } : chat);
  }

  async function listChatSessions(
    projectId: string,
    options: ListChatSessionsOptions,
  ): Promise<OperationResponse<'listChatSessions', '200'>> {
    context.applyOperation('listChatSessions');
    context.requireProjectAccess('listChatSessions', projectId);
    // One list per project; a viewer still sees only the chats of the published text.
    const readsDraft = context.actorRole(projectId) !== 'viewer';
    // The sessions of the helpers of the drawing workshop are not chats (specs/2026-11-02-image-workshop.md IW4.2).
    const helperSessions = new Set([...context.state.gallery.drawings.map((drawing) => drawing.chatId), ...context.state.draftHelperChatIds]);
    const chats = context.state.chats
      .filter((chat) => chat.projectId === projectId && (chat.sourceView === 'published' || readsDraft) && !helperSessions.has(chat.id))
      .sort((left, right) => right.updatedAt.localeCompare(left.updatedAt))
      .map(visibleChat);
    return paginateList('listChatSessions', chats, options, { projectId });
  }

  async function createChatSession(
    projectId: string,
    body: OperationRequest<'createChatSession'>,
    options: RequiredIdempotencyOptions,
  ): Promise<OperationResponse<'createChatSession', '201'>> {
    context.applyOperation('createChatSession');
    context.requireProjectAccess('createChatSession', projectId, ['editor', 'admin', 'owner']);
    const sourceView = context.requireReadableSourceView('createChatSession', projectId, body.sourceView);
    if (body.scope !== 'project' || 'bookId' in body) {
      throw new MockApiError(400, 'Chat scope must be project.', 'createChatSession', 'validation_failed');
    }
    const savedAssignment = body.savedAssignment?.trim();
    if (body.savedAssignment !== undefined && (!savedAssignment || [...savedAssignment].length > 8000)) {
      throw new MockApiError(400, 'Saved assignment is invalid.', 'createChatSession', 'validation_failed');
    }
    const idempotencyBody = savedAssignment === undefined ? body : { ...body, savedAssignment };
    return context.idempotentCommand('createChatSession', projectId, options, idempotencyBody, () => {
      const number = context.state.nextAgentId.chatSession++;
      const chat: ChatSession = {
        id: `chat-${number}`,
        projectId,
        sourceView,
        scope: body.scope,
        title: body.title?.trim() || 'Новый чат',
        savedAssignment: savedAssignment ? { body: savedAssignment, availableActions: ['start'] } : null,
        outdated: false,
        createdAt: now,
        updatedAt: now,
      } as ChatSession;
      context.state.chats.push(chat);
      return clone(chat);
    });
  }

  async function getChatSession(chatId: string): Promise<OperationResponse<'getChatSession', '200'>> {
    context.applyOperation('getChatSession');
    return visibleChat(readableChat(chatId, 'getChatSession'));
  }

  /** Keys follow the server rule: the global number from «Глава N» when unique, else b<book>c<order>. */
  async function resolveChatChapterKey(chatId: string, chapterKey: string): Promise<ChatChapterLocation> {
    const operation = 'resolveChatChapterKey';
    context.applyOperation(operation);
    const chat = readableChat(chatId, operation);
    const books = context.state.books.filter((book) => book.projectId === chat.projectId);
    const chapters = context.state.chapters.filter((chapter) => books.some((book) => book.id === chapter.bookId) &&
      (chat.sourceView === 'draft' || chapter.status === 'published' || chapter.status === 'stale'));
    const numberOf = (title: string) => Number(/^\s*глава\s+(\d+)/iu.exec(title)?.[1] ?? 0);
    const counts = new Map<number, number>();
    for (const chapter of chapters) counts.set(numberOf(chapter.title), (counts.get(numberOf(chapter.title)) ?? 0) + 1);
    const found = chapters.find((chapter) => {
      const book = books.find((item) => item.id === chapter.bookId)!;
      const number = numberOf(chapter.title);
      const key = number > 0 && counts.get(number) === 1 ? String(number) : `b${book.order}c${chapter.order}`;
      return key === chapterKey;
    });
    if (!found) throw new MockApiError(404, 'Chapter key not found.', operation);
    const book = books.find((item) => item.id === found.bookId)!;
    return { chapterKey, bookId: book.id, chapterId: found.id, bookTitle: book.title, title: found.title };
  }

  async function renameChatSession(
    chatId: string,
    body: OperationRequest<'updateChatSession'>,
  ): Promise<OperationResponse<'updateChatSession', '200'>> {
    context.applyOperation('updateChatSession');
    const chat = readableChat(chatId, 'updateChatSession');
    context.requireProjectAccess('updateChatSession', chat.projectId, ['editor', 'admin', 'owner']);
    if (body.title === undefined) {
      throw new MockApiError(400, 'Nothing to change.', 'updateChatSession', 'validation_failed');
    }
    const title = body.title.trim();
    if (!title) {
      throw new MockApiError(422, 'Title is empty.', 'updateChatSession', 'title_empty');
    }
    if ([...title].length > 120) {
      throw new MockApiError(422, 'Title exceeds 120 characters.', 'updateChatSession', 'title_too_long');
    }
    chat.title = title;
    chat.updatedAt = now;
    return clone(chat);
  }

  async function deleteChatSession(chatId: string): Promise<void> {
    context.applyOperation('deleteChatSession');
    const chat = readableChat(chatId, 'deleteChatSession');
    context.requireProjectAccess('deleteChatSession', chat.projectId, ['editor', 'admin', 'owner']);
    context.state.chats = context.state.chats.filter((item) => item.id !== chat.id);
    // The chapters attached to its questions go with the chat (CA6.2); a draft made of one stays.
    delete context.state.chatAttachments[chat.id];
    context.state.jobs = context.state.jobs.filter((job) => (
      job.subject.type !== 'research_chat_turn' || job.subject.chatId !== chat.id
    ));
  }

  /**
   * A question of the chat — or, from the workshop mocks, a message of a helper: only the helper's message carries the
   * author's images (the chat takes none, image-workshop IW7.2), as the backend's start of a turn takes them.
   */
  async function createChatTurn(
    chatId: string,
    body: OperationRequest<'createChatTurn'> & { images?: readonly number[] },
    options: RequiredIdempotencyOptions,
  ): Promise<OperationResponse<'createChatTurn', '202'>> {
    context.applyOperation('createChatTurn');
    const chat = readableChat(chatId, 'createChatTurn');
    context.requireProjectAccess('createChatTurn', chat.projectId, ['editor', 'admin', 'owner']);
    if (chat.outdated) {
      throw new MockApiError(409, 'chat was created by a previous assistant version; start a new chat', 'createChatTurn', 'chat.outdated_agent');
    }
    const assignment = body.body.trim();
    if (!assignment) throw new MockApiError(400, 'Message body is required.', 'createChatTurn', 'validation_failed');
    if (chat.savedAssignment && chat.savedAssignment.body !== assignment) {
      throw new MockApiError(409, 'Saved assignment does not match.', 'createChatTurn', 'saved_assignment_mismatch');
    }
    // The images of a helper's message are ready images of the project's gallery.
    const images = body.images ?? [];
    if (images.length > 8 || new Set(images).size !== images.length) {
      throw new MockApiError(400, 'validation failed', 'createChatTurn', 'request.validation');
    }
    if (images.some((number) => !context.state.gallery.images.some((image) => (
      image.projectId === chat.projectId && image.number === number && image.status === 'ready'
    )))) {
      throw new MockApiError(404, 'Image not found.', 'createChatTurn', 'not_found');
    }
    return context.idempotentCommand('createChatTurn', chatId, options, { ...body, body: assignment }, () => {
      const started = createTurn(chat);
      const subject = started.job.subject;
      if (images.length && subject.type === 'research_chat_turn') {
        context.state.gallery.turnQuestions[subject.turnId] = { text: assignment, images: [...images] };
      }
      if (mockAsksToDraw(assignment) && subject.type === 'research_chat_turn') context.state.gallery.drawingRequests[subject.turnId] = true;
      return started;
    });
  }

  /**
   * A question with the author's chapter attached (specs/2026-11-07-chat-attach-chapter.md CA2): the chapter is read as the
   * proofreading of the mock reads it and numbered in its chat; the question goes as any question.
   */
  async function createChatTurnWithChapter(
    chatId: string,
    input: ChapterFileInput & { body: string },
    options: RequiredIdempotencyOptions,
  ): Promise<OperationResponse<'createChatTurnWithChapter', '202'>> {
    const operation = 'createChatTurnWithChapter';
    context.applyOperation(operation);
    const chat = readableChat(chatId, operation);
    context.requireProjectAccess(operation, chat.projectId, ['editor', 'admin', 'owner']);
    const question = input.body.trim();
    if (!question || [...question].length > 8000) throw new MockApiError(400, 'validation failed', operation, 'request.validation');
    const read = await readChapterFile(input.file ? { file: input.file } : { text: input.text ?? '' }, 'startChapterProofread').catch((error: unknown) => {
      if (error instanceof MockApiError && error.code && attachmentCodes[error.code]) {
        throw new MockApiError(error.status, error.message, operation, attachmentCodes[error.code]);
      }
      throw error;
    });
    if ([...read.text].length > attachmentSigns) throw new MockApiError(422, 'The chapter is longer than 400 000 signs.', operation, 'chat.attachment_too_long');
    const fileName = input.file?.name ?? '';
    const title = read.title || fileName.replace(/\.[^.]+$/u, '');
    const intent = { body: question, chapter: input.file ? `${fileName}:${input.file.size}` : input.text };
    return context.idempotentCommand(operation, chatId, options, intent, () => {
      const started = createTurn(chat);
      const subject = started.job.subject;
      const attachments = context.state.chatAttachments[chatId] ??= [];
      const paragraphs = read.text.split('\n\n');
      const attachment: ChatAttachment = {
        number: attachments.length + 1, title, ...(fileName ? { fileName } : {}), format: read.format, paragraphs,
        words: paragraphs.join(' ').split(/\s+/u).filter(Boolean).length, createdAt: now,
      };
      attachments.push(attachment);
      if (subject.type === 'research_chat_turn') {
        context.state.gallery.turnQuestions[subject.turnId] = {
          text: question, images: [], attachment: { number: attachment.number, title: attachment.title, words: attachment.words },
        };
      }
      return started;
    });
  }

  /** The chapter attached to a question of the chat (CA4.3). */
  async function getChatAttachment(chatId: string, number: number): Promise<ChatAttachment> {
    context.applyOperation('getChatAttachment');
    readableChat(chatId, 'getChatAttachment');
    const attachment = context.state.chatAttachments[chatId]?.find((item) => item.number === number);
    if (!attachment) throw new MockApiError(404, 'Attachment not found.', 'getChatAttachment');
    return clone(attachment);
  }

  /** A correction of the running turn (specs/2026-10-30-chat-steering.md): no Job and no quota. */
  async function steerChatTurn(
    chatId: string,
    turnId: string,
    body: OperationRequest<'steerChatTurn'>,
    options: RequiredIdempotencyOptions,
  ): Promise<OperationResponse<'steerChatTurn', '202'>> {
    context.applyOperation('steerChatTurn');
    const chat = readableChat(chatId, 'steerChatTurn');
    context.requireProjectAccess('steerChatTurn', chat.projectId, ['editor', 'admin', 'owner']);
    const job = context.state.jobs.find((item) => item.subject.type === 'research_chat_turn' && item.subject.chatId === chatId && item.subject.turnId === turnId);
    if (!job) throw new MockApiError(404, 'Turn not found.', 'steerChatTurn', 'not_found');
    if (job.status !== 'queued' && job.status !== 'running') {
      throw new MockApiError(409, 'the turn has already ended', 'steerChatTurn', 'chat.turn_ended');
    }
    const text = body.body.trim();
    if (!text) throw new MockApiError(400, 'validation failed', 'steerChatTurn', 'request.validation');
    const images = body.attachedImages ?? [];
    if (images.some((number) => !context.state.gallery.images.some((image) => (
      image.projectId === chat.projectId && image.number === number && image.status === 'ready'
    )))) {
      throw new MockApiError(404, 'Image not found.', 'steerChatTurn', 'not_found');
    }
    return context.idempotentCommand('steerChatTurn', `${chatId}:${turnId}`, options, { ...body, body: text }, () => {
      const steerId = `steer-${turnId}-${context.state.nextAgentId.chatTurn}-${(context.state.gallery.turnSteers[turnId] ?? []).length + 1}`;
      (context.state.gallery.turnSteers[turnId] ??= []).push({
        steerId, text, images: [...images], ...(body.firstPhoto ? { firstPhoto: body.firstPhoto } : {}),
      });
      return { steerId };
    });
  }

  function createTurn(chat: ChatSession): OperationResponse<'createChatTurn', '202'> {
    const number = context.state.nextAgentId.chatTurn++;
    const turnId = `chat-turn-${number}`;
    const jobId = `chat-job-${number}`;
    const subject = { type: 'research_chat_turn' as const, projectId: chat.projectId, chatId: chat.id, turnId, sourceView: chat.sourceView, scope: 'project' as const };
    const job = createJob({
      id: jobId, kind: 'research_chat_turn', phase: 'waiting', status: agentWorkflowScenario.jobTransition.start, progress: 0, subject,
      result: null, canCancel: true,
    });
    context.state.jobs.push(job);
    context.markJobCreator(job);
    chat.latestTurnId = turnId;
    chat.latestTurnJobId = jobId;
    chat.savedAssignment = null;
    chat.updatedAt = now;
    return startResponse(context.visibleJob(job));
  }

  return {
    listChatSessions,
    createChatSession,
    getChatSession,
    resolveChatChapterKey,
    renameChatSession,
    deleteChatSession,
    createChatTurn,
    createChatTurnWithChapter,
    getChatAttachment,
    steerChatTurn,
  };
}
