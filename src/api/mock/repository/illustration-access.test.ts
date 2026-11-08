import { describe, expect, it } from 'vitest';
import { createMockApiClient } from '../repository';
import { projectId } from './fixtures';

const newAuthor = '11111111-0000-4000-8000-000000000002';

// specs/2026-10-29-saga-illustrations.md UC11: the mock lets the web be built against the trial,
// a locked account, the request for access, the chat toggle and the admin panel's grants.
describe('mock access to illustrations', () => {
  it('spends the trial drawing by drawing and locks the sixth', async () => {
    const api = createMockApiClient();
    expect(await api.getIllustrationAccess()).toMatchObject({ state: 'trial', trialLeft: 5, activeUntil: null, request: null, images: { used: 0, limit: 30 } });
    for (let index = 0; index < 5; index += 1) {
      await api.createProjectImageVariant(projectId, 1, {}, { idempotencyKey: `idem-mock-trial-${index}` });
    }
    expect(await api.getIllustrationAccess()).toMatchObject({ state: 'none', trialLeft: 0, images: { used: 5 } });
    await expect(api.createProjectImageVariant(projectId, 1, {}, { idempotencyKey: 'idem-mock-trial-6' }))
      .rejects.toMatchObject({ status: 403, code: 'illustrations.locked' });
    const drawing = await api.createDrawing(projectId, { idea: 'Мара на причале' }, { idempotencyKey: 'idem-mock-trial-drawing' });
    await expect(api.submitDrawingTurn(projectId, drawing.id, { body: 'Нарисуй' }, { idempotencyKey: 'idem-mock-trial-turn' }))
      .rejects.toMatchObject({ status: 403, code: 'illustrations.locked' });
  });

  it('draws the first style sketches of a saga without a style for free, once', async () => {
    const api = createMockApiClient();
    await api.updateProjectIllustrationStyle(projectId, { style: null });
    const free = await api.createIllustrationStyleSketches(projectId, {}, { idempotencyKey: 'idem-mock-free-sketches' });
    expect(free.data).toHaveLength(4);
    expect((await api.getIllustrationAccess()).trialLeft).toBe(5);
    await api.createProjectImageVariant(projectId, 1, {}, { idempotencyKey: 'idem-mock-free-variant-1' });
    await api.createProjectImageVariant(projectId, 1, {}, { idempotencyKey: 'idem-mock-free-variant-2' });
    // Three trial drawings left: four more sketches count as drawings and are refused whole.
    await expect(api.createIllustrationStyleSketches(projectId, {}, { idempotencyKey: 'idem-mock-paid-sketches' }))
      .rejects.toMatchObject({ status: 403, code: 'illustrations.locked' });
  });

  it('keeps one pending request and refuses a repeat within a day', async () => {
    const api = createMockApiClient();
    const request = await api.createIllustrationAccessRequest({ message: ' Для глав ' });
    expect(request).toMatchObject({ status: 'pending', message: 'Для глав', decidedAt: null });
    expect((await api.getIllustrationAccess()).request?.id).toBe(request.id);
    await expect(api.createIllustrationAccessRequest({})).rejects.toMatchObject({ status: 429, code: 'illustrations.request_pending' });
  });

  it('turns access on in the admin panel, granting the request, and declines once', async () => {
    const api = createMockApiClient();
    const requests = await api.listAdminIllustrationRequests();
    expect(requests.pending).toBe(1);
    expect(requests.requests[0]).toMatchObject({ userId: newAuthor, email: 'new-author@example.test', status: 'pending', accessState: 'trial' });
    expect((await api.getAdminWriter(newAuthor)).illustrations).toMatchObject({ state: 'trial', request: { status: 'pending' } });
    const until = new Date(Date.now() + 40 * 24 * 60 * 60 * 1000).toISOString().slice(0, 10);
    const active = await api.setAdminWriterIllustrations(newAuthor, { state: 'active', until, imagesPerDay: 40 });
    expect(active).toMatchObject({ state: 'active', activeUntil: until, request: null, images: { limit: 40 } });
    expect((await api.listAdminIllustrationRequests()).requests[0]).toMatchObject({ status: 'granted', accessState: 'active' });
    await expect(api.setAdminWriterIllustrations(newAuthor, { state: 'blocked', until })).rejects.toMatchObject({ status: 400 });
    expect(await api.setAdminWriterIllustrations(newAuthor, { state: 'none', resetTrial: true })).toMatchObject({ state: 'trial', trialLeft: 5 });

    const own = await api.createIllustrationAccessRequest({});
    const declined = await api.declineAdminIllustrationRequest(own.id, { message: 'Пока только бета' });
    expect(declined).toMatchObject({ status: 'declined' });
    await expect(api.declineAdminIllustrationRequest(own.id, {})).rejects.toMatchObject({ status: 409 });
  });
});
