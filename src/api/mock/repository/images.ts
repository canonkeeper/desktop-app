import type {
  ListProjectImagesOptions,
  OperationRequest,
  OperationResponse,
  RemoveProjectImageReferenceQuery,
  RequiredIdempotencyOptions,
} from '../../../shared/api';
import { MockApiError } from '../scenarios';
import type { MockRepositoryContext } from './context';
import { mockImagePNG, type MockSagaImage } from './gallery-state';
import { createGalleryView, editorRoles } from './gallery-view';

export type ProjectImageFileQuery = {
  size?: 'thumb' | 'screen' | 'full' | 'base' | 'before' | 'partial';
  layer?: string;
  v?: number;
};

export function createImageOperations(context: MockRepositoryContext) {
  const { gallery, advance, view, imageFor } = createGalleryView(context);

  async function listProjectImages(projectId: string, options: ListProjectImagesOptions = {}): Promise<OperationResponse<'listProjectImages', '200'>> {
    context.applyOperation('listProjectImages');
    context.projectFor(projectId, 'listProjectImages');
    const limit = options.limit ?? 50;
    const before = options.cursor ? Number(options.cursor) : Number.POSITIVE_INFINITY;
    if (!Number.isInteger(limit) || limit < 1 || limit > 100 || Number.isNaN(before) ||
      (options.group !== undefined && (options.branch !== undefined || options.turn !== undefined))) {
      throw new MockApiError(400, 'Invalid gallery page.', 'listProjectImages', 'validation_failed');
    }
    const project = gallery().images.filter((image) => image.projectId === projectId);
    project.forEach(advance);
    // Variants waiting for the choice are the workshop's, not the gallery's: only the drawings of a turn list them (IW6.3).
    const tile = (image: MockSagaImage) => !image.variantGroup || options.turn !== undefined;
    const isReference = (image: MockSagaImage) => gallery().references.some((reference) => reference.projectId === projectId && reference.number === image.number);
    const matches = (image: MockSagaImage) => (options.kind === undefined || image.kind === options.kind)
      && (!options.drawings || image.kind !== 'upload')
      && (options.chapter === undefined || image.chapter === options.chapter)
      && (options.turn === undefined || image.turnId === options.turn)
      && (options.branch === undefined || image.branch === options.branch)
      && (!options.references || isReference(image))
      && (options.subject === undefined || image.subjects.some((subject) => subject.key === options.subject) ||
        gallery().references.some((reference) => reference.projectId === projectId && reference.number === image.number && reference.subjectKey === options.subject));
    if (options.group === 'branch') return branchPage(project, matches, tile, before, limit);
    const found = project.filter((image) => image.number < before && matches(image) && tile(image)).sort((left, right) => right.number - left.number);
    const page = found.slice(0, limit);
    const hasMore = found.length > limit;
    return { data: page.map(view), meta: { hasMore, nextCursor: hasMore ? String(page.at(-1)!.number) : null } };
  }

  /**
   * The gallery by drawing (IW6.3, R14.3): a tile for every drawing with a current version, which is its cover, and
   * for every upload; a drawing without a current version and the variants waiting for the choice are the workshop's.
   * A tile counts its versions without the variants; newest latest version first, paged by it.
   */
  function branchPage(project: MockSagaImage[], matches: (image: MockSagaImage) => boolean, tile: (image: MockSagaImage) => boolean, before: number, limit: number): OperationResponse<'listProjectImages', '200'> {
    const branches = new Map<number, MockSagaImage[]>();
    for (const image of project.filter(tile)) branches.set(image.branch, [...(branches.get(image.branch) ?? []), image]);
    const shown = [...branches.entries()]
      .filter(([, versions]) => versions.some(matches))
      .flatMap(([branch, versions]) => {
        const ordered = [...versions].sort((left, right) => right.number - left.number);
        const drawing = gallery().drawings.find((item) => item.projectId === ordered[0]!.projectId && item.branch === branch);
        const cover = drawing ? ordered.find((image) => image.number === drawing.current) : ordered.find((image) => image.kind === 'upload');
        return cover ? [{ cover, latest: ordered[0]!.number, versions: ordered.length }] : [];
      })
      .filter((branch) => branch.latest < before)
      .sort((left, right) => right.latest - left.latest);
    const page = shown.slice(0, limit);
    const hasMore = shown.length > limit;
    return {
      data: page.map((branch) => ({ ...view(branch.cover), versions: branch.versions })),
      meta: { hasMore, nextCursor: hasMore ? String(page.at(-1)!.latest) : null },
    };
  }

  async function getProjectImage(projectId: string, number: number): Promise<OperationResponse<'getProjectImage', '200'>> {
    context.applyOperation('getProjectImage');
    context.projectFor(projectId, 'getProjectImage');
    const image = imageFor('getProjectImage', projectId, number);
    advance(image);
    return view(image);
  }

  /**
   * The file of a drawing: the render, its copies and its base of a ready drawing, the draft frame of
   * one still drawn or of a layer still drawn; the mock serves one PNG for every file.
   */
  async function getProjectImageFile(
    projectId: string, number: number, query: ProjectImageFileQuery = {},
  ): Promise<{ content: Uint8Array<ArrayBuffer>; mimeType: string; cacheControl: string }> {
    context.applyOperation('getProjectImageFile');
    context.projectFor(projectId, 'getProjectImageFile');
    const image = imageFor('getProjectImageFile', projectId, number);
    const notFound = () => new MockApiError(404, 'Image not found.', 'getProjectImageFile', 'not_found');
    if (query.size === 'partial') {
      const drawn = query.layer === undefined
        ? image.status === 'drawing' && image.partialFrame > 0
        : image.layers.some((layer) => layer.id === query.layer && layer.status === 'drawing' && layer.partialFrame > 0);
      if (!drawn) throw notFound();
      return { content: mockImagePNG, mimeType: 'image/png', cacheControl: 'no-store' };
    }
    if (image.status !== 'ready' || query.layer !== undefined) throw notFound();
    const current = query.v === image.renderRevision;
    return { content: mockImagePNG, mimeType: 'image/png', cacheControl: current ? 'private, max-age=31536000, immutable' : 'private, no-cache' };
  }

  async function updateProjectImageDetails(
    projectId: string, number: number, body: OperationRequest<'updateProjectImageDetails'>,
  ): Promise<OperationResponse<'updateProjectImageDetails', '200'>> {
    context.applyOperation('updateProjectImageDetails');
    context.projectFor(projectId, 'updateProjectImageDetails', [...editorRoles]);
    const image = imageFor('updateProjectImageDetails', projectId, number);
    const description = body.description?.trim().replace(/\s+/gu, ' ') ?? '';
    const chapter = body.chapter?.trim() ?? '';
    if (description.length > 200 || body.subjects.length > 5) {
      throw new MockApiError(400, 'The details are too long.', 'updateProjectImageDetails', 'validation_failed');
    }
    image.title = description || null;
    image.chapter = chapter || null;
    image.subjects = body.subjects.map(({ key, name }) => ({ key: key ?? name.toLowerCase(), name }));
    return view(image);
  }

  async function setProjectImageReference(
    projectId: string, number: number, body: OperationRequest<'setProjectImageReference'>,
  ): Promise<OperationResponse<'setProjectImageReference', '200'>> {
    context.applyOperation('setProjectImageReference');
    context.projectFor(projectId, 'setProjectImageReference', [...editorRoles]);
    const image = imageFor('setProjectImageReference', projectId, number);
    if (image.status !== 'ready' || !body.subjectKey.trim() || !body.subjectName.trim()) {
      throw new MockApiError(400, 'Only a ready drawing can be a reference.', 'setProjectImageReference', 'validation_failed');
    }
    const fromChapter = body.fromChapter ?? null;
    const look = body.look?.trim().replace(/\s+/gu, ' ') || undefined;
    const state = gallery();
    if (body.replaces && !state.references.some((reference) => reference.projectId === projectId && reference.subjectKey === body.subjectKey
      && reference.fromChapter === fromChapter && reference.number === body.replaces)) {
      throw new MockApiError(404, 'Reference not found.', 'setProjectImageReference', 'not_found');
    }
    state.references = state.references.filter((reference) =>
      !(reference.projectId === projectId && reference.subjectKey === body.subjectKey && reference.fromChapter === fromChapter && (reference.number === number || reference.number === body.replaces)));
    state.references.push({ projectId, number, subjectKey: body.subjectKey, subjectName: body.subjectName, fromChapter, ...(look ? { look } : {}) });
    return view(image);
  }

  async function removeProjectImageReference(
    projectId: string, number: number, query: RemoveProjectImageReferenceQuery,
  ): Promise<OperationResponse<'removeProjectImageReference', '200'>> {
    context.applyOperation('removeProjectImageReference');
    context.projectFor(projectId, 'removeProjectImageReference', [...editorRoles]);
    const image = imageFor('removeProjectImageReference', projectId, number);
    const state = gallery();
    const fromChapter = query.fromChapter ?? null;
    const kept = state.references.filter((reference) => !(reference.projectId === projectId && reference.number === number &&
      reference.subjectKey === query.subject && reference.fromChapter === fromChapter));
    if (kept.length === state.references.length) {
      throw new MockApiError(404, 'Reference not found.', 'removeProjectImageReference', 'not_found');
    }
    state.references = kept;
    return view(image);
  }

  /** chooseProjectImageVariant (R21.5): K stays and is a plain drawing, the others go; a repeat answers the same. */
  async function chooseProjectImageVariant(
    projectId: string, number: number, options: RequiredIdempotencyOptions,
  ): Promise<OperationResponse<'chooseProjectImageVariant', '200'>> {
    context.applyOperation('chooseProjectImageVariant');
    context.projectFor(projectId, 'chooseProjectImageVariant', [...editorRoles]);
    return context.idempotentCommand('chooseProjectImageVariant', projectId, options, { number }, () => {
      let image: MockSagaImage;
      try {
        image = imageFor('chooseProjectImageVariant', projectId, number);
      } catch (error) {
        if (error instanceof MockApiError && error.code === 'illustrations.variant_not_chosen') {
          throw new MockApiError(409, 'Another variant was chosen.', 'chooseProjectImageVariant', 'conflict');
        }
        throw error;
      }
      const owner = gallery().drawings.find((item) => item.projectId === projectId && item.branch === image.branch);
      if (!image.variantGroup) {
        if (owner?.current === number) return view(image);
        throw new MockApiError(409, 'The drawing is no variant.', 'chooseProjectImageVariant', 'conflict');
      }
      if (image.status !== 'ready') throw new MockApiError(409, 'The variant is not ready.', 'chooseProjectImageVariant', 'conflict');
      // Nothing goes (specs/2026-11-05-drawing-one-screen.md D4.3): the others stay as versions, the spend with the one chosen.
      const group = gallery().images.filter((item) => item.projectId === projectId && item.variantGroup === image.variantGroup);
      for (const other of group) {
        delete other.variantGroup;
        delete other.variantPaid;
      }
      const chosen = imageFor('chooseProjectImageVariant', projectId, number);
      if (owner) owner.current = chosen.number;
      return view(chosen);
    });
  }

  /** Removes a drawing as the backend's deletion does: its references go, its edits keep no base (UC4). */
  function removeImage(projectId: string, image: MockSagaImage) {
    const state = gallery();
    state.images = state.images
      .filter((item) => !(item.projectId === projectId && item.number === image.number))
      .map((item) => (item.projectId === projectId && item.baseImage === image.number ? { ...item, baseImage: null } : item));
    state.references = state.references.filter((reference) => !(reference.projectId === projectId && reference.number === image.number));
    if (state.styleImages[projectId] === image.number) state.styleImages[projectId] = null;
  }

  async function deleteProjectImage(projectId: string, number: number): Promise<void> {
    context.applyOperation('deleteProjectImage');
    context.projectFor(projectId, 'deleteProjectImage');
    const image = imageFor('deleteProjectImage', projectId, number);
    if (!view(image).canDelete) throw new MockApiError(403, 'Only the author or the owner deletes a drawing.', 'deleteProjectImage', 'forbidden');
    removeImage(projectId, image);
  }

  /**
   * Drawings by number and whole branches at once (R14.10): an editor or above; each drawing goes by
   * the rule of a single deletion, the others stay with the reason; a replay of the key answers the
   * first result.
   */
  async function deleteProjectImages(
    projectId: string, body: OperationRequest<'deleteProjectImages'>, options: RequiredIdempotencyOptions,
  ): Promise<OperationResponse<'deleteProjectImages', '200'>> {
    context.applyOperation('deleteProjectImages');
    context.projectFor(projectId, 'deleteProjectImages', [...editorRoles]);
    const numbers = [...(body.numbers ?? [])].sort((left, right) => left - right);
    const branches = [...(body.branches ?? [])].sort((left, right) => left - right);
    const total = numbers.length + branches.length;
    if (total === 0 || total > 200 || new Set(numbers).size !== numbers.length || new Set(branches).size !== branches.length ||
      [...numbers, ...branches].some((number) => !Number.isInteger(number) || number < 1)) {
      throw new MockApiError(400, 'Name 1 to 200 drawings or branches, none twice.', 'deleteProjectImages', 'validation_failed');
    }
    return context.idempotentCommand('deleteProjectImages', projectId, options, { numbers, branches }, () => {
      const named = gallery().images
        .filter((image) => image.projectId === projectId && (numbers.includes(image.number) || branches.includes(image.branch)))
        .sort((left, right) => left.number - right.number);
      const deleted: number[] = [];
      const skipped: Array<{ number: number; reason: 'forbidden' | 'not_found' }> = [];
      for (const image of named) {
        if (view(image).canDelete) deleted.push(image.number);
        else skipped.push({ number: image.number, reason: 'forbidden' });
      }
      for (const number of numbers) if (!named.some((image) => image.number === number)) skipped.push({ number, reason: 'not_found' });
      for (const branch of branches) {
        if (!named.some((image) => image.branch === branch) && !numbers.includes(branch)) skipped.push({ number: branch, reason: 'not_found' });
      }
      for (const image of named) if (deleted.includes(image.number)) removeImage(projectId, image);
      return { deleted, skipped: skipped.sort((left, right) => left.number - right.number) };
    });
  }

  async function getProjectIllustrationStyle(projectId: string): Promise<OperationResponse<'getProjectIllustrationStyle', '200'>> {
    context.applyOperation('getProjectIllustrationStyle');
    context.projectFor(projectId, 'getProjectIllustrationStyle');
    return styleOf(projectId);
  }

  function styleOf(projectId: string) {
    return { style: gallery().styles[projectId] ?? null, styleImage: gallery().styleImages[projectId] ?? null };
  }

  async function updateProjectIllustrationStyle(
    projectId: string, body: OperationRequest<'updateProjectIllustrationStyle'>,
  ): Promise<OperationResponse<'updateProjectIllustrationStyle', '200'>> {
    context.applyOperation('updateProjectIllustrationStyle');
    context.projectFor(projectId, 'updateProjectIllustrationStyle', [...editorRoles]);
    if (body.styleImage !== undefined) {
      if (body.style || body.fromSketch !== undefined) {
        throw new MockApiError(400, 'The style sample changes alone.', 'updateProjectIllustrationStyle', 'validation_failed');
      }
      if (body.styleImage !== null && imageFor('updateProjectIllustrationStyle', projectId, body.styleImage).status !== 'ready') {
        throw new MockApiError(404, 'Image not found.', 'updateProjectIllustrationStyle', 'not_found');
      }
      if (body.styleImage !== null && imageFor('updateProjectIllustrationStyle', projectId, body.styleImage).variantGroup) {
        throw new MockApiError(409, 'The variant is not chosen yet.', 'updateProjectIllustrationStyle', 'illustrations.variant_pending');
      }
      gallery().styleImages[projectId] = body.styleImage;
      return styleOf(projectId);
    }
    if (body.fromSketch !== undefined) {
      const sketch = imageFor('updateProjectIllustrationStyle', projectId, body.fromSketch);
      if (body.style || !sketch.sketch || sketch.status !== 'ready' || !sketch.style || sketch.subjects.length === 0) {
        throw new MockApiError(400, 'Only a ready style sketch can be adopted.', 'updateProjectIllustrationStyle', 'validation_failed');
      }
      const [subject] = sketch.subjects;
      const state = gallery();
      state.styles[projectId] = sketch.style;
      state.styleImages[projectId] = sketch.number;
      state.references = state.references.filter((reference) =>
        !(reference.projectId === projectId && reference.subjectKey === subject!.key && reference.fromChapter === null));
      state.references.push({ projectId, number: sketch.number, subjectKey: subject!.key, subjectName: subject!.name, fromChapter: null });
      return styleOf(projectId);
    }
    const style = body.style?.trim() || null;
    if (style !== null && style.length > 500) {
      throw new MockApiError(400, 'Style is too long.', 'updateProjectIllustrationStyle', 'validation_failed');
    }
    gallery().styles[projectId] = style;
    return styleOf(projectId);
  }

  return {
    listProjectImages,
    getProjectImage,
    getProjectImageFile,
    setProjectImageReference,
    updateProjectImageDetails,
    removeProjectImageReference,
    chooseProjectImageVariant,
    deleteProjectImage,
    deleteProjectImages,
    getProjectIllustrationStyle,
    updateProjectIllustrationStyle,
  };
}
