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
    await expect(api.drawDrawing(projectId, drawing.id, { variants: 3, expectedRevision: 2 }, { idempotencyKey: 'idem-mock-draw-3' }))
      .rejects.toMatchObject({ status: 400 });

    drawing = await api.drawDrawing(projectId, drawing.id, { variants: 2, expectedRevision: 2 }, { idempotencyKey: 'idem-mock-draw-2' });
    expect(drawing).toMatchObject({ stage: 'variants', variants: 2, current: null, activity: 'working' });
    const [first, second] = drawing.variantImages;
    // D1.5: a drawing with variants and no current version still has a picture on its tile.
    expect(drawing.cover?.number).toBe(first!.number);
    await readUntilReady(api, [first!.number, second!.number]);
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
    expect([...drawing.versions].sort((left, right) => left - right)).toEqual([first!.number, second!.number]);
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
    expect(drawing.versions).toHaveLength(3);
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
