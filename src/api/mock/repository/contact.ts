import type {
  FeedbackImageUploadInput,
  FeedbackImageUploadOptions,
  OperationId,
  OperationRequest,
  OperationResponse,
  Schema,
} from '../../../shared/api';
import { MockApiError } from '../scenarios';
import { clone } from './common';
import type { MockRepositoryContext } from './context';
import type { MockFeedbackMessage, MockFeedbackThread } from './state';

type FeedbackThread = Schema<'FeedbackThread'>;
type AdminFeedbackThread = Schema<'AdminFeedbackThread'>;
type FeedbackMessage = Schema<'FeedbackMessage'>;
type BlockInput = Schema<'FeedbackBlockInput'>;

const maxTextRunes = 4000;
const maxImages = 10;
const maxImageBytes = 5 * 1024 * 1024;
const draftKeyPattern = /^[0-9a-f]{32,64}$/;
const emailPattern = /^[^\s@<>"]+@[^\s@<>"]+$/;

function randomId() {
  return globalThis.crypto.randomUUID();
}

function invalid(operationId: OperationId, detail = 'validation failed') {
  return new MockApiError(400, detail, operationId, 'request.validation');
}

/** The contacts and the feedback of «Связаться с нами» as the backend keeps them (specs/2026-11-07-contacts.md). */
export function createContactOperations(context: MockRepositoryContext) {
  /** The author of his own session: none for a guest, 403 for a session opened on his behalf. */
  function ownUser(operationId: OperationId, required: boolean) {
    context.applyOperation(operationId);
    const session = context.state.session;
    if (!session) {
      if (required) throw new MockApiError(401, 'Сессия истекла. Войдите снова.', operationId, 'auth.session_required');
      return null;
    }
    if (session.user.impersonation) throw new MockApiError(403, 'Forbidden', operationId, 'forbidden');
    return session.user;
  }

  function adminOperation(operationId: OperationId) {
    context.applyOperation(operationId);
    if (!context.state.session) throw new MockApiError(401, 'Сессия истекла. Войдите снова.', operationId, 'auth.session_required');
    if (context.state.session.user.impersonation) throw new MockApiError(403, 'Forbidden', operationId, 'admin.forbidden');
  }

  function normalizeTelegram(value: string | null | undefined) {
    const raw = (value ?? '').trim();
    if (!raw) return null;
    const name = raw.replace(/^https?:\/\//i, '').replace(/^(?:www\.)?(?:t|telegram)\.me\//i, '').replace(/^@/, '').replace(/\/$/, '');
    if (!/^[A-Za-z][A-Za-z0-9_]{4,31}$/.test(name)) throw invalid('setAdminContacts');
    return `https://t.me/${name}`;
  }

  async function getContacts(): Promise<OperationResponse<'getContacts', '200'>> {
    context.applyOperation('getContacts');
    const { telegram, email, note } = context.state.contacts;
    return { telegram, email, note };
  }

  async function getAdminContacts(): Promise<OperationResponse<'getAdminContacts', '200'>> {
    adminOperation('getAdminContacts');
    return clone(context.state.contacts);
  }

  async function setAdminContacts(body: OperationRequest<'setAdminContacts'>): Promise<OperationResponse<'setAdminContacts', '200'>> {
    adminOperation('setAdminContacts');
    const telegram = normalizeTelegram(body.telegram);
    const email = (body.email ?? '').trim() || null;
    const note = (body.note ?? '').trim() || null;
    if (email && !emailPattern.test(email)) throw invalid('setAdminContacts');
    if (note && (note.length > 120 || /[\r\n]/.test(note))) throw invalid('setAdminContacts');
    const current = context.state.contacts;
    if (current.telegram !== telegram || current.email !== email || current.note !== note) {
      context.state.contacts = { telegram, email, note, updatedAt: new Date().toISOString() };
    }
    return clone(context.state.contacts);
  }

  async function uploadFeedbackImage(
    input: FeedbackImageUploadInput, options: FeedbackImageUploadOptions = {},
  ): Promise<OperationResponse<'uploadFeedbackImage', '201'>> {
    const user = ownUser('uploadFeedbackImage', false);
    if (!draftKeyPattern.test(input.draftKey)) throw invalid('uploadFeedbackImage');
    if (input.file.size > maxImageBytes) {
      throw new MockApiError(413, 'The image is too large.', 'uploadFeedbackImage', 'feedback.image_too_large');
    }
    const type = input.file.type || 'image/png';
    if (!['image/png', 'image/jpeg', 'image/webp'].includes(type)) {
      throw new MockApiError(415, 'The file is not an image.', 'uploadFeedbackImage', 'feedback.image_unsupported');
    }
    const content = await fileBytes(input.file);
    options.onUploadProgress?.({ loadedBytes: content.byteLength, totalBytes: content.byteLength });
    const image = { id: randomId(), userId: user?.id ?? null, draftKey: input.draftKey, messageId: null, width: 640, height: 400, content, mimeType: type };
    context.state.feedbackImages.push(image);
    return { id: image.id, width: image.width, height: image.height, preview: `data:image/jpeg;base64,${base64(content)}` };
  }

  /** The blocks of a message checked as the backend does, with the images of its draft taken by the message. */
  function messageOf(
    operationId: OperationId, from: MockFeedbackMessage['from'], draftKey: string, blocks: BlockInput[],
    uploaderId: string | null, context_?: Schema<'FeedbackContextInput'>,
  ): MockFeedbackMessage {
    if (!draftKeyPattern.test(draftKey) || blocks.length === 0) throw invalid(operationId);
    const kept: MockFeedbackMessage['blocks'] = [];
    let runes = 0;
    const images: string[] = [];
    for (const block of blocks) {
      if (block.type === 'image') {
        const id = block.image?.id;
        if (!id || images.includes(id)) throw invalid(operationId);
        images.push(id);
        kept.push({ image: id });
      } else {
        const text = (block.text ?? '').replace(/\r\n?/g, '\n').split('\n').map((line) => line.trimEnd()).join('\n').replace(/^\n+|\n+$/g, '');
        if (!text) continue;
        runes += [...text].length;
        kept.push({ text });
      }
    }
    if (kept.length === 0 || runes > maxTextRunes || images.length > maxImages) throw invalid(operationId);
    const id = randomId();
    for (const imageId of images) {
      const image = context.state.feedbackImages.find((candidate) => candidate.id === imageId);
      if (!image || image.draftKey !== draftKey || image.messageId !== null || image.userId !== uploaderId) throw invalid(operationId);
    }
    for (const imageId of images) {
      context.state.feedbackImages.find((candidate) => candidate.id === imageId)!.messageId = id;
    }
    const message: MockFeedbackMessage = { id, number: 0, from, createdAt: new Date().toISOString(), blocks: kept, draftKey };
    if (context_) message.context = { ...context_, page: context_.page.split(/[?#]/)[0] || '/', build: 'mock' };
    return message;
  }

  function sentDraft(draftKey: string) {
    for (const thread of context.state.feedbackThreads) {
      if (thread.messages.some((message) => message.draftKey === draftKey)) return thread;
    }
    return undefined;
  }

  function subjectOf(topic: MockFeedbackThread['topic'], blocks: MockFeedbackMessage['blocks']) {
    const labels = { bug: 'Ошибка', idea: 'Предложение', question: 'Вопрос', other: 'Другое' };
    for (const block of blocks) {
      if (!('text' in block)) continue;
      const line = block.text.split('\n').map((value) => value.replace(/^- /, '').replace(/\*\*|_/g, '').trim()).find(Boolean);
      if (line) return line.length > 60 ? `${line.slice(0, 59).trim()}…` : line;
    }
    return labels[topic];
  }

  function blocksOf(thread: MockFeedbackThread, message: MockFeedbackMessage): Schema<'FeedbackBlock'>[] {
    return message.blocks.map((block) => {
      if ('text' in block) return { type: 'text', text: block.text };
      const image = context.state.feedbackImages.find((candidate) => candidate.id === block.image);
      const url = `/api/v1/feedback/threads/${thread.id}/images/${block.image}`;
      return { type: 'image', image: { id: block.image, width: image?.width ?? 1, height: image?.height ?? 1, url: `${url}?size=full`, thumbUrl: `${url}?size=screen` } };
    });
  }

  function messageView(thread: MockFeedbackThread, message: MockFeedbackMessage, withContext: boolean): FeedbackMessage {
    const view: FeedbackMessage = { id: message.id, number: message.number, from: message.from, createdAt: message.createdAt, blocks: blocksOf(thread, message) };
    if (withContext && message.context) view.context = clone(message.context);
    return view;
  }

  function unread(thread: MockFeedbackThread) {
    return thread.messages.some((message) => message.from === 'support' && (!thread.authorReadAt || message.createdAt > thread.authorReadAt));
  }

  function threadView(thread: MockFeedbackThread, messages = thread.messages): FeedbackThread {
    return {
      id: thread.id, topic: thread.topic, subject: thread.subject, status: thread.status, email: thread.email,
      createdAt: thread.createdAt, updatedAt: thread.updatedAt, unread: unread(thread), lastFrom: thread.messages.at(-1)!.from,
      messages: messages.map((message) => messageView(thread, message, false)),
    };
  }

  function adminView(thread: MockFeedbackThread, whole: boolean): AdminFeedbackThread {
    const view: AdminFeedbackThread = {
      ...threadView(thread, whole ? thread.messages : []),
      messages: whole ? thread.messages.map((message) => messageView(thread, message, true)) : [],
      author: thread.userId ? { id: thread.userId, name: thread.authorName } : null,
      messageCount: thread.messages.length,
    };
    if (!whole) view.first = messageView(thread, thread.messages[0]!, true);
    return view;
  }

  function ownThread(operationId: OperationId, userId: string, threadId: string) {
    const thread = context.state.feedbackThreads.find((candidate) => candidate.id === threadId && candidate.userId === userId);
    if (!thread) throw new MockApiError(404, 'Resource not found', operationId, 'not_found');
    return thread;
  }

  function anyThread(operationId: OperationId, threadId: string) {
    const thread = context.state.feedbackThreads.find((candidate) => candidate.id === threadId);
    if (!thread) throw new MockApiError(404, 'Resource not found', operationId, 'not_found');
    return thread;
  }

  function add(thread: MockFeedbackThread, message: MockFeedbackMessage) {
    message.number = thread.messages.length + 1;
    thread.messages.push(message);
    thread.updatedAt = message.createdAt;
    thread.status = message.from === 'support' ? 'answered' : 'new';
  }

  async function createFeedbackThread(body: OperationRequest<'createFeedbackThread'>): Promise<OperationResponse<'createFeedbackThread', '201'>> {
    const user = ownUser('createFeedbackThread', false);
    if (body.website || !['bug', 'idea', 'question', 'other'].includes(body.topic)) throw invalid('createFeedbackThread');
    const email = (body.email ?? '').trim() || user?.email || '';
    if (!emailPattern.test(email)) throw invalid('createFeedbackThread');
    const replayed = sentDraft(body.draftKey);
    if (replayed) return threadView(replayed);
    const message = messageOf('createFeedbackThread', 'author', body.draftKey, body.blocks, user?.id ?? null, body.context);
    const thread: MockFeedbackThread = {
      id: randomId(), userId: user?.id ?? null, authorName: user?.displayName ?? '', email, topic: body.topic,
      subject: subjectOf(body.topic, message.blocks), status: 'new', createdAt: message.createdAt, updatedAt: message.createdAt,
      authorReadAt: null, messages: [],
    };
    add(thread, message);
    context.state.feedbackThreads.push(thread);
    return threadView(thread);
  }

  async function listFeedbackThreads(): Promise<OperationResponse<'listFeedbackThreads', '200'>> {
    const user = ownUser('listFeedbackThreads', true)!;
    const own = context.state.feedbackThreads.filter((thread) => thread.userId === user.id)
      .sort((left, right) => Number(unread(right)) - Number(unread(left)) || right.updatedAt.localeCompare(left.updatedAt));
    return { threads: own.map((thread) => threadView(thread, thread.messages.slice(0, 1))), unread: own.filter(unread).length };
  }

  async function getFeedbackThread(threadId: string): Promise<OperationResponse<'getFeedbackThread', '200'>> {
    const user = ownUser('getFeedbackThread', true)!;
    const thread = ownThread('getFeedbackThread', user.id, threadId);
    thread.authorReadAt = new Date().toISOString();
    return threadView(thread);
  }

  async function addFeedbackMessage(threadId: string, body: OperationRequest<'addFeedbackMessage'>): Promise<OperationResponse<'addFeedbackMessage', '201'>> {
    const user = ownUser('addFeedbackMessage', true)!;
    const thread = ownThread('addFeedbackMessage', user.id, threadId);
    if (sentDraft(body.draftKey)) return threadView(thread);
    add(thread, messageOf('addFeedbackMessage', 'author', body.draftKey, body.blocks, user.id, body.context));
    return threadView(thread);
  }

  /** The bytes of an image of a sent message, for the author of its thread or the administrator. */
  async function getFeedbackImage(threadId: string, imageId: string): Promise<{ content: Uint8Array<ArrayBuffer>; mimeType: string }> {
    context.applyOperation('getFeedbackImage');
    const session = context.state.session;
    if (!session) throw new MockApiError(401, 'Сессия истекла. Войдите снова.', 'getFeedbackImage', 'auth.session_required');
    const thread = anyThread('getFeedbackImage', threadId);
    const image = context.state.feedbackImages.find((candidate) => candidate.id === imageId && thread.messages.some((message) => message.id === candidate.messageId));
    if (!image) throw new MockApiError(404, 'Resource not found', 'getFeedbackImage', 'not_found');
    return { content: image.content, mimeType: image.mimeType };
  }

  async function listAdminFeedback(): Promise<OperationResponse<'listAdminFeedback', '200'>> {
    adminOperation('listAdminFeedback');
    const threads = [...context.state.feedbackThreads]
      .sort((left, right) => Number(right.status === 'new') - Number(left.status === 'new') || right.updatedAt.localeCompare(left.updatedAt));
    return { threads: threads.map((thread) => adminView(thread, false)), new: threads.filter((thread) => thread.status === 'new').length };
  }

  async function getAdminFeedbackThread(threadId: string): Promise<OperationResponse<'getAdminFeedbackThread', '200'>> {
    adminOperation('getAdminFeedbackThread');
    return adminView(anyThread('getAdminFeedbackThread', threadId), true);
  }

  async function replyAdminFeedback(threadId: string, body: OperationRequest<'replyAdminFeedback'>): Promise<OperationResponse<'replyAdminFeedback', '201'>> {
    adminOperation('replyAdminFeedback');
    const thread = anyThread('replyAdminFeedback', threadId);
    if (!sentDraft(body.draftKey)) {
      add(thread, messageOf('replyAdminFeedback', 'support', body.draftKey, body.blocks, context.state.session!.user.id));
    }
    return adminView(thread, true);
  }

  async function setAdminFeedbackStatus(threadId: string, body: OperationRequest<'setAdminFeedbackStatus'>): Promise<OperationResponse<'setAdminFeedbackStatus', '200'>> {
    adminOperation('setAdminFeedbackStatus');
    if (body.status !== 'new' && body.status !== 'answered') throw invalid('setAdminFeedbackStatus');
    const thread = anyThread('setAdminFeedbackStatus', threadId);
    thread.status = body.status;
    return adminView(thread, true);
  }

  return {
    getContacts, getAdminContacts, setAdminContacts,
    uploadFeedbackImage, createFeedbackThread, listFeedbackThreads, getFeedbackThread, addFeedbackMessage, getFeedbackImage,
    listAdminFeedback, getAdminFeedbackThread, replyAdminFeedback, setAdminFeedbackStatus,
  };
}

/** The bytes of a file; a test environment whose File cannot give them keeps an empty picture. */
async function fileBytes(file: File): Promise<Uint8Array<ArrayBuffer>> {
  if (typeof file.arrayBuffer === 'function') return new Uint8Array(await file.arrayBuffer());
  if (typeof file.text === 'function') return new TextEncoder().encode(await file.text()) as Uint8Array<ArrayBuffer>;
  return new Uint8Array([137, 80, 78, 71]);
}

function base64(content: Uint8Array) {
  let binary = '';
  for (const byte of content) binary += String.fromCharCode(byte);
  return globalThis.btoa(binary);
}
