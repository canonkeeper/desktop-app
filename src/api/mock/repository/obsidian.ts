// SPDX-License-Identifier: AGPL-3.0-only
import type { OperationRequest, OperationResponse, Schema } from '../../../shared/api';
import { MockApiError } from '../scenarios';
import type { createAuthorMaterialOperations } from './author-materials';
import type { createBookOperations } from './books';
import type { MockRepositoryContext } from './context';
import { desktopLinked } from './desktop-account';
import type { createImportQueueOperations } from './import-queues';
import { digest, knownFields, overlapOf, ownerOf, within, type MockChoice } from './obsidian-choice';
import { createObsidianMaterialsOperations, previewMaterials, tooManyRecords } from './obsidian-materials';

type Operation = 'getObsidianVault' | 'beginObsidianVaultChoice' | 'unlinkObsidianVault' | 'scanObsidianVault' | 'previewObsidianImport'
  | 'importObsidianBooks' | 'updateBookFromObsidian' | 'linkBookToObsidianFolder' | 'unlinkBookFromObsidianFolder' | 'importObsidianMaterials';
type Folder = Schema<'ObsidianTreeFolder'>;
type Book = Schema<'ObsidianBookPreview'>;

/** The vault the shell of the mock picks when a scenario names no other: the example saga «Северный предел». */
const examplePath = '/home/author/Северный предел';

/** The books of the saga the folders of the example are linked to when the scenario says so (part A2). */
const linkedBooks = {
  'Книга 1': { bookId: '7c1e4a52-3b0d-4f6e-9a21-5d8c0b7e4f13', title: 'Застава' },
  'Книга 2': { bookId: '2f9d6b18-c4a7-4e35-8b02-61e7d3a9c5f4', title: 'Белый порт' },
} as const;

/** A live book of the scenario whose folder «Книга 1» holds: to link anew (specs/2026-11-14-desk-obsidian-books.md OK2.6). */
const relinkedBook = { bookId: '5a7b9c1d-2e4f-4a6b-8c0d-1e2f3a4b5c6d', title: 'Застава (прежняя)', folder: 'Книга 1' } as const;

/** When the scenario says the folder of «Книга 1» was last loaded. */
const loadedAt = '2026-11-14T06:00:00Z';

/** The tree of the example vault (OB2.1, OB2.2): its folders with notes, the root first, with what each skipped. */
const exampleTree: Folder[] = [
  { path: '.', name: 'Северный предел', depth: 0, notes: 0, below: 30, skipped: [{ reason: 'hidden', count: 1 }, { reason: 'link', count: 1 }] },
  {
    path: 'Книга 1', name: 'Книга 1', depth: 1, notes: 5, below: 0,
    skipped: [{ reason: 'not_note', count: 1 }, { reason: 'service', count: 1 }, { reason: 'too_big', count: 1 }], hint: { kind: 'book' },
  },
  {
    path: 'Книга 2', name: 'Книга 2', depth: 1, notes: 2, below: 0, skipped: [{ reason: 'cloud_only', count: 1 }, { reason: 'link', count: 1 }],
    hint: { kind: 'skip' },
  },
  { path: 'Книга 3', name: 'Книга 3', depth: 1, notes: 2, below: 0, skipped: [], hint: { kind: 'skip' } },
  { path: 'Книга 4', name: 'Книга 4', depth: 1, notes: 2, below: 0, skipped: [], hint: { kind: 'book' } },
  { path: 'Персонажи', name: 'Персонажи', depth: 1, notes: 5, below: 0, skipped: [], hint: { kind: 'materials', material: 'character' } },
  { path: 'Места', name: 'Места', depth: 1, notes: 5, below: 0, skipped: [], hint: { kind: 'materials', material: 'place' } },
  { path: 'Шаблоны', name: 'Шаблоны', depth: 1, notes: 1, below: 0, skipped: [{ reason: 'not_note', count: 2 }], hint: { kind: 'skip' } },
  // A book with parts and records inside it (specs/2026-11-15-desk-obsidian-web.md OW7.2): its folders of the second level
  // are hinted «skip» like every other one, and one folder lies deeper.
  { path: 'Книга 5', name: 'Книга 5', depth: 1, notes: 1, below: 7, skipped: [], hint: { kind: 'book' } },
  { path: 'Книга 5/Герои', name: 'Герои', depth: 2, notes: 2, below: 0, skipped: [], hint: { kind: 'materials', material: 'character' } },
  { path: 'Книга 5/Часть 1', name: 'Часть 1', depth: 2, notes: 2, below: 0, skipped: [], hint: { kind: 'skip' } },
  { path: 'Книга 5/Часть 2', name: 'Часть 2', depth: 2, notes: 2, below: 1, skipped: [], hint: { kind: 'skip' } },
  { path: 'Книга 5/Часть 2/Черновики', name: 'Черновики', depth: 3, notes: 1, below: 0, skipped: [] },
];

const nothingDropped: Book['dropped'] = { footnotes: 0, embeds: 0, comments: 0, unpairedComments: 0, pluginBlocks: 0, tables: 0, frontKeys: 0 };
const newFolder: Book['state'] = { kind: 'new', changed: false, inTrash: false };

/** A long title of a book, cut to what the import takes as a heading (OB3.3). */
const longTitle = 'Очень длинное название тома о заставе на перевале, о переправе через реку и о весне, которая пришла на север позже обычного';

/**
 * The books of the folders of the example as the import reads their glued text (OB4.2): every warning of a book, what did
 * not go into the text and what was skipped by every reason a note of a book has.
 */
const exampleBooks: { [folder: string]: Omit<Book, 'folder'> } = {
  'Книга 1': {
    title: 'Застава',
    notes: [
      { path: 'Книга 1/01 Пролог.md', title: 'Пролог', orderBy: 'index', chapters: ['Пролог'], inNote: false },
      { path: 'Книга 1/02 Переправа.md', title: 'Переправа', orderBy: 'index', chapters: ['Переправа'], inNote: false },
    ],
    chapters: [{ title: 'Пролог', characters: 26 }, { title: 'Переправа', characters: 23 }, { title: 'Интерлюдия', characters: 24 }],
    characters: 73,
    warnings: [{ code: 'chapters_differ', notes: ['Книга 1/02 Переправа.md'] }],
    dropped: { ...nothingDropped, embeds: 1, comments: 2, footnotes: 1 },
    skipped: [{ reason: 'empty', count: 1 }, { reason: 'encoding', count: 1 }, { reason: 'service', count: 1 }, { reason: 'too_big', count: 1 }],
    state: newFolder,
    fingerprint: '6f1c2b0e9a8d7c6b5a4f3e2d1c0b9a8f7e6d5c4b3a2f1e0d9c8b7a6f5e4d3c2b',
  },
  'Книга 2': {
    title: 'Книга 2',
    notes: [{ path: 'Книга 2/Том.md', title: 'Том', orderBy: 'name', chapters: ['Белый порт', 'Чёрный берег'], inNote: true }],
    chapters: [{ title: 'Белый порт', characters: 19 }, { title: 'Чёрный берег', characters: 15 }],
    characters: 34,
    warnings: [{ code: 'book_in_note', notes: ['Книга 2/Том.md'] }],
    dropped: nothingDropped,
    skipped: [{ reason: 'cloud_only', count: 1 }, { reason: 'link', count: 1 }, { reason: 'unreadable', count: 1 }],
    state: newFolder,
    fingerprint: '1d2c3b4a59687766554433221100ffeeddccbbaa99887766554433221100ffee',
  },
  'Книга 3': {
    title: 'Книга 3',
    notes: [
      { path: 'Книга 3/Весна.md', title: 'Весна', orderBy: 'name', chapters: ['Весна'], inNote: false },
      { path: 'Книга 3/Осень.md', title: 'Осень', orderBy: 'name', chapters: ['Осень'], inNote: false },
    ],
    chapters: [{ title: 'Весна', characters: 20 }, { title: 'Осень', characters: 18 }],
    characters: 38,
    warnings: [{ code: 'order_by_name', notes: [] }],
    dropped: nothingDropped,
    skipped: [],
    state: newFolder,
    fingerprint: '9e8d7c6b5a4f3e2d1c0b9a8f7e6d5c4b3a2f1e0d9c8b7a6f5e4d3c2b1a0f9e8d',
  },
  'Книга 4': {
    title: longTitle,
    notes: [{ path: 'Книга 4/Глава.md', title: 'Глава', orderBy: 'name', chapters: ['Глава'], inNote: false }],
    chapters: [{ title: 'Глава', characters: 12 }],
    characters: 12,
    warnings: [{ code: 'title_cut', notes: [] }],
    dropped: { ...nothingDropped, tables: 1, pluginBlocks: 1, unpairedComments: 1, frontKeys: 2 },
    skipped: [],
    state: newFolder,
    fingerprint: '3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7f8091a2b',
  },
  // Every note under the folder: which of them are chapters is said by the choice (its folders of records, its exclusions).
  'Книга 5': {
    title: 'Долгая зима',
    notes: [
      { path: 'Книга 5/Часть 1/01 Встреча.md', title: 'Встреча', orderBy: 'index', chapters: ['Встреча'], inNote: false },
      { path: 'Книга 5/Часть 1/02 Дорога.md', title: 'Дорога', orderBy: 'index', chapters: ['Дорога'], inNote: false },
      { path: 'Книга 5/Часть 2/03 Мост.md', title: 'Мост', orderBy: 'index', chapters: ['Мост'], inNote: false },
      { path: 'Книга 5/Часть 2/04 Зимовье.md', title: 'Зимовье', orderBy: 'index', chapters: ['Зимовье'], inNote: false },
      { path: 'Книга 5/Часть 2/Черновики/Набросок.md', title: 'Набросок', orderBy: 'name', chapters: ['Набросок'], inNote: false },
      { path: 'Книга 5/Герои/Тим.md', title: 'Тим', orderBy: 'name', chapters: ['Тим'], inNote: false },
      { path: 'Книга 5/Герои/Сава.md', title: 'Сава', orderBy: 'name', chapters: ['Сава'], inNote: false },
    ],
    chapters: [],
    characters: 0,
    warnings: [],
    dropped: nothingDropped,
    skipped: [],
    state: newFolder,
    fingerprint: '',
  },
};

/** The folders and the notes as part A2 keeps them linked, when the scenario says so. */
function linkedState(folder: string, book: Book['state']): Book['state'] {
  switch (folder) {
    case 'Книга 1':
      return { kind: 'linked', ...linkedBooks['Книга 1'], changed: true, inTrash: false };
    case 'Книга 2':
      return { kind: 'linked', ...linkedBooks['Книга 2'], changed: false, inTrash: true };
    default:
      return book;
  }
}

/**
 * A path of a request as the server reads it (OB0.3): relative, by «/», in NFC and otherwise as it is — a space at the edge
 * of a name is a part of it —, without names on a dot, backslashes or colons.
 */
function relative(value: string): string | null {
  const path = value.normalize('NFC');
  if (path === '' || path === '.') return '.';
  if (path.startsWith('/') || path.endsWith('/') || /[\\:]/u.test(path) || [...path].some((sign) => sign.charCodeAt(0) < 0x20)) return null;
  return path.split('/').every((name) => name !== '' && !name.startsWith('.')) ? path : null;
}

/**
 * The bridge with an Obsidian vault in the mock (specs/2026-11-13-desk-obsidian.md): there is no shell and no vault, so a
 * choice ends as the scenario says — by default the shell picks the example vault and the saga is linked —, the tree and
 * the preview are those of the example — every warning, every reason of a skip and every state of a new load —, the
 * scenario links its folders and notes as part A2 keeps them, and makes the vault answer as every refusal of Data.
 */
export function createObsidianOperations(
  context: MockRepositoryContext,
  books: Pick<ReturnType<typeof createBookOperations>, 'importBookFile'>,
  queues: Pick<ReturnType<typeof createImportQueueOperations>, 'createImportQueue' | 'uploadImportQueueItem' | 'startImportQueue' | 'getImportQueue'>,
  materials: Pick<ReturnType<typeof createAuthorMaterialOperations>, 'obsidianRecords'>,
) {
  function saga(operation: Operation, projectId: string, write: boolean) {
    context.applyOperation(operation);
    if (!context.state.session) throw new MockApiError(401, 'Сессия отсутствует.', operation, 'auth.session_required');
    context.projectFor(projectId, operation);
    if (write && context.actorRole(projectId) === 'viewer') {
      throw new MockApiError(403, 'the role does not change the books of the saga', operation, 'forbidden');
    }
  }

  /** A saga linked with a vault, or not_linked. */
  function linkedOf(operation: Operation, projectId: string) {
    if (!context.state.obsidianVaults[projectId]) throw new MockApiError(409, 'the saga is not linked with a vault', operation, 'obsidian.not_linked');
  }

  /** The place of the saga under the fence (OB0.1): another operation reads its vault — busy, before the answer of a key. */
  function free(operation: Operation) {
    if (context.getScenario().obsidianVault === 'busy') {
      throw new MockApiError(429, 'another operation reads a vault', operation, 'obsidian.busy', { retryAfter: 5 });
    }
  }

  /** The notes of the example as they are now: edited since a load, by the scenario. */
  function edited() {
    return context.getScenario().obsidianEdited === true;
  }

  /** The vault read in the place: it does not open, or does not answer in time. */
  function readable(operation: Operation) {
    const vault = context.getScenario().obsidianVault;
    if (vault && typeof vault === 'object') {
      throw new MockApiError(409, 'the vault does not open', operation, 'obsidian.vault_missing', { fields: { vaultReason: vault.missing } });
    }
    if (vault === 'slow') throw new MockApiError(503, 'the vault did not answer in time', operation, 'obsidian.slow');
  }

  /** The vault of a linked saga as it answers now, by the scenario. */
  function opened(operation: Operation, projectId: string) {
    linkedOf(operation, projectId);
    readable(operation);
    free(operation);
  }

  /** The folders the books of the saga hold — live, in «Недавно удалённые», volumes in work — and a refusal of a choice around them. */
  function overlapping(operation: Operation, projectId: string, books: readonly string[], materials: readonly string[], held: boolean) {
    const folders = overlapOf(books, materials, held ? holders(projectId).map(([folder]) => folder) : []);
    if (folders) throw new MockApiError(422, 'the folders overlap', operation, 'obsidian.folders_overlap', { fields: { folders } });
  }

  // specs/2026-11-15-desk-obsidian-notes.md: the records of the notes of the folders of materials.
  const { memoryOf, ...notes } = createObsidianMaterialsOperations(context, {
    saga: (projectId, write) => saga('importObsidianMaterials', projectId, write),
    linked: (projectId) => linkedOf('importObsidianMaterials', projectId),
    free: () => free('importObsidianMaterials'),
    readable: () => readable('importObsidianMaterials'),
    overlapping: (projectId, books, folders, held) => overlapping('importObsidianMaterials', projectId, books, folders, held),
    inVault: (path) => exampleTree.some((one) => one.path === path),
    relative,
  }, materials.obsidianRecords);

  return {
    ...notes,
    async getObsidianVault(projectId: string): Promise<OperationResponse<'getObsidianVault', '200'>> {
      saga('getObsidianVault', projectId, false);
      const vault = context.state.obsidianVaults[projectId];
      const choice = context.state.obsidianChoices[projectId];
      const answer: OperationResponse<'getObsidianVault', '200'> = { linked: !!vault, choosing: !!choice?.request };
      if (choice?.refused) answer.refused = choice.refused;
      if (vault) {
        const now = context.getScenario().obsidianVault;
        Object.assign(answer, { path: vault.path, name: vault.name, availability: typeof now === 'object' ? 'missing' : now === 'unknown' ? 'unknown' : 'available' });
        // OK2.6: the linked books and the books to link anew, the folders by the one check of the vault.
        const presence = (folder: string) => typeof now === 'object' || now === 'unknown'
          ? 'unknown' as const
          : exampleTree.some((one) => one.path === folder) ? 'present' as const : 'missing' as const;
        // specs/2026-11-15-desk-obsidian-web.md OW2.5: with the composition each link keeps.
        const linked = holders(projectId).flatMap(([folder, held]) => held.kind === 'linked'
          ? [{
            bookId: held.bookId!, title: held.title!, folder, loadedAt: held.loadedAt ?? null, presence: presence(folder),
            excluded: [...held.excluded ?? []], nested: [...held.nested ?? []],
          }]
          : []);
        if (linked.length) answer.books = linked;
        const relink = holders(projectId).flatMap(([folder, held]) => (held.kind === 'linked' ? held.others ?? [] : [])
          .map((other) => ({ ...other, folder, loadedAt: null, presence: presence(folder), excluded: [], nested: [] })));
        if (context.getScenario().obsidianLinked) relink.push({ ...relinkedBook, loadedAt: null, presence: presence(relinkedBook.folder), excluded: [], nested: [] });
        if (relink.length) answer.relink = relink;
      }
      return answer;
    },

    async beginObsidianVaultChoice(projectId: string): Promise<OperationResponse<'beginObsidianVaultChoice', '202'>> {
      saga('beginObsidianVaultChoice', projectId, true);
      const shell = context.getScenario().obsidianShell;
      if (shell === 'absent') throw new MockApiError(503, 'no window chooses a folder', 'beginObsidianVaultChoice', 'obsidian.choice_unavailable');
      const request = btoa(String.fromCharCode(...crypto.getRandomValues(new Uint8Array(32))))
        .replaceAll('+', '-').replaceAll('/', '_').replace(/=+$/u, '');
      const choice = { request, refused: context.state.obsidianChoices[projectId]?.refused ?? null };
      // The shell of the mock answers at once — unless the scenario keeps the window open.
      if (shell === 'waits') {
        context.state.obsidianChoices[projectId] = choice;
      } else if (shell && typeof shell === 'object' && 'refused' in shell) {
        context.state.obsidianChoices[projectId] = { request: null, refused: shell.refused };
      } else {
        const path = shell && typeof shell === 'object' ? shell.picked : examplePath;
        context.state.obsidianVaults[projectId] = { path, name: path.split(/[\\/]/u).filter(Boolean).pop() ?? path };
        context.state.obsidianChoices[projectId] = { request: null, refused: null };
      }
      return { request };
    },

    async unlinkObsidianVault(projectId: string): Promise<void> {
      saga('unlinkObsidianVault', projectId, true);
      delete context.state.obsidianVaults[projectId];
      // specs/2026-11-14-desk-obsidian-books.md OK0.2: the links of the books go with the link of the vault.
      delete context.state.obsidianBooks[projectId];
      // specs/2026-11-15-desk-obsidian-notes.md ON1.9: and the links of the notes (OW7.2: their records stay, of another source).
      delete context.state.obsidianNotes[projectId];
    },

    async scanObsidianVault(projectId: string): Promise<OperationResponse<'scanObsidianVault', '200'>> {
      saga('scanObsidianVault', projectId, true);
      opened('scanObsidianVault', projectId);
      const scenario = context.getScenario();
      const held = new Map(holders(projectId));
      const folders = structuredClone(exampleTree).map((folder) => {
        const linked = held.get(folder.path);
        const book = linked?.bookId ? { bookId: linked.bookId, title: linked.title! } : undefined;
        return book ? { ...folder, book, hint: { kind: 'book' as const } } : folder;
      });
      const skipped: Schema<'ObsidianSkipCount'>[] = [
        { reason: 'cloud_only', count: 1 }, { reason: 'hidden', count: 1 }, { reason: 'link', count: 2 }, { reason: 'not_note', count: 3 },
        { reason: 'service', count: 1 }, { reason: 'too_big', count: 1 },
      ];
      // OB2.1: a look past its limits gives the tree it walked with the entries it counted, not a refusal.
      return scenario.obsidianVault === 'truncated'
        ? { folders, truncated: true, entries: 200_000, skipped }
        : { folders, truncated: false, entries: 34, skipped };
    },

    async previewObsidianImport(projectId: string, body: OperationRequest<'previewObsidianImport'>): Promise<OperationResponse<'previewObsidianImport', '200'>> {
      saga('previewObsidianImport', projectId, true);
      const invalid = () => new MockApiError(400, 'a folder is not a folder of the vault', 'previewObsidianImport', 'request.validation');
      if (!knownFields(body, { books: null, materials: ['path', 'kind'], exclude: null })) throw invalid();
      // A book named twice is named once (domain/obsidian Check).
      const books = [...new Set((body.books ?? []).map((folder) => relative(folder)))];
      const materials = (body.materials ?? []).map((material) => ({ path: relative(material.path), kind: material.kind ?? 'other' }));
      if (books.length + materials.length === 0 || books.includes(null) || materials.some((material) => material.path === null) ||
        (body.exclude ?? []).some((path) => relative(path) === null)) throw invalid();
      linkedOf('previewObsidianImport', projectId);
      overlapping('previewObsidianImport', projectId, books.map((folder) => folder!), materials.map((material) => material.path!), true);
      free('previewObsidianImport');
      readable('previewObsidianImport');
      const scenario = context.getScenario();
      if (scenario.obsidianVault === 'too_large') {
        throw new MockApiError(422, 'the chosen folders are too large', 'previewObsidianImport', 'obsidian.too_large', {
          fields: { notes: 20_412, bytes: 96_468_992, limitNotes: 20_000, limitBytes: 209_715_200 },
        });
      }
      if (scenario.obsidianVault === 'too_many_entries') {
        throw new MockApiError(422, 'the chosen folders are too large', 'previewObsidianImport', 'obsidian.too_large', {
          fields: { notes: 1_204, bytes: 9_437_184, limitNotes: 20_000, limitBytes: 209_715_200, entries: 200_001, limitEntries: 200_000 },
        });
      }
      const known = (folder: string | null) => exampleTree.some((one) => one.path === folder);
      if (!books.every(known) || !materials.every((material) => known(material.path))) throw invalid();
      // specs/2026-11-15-desk-obsidian-notes.md ON1.7: more records than a load or a saga keeps.
      if (scenario.obsidianVault === 'too_many_records' && materials.length) throw tooManyRecords('previewObsidianImport');
      const choice: MockChoice = {
        books: books.map((folder) => folder!),
        materials: materials.map((material) => ({ path: material.path!, kind: material.kind })),
        exclude: (body.exclude ?? []).map((path) => relative(path)!),
      };
      const previewed: Book[] = books.map((folder) => {
        const book: Book = { folder: folder!, ...glueOf(folder!, choice, edited()) };
        return { ...book, state: stateOf(projectId, folder!, book.state, book.fingerprint) };
      });
      const { source } = context.state.aiSettings;
      // Who reads the books, by the one helper of the build (ownAI.reads): the account only while the app is linked.
      const helper = source === 'own' ? 'own' : source === 'account' && context.state.aiAccount && desktopLinked(context) ? 'account' : 'none';
      // OB4.4: what the helper is to read — the new books and the books changed since their load.
      const read = previewed.filter((book) => book.state.kind === 'new' || book.state.changed);
      return {
        books: previewed,
        materials: previewMaterials(scenario.obsidianLinked === true, choice, memoryOf(projectId)),
        reading: {
          newChapters: read.reduce((sum, book) => sum + book.chapters.length, 0),
          newCharacters: read.reduce((sum, book) => sum + book.characters, 0),
          helper, consent: helper === 'own' && scenario.consentMissing !== true,
        },
      };
    },

    async importObsidianBooks(
      projectId: string, body: OperationRequest<'importObsidianBooks'>, options: { idempotencyKey: string },
    ): Promise<OperationResponse<'importObsidianBooks', '200'>> {
      saga('importObsidianBooks', projectId, true);
      const invalid = () => new MockApiError(400, 'validation failed', 'importObsidianBooks', 'request.validation');
      if (!knownFields(body, { books: ['folder', 'fingerprint'], materials: ['path', 'kind'], exclude: null })) throw invalid();
      const folders = (body.books ?? []).map((book) => relative(book.folder));
      if (!folders.length || folders.includes(null) || new Set(folders).size !== folders.length ||
        (body.books ?? []).some((book) => !fingerprinted(book.fingerprint)) ||
        (body.materials ?? []).some((material) => relative(material.path) === null) || (body.exclude ?? []).some((path) => relative(path) === null)) {
        throw invalid();
      }
      // The order of the server (OK1.6): the link, the request, the place of the saga — busy before the answer of the key —,
      // then the answer of the key, the folders of the saga, the vault and the fingerprints.
      linkedOf('importObsidianBooks', projectId);
      const materialPaths = (body.materials ?? []).map((material) => relative(material.path)!);
      overlapping('importObsidianBooks', projectId, folders.map((folder) => folder!), materialPaths, false);
      free('importObsidianBooks');
      return context.idempotentCommand('importObsidianBooks', projectId, options, body, async () => {
        const write = context.getScenario().obsidianWrite;
        if (write === 'load_interrupted') throw new MockApiError(409, 'the load of the key was interrupted', 'importObsidianBooks', 'obsidian.load_interrupted');
        overlapping('importObsidianBooks', projectId, folders.map((folder) => folder!), materialPaths, true);
        readable('importObsidianBooks');
        // A folder gone since its preview (sincePreview): folder_changed.
        const gone = folders.filter((folder) => !exampleTree.some((one) => one.path === folder)).map((folder) => folder!);
        if (gone.length) {
          throw new MockApiError(409, 'a folder changed since its preview', 'importObsidianBooks', 'obsidian.folder_changed', { fields: { folders: gone } });
        }
        const choice: MockChoice = {
          books: folders.map((folder) => folder!),
          materials: (body.materials ?? []).map((material) => ({ path: relative(material.path)!, kind: material.kind ?? 'other' })),
          exclude: (body.exclude ?? []).map((path) => relative(path)!),
        };
        // OK1.1: each folder is glued again by the choice; a glue other than the one of the preview is folder_changed.
        const changed = folders.filter((folder, index) => body.books[index]!.fingerprint !== glueOf(folder!, choice, edited()).fingerprint).map((folder) => folder!);
        if (changed.length) {
          throw new MockApiError(409, 'a folder changed since its preview', 'importObsidianBooks', 'obsidian.folder_changed', { fields: { folders: changed } });
        }
        const held = new Map(holders(projectId));
        const outcomes: Schema<'ObsidianFolderOutcome'>[] = folders.map((folder) => {
          const link = held.get(folder!);
          switch (link?.kind) {
            case 'linked': return { folder: folder!, outcome: 'linked', bookId: link.bookId!, title: link.title! };
            case 'in_trash': return { folder: folder!, outcome: 'in_trash', bookId: link.bookId!, title: link.title! };
            case 'queued': return { folder: folder!, outcome: 'queued', queueId: link.queueId! };
            default:
          }
          const refusal = glueOf(folder!, choice, edited()).refusal;
          return refusal ? { folder: folder!, outcome: 'refused', refusal } : { folder: folder!, outcome: 'queued' };
        });
        const volumes = outcomes.filter((outcome) => outcome.outcome === 'queued' && !outcome.queueId);
        if (!volumes.length) return { queue: null, folders: outcomes };
        // OK1.3: the queue of the core is the first write; a live queue of the saga refuses it with nothing written.
        if (write === 'queue_busy') throw queueBusy('importObsidianBooks');
        // OW7.2: the volumes are a queue of «Книги» of the mock — a file of the glue each, in the order of the request — that
        // gives books as any upload does; the link of a folder names its volume.
        const files = volumes.map((volume) => {
          const glue = glueOf(volume.folder, choice, edited());
          const text = [`# ${glue.title}`, ...glue.chapters.flatMap((chapter) => [`## ${chapter.title}`, 'Текст главы.'])].join('\n\n');
          return new File([text], `${glue.title}.md`, { type: 'text/markdown' });
        });
        // The writes of the queue go by keys of their own, derived from the key of the load.
        const loadKey = options.idempotencyKey;
        const keyOf = (what: string) => {
          const derived = ['obsidian', what, loadKey].join('-').slice(0, 128);
          return { idempotencyKey: derived };
        };
        let created;
        try {
          created = await queues.createImportQueue(projectId, {
            items: files.map((file, index) => ({ clientItemId: `obsidian-${index + 1}`, position: index + 1, sourceFileName: file.name })),
          }, keyOf('queue'));
        } catch (error) {
          if (error instanceof MockApiError && error.code === 'active_import_queue_exists') throw queueBusy('importObsidianBooks');
          throw error;
        }
        for (const [index, item] of created.items.entries()) {
          await queues.uploadImportQueueItem(projectId, created.id, { itemId: item.id, file: files[index]! }, keyOf(`volume-${item.id}`));
        }
        const uploaded = await queues.getImportQueue(projectId, created.id);
        await queues.startImportQueue(projectId, created.id, { expectedVersion: uploaded.version }, keyOf('start'));
        const links = (context.state.obsidianBooks[projectId] ??= []);
        for (const [index, volume] of volumes.entries()) {
          volume.queueId = created.id;
          links.push({
            folder: volume.folder, queueId: created.id, itemId: created.items[index]!.id, at: links.length + 1, ...compositionOf(choice, volume.folder),
            fingerprint: glueOf(volume.folder, choice, edited()).fingerprint,
          });
        }
        return { queue: { id: created.id, status: 'running' as const }, folders: outcomes };
      });
    },

    async updateBookFromObsidian(
      projectId: string, bookId: string, body: OperationRequest<'updateBookFromObsidian'>, options: { idempotencyKey: string },
    ): Promise<OperationResponse<'updateBookFromObsidian', '202'>> {
      saga('updateBookFromObsidian', projectId, true);
      const apply = body.mode === 'apply';
      if ((body.mode !== 'preview' && !apply) || (apply ? !fingerprinted(body.expectedFingerprint) : body.expectedFingerprint !== undefined || body.confirmUnread !== undefined)) {
        throw new MockApiError(400, 'validation failed', 'updateBookFromObsidian', 'request.validation');
      }
      return context.idempotentCommand('updateBookFromObsidian', `${projectId}:${bookId}`, options, body, async () => {
        opened('updateBookFromObsidian', projectId);
        const write = context.getScenario().obsidianWrite;
        // OK2.4: a live queue of the saga is queue_busy before the folder is read.
        if (write === 'queue_busy') throw queueBusy('updateBookFromObsidian');
        const [folder, held] = holders(projectId).find(([, one]) => one.kind === 'linked' && one.bookId === bookId) ?? [];
        if (!folder || !held) throw new MockApiError(409, 'the book is not linked with a folder', 'updateBookFromObsidian', 'obsidian.book_not_linked');
        if (write === 'folder_missing' || !exampleTree.some((one) => one.path === folder)) {
          throw new MockApiError(409, 'the folder is not in the vault', 'updateBookFromObsidian', 'obsidian.folder_missing', { fields: { folders: [folder] } });
        }
        // OK2.1: the folder glued by the composition of its link, as the notes are now.
        const glue = glueOf(folder, { books: [folder], materials: (held.nested ?? []).map((path) => ({ path, kind: 'other' })), exclude: held.excluded ?? [] }, edited());
        // The book has the glue of its load or of its last update: the same glue changes nothing; a link without a load changes it.
        const changed = write !== 'nothing_to_apply' && (held.fingerprint === undefined || held.fingerprint !== glue.fingerprint);
        const report: Book = {
          folder, ...glue,
          skipped: write === 'notes_unread' ? [...glue.skipped, { reason: 'cloud_only', count: 1 }] : glue.skipped,
          state: { kind: 'linked', bookId, title: context.state.books.find((one) => one.id === bookId)?.title ?? glue.title, changed, inTrash: false },
        };
        if (apply) {
          // OK2.2: the glue of the preview, the word of the author on the notes that did not read, a plan that changes the book.
          if (body.expectedFingerprint !== glue.fingerprint) {
            throw new MockApiError(409, 'a folder changed since its preview', 'updateBookFromObsidian', 'obsidian.folder_changed', { fields: { folders: [folder] } });
          }
          if (write === 'notes_unread' && !body.confirmUnread) {
            throw new MockApiError(409, 'notes of the folder did not read', 'updateBookFromObsidian', 'obsidian.notes_unread', {
              fields: { unread: [{ reason: 'cloud_only', count: 1 }] },
            });
          }
          if (write === 'nothing_to_apply') throw new MockApiError(409, 'the update changes nothing', 'updateBookFromObsidian', 'obsidian.nothing_to_apply');
        }
        // OK2.1: the update is «Обновить из файла» of the core with the glued text.
        const text = [`# ${glue.title}`, ...glue.chapters.flatMap((chapter) => [`# ${chapter.title}`, 'Текст главы.'])].join('\n\n');
        const started = await books.importBookFile(projectId, {
          file: new File([text], `${glue.title}.md`, { type: 'text/markdown' }),
          options: { importMode: apply ? 'update_book' : 'update_preview', bookId },
        }, { idempotencyKey: `idem-obsidian-${body.mode}-${options.idempotencyKey.slice(0, 100)}` });
        // The book takes the glue of the folder: the next preview has no changes until the notes change again.
        const link = apply ? linkOf(projectId, bookId) : undefined;
        if (link) link.fingerprint = glue.fingerprint;
        return apply ? started : { ...started, fingerprint: glue.fingerprint, book: report };
      });
    },

    async linkBookToObsidianFolder(projectId: string, bookId: string, body: OperationRequest<'linkBookToObsidianFolder'>): Promise<void> {
      saga('linkBookToObsidianFolder', projectId, true);
      const folder = relative(body.folder);
      if (folder === null) throw new MockApiError(400, 'validation failed', 'linkBookToObsidianFolder', 'request.validation');
      opened('linkBookToObsidianFolder', projectId);
      if (context.getScenario().obsidianWrite === 'queue_busy') throw queueBusy('linkBookToObsidianFolder');
      if (!context.state.books.some((book) => book.id === bookId && book.projectId === projectId)) {
        throw new MockApiError(404, 'saga not found', 'linkBookToObsidianFolder', 'not_found');
      }
      // OK2.3: no other live book holds the folder, and it lies in none of the folders of the saga and holds none.
      for (const [other, held] of holders(projectId)) {
        if (held.bookId === bookId && held.kind === 'linked') continue;
        if (other === folder && held.kind === 'linked') {
          throw new MockApiError(409, 'another book holds the folder', 'linkBookToObsidianFolder', 'obsidian.folder_linked', { fields: { folders: [folder], bookId: held.bookId! } });
        }
        if (other !== folder && (within(other, folder) || within(folder, other))) {
          const [outer, inner] = within(other, folder) ? [other, folder] : [folder, other];
          throw new MockApiError(422, 'the folders overlap', 'linkBookToObsidianFolder', 'obsidian.folders_overlap', { fields: { folders: [outer, inner] } });
        }
      }
      if (!exampleTree.some((one) => one.path === folder)) {
        throw new MockApiError(409, 'the folder is not in the vault', 'linkBookToObsidianFolder', 'obsidian.folder_missing', { fields: { folders: [folder] } });
      }
      const links = (context.state.obsidianBooks[projectId] ??= []).filter((link) => link.bookId !== bookId);
      links.push({ folder, bookId, at: Math.max(0, ...links.map((link) => link.at)) + 1 });
      context.state.obsidianBooks[projectId] = links;
    },

    async unlinkBookFromObsidianFolder(projectId: string, bookId: string): Promise<void> {
      saga('unlinkBookFromObsidianFolder', projectId, true);
      if (!context.state.obsidianVaults[projectId]) throw new MockApiError(409, 'the saga is not linked with a vault', 'unlinkBookFromObsidianFolder', 'obsidian.not_linked');
      context.state.obsidianBooks[projectId] = (context.state.obsidianBooks[projectId] ?? []).filter((link) => link.bookId !== bookId);
    },
  };

  /** The link of a book: linked without a load, or the volume of the load that gave it. */
  function linkOf(projectId: string, bookId: string) {
    return (context.state.obsidianBooks[projectId] ?? []).filter((link) => {
      if (link.bookId) return link.bookId === bookId;
      const queue = context.state.importQueues.find((one) => one.id === link.queueId);
      return queue?.items.find((one) => one.id === link.itemId)?.bookId === bookId;
    }).sort((left, right) => right.at - left.at)[0];
  }

  /**
   * What holds each folder of the saga (specs/2026-11-14-desk-obsidian-books.md OK0.3): the latest link of a folder whose book
   * is live, else one whose book lies in «Недавно удалённые», else a volume in work; the scenario links the folders of the
   * example as part A2 keeps them.
   */
  function holders(projectId: string): Array<[string, Held]> {
    const held = new Map<string, Held>();
    if (context.getScenario().obsidianLinked) {
      held.set('Книга 1', { kind: 'linked', ...linkedBooks['Книга 1'], loadedAt, excluded: [], nested: [] });
      held.set('Книга 2', { kind: 'in_trash', ...linkedBooks['Книга 2'] });
    }
    const links = [...(context.state.obsidianBooks[projectId] ?? [])].sort((left, right) => left.at - right.at);
    for (const link of links) {
      const live = context.state.books.find((book) => book.id === link.bookId && book.projectId === projectId);
      const trashed = context.state.deletedBooks.find((source) => source.resource.id === link.bookId);
      if (live) {
        // OK2.6: a live book whose folder a later link of another live book holds is to be linked anew.
        const before = held.get(link.folder);
        const others = [...before?.others ?? [], ...before?.kind === 'linked' && before.bookId !== live.id ? [{ bookId: before.bookId!, title: before.title! }] : []]
          .filter((other) => other.bookId !== live.id);
        held.set(link.folder, { kind: 'linked', bookId: live.id, title: live.title, excluded: [], nested: [], others, fingerprint: link.fingerprint });
      }
      else if (trashed && held.get(link.folder)?.kind !== 'linked') held.set(link.folder, { kind: 'in_trash', bookId: trashed.resource.id, title: trashed.resource.title });
      else if (link.queueId && !held.has(link.folder)) {
        // OK0.3: a volume of the bridge holds its folder while it is in work, and its book once it gave one.
        const queue = context.state.importQueues.find((one) => one.id === link.queueId);
        const item = queue?.items.find((one) => one.id === link.itemId);
        const book = item?.bookId ? context.state.books.find((one) => one.id === item.bookId && one.projectId === projectId) : undefined;
        if (book) {
          held.set(link.folder, { kind: 'linked', bookId: book.id, title: book.title, excluded: link.excluded ?? [], nested: link.nested ?? [], fingerprint: link.fingerprint });
        }
        else if (!queue || ['uploading', 'draft', 'running'].includes(queue.status)) held.set(link.folder, { kind: 'queued', queueId: link.queueId });
      }
    }
    return [...held];
  }

  /**
   * The state of the folder of a book in a preview: new, linked — changed when the glue of the choice of the request is not
   * the glue of the composition of its load (the notes of the example do not change; specs/2026-11-15-desk-obsidian-web.md
   * OW2.5), changed since its load in the scenario —, or queued.
   */
  function stateOf(projectId: string, folder: string, book: Book['state'], fingerprint: string): Book['state'] {
    const held = new Map(holders(projectId)).get(folder);
    if (context.getScenario().obsidianLinked && (folder === 'Книга 1' || folder === 'Книга 2')) return linkedState(folder, book);
    switch (held?.kind) {
      case 'linked': {
        // The glue the book has — of its load or its last update —, else the glue of the composition of its link now.
        const loaded = held.fingerprint
          ?? glueOf(folder, { books: [folder], materials: (held.nested ?? []).map((path) => ({ path, kind: 'other' })), exclude: held.excluded ?? [] }, edited()).fingerprint;
        return { kind: 'linked', bookId: held.bookId!, title: held.title!, changed: loaded !== fingerprint, inTrash: false };
      }
      case 'in_trash':
        return { kind: 'linked', bookId: held.bookId!, title: held.title!, changed: false, inTrash: true };
      case 'queued':
        return { kind: 'queued', queueId: held.queueId!, changed: false, inTrash: false };
      default:
        return book;
    }
  }

  function queueBusy(operation: Operation) {
    return new MockApiError(409, 'the saga has a live queue of the volumes', operation, 'obsidian.queue_busy', {
      fields: { queue: { id: 'import-queue-1', status: 'running' } },
    });
  }
}

/** A folder held by a book of the saga or by a volume in work of its queue; a live book with the composition of its link. */
type Held = {
  kind: 'linked' | 'in_trash' | 'queued'; bookId?: string; title?: string; queueId?: string; loadedAt?: string; excluded?: string[]; nested?: string[];
  /** The glue the book has: of its load or of its last update from the folder. */
  fingerprint?: string | undefined;
  /** The live books whose links act while this one holds the folder: to be linked anew (OK2.6). */
  others?: Array<{ bookId: string; title: string }>;
};

/** The composition of the book of a folder a load keeps (OK1.3): what it left out under the folder and the folders inside it it chose. */
function compositionOf(choice: MockChoice, folder: string) {
  const chosen = [...choice.books, ...choice.materials.map((material) => material.path)];
  return {
    excluded: choice.exclude.filter((path) => path !== folder && within(folder, path)),
    nested: chosen.filter((path) => path !== folder && within(folder, path)),
  };
}

const emptyGlue = 'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855';

/**
 * The glue of a folder of the example as a preview reads it by the choice (OB3.3): the notes the folder owns — a deeper
 * folder of the choice takes its notes, an exclusion takes them out (specs/2026-11-15-desk-obsidian-web.md OW7.2) —, its
 * chapters and its fingerprint by what is in it.
 */
function glueOf(folder: string, choice: MockChoice = { books: [folder], materials: [], exclude: [] }, edited = false): Omit<Book, 'folder'> {
  const glue = glueAsLoaded(folder, choice);
  // The notes edited in Obsidian since (the scenario obsidianEdited): one chapter more, another glue.
  if (!edited || glue.refusal) return glue;
  const chapters = [...glue.chapters, { title: 'Ночной дозор', characters: 31 }];
  return { ...glue, chapters, characters: glue.characters + 31, fingerprint: digest(`${glue.fingerprint}:edited`, 64) };
}

function glueAsLoaded(folder: string, choice: MockChoice): Omit<Book, 'folder'> {
  const base = structuredClone(exampleBooks[folder] ?? {
    title: folder, notes: [], chapters: [], characters: 0, refusal: 'empty_import_content', warnings: [], dropped: nothingDropped, skipped: [],
    state: newFolder, fingerprint: emptyGlue,
  });
  const notes = base.notes.filter((note) => ownerOf(note.path, { ...choice, books: [...new Set([...choice.books, folder])] })?.path === folder);
  if (notes.length === base.notes.length && base.fingerprint) return base;
  const chapters = notes.flatMap((note) => note.chapters.map((title) => ({ title, characters: 900 + title.length * 37 })));
  const kept = new Set(notes.map((note) => note.path));
  return {
    ...base, notes, chapters, characters: chapters.reduce((sum, chapter) => sum + chapter.characters, 0),
    warnings: base.warnings.filter((warning) => warning.notes.length === 0 || warning.notes.some((note) => kept.has(note))),
    fingerprint: notes.length ? digest(`${folder}:${notes.map((note) => note.path).join('|')}`, 64) : emptyGlue,
    ...(notes.length ? {} : { refusal: 'empty_import_content' }),
  };
}

function fingerprinted(value: string | undefined): boolean {
  return /^[0-9a-f]{64}$/u.test(value ?? '');
}
