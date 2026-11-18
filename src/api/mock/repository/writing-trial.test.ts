import { describe, expect, it } from 'vitest';
import { putOnFree } from '../../../test/illustration-api';
import { createMockApiClient } from '../index';

const projectId = 'project-white-port';
const idea = 'Кира приходит ночью к северным воротам встретить Тарена. Его нет. Она находит обрывок письма и решает идти одна.';
const chapter = '# Глава 12. Брод\n\nКайл шол к реке, и вода была холодной.\n\nОн сказал что пора.';
const file = (name: string) => new File([chapter], name, { type: 'text/markdown' });

async function code(work: Promise<unknown>) {
  try {
    await work;
  } catch (error) {
    return (error as { code?: string }).code;
  }
  return 'passed';
}

async function freeEditor() {
  const api = createMockApiClient();
  await putOnFree(api);
  api.setScenario({ actorRole: 'editor', preset: 'normal' });
  return api;
}

// specs/2026-11-18-free-tier.md FT2.2, FT2.4: the mock decides the work of the model in a draft by the rule of the server and says
// the trial of the draft to its reader — it is not kinder than the server.
describe('the trial of «Письмо» in the mock', () => {
  it('gives one trial proofreading and refuses the second and another file', async () => {
    const api = await freeEditor();
    const first = await api.startChapterProofread(projectId, { file: file('Глава 1.md') }, { idempotencyKey: 'idem-proofread-1' });
    for (let read = 0; read < 4; read += 1) await api.getChapterDraft(projectId, first.id);
    expect((await api.getAccountPlan()).trials.proofreadsLeft).toBe(0);
    expect(await code(api.startChapterProofread(projectId, { file: file('Глава 2.md') }, { idempotencyKey: 'idem-proofread-2' }))).toBe('tiers.writing_none');
    const draft = await api.getChapterDraft(projectId, first.id);
    expect(await code(api.loadChapterDraftFile(projectId, first.id, { file: file('Глава 3.md'), expectedRevision: draft.revision }, { idempotencyKey: 'idem-proofread-3' }))).toBe('tiers.writing_none');
    expect((await api.getChapterDraft(projectId, first.id)).revision).toBe(draft.revision);
  });

  it('lets the helper plan a new chapter before its trial, writes it as the trial chapter and checks it in its day', async () => {
    const api = await freeEditor();
    const draft = await api.createChapterDraft(projectId, { kind: 'sketch', title: 'Ворота', idea, text: 'Кира стояла у северных ворот.' }, { idempotencyKey: 'idem-trial-new' });
    // A proofreading of the draft is a new trial one while it is left (FT2.2).
    expect(draft.trial).toEqual({ kind: 'before', until: null, allows: ['scenes', 'proofread', 'turn'] });
    expect(await code(api.startChapterDraftJob(projectId, draft.id, { mode: 'check', expectedRevision: draft.revision }, { idempotencyKey: 'idem-trial-check' }))).toBe('tiers.writing_none');
    expect(await code(api.submitDraftTurn(projectId, draft.id, { text: 'Написать главу' }, { idempotencyKey: 'idem-trial-turn' }))).toBe('passed');
    await api.helperPlanDraft(projectId, draft.id);
    const planned = await api.getChapterDraft(projectId, draft.id);
    expect(await code(api.startChapterDraftJob(projectId, draft.id, { mode: 'scenes', plan: planned.plan!.number, expectedRevision: planned.revision },
      { idempotencyKey: 'idem-trial-scenes' }))).toBe('passed');
    const written = await api.getChapterDraft(projectId, draft.id);
    expect(written.trial).toMatchObject({ kind: 'chapter', allows: ['scenes', 'check', 'rewrite', 'proofread', 'turn'] });
    expect(Date.parse(written.trial!.until!) - Date.now()).toBeGreaterThan(23 * 60 * 60 * 1000);

    // Another new chapter after the trial: the helper neither plans nor writes it.
    const second = await api.createChapterDraft(projectId, { kind: 'sketch', title: 'Вторая', idea, text: '' }, { idempotencyKey: 'idem-trial-second' });
    expect(second.trial).toEqual({ kind: 'none', until: null, allows: ['proofread'] });
    expect(await code(api.submitDraftTurn(projectId, second.id, { text: 'Написать главу' }, { idempotencyKey: 'idem-trial-turn-2' }))).toBe('tiers.writing_none');
  });

  // The order of the server (StartAgentJobInTx): the trial, then the limit of the day, then the new chapter of the tier — the
  // scenes of a new chapter past its trial on a spent day are refused by the day, with a day left by tiers.chapter_none.
  it('refuses the scenes of a new chapter by the day first, then by the new chapter', async () => {
    const api = await freeEditor();
    const free = (await api.listSubscriptionTiers()).tiers.find((tier) => tier.code === 'free')!;
    const withDay = (chapterDraftsPerDay: number) => api.setAdminSubscriptionTier('free', { lotUrl: null, limits: { ...free.limits, chapterDraftsPerDay } });
    await withDay(1);
    const scenes = async (key: string, start: string) => {
      const draft = await api.createChapterDraft(projectId, { kind: 'sketch', title: key, idea, text: 'Кира стояла у северных ворот.' }, { idempotencyKey: key });
      await api.helperPlanDraft(projectId, draft.id);
      const planned = await api.getChapterDraft(projectId, draft.id);
      return code(api.startChapterDraftJob(projectId, draft.id, { mode: 'scenes', plan: planned.plan!.number, expectedRevision: planned.revision },
        { idempotencyKey: start }));
    };
    expect(await scenes('idem-first', 'idem-scenes-first')).toBe('passed');
    expect((await api.getAccountQuota()).chapterDrafts).toEqual({ used: 1, limit: 1 });
    expect(await scenes('idem-second', 'idem-scenes-second')).toBe('quota.chapter_drafts_daily');
    await withDay(5);
    expect(await scenes('idem-third', 'idem-scenes-third')).toBe('tiers.chapter_none');
  });

  it('says nothing of a trial on a tier with a window', async () => {
    const api = createMockApiClient();
    api.setScenario({ actorRole: 'editor', preset: 'normal' });
    const draft = await api.createChapterDraft(projectId, { kind: 'sketch', title: 'Ворота', idea, text: '' }, { idempotencyKey: 'idem-window' });
    expect(draft.trial).toBeUndefined();
  });
});
