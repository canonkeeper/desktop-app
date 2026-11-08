import type {
  OperationRequest,
  OperationResponse,
  RequiredIdempotencyOptions,
  Schema,
} from '../../../shared/api';
import { MockApiError } from '../scenarios';
import type { createChatOperations } from './chat';
import type { MockRepositoryContext } from './context';
import type { MockDrawing, MockSagaImage } from './gallery-state';
import { createGalleryView, editorRoles } from './gallery-view';
import { createIllustrationAccess } from './illustration-access';

type Drawing = Schema<'Drawing'>;
type DrawingBrief = Schema<'DrawingBrief'>;
type DrawingOperationId = Parameters<MockRepositoryContext['projectFor']>[1];

// helperKey keys the session of the helper the first message opens, apart from the key of the message.
function helperKey(key: string) {
  return `${key}:helper`;
}

/** The words of a drawing button, as the backend writes them from the chat question (IW1.1). */
function buttonIdea(request: Schema<'DrawingButtonRequest'>) {
  const subject = request.subject?.trim();
  const chapter = request.chapter?.trim();
  switch (request.kind) {
    case 'hero':
      return `Нарисуй главного героя саги${subject ? ` «${subject}»` : ''} — портрет по пояс${chapter ? ` на гл. ${chapter}` : ''}, облик по тексту.`;
    case 'portrait':
      return subject ? `Нарисуй портрет «${subject}»${chapter ? ` на гл. ${chapter}` : ''}: облик по фактам этой главы.` : '';
    case 'scene':
      return chapter ? `Нарисуй сцену гл. ${chapter}${subject ? ` с «${subject}»` : ''}: кто в кадре, место, свет и действие — по тексту.` : '';
    case 'chapter':
      return chapter ? `Проиллюстрируй гл. ${chapter}: выбери ключевой момент главы и нарисуй его по тексту.` : '';
    default:
      return '';
  }
}

/** The part of the description the helper made up and the author has not confirmed, by characters (IW2.3). */
function inventedShare(brief: DrawingBrief) {
  const lengths = brief.segments.map((segment) => [segment.text.trim().length, segment.origin === 'helper' && !segment.approved] as const);
  const total = lengths.reduce((sum, [length]) => sum + length, 0);
  return total ? lengths.reduce((sum, [length, invented]) => sum + (invented ? length : 0), 0) / total : 0;
}

/**
 * The drawings of the mock workshop: listed, created from the author's words or a button, read with their
 * current version and the variants waiting, changed, drawn by their brief, deleted whole; the choice of an
 * edit candidate. The versions develop on later reads as the drawings of the gallery do (gallery-view.ts).
 */
export function createDrawingOperations(context: MockRepositoryContext, chat: ReturnType<typeof createChatOperations>) {
  const { gallery, advance, view, imageFor } = createGalleryView(context);
  const access = createIllustrationAccess(context);
  const drawings = () => gallery().drawings;

  function drawingFor(operationId: DrawingOperationId, projectId: string, drawingId: string) {
    const drawing = drawings().find((item) => item.projectId === projectId && item.id === drawingId);
    if (!drawing) throw new MockApiError(404, 'Drawing not found.', operationId, 'not_found');
    return drawing;
  }

  function versionsOf(drawing: MockDrawing) {
    return gallery().images.filter((image) => image.projectId === drawing.projectId && drawing.branch !== null && image.branch === drawing.branch);
  }

  /** The variants of a drawing waiting for the choice: a group with an image drawn or ready (I5). */
  function waitingOf(drawing: MockDrawing) {
    const versions = versionsOf(drawing);
    const groups = new Set(versions.filter((image) => image.variantGroup && (image.status === 'drawing' || image.status === 'ready')).map((image) => image.variantGroup));
    return versions.filter((image) => image.variantGroup && groups.has(image.variantGroup)).sort((left, right) => left.number - right.number);
  }

  function stageOf(drawing: MockDrawing, waiting: readonly MockSagaImage[]): Schema<'DrawingStage'> {
    if (waiting.length) return 'variants';
    if (drawing.current) return 'refine';
    return drawing.brief.segments.length ? 'describe' : 'idea';
  }

  /** What the tile says is happening (specs/2026-11-05-drawing-one-screen.md D1.5), as `Drawing.Activity` of the backend. */
  function activityOf(drawing: MockDrawing, versions: readonly MockSagaImage[], waiting: readonly MockSagaImage[]): Schema<'DrawingSummary'>['activity'] {
    if (drawing.drawAfterReads || versions.some((image) => image.status === 'drawing' || image.layers.some((layer) => layer.status === 'drawing'))) return 'working';
    if (waiting.length) return 'choosing';
    const newest = versions.reduce<MockSagaImage | undefined>((latest, image) => (!latest || image.number > latest.number ? image : latest), undefined);
    return newest?.status === 'failed' || newest?.status === 'refused' ? 'failed' : null;
  }

  function canDelete(drawing: MockDrawing) {
    const role = context.actorRole(drawing.projectId);
    return role === 'owner' || drawing.createdBy === context.currentPrincipalId() && editorRoles.some((item) => item === role);
  }

  function summaryOf(drawing: MockDrawing): Schema<'DrawingSummary'> {
    const waiting = waitingOf(drawing);
    const versions = versionsOf(drawing);
    const current = drawing.current ? versions.find((image) => image.number === drawing.current) : undefined;
    const cover = current ?? waiting.find((image) => image.status === 'ready') ?? waiting[0];
    return {
      id: drawing.id, title: drawing.brief.title, idea: drawing.idea, stage: stageOf(drawing, waiting), activity: activityOf(drawing, versions, waiting),
      current: current ? { ...view(current), drawingId: drawing.id } : null, cover: cover ? { ...view(cover), drawingId: drawing.id } : null, variants: waiting.length,
      canEdit: editorRoles.some((role) => role === context.actorRole(drawing.projectId)), canDelete: canDelete(drawing),
      createdAt: drawing.createdAt, updatedAt: drawing.updatedAt,
    };
  }

  function drawingView(drawing: MockDrawing): Drawing {
    const summary = summaryOf(drawing);
    const versions = versionsOf(drawing);
    const waiting = waitingOf(drawing);
    return {
      ...summary, brief: structuredClone(drawing.brief), revision: drawing.revision, inventedShare: inventedShare(drawing.brief),
      variantImages: waiting.map((image) => ({ ...view(image), drawingId: drawing.id })),
      candidates: (summary.current?.layers ?? []).filter((layer) => layer.candidate).map((layer) => layer.id),
      versions: versions.filter((image) => !image.variantGroup).map((image) => image.number).sort((left, right) => right - left),
      branch: drawing.branch, chatId: drawing.chatId,
    };
  }

  function touch(drawing: MockDrawing) {
    drawing.updatedAt = new Date().toISOString();
  }

  async function listDrawings(
    projectId: string, options: { cursor?: string; limit?: number; signal?: AbortSignal } = {},
  ): Promise<OperationResponse<'listDrawings', '200'>> {
    context.applyOperation('listDrawings');
    context.projectFor(projectId, 'listDrawings');
    const listed = drawings()
      .filter((drawing) => drawing.projectId === projectId)
      .sort((left, right) => right.updatedAt.localeCompare(left.updatedAt));
    return { data: listed.slice(0, options.limit ?? 30).map(summaryOf), meta: { hasMore: listed.length > (options.limit ?? 30), nextCursor: null } };
  }

  async function createDrawing(
    projectId: string, body: OperationRequest<'createDrawing'>, options: RequiredIdempotencyOptions,
  ): Promise<OperationResponse<'createDrawing', '201'>> {
    context.applyOperation('createDrawing');
    context.projectFor(projectId, 'createDrawing', [...editorRoles]);
    const idea = (body.request ? buttonIdea(body.request) : body.idea ?? '').trim();
    if (!idea || idea.length > 4000 || Boolean(body.request) === (body.idea !== undefined)) {
      throw new MockApiError(400, 'A drawing needs the author\'s idea or a button\'s request.', 'createDrawing', 'validation_failed');
    }
    return context.idempotentCommand('createDrawing', projectId, options, body, () => {
      const kind = body.request ? (body.request.kind === 'hero' || body.request.kind === 'portrait' ? 'character' : 'scene') : body.kind ?? 'scene';
      const chapter = body.request?.chapter ?? body.chapter ?? null;
      const subjects = body.request?.subject ? [body.request.subject] : body.subjects ?? [];
      const at = new Date().toISOString();
      const drawing: MockDrawing = {
        id: `00000000-0000-4000-a000-${String(gallery().nextLayer++).padStart(12, '0')}`, projectId, idea, revision: 1,
        brief: {
          title: '', kind, aspect: body.aspect ?? null, chapter, subjects,
          segments: body.selfDescribed ? [{ text: idea, origin: 'author' }] : [], facts: [], staging: [],
          references: body.references ? [...body.references] : null, images: [...(body.images ?? [])],
        },
        branch: null, current: null, chatId: null, createdBy: context.currentPrincipalId(), createdAt: at, updatedAt: at,
      };
      drawings().push(drawing);
      return drawingView(drawing);
    });
  }

  async function getDrawing(projectId: string, drawingId: string): Promise<OperationResponse<'getDrawing', '200'>> {
    context.applyOperation('getDrawing');
    context.projectFor(projectId, 'getDrawing');
    const drawing = drawingFor('getDrawing', projectId, drawingId);
    if (drawing.drawAfterReads && --drawing.drawAfterReads === 0) {
      const stagings = drawing.brief.staging.length;
      if (!waitingOf(drawing).length && !drawing.current) startDrawing(projectId, drawing, stagings >= 2 ? stagings : 1, 'submitDrawingTurn');
    }
    // A read of the drawing moves its versions one step, as a read of a drawing of the gallery does.
    for (const image of versionsOf(drawing).filter((item) => item.number === drawing.current || item.variantGroup)) advance(image);
    return drawingView(drawing);
  }

  async function updateDrawing(projectId: string, drawingId: string, body: OperationRequest<'updateDrawing'>): Promise<OperationResponse<'updateDrawing', '200'>> {
    context.applyOperation('updateDrawing');
    context.projectFor(projectId, 'updateDrawing', [...editorRoles]);
    const drawing = drawingFor('updateDrawing', projectId, drawingId);
    if (body.brief) {
      if (body.expectedRevision !== drawing.revision) {
        throw new MockApiError(409, 'The description changed since it was read.', 'updateDrawing', 'drawing.brief_changed');
      }
      drawing.brief = structuredClone(body.brief);
      drawing.revision += 1;
    } else if (body.idea !== undefined) {
      drawing.idea = body.idea.trim();
    } else if (body.current !== undefined) {
      if (waitingOf(drawing).length) throw new MockApiError(409, 'The variant is not chosen yet.', 'updateDrawing', 'illustrations.variant_pending');
      const version = versionsOf(drawing).find((image) => image.number === body.current && image.status === 'ready' && !image.variantGroup);
      if (!version) throw new MockApiError(409, 'No ready version of the drawing.', 'updateDrawing', 'conflict');
      drawing.current = version.number;
    } else {
      throw new MockApiError(400, 'Name one change.', 'updateDrawing', 'validation_failed');
    }
    touch(drawing);
    return drawingView(drawing);
  }

  async function deleteDrawing(projectId: string, drawingId: string): Promise<void> {
    context.applyOperation('deleteDrawing');
    context.projectFor(projectId, 'deleteDrawing');
    const drawing = drawingFor('deleteDrawing', projectId, drawingId);
    if (!canDelete(drawing)) throw new MockApiError(403, 'Only the author or the owner deletes a drawing.', 'deleteDrawing', 'forbidden');
    const versions = new Set(versionsOf(drawing));
    gallery().images = gallery().images.filter((image) => !versions.has(image));
    gallery().drawings = drawings().filter((item) => item !== drawing);
  }

  /** One version, current when the drawing has none, or a group by the stagings (IW1.4), paid once. */
  function startDrawing(projectId: string, drawing: MockDrawing, variants: number, operationId: 'drawDrawing' | 'submitDrawingTurn') {
    access.reserveDrawings(operationId, 1);
    const { brief } = drawing;
    const state = gallery();
    const variantGroup = variants > 1 ? `00000000-0000-4000-8000-${String(state.nextLayer++).padStart(12, '0')}` : undefined;
    const stagings = variantGroup ? brief.staging : [undefined];
    for (const [index, staging] of stagings.entries()) {
      const number = (state.lastNumber[projectId] ?? 0) + 1;
      state.lastNumber[projectId] = number;
      drawing.branch ??= number;
      state.images.push({
        projectId, createdBy: context.currentPrincipalId(), number, branch: drawing.branch, status: 'drawing', phase: 'queued', partialFrame: 0,
        kind: brief.kind, aspect: brief.aspect ?? (brief.kind === 'character' ? 'portrait' : 'landscape'), title: brief.title || null,
        prompt: brief.segments.map((segment) => segment.text).join(' '), revisedPrompt: null, style: state.styles[projectId] ?? null,
        chapter: brief.chapter, subjects: brief.subjects.map((name) => ({ key: name.toLowerCase(), name })), baseImage: null,
        referenceImages: [...(brief.references ?? [])], chatId: null, width: null, height: null, errorCode: null, errorMessage: null, retryAt: null,
        createdAt: new Date().toISOString(), readyAt: null, sources: brief.segments.flatMap((segment) => (segment.source ? [segment.source] : [])),
        sketch: false, renderRevision: 0, layers: [], facts: brief.facts.map((fact) => fact.text), charge: index === 0 ? 'quota' : null,
        ...(variantGroup ? { variantGroup, variantPrompt: staging!, ...(index > 0 ? { variantPaid: true } : {}) } : {}),
      });
      if (!variantGroup && drawing.current === null) drawing.current = number;
    }
  }

  /** «Нарисовать N вариантов» (IW1.4). */
  async function drawDrawing(
    projectId: string, drawingId: string, body: OperationRequest<'drawDrawing'>, options: RequiredIdempotencyOptions,
  ): Promise<OperationResponse<'drawDrawing', '201'>> {
    context.applyOperation('drawDrawing');
    context.projectFor(projectId, 'drawDrawing', [...editorRoles]);
    const drawing = drawingFor('drawDrawing', projectId, drawingId);
    if (body.expectedRevision !== drawing.revision) throw new MockApiError(409, 'The description changed since it was read.', 'drawDrawing', 'drawing.brief_changed');
    const { brief } = drawing;
    if (!brief.segments.length || body.variants > 1 && brief.staging.length !== body.variants) {
      throw new MockApiError(400, 'Each variant needs its own staging.', 'drawDrawing', 'validation_failed');
    }
    if (waitingOf(drawing).length) throw new MockApiError(409, 'The variant is not chosen yet.', 'drawDrawing', 'illustrations.variant_pending');
    return context.idempotentCommand('drawDrawing', projectId, options, { drawingId, ...body }, () => {
      startDrawing(projectId, drawing, body.variants, 'drawDrawing');
      touch(drawing);
      return drawingView(drawing);
    });
  }

  /** The click on a ready edit candidate (IW3.5): it becomes a visible layer, the others of its edit go. */
  async function chooseProjectImageLayerVariant(
    projectId: string, number: number, layerId: string, options: RequiredIdempotencyOptions,
  ): Promise<OperationResponse<'chooseProjectImageLayerVariant', '200'>> {
    context.applyOperation('chooseProjectImageLayerVariant');
    context.projectFor(projectId, 'chooseProjectImageLayerVariant', [...editorRoles]);
    return context.idempotentCommand('chooseProjectImageLayerVariant', projectId, options, { number, layerId }, () => {
      const image = imageFor('chooseProjectImageLayerVariant', projectId, number);
      const layer = image.layers.find((item) => item.id === layerId);
      if (!layer) throw new MockApiError(404, 'Layer not found.', 'chooseProjectImageLayerVariant', 'not_found');
      if (!layer.candidate) return view(image);
      if (layer.status !== 'ready') throw new MockApiError(409, 'The candidate is not ready.', 'chooseProjectImageLayerVariant', 'conflict');
      image.layers = image.layers.filter((item) => item === layer || !item.candidate);
      layer.visible = true;
      delete layer.candidate;
      image.renderRevision += 1;
      return view(image);
    });
  }

  /**
   * A message to the helper of a drawing (IW4.2): the first opens its session — a chat session the chat list does
   * not show; the turn goes the way the mock chat's turns go. «Нарисовать» starts the drawing when the turn ends (D3.7):
   * here, after two reads of the page, which is as long as the mock turn takes.
   */
  async function submitDrawingTurn(
    projectId: string, drawingId: string, body: OperationRequest<'submitDrawingTurn'>, options: RequiredIdempotencyOptions,
  ): Promise<OperationResponse<'submitDrawingTurn', '202'>> {
    context.applyOperation('submitDrawingTurn');
    context.projectFor(projectId, 'submitDrawingTurn', [...editorRoles]);
    access.requireDrawingRight('submitDrawingTurn');
    const drawing = drawingFor('submitDrawingTurn', projectId, drawingId);
    if (!body.body.trim()) throw new MockApiError(400, 'Write what to change.', 'submitDrawingTurn', 'validation_failed');
    if (!drawing.chatId) {
      const session = await chat.createChatSession(projectId, {
        sourceView: 'published', scope: 'project', title: `Рисование: ${drawing.brief.title || drawing.idea}`.slice(0, 180),
      }, { idempotencyKey: helperKey(options.idempotencyKey) });
      drawing.chatId = session.id;
    }
    const started = await chat.createChatTurn(drawing.chatId, { body: body.body, ...(body.images?.length ? { images: body.images } : {}) }, options);
    const subject = started.job.subject;
    if (subject.type !== 'research_chat_turn') throw new MockApiError(500, 'The turn has no subject.', 'submitDrawingTurn');
    // The timeline shows the author's message as the question of the turn, as the backend reads it from the thread.
    context.state.gallery.turnQuestions[subject.turnId] ??= { text: body.body, images: [...(body.images ?? [])] };
    // The mock helper writes the description on its first message, as drawing.describe does (IW1.3, IW2.1): the idea
    // as the author's piece, a piece it made up and three stagings for variants. Asked to draw the whole drawing again, it
    // rewrites its own pieces and keeps the author's; the drawing is the author's button (D6.7).
    if (!drawing.brief.segments.length) {
      drawing.brief = {
        ...drawing.brief, title: drawing.brief.title || drawing.idea.slice(0, 80),
        segments: [{ text: drawing.idea, origin: 'author' }, { text: 'Закатный свет сбоку, длинная тень по камню.', origin: 'helper' }],
        staging: ['общий план с моря', 'средний план сбоку', 'низкий ракурс от подножия'],
      };
      drawing.revision += 1;
      context.state.gallery.turnDescribes[subject.turnId] = drawing.revision;
    } else if (/перерисуй/iu.test(body.body)) {
      const own = drawing.brief.segments.filter((segment) => segment.origin !== 'helper');
      drawing.brief = { ...drawing.brief, segments: [...own, { text: 'Тот же причал целиком заново: рассветный свет, общий план с воды.', origin: 'helper' }] };
      drawing.revision += 1;
      context.state.gallery.turnDescribes[subject.turnId] = drawing.revision;
    }
    if (body.draw) drawing.drawAfterReads = 2;
    touch(drawing);
    return { chatId: drawing.chatId, turnId: subject.turnId, jobId: started.jobId };
  }

  return { listDrawings, createDrawing, getDrawing, updateDrawing, deleteDrawing, drawDrawing, chooseProjectImageLayerVariant, submitDrawingTurn };
}
