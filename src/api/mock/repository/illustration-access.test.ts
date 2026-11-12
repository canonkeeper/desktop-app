import { describe, expect, it } from 'vitest';
import { putOnFree } from '../../../test/illustration-api';
import { createMockApiClient } from '../repository';
import { projectId } from './fixtures';

const newAuthor = '11111111-0000-4000-8000-000000000002';

// specs/2026-11-08-subscription-tiers.md TR6: the mock lets the web be built against drawing by the tier, the trial of
// «Бесплатно», a locked account and the administrator's block.
describe('mock right to draw', () => {
  it('draws by the day on the tier of the pilot', async () => {
    const api = createMockApiClient();
    expect(await api.getIllustrationAccess()).toMatchObject({ state: 'active', trialLeft: 0, images: { used: 0, limit: 30 } });
    await api.createProjectImageVariant(projectId, 1, {}, { idempotencyKey: 'idem-mock-day-1' });
    expect(await api.getIllustrationAccess()).toMatchObject({ state: 'active', images: { used: 1, limit: 30 } });
    expect(await api.getAccountPlan()).toMatchObject({ tier: { code: 'pro' }, trials: { imagesLeft: 0 } });
  });

  it('spends the three trial drawings of «Бесплатно» and locks the fourth', async () => {
    const api = createMockApiClient();
    await putOnFree(api);
    expect(await api.getIllustrationAccess()).toMatchObject({ state: 'trial', trialLeft: 3, images: { used: 0, limit: 0 } });
    for (let index = 0; index < 3; index += 1) {
      await api.createProjectImageVariant(projectId, 1, {}, { idempotencyKey: `idem-mock-trial-${index}` });
    }
    expect(await api.getIllustrationAccess()).toMatchObject({ state: 'none', trialLeft: 0, images: { used: 3 } });
    expect((await api.getAccountPlan()).trials).toEqual({ imagesLeft: 0, chaptersLeft: 1, voiceCallsLeft: 3 });
    await expect(api.createProjectImageVariant(projectId, 1, {}, { idempotencyKey: 'idem-mock-trial-4' }))
      .rejects.toMatchObject({ status: 403, code: 'illustrations.locked' });
    const drawing = await api.createDrawing(projectId, { idea: 'Мара на причале' }, { idempotencyKey: 'idem-mock-trial-drawing' });
    await expect(api.submitDrawingTurn(projectId, drawing.id, { body: 'Нарисуй' }, { idempotencyKey: 'idem-mock-trial-turn' }))
      .rejects.toMatchObject({ status: 403, code: 'illustrations.locked' });
  });

  it('draws the first style sketches of a saga without a style for free, once', async () => {
    const api = createMockApiClient();
    await putOnFree(api);
    await api.updateProjectIllustrationStyle(projectId, { style: null });
    const free = await api.createIllustrationStyleSketches(projectId, {}, { idempotencyKey: 'idem-mock-free-sketches' });
    expect(free.data).toHaveLength(4);
    expect((await api.getIllustrationAccess()).trialLeft).toBe(3);
    await api.createProjectImageVariant(projectId, 1, {}, { idempotencyKey: 'idem-mock-free-variant-1' });
    // Two trial drawings left: four more sketches count as drawings and are refused whole.
    await expect(api.createIllustrationStyleSketches(projectId, {}, { idempotencyKey: 'idem-mock-paid-sketches' }))
      .rejects.toMatchObject({ status: 403, code: 'illustrations.locked' });
  });

  it('blocks and unblocks drawing in the admin panel and gives the trial back', async () => {
    const api = createMockApiClient();
    // specs/2026-11-08-admin-redesign.md AR4.8: the card names only the state; the trials are the plan's.
    expect((await api.getAdminWriter(newAuthor)).illustrations).toEqual({ state: 'trial' });
    expect((await api.getAdminWriter(newAuthor)).plan.trials.imagesLeft).toBe(3);
    expect(await api.setAdminWriterIllustrations(newAuthor, { state: 'blocked' })).toMatchObject({ state: 'blocked' });
    await expect(api.setAdminWriterIllustrations(newAuthor, { state: 'active' } as never)).rejects.toMatchObject({ status: 400 });
    expect(await api.setAdminWriterIllustrations(newAuthor, { state: 'none' })).toMatchObject({ state: 'trial' });
    expect(await api.resetAdminWriterTrials(newAuthor, { images: true })).toMatchObject({ tier: { code: 'free' }, trials: { imagesLeft: 3 } });
    await expect(api.resetAdminWriterTrials(newAuthor, { images: false } as never)).rejects.toMatchObject({ status: 400 });
  });
});
