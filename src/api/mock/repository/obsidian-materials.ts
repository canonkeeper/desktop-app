// SPDX-License-Identifier: AGPL-3.0-only
import type { OperationRequest, OperationResponse, Schema } from '../../../shared/api';
import { MockApiError } from '../scenarios';
import type { createAuthorMaterialOperations } from './author-materials';
import type { MockRepositoryContext } from './context';
import { digest, knownFields, ownerOf, type MockChoice } from './obsidian-choice';

type Record = Schema<'ObsidianRecordPreview'>;
type Materials = Schema<'ObsidianMaterialsPreview'>;
type Loaded = OperationResponse<'importObsidianMaterials', '200'>;
type Group = Schema<'ObsidianNoteGroup'>;
type Example = Omit<Record, 'folder' | 'kind' | 'seen' | 'replaceable'>;
type ObsidianRecords = ReturnType<typeof createAuthorMaterialOperations>['obsidianRecords'];

/**
 * The notes of the example that make records (OB4.3), by the folder they lie in: every state of a new load and both
 * warnings; the folder «Книга 5/Герои» lies inside a book (specs/2026-11-15-desk-obsidian-web.md OW7.2).
 */
const exampleRecords: { [folder: string]: Example[] } = {
  Персонажи: [
    { path: 'Персонажи/Ольга Сватова.md', name: 'Ольга', state: 'in_book', changed: false, bookName: 'Ольга', aliases: ['Ольга Сватова'], properties: 1, warnings: [], cut: 0, left: 0 },
    { path: 'Персонажи/Мирон Сватов.md', name: 'Мирон Сватов', state: 'in_book_by_alias', changed: false, bookName: 'Мирон', aliases: ['Мирон'], properties: 1, warnings: [], cut: 0, left: 0 },
    { path: 'Персонажи/Йорген.md', name: 'Йорген', state: 'new', changed: false, aliases: [], properties: 0, warnings: [], cut: 0, left: 0 },
    { path: 'Персонажи/Йорген старший.md', name: 'Йорген', state: 'other_source', changed: false, aliases: [], properties: 0, warnings: [], cut: 0, left: 0, heldBy: 'Персонажи/Йорген.md' },
    { path: 'Персонажи/Семья.md', name: 'Семья', state: 'new', changed: false, aliases: [], properties: 0, warnings: ['several_sections'], cut: 0, left: 0 },
  ],
  Места: [
    { path: 'Места/Перевал.md', name: 'Перевал', state: 'other_source', changed: false, aliases: [], properties: 0, warnings: [], cut: 0, left: 0 },
    { path: 'Места/Все места.md', name: 'Все места', state: 'new', changed: false, aliases: [], properties: 0, warnings: ['list_note'], cut: 0, left: 0 },
    { path: 'Места/Застава.md', name: 'Застава', state: 'new', changed: false, aliases: [], properties: 50, warnings: [], cut: 2, left: 3 },
  ],
  'Книга 5/Герои': [
    { path: 'Книга 5/Герои/Тим.md', name: 'Тим', state: 'new', changed: false, aliases: [], properties: 0, warnings: [], cut: 0, left: 0 },
    { path: 'Книга 5/Герои/Сава.md', name: 'Сава', state: 'new', changed: false, aliases: [], properties: 2, warnings: [], cut: 0, left: 0 },
  ],
};

/**
 * The records whose name another record keeps and which a decision may replace (specs/2026-11-15-desk-obsidian-notes.md
 * ON1.4): «Перевал» is kept by a record of the author; the name of «Йорген старший» another note of the load gets — its
 * heldBy names it.
 */
const replaceable = new Set(['Места/Перевал.md']);

/** What the notes of the folders of materials of the example skip: a note without a name and one with a name too long. */
const exampleRecordsSkipped: { [folder: string]: Schema<'ObsidianSkipCount'>[] } = {
  Места: [{ reason: 'long_name', count: 1 }, { reason: 'no_name', count: 1 }],
};

/** The notes as part A3 keeps them linked when the scenario says so: one unchanged, one changed, one edited in the app. */
function linkedRecord(record: Record): Record {
  switch (record.path) {
    case 'Персонажи/Семья.md':
      return { ...record, state: 'same_note', changed: false };
    case 'Места/Перевал.md':
      return { ...record, state: 'same_note', changed: true };
    case 'Персонажи/Йорген.md':
      return { ...record, state: 'edited_in_app', changed: false };
    default:
      return record;
  }
}

const keyOf = (name: string) => name.trim().toLowerCase().replaceAll('ё', 'е');

/** What the author sees of the state of a record: a load names it back (ON1.3); an edit in the app changes it. */
function seenOf(record: Omit<Record, 'seen' | 'replaceable'>, revision: number | null): string {
  const creates = record.state === 'new' || record.state === 'in_book' || record.state === 'in_book_by_alias';
  if (creates) return digest('create', 16);
  const known = record.state === 'same_note' || record.state === 'edited_in_app' ? `:${revision ?? 0}:${record.changed}` : '';
  return digest(`${record.state}:${record.path}${known}`, 16);
}

/** What the mock remembers of the notes of a saga: their links with the records they made, and the records themselves. */
export interface NotesMemory {
  links: ReadonlyArray<{ path: string; materialId: string; revision: number }>;
  records: Pick<ObsidianRecords, 'named' | 'byId'>;
  projectId: string;
}

/** A record of the preview and the revision its seen was told by. */
type Stated = { record: Record; revision: number | null };

/**
 * The state of a record now (specs/2026-11-15-desk-obsidian-web.md OW7.2): a note linked with its record — the same note,
 * or edited in the app when the record changed since the load —; a note whose name a record keeps — the record of another
 * source, replaceable by the first note of the name, held by it for the others —; else the state of the example.
 */
function stateNow(previewed: Record, memory: NotesMemory | null, load: readonly Record[]): Stated {
  if (!memory) return { record: previewed, revision: null };
  const link = memory.links.find((one) => one.path === previewed.path);
  const linked = link ? memory.records.byId(memory.projectId, link.materialId) : null;
  if (link && linked) {
    return linked.revision !== link.revision
      ? { record: { ...previewed, state: 'edited_in_app', changed: false, heldBy: undefined }, revision: linked.revision }
      : { record: { ...previewed, state: 'same_note', changed: false, heldBy: undefined }, revision: linked.revision };
  }
  const holder = memory.records.named(memory.projectId, previewed.name);
  if (!holder) return { record: previewed, revision: null };
  const holderLink = memory.links.find((one) => one.materialId === holder.id && one.path !== previewed.path);
  const first = load.find((other) => keyOf(other.name) === keyOf(previewed.name));
  const heldBy = holderLink && load.some((other) => other.path === holderLink.path) ? holderLink.path
    : first && first.path !== previewed.path ? first.path : undefined;
  const { heldBy: _was, ...rest } = previewed;
  return { record: { ...rest, state: 'other_source', changed: false, ...(heldBy ? { heldBy } : {}) }, revision: null };
}

/**
 * What the folders of materials of a choice give in the mock (OB4.3, ON1.1): the notes each folder owns by the choice — a
 * book or a deeper folder of the choice takes its notes, an exclusion takes them out —, their states by what the mock
 * remembers of the saga or, when the scenario says so, linked as part A3 keeps them; what the author sees of each, the
 * skipped notes, and the fingerprint of the records of each folder.
 */
export function previewMaterials(linked: boolean, choice: MockChoice, memory: NotesMemory | null = null): Materials {
  const owned = Object.values(exampleRecords).flat().flatMap((example) => {
    const owner = ownerOf(example.path, choice);
    return owner && !owner.book ? [{ ...structuredClone(example), folder: owner.path, kind: owner.kind, seen: '', replaceable: false }] : [];
  });
  const records: Record[] = owned.map((previewed) => {
    const { record, revision } = linked ? { record: linkedRecord(previewed), revision: null } : stateNow(previewed, memory, owned);
    const free = record.state === 'other_source' && !record.heldBy && (memory?.records.named(memory.projectId, record.name) || replaceable.has(record.path));
    return { ...record, seen: seenOf(record, revision), replaceable: Boolean(free) };
  });
  return {
    records,
    skipped: choice.materials.flatMap((material) => structuredClone(exampleRecordsSkipped[material.path] ?? [])),
    folders: choice.materials.map((material) => ({
      folder: material.path,
      fingerprint: digest(`${material.path}:${material.kind}:${owned.filter((record) => record.folder === material.path).map((record) => record.path).join('|')}`, 64),
    })),
  };
}

interface Guards {
  saga(projectId: string, write: boolean): void;
  /** not_linked; busy — the place of the saga, before the answer of a key; the vault that does not open or answer in time. */
  linked(projectId: string): void;
  free(): void;
  readable(): void;
  /** folders_overlap of a choice — with the folders the books of the saga hold, when `held`. */
  overlapping(projectId: string, books: readonly string[], folders: readonly string[], held: boolean): void;
  inVault(path: string): boolean;
  relative(value: string): string | null;
}

const outcomeOf = (record: Record, seen: string | undefined, replace: boolean) => {
  if (seen !== record.seen) return 'changed';
  switch (record.state) {
    case 'new':
    case 'in_book':
    case 'in_book_by_alias':
      return 'created';
    case 'same_note':
      return record.changed ? 'updated' : 'unchanged';
    case 'edited_in_app':
      return replace ? 'updated' : 'edited';
    default:
      return replace && record.replaceable ? 'replaced' : 'taken';
  }
};

/**
 * importObsidianMaterials in the mock (specs/2026-11-15-desk-obsidian-notes.md ON1): the records of the example of the chosen
 * folders by their states now, what the author saw and his decisions — every group of the answer —; another fingerprint of
 * a folder is folder_changed, a record of the folders the request does not name request.validation, a scenario of too many
 * records too_large with its numbers, a scenario of a race with the author conflict; the answer is kept for the key. What a
 * load writes goes into «Ваши материалы» of the mock with the note as its source, and the note keeps its link
 * (specs/2026-11-15-desk-obsidian-web.md OW7.2).
 */
export function createObsidianMaterialsOperations(context: MockRepositoryContext, guards: Guards, obsidianRecords: ObsidianRecords) {
  const memoryOf = (projectId: string): NotesMemory => ({ links: context.state.obsidianNotes[projectId] ?? [], records: obsidianRecords, projectId });

  /** A record written from a note: its link names the revision the load left it at; one record has one note. */
  function write(projectId: string, record: Record, into: Schema<'AuthorMaterial'> | null) {
    const written = obsidianRecords.write(projectId, { name: record.name, kind: record.kind, section: record.folder, path: record.path }, into);
    context.state.obsidianNotes[projectId] = [
      ...(context.state.obsidianNotes[projectId] ?? []).filter((link) => link.path !== record.path && link.materialId !== written.id),
      { path: record.path, materialId: written.id, revision: written.revision },
    ];
  }

  return {
    memoryOf,
    async importObsidianMaterials(
      projectId: string, body: OperationRequest<'importObsidianMaterials'>, options: { idempotencyKey: string },
    ): Promise<Loaded> {
      guards.saga(projectId, true);
      const invalid = () => new MockApiError(400, 'validation failed', 'importObsidianMaterials', 'request.validation');
      if (!knownFields(body, { books: null, materials: ['path', 'kind', 'fingerprint'], exclude: null, records: ['path', 'seen', 'decision'], replace: null })) throw invalid();
      const materials = body.materials.map((material) => ({ path: guards.relative(material.path), kind: material.kind ?? 'other', fingerprint: material.fingerprint }));
      const records = body.records ?? [];
      if (materials.length === 0 || materials.some((material) => material.path === null || !/^[0-9a-f]{64}$/u.test(material.fingerprint)) ||
        [...body.books ?? [], ...body.exclude ?? []].some((path) => guards.relative(path) === null) ||
        records.some((record) => !/^[0-9a-f]{16}$/u.test(record.seen)) || new Set(records.map((record) => record.path)).size !== records.length) {
        throw invalid();
      }
      // The order of the server: the link, the request, the place of the saga — busy before the answer of the key —, then the
      // answer of the key, the folders of the saga, the vault and the fingerprints.
      guards.linked(projectId);
      const bookPaths = (body.books ?? []).map((path) => guards.relative(path)!);
      guards.overlapping(projectId, bookPaths, materials.map((material) => material.path!), false);
      guards.free();
      return context.idempotentCommand('importObsidianMaterials', projectId, options, body, () => {
        guards.overlapping(projectId, bookPaths, materials.map((material) => material.path!), true);
        guards.readable();
        const scenario = context.getScenario();
        if (scenario.obsidianVault === 'too_many_records') throw tooManyRecords('importObsidianMaterials');
        // A folder gone since its preview (sincePreview): folder_changed.
        const gone = materials.filter((material) => !guards.inVault(material.path!)).map((material) => material.path!);
        if (gone.length) {
          throw new MockApiError(409, 'a folder changed since its preview', 'importObsidianMaterials', 'obsidian.folder_changed', { fields: { folders: gone } });
        }
        const choice: MockChoice = {
          books: (body.books ?? []).map((path) => guards.relative(path)!),
          materials: materials.map((material) => ({ path: material.path!, kind: material.kind })),
          exclude: (body.exclude ?? []).map((path) => guards.relative(path)!),
        };
        const preview = previewMaterials(scenario.obsidianLinked === true, choice, memoryOf(projectId));
        const changed = preview.folders.filter((folder, index) => folder.fingerprint !== materials[index]!.fingerprint).map((folder) => folder.folder);
        if (changed.length) {
          throw new MockApiError(409, 'a folder changed since its preview', 'importObsidianMaterials', 'obsidian.folder_changed', { fields: { folders: changed } });
        }
        const seen = new Map(records.map((record) => [record.path, record]));
        // ON1.3: what the author saw of every record of the folders, before the saga is read.
        if (preview.records.some((record) => !seen.has(record.path))) throw invalid();
        // ON1.5: a name the author took during the load — nothing written, nothing kept for the key.
        if (scenario.obsidianWrite === 'conflict') {
          throw new MockApiError(409, 'the write conflicts with the state of the saga', 'importObsidianMaterials', 'conflict');
        }
        const group = (): Group => ({ count: 0, names: [] });
        const loaded: Loaded = {
          created: group(), updated: group(), replaced: group(), unchanged: group(), kept: { taken: group(), edited: group(), changed: group() },
          skipped: preview.skipped,
        };
        const links = context.state.obsidianNotes[projectId] ?? [];
        for (const record of preview.records) {
          const asked = seen.get(record.path);
          const replace = (asked?.decision ?? ((body.replace ?? []).includes(record.state as 'edited_in_app' | 'other_source') ? 'replace' : 'keep')) === 'replace';
          const outcome = outcomeOf(record, asked?.seen, replace);
          const into = outcome === 'taken' || outcome === 'edited' || outcome === 'changed' ? loaded.kept[outcome] : loaded[outcome];
          into.count += 1;
          if (into.names.length < 50) into.names.push(record.name);
          const link = links.find((one) => one.path === record.path);
          if (outcome === 'created') write(projectId, record, null);
          if (outcome === 'updated') write(projectId, record, link ? obsidianRecords.byId(projectId, link.materialId) : obsidianRecords.named(projectId, record.name));
          if (outcome === 'replaced') write(projectId, record, obsidianRecords.named(projectId, record.name));
        }
        return loaded;
      });
    },
  };
}

/** A choice past the records of a load or of a saga (ON1.7). */
export function tooManyRecords(operation: 'previewObsidianImport' | 'importObsidianMaterials') {
  return new MockApiError(422, 'too many records', operation, 'obsidian.too_large', { fields: { records: 5_003, sagaRecords: 5_003, limitRecords: 5_000 } });
}
