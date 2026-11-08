import type { AuthorMaterialsImportInput, CanonKeeperApiClient, ListAuthorMaterialsOptions, OperationRequest, Schema } from '../../../shared/api';
import { MockApiError } from '../scenarios';
import { clone } from './common';
import type { MockRepositoryContext } from './context';

type Material = Schema<'AuthorMaterial'>;
type MaterialImport = Schema<'AuthorMaterialsImport'>;
type Candidate = Schema<'AuthorMaterialCandidate'>;
type Operations = Pick<CanonKeeperApiClient, 'listAuthorMaterials' | 'updateAuthorMaterial' | 'deleteAuthorMaterial'
  | 'startAuthorMaterialsImport' | 'getAuthorMaterialsImport' | 'confirmAuthorMaterialsImport' | 'discardAuthorMaterialsImport'>;

const writers = ['owner', 'admin', 'editor'] as const;
const keyOf = (name: string) => name.trim().toLowerCase().replaceAll('ё', 'е');

/** What every file of the mock holds (specs/2026-11-04-saga-map.md, UC9): a new skill, a new place and a name the book knows. */
const fileCandidates: readonly Omit<Candidate, 'match'>[] = [
  {
    name: 'оживший кошмар', kind: 'skill', description: 'Сны становятся явью и пугают Мару.',
    properties: [{ key: 'Редкость', value: 'эпический (оранжевая заливка)' }, { key: 'Ранг', value: 'Уникальный (вне уровней)' }], section: 'Навыки',
  },
  { name: 'Миг назад', kind: 'place', description: 'Трактир, куда заходит Сава.', properties: [{ key: 'Этаж', value: 'второй' }], section: 'Места' },
  { name: 'Мара', kind: 'character', description: 'Родилась на севере.', properties: [], section: 'Персонажи' },
];

/** A file of the size of a real one, by the name «большой…» (R9.4): six sections, half of the rows with properties only, most names known to the book. */
const largeFileSections: ReadonlyArray<{ section: string; kind: Candidate['kind']; count: number; bare: number }> = [
  { section: 'навыки · Искусство клинка', kind: 'skill', count: 30, bare: 20 },
  { section: 'навыки · Тени и дым', kind: 'skill', count: 24, bare: 8 },
  { section: 'навыки · Приёмы улицы', kind: 'skill', count: 22, bare: 18 },
  { section: 'квесты', kind: 'other', count: 21, bare: 0 },
  { section: 'навыки · Название, Ранг, Тип', kind: 'skill', count: 9, bare: 6 },
  { section: 'навыки · Название, Школа, Цена', kind: 'skill', count: 8, bare: 4 },
  { section: 'предметы и прочее', kind: 'item', count: 19, bare: 0 },
];
const largeNames = ['Удар', 'Шаг', 'Щит', 'Плач', 'Сон', 'Зов', 'Круг', 'Узел', 'Знак', 'След', 'Хор', 'Свет'];
const largeTails = ['в полшага', 'ветра', 'наяву', 'из ртути', 'без тени', 'старого моря', 'на излёте', 'мёртвых часов', 'за спиной', 'первого снега', 'на крови', 'вполголоса'];
const largeFile: readonly Omit<Candidate, 'match'>[] = largeFileSections
  .flatMap(({ bare, count, kind, section }) => Array.from({ length: count }, (_, at) => ({ at, bare, kind, section })))
  .map(({ at, bare, kind, section }, place) => ({
    name: `${largeNames[place % largeNames.length]} ${largeTails[Math.floor(place / largeNames.length)]}`,
    kind, section,
    description: at < bare ? '' : 'Описание из файла: что делает и когда открывается.',
    properties: [{ key: 'Ранг', value: 'SABC'[at % 4]! }, { key: 'Тип', value: at % 2 ? 'активный' : 'пассивный' }, { key: 'Затраты', value: `${(at + 1) * 5} эфира` }],
  }));
/** Of a large file: a place in the list says what the book and the materials hold of it. */
const largeBook = (at: number) => at % 8 < 5;
const largeOwn = (at: number) => at % 16 === 7;

/** The materials of the author of the mock saga; every project starts with none. A Job ends when its import is read. */
/** What the server answers about a file it does not read, by the name of the file. */
function refusedFormat(name: string): string | null {
  const extension = name.toLowerCase().split('.').pop() ?? '';
  if (['xlsx', 'xlsm', 'docx'].includes(extension)) return null;
  if (['xls', 'doc', 'rtf'].includes(extension)) return 'author_materials.old_format';
  if (['ods', 'odt'].includes(extension)) return 'author_materials.open_document';
  if (extension === 'pdf') return 'author_materials.pdf';
  if (['png', 'jpg', 'jpeg', 'gif', 'webp', 'heic'].includes(extension)) return 'author_materials.picture';
  if (['csv', 'txt', 'md'].includes(extension)) return 'author_materials.plain_text';
  return 'author_materials.unreadable';
}

export function createAuthorMaterialOperations(context: MockRepositoryContext) {
  const records = new Map<string, Material[]>();
  const imports = new Map<string, MaterialImport>();
  const replies = new Map<string, MaterialImport>();
  let nextId = 1;
  // A new scenario of the mock starts with a clean saga, so with no materials either.
  let owner = context.state;
  const ofProject = (projectId: string) => {
    if (owner !== context.state) {
      owner = context.state;
      records.clear();
      imports.clear();
      replies.clear();
    }
    return records.get(projectId) ?? [];
  };
  const materialOf = (projectId: string, id: string, operationId: 'updateAuthorMaterial' | 'deleteAuthorMaterial') => {
    const found = ofProject(projectId).find((item) => item.id === id);
    if (!found) throw new MockApiError(404, 'Record not found.', operationId);
    return found;
  };
  const matched = (projectId: string, candidate: Omit<Candidate, 'match'>): Candidate => {
    const own = ofProject(projectId).some((item) => keyOf(item.name) === keyOf(candidate.name));
    if (keyOf(candidate.name) === 'мара') return { ...candidate, match: own ? 'materials' : 'book', bookName: 'Мара' };
    return { ...candidate, match: own ? 'materials' : 'new' };
  };

  const operations: Operations = {
    async listAuthorMaterials(projectId, options: ListAuthorMaterialsOptions = {}) {
      context.requireProjectAccess('listAuthorMaterials', projectId);
      const query = options.q?.trim().toLowerCase() ?? '';
      const data = ofProject(projectId)
        .filter((item) => (!options.kind || item.kind === options.kind) && (!query || item.name.toLowerCase().includes(query) || item.description.toLowerCase().includes(query)))
        .sort((left, right) => keyOf(left.name).localeCompare(keyOf(right.name), 'ru'));
      return clone({ data, meta: { hasMore: false, nextCursor: null, totalCount: data.length } });
    },
    async updateAuthorMaterial(projectId, materialId, body: OperationRequest<'updateAuthorMaterial'>) {
      context.requireProjectAccess('updateAuthorMaterial', projectId, [...writers]);
      const found = materialOf(projectId, materialId, 'updateAuthorMaterial');
      if (found.revision !== body.expectedRevision) throw new MockApiError(409, 'The record changed.', 'updateAuthorMaterial', 'conflict');
      const updated: Material = {
        ...found, name: body.name.trim(), kind: body.kind, description: body.description, properties: body.properties,
        revision: found.revision + 1, updatedAt: new Date().toISOString(),
      };
      records.set(projectId, ofProject(projectId).map((item) => (item.id === materialId ? updated : item)));
      return clone(updated);
    },
    async deleteAuthorMaterial(projectId, materialId, expectedRevision) {
      context.requireProjectAccess('deleteAuthorMaterial', projectId, [...writers]);
      const found = materialOf(projectId, materialId, 'deleteAuthorMaterial');
      if (found.revision !== expectedRevision) throw new MockApiError(409, 'The record changed.', 'deleteAuthorMaterial', 'conflict');
      records.set(projectId, ofProject(projectId).filter((item) => item.id !== materialId));
    },
    async startAuthorMaterialsImport(projectId, input: AuthorMaterialsImportInput, options) {
      context.requireProjectAccess('startAuthorMaterialsImport', projectId, [...writers]);
      ofProject(projectId);
      const replay = replies.get(options.idempotencyKey);
      if (replay) return clone(replay);
      const running = imports.get(projectId);
      if (running && running.jobStatus === 'queued') throw new MockApiError(409, 'An import is running.', 'startAuthorMaterialsImport', 'author_materials.import_busy');
      const name = input.file?.name ?? '';
      if (input.file) {
        // The server tells a file by what it holds; the mock tells it by its name.
        const refused = input.file.size === 0 ? 'author_materials.empty' : refusedFormat(name);
        if (refused) throw new MockApiError(422, 'Not a workbook.', 'startAuthorMaterialsImport', refused);
      }
      if (!input.file && !input.text?.trim()) throw new MockApiError(422, 'Nothing to read.', 'startAuthorMaterialsImport', 'author_materials.empty');
      const id = nextId++;
      const started: MaterialImport = {
        id: `00000000-0000-4000-8000-${String(id).padStart(12, '0')}`, jobId: `00000000-0000-4000-9000-${String(id).padStart(12, '0')}`, jobStatus: 'queued',
        sourceName: name, sourceKind: name.toLowerCase().endsWith('.docx') ? 'docx' : input.file ? 'xlsx' : 'text', createdAt: new Date().toISOString(), candidates: null,
      };
      imports.set(projectId, started);
      replies.set(options.idempotencyKey, started);
      return clone(started);
    },
    async getAuthorMaterialsImport(projectId) {
      context.requireProjectAccess('getAuthorMaterialsImport', projectId);
      ofProject(projectId);
      const found = imports.get(projectId);
      if (!found) return { import: null };
      const ended: MaterialImport = {
        ...found, jobStatus: 'succeeded',
        candidates: found.sourceName.startsWith('большой')
          ? largeFile.map((candidate, at): Candidate => (
            largeOwn(at) ? { ...candidate, match: 'materials' } : largeBook(at) ? { ...candidate, match: 'book', bookName: at % 24 === 1 ? 'Мара' : candidate.name } : { ...candidate, match: 'new' }
          ))
          : fileCandidates.map((candidate) => matched(projectId, candidate)),
      };
      imports.set(projectId, ended);
      return clone({ import: ended });
    },
    async confirmAuthorMaterialsImport(projectId, importId, body, options) {
      context.requireProjectAccess('confirmAuthorMaterialsImport', projectId, [...writers]);
      const found = imports.get(projectId);
      if (!found || found.id !== importId) throw new MockApiError(404, 'No such import.', 'confirmAuthorMaterialsImport');
      if (!found.candidates) throw new MockApiError(409, 'The import is running.', 'confirmAuthorMaterialsImport', 'author_materials.import_busy');
      const at = new Date().toISOString();
      for (const item of body.items) {
        const candidate = found.candidates[item.index];
        if (!candidate || options.idempotencyKey === '') throw new MockApiError(400, 'No such candidate.', 'confirmAuthorMaterialsImport', 'validation_failed');
        const name = item.name?.trim() ?? candidate.name;
        const kept = ofProject(projectId).find((record) => keyOf(record.name) === keyOf(name));
        const record: Material = {
          id: kept?.id ?? `00000000-0000-4000-a000-${String(nextId++).padStart(12, '0')}`, name, kind: item.kind ?? candidate.kind,
          description: item.description ?? candidate.description, properties: item.properties ?? candidate.properties, section: candidate.section,
          sourceName: found.sourceName, sourceKind: found.sourceKind, revision: (kept?.revision ?? 0) + 1, createdAt: kept?.createdAt ?? at, updatedAt: at,
        };
        records.set(projectId, kept ? ofProject(projectId).map((other) => (other.id === kept.id ? record : other)) : [...ofProject(projectId), record]);
      }
      imports.delete(projectId);
      return { saved: body.items.length };
    },
    async discardAuthorMaterialsImport(projectId, importId) {
      context.requireProjectAccess('discardAuthorMaterialsImport', projectId, [...writers]);
      if (imports.get(projectId)?.id !== importId) throw new MockApiError(404, 'No such import.', 'discardAuthorMaterialsImport');
      imports.delete(projectId);
    },
  };
  return { ...operations, materialsOf: ofProject };
}
