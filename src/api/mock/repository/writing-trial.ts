import type { OperationId, Schema } from '../../../shared/api';
import { MockApiError } from '../scenarios';
import type { MockRepositoryContext } from './context';
import { mockSpendWritingDay } from './quota';
import type { MockChapterCharge, MockState } from './state';
import { mockChargeNewChapter, mockDraftChapterCounts, mockEffectiveTier, mockSettleChapter } from './tiers';

type Work = Schema<'ChapterDraftTrial'>['allows'][number];

/** Every work of the model in a draft, in the order a draft says what it allows (domain/subscriptions Works). */
const works: Work[] = ['scenes', 'check', 'rewrite', 'proofread', 'turn'];

/** How long a trial works in its draft from its start (domain/subscriptions TrialTerm): a day. */
const trialTerm = 24 * 60 * 60 * 1000;

/** What «Письмо» says of a draft, as values (FT2.3): its scenes are a new chapter, and it edits a chapter of the book. */
export interface MockDraftFacts {
  newChapter: boolean;
  edits: boolean;
}

/** A charge counts while it is not given back and its Job lives or reached the author (TR7.3, FT2.1). */
const counted = (charge: MockChapterCharge) => !charge.returned && charge.job !== 'none';

/**
 * The trial of «Письмо» of a tier without a window (specs/2026-11-18-free-tier.md FT2.2), by the rule of the server
 * (domain/subscriptions Writing.Admit and Writing.Trial): the tier and the charges of the writer who starts the work, his
 * charges in the draft and the facts of the draft. Null on a tier with a window — «Письмо» is as before.
 */
function writing(state: MockState, userId: string, draftId: string, facts: MockDraftFacts, at: number) {
  const tier = mockEffectiveTier(state, userId, at);
  if (tier.limits.chapterEveryDays !== null) return null;
  const own = state.chapterCharges.filter((charge) => charge.userId === userId && counted(charge));
  const inDraft = own.filter((charge) => charge.draftId === draftId);
  const chapter = inDraft.find((charge) => charge.kind === 'trial');
  const proofread = inDraft.filter((charge) => charge.kind === 'proofread').sort((a, b) => a.chargedAt.localeCompare(b.chargedAt)).at(-1);
  const chapterUntil = chapter ? Date.parse(chapter.chargedAt) + trialTerm : null;
  const proofreadUntil = proofread ? Date.parse(proofread.chargedAt) + trialTerm : null;
  const chapterAt = chapterUntil !== null && !facts.edits && at < chapterUntil;
  const proofreadAt = proofreadUntil !== null && at < proofreadUntil;
  const trials = own.filter((charge) => charge.kind === 'trial').length;
  const proofreads = own.filter((charge) => charge.kind === 'proofread').length;
  const trialsLeft = trials < tier.limits.trialChapters;
  /** The work admitted, the new chapter left to the charge of a new chapter, or refused. */
  const admit = (work: Work): 'yes' | 'new_chapter' | 'no' => {
    switch (work) {
      case 'scenes':
        if (chapterAt) return 'yes';
        return facts.newChapter && !chapter ? (trialsLeft ? 'new_chapter' : 'no') : 'no';
      case 'check':
        return chapterAt ? 'yes' : 'no';
      case 'rewrite':
        return chapterAt || proofreadAt ? 'yes' : 'no';
      case 'proofread':
        return proofreads < tier.limits.trialProofreads ? 'yes' : 'no';
      default:
        return chapterAt || proofreadAt || (facts.newChapter && trialsLeft) ? 'yes' : 'no';
    }
  };
  const kind: Schema<'ChapterDraftTrial'>['kind'] = chapterAt ? 'chapter' : proofreadAt ? 'proofread'
    : facts.newChapter && !chapter && trialsLeft ? 'before' : 'none';
  const until = kind === 'chapter' ? chapterUntil : kind === 'proofread' ? proofreadUntil : null;
  return {
    admit, chapter: Boolean(chapter),
    trial: { kind, until: until === null ? null : new Date(until).toISOString(), allows: works.filter((work) => admit(work) !== 'no') },
  };
}

/** What the draft says of the trial to the writer who reads it (FT2.4); none on a tier with a window. */
export function mockDraftTrial(state: MockState, userId: string, draftId: string, facts: MockDraftFacts, at = Date.now()) {
  return writing(state, userId, draftId, facts, at)?.trial;
}

/** A draft as its reader reads the tier (UW1.1, FT2.4): whether its first scenes would count as a new chapter, and its trial. */
export function mockDraftTier(state: MockState, reader: string, draftId: string, facts: MockDraftFacts) {
  const trial = mockDraftTrial(state, reader, draftId, facts);
  return { newChapter: facts.newChapter && !mockDraftChapterCounts(state, draftId), ...(trial ? { trial } : {}) };
}

/** The work of the model a mode of the Job of a draft is (FT2.4): «Переписать» and the seams of a piece are a rewrite. */
const workOfMode = (mode: string): Work => (mode === 'selection' || mode === 'seams' ? 'rewrite' : mode as Work);

/** The limit of a day a work of the model spends (specs/2026-11-09-quota-accounting.md): the rewrites, else the writing. */
const dayOf = (work: Work) => (work === 'rewrite' ? 'draft_rewrites_per_day' as const : 'chapter_drafts_per_day' as const);

/** Refused outside the trial (FT2.2): 403 tiers.writing_none. */
function outside(operationId: OperationId) {
  return new MockApiError(403, 'The work of the model is outside the trial of the tier.', operationId, 'tiers.writing_none');
}

/**
 * The start of a work of the model in a draft in the order of the server (StartAgentJobInTx; specs/2026-11-18-free-tier.md
 * FT2.3): the trial first — 403 tiers.writing_none, the refusals of a new chapter left to its charge —, then the limit of the
 * day (`ReserveDailyInTx`), then the new chapter of the tier (`ChargeNewChapterInTx`: tiers.chapter_wait, tiers.chapter_none)
 * and the charge of a trial proofreading. A refusal leaves nothing; the answer is the Job.
 */
export function mockStartWork(context: MockRepositoryContext, draftId: string, facts: MockDraftFacts, mode: string, operationId: OperationId, jobId: string) {
  const { state } = context;
  const userId = context.currentPrincipalId();
  const work = workOfMode(mode);
  const rule = writing(state, userId, draftId, facts, Date.now());
  const newChapter = work === 'scenes' && facts.newChapter && !(rule?.chapter ?? false);
  if (rule && rule.admit(work) === 'no' && !newChapter) throw outside(operationId);
  mockSpendWritingDay(context, dayOf(work), operationId);
  if (work === 'scenes' && facts.newChapter) mockChargeNewChapter(state, userId, draftId, jobId);
  if (work === 'proofread' && rule) {
    state.chapterCharges.push({ userId, draftId, jobId, kind: 'proofread', chargedAt: new Date().toISOString(), job: 'working', returned: false });
  }
  return jobId;
}

/** A turn of the helper (FT2.2): refused outside the trial, decided before the turn. */
export function mockAdmitTurn(context: MockRepositoryContext, draftId: string, facts: MockDraftFacts) {
  const rule = writing(context.state, context.currentPrincipalId(), draftId, facts, Date.now());
  if (rule && rule.admit('turn') === 'no') throw outside('submitDraftTurn');
}

/**
 * The seams of a piece taken from a version (WA7.5, FT2.3): checked only by the trial and while the rewrites of the day are
 * left — else the piece stays unchecked, the take itself never fails.
 */
export function mockSeamsAllowed(context: MockRepositoryContext, draftId: string, facts: MockDraftFacts) {
  const rule = writing(context.state, context.currentPrincipalId(), draftId, facts, Date.now());
  if (rule && rule.admit('rewrite') === 'no') return false;
  try {
    mockSpendWritingDay(context, 'draft_rewrites_per_day', 'takeChapterDraftVersion');
    return true;
  } catch {
    return false;
  }
}

/** The end of the Job of a draft settles its charges: a new chapter by its first scene, a trial proofreading by its end. */
export function mockSettleWork(state: MockState, job: { id: string; mode?: string; status: string; done: number }) {
  mockSettleChapter(state, job);
  mockSettleProofread(state, job);
}

/** A trial proofreading counts once its corrections reached the author; one that ended before them is given back (FT2.1). */
function mockSettleProofread(state: MockState, job: { id: string; status: string }) {
  const charge = state.chapterCharges.find((candidate) => candidate.jobId === job.id && candidate.kind === 'proofread' && candidate.job === 'working');
  if (!charge) return;
  if (job.status === 'succeeded') charge.job = 'scene';
  else if (!['queued', 'running', 'canceling'].includes(job.status)) charge.job = 'none';
}
