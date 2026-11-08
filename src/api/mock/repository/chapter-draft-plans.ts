import type { OperationRequest, Schema } from '../../../shared/api';
import { MockApiError } from '../scenarios';
import { clone } from './common';

type Draft = Schema<'ChapterDraft'>;
type Plan = Schema<'ChapterDraftPlan'>;
type Choice = Schema<'ChapterDraftMoment'>['choices'][number];

/** The versions of the plan of a mock draft (specs/2026-11-06-writing-agent.md, WA4), the latest last. */
const plans = new WeakMap<Draft, Plan[]>();
/** The author's choices about the pack (WA2.5). */
const choices = new WeakMap<Draft, Choice[]>();

export const plansOf = (draft: Draft) => plans.get(draft) ?? [];

/** Adds the next version of the plan; every version lists them all, the latest first, and the draft points at it. */
export function addPlanVersion(draft: Draft, plan: Omit<Plan, 'number' | 'versions' | 'approved' | 'stale' | 'basisRevision'>): Plan {
  const versions = [...plansOf(draft)];
  const added: Plan = { ...plan, number: versions.length + 1, basisRevision: draft.revision, approved: false, stale: false, versions: [] };
  versions.push(added);
  for (const version of versions) {
    version.versions = [...versions].reverse().map((item) => ({ number: item.number, origin: item.origin, reason: item.reason, scenes: item.scenes.length }));
  }
  plans.set(draft, versions);
  draft.plan = { number: added.number, approved: false, stale: false };
  return clone(added);
}

/** updateChapterDraftPlan: the author's version at the latest one he saw (WA4.2). */
export function authorPlan(draft: Draft, body: OperationRequest<'updateChapterDraftPlan'>): Plan {
  const latest = plansOf(draft).at(-1);
  if (!latest) throw new MockApiError(404, 'No plan yet.', 'updateChapterDraftPlan');
  if (latest.number !== body.expectedNumber) {
    throw new MockApiError(409, 'A newer version stands.', 'updateChapterDraftPlan', 'chapter_draft.plan_changed');
  }
  return addPlanVersion(draft, {
    origin: 'author', reason: 'author_edit', case: latest.case, understanding: body.understanding ?? latest.understanding, scenes: body.scenes, marks: [],
  });
}

/** «Писать» approves the version it names, which must be the latest (WA4.3). */
export function approvePlan(draft: Draft, number: number) {
  if (plansOf(draft).at(-1)?.number !== number) {
    throw new MockApiError(409, 'A newer version stands.', 'startChapterDraftJob', 'chapter_draft.plan_changed');
  }
  draft.plan = { number, approved: true, stale: false };
}

export const choicesOf = (draft: Draft) => clone(choices.get(draft) ?? []);

/** updateChapterDraftMoment: the author's choices replace the ones he had; the last about a name counts. */
export function setChoices(draft: Draft, next: Choice[]) {
  const byName = new Map<string, Choice>();
  for (const choice of next) {
    const entry = choice.entry.trim();
    byName.delete(entry.toLowerCase());
    byName.set(entry.toLowerCase(), { action: choice.action, entry });
  }
  choices.set(draft, [...byName.values()]);
}
