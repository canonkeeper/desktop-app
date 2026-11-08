import { describe, expect, it } from 'vitest';
import { createMockApiClient } from '../repository';
import { projectId } from './fixtures';

// specs/2026-10-29-saga-illustrations.md R8.1, R8.15, R8.10: the mock gallery lets the web be
// built against drawings that develop on later reads, layers that render the drawing again and
// drawing buttons that post into the «Иллюстрации» chat.
describe('mock saga gallery drawings', () => {
  // R19.2: the style sample is apart from the text, a ready image of the project, and goes with it.
  it('keeps the style sample apart from the text and clears it with the drawing', async () => {
    const api = createMockApiClient();
    expect(await api.updateProjectIllustrationStyle(projectId, { styleImage: 1 })).toEqual({ style: 'акварель, тёплый свет', styleImage: 1 });
    expect(await api.updateProjectIllustrationStyle(projectId, { style: 'гравюра' })).toEqual({ style: 'гравюра', styleImage: 1 });
    await expect(api.updateProjectIllustrationStyle(projectId, { styleImage: 99 })).rejects.toMatchObject({ status: 404 });
    await expect(api.updateProjectIllustrationStyle(projectId, { style: 'гравюра', styleImage: 2 })).rejects.toMatchObject({ status: 400 });
    expect(await api.updateProjectIllustrationStyle(projectId, { styleImage: null })).toEqual({ style: 'гравюра', styleImage: null });
    await api.updateProjectIllustrationStyle(projectId, { styleImage: 1 });
    await api.deleteProjectImage(projectId, 1);
    expect(await api.getProjectIllustrationStyle(projectId)).toEqual({ style: 'гравюра', styleImage: null });
  });

  it('develops a drawing and a layer phase by phase on reads', async () => {
    const api = createMockApiClient();
    const version = await api.cutoutProjectImage(projectId, 1, { idempotencyKey: 'idem-mock-cutout-1' });
    expect(version).toMatchObject({ status: 'drawing', phase: 'queued', baseImage: 1, partialUrl: null, fileUrl: null });
    const phases = [];
    for (let read = 0; read < 6; read += 1) {
      const image = await api.getProjectImage(projectId, version.number);
      phases.push(image.phase + (image.partialUrl ? ` ${image.partialUrl.split('frame=')[1]}` : ''));
    }
    expect(phases).toEqual(['reading', 'drawing 1', 'drawing 2', 'drawing 3', 'ready', 'ready']);
    const replay = await api.cutoutProjectImage(projectId, 1, { idempotencyKey: 'idem-mock-cutout-1' });
    expect(replay.number).toBe(version.number);

    const layered = await api.createProjectImageLayer(projectId, 1, {
      instruction: 'Сова вместо ворона.', region: { x: 0.5, y: 0.1, w: 0.25, h: 0.25 },
    }, { idempotencyKey: 'idem-mock-layer-1' });
    const layerId = layered.layers[0]!.id;
    expect(layered.layers[0]).toMatchObject({ status: 'drawing', phase: 'queued', visible: true, canDelete: true });
    let image = layered;
    for (let read = 0; read < 5; read += 1) image = await api.getProjectImage(projectId, 1);
    expect(image.layers[0]).toMatchObject({ status: 'ready', phase: 'ready' });
    expect(image.renderRevision).toBe(1);
    expect(image.fileUrl).toContain('v=1');
    image = await api.updateProjectImageLayer(projectId, 1, layerId, { visible: false });
    expect(image.renderRevision).toBe(2);
    expect((await api.getAccountQuota()).images.used).toBe(2);
    // specs/2026-11-05-drawing-one-screen.md D8.2, D8.5: «Свести слои» went; «До» is a file of its own the server makes.
    expect(api).not.toHaveProperty('flattenProjectImage');
    expect((await api.getProjectImageFile(projectId, 1, { size: 'before' })).mimeType).toBe('image/png');
  });

  // specs/2026-10-29-saga-illustrations.md R13.5–R13.7: under the provider's image limit a drawing
  // and a layer end draw_limited with its reset and give back the trial drawing and the day's place.
  it('ends a drawing and a layer draw_limited under the image limit and gives back what they spent', async () => {
    const api = createMockApiClient();
    const before = await api.getIllustrationAccess();
    api.setScenario({ ...api.getScenario(), drawingLimit: { retryAt: '2026-10-30T11:30:00.000Z' } });
    const variant = await api.createProjectImageVariant(projectId, 2, {}, { idempotencyKey: 'idem-mock-limit-1' });
    expect(variant).toMatchObject({ status: 'drawing', errorCode: null, retryAt: null });
    expect(variant).not.toHaveProperty('charge');
    expect(await api.getIllustrationAccess()).toMatchObject({ trialLeft: before.trialLeft - 1, images: { used: 1 } });
    expect((await api.getProjectImage(projectId, variant.number)).phase).toBe('reading');
    expect(await api.getProjectImage(projectId, variant.number)).toMatchObject({
      status: 'failed', phase: 'failed', errorCode: 'draw_limited', retryAt: '2026-10-30T11:30:00.000Z', fileUrl: null, partialUrl: null,
    });

    const layered = await api.createProjectImageLayer(projectId, 1, {
      instruction: 'Сова вместо ворона.', region: { x: 0.5, y: 0.1, w: 0.25, h: 0.25 },
    }, { idempotencyKey: 'idem-mock-limit-layer' });
    expect(layered.layers[0]).toMatchObject({ retryAt: null });
    expect(layered.layers[0]).not.toHaveProperty('charge');
    let image = layered;
    for (let read = 0; read < 3; read += 1) image = await api.getProjectImage(projectId, 1);
    expect(image.layers[0]).toMatchObject({ status: 'failed', errorCode: 'draw_limited', retryAt: '2026-10-30T11:30:00.000Z' });
    expect(image.renderRevision).toBe(0);
    expect(await api.getIllustrationAccess()).toMatchObject({ trialLeft: before.trialLeft, images: { used: 0 } });
    expect((await api.getAccountQuota()).images.used).toBe(0);
  });

  // specs/2026-10-29-saga-illustrations.md R12.2, R12.3, R12.5: a drawing or a layer that becomes
  // ready shows its check pending on one read and done on the next; an edit is checked against the
  // facts of its base; a hidden layer leaves the drawing without a check of what it shows now.
  it('checks a ready drawing and a laid layer against the facts', async () => {
    const api = createMockApiClient();
    const seeded = await api.getProjectImage(projectId, 2);
    expect(seeded.check).toMatchObject({ status: 'done', total: 3, ok: 2, contradicts: 1, defects: 1 });
    expect(seeded.check?.facts[1]).toMatchObject({ verdict: 'contradicts', region: { x: 0.58 } });
    const edit = await api.agentDrawVersion(projectId, 1, { instruction: 'Длиннее волосы.' });
    expect(edit.check).toBeUndefined();
    let image = edit;
    for (let read = 0; read < 5; read += 1) image = await api.getProjectImage(projectId, edit.number);
    expect(image.status).toBe('ready');
    expect(image.check).toMatchObject({ status: 'pending', total: 3, ok: 0 });
    expect(image.check?.facts[0]).toEqual({ text: 'волосы Мары рыжие, до плеч', verdict: null, note: null });
    image = await api.getProjectImage(projectId, edit.number);
    expect(image.check).toMatchObject({ status: 'done', total: 3, ok: 3 });

    const layered = await api.createProjectImageLayer(projectId, 1, {
      instruction: 'Синий плащ.', region: { x: 0.2, y: 0.4, w: 0.3, h: 0.3 },
    }, { idempotencyKey: 'idem-mock-check-layer' });
    image = layered;
    for (let read = 0; read < 5; read += 1) image = await api.getProjectImage(projectId, 1);
    expect(image.renderRevision).toBe(1);
    expect(image.check?.status).toBe('pending');
    expect(image.layers[0]!.check?.status).toBe('pending');
    image = await api.getProjectImage(projectId, 1);
    expect(image.check).toMatchObject({ status: 'done', ok: 3 });
    expect(image.layers[0]!.check).toMatchObject({ status: 'done', ok: 3 });
    image = await api.updateProjectImageLayer(projectId, 1, image.layers[0]!.id, { visible: false });
    expect(image.check).toBeUndefined();
  });

  it('draws four style sketches and adopts one as the saga style and the first reference', async () => {
    const api = createMockApiClient();
    const { data } = await api.createIllustrationStyleSketches(projectId, {}, { idempotencyKey: 'idem-mock-sketches-1' });
    expect(data).toHaveLength(4);
    expect(data.every((sketch) => sketch.sketch && sketch.style)).toBe(true);
    for (let read = 0; read < 5; read += 1) await api.listProjectImages(projectId);
    const style = await api.updateProjectIllustrationStyle(projectId, { fromSketch: data[1]!.number });
    expect(style.style).toBe(data[1]!.style);
    const adopted = await api.getProjectImage(projectId, data[1]!.number);
    expect(adopted.references).toEqual([{ subjectKey: 'мара', subjectName: 'Мара', fromChapter: null }]);
  });

  // specs/2026-11-02-image-workshop.md IW7.1, IW7.3: the chat draws nothing; a question that asks to draw is answered
  // with the link to the workshop and spends nothing.
  it('answers a question that asks to draw with the link to the workshop and draws nothing', async () => {
    const api = createMockApiClient();
    const before = (await api.listProjectImages(projectId, { limit: 50 })).data.length;
    const chat = await api.createChatSession(projectId, { sourceView: 'published', scope: 'project', title: 'Рисунок' }, { idempotencyKey: 'idem-mock-draw-chat' });
    const request = await api.createChatTurn(chat.id, { body: 'Нарисуй Мару на причале' }, { idempotencyKey: 'idem-mock-draw-turn' });
    expect((await api.listProjectImages(projectId, { limit: 50 })).data.length).toBe(before);
    expect((await api.getIllustrationAccess()).trialLeft).toBe(5);
    const events: Array<{ kind: string; text?: string; tool?: { name: string } }> = [];
    let cursor: string | null | undefined;
    do {
      const page = await api.getAgentTimeline(projectId, request.jobId, { cursor }) as unknown as {
        events: typeof events; nextCursor: string | null; eof: boolean;
      };
      events.push(...page.events);
      cursor = page.eof ? undefined : page.nextCursor;
    } while (cursor);
    expect(events.some((event) => event.tool?.name === 'draw')).toBe(false);
    expect(events.find((event) => event.kind === 'assistant')?.text).toContain('](drawing:new)');
  });

  // specs/2026-10-29-saga-illustrations.md R14.1, R14.3, R14.10, R14.11, image-workshop IW6.3: versions join the
  // branch of their drawing; the gallery by drawing shows its current version with the count of versions; one
  // deletion takes drawings and whole branches by the rule of a single deletion and replays its answer.
  it('keeps branches of drawings, lists them by branch and deletes many at once', async () => {
    const api = createMockApiClient();
    const ready = async (number: number) => {
      for (let read = 0; read < 5; read += 1) await api.getProjectImage(projectId, number);
    };
    const edit = await api.agentDrawVersion(projectId, 1, { instruction: 'Длиннее волосы.' });
    await ready(edit.number);
    const variant = await api.createProjectImageVariant(projectId, 1, {}, { idempotencyKey: 'idem-mock-branch-variant' });
    expect([edit.branch, variant.branch, variant.baseImage]).toEqual([1, 1, null]);
    const tiles = await api.listProjectImages(projectId, { group: 'branch' });
    expect(tiles.data.map((tile) => [tile.number, tile.branch, tile.versions])).toEqual([[1, 1, 3], [2, 2, 1]]);
    await ready(variant.number);
    expect((await api.listProjectImages(projectId, { group: 'branch', references: true })).data.map((tile) => tile.number)).toEqual([1]);
    expect((await api.listProjectImages(projectId, { branch: 1 })).data.map((image) => image.number)).toEqual([variant.number, edit.number, 1]);
    expect((await api.listProjectImages(projectId, { group: 'branch', limit: 1 })).meta).toEqual({ hasMore: true, nextCursor: String(variant.number) });

    const deletion = await api.deleteProjectImages(projectId, { branches: [1], numbers: [99] }, { idempotencyKey: 'idem-mock-delete-many' });
    expect(deletion).toEqual({ deleted: [1, edit.number, variant.number], skipped: [{ number: 99, reason: 'not_found' }] });
    expect(await api.deleteProjectImages(projectId, { numbers: [99], branches: [1] }, { idempotencyKey: 'idem-mock-delete-many' })).toEqual(deletion);
    expect((await api.listProjectImages(projectId, { group: 'branch' })).data.map((tile) => tile.number)).toEqual([2]);
    await expect(api.deleteProjectImages(projectId, { numbers: [] }, { idempotencyKey: 'idem-mock-delete-none' })).rejects.toMatchObject({ status: 400 });
  });
});

// specs/2026-10-29-saga-illustrations.md R17.1–R17.4, R17.11, R17.12: the mock enlarges as the backend does.
describe('mock enlargement', () => {
  it('starts a version of the branch, counts its tiles and comes out at twice the size', async () => {
    const api = createMockApiClient();
    const before = await api.getIllustrationAccess();
    const version = await api.enlargeProjectImage(projectId, 2, { idempotencyKey: 'idem-mock-enlarge' });
    expect(version.prompt).toBe('Увеличено ×2');
    expect(version).toMatchObject({ enlarged: true, baseImage: 2, branch: 2, status: 'drawing', enlargeProgress: { done: 0, total: 6 } });
    expect(await api.enlargeProjectImage(projectId, 2, { idempotencyKey: 'idem-mock-enlarge' })).toMatchObject({ number: version.number });
    expect(await api.getIllustrationAccess()).toMatchObject({ trialLeft: before.trialLeft - 1 });
    const seen: string[] = [];
    let image = version;
    for (let read = 0; read < 12 && image.status === 'drawing'; read += 1) {
      image = await api.getProjectImage(projectId, version.number);
      if (image.enlargeProgress) seen.push(`${image.phase} ${image.enlargeProgress.done}`);
    }
    expect(seen).toEqual(['reading 0', 'drawing 0', 'drawing 1', 'drawing 2', 'drawing 3', 'drawing 4', 'drawing 5', 'drawing 6']);
    expect(image).toMatchObject({ status: 'ready', width: 3072, height: 2048, enlarged: true });
    expect(image).not.toHaveProperty('enlargeProgress');
    expect(image.check?.status).not.toBe('pending');
    await expect(api.enlargeProjectImage(projectId, image.number, { idempotencyKey: 'idem-mock-enlarge-twice' })).rejects.toMatchObject({ status: 400 });
    const again = await api.createProjectImageVariant(projectId, image.number, {}, { idempotencyKey: 'idem-mock-enlarge-variant' });
    expect(again).toMatchObject({ enlarged: true, baseImage: 2, branch: 2, enlargeProgress: { done: 0, total: 6 } });
  });
});

// specs/2026-10-29-saga-illustrations.md R26.3–R26.5: the mock answers «Перегенерировать» as the backend does.
describe('a regeneration of the mock gallery', () => {
  it('takes what the author says, keeps the description and refuses what cannot be drawn', async () => {
    const api = createMockApiClient();
    const original = await api.getProjectImage(projectId, 2);
    const image = await api.createProjectImageVariant(projectId, 2, { note: '  без \n шлема ', aspect: 'portrait', references: [1] }, { idempotencyKey: 'idem-mock-regenerate-1' });
    expect(image).toMatchObject({ regenerateNote: 'без шлема', aspect: 'portrait', referenceImages: [1], prompt: original.prompt, branch: original.branch });
    await expect(api.createProjectImageVariant(projectId, 2, { note: 'другое' }, { idempotencyKey: 'idem-mock-regenerate-1' })).rejects.toMatchObject({ status: 409 });
    for (const body of [{ note: 'я'.repeat(4001) },{ references: [2] }, { references: [1, 1] }, { references: [1, 3, 4, 5, 6, 7, 8, 9, 10, 11] }]) {
      await expect(api.createProjectImageVariant(projectId, 2, body, { idempotencyKey: 'idem-mock-regenerate-2' })).rejects.toMatchObject({ status: 400 });
    }
    const plain = await api.createProjectImageVariant(projectId, 2, {}, { idempotencyKey: 'idem-mock-regenerate-3' });
    expect(plain).toMatchObject({ aspect: original.aspect, referenceImages: original.referenceImages });
    expect(plain).not.toHaveProperty('regenerateNote');
  });
});

// specs/2026-10-29-saga-illustrations.md R16.6, R16.7, R18.3, R24.2: the mock answers as the backend does.
// specs/2026-11-03-workshop-helpers-steering.md S3.4: a regeneration of 2–4 variants is a group by as many stagings of
// the brief of the drawing, as the backend draws it.
describe('a regeneration by variants in the mock', () => {
  it('draws a group by the stagings of the brief and refuses another count', async () => {
    const api = createMockApiClient();
    const pier = '00000000-0000-4000-9000-000000000002';
    await expect(api.createProjectImageVariant(projectId, 2, { variants: 3 }, { idempotencyKey: 'idem-regroup-none' }))
      .rejects.toMatchObject({ status: 400 });
    const drawing = await api.getDrawing(projectId, pier);
    await api.updateDrawing(projectId, pier, { brief: { ...drawing.brief, staging: ['общий план', 'крупный план', 'вид сверху'] }, expectedRevision: drawing.revision });
    await expect(api.createProjectImageVariant(projectId, 2, { variants: 2 }, { idempotencyKey: 'idem-regroup-two' }))
      .rejects.toMatchObject({ status: 400 });
    const first = await api.createProjectImageVariant(projectId, 2, { variants: 3 }, { idempotencyKey: 'idem-regroup-three' });
    expect(first.variantGroup).toBeTruthy();
    expect(first.variantNumbers).toHaveLength(3);
    const waiting = await api.getDrawing(projectId, pier);
    expect(waiting.variants).toBe(3);
    expect(waiting.current?.number).toBe(2);
    await expect(api.createProjectImageVariant(projectId, 2, { variants: 3 }, { idempotencyKey: 'idem-regroup-again' }))
      .rejects.toMatchObject({ status: 409, problem: { code: 'illustrations.variant_pending' } });
  });
});

describe('the frames and cutouts of the mock', () => {
  it('cuts the background away in a version of one spend whose replay answers the first, and takes no frame for a hero', async () => {
    const api = createMockApiClient();
    const before = (await api.getIllustrationAccess()).trialLeft;
    const cutout = await api.cutoutProjectImage(projectId, 2, { idempotencyKey: 'idem-cut' });
    expect(cutout).toMatchObject({ transparent: true, baseImage: 2, branch: 2, aspect: 'landscape', status: 'drawing' });
    expect(cutout.prompt).toMatch(/^Вырежи .+без фона; лицо, позу и цвета не меняй\.$/u);
    expect((await api.cutoutProjectImage(projectId, 2, { idempotencyKey: 'idem-cut' })).number).toBe(cutout.number);
    expect((await api.getIllustrationAccess()).trialLeft).toBe(before - 1);
    expect((await api.listProjectImages(projectId, { branch: 2 })).data.map((image) => image.number)).toEqual([cutout.number, 2]);
  });

  it('draws the version of the agent in the frame asked for, and in that of the drawing without one', async () => {
    const api = createMockApiClient();
    expect((await api.agentDrawVersion(projectId, 2, { instruction: 'шире', aspect: 'wide' })).aspect).toBe('wide');
    expect((await api.agentDrawVersion(projectId, 2, { instruction: 'шрам' })).aspect).toBe('landscape');
  });

  // specs/2026-10-29-saga-illustrations.md R21.3–R21.8, image-workshop IW6.3 and specs/2026-11-05-drawing-one-screen.md
  // D4.3, D4.10: the mock keeps what the backend keeps — the choice deletes nothing, the gallery shows one tile a branch.
  it('draws variants as one start and one spend, keeps them out of the gallery and closes the group by the choice', async () => {
    const api = createMockApiClient();
    const before = (await api.getIllustrationAccess()).trialLeft;
    const first = await api.agentDrawVersion(projectId, 1, { instruction: 'ночь', variants: 3 });
    const numbers = first.variantNumbers!;
    expect(numbers.length).toBe(3);
    expect((await api.getIllustrationAccess()).trialLeft).toBe(before - 1);
    const flat = await api.listProjectImages(projectId, {});
    expect(flat.data.filter((image) => image.variantGroup).length).toBe(0);
    const branches = await api.listProjectImages(projectId, { group: 'branch' });
    expect(branches.data.find((image) => image.branch === first.branch)?.versions).toBe(1);
    await expect(api.chooseProjectImageVariant(projectId, numbers[1]!, { idempotencyKey: 'idem-mock-early' })).rejects.toMatchObject({ status: 409 });
    for (const number of numbers) for (let read = 0; read < 7; read += 1) await api.getProjectImage(projectId, number);
    await expect(api.cutoutProjectImage(projectId, numbers[1]!, { idempotencyKey: 'idem-mock-base' })).rejects.toMatchObject({ status: 409, problem: { code: 'illustrations.variant_pending' } });
    const chosen = await api.chooseProjectImageVariant(projectId, numbers[1]!, { idempotencyKey: 'idem-mock-choice' });
    expect(chosen.variantGroup).toBeUndefined();
    expect(chosen.unchosenVariants).toBeUndefined();
    for (const number of [numbers[0]!, numbers[2]!]) {
      const kept = await api.getProjectImage(projectId, number);
      expect(kept).toMatchObject({ number, status: 'ready', branch: first.branch });
      expect(kept.variantGroup).toBeUndefined();
    }
    // One spend for the group, whatever was chosen.
    expect((await api.getIllustrationAccess()).trialLeft).toBe(before - 1);
    // The gallery still has one tile for the branch: the chosen picture, with every version counted.
    const tiles = (await api.listProjectImages(projectId, { group: 'branch' })).data.filter((image) => image.branch === first.branch);
    expect(tiles.map((tile) => [tile.number, tile.versions])).toEqual([[numbers[1], 4]]);
    expect((await api.chooseProjectImageVariant(projectId, numbers[1]!, { idempotencyKey: 'idem-mock-repeat' })).number).toBe(numbers[1]);
    await expect(api.chooseProjectImageVariant(projectId, numbers[2]!, { idempotencyKey: 'idem-mock-other' })).rejects.toMatchObject({ status: 409 });
    // As chooseVariantInTx: the current version of a drawing answers the same, whether it ever was a variant or not.
    expect((await api.chooseProjectImageVariant(projectId, 2, { idempotencyKey: 'idem-mock-plain' })).number).toBe(2);
  });
});
