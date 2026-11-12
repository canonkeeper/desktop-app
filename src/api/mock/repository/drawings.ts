import type {
  GetDrawingsFacetsOptions,
  ListDrawingsOptions,
  OperationRequest,
  OperationResponse,
  RequiredIdempotencyOptions,
  Schema,
} from '../../../shared/api';
import { MockApiError } from '../scenarios';
import type { createChatOperations } from './chat';
import type { MockRepositoryContext } from './context';
import { carryBookPlace, variantStagings, type MockDrawing, type MockSagaImage } from './gallery-state';
import { createGalleryView, editorRoles } from './gallery-view';
import { createIllustrationAccess } from './illustration-access';
import { chapterKey, chapterPlaces, placesOf } from './places';

type Drawing = Schema<'Drawing'>;
type DrawingBrief = Schema<'DrawingBrief'>;
type DrawingOperationId = Parameters<MockRepositoryContext['projectFor']>[1];

// helperKey keys the session of the helper the first message opens, apart from the key of the message.
function helperKey(key: string) {
  return `${key}:helper`;
}

/**
 * The words of a drawing button, as the backend writes them (IW1.1; specs/2026-11-11-ux9-drawings.md DR1.5): what the
 * drawing is about in the words of the author, no order to the helper; the chapter by its place, the paragraphs in
 * words, never a key or a link (specs/2026-11-10-ux1-names.md N3.3).
 */
function buttonIdea(request: Schema<'DrawingButtonRequest'>, place: string | undefined) {
  const subject = request.subject?.trim();
  const { from, to } = request;
  const paragraphs = from ? (to && to > from ? `, абзацы ${from}–${to}` : `, абзац ${from}`) : '';
  const as = place ? `, как в ${place.replace(/^Книга /u, 'Книге ')}` : '';
  switch (request.kind) {
    case 'hero':
      return `${subject ?? 'Главный герой саги'} — портрет по пояс${as}`;
    case 'portrait':
      return subject ? (place ? `${subject} — портрет${as}` : `${subject} — портрет по пояс`) : '';
    case 'scene':
      return place ? `${subject ? `${subject} — сцена` : 'Сцена'}: ${place}${paragraphs}` : '';
    case 'chapter':
      return place ? `Иллюстрация к главе: ${place}` : '';
    default:
      return '';
  }
}

/** What narrows the list of drawings and its counts (specs/2026-11-10-drawings-list-server.md DL1.1). */
type DrawingSearch = Pick<ListDrawingsOptions, 'q' | 'subject' | 'chapter' | 'look'>;
type DrawingListOperation = 'listDrawings' | 'getDrawingsFacets';

/** A word of the search as the server compares it: without case, ё as е. */
const fold = (text: string) => text.toLowerCase().replaceAll('ё', 'е');

function invalid(operationId: DrawingListOperation, message: string): never {
  throw new MockApiError(400, message, operationId, 'validation_failed');
}

/** One text parameter: absent, or 1–max characters after trimming (DL1.1). */
function textParameter(operationId: DrawingListOperation, value: string | undefined, max: number) {
  if (value === undefined) return undefined;
  const clean = value.trim();
  if (!clean || [...clean].length > max) invalid(operationId, 'A parameter of the list of drawings is empty or too long.');
  return clean;
}

/**
 * The parameters of the list of drawings from the address, for the mock server and the MSW handlers alike: a parameter
 * present goes as it is, so the repository refuses an empty or a wrong one as the server does.
 */
export function drawingListQuery(query: URLSearchParams): ListDrawingsOptions {
  const text = (name: 'q' | 'subject' | 'chapter') => (query.has(name) ? { [name]: query.get(name)! } : {});
  const look = query.get('look');
  return {
    ...text('q'), ...text('subject'), ...text('chapter'),
    ...(query.has('state') ? { state: query.get('state') as ListDrawingsOptions['state'] } : {}),
    ...(query.has('sort') ? { sort: query.get('sort') as ListDrawingsOptions['sort'] } : {}),
    ...(look === null ? {} : { look: look === 'true' ? true : look === 'false' ? false : (look as unknown as boolean) }),
    ...(query.has('cursor') ? { cursor: query.get('cursor')! } : {}),
    ...(query.has('limit') ? { limit: Number(query.get('limit')) } : {}),
  };
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

  /** The pictures of a drawing put off until the service of drawings is free (specs/2026-11-12-ux10-drawing-ops.md DO2.3). */
  function putOffOf(drawing: MockDrawing) {
    return versionsOf(drawing).filter((image) => image.status === 'drawing' && image.startsAfter && Date.parse(image.startsAfter) > Date.now());
  }

  /** The edits of a drawing put off the same way (specs/2026-11-12-ux12-drawings.md DW5.1). */
  function putOffEditsOf(drawing: MockDrawing) {
    return versionsOf(drawing).flatMap((image) => image.layers.filter((layer) => layer.status === 'drawing' && layer.startsAfter && Date.parse(layer.startsAfter) > Date.now()));
  }

  /**
   * The states of a drawing, one rule for its tile and for the list narrowed by state (specs/2026-11-10-drawings-list-server.md
   * DL1.2): work goes on, a picture waits for the service, a choice waits, the latest picture did not come out.
   */
  function statesOf(drawing: MockDrawing) {
    const versions = versionsOf(drawing);
    const newest = versions.reduce<MockSagaImage | undefined>((latest, image) => (!latest || image.number > latest.number ? image : latest), undefined);
    const putOff = new Set(putOffOf(drawing));
    const putOffEdits = new Set(putOffEditsOf(drawing));
    const startsAfter = [...[...putOff].map((image) => image.startsAfter!), ...[...putOffEdits].map((layer) => layer.startsAfter!)].sort()[0];
    return {
      working: Boolean(drawing.drawAfterReads) || versions.some((image) => (image.status === 'drawing' && !putOff.has(image)) || image.layers.some((layer) => layer.status === 'drawing' && !putOffEdits.has(layer))),
      waiting: waitingOf(drawing).length > 0,
      failed: newest?.status === 'failed' || newest?.status === 'refused',
      startsAfter,
    };
  }

  /** What the tile says is happening (specs/2026-11-05-drawing-one-screen.md D1.5), as `Drawing.Activity` of the backend. */
  function activityOf(drawing: MockDrawing): Schema<'DrawingSummary'>['activity'] {
    const states = statesOf(drawing);
    if (states.working) return 'working';
    if (states.startsAfter) return 'deferred';
    if (states.waiting) return 'choosing';
    return states.failed ? 'failed' : null;
  }

  function canDelete(drawing: MockDrawing) {
    const role = context.actorRole(drawing.projectId);
    return role === 'owner' || drawing.createdBy === context.currentPrincipalId() && editorRoles.some((item) => item === role);
  }

  /** What the list and a drawing share: the drawing as its tile shows it. */
  function summaryOf(drawing: MockDrawing): Omit<Schema<'DrawingSummary'>, 'subjects' | 'place' | 'lookOf'> {
    const waiting = waitingOf(drawing);
    const versions = versionsOf(drawing);
    const current = drawing.current ? versions.find((image) => image.number === drawing.current) : undefined;
    const cover = current ?? waiting.find((image) => image.status === 'ready') ?? waiting[0];
    const { startsAfter } = statesOf(drawing);
    return {
      id: drawing.id, title: drawing.brief.title, idea: drawing.idea, stage: stageOf(drawing, waiting), activity: activityOf(drawing), ...(startsAfter ? { startsAfter } : {}),
      current: current ? { ...view(current), drawingId: drawing.id } : null, cover: cover ? { ...view(cover), drawingId: drawing.id } : null, variants: waiting.length,
      canEdit: editorRoles.some((role) => role === context.actorRole(drawing.projectId)), canDelete: canDelete(drawing),
      createdAt: drawing.createdAt, updatedAt: drawing.updatedAt,
    };
  }

  /** The names whose look a version of the drawing is (specs/2026-11-10-drawings-list-server.md DL1.6). */
  function looksOf(drawing: MockDrawing) {
    const numbers = new Set(versionsOf(drawing).map((image) => image.number));
    const names = gallery().references.filter((reference) => reference.projectId === drawing.projectId && numbers.has(reference.number)).map((reference) => reference.subjectName);
    return [...new Set(names)].sort();
  }

  /** A row of the list: the tile with who is in the frame, the place of its chapter and whose look it is (DL1.6). */
  function rowOf(drawing: MockDrawing): Schema<'DrawingSummary'> {
    const chapter = drawing.brief.chapter?.trim();
    const place = chapter ? placesOf(context, drawing.projectId, [chapter])?.[chapter] : undefined;
    return { ...summaryOf(drawing), subjects: [...drawing.brief.subjects], ...(place ? { place } : {}), lookOf: looksOf(drawing) };
  }

  function drawingView(drawing: MockDrawing): Drawing {
    const summary = summaryOf(drawing);
    const versions = versionsOf(drawing);
    const waiting = waitingOf(drawing);
    // Beside the keys of the description stand the places of their chapters, as the backend writes them (N3.1).
    const places = placesOf(context, drawing.projectId, [
      drawing.brief.chapter, ...drawing.brief.segments.map((segment) => segment.source?.split('/')[0]), ...drawing.brief.facts.map((fact) => fact.source?.split('/')[0]),
    ]);
    return {
      ...summary, ...(places ? { places } : {}), brief: structuredClone(drawing.brief), revision: drawing.revision, inventedShare: inventedShare(drawing.brief),
      variantImages: waiting.map((image) => ({ ...view(image), drawingId: drawing.id })),
      candidates: (summary.current?.layers ?? []).filter((layer) => layer.candidate).map((layer) => layer.id),
      versions: versions.filter((image) => !image.variantGroup).map((image) => image.number).sort((left, right) => right - left),
      branch: drawing.branch, chatId: drawing.chatId,
    };
  }

  function touch(drawing: MockDrawing) {
    drawing.updatedAt = new Date().toISOString();
  }

  /** The chapters of the text of the saga in its order: their keys and places (DL1.3). */
  function sagaChapters(projectId: string) {
    const places = chapterPlaces(context, projectId);
    const books = context.state.books.filter((book) => book.projectId === projectId).sort((left, right) => left.order - right.order);
    const keys = books.flatMap((book) => context.state.chapters.filter((chapter) => chapter.bookId === book.id)
      .sort((left, right) => left.order - right.order).map((chapter) => chapterKey(book.order, chapter.order)));
    return keys.filter((key) => places.has(key)).map((key) => ({ key, place: places.get(key)! }));
  }

  /** The drawings of a project the search narrows to, as the server narrows them (DL1.1). */
  function searched(operationId: DrawingListOperation, projectId: string, options: DrawingSearch) {
    const words = textParameter(operationId, options.q, 100)?.split(/\s+/u).map(fold) ?? [];
    const subject = textParameter(operationId, options.subject, 200);
    const chapter = textParameter(operationId, options.chapter, 64);
    if (options.look !== undefined && typeof options.look !== 'boolean') invalid(operationId, 'look is true or false.');
    const places = chapterPlaces(context, projectId);
    return drawings().filter((drawing) => {
      if (drawing.projectId !== projectId) return false;
      const { brief } = drawing;
      const current = versionsOf(drawing).find((image) => image.number === drawing.current);
      const texts = [brief.title, current?.title ?? '', drawing.idea, ...brief.subjects, places.get(brief.chapter?.trim() ?? '') ?? ''].map(fold);
      return words.every((word) => texts.some((text) => text.includes(word)))
        && (subject === undefined || brief.subjects.some((name) => fold(name.trim()) === fold(subject)))
        && (chapter === undefined || brief.chapter?.trim() === chapter)
        && (!options.look || looksOf(drawing).length > 0);
    });
  }

  /** The order of the list (DL1.4): the last action first, or the chapters of the saga first and those without at the end. */
  function ordered(projectId: string, listed: MockDrawing[], sort: NonNullable<ListDrawingsOptions['sort']>) {
    const ranks = new Map(sort === 'chapters' ? sagaChapters(projectId).map(({ key }, index) => [key, index + 1] as const) : []);
    const rank = (drawing: MockDrawing) => ranks.get(drawing.brief.chapter?.trim() ?? '') ?? Number.MAX_SAFE_INTEGER;
    // Equal times keep the order the drawings were made in, as the list of the mock always did.
    return listed.sort((left, right) => rank(left) - rank(right) || right.updatedAt.localeCompare(left.updatedAt));
  }

  async function listDrawings(projectId: string, options: ListDrawingsOptions = {}): Promise<OperationResponse<'listDrawings', '200'>> {
    context.applyOperation('listDrawings');
    context.projectFor(projectId, 'listDrawings');
    const sort = options.sort ?? 'recent';
    const limit = options.limit ?? 30;
    if (sort !== 'recent' && sort !== 'chapters') invalid('listDrawings', 'sort is recent or chapters.');
    if (options.state !== undefined && !['waiting', 'failed', 'working'].includes(options.state)) invalid('listDrawings', 'state is waiting, failed or working.');
    if (!Number.isInteger(limit) || limit < 1 || limit > 100) invalid('listDrawings', 'limit is 1–100.');
    const state = options.state;
    const listed = ordered(projectId, searched('listDrawings', projectId, options).filter((drawing) => !state || statesOf(drawing)[state]), sort);
    // The cursor carries the order it was given in and the last drawing given (DL1.4).
    let start = 0;
    if (options.cursor) {
      const [cursorSort, afterId] = options.cursor.split(':');
      if (cursorSort !== sort || !afterId) invalid('listDrawings', 'The cursor belongs to another order.');
      start = listed.findIndex((drawing) => drawing.id === afterId) + 1;
    }
    const page = listed.slice(start, start + limit);
    const hasMore = start + limit < listed.length;
    return { data: page.map(rowOf), meta: { hasMore, nextCursor: hasMore ? `${sort}:${page.at(-1)!.id}` : null } };
  }

  /** The counts of the states within the search and everything the list narrows by (DL2.1). */
  async function getDrawingsFacets(projectId: string, options: GetDrawingsFacetsOptions = {}): Promise<OperationResponse<'getDrawingsFacets', '200'>> {
    context.applyOperation('getDrawingsFacets');
    context.projectFor(projectId, 'getDrawingsFacets');
    const found = searched('getDrawingsFacets', projectId, options).map(statesOf);
    const all = drawings().filter((drawing) => drawing.projectId === projectId);
    const names = new Map<string, { drawings: number; spellings: Map<string, number> }>();
    for (const drawing of all) {
      const spelled = new Map(drawing.brief.subjects.map((name) => name.trim()).filter(Boolean).map((name) => [fold(name), name] as const));
      for (const [folded, name] of spelled) {
        const entry = names.get(folded) ?? { drawings: 0, spellings: new Map<string, number>() };
        entry.drawings += 1;
        entry.spellings.set(name, (entry.spellings.get(name) ?? 0) + 1);
        names.set(folded, entry);
      }
    }
    const subjects = [...names.entries()].map(([folded, entry]) => {
      const [name] = [...entry.spellings.entries()].sort(([left, leftUses], [right, rightUses]) => rightUses - leftUses || (left < right ? -1 : left > right ? 1 : 0))[0]!;
      return { folded, name, drawings: entry.drawings };
    }).sort((left, right) => right.drawings - left.drawings || (left.folded < right.folded ? -1 : left.folded > right.folded ? 1 : 0))
      .map(({ name, drawings: count }) => ({ name, drawings: count }));
    const chapters = sagaChapters(projectId)
      .map(({ key, place }) => ({ chapter: key, place, drawings: all.filter((drawing) => drawing.brief.chapter?.trim() === key).length }))
      .filter((chapter) => chapter.drawings > 0);
    return {
      counts: {
        all: found.length, waiting: found.filter((states) => states.waiting).length,
        failed: found.filter((states) => states.failed).length, working: found.filter((states) => states.working).length,
      },
      subjects, chapters, looks: all.filter((drawing) => looksOf(drawing).length > 0).length,
    };
  }

  async function createDrawing(
    projectId: string, body: OperationRequest<'createDrawing'>, options: RequiredIdempotencyOptions,
  ): Promise<OperationResponse<'createDrawing', '201'>> {
    context.applyOperation('createDrawing');
    context.projectFor(projectId, 'createDrawing', [...editorRoles]);
    const place = body.request?.chapter ? placesOf(context, projectId, [body.request.chapter])?.[body.request.chapter.trim()] : undefined;
    const idea = (body.request ? buttonIdea(body.request, place) : body.idea ?? '').trim();
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
          ...(body.withoutStyle ? { withoutStyle: true } : {}),
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
      carryBookPlace(versionsOf(drawing).find((image) => image.number === drawing.current), version);
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
    // To «Недавно удалённые» for 7 days with its versions and looks (specs/2026-11-11-ux7-trash.md TB3.1); the
    // conversation of its helper goes at once.
    const versions = new Set(versionsOf(drawing));
    const numbers = new Set([...versions].map((image) => image.number));
    const deletedAt = new Date().toISOString();
    gallery().deletedDrawings.push({
      drawing: { ...drawing, chatId: null }, images: [...versions],
      references: gallery().references.filter((reference) => reference.projectId === projectId && numbers.has(reference.number)),
      deletedAt, restoreUntil: new Date(Date.parse(deletedAt) + 7 * 24 * 60 * 60 * 1000).toISOString(),
    });
    gallery().images = gallery().images.filter((image) => !versions.has(image));
    gallery().references = gallery().references.filter((reference) => !(reference.projectId === projectId && numbers.has(reference.number)));
    gallery().drawings = drawings().filter((item) => item !== drawing);
  }

  /** «Вернуть» of «Недавно удалённые» (TB3.1): the drawing back in its place with its versions and looks. */
  async function restoreDrawing(projectId: string, drawingId: string): Promise<OperationResponse<'restoreDrawing', '200'>> {
    context.applyOperation('restoreDrawing');
    context.projectFor(projectId, 'restoreDrawing');
    const index = gallery().deletedDrawings.findIndex((item) => item.drawing.projectId === projectId && item.drawing.id === drawingId);
    if (index < 0) {
      if (drawings().some((item) => item.projectId === projectId && item.id === drawingId)) {
        throw new MockApiError(409, 'The drawing is back already.', 'restoreDrawing', 'conflict');
      }
      throw new MockApiError(404, 'Drawing not found.', 'restoreDrawing', 'not_found');
    }
    const deleted = gallery().deletedDrawings[index]!;
    if (!canDelete(deleted.drawing)) throw new MockApiError(403, 'Only the author or the owner returns a drawing.', 'restoreDrawing', 'forbidden');
    gallery().deletedDrawings.splice(index, 1);
    gallery().drawings.push(deleted.drawing);
    gallery().images.push(...deleted.images);
    gallery().references.push(...deleted.references);
    return drawingView(deleted.drawing);
  }

  /**
   * When a drawing put off after the last picture of the drawing starts (DO2.1): a little after the time the service of
   * drawings named, as the backend's `illustrations.DeferredStart`; none ahead — 400.
   */
  function deferredStart(operationId: 'drawDrawing' | 'redrawProjectImageVariant', last: MockSagaImage | undefined) {
    const reset = last?.status === 'failed' && last.errorCode === 'draw_limited' && last.retryAt ? Date.parse(last.retryAt) : Number.NaN;
    if (!(reset > Date.now())) throw new MockApiError(400, 'The service of drawings names no time ahead.', operationId, 'illustrations.not_busy');
    return new Date(reset + 30_000).toISOString();
  }

  /** One version, current when the drawing has none, or a group by the stagings (IW1.4), paid once. */
  function startDrawing(projectId: string, drawing: MockDrawing, variants: number, operationId: 'drawDrawing' | 'submitDrawingTurn', startsAfter?: string) {
    const { charge } = access.reserveDrawings(operationId, 1);
    const { brief } = drawing;
    const state = gallery();
    const variantGroup = variants > 1 ? `00000000-0000-4000-8000-${String(state.nextLayer++).padStart(12, '0')}` : undefined;
    const stagings = variantGroup ? variantStagings(brief.staging, variants) : [undefined];
    for (const [index, staging] of stagings.entries()) {
      const number = (state.lastNumber[projectId] ?? 0) + 1;
      state.lastNumber[projectId] = number;
      drawing.branch ??= number;
      state.images.push({
        projectId, createdBy: context.currentPrincipalId(), number, branch: drawing.branch, status: 'drawing', phase: 'queued', partialFrame: 0,
        kind: brief.kind, aspect: brief.aspect ?? (brief.kind === 'character' ? 'portrait' : 'landscape'), title: brief.title || null,
        prompt: brief.segments.map((segment) => segment.text).join(' '), revisedPrompt: null, style: brief.withoutStyle ? null : state.styles[projectId] ?? null,
        chapter: brief.chapter, subjects: brief.subjects.map((name) => ({ key: name.toLowerCase(), name })), baseImage: null,
        referenceImages: [...(brief.references ?? [])], chatId: null, width: null, height: null, errorCode: null, errorMessage: null, retryAt: null,
        createdAt: new Date().toISOString(), readyAt: null, sources: brief.segments.flatMap((segment) => (segment.source ? [segment.source] : [])),
        sketch: false, renderRevision: 0, layers: [], facts: brief.facts.map((fact) => fact.text), charge: index === 0 ? charge : null,
        ...(variantGroup ? { variantGroup, variantPrompt: staging!, ...(index > 0 ? { variantPaid: true } : {}) } : {}),
        ...(startsAfter ? { startsAfter } : {}),
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
    if (!brief.segments.length) throw new MockApiError(400, 'A drawing without a description draws nothing.', 'drawDrawing', 'validation_failed');
    if (waitingOf(drawing).length) throw new MockApiError(409, 'The variant is not chosen yet.', 'drawDrawing', 'illustrations.variant_pending');
    return context.idempotentCommand('drawDrawing', projectId, options, { drawingId, ...body }, () => {
      const newest = versionsOf(drawing).reduce<MockSagaImage | undefined>((latest, image) => (!latest || image.number > latest.number ? image : latest), undefined);
      startDrawing(projectId, drawing, body.variants, 'drawDrawing', body.whenFree ? deferredStart('drawDrawing', newest) : undefined);
      touch(drawing);
      return drawingView(drawing);
    });
  }

  /**
   * «Нарисовать этот ещё раз» of a variant that did not come out (specs/2026-11-12-ux10-drawing-ops.md DO1): the same variant
   * in its place, for nothing while its group holds the spend; a refusal of the model is not drawn again.
   */
  async function redrawProjectImageVariant(
    projectId: string, number: number, body: OperationRequest<'redrawProjectImageVariant'>, options: RequiredIdempotencyOptions,
  ): Promise<OperationResponse<'redrawProjectImageVariant', '200'>> {
    context.applyOperation('redrawProjectImageVariant');
    context.projectFor(projectId, 'redrawProjectImageVariant', [...editorRoles]);
    return context.idempotentCommand('redrawProjectImageVariant', projectId, options, { number, ...body }, () => {
      const image = imageFor('redrawProjectImageVariant', projectId, number);
      const owner = drawings().find((item) => item.projectId === projectId && item.branch === image.branch);
      // A version out of any group is drawn again as a new version, for one drawing (specs/2026-11-12-ux12-drawings.md DW5.1).
      if (!image.variantGroup && (!owner || image.enlarged)) throw new MockApiError(409, 'The drawing is no variant or version of a drawing.', 'redrawProjectImageVariant', 'conflict');
      if (!image.variantGroup && waitingOf(owner!).length) throw new MockApiError(409, 'The variant is not chosen yet.', 'redrawProjectImageVariant', 'illustrations.variant_pending');
      if (image.status === 'refused') throw new MockApiError(409, 'The model refused this description.', 'redrawProjectImageVariant', 'illustrations.refused');
      if (image.status !== 'failed') throw new MockApiError(409, 'The variant is drawn or ready.', 'redrawProjectImageVariant', 'conflict');
      const startsAfter = body.whenFree ? deferredStart('redrawProjectImageVariant', image) : undefined;
      const group = image.variantGroup ? gallery().images.filter((item) => item.projectId === projectId && item.variantGroup === image.variantGroup) : [];
      const paid = group.some((item) => item.charge || item.status === 'ready' || item.status === 'refused' || (item.status === 'drawing' && item !== image));
      const charge = paid ? null : access.reserveDrawings('redrawProjectImageVariant', 1).charge;
      // A new Job: its work starts now, the row keeps its making (specs/2026-11-12-ux12-drawings.md DW1.2).
      Object.assign(image, { status: 'drawing', phase: 'queued', partialFrame: 0, errorCode: null, errorMessage: null, retryAt: null, charge, variantPaid: paid, startedAt: new Date().toISOString() });
      if (startsAfter) image.startsAfter = startsAfter;
      else delete image.startsAfter;
      const drawing = drawings().find((item) => item.projectId === projectId && item.branch === image.branch);
      if (drawing) touch(drawing);
      return { ...view(image), ...(drawing ? { drawingId: drawing.id } : {}) };
    });
  }

  /**
   * «Отменить» of «Нарисую после 12:50» (DO2.2): the pictures put off end as they were before — the service busy until about
   * that time — and what they spent goes back; none put off is a conflict.
   */
  async function cancelDeferredDrawing(projectId: string, drawingId: string): Promise<OperationResponse<'cancelDeferredDrawing', '200'>> {
    context.applyOperation('cancelDeferredDrawing');
    context.projectFor(projectId, 'cancelDeferredDrawing', [...editorRoles]);
    const drawing = drawingFor('cancelDeferredDrawing', projectId, drawingId);
    const putOff = putOffOf(drawing);
    const putOffEdits = putOffEditsOf(drawing);
    if (!putOff.length && !putOffEdits.length) throw new MockApiError(409, 'Nothing of the drawing is put off.', 'cancelDeferredDrawing', 'conflict');
    for (const layer of putOffEdits) {
      Object.assign(layer, { status: 'failed', phase: 'failed', errorCode: 'draw_limited', errorMessage: 'Рисунок ждал, пока освободится сервис рисунков, и отменён.', retryAt: layer.startsAfter });
      delete layer.startsAfter;
      if (layer.charge === 'trial') context.state.trialImagesUsed[layer.createdBy] = Math.max(0, (context.state.trialImagesUsed[layer.createdBy] ?? 0) - 1);
      layer.charge = null;
    }
    for (const image of putOff) {
      Object.assign(image, { status: 'failed', phase: 'failed', errorCode: 'draw_limited', errorMessage: 'Рисунок ждал, пока освободится сервис рисунков, и отменён.', retryAt: image.startsAfter });
      delete image.startsAfter;
      if (image.charge === 'trial') context.state.trialImagesUsed[image.createdBy] = Math.max(0, (context.state.trialImagesUsed[image.createdBy] ?? 0) - 1);
      image.charge = null;
    }
    touch(drawing);
    return drawingView(drawing);
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

  return {
    listDrawings, getDrawingsFacets, createDrawing, getDrawing, updateDrawing, deleteDrawing, restoreDrawing, drawDrawing, cancelDeferredDrawing,
    redrawProjectImageVariant, chooseProjectImageLayerVariant, submitDrawingTurn,
  };
}
