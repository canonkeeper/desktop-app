import { describe, expect, it } from 'vitest';
import { createMockApiClient } from '../repository';
import { projectId } from './fixtures';

type Api = ReturnType<typeof createMockApiClient>;

async function readUntilReady(api: Api, numbers: readonly number[]) {
  for (let read = 0; read < 6; read += 1) for (const number of numbers) await api.getProjectImage(projectId, number);
}

// specs/2026-11-02-image-workshop.md IW1, IW5 and specs/2026-11-05-drawing-one-screen.md D1.5, D3.7, D4.3, D4.10, D10:
// the mock workshop lets the web be built against drawings as objects — a new one from the author's words, «Нарисовать»
// by the brief with variants waiting for the choice, a choice that deletes nothing, the version chosen by click.
describe('mock drawing workshop', () => {
  it('draws a drawing by its brief, keeps every variant after the choice and chooses a version', async () => {
    const api = createMockApiClient();
    const listed = await api.listDrawings(projectId);
    expect(listed.data.map((drawing) => drawing.current?.number)).toEqual([1, 2]);
    // D10.2: no «Готово» anywhere, the stage goes by the variants, the current version and the description.
    for (const summary of listed.data) {
      expect(summary).not.toHaveProperty('finished');
      expect(summary).not.toHaveProperty('finishedAt');
      expect(summary).toMatchObject({ stage: 'refine', activity: null });
      expect(summary.cover?.number).toBe(summary.current?.number);
    }

    let drawing = await api.createDrawing(projectId, { idea: 'Дракон с двумя рогами на скале', kind: 'character', selfDescribed: true }, { idempotencyKey: 'idem-mock-drawing-1' });
    expect(drawing).toMatchObject({ stage: 'describe', revision: 1, current: null, cover: null, activity: null, inventedShare: 0 });
    expect(drawing.brief.segments).toEqual([{ text: 'Дракон с двумя рогами на скале', origin: 'author' }]);

    drawing = await api.updateDrawing(projectId, drawing.id, {
      brief: { ...drawing.brief, segments: [...drawing.brief.segments, { text: 'на закате', origin: 'helper' }], staging: ['крупно', 'издалека'] },
      expectedRevision: 1,
    });
    expect(drawing.revision).toBe(2);
    expect(drawing.inventedShare).toBeGreaterThan(0);
    await expect(api.drawDrawing(projectId, drawing.id, { variants: 2, expectedRevision: 1 }, { idempotencyKey: 'idem-mock-draw-stale' }))
      .rejects.toMatchObject({ status: 409, code: 'drawing.brief_changed' });
    // specs/2026-11-11-ux6-drawing-flow.md DF3.3: three variants by two angles — the author's two and one the server picks.
    drawing = await api.drawDrawing(projectId, drawing.id, { variants: 3, expectedRevision: 2 }, { idempotencyKey: 'idem-mock-draw-3' });
    expect(drawing).toMatchObject({ stage: 'variants', variants: 3, current: null, activity: 'working' });
    expect(drawing.variantImages.map((image) => image.variantPrompt)).toEqual(['крупно', 'издалека', 'общий план: место и герои целиком']);
    const [first, second, third] = drawing.variantImages;
    // D1.5: a drawing with variants and no current version still has a picture on its tile.
    expect(drawing.cover?.number).toBe(first!.number);
    await readUntilReady(api, [first!.number, second!.number, third!.number]);
    const waiting = (await api.listDrawings(projectId)).data.find((item) => item.id === drawing.id)!;
    expect(waiting).toMatchObject({ activity: 'choosing', current: null });
    expect(waiting.cover).toMatchObject({ number: first!.number, status: 'ready' });
    await expect(api.drawDrawing(projectId, drawing.id, { variants: 1, expectedRevision: 2 }, { idempotencyKey: 'idem-mock-draw-waiting' }))
      .rejects.toMatchObject({ status: 409, code: 'illustrations.variant_pending' });
    await expect(api.updateDrawing(projectId, drawing.id, { current: first!.number }))
      .rejects.toMatchObject({ status: 409, code: 'illustrations.variant_pending' });

    await api.chooseProjectImageVariant(projectId, second!.number, { idempotencyKey: 'idem-mock-choose-2' });
    drawing = await api.getDrawing(projectId, drawing.id);
    expect(drawing).toMatchObject({ stage: 'refine', variants: 0, activity: null });
    expect(drawing.current?.number).toBe(second!.number);
    expect(drawing.current?.drawingId).toBe(drawing.id);
    // D4.3: the choice deletes nothing — the other variant is a version of the same drawing.
    expect([...drawing.versions].sort((left, right) => left - right)).toEqual([first!.number, second!.number, third!.number]);
    const other = await api.getProjectImage(projectId, first!.number);
    expect(other).toMatchObject({ status: 'ready', drawingId: drawing.id });
    expect(other.variantGroup).toBeUndefined();
    // D4.10: a repeat of the choice answers the same; the other variant of the closed group is a conflict.
    expect((await api.chooseProjectImageVariant(projectId, second!.number, { idempotencyKey: 'idem-mock-choose-2-again' })).number).toBe(second!.number);
    await expect(api.chooseProjectImageVariant(projectId, first!.number, { idempotencyKey: 'idem-mock-choose-1-late' })).rejects.toMatchObject({ status: 409 });

    // D4.4: «Выбрать этот» of a version.
    drawing = await api.updateDrawing(projectId, drawing.id, { current: first!.number });
    expect(drawing.current?.number).toBe(first!.number);

    drawing = await api.drawDrawing(projectId, drawing.id, { variants: 1, expectedRevision: 2 }, { idempotencyKey: 'idem-mock-draw-again' });
    expect(drawing.current?.number).toBe(first!.number);
    expect(drawing.versions).toHaveLength(4);
    await api.deleteDrawing(projectId, drawing.id);
    await expect(api.getDrawing(projectId, drawing.id)).rejects.toMatchObject({ status: 404 });
    await expect(api.getProjectImage(projectId, first!.number)).rejects.toMatchObject({ status: 404 });
    await expect(api.getProjectImage(projectId, second!.number)).rejects.toMatchObject({ status: 404 });
  });

  // D3.7: «Нарисовать» with the helper is one request; the drawing starts by itself when the turn ends.
  it('starts the drawing by the stagings once the turn asked to draw ends', async () => {
    const api = createMockApiClient();
    const drawing = await api.createDrawing(projectId, { idea: 'Мара на причале' }, { idempotencyKey: 'idem-mock-draw-turn' });
    await api.submitDrawingTurn(projectId, drawing.id, { body: 'Подготовь описание для художника по моему замыслу.', draw: true }, { idempotencyKey: 'idem-mock-draw-turn-1' });
    const first = await api.getDrawing(projectId, drawing.id);
    expect(first).toMatchObject({ stage: 'describe', variants: 0, activity: 'working' });
    const started = await api.getDrawing(projectId, drawing.id);
    expect(started.variants).toBe(started.brief.staging.length);
    expect(started.variantImages).toHaveLength(3);

    const plain = await api.createDrawing(projectId, { idea: 'Кира у ворот' }, { idempotencyKey: 'idem-mock-plain-turn' });
    await api.submitDrawingTurn(projectId, plain.id, { body: 'Какие у Киры волосы?' }, { idempotencyKey: 'idem-mock-plain-turn-1' });
    for (let read = 0; read < 3; read += 1) expect((await api.getDrawing(projectId, plain.id)).variantImages).toEqual([]);
  });
});

// specs/2026-11-10-drawings-list-server.md DL1, DL2, DL3.1: the mock narrows the list and counts it by the rule of the server —
// every word of the search in the caption, the idea, a name in the frame or the place of the chapter; a name and a chapter;
// the states as the tiles say them; the looks; the order of the chapters of the saga; the counts within the search and every
// name and chapter there is to narrow by.
describe('mock list of drawings narrowed and counted', () => {
  async function dragon(api: Api) {
    const drawing = await api.createDrawing(projectId, {
      idea: 'Дракон над гаванью', kind: 'character', chapter: 'b2c12', subjects: ['Тарен', 'мара'], selfDescribed: true,
    }, { idempotencyKey: 'idem-mock-list-dragon' });
    return api.drawDrawing(projectId, drawing.id, { variants: 1, expectedRevision: 1 }, { idempotencyKey: 'idem-mock-list-dragon-draw' });
  }

  it('finds by every word, a name, a chapter, a state and the looks', async () => {
    const api = createMockApiClient();
    const drawing = await dragon(api);
    const titles = async (options: Parameters<Api['listDrawings']>[1]) => (await api.listDrawings(projectId, options)).data.map((item) => item.title || item.idea);

    expect(await titles({ q: '  ТАРЁН  ' })).toEqual(['Дракон над гаванью']);
    expect(await titles({ q: 'пристани мара' })).toEqual(['Мара у пристани']);
    expect(await titles({ q: 'белый порт' })).toEqual(['Дракон над гаванью']);
    expect(await titles({ q: '%' })).toEqual([]);
    expect((await titles({ subject: 'МАРА' })).sort()).toEqual(['Дракон над гаванью', 'Мара у пристани', 'Портрет Мары']);
    expect(await titles({ chapter: 'b2c12' })).toEqual(['Дракон над гаванью']);
    expect(await titles({ state: 'working' })).toEqual(['Дракон над гаванью']);
    expect(await titles({ state: 'waiting' })).toEqual([]);
    expect(await titles({ look: true })).toEqual(['Портрет Мары']);
    expect(await titles({ sort: 'chapters', limit: 1 })).toEqual(['Дракон над гаванью']);

    const row = (await api.listDrawings(projectId, { q: 'тарен' })).data[0]!;
    expect(row).toMatchObject({ id: drawing.id, subjects: ['Тарен', 'мара'], place: 'Книга 2, гл. 12 „Белый порт“', lookOf: [] });
    const portrait = (await api.listDrawings(projectId, { look: true })).data[0]!;
    expect(portrait).toMatchObject({ lookOf: ['Мара'] });
    expect(portrait).not.toHaveProperty('place');

    await expect(api.listDrawings(projectId, { q: '   ' })).rejects.toMatchObject({ status: 400 });
    await expect(api.listDrawings(projectId, { state: 'later' as 'waiting' })).rejects.toMatchObject({ status: 400 });
  });

  it('pages in the order it was asked and refuses the cursor of another order', async () => {
    const api = createMockApiClient();
    await dragon(api);
    const seen: string[] = [];
    let cursor: string | undefined;
    do {
      const page = await api.listDrawings(projectId, { sort: 'chapters', limit: 2, ...(cursor ? { cursor } : {}) });
      seen.push(...page.data.map((item) => item.id));
      cursor = page.meta.nextCursor ?? undefined;
      if (cursor) await expect(api.listDrawings(projectId, { cursor })).rejects.toMatchObject({ status: 400 });
    } while (cursor);
    expect(seen).toHaveLength(3);
    expect(new Set(seen).size).toBe(3);
  });

  it('counts the states within the search and lists every name and chapter', async () => {
    const api = createMockApiClient();
    await dragon(api);
    const all = await api.getDrawingsFacets(projectId);
    expect(all).toEqual({
      counts: { all: 3, waiting: 0, failed: 0, working: 1 },
      subjects: [{ name: 'Мара', drawings: 3 }, { name: 'Тарен', drawings: 1 }],
      chapters: [{ chapter: 'b2c12', place: 'Книга 2, гл. 12 „Белый порт“', drawings: 1 }],
      looks: 1,
    });
    const searched = await api.getDrawingsFacets(projectId, { q: 'пристани' });
    expect(searched.counts).toEqual({ all: 1, waiting: 0, failed: 0, working: 0 });
    expect(searched.subjects).toEqual(all.subjects);
    expect(searched.chapters).toEqual(all.chapters);
    const working = await api.listDrawings(projectId, { q: 'дракон', state: 'working' });
    expect(working.data).toHaveLength((await api.getDrawingsFacets(projectId, { q: 'дракон' })).counts.working);
  });
});

// specs/2026-11-12-ux10-drawing-ops.md DO1, DO2: the mock redraws a variant that did not come out in its place, puts a
// picture off until the service of drawings is free and takes it off again, as the backend does.
describe('mock redraw and drawings put off', () => {
  it('redraws a failed variant, puts it off until the service is free and cancels it', async () => {
    const api = createMockApiClient();
    let drawing = await api.createDrawing(projectId, { idea: 'Причал на рассвете', kind: 'scene', selfDescribed: true }, { idempotencyKey: 'idem-redraw-drawing' });
    drawing = await api.drawDrawing(projectId, drawing.id, { variants: 3, expectedRevision: 1 }, { idempotencyKey: 'idem-redraw-draw' });
    const [first, second, third] = drawing.variantImages.map((image) => image.number);
    await readUntilReady(api, [first!, third!]);
    const reset = new Date(Date.now() + 60 * 60 * 1000).toISOString();
    api.setScenario({ ...api.getScenario(), drawingLimit: { retryAt: reset } });
    await api.getProjectImage(projectId, second!);
    expect(await api.getProjectImage(projectId, second!)).toMatchObject({ status: 'failed', errorCode: 'draw_limited', retryAt: reset });
    await expect(api.redrawProjectImageVariant(projectId, first!, {}, { idempotencyKey: 'idem-redraw-ready' })).rejects.toMatchObject({ status: 409 });

    const putOff = await api.redrawProjectImageVariant(projectId, second!, { whenFree: true }, { idempotencyKey: 'idem-redraw-later' });
    expect(putOff).toMatchObject({ number: second, status: 'drawing', errorCode: null });
    expect(Date.parse(putOff.startsAfter!)).toBe(Date.parse(reset) + 30_000);
    drawing = await api.getDrawing(projectId, drawing.id);
    expect(drawing).toMatchObject({ activity: 'deferred', startsAfter: putOff.startsAfter, variants: 3 });
    expect((await api.getProjectImage(projectId, second!)).phase).toBe('queued');

    drawing = await api.cancelDeferredDrawing(projectId, drawing.id);
    expect(drawing).toMatchObject({ activity: 'choosing' });
    expect(drawing.startsAfter).toBeUndefined();
    expect(await api.getProjectImage(projectId, second!)).toMatchObject({ status: 'failed', errorCode: 'draw_limited', retryAt: putOff.startsAfter });
    await expect(api.cancelDeferredDrawing(projectId, drawing.id)).rejects.toMatchObject({ status: 409 });

    api.setScenario({ ...api.getScenario(), drawingLimit: undefined });
    const again = await api.redrawProjectImageVariant(projectId, second!, {}, { idempotencyKey: 'idem-redraw-now' });
    expect(again).toMatchObject({ number: second, status: 'drawing' });
    expect(again.startsAfter).toBeUndefined();
    await expect(api.drawDrawing(projectId, drawing.id, { variants: 1, expectedRevision: 1, whenFree: true }, { idempotencyKey: 'idem-redraw-free-waiting' }))
      .rejects.toMatchObject({ status: 409, code: 'illustrations.variant_pending' });
  });

  it('puts a whole drawing off only after the service named a time ahead', async () => {
    const api = createMockApiClient();
    let drawing = await api.createDrawing(projectId, { idea: 'Маяк в шторм', kind: 'scene', selfDescribed: true }, { idempotencyKey: 'idem-free-drawing' });
    await expect(api.drawDrawing(projectId, drawing.id, { variants: 1, expectedRevision: 1, whenFree: true }, { idempotencyKey: 'idem-free-nothing' }))
      .rejects.toMatchObject({ status: 400, code: 'illustrations.not_busy' });
    const reset = new Date(Date.now() + 30 * 60 * 1000).toISOString();
    api.setScenario({ ...api.getScenario(), drawingLimit: { retryAt: reset } });
    drawing = await api.drawDrawing(projectId, drawing.id, { variants: 1, expectedRevision: 1 }, { idempotencyKey: 'idem-free-first' });
    await readUntilReady(api, [drawing.current!.number]);
    drawing = await api.getDrawing(projectId, drawing.id);
    expect(drawing.activity).toBe('failed');
    drawing = await api.drawDrawing(projectId, drawing.id, { variants: 2, expectedRevision: 1, whenFree: true }, { idempotencyKey: 'idem-free-later' });
    expect(drawing).toMatchObject({ activity: 'deferred', variants: 2 });
    expect(drawing.variantImages.every((image) => image.startsAfter === drawing.startsAfter)).toBe(true);
    drawing = await api.cancelDeferredDrawing(projectId, drawing.id);
    expect(drawing.activity).toBe('failed');
  });
});
