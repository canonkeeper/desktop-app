import { describe, expect, it } from 'vitest';
import { MockApiError } from '../scenarios';
import { createMockApiClient } from '../repository';
import { projectId } from './fixtures';

const png = (name = 'Анна у окна.png', size = 3) => new File([new Uint8Array(size)], name, { type: 'image/png' });

async function refusal(promise: Promise<unknown>) {
  const error = await promise.then(() => null, (failure: unknown) => failure);
  expect(error).toBeInstanceOf(MockApiError);
  return error as MockApiError;
}

// specs/2026-10-29-saga-illustrations.md UC15: the mock keeps the author's own images as the
// backend does, so the web is built against the confirmation, the day's limit and the refusals.
describe('mock uploads of the author\'s own images', () => {
  it('asks the confirmation once, keeps the image as a ready upload and replays it', async () => {
    const api = createMockApiClient();
    const before = await api.getIllustrationAccess();
    expect(before.uploads).toEqual({ consentVersion: '2026-11-04', consented: false, today: { used: 0, limit: 30 } });
    expect((await refusal(api.uploadProjectImage(projectId, { file: png() }, { idempotencyKey: 'idem-upload-0' }))).code)
      .toBe('illustrations.upload_consent_required');
    expect((await refusal(api.uploadProjectImage(projectId, { file: png(), consentVersion: '2026-10-28' }, { idempotencyKey: 'idem-upload-0' }))).code)
      .toBe('illustrations.upload_consent_required');

    const image = await api.uploadProjectImage(projectId, { file: png(), consentVersion: '2026-11-04' }, { idempotencyKey: 'idem-upload-1' });
    // Without the author's caption it is «Моя картинка от <день>», not the name of the file (specs/2026-11-12-ux12-drawings.md DW4.1).
    expect(image).toMatchObject({ number: 3, kind: 'upload', status: 'ready', prompt: '', canDelete: true });
    expect(image.title).toMatch(/^Моя картинка от \d{1,2} [а-я]+$/u);
    expect(image.fileUrl).toContain('/images/3/file?size=full');
    expect(image.screenUrl).toContain('/images/3/file?size=screen');
    const replay = await api.uploadProjectImage(projectId, { file: png(), consentVersion: '2026-11-04' }, { idempotencyKey: 'idem-upload-1' });
    expect(replay.number).toBe(3);
    const second = await api.uploadProjectImage(projectId, { file: png('эскиз.png'), title: 'Эскиз замка' }, { idempotencyKey: 'idem-upload-2' });
    expect(second).toMatchObject({ number: 4, title: 'Эскиз замка' });
    expect((await api.getIllustrationAccess()).uploads).toEqual({ consentVersion: '2026-11-04', consented: true, today: { used: 2, limit: 30 } });
    const uploads = await api.listProjectImages(projectId, { kind: 'upload' });
    expect(uploads.data.map((item) => item.number)).toEqual([4, 3]);
  });

  // R15.8, R14.1: an upload is a branch of its own; a drawing made from it starts another branch.
  it('keeps an upload a branch of one version that the drawings made from it do not join', async () => {
    const api = createMockApiClient();
    const sketch = await api.uploadProjectImage(projectId, { file: png('Эскиз замка.png'), title: 'Эскиз замка', consentVersion: '2026-11-04' }, { idempotencyKey: 'idem-upload-b' });
    expect(sketch.branch).toBe(sketch.number);
    const castle = await api.agentDrawVersion(projectId, sketch.number, { instruction: 'Дорисуй замок' });
    expect(castle).toMatchObject({ kind: 'scene', title: 'Эскиз замка', baseImage: sketch.number, branch: castle.number });
    expect((await refusal(api.createProjectImageVariant(projectId, sketch.number, {}, { idempotencyKey: 'idem-variant-b' }))).status).toBe(400);
    const tiles = await api.listProjectImages(projectId, { group: 'branch', kind: 'upload' });
    expect(tiles.data.map((tile) => [tile.number, tile.versions])).toEqual([[sketch.number, 1]]);
    const deletion = await api.deleteProjectImages(projectId, { branches: [sketch.number] }, { idempotencyKey: 'idem-delete-b' });
    expect(deletion).toEqual({ deleted: [sketch.number], skipped: [] });
    expect((await api.getProjectImage(projectId, castle.number)).branch).toBe(castle.number);
  });

  it('refuses what is not a PNG, JPEG or WebP image of at most 20 MiB', async () => {
    const api = createMockApiClient();
    await api.uploadProjectImage(projectId, { file: png(), consentVersion: '2026-11-04' }, { idempotencyKey: 'idem-upload-ok' });
    expect((await refusal(api.uploadProjectImage(projectId, { file: new File(['gif'], 'кот.gif', { type: 'image/gif' }) }, { idempotencyKey: 'idem-upload-gif' }))).code)
      .toBe('illustrations.upload_unsupported');
    expect((await refusal(api.uploadProjectImage(projectId, { file: png('big.png', 20 * 1024 * 1024 + 1) }, { idempotencyKey: 'idem-upload-big' }))).code)
      .toBe('illustrations.upload_too_large');
  });

  // The chat takes no images (image-workshop IW7.2): the author's images go with a message to the helper of a drawing.
  it('carries the images of a message to the helper into its turn and the timeline, and refuses an image the gallery has not', async () => {
    const api = createMockApiClient();
    const image = await api.uploadProjectImage(projectId, { file: png(), consentVersion: '2026-11-04' }, { idempotencyKey: 'idem-upload-q' });
    const drawing = await api.createDrawing(projectId, { idea: 'Мара у пристани' }, { idempotencyKey: 'idem-drawing-q' });
    expect((await refusal(api.submitDrawingTurn(projectId, drawing.id, { body: 'Нарисуй её', images: [99] }, { idempotencyKey: 'idem-turn-q0' }))).status).toBe(404);
    const turn = await api.submitDrawingTurn(projectId, drawing.id, { body: 'Нарисуй её', images: [image.number] }, { idempotencyKey: 'idem-turn-q1' });
    const timeline = await api.getAgentTimeline(projectId, turn.jobId);
    expect(timeline.events[0]).toMatchObject({ kind: 'user', text: 'Нарисуй её', images: [image.number], cursor: 'tl_1' });
  });

  // R25.1–R25.3: the mock takes what the author chose as the server does; a question of the chat takes no choice,
  // as the chat draws nothing (specs/2026-11-02-image-workshop.md IW7.1).
  it('takes the images the author chose, within the limit', async () => {
    const api = createMockApiClient();
    const region = { x: 0.1, y: 0.1, w: 0.2, h: 0.2 };
    expect((await refusal(api.createProjectImageLayer(projectId, 2, { instruction: 'шляпа', region, references: [2] }, { idempotencyKey: 'idem-layer-r0' }))).status).toBe(400);
    expect((await refusal(api.createProjectImageLayer(projectId, 2, { instruction: 'шляпа', region, references: [1, 3, 4, 5, 6, 7, 8, 9, 10, 11] }, { idempotencyKey: 'idem-layer-r1' }))).status).toBe(400);
    expect((await refusal(api.createProjectImageLayer(projectId, 2, { instruction: 'шляпа', region, references: [99] }, { idempotencyKey: 'idem-layer-r2' }))).status).toBe(404);
    expect((await api.createProjectImageLayer(projectId, 2, { instruction: 'шляпа', region, references: [] }, { idempotencyKey: 'idem-layer-r3' })).layers).toHaveLength(1);
  });
});
