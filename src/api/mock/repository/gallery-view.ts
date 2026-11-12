import type { Schema } from '../../../shared/api';
import { MockApiError, type MockScenarioControl } from '../scenarios';
import { clone, now } from './common';
import type { MockRepositoryContext } from './context';
import { mockAspectSize, pendingCheck, settledCheck, type MockImageLayer, type MockSagaImage } from './gallery-state';
import { placesOf } from './places';

type SagaImage = Schema<'SagaImage'>;

export const editorRoles = ['editor', 'admin', 'owner'] as const;

/** The tiles of an enlarged version by its shape, as the backend cuts it (R17.5). */
function enlargeTiles(aspect: MockSagaImage['aspect']) {
  if (aspect === 'panorama') return 8;
  return aspect === 'banner' ? 10 : 6;
}

function fileAddress(image: MockSagaImage, query: string) {
  return `/api/v1/projects/${image.projectId}/images/${image.number}/file?${query}`;
}

/**
 * The mock drawing Job: every read of a drawing or a layer still drawn moves it one step —
 * queued, reading, three draft frames, ready (specs/2026-10-29-saga-illustrations.md R8.1) — so the
 * web sees the drawing develop by polling, as against the backend. Under the scenario's image limit
 * the provider answers the drawing request with it: the item ends draw_limited with its reset (R13.5).
 */
function step(item: MockSagaImage | MockImageLayer, limit: MockScenarioControl['drawingLimit'], lastFrame = 3) {
  if (item.status !== 'drawing') return false;
  // A drawing put off until the service of drawings is free waits for its time (specs/2026-11-12-ux10-drawing-ops.md DO2.1).
  if ('startsAfter' in item && item.startsAfter) {
    if (Date.parse(item.startsAfter) > Date.now()) return false;
    delete item.startsAfter;
    // Its work begins now, as the server's first claim of its Job (specs/2026-11-12-ux12-drawings.md DW1.2).
    item.startedAt = new Date().toISOString();
  }
  if (item.phase === 'queued') {
    item.phase = 'reading';
  } else if (item.phase === 'reading' && limit) {
    item.status = 'failed';
    item.phase = 'failed';
    item.errorCode = 'draw_limited';
    item.errorMessage = 'Лимит рисунков провайдера исчерпан.';
    item.retryAt = limit.retryAt;
  } else if (item.phase === 'reading') {
    item.phase = 'drawing';
    item.partialFrame = 1;
  } else if (item.partialFrame < lastFrame) {
    item.partialFrame += 1;
  } else {
    item.status = 'ready';
    item.phase = 'ready';
    item.partialFrame = 0;
    item.readyAt = now;
    return true;
  }
  return false;
}

export function createGalleryView(context: MockRepositoryContext) {
  const gallery = () => context.state.gallery;

  /**
   * A render checked on one read is settled on the next (R12.2): a drawing or a layer that becomes
   * ready shows its check pending first, then done with every fact seen.
   */
  function advance(image: MockSagaImage) {
    const limit = context.scenario.drawingLimit;
    if (image.check) image.check = settledCheck(image.check);
    for (const layer of image.layers) {
      if (layer.check) layer.check = settledCheck(layer.check);
    }
    if (step(image, limit, image.enlarged ? enlargeTiles(image.aspect) + 1 : 3)) {
      if (image.enlarged) finishEnlarged(image);
      else {
        [image.width, image.height] = mockAspectSize[image.aspect];
        image.check = pendingCheck(image.facts ?? []);
        image.checkRevision = image.renderRevision;
      }
    }
    giveBack(image);
    dropCurrent(image);
    for (const layer of image.layers) {
      if (step(layer, limit) && layer.visible) {
        image.renderRevision += 1;
        layer.check = pendingCheck(layer.facts ?? []);
        image.check = pendingCheck(layer.facts ?? []);
        image.checkRevision = image.renderRevision;
      }
      giveBack(layer);
    }
  }

  /**
   * A version that ended without being drawn stops being the current one of its drawing, as its Job ends it on the
   * backend: the latest ready version without a group takes its place, otherwise none (image-workshop IW5.2, I2, I3).
   */
  function dropCurrent(image: MockSagaImage) {
    if (image.status !== 'failed' && image.status !== 'refused') return;
    const drawing = gallery().drawings.find((item) => item.projectId === image.projectId && item.branch === image.branch && item.current === image.number);
    if (!drawing) return;
    const ready = gallery().images.filter((item) => item.projectId === image.projectId && item.branch === image.branch && item.status === 'ready' && !item.variantGroup && item.kind !== 'upload');
    drawing.current = ready.reduce<number | null>((latest, item) => (latest === null || item.number > latest ? item.number : latest), null);
  }

  /** A version enlarged is ready at twice the size with the check of its base: no critic looks at it (R17.11). */
  function finishEnlarged(image: MockSagaImage) {
    image.width = image.enlargedSize?.width ?? image.width;
    image.height = image.enlargedSize?.height ?? image.height;
    const base = gallery().images.find((item) => item.projectId === image.projectId && item.number === image.baseImage);
    if (base?.check && base.checkRevision === base.renderRevision) {
      image.check = settledCheck(base.check);
      image.checkRevision = image.renderRevision;
    }
  }

  /**
   * A drawing or a layer stopped by the provider's image limit gives its author back the trial
   * drawing its start spent, once; its place in images_per_day is no longer counted (R13.6).
   */
  function giveBack(item: MockSagaImage | MockImageLayer) {
    if (item.status !== 'failed' || item.errorCode !== 'draw_limited' || !item.charge) return;
    if (item.charge === 'trial') context.state.trialImagesUsed[item.createdBy] = Math.max(0, (context.state.trialImagesUsed[item.createdBy] ?? 0) - 1);
    item.charge = null;
  }

  function layerView(image: MockSagaImage, layer: MockImageLayer, role: string): Schema<'SagaImageLayer'> {
    const { createdBy, partialFrame, facts: _facts, charge: _charge, ...rest } = layer;
    return {
      ...rest,
      partialUrl: layer.status === 'drawing' && partialFrame > 0 ? fileAddress(image, `size=partial&layer=${layer.id}&frame=${partialFrame}`) : null,
      canDelete: role === 'owner' || (createdBy === context.currentPrincipalId() && editorRoles.includes(role as typeof editorRoles[number])),
    };
  }

  function drawingOf(image: MockSagaImage) {
    const drawing = gallery().drawings.find((item) => item.projectId === image.projectId && item.branch !== null && item.branch === image.branch);
    return drawing ? { drawingId: drawing.id } : {};
  }

  function view(image: MockSagaImage): SagaImage {
    const { projectId, createdBy, partialFrame, layers, turnId: _turnId, facts: _facts, check, checkRevision, charge: _charge, enlargedSize: _enlargedSize, variantPaid: _variantPaid, freeSketch: _freeSketch, ...rest } = image;
    const role = context.actorRole(projectId);
    const ready = image.status === 'ready';
    // The check of an older render is not shown, as the backend's (R12.3).
    const current = check && checkRevision === image.renderRevision ? { check } : {};
    const references = gallery().references.filter((reference) => reference.projectId === projectId && reference.number === image.number);
    // Beside the keys of the drawing stand the places of their chapters, as the backend writes them (ux1-names N3.1).
    const places = placesOf(context, projectId, [image.chapter, ...image.sources.map((source) => source.split('/')[0]), ...references.map((reference) => reference.fromChapter)]);
    return clone({
      ...rest,
      ...current,
      ...(places ? { places } : {}),
      ...(image.kind === 'item' ? { transparent: true } : {}),
      references: references.map(({ fromChapter, look, subjectKey, subjectName }) => ({
        subjectKey, subjectName, fromChapter, ...(fromChapter && places?.[fromChapter] ? { fromPlace: places[fromChapter] } : {}), ...(look ? { look } : {}),
      })),
      canDelete: role === 'owner' || (createdBy === context.currentPrincipalId() && editorRoles.includes(role as typeof editorRoles[number])),
      partialUrl: image.status === 'drawing' && partialFrame > 0 ? fileAddress(image, `size=partial&frame=${partialFrame}`) : null,
      ...(image.enlarged && image.status === 'drawing' ? { enlargeProgress: { done: Math.max(0, partialFrame - 1), total: enlargeTiles(image.aspect) } } : {}),
      fileUrl: ready ? fileAddress(image, `size=full&v=${image.renderRevision}`) : null,
      screenUrl: ready ? fileAddress(image, `size=screen&v=${image.renderRevision}`) : null,
      thumbUrl: ready ? fileAddress(image, `size=thumb&v=${image.renderRevision}`) : null,
      layers: layers.map((layer) => layerView(image, layer, role)),
      // The drawing of the workshop the version belongs to (image-workshop IW6.1).
      ...drawingOf(image),
      ...(image.variantGroup ? {
        variantNumbers: gallery().images.filter((item) => item.projectId === projectId && item.variantGroup === image.variantGroup).map((item) => item.number).sort((left, right) => left - right),
      } : {}),
    });
  }

  function imageFor(operationId: Parameters<MockRepositoryContext['projectFor']>[1], projectId: string, number: number) {
    const image = gallery().images.find((item) => item.projectId === projectId && item.number === number);
    if (!image) {
      const chosen = gallery().images.some((item) => item.projectId === projectId && item.unchosenVariants?.includes(number));
      throw chosen ? new MockApiError(404, 'The variant was not chosen.', operationId, 'illustrations.variant_not_chosen') : new MockApiError(404, 'Image not found.', operationId, 'not_found');
    }
    return image;
  }

  return { gallery, advance, view, imageFor };
}
