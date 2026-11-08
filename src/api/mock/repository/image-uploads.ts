import type { ImageUploadInput, ImportBookRequestOptions, OperationResponse } from '../../../shared/api';
import { MockApiError } from '../scenarios';
import type { MockRepositoryContext } from './context';
import type { MockSagaImage } from './gallery-state';
import { createGalleryView, editorRoles } from './gallery-view';
import { createIllustrationAccess, mockUploadEdition } from './illustration-access';
import { mockDefault } from './quota';

const acceptedTypes = new Set(['image/png', 'image/jpeg', 'image/webp']);
const maxUploadBytes = 20 * 1024 * 1024;

/** The caption of an upload: the author's, else the file name without its extension (R15.1). */
export function mockUploadTitle(title: string | undefined, fileName: string) {
  const clean = (value: string) => value.replace(/\s+/gu, ' ').trim();
  const own = clean(title ?? '');
  if (own && [...own].length <= 80) return own;
  const name = clean(fileName.replace(/^.*[\\/]/u, '').replace(/\.[^.]*$/u, ''));
  return name ? [...name].slice(0, 80).join('') : 'Изображение автора';
}

/**
 * uploadProjectImage of the mock API (specs/2026-10-29-saga-illustrations.md UC15): the switch, an
 * editor with the right to draw, the confirmation of the current edition, image_uploads_per_day and
 * the file's type and size; the mock cannot decode the file, so the item is a square of 1024 px.
 */
export function createImageUploadOperations(context: MockRepositoryContext) {
  const { gallery, view } = createGalleryView(context);
  const access = createIllustrationAccess(context);

  async function uploadProjectImage(
    projectId: string, input: ImageUploadInput, options: ImportBookRequestOptions,
  ): Promise<OperationResponse<'uploadProjectImage', '201'>> {
    const operationId = 'uploadProjectImage';
    context.applyOperation(operationId);
    context.projectFor(projectId, operationId, [...editorRoles]);
    const state = gallery();
    if (!state.uploads.enabled) throw new MockApiError(403, 'uploads of images are switched off', operationId, 'illustrations.uploads_off');
    access.requireDrawingRight(operationId);
    const userId = context.currentPrincipalId();
    const confirm = input.consentVersion?.trim();
    if (confirm ? confirm !== mockUploadEdition : state.uploads.consents[userId] !== mockUploadEdition) {
      throw new MockApiError(409, 'the author has not confirmed the rights to his images', operationId, 'illustrations.upload_consent_required');
    }
    if (input.file.size > maxUploadBytes) throw new MockApiError(413, 'the image is larger than 20 MiB', operationId, 'illustrations.upload_too_large');
    if (!acceptedTypes.has(input.file.type)) throw new MockApiError(415, 'the file is not a PNG, JPEG or WebP image', operationId, 'illustrations.upload_unsupported');
    const title = mockUploadTitle(input.title, input.file.name);
    return context.idempotentCommand(operationId, projectId, options, {
      name: input.file.name, size: input.file.size, title, consent: confirm ?? null,
    }, () => {
      if (access.uploadsToday(userId) >= mockDefault(context.state.quotaDefaults, 'image_uploads_per_day')) {
        throw new MockApiError(429, 'Daily upload limit is used up.', operationId, 'quota.image_uploads_daily');
      }
      if (confirm) state.uploads.consents[userId] = confirm;
      const number = (state.lastNumber[projectId] ?? 0) + 1;
      state.lastNumber[projectId] = number;
      const image: MockSagaImage = {
        projectId, createdBy: userId, number, branch: number, status: 'ready', kind: 'upload', aspect: 'square', prompt: '', title,
        revisedPrompt: null, style: null, chapter: null, subjects: [], baseImage: null, referenceImages: [], chatId: null,
        width: 1024, height: 1024, errorCode: null, errorMessage: null, retryAt: null, createdAt: new Date().toISOString(),
        readyAt: new Date().toISOString(), phase: 'ready', partialFrame: 0, sources: [], sketch: false, renderRevision: 0, layers: [],
      };
      state.images.push(image);
      return view(image);
    });
  }

  return { uploadProjectImage };
}
