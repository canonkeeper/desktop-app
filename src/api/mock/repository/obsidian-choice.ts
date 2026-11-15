// SPDX-License-Identifier: AGPL-3.0-only
import type { Schema } from '../../../shared/api';

/**
 * The choice of a vault as the server reads it in every operation of the bridge (specs/2026-11-13-desk-obsidian.md OB3.3;
 * domain/obsidian/choice.go `Owner`): a note belongs to the deepest chosen folder it lies in — a book or a folder of
 * materials —, and an exclusion not shallower than that folder takes it out of the choice.
 */
export interface MockChoice {
  books: readonly string[];
  materials: ReadonlyArray<{ path: string; kind: Schema<'AuthorMaterialKind'> }>;
  exclude: readonly string[];
}

export function depth(path: string) {
  return path === '.' ? 0 : path.split('/').length;
}

export function within(folder: string, path: string) {
  return folder === '.' || path === folder || path.startsWith(`${folder}/`);
}

export function folderOf(notePath: string) {
  const slash = notePath.lastIndexOf('/');
  return slash < 0 ? '.' : notePath.slice(0, slash);
}

/** The chosen folder a note belongs to, or null when it is out of the choice. */
export function ownerOf(notePath: string, choice: MockChoice): { path: string; book: boolean; kind: Schema<'AuthorMaterialKind'> } | null {
  const folder = folderOf(notePath);
  const chosen = [
    ...choice.books.map((path) => ({ path, book: true, kind: 'other' as const })),
    ...choice.materials.map((material) => ({ path: material.path, book: false, kind: material.kind })),
  ].filter((candidate) => within(candidate.path, folder)).sort((left, right) => depth(right.path) - depth(left.path));
  const owner = chosen[0];
  if (!owner) return null;
  const out = choice.exclude.some((excluded) => excluded === notePath || (within(excluded, folder) && depth(excluded) >= depth(owner.path)));
  return out ? null : owner;
}

/** A hex digest of the mock: the same text gives the same digest — what the server's SHA-256 tells, a change. */
export function digest(text: string, length: number): string {
  let hash = 0x811c9dc5;
  let out = '';
  while (out.length < length) {
    for (const sign of `${text}:${out.length}`) hash = Math.imul(hash ^ sign.codePointAt(0)!, 0x01000193) >>> 0;
    out += hash.toString(16).padStart(8, '0');
  }
  return out.slice(0, length);
}

/**
 * A body of the bridge as the server reads it (`DisallowUnknownFields`; the contract says `additionalProperties: false`): only
 * the fields it names, at the top and in the items of its lists — `items` names the fields of the items of a list of objects.
 */
export function knownFields(body: unknown, fields: Readonly<Record<string, readonly string[] | null>>): boolean {
  if (!body || typeof body !== 'object' || Array.isArray(body)) return false;
  return Object.entries(body).every(([key, value]) => {
    if (!Object.hasOwn(fields, key)) return false;
    const items = fields[key];
    if (!items) return true;
    return Array.isArray(value) && value.every((item) => item && typeof item === 'object' && Object.keys(item).every((name) => items.includes(name)));
  });
}

/**
 * Two folders of a choice the server refuses (domain/obsidian `Check`): a folder of records named twice or as a book, a book
 * inside another book, a book around or inside a folder a book of the saga holds — the folder itself is that book.
 */
export function overlapOf(books: readonly string[], materials: readonly string[], held: readonly string[]): [string, string] | null {
  const seen = new Set(books);
  for (const folder of materials) {
    if (seen.has(folder)) return [folder, folder];
    seen.add(folder);
  }
  for (const book of books) {
    const inner = books.find((other) => other !== book && within(book, other));
    if (inner) return [book, inner];
  }
  for (const book of books) {
    for (const folder of held) {
      if (folder === book) continue;
      if (within(book, folder)) return [book, folder];
      if (within(folder, book)) return [folder, book];
    }
  }
  return null;
}
