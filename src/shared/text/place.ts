/**
 * The place of a book as the author reads it everywhere — «Книга 2, гл. 12 „Белый порт“, абзац 3»
 * (specs/2026-11-10-ux1-names.md N1): the server writes the name of the chapter beside its key (`label`, `places`), the
 * page adds the paragraphs in words. The key of a chapter and «¶» are the address of the model and are never shown.
 */

/** The places of chapter keys, as the server names them beside the keys. */
export type Places = Readonly<Record<string, string>>;

/** What stands for a chapter the text of the saga does not have now. */
export const missingPlace = 'глава, которой нет в книге';

/** «абзац 3», «абзацы 3–5»; nothing without a first paragraph. */
export function paragraphsLabel(from?: number, to?: number) {
  if (!from) return '';
  return to !== undefined && to > from ? `абзацы ${from}–${to}` : `абзац ${from}`;
}

/** «Книга 2, гл. 12 „Белый порт“, абзацы 3–5»; a chapter without a place is «глава, которой нет в книге». */
export function placeLabel(place: string | undefined, from?: number, to?: number) {
  return [place || missingPlace, paragraphsLabel(from, to)].filter(Boolean).join(', ');
}

const sourceLink = /\[[^\]\n]*\]\(source:([^)\s/]+)(?:\/(\d+)(?:[-–](\d+))?)?\)/gu;

/**
 * Plain words of the author with the links the product wrote into them (`[гл. 14 ¶33](source:14/33)`) replaced by the
 * places they name (N2.3, N3.4).
 */
export function plainPlaces(text: string, places: Places | undefined) {
  return text.replace(sourceLink, (_link, key: string, from?: string, to?: string) =>
    placeLabel(places?.[key], from ? Number(from) : undefined, to ? Number(to) : undefined));
}

/**
 * The words the model gave a link, while the server has not named its place yet (a remark being written): «¶» is
 * said in words and a key in brackets is dropped.
 */
export function withoutDeviceWords(text: string) {
  return text
    .replace(/¶\s*(\d+)\s*[–-]\s*¶?\s*(\d+)/gu, 'абзацы $1–$2')
    .replace(/¶\s*(\d+)/gu, 'абзац $1')
    .replace(/\s*¶/gu, '')
    .replace(/\s*\[?\bb\d+c\d+\b\]?/gu, '');
}
