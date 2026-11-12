import type {
  AgentTimelineEvent,
  Job,
  OperationResponse,
  TimelinePage,
} from '../../../shared/api';
import { clone } from './common';
import type { MockRepositoryContext } from './context';
import { linkedKeys, placesOf } from './places';
import { MockApiError } from '../scenarios';

const timelinePageSize = 2;

function cursorFor(offset: number) {
  return `tl_${offset}`;
}

function eventId(jobId: string, suffix: string) {
  return `timeline-${jobId}-${suffix}`;
}

/** A question that asks the chat to draw: the chat draws nothing and sends to the workshop (image-workshop IW7.3). */
export function mockAsksToDraw(question: string) {
  return /^(нарисуй|изобрази|проиллюстрируй)/iu.test(question.trim());
}

/** The answer of the chat to a request to draw: drawings are made in the workshop (image-workshop IW7.3). */
const workshopAnswer = 'Рисунки делаются в разделе «Рисунки»: там помощник подготовит описание по книге, а нарисуете вы. '
  + '[Нарисовать в «Рисунках»](drawing:new)';

/** The chapter of the author attached to a question, as the timeline shows it (specs/2026-11-07-chat-attach-chapter.md CA4.1). */
type QuestionAttachment = NonNullable<AgentTimelineEvent['attachment']>;

/** The answer of the chat about an attached chapter: a place of it against a place of the book (CA3). */
const attachmentAnswer = (attachment: QuestionAttachment) => 'Глава ложится на канон, кроме одного места: '
  + `[прил. ¶2](attachment:${attachment.number}/2) расходится с [«Белый порт», ¶3](source:b2c12/3).`;

/** The answer of the helper of a drawing that wrote its description: the drawing is the author's button under it (D6.7). */
const describedAnswer = 'Поправил описание для художника: согласованные детали могут измениться. '
  + 'Нарисуйте кнопкой «Нарисовать по описанию» под этим ответом.';

/** `drawing.describe` of a helper's turn as the backend shows it: the first line of the tool's answer (D6.7). */
function describeEvent(job: Job, revision: number): AgentTimelineEvent {
  const turn = job.subject.type === 'research_chat_turn' ? { providerTurnId: job.subject.turnId } : {};
  return {
    id: eventId(job.id, 'describe'), cursor: cursorFor(1), kind: 'tool_result', jobId: job.id, role: 'main', logicalSection: 0, ...turn,
    tool: {
      namespace: 'drawing', name: 'describe', arguments: { revision: revision - 1 }, status: 'completed', success: true,
      summary: `Описание записано: ревизия ${revision}. Придумано тобой и не подтверждено автором: 40 %.`,
    },
  };
}

function timelineEvents(job: Job, drawingRequest = false, attachment?: QuestionAttachment, described?: number): AgentTimelineEvent[] {
  const role = 'main' as const;
  const turn = job.subject.type === 'research_chat_turn' ? { providerTurnId: job.subject.turnId } : {};
  const events: AgentTimelineEvent[] = [
    {
      id: eventId(job.id, 'reasoning'),
      cursor: cursorFor(1),
      kind: 'reasoning',
      jobId: job.id,
      role,
      logicalSection: 0,
      ...turn,
      text: 'Ищу опору в тексте.',
    },
    {
      id: eventId(job.id, 'tool'),
      cursor: cursorFor(2),
      kind: 'tool_request',
      jobId: job.id,
      role,
      logicalSection: 0,
      ...turn,
      tool: attachment
        ? { namespace: 'saga', name: 'attachment', arguments: { attachment: attachment.number }, status: 'completed', success: true, summary: `Приложение ${attachment.number}: ${attachment.words} слов` }
        : { namespace: 'saga', name: 'search', arguments: { queries: ['Белый порт'] }, status: 'completed', success: true, summary: '«Белый порт» (any): 3 абзаца в 2 главах' },
    },
    {
      id: eventId(job.id, 'assistant'),
      cursor: cursorFor(3),
      kind: 'assistant',
      jobId: job.id,
      role,
      logicalSection: 0,
      ...turn,
      phase: 'final_answer',
      text: drawingRequest ? workshopAnswer
        : attachment ? attachmentAnswer(attachment)
        : described ? describedAnswer
        : job.subject.type === 'research_chat_turn'
          ? 'Задача принята. Опора в тексте: [«Белый порт», ¶3](source:b2c12/3).'
          : 'Задача принята. Публикуем хронологию выполнения.',
    },
  ];

  if (described) events.splice(2, 0, describeEvent(job, described));
  if (job.status === 'succeeded' || job.status === 'failed' || job.status === 'canceled') {
    events.push({
      id: eventId(job.id, 'outcome'),
      cursor: cursorFor(4),
      kind: 'turn_outcome',
      jobId: job.id,
      role,
      logicalSection: 0,
      ...turn,
      // A stopped or interrupted native turn ends `interrupted`; any other refusal `failed`.
      outcome: job.status === 'succeeded' ? 'completed'
        : job.status === 'canceled' || job.error?.code === 'model.turn_interrupted' ? 'interrupted' : 'failed',
    });
  }

  return events.map((event, index) => ({ ...event, cursor: cursorFor(index + 1) }));
}

/**
 * A question with images attached comes first in the timeline, as the backend reads it from the
 * native question (illustrations R15.9), and so does a question to the writing helper; the mock shows no other questions.
 */
function withQuestion(
  job: Job, question: { text: string; images: number[]; attachment?: QuestionAttachment; button?: boolean } | undefined, events: AgentTimelineEvent[],
): AgentTimelineEvent[] {
  if (!question) return events;
  const turn = job.subject.type === 'research_chat_turn' ? { providerTurnId: job.subject.turnId } : {};
  const asked: AgentTimelineEvent = {
    id: eventId(job.id, 'question'), cursor: cursorFor(1), kind: 'user', jobId: job.id, role: 'main', logicalSection: 0, ...turn,
    text: question.text, images: [...question.images], ...(question.attachment ? { attachment: { ...question.attachment } } : {}),
    // A button of the page sent it (specs/2026-11-11-ux9-writing-b.md WB3.3).
    ...(question.button ? { button: true } : {}),
  };
  return [asked, ...events].map((event, index) => ({ ...event, cursor: cursorFor(index + 1) }));
}

/** The corrections of the turn, read by the model right after what it has done so far. */
function withSteers(job: Job, steers: readonly { steerId: string; text: string; images: number[]; firstPhoto?: number }[], events: AgentTimelineEvent[]): AgentTimelineEvent[] {
  if (!steers.length) return events;
  const turn = job.subject.type === 'research_chat_turn' ? { providerTurnId: job.subject.turnId } : {};
  const rows = steers.map((steer, index): AgentTimelineEvent => ({
    id: eventId(job.id, `steer-${index}`), cursor: cursorFor(1), kind: 'steer', jobId: job.id, role: 'main', logicalSection: 0, ...turn,
    text: steer.text, steerId: steer.steerId,
    ...(steer.images.length ? { images: [...steer.images], firstPhoto: steer.firstPhoto ?? 1 } : {}),
  }));
  return [...events, ...rows].map((event, index) => ({ ...event, cursor: cursorFor(index + 1) }));
}

/** The arguments of a tool that name a chapter by its key, as the backend reads them. */
const keyArguments = ['chapter', 'from', 'to', 'at', 'from_chapter'] as const;

/**
 * Beside the keys an event shows stand the places of their chapters, as the backend writes them
 * (specs/2026-11-10-ux1-names.md N2.1): the arguments of its tool and the source links of its text.
 */
function withPlaces(context: MockRepositoryContext, projectId: string, event: AgentTimelineEvent): AgentTimelineEvent {
  const args = event.tool?.arguments ?? {};
  const keys = keyArguments.flatMap((name) => typeof args[name] === 'string' ? [args[name]] : []);
  const text = event.kind === 'user' || event.kind === 'steer' || event.kind === 'assistant' ? linkedKeys(event.text) : [];
  const places = placesOf(context, projectId, [...keys, ...text]);
  return places ? { ...event, places } : event;
}

function parseCursor(cursor: string | null | undefined) {
  if (cursor === undefined || cursor === null || cursor === '') return 0;
  const match = /^tl_(\d+)$/u.exec(cursor);
  if (!match) {
    throw new MockApiError(400, 'Timeline cursor is invalid.', 'getAgentTimeline', 'invalid_cursor');
  }
  const offset = Number(match[1]);
  if (offset === 0) {
    throw new MockApiError(400, 'Timeline cursor is invalid.', 'getAgentTimeline', 'invalid_cursor');
  }
  return offset;
}

export function createAgentTimelineOperations(context: MockRepositoryContext) {
  async function getAgentTimeline(
    projectId: string,
    jobId: string,
    options: { cursor?: string | null } = {},
  ): Promise<OperationResponse<'getAgentTimeline', '200'>> {
    context.applyOperation('getAgentTimeline');
    const job = context.jobFor(jobId, 'getAgentTimeline');
    if (job.subject.projectId !== projectId) {
      throw new MockApiError(404, 'Timeline not found.', 'getAgentTimeline');
    }
    const question = job.subject.type === 'research_chat_turn' ? context.state.gallery.turnQuestions[job.subject.turnId] : undefined;
    // The chat draws nothing: a question that asks to draw is answered with a link to the drawing workshop.
    const drawingRequest = job.subject.type === 'research_chat_turn' && Boolean(context.state.gallery.drawingRequests[job.subject.turnId]);
    const steers = job.subject.type === 'research_chat_turn' ? context.state.gallery.turnSteers[job.subject.turnId] ?? [] : [];
    const described = job.subject.type === 'research_chat_turn' ? context.state.gallery.turnDescribes[job.subject.turnId] : undefined;
    const events = withSteers(job, steers, withQuestion(job, question, timelineEvents(job, drawingRequest, question?.attachment, described)))
      .map((event) => withPlaces(context, projectId, event));
    const offset = parseCursor(options.cursor);
    const pageEvents = events.slice(offset, offset + timelinePageSize);
    const nextOffset = offset + pageEvents.length;
    const page: TimelinePage = {
      events: clone(pageEvents),
      eof: nextOffset >= events.length,
      nextCursor: nextOffset < events.length ? cursorFor(nextOffset) : null,
      needsReconcile: false,
    };
    return page as unknown as OperationResponse<'getAgentTimeline', '200'>;
  }

  return { getAgentTimeline };
}
