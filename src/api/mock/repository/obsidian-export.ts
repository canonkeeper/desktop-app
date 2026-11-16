// SPDX-License-Identifier: AGPL-3.0-only
import type { OperationRequest, OperationResponse, Schema } from '../../../shared/api';
import { MockApiError } from '../scenarios';
import type { MockRepositoryContext } from './context';
import { knownFields } from './obsidian-choice';

type Exported = OperationResponse<'exportObsidianSaga', '200'>;
type Group = Schema<'ObsidianExportGroup'>;

/** The references the export of the example writes (specs/2026-11-15-desk-obsidian-export.md OE2.1). */
const exampleReferences = [
  'Сага.md', 'Герои/Мара.md', 'Герои/Сава.md', 'Места/Белый порт.md', 'Предметы/Ледяной трон.md',
  'Главы/Застава/1 Застава.md', 'Главы/Застава/2 Переправа.md',
];

/** The reference of the example the author changed in Obsidian, by the scenario. */
const changedReference = 'Герои/Сава.md';

const group = (names: readonly string[]): Group => ({ count: names.length, names: names.slice(0, 50) });

/** What the export guards of the bridge check before and around it. */
export interface ExportGuards {
  saga(projectId: string, write: boolean): void;
  linked(projectId: string): void;
  free(): void;
  readable(): void;
}

/**
 * exportObsidianSaga in the mock (specs/2026-11-15-desk-obsidian-export.md): no vault, so the scenario says how the map, the
 * folder and the files answer; the folder of the export is CanonKeeper/<the title of the saga>, kept on the link by the first
 * export; the first export creates the references of the example, the next ones change none; a file the author changed is
 * not updated without his word and moved into _Удалено/ with it; the key keeps only the answer of an export that ended.
 */
export function createObsidianExportOperations(context: MockRepositoryContext, guards: ExportGuards) {
  function refuse(scenario: string | undefined) {
    switch (scenario) {
      case 'updating':
      case 'no_cards':
        throw new MockApiError(409, 'the map of the saga is not ready', 'exportObsidianSaga', 'obsidian.map_not_ready', { fields: { reason: scenario } });
      case 'unsafe':
        throw new MockApiError(409, 'the path to the folder of the export is not a folder', 'exportObsidianSaga', 'obsidian.export_dir_unsafe');
      case 'manifest_unreadable':
        throw new MockApiError(409, 'the manifest of the export does not read', 'exportObsidianSaga', 'obsidian.export_manifest_unreadable');
    }
  }

  function run(projectId: string, body: OperationRequest<'exportObsidianSaga'>): Exported {
    const scenario = context.getScenario().obsidianExport;
    refuse(scenario);
    const vault = context.state.obsidianVaults[projectId]!;
    const first = !vault.exportDir;
    vault.exportDir ??= `CanonKeeper/${context.projectFor(projectId, 'exportObsidianSaga').title}`;
    const at = new Date().toISOString();
    const complete = scenario !== 'partial';
    const exported: Exported = {
      folder: vault.exportDir, at, complete, created: group([]), updated: group([]), unchanged: group([]), moved: group([]), kept: group([]), skipped: [],
    };
    const changed = scenario === 'changed' && !first;
    const rest = exampleReferences.filter((reference) => !changed || reference !== changedReference);
    if (first) exported.created = group(complete ? rest : rest.slice(0, 3));
    else exported.unchanged = group(rest);
    if (changed && body.replaceChanged) {
      exported.moved = group([`_Удалено/${changedReference.split('/').pop()}`]);
      exported.updated = group([changedReference]);
    } else if (changed) {
      exported.kept = group([changedReference]);
    }
    if (complete) vault.lastExportAt = at;
    return exported;
  }

  return {
    async exportObsidianSaga(projectId: string, body: OperationRequest<'exportObsidianSaga'>, options: { idempotencyKey: string }): Promise<Exported> {
      // The server reads the body before the saga: an object with the one optional word of the author, nothing else.
      if (!knownFields(body, { replaceChanged: null }) || !['undefined', 'boolean'].includes(typeof body.replaceChanged)) {
        throw new MockApiError(400, 'validation failed', 'exportObsidianSaga', 'request.validation');
      }
      guards.saga(projectId, true);
      guards.linked(projectId);
      guards.free();
      guards.readable();
      // OE1.1: the key keeps only the answer of an export that ended; a run that did not end is run again by the same key.
      if (context.getScenario().obsidianExport === 'partial' || refusing(context.getScenario().obsidianExport)) {
        if (!options.idempotencyKey) throw new MockApiError(400, 'Idempotency-Key is required.', 'exportObsidianSaga', 'idempotency_key_required');
        return run(projectId, body);
      }
      return context.idempotentCommand('exportObsidianSaga', projectId, options, body, () => run(projectId, body));
    },
  };
}

/** A scenario whose export is refused: its key keeps nothing. */
function refusing(scenario: string | undefined) {
  return scenario === 'updating' || scenario === 'no_cards' || scenario === 'unsafe' || scenario === 'manifest_unreadable';
}
