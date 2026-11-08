import type {
  OperationRequest,
  OperationResponse,
  RequiredIdempotencyOptions,
  Schema,
} from '../../../shared/api';
import { MockApiError } from '../scenarios';
import type { MockRepositoryContext } from './context';
import type { MockCharge, MockImageLayer, MockSagaImage } from './gallery-state';
import { createGalleryView, editorRoles } from './gallery-view';
import { createIllustrationAccess } from './illustration-access';


type ImageOperationId = Parameters<MockRepositoryContext['projectFor']>[1];

/** How the mock agent reads a change of a whole drawing and stages a new one (R21.14). */
const mockReadings = ['сдержанная и естественная, приглушённые цвета', 'другая форма и цвет того, что меняется', 'самая выразительная: крупнее и драматичнее', 'неожиданная: другой свет и настроение'];
/** The instruction of «Вырезать фон», as the backend has it (R18.3, R24.2). */
const mockCutoutInstruction = 'Вырежи главного героя или предмет этого рисунка: только он, без фона; лицо, позу и цвета не меняй.';

/** Styles the mock sketches try, as the backend picks four by the genre of the saga (R8.6). */
const sketchStyles = [
  'Цифровая живопись — детализированная цифровая живопись, кинематографичный свет, насыщенные цвета',
  'Акварель — акварель, мягкие размытые края, прозрачные слои краски, фактура бумаги',
  'Графический роман — графический роман, чёткий контур тушью, плоские тени, сдержанная палитра',
  'Масляная живопись — классическая масляная живопись, густые мазки, тёплый свет, глубокие тени',
];

/**
 * The drawings the web starts and the layers of a drawing in the mock gallery
 * (specs/2026-10-29-saga-illustrations.md UC8). New drawings and layers develop on later reads
 * (gallery-view.ts).
 */
export function createImageDrawingOperations(context: MockRepositoryContext) {
  const { gallery, view, imageFor } = createGalleryView(context);

  function requireEditor(operationId: ImageOperationId, projectId: string) {
    context.projectFor(projectId, operationId, [...editorRoles]);
  }

  const access = createIllustrationAccess(context);

  /**
   * The right to illustrations and images_per_day of the actor (R11.2, R5.1); what it spent, so a
   * drawing stopped by the provider's image limit gives it back (R13.6).
   */
  function reserveImages(operationId: ImageOperationId): MockCharge {
    const trial = access.viewOf(context.currentPrincipalId()).state === 'trial';
    access.reserveDrawings(operationId, 1);
    return trial ? 'trial' : 'quota';
  }

  function readyImage(operationId: ImageOperationId, projectId: string, number: number) {
    const image = imageFor(operationId, projectId, number);
    if (image.status !== 'ready') throw new MockApiError(404, 'Image not found.', operationId, 'not_found');
    // A variant the author has not chosen is no input of anything (R21.8).
    if (image.variantGroup) throw new MockApiError(409, 'The variant is not chosen yet.', operationId, 'illustrations.variant_pending');
    return image;
  }

  /** The images attached to a region edit or to the agent's new version: ready images of the project (R15.15). */
  function attachedOf(operationId: ImageOperationId, projectId: string, number: number, images: readonly number[] | undefined) {
    const numbers = images ?? [];
    if (numbers.length > 8 || new Set(numbers).size !== numbers.length || numbers.some((item) => item < 1 || item === number)) {
      throw new MockApiError(400, 'At most eight distinct images, not the drawing itself.', operationId, 'validation_failed');
    }
    for (const item of numbers) readyImage(operationId, projectId, item);
    return numbers.length ? { attachedImages: [...numbers] } : {};
  }

  function startDrawing(projectId: string, fields: Partial<MockSagaImage> & Pick<MockSagaImage, 'kind' | 'aspect' | 'prompt' | 'title'>): MockSagaImage {
    const state = gallery();
    const number = (state.lastNumber[projectId] ?? 0) + 1;
    state.lastNumber[projectId] = number;
    const image: MockSagaImage = {
      projectId, createdBy: context.currentPrincipalId(), number, status: 'drawing', phase: 'queued', partialFrame: 0,
      revisedPrompt: null, style: state.styles[projectId] ?? null, chapter: null, subjects: [], baseImage: null,
      referenceImages: [], chatId: null, width: null, height: null, errorCode: null, errorMessage: null, retryAt: null,
      createdAt: new Date().toISOString(), readyAt: null, sources: [], sketch: false, renderRevision: 0, layers: [],
      ...fields,
      // A new drawing starts its own branch; a version or a variant takes the branch it is made from (R14.1).
      branch: fields.branch ?? number,
    };
    state.images.push(image);
    // A version that starts a branch starts its drawing too, current unless it waits among variants (IW5.3).
    const owner = state.drawings.find((drawing) => drawing.projectId === projectId && drawing.branch === image.branch);
    if (owner) {
      owner.updatedAt = image.createdAt;
    } else {
      state.drawings.push({
        id: `00000000-0000-4000-a000-${String(state.nextLayer++).padStart(12, '0')}`, projectId, idea: '', revision: 1, branch: image.branch,
        current: image.variantGroup ? null : number, chatId: null,
        brief: { title: image.title ?? '', kind: image.kind === 'upload' ? 'scene' : image.kind, aspect: null, chapter: image.chapter, subjects: image.subjects.map((subject) => subject.name), segments: [{ text: image.prompt, origin: 'helper' }], facts: [], staging: [], references: null, images: [] },
        createdBy: image.createdBy, createdAt: image.createdAt, updatedAt: image.createdAt, 
      });
    }
    return image;
  }

  /** The variants of one start (R21.3, R21.4): rows of their own in one branch, paid for by the first. */
  function startVariants(projectId: string, count: number, prompts: readonly string[], fields: Parameters<typeof startDrawing>[1]): MockSagaImage[] {
    if (count === 1) return [startDrawing(projectId, fields)];
    const variantGroup = `00000000-0000-4000-8000-${String(gallery().nextLayer++).padStart(12, '0')}`;
    const first = startDrawing(projectId, { ...fields, variantGroup, variantPrompt: prompts[0]! });
    const rest = prompts.slice(1, count).map((variantPrompt) => startDrawing(projectId, { ...fields, charge: null, branch: first.branch, variantGroup, variantPrompt, variantPaid: true }));
    return [first, ...rest];
  }

  /** What a version takes from the drawing it is made from; an upload is a branch of its own, a version from it a scene (R15.8). */
  function branchOf(image: MockSagaImage) {
    const upload = image.kind === 'upload';
    return {
      ...(upload ? {} : { branch: image.branch }), kind: upload ? 'scene' : image.kind, aspect: image.aspect, title: image.title,
      ...(image.transparent ? { transparent: true } : {}),
      chapter: image.chapter, subjects: image.subjects,
      referenceImages: image.referenceImages.slice(0, 4), sources: image.sources, facts: currentFacts(image),
    };
  }

  /** The facts a drawing is checked against now: those of its last ready visible layer, or its own (R12.1). */
  function currentFacts(image: MockSagaImage) {
    const laid = image.layers.filter((layer) => layer.status === 'ready' && layer.visible).at(-1);
    return [...(laid?.facts ?? image.facts ?? [])];
  }

  /** «Вырезать фон» (R18.3, R24.2): a version of the branch on a transparent background, without the facts of its base. */
  async function cutoutProjectImage(
    projectId: string, number: number, options: RequiredIdempotencyOptions,
  ): Promise<OperationResponse<'cutoutProjectImage', '201'>> {
    context.applyOperation('cutoutProjectImage');
    requireEditor('cutoutProjectImage', projectId);
    const base = readyImage('cutoutProjectImage', projectId, number);
    return context.idempotentCommand('cutoutProjectImage', projectId, options, { number }, () => {
      const charge = reserveImages('cutoutProjectImage');
      return view(startDrawing(projectId, {
        ...branchOf(base), prompt: mockCutoutInstruction, baseImage: number, charge, transparent: true, facts: [],
      }));
    });
  }

  /**
   * What the mock agent starts in a chat turn with saga.draw, base_image and no region (R21.1, R24.3): a version
   * of the branch or, with variants, a group to choose from. It is no operation of the API.
   */
  async function agentDrawVersion(
    projectId: string, number: number, body: { instruction: string; aspect?: Schema<'SagaImageAspect'>; images?: readonly number[]; variants?: number },
  ) {
    const base = readyImage('createProjectImageVariant', projectId, number);
    const charge = reserveImages('createProjectImageVariant');
    return view(startVariants(projectId, body.variants ?? 1, mockReadings, {
      ...branchOf(base), prompt: body.instruction, baseImage: number, charge,
      ...attachedOf('createProjectImageVariant', projectId, number, body.images),
      ...(body.aspect ? { aspect: body.aspect } : {}),
    })[0]!);
  }

  /** «Перегенерировать» (R26.3): what the author says goes with it; what would not reach the model is refused. */
  async function createProjectImageVariant(
    projectId: string, number: number, body: OperationRequest<'createProjectImageVariant'>, options: RequiredIdempotencyOptions,
  ): Promise<OperationResponse<'createProjectImageVariant', '201'>> {
    context.applyOperation('createProjectImageVariant');
    requireEditor('createProjectImageVariant', projectId);
    const original = readyImage('createProjectImageVariant', projectId, number);
    if (original.kind === 'upload' || (original.enlarged && !original.baseImage)) {
      throw new MockApiError(400, 'There is nothing to draw again.', 'createProjectImageVariant', 'validation_failed');
    }
    const note = (body.note ?? '').replace(/\s+/gu, ' ').trim();
    const references = body.references;
    const invalid = (note.length > 4000)
      || (original.enlarged && (note || body.aspect || references))
      || (original.transparent && original.baseImage && (body.aspect || references))
      || (references && (references.includes(number) || new Set(references).size !== references.length || new Set([...references, ...(original.attachedImages ?? [])]).size > 9));
    if (invalid) throw new MockApiError(400, 'This regeneration cannot be drawn.', 'createProjectImageVariant', 'validation_failed');
    for (const item of references ?? []) readyImage('createProjectImageVariant', projectId, item);
    // 2–4 variants are a group by as many stagings of the brief of the drawing, as the backend draws them (IW3.7, S3.4).
    const count = body.variants ?? 1;
    const drawing = gallery().drawings.find((item) => item.projectId === projectId && item.branch === original.branch);
    if (count > 1) {
      if (!drawing || drawing.brief.staging.length !== count || original.enlarged || (original.transparent && original.baseImage)) {
        throw new MockApiError(400, 'As many stagings as variants.', 'createProjectImageVariant', 'validation_failed');
      }
      if (gallery().images.some((item) => item.projectId === projectId && item.branch === original.branch && item.variantGroup)) {
        throw new MockApiError(409, 'The variant is not chosen yet.', 'createProjectImageVariant', 'illustrations.variant_pending');
      }
    }
    return context.idempotentCommand('createProjectImageVariant', projectId, options, { number, ...body }, () => {
      const charge = reserveImages('createProjectImageVariant');
      if (count > 1) {
        const [first] = startVariants(projectId, count, drawing!.brief.staging, {
          ...branchOf(original), prompt: original.prompt, style: original.style, charge,
          ...(body.aspect ? { aspect: body.aspect } : {}),
          ...(references ? { referenceImages: [...references] } : {}),
          ...(note ? { regenerateNote: note } : {}),
        });
        return view(first!);
      }
      return view(startDrawing(projectId, {
        ...branchOf(original), prompt: original.prompt, baseImage: original.baseImage, style: original.style, sketch: original.sketch, charge,
        ...(body.aspect ? { aspect: body.aspect } : {}),
        ...(references ? { referenceImages: [...references] } : {}),
        ...(note ? { regenerateNote: note } : {}),
        ...(original.attachedImages ? { attachedImages: [...original.attachedImages] } : {}),
        ...(original.enlarged ? { enlarged: true as const, enlargedSize: original.enlargedSize } : {}),
      }));
    });
  }

  /** «Увеличить ×2» (R17.1, R17.2): a version of the branch from the drawing, at twice its size once it is done. */
  async function enlargeProjectImage(
    projectId: string, number: number, options: RequiredIdempotencyOptions,
  ): Promise<OperationResponse<'enlargeProjectImage', '201'>> {
    context.applyOperation('enlargeProjectImage');
    requireEditor('enlargeProjectImage', projectId);
    const base = readyImage('enlargeProjectImage', projectId, number);
    const width = base.width ?? 0;
    const height = base.height ?? 0;
    if (Math.max(width, height) >= 2500) {
      throw new MockApiError(400, 'The drawing is large enough.', 'enlargeProjectImage', 'validation_failed');
    }
    return context.idempotentCommand('enlargeProjectImage', projectId, options, { number }, () => {
      const charge = reserveImages('enlargeProjectImage');
      return view(startDrawing(projectId, {
        ...branchOf(base), prompt: 'Увеличено ×2', baseImage: number, style: base.style, charge,
        enlarged: true, enlargedSize: { width: width * 2, height: height * 2 },
      }));
    });
  }

  function regionOf(body: OperationRequest<'createProjectImageLayer'>): Schema<'SagaImageRegion'> {
    const region = body.region;
    if (!region) return { x: 0.25, y: 0.25, w: 0.5, h: 0.5 };
    const valid = [region.x, region.y].every((value) => value >= 0 && value < 1) && region.w >= 0.01 && region.h >= 0.01;
    if (!valid) throw new MockApiError(400, 'Region is outside the drawing.', 'createProjectImageLayer', 'validation_failed');
    return { x: region.x, y: region.y, w: Math.min(region.w, 1 - region.x), h: Math.min(region.h, 1 - region.y) };
  }

  async function createProjectImageLayer(
    projectId: string, number: number, body: OperationRequest<'createProjectImageLayer'>, options: RequiredIdempotencyOptions,
  ): Promise<OperationResponse<'createProjectImageLayer', '201'>> {
    context.applyOperation('createProjectImageLayer');
    requireEditor('createProjectImageLayer', projectId);
    const instruction = body.instruction.trim();
    if (!instruction || (!body.region && !body.brushMask)) {
      throw new MockApiError(400, 'A region edit needs an instruction and a region or a brush shape.', 'createProjectImageLayer', 'validation_failed');
    }
    const image = readyImage('createProjectImageLayer', projectId, number);
    const region = regionOf(body);
    const attached = attachedOf('createProjectImageLayer', projectId, number, body.images);
    // What the author chose goes as it is, within the limit and not the drawing itself (R25.2, R25.3).
    const references = body.references ?? [];
    if (new Set(references).size !== references.length || references.includes(number) || new Set([...references, ...(body.images ?? [])]).size > 9) {
      throw new MockApiError(400, 'Too many images chosen, or the drawing itself.', 'createProjectImageLayer', 'validation_failed');
    }
    for (const item of references) readyImage('createProjectImageLayer', projectId, item);
    return context.idempotentCommand('createProjectImageLayer', projectId, options, { number, instruction, region, brush: Boolean(body.brushMask), ...attached, references: body.references }, () => {
      const charge = reserveImages('createProjectImageLayer');
      if (image.layers.length >= 30) throw new MockApiError(409, 'Flatten the layers first.', 'createProjectImageLayer', 'conflict');
      const layer: MockImageLayer = {
        id: `00000000-0000-4000-8000-${String(gallery().nextLayer++).padStart(12, '0')}`,
        position: (image.layers.at(-1)?.position ?? 0) + 1, status: 'drawing', phase: 'queued', partialFrame: 0,
        instruction, region, brush: Boolean(body.brushMask), visible: true, createdBy: context.currentPrincipalId(),
        errorCode: null, errorMessage: null, retryAt: null, createdAt: new Date().toISOString(), readyAt: null, facts: currentFacts(image), charge, ...attached,
      };
      image.layers.push(layer);
      return view(image);
    });
  }

  function layerOf(operationId: ImageOperationId, image: MockSagaImage, layerId: string) {
    const layer = image.layers.find((item) => item.id === layerId);
    if (!layer) throw new MockApiError(404, 'Layer not found.', operationId, 'not_found');
    return layer;
  }

  async function updateProjectImageLayer(
    projectId: string, number: number, layerId: string, body: OperationRequest<'updateProjectImageLayer'>,
  ): Promise<OperationResponse<'updateProjectImageLayer', '200'>> {
    context.applyOperation('updateProjectImageLayer');
    requireEditor('updateProjectImageLayer', projectId);
    const image = readyImage('updateProjectImageLayer', projectId, number);
    const layer = layerOf('updateProjectImageLayer', image, layerId);
    if (layer.visible !== body.visible) {
      layer.visible = body.visible;
      if (layer.status === 'ready') image.renderRevision += 1;
    }
    return view(image);
  }

  async function deleteProjectImageLayer(projectId: string, number: number, layerId: string): Promise<OperationResponse<'deleteProjectImageLayer', '200'>> {
    context.applyOperation('deleteProjectImageLayer');
    requireEditor('deleteProjectImageLayer', projectId);
    const image = readyImage('deleteProjectImageLayer', projectId, number);
    const layer = layerOf('deleteProjectImageLayer', image, layerId);
    if (context.actorRole(projectId) !== 'owner' && layer.createdBy !== context.currentPrincipalId()) {
      throw new MockApiError(403, 'Only the author of the layer or the owner removes it.', 'deleteProjectImageLayer', 'forbidden');
    }
    image.layers = image.layers.filter((item) => item !== layer);
    if (layer.status === 'ready' && layer.visible) image.renderRevision += 1;
    return view(image);
  }

  async function createProjectCharacterSheet(
    projectId: string, body: OperationRequest<'createProjectCharacterSheet'>, options: RequiredIdempotencyOptions,
  ): Promise<OperationResponse<'createProjectCharacterSheet', '201'>> {
    context.applyOperation('createProjectCharacterSheet');
    requireEditor('createProjectCharacterSheet', projectId);
    const name = body.subject.trim();
    if (!name) throw new MockApiError(400, 'Subject is required.', 'createProjectCharacterSheet', 'validation_failed');
    return context.idempotentCommand('createProjectCharacterSheet', projectId, options, { name, chapter: body.chapter ?? null }, () => {
      const charge = reserveImages('createProjectCharacterSheet');
      const description = `Лист персонажа «${name}»: анфас, в профиль и со спины.`;
      return view(startDrawing(projectId, {
        kind: 'sheet', aspect: 'landscape', chapter: body.chapter ?? null, subjects: [{ key: name.toLowerCase(), name }],
        prompt: description, title: `Лист персонажа: ${name}`, charge,
      }));
    });
  }

  async function createIllustrationStyleSketches(
    projectId: string, body: OperationRequest<'createIllustrationStyleSketches'>, options: RequiredIdempotencyOptions,
  ): Promise<OperationResponse<'createIllustrationStyleSketches', '201'>> {
    context.applyOperation('createIllustrationStyleSketches');
    requireEditor('createIllustrationStyleSketches', projectId);
    const name = body.subject?.trim() || 'Мара';
    return context.idempotentCommand('createIllustrationStyleSketches', projectId, options, { name }, () => {
      const count = access.reserveSketches('createIllustrationStyleSketches', projectId, sketchStyles.length);
      const description = `Портрет персонажа «${name}» по пояс.`;
      return {
        data: sketchStyles.slice(0, count).map((style) => view(startDrawing(projectId, {
          kind: 'character', aspect: 'portrait', subjects: [{ key: name.toLowerCase(), name }], style, sketch: true,
          prompt: description, title: `Эскиз: ${style.split(' — ')[0]!.toLowerCase()}`,
        }))),
      };
    });
  }

  return {
    cutoutProjectImage,
    agentDrawVersion,
    createProjectImageVariant,
    enlargeProjectImage,
    createProjectImageLayer,
    updateProjectImageLayer,
    deleteProjectImageLayer,
    createProjectCharacterSheet,
    createIllustrationStyleSketches,
  };
}
