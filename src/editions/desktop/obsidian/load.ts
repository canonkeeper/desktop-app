// SPDX-License-Identifier: AGPL-3.0-only
import type { OperationRequest } from '../../../shared/api';
import type { ObsidianPreview, ObsidianRecordPreview } from './obsidian-api';

/** A decision on a record the server takes one of (OW3.3): replace it with what its note says, or keep it. */
export type Decision = 'replace' | 'keep';
export type DecidedState = 'edited_in_app' | 'other_source';

/** The decisions of the author on the records of a preview: by a record, and one rule a state; a record's is older (OW3.3). */
export interface Decisions {
  byPath: Readonly<Record<string, Decision>>;
  rule: Readonly<Partial<Record<DecidedState, Decision>>>;
}

export const noDecisions: Decisions = { byPath: {}, rule: {} };

/** The state of a record a decision changes, or null: edited in the app, or a name a record of «Ваших материалов» keeps. */
export function decidedState(record: ObsidianRecordPreview): DecidedState | null {
  if (record.state === 'edited_in_app') return 'edited_in_app';
  return record.state === 'other_source' && record.replaceable && !record.heldBy ? 'other_source' : null;
}

/** The decision a record goes with: its own, else the rule of its state, else «Не менять». */
export function decisionOf(record: ObsidianRecordPreview, decisions: Decisions): Decision {
  const state = decidedState(record);
  if (!state) return 'keep';
  return decisions.byPath[record.path] ?? decisions.rule[state] ?? 'keep';
}

const creates = (record: ObsidianRecordPreview) => record.state === 'new' || record.state === 'in_book' || record.state === 'in_book_by_alias';

/** What «Загрузить» writes of the records (OW3.6): the new ones, and the ones that change — a changed note, a decision to replace. */
export function recordsToWrite(preview: ObsidianPreview, decisions: Decisions) {
  const records = preview.answer.materials.records;
  return {
    created: records.filter(creates).length,
    updated: records.filter((record) => (record.state === 'same_note' && record.changed) || (decidedState(record) && decisionOf(record, decisions) === 'replace')).length,
  };
}

/** The folders of new books that will load (OW4.2): new, without a refusal, in the order of the preview. */
export function newBooks(preview: ObsidianPreview) {
  return preview.answer.books.filter((book) => book.state.kind === 'new' && !book.refusal);
}

/** The books of the saga whose notes changed: «Загрузить» does not update them, and says so (OW3.6, OW4.4). */
export function changedBooks(preview: ObsidianPreview) {
  return preview.answer.books.filter((book) => book.state.kind === 'linked' && !book.state.inTrash && book.state.changed && !book.refusal);
}

/**
 * The body of the write of the records (OW4.2): every folder of records with its kind and the fingerprint of its records, ALL
 * the folders of books of the preview in any state — so the notes of a book inside a folder of records do not become records
 * —, the exclusions of the preview, every record with what the author saw and his decision on it, and the rules.
 */
export function materialsBody(preview: ObsidianPreview, decisions: Decisions): OperationRequest<'importObsidianMaterials'> {
  const kinds = new Map((preview.request.materials ?? []).map((material) => [material.path, material.kind]));
  return {
    books: preview.answer.books.map((book) => book.folder),
    materials: preview.answer.materials.folders.map((folder) => {
      const kind = kinds.get(folder.folder);
      return { path: folder.folder, ...(kind ? { kind } : {}), fingerprint: folder.fingerprint };
    }),
    exclude: [...(preview.request.exclude ?? [])],
    records: preview.answer.materials.records.map((record) => {
      const decision = decidedState(record) ? decisions.byPath[record.path] : undefined;
      return { path: record.path, seen: record.seen, ...(decision ? { decision } : {}) };
    }),
    replace: (['edited_in_app', 'other_source'] as const).filter((state) => decisions.rule[state] === 'replace'),
  };
}

/**
 * The body of the write of the books (OW4.2): only the folders of new books without a refusal with the fingerprints of their
 * glue, in the order of the preview; the folders of records (path and kind, no fingerprint) and the exclusions as in the
 * preview.
 */
export function booksBody(preview: ObsidianPreview): OperationRequest<'importObsidianBooks'> {
  return {
    books: newBooks(preview).map((book) => ({ folder: book.folder, fingerprint: book.fingerprint })),
    materials: (preview.request.materials ?? []).map((material) => ({ path: material.path, ...(material.kind ? { kind: material.kind } : {}) })),
    exclude: [...(preview.request.exclude ?? [])],
  };
}

/** What one «Загрузить» sends: the records when there are folders of records, the books when there are new ones and no live load. */
export function loadPlan(preview: ObsidianPreview, decisions: Decisions, queueLive: boolean) {
  return {
    materials: preview.answer.materials.folders.length > 0 ? materialsBody(preview, decisions) : null,
    books: !queueLive && newBooks(preview).length > 0 ? booksBody(preview) : null,
  };
}

/** Records whose `heldBy` name each other: two notes swapped their names while a record of one was edited in the app (OW3.3). */
export function deadlocked(record: ObsidianRecordPreview, records: readonly ObsidianRecordPreview[]) {
  if (!record.heldBy) return null;
  const other = records.find((one) => one.path === record.heldBy);
  return other?.heldBy === record.path ? other : null;
}
