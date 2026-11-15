// SPDX-License-Identifier: AGPL-3.0-only
import type { Schema } from '../../../shared/api';
import type { ObsidianPreviewRequest, ObsidianTreeFolder, ObsidianVaultBook } from './obsidian-api';
import { materialChoiceWords, materialKindsInOrder, words } from './words';

type Kind = Schema<'AuthorMaterialKind'>;

/**
 * What the author takes of a folder (specs/2026-11-15-desk-obsidian-web.md OW2.3): a book, records of a kind, nothing, or —
 * for a folder inside a chosen one — its notes go with that folder.
 */
export type FolderChoice = 'skip' | 'book' | 'together' | `materials:${Kind}`;

/** The choice of the author the browser keeps (OW2.6): the folders he set and the notes he left out, by paths in the vault. */
export interface ChoiceMemory {
  kinds: Readonly<Record<string, FolderChoice>>;
  notes: readonly string[];
}

export const emptyMemory: ChoiceMemory = { kinds: {}, notes: [] };

/** The limits of one request of a preview (OW2.7; contract `ObsidianPreviewRequest`). */
export const limits = { books: 200, materials: 200, exclude: 2000 } as const;

export function isMaterials(choice: FolderChoice): choice is `materials:${Kind}` {
  return choice.startsWith('materials:');
}

export function kindOf(choice: FolderChoice): Kind {
  return choice.slice('materials:'.length) as Kind;
}

function parentOf(path: string) {
  if (path === '.') return null;
  const slash = path.lastIndexOf('/');
  return slash < 0 ? '.' : path.slice(0, slash);
}

function depthOf(path: string) {
  return path === '.' ? 0 : path.split('/').length;
}

/** The folder of a note by its path. */
export function noteFolder(path: string) {
  return parentOf(path) ?? '.';
}

/** A variant of the choice of a folder, with the reason the server would not take it, said by the variant (OW2.3). */
export interface ChoiceOption {
  value: FolderChoice;
  label: string;
  reason: string | null;
}

/** A folder of the list as the page shows it: its choice now and what may be chosen. */
export interface FolderRow {
  folder: ObsidianTreeFolder;
  children: string[];
  /**
   * Inside the folder of a book of the saga, outside its folders of records: no choice — its composition is the one its link
   * keeps (OW2.5); `fixedWord` says what the folder is to the book.
   */
  fixed: boolean;
  fixedWord: string | null;
  /** A folder of records of a book of the saga asked without a kind: the server takes the kinds of its notes (OW2.5). */
  kindless: boolean;
  /** The chosen folder whose notes it goes with when it takes nothing of its own. */
  owner: ObsidianTreeFolder | null;
  /** A book the folder holds: in the saga — `live`, else in «Недавно удалённые» (OW2.2). */
  book: { bookId: string; title: string; live: boolean } | null;
  choice: FolderChoice;
  options: ChoiceOption[];
}

/** The list of folders of the look with the choices, the request of a preview and its counts (OW2.1–OW2.7). */
export interface ChoiceState {
  rows: ReadonlyMap<string, FolderRow>;
  order: readonly string[];
  request: ObsidianPreviewRequest;
  books: number;
  materials: number;
  /** Why «Показать, что получится» is closed by the choice itself, or null. */
  reason: string | null;
}

/** A path inside a folder of the vault, not the folder itself. */
function inFolder(folder: string, path: string) {
  return folder === '.' ? path !== '.' : path.startsWith(`${folder}/`);
}

/**
 * The choice of every folder by the rules of the server (table «what a nested folder may» of the review; domain/obsidian
 * `Check`, `Owner`): a note belongs to the deepest chosen folder; a book inside a book — chosen or of the saga — or around
 * one is refused; records inside a book and a book inside records may be. The default goes by seniority (OW2.4): the
 * remembered choice of the author → the book of the folder → the hint of the server; else a folder without a chosen one
 * above takes nothing, and a nested one goes with the folder above — its hint `skip` excludes nothing.
 *
 * A live book of the saga — `getObsidianVault.books`, which knows them better than a look read before a link changed — is
 * asked by the composition its link keeps (OW2.5): its exclusions, and its folders of records with the kind the author chose,
 * else the hint, else none; its other folders take no choice, and the memory of the browser does not change it.
 */
export function choiceState(folders: readonly ObsidianTreeFolder[], vaultBooks: readonly ObsidianVaultBook[], memory: ChoiceMemory): ChoiceState {
  const live = new Set(vaultBooks.map((book) => book.bookId));
  const liveAt = new Map(vaultBooks.map((book) => [book.folder, book]));
  const byPath = new Map(folders.map((folder) => [folder.path, folder]));
  const children = new Map<string, string[]>();
  for (const folder of folders) {
    const parent = parentOf(folder.path);
    if (parent !== null && byPath.has(parent)) children.set(parent, [...(children.get(parent) ?? []), folder.path]);
  }
  // The book of a folder: the live book that holds it now, else the book of the look in «Недавно удалённые».
  const bookOf = (folder: ObsidianTreeFolder): FolderRow['book'] => {
    const held = liveAt.get(folder.path);
    if (held) return { bookId: held.bookId, title: held.title, live: true };
    return folder.book && !live.has(folder.book.bookId) ? { ...folder.book, live: false } : null;
  };
  // Every folder a book of the saga holds — a live one, also not in the look, and one in «Недавно удалённые».
  const held = [
    ...vaultBooks.map((book) => ({ path: book.folder, title: book.title })),
    ...folders.flatMap((folder) => (folder.book && !live.has(folder.book.bookId) ? [{ path: folder.path, title: folder.book.title }] : [])),
  ];
  const ancestors = (path: string) => {
    const above: ObsidianTreeFolder[] = [];
    for (let parent = parentOf(path); parent !== null; parent = parentOf(parent)) {
      const folder = byPath.get(parent);
      if (folder) above.push(folder);
    }
    return above;
  };
  const descendants = (path: string) => folders.filter((folder) => folder.path !== path && (path === '.' || folder.path.startsWith(`${path}/`)));
  const choices = new Map<string, FolderChoice>();
  const rows = new Map<string, FolderRow>();
  const titleOf = (folder: ObsidianTreeFolder) => bookOf(folder)?.title ?? folder.name;
  const hintOf = (folder: ObsidianTreeFolder) => (folder.hint?.kind === 'book' ? 'book' as const
    : folder.hint?.kind === 'materials' ? `materials:${folder.hint.material ?? 'other'}` as const : undefined);
  const records = (): ChoiceOption[] => materialKindsInOrder.map((kind) => ({ value: `materials:${kind}`, label: materialChoiceWords[kind], reason: null }));
  // The live books chosen as books: their composition is asked as their link keeps it (OW2.5).
  const homes = new Map<string, ObsidianVaultBook>();

  // Parents before their folders: a choice above decides the variants below.
  for (const folder of [...folders].sort((left, right) => depthOf(left.path) - depthOf(right.path))) {
    const above = ancestors(folder.path);
    const base = { folder, children: children.get(folder.path) ?? [], book: bookOf(folder) };
    const home = above.map((one) => homes.get(one.path)).find(Boolean);
    const recordsOfHome = home ? above.find((one) => home.nested.includes(one.path)) : undefined;
    if (home && home.nested.includes(folder.path)) {
      // A folder of records of a book of the saga: the kind of its records, or «Не брать».
      const remembered = memory.kinds[folder.path];
      const hint = hintOf(folder);
      const own = remembered === 'skip' || (remembered && isMaterials(remembered)) ? remembered : hint && isMaterials(hint) ? hint : null;
      const owner = byPath.get(home.folder) ?? null;
      choices.set(folder.path, own ?? 'materials:other');
      rows.set(folder.path, {
        ...base, fixed: false, fixedWord: null, kindless: own === null, owner, choice: own ?? 'materials:other',
        options: [{ value: 'skip', label: words.skip, reason: null }, ...records()],
      });
      continue;
    }
    if (home && !recordsOfHome) {
      // Any other folder of a book of the saga: in the book, or left out as at its load.
      const left = home.excluded.some((path) => path === folder.path || inFolder(path, folder.path));
      choices.set(folder.path, left ? 'skip' : 'together');
      rows.set(folder.path, {
        ...base, fixed: true, fixedWord: left ? words.fixedLeft : words.fixedPart, kindless: false, owner: null,
        choice: left ? 'skip' : 'together', options: [],
      });
      continue;
    }
    let owner: ObsidianTreeFolder | null = null;
    for (const one of above) {
      const choice = choices.get(one.path);
      if (choice === 'book' || (choice && isMaterials(choice))) {
        owner = one;
        break;
      }
      if (choice === 'skip' && rows.get(one.path)?.owner) break;
    }
    // The book variant the server would refuse: inside a book chosen or of the saga, or around one (OW2.3).
    const insideChosen = above.find((one) => choices.get(one.path) === 'book');
    const insideHeld = held.filter((one) => inFolder(one.path, folder.path)).sort((left, right) => depthOf(right.path) - depthOf(left.path))[0];
    const holds = held.find((one) => inFolder(folder.path, one.path));
    const bookReason = insideChosen ? words.whyInside(titleOf(insideChosen)) : insideHeld ? words.whyInside(insideHeld.title)
      : holds ? words.whyHolds(holds.title) : null;
    const options: ChoiceOption[] = [
      ...(owner ? [{ value: 'together' as const, label: words.together(owner.name), reason: null }] : []),
      { value: 'skip', label: words.skip, reason: null },
      { value: 'book', label: words.book, reason: bookReason },
      ...records(),
    ];
    const allowed = (choice: FolderChoice | undefined): choice is FolderChoice => Boolean(choice && options.some((option) => option.value === choice && !option.reason));
    const hint = hintOf(folder);
    const remembered = memory.kinds[folder.path];
    const choice: FolderChoice = allowed(remembered) ? remembered
      : base.book && allowed('book') ? 'book'
        : allowed(hint) ? hint
          : owner ? 'together' : 'skip';
    choices.set(folder.path, choice);
    rows.set(folder.path, { ...base, fixed: false, fixedWord: null, kindless: false, owner, choice, options });
    const vaultBook = liveAt.get(folder.path);
    if (choice === 'book' && vaultBook) homes.set(folder.path, vaultBook);
  }
  // A book chosen below a folder closes its book variant too: the folder above holds a book then (OW2.3).
  for (const row of rows.values()) {
    const book = row.options.find((option) => option.value === 'book');
    if (!book) continue;
    const below = descendants(row.folder.path).find((one) => choices.get(one.path) === 'book' && !rows.get(one.path)!.fixed);
    if (below && !book.reason && row.choice !== 'book') book.reason = words.whyHolds(titleOf(below));
  }

  const order = folders.map((folder) => folder.path);
  const books = order.filter((path) => rows.get(path)!.choice === 'book');
  const materials = order.filter((path) => isMaterials(rows.get(path)!.choice));
  const excludedFolders = order.filter((path) => !rows.get(path)!.fixed && rows.get(path)!.choice === 'skip' && rows.get(path)!.owner);
  // The deepest chosen folder of a note: a note the browser left out stays out unless a book of the saga owns it — its
  // composition is the link's.
  const ownerOfNote = (path: string) => [noteFolder(path), ...ancestors(noteFolder(path)).map((one) => one.path)]
    .find((folder) => { const choice = choices.get(folder); return choice === 'book' || (choice !== undefined && isMaterials(choice)); });
  const excludedNotes = memory.notes.filter((path) => byPath.has(noteFolder(path)) && !homes.has(ownerOfNote(path) ?? ''));
  const composition = [...homes.values()].flatMap((book) => book.excluded);
  const exclude = [...new Set([...excludedFolders, ...composition, ...excludedNotes])];
  const request: ObsidianPreviewRequest = {
    ...(books.length ? { books } : {}),
    ...(materials.length ? {
      materials: materials.map((path) => (rows.get(path)!.kindless ? { path } : { path, kind: kindOf(rows.get(path)!.choice) })),
    } : {}),
    ...(exclude.length ? { exclude } : {}),
  };
  const reason = books.length + materials.length === 0 ? words.whyNone
    : books.length > limits.books || materials.length > limits.materials ? words.whyLimit
      : exclude.length > limits.exclude ? words.whyExcl : null;
  return { rows, order, request, books: books.length, materials: materials.length, reason };
}

/**
 * How many folders below a folder have a choice of their own (OW2.1): a book, records, or a folder left out of the folder
 * above it — «внутри: K папок со своим выбором» of a folded folder.
 */
export function ownChoicesBelow(state: ChoiceState, path: string) {
  return state.order.filter((other) => other !== path && (path === '.' || other.startsWith(`${path}/`))).filter((other) => {
    const row = state.rows.get(other)!;
    if (row.fixed || row.choice === 'together') return false;
    return row.choice !== 'skip' || row.owner !== null;
  }).length;
}

/** The remembered choice without the folders the tree no longer has and the notes of such folders (OW2.6). */
export function pruneMemory(memory: ChoiceMemory, folders: readonly ObsidianTreeFolder[]): ChoiceMemory {
  const known = new Set(folders.map((folder) => folder.path));
  return {
    kinds: Object.fromEntries(Object.entries(memory.kinds).filter(([path]) => known.has(path))),
    notes: memory.notes.filter((path) => known.has(noteFolder(path))),
  };
}

/** Two requests of a preview are the same choice. */
export function sameRequest(left: ObsidianPreviewRequest, right: ObsidianPreviewRequest) {
  return JSON.stringify(left) === JSON.stringify(right);
}
