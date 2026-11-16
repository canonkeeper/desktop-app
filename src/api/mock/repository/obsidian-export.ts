// SPDX-License-Identifier: AGPL-3.0-only
import type { OperationRequest, OperationResponse, Schema } from '../../../shared/api';
import { MockApiError } from '../scenarios';
import type { MockRepositoryContext } from './context';
import { knownFields } from './obsidian-choice';

type Exported = OperationResponse<'exportObsidianSaga', '200'>;
type Group = Schema<'ObsidianExportGroup'>;
type Skip = Schema<'ObsidianExportSkip'>;

/** The references the export of the example writes (specs/2026-11-15-desk-obsidian-export.md OE2.1). */
const exampleReferences = [
  'Сага.md', 'Герои/Мара.md', 'Герои/Сава.md', 'Места/Белый порт.md', 'Предметы/Ледяной трон.md',
  'Главы/Застава/1 Застава.md', 'Главы/Застава/2 Переправа.md',
];

/** The reference of the example the author changed in Obsidian, by the scenario. */
const changedReference = 'Герои/Сава.md';

/**
 * The references of the example the scenario `skipped` does not read, one by each reason of `ObsidianExportSkip` — in the order
 * of the server, by the name of the reason.
 */
const unreadReferences: ReadonlyArray<[Skip['reason'], string]> = [
  ['busy', 'Герои/Сава.md'], ['cloud_only', 'Герои/Мара.md'], ['link', 'Предметы/Ледяной трон.md'], ['unreadable', 'Места/Белый порт.md'],
];

const group = (names: readonly string[]): Group => ({ count: names.length, names: names.slice(0, 50) });

/** What the export guards of the bridge check before and around it. */
export interface ExportGuards {
  saga(projectId: string, write: boolean): void;
  linked(projectId: string): void;
  free(): void;
  readable(): void;
  /** The map of the saga by the same reading as getSagaMap. */
  map(projectId: string): Promise<OperationResponse<'getSagaMap', '200'>>;
}

/** A run that did not reach its end: its answer goes back, the key keeps nothing (OE1.1). */
class Unfinished {
  constructor(readonly exported: Exported) {}
}

/**
 * exportObsidianSaga in the mock (specs/2026-11-15-desk-obsidian-export.md; specs/2026-11-16-desk-obsidian-export-web.md XW4):
 * no vault, so the scenario says how the folder and the files answer; the map is the map of the saga — its getSagaMap. The order
 * is the server's: the key and the body, the saga and the role, the link, the place of the saga (busy), the answer of the key,
 * the map, the vault, the folder of the export. The folder is CanonKeeper/<the title of the saga>, kept on the link by the first
 * export; the first export creates the references of the example, the next ones change none; a file the author changed is
 * not updated without his word and moved into _Удалено/ with it; the key keeps only the answer of an export that ended.
 */
export function createObsidianExportOperations(context: MockRepositoryContext, guards: ExportGuards) {
  /** The map of the saga is ready: not being built, a card read (OE1.2). */
  async function mapReady(projectId: string) {
    const map = await guards.map(projectId);
    if (map.coverage.withCards === 0 || map.building) {
      const reason = map.coverage.withCards === 0 ? 'no_cards' : 'updating';
      throw new MockApiError(409, 'the map of the saga is not ready', 'exportObsidianSaga', 'obsidian.map_not_ready', { fields: { reason } });
    }
  }

  function folder(scenario: string | undefined) {
    if (scenario === 'unsafe') {
      throw new MockApiError(409, 'the path to the folder of the export is not a folder', 'exportObsidianSaga', 'obsidian.export_dir_unsafe');
    }
    if (scenario === 'manifest_unreadable') {
      throw new MockApiError(409, 'the manifest of the export does not read', 'exportObsidianSaga', 'obsidian.export_manifest_unreadable');
    }
  }

  async function run(projectId: string, body: OperationRequest<'exportObsidianSaga'>): Promise<Exported> {
    const scenario = context.getScenario().obsidianExport;
    await mapReady(projectId);
    guards.readable();
    folder(scenario);
    const vault = context.state.obsidianVaults[projectId]!;
    const first = !vault.exportDir;
    vault.exportDir ??= `CanonKeeper/${context.projectFor(projectId, 'exportObsidianSaga').title}`;
    const at = new Date().toISOString();
    // `skipped` is a run that did not read some files of its folder: it did not end either.
    const complete = scenario !== 'partial' && scenario !== 'skipped';
    const exported: Exported = {
      folder: vault.exportDir, at, complete, created: group([]), updated: group([]), unchanged: group([]), moved: group([]), kept: group([]), skipped: [],
    };
    const changed = scenario === 'changed' && !first;
    const unread = scenario === 'skipped' ? unreadReferences : [];
    const rest = exampleReferences.filter((reference) => (!changed || reference !== changedReference) && !unread.some(([, name]) => name === reference));
    if (first) exported.created = group(scenario === 'partial' ? rest.slice(0, 3) : rest);
    else exported.unchanged = group(rest);
    if (changed && body.replaceChanged) {
      exported.moved = group([`_Удалено/${changedReference.split('/').pop()}`]);
      exported.updated = group([changedReference]);
    } else if (changed) {
      exported.kept = group([changedReference]);
    }
    exported.skipped = unread.map(([reason, name]) => ({ reason, count: 1, names: [name] }));
    if (complete) vault.lastExportAt = at;
    return exported;
  }

  return {
    async exportObsidianSaga(projectId: string, body: OperationRequest<'exportObsidianSaga'>, options: { idempotencyKey: string }): Promise<Exported> {
      // The server reads one key of 8 to 128 signs and the body before the saga: an object with the one optional word of the
      // author, nothing else.
      const key = options.idempotencyKey?.trim() ?? '';
      if (key.length < 8 || key.length > 128 || !knownFields(body, { replaceChanged: null }) || !['undefined', 'boolean'].includes(typeof body.replaceChanged)) {
        throw new MockApiError(400, 'validation failed', 'exportObsidianSaga', 'request.validation');
      }
      guards.saga(projectId, true);
      guards.linked(projectId);
      guards.free();
      // OE1.1: the key keeps only the answer of an export that ended; a run that did not end is run again by the same key, and
      // the same key with the other word of the author is a conflict (the hash of the server: «replace» or «keep»).
      try {
        return await context.idempotentCommand('exportObsidianSaga', projectId, options, { replace: body.replaceChanged === true }, async () => {
          const exported = await run(projectId, body);
          if (!exported.complete) throw new Unfinished(exported);
          return exported;
        });
      } catch (error) {
        if (error instanceof Unfinished) return error.exported;
        if (error instanceof MockApiError && error.code === 'idempotency_key_mismatch') {
          throw new MockApiError(409, 'the key was given to another export', 'exportObsidianSaga', 'idempotency.conflict');
        }
        throw error;
      }
    },
  };
}
