/**
 * The words found in the text of a chapter (specs/2026-11-10-ux7-reader.md RD2.1): the search of the server names the
 * chapters of the book that hold the words by their forms («порт» finds «порту»), the page marks them in the paragraphs.
 * A word of the query matches a word of the text that starts with its stem; the words of a query follow each other.
 */

export interface FoundRange {
  start: number;
  end: number;
}

/** A place found: a range in a paragraph of a chapter. */
export interface FoundPlace extends FoundRange {
  chapterId: string;
  paragraphId: string;
  /** The place of the paragraph in its chapter, from one: «абзац 3». */
  paragraph: number;
}

/** The shortest query the page asks the server for. */
export const minFindLength = 2;

const word = /[\p{L}\p{N}]+/gu;
// The endings of Russian words, longest first; a stem keeps at least three letters.
const endings = ['ами', 'ями', 'ого', 'его', 'ому', 'ему', 'ыми', 'ими', 'ой', 'ей', 'ий', 'ый', 'ая', 'яя', 'ое', 'ее', 'ую', 'юю', 'ам', 'ям', 'ах', 'ях', 'ом', 'ем', 'ов', 'ев', 'ы', 'и', 'а', 'я', 'о', 'е', 'у', 'ю', 'ь', 'й'];

function fold(text: string) {
  return text.toLocaleLowerCase('ru').replaceAll('ё', 'е');
}

function stem(term: string) {
  const ending = endings.find((item) => term.endsWith(item) && term.length - item.length >= 3);
  return ending ? term.slice(0, -ending.length) : term;
}

/** The stems of the words of a query; none for a query without words. */
export function findTerms(query: string) {
  return [...fold(query).matchAll(word)].map((match) => stem(match[0]));
}

/** The ranges of a paragraph where the words of the query stand one after another. */
export function findInText(text: string, terms: readonly string[]): FoundRange[] {
  if (!terms.length) return [];
  const tokens = [...fold(text).matchAll(word)].map((match) => ({ start: match.index ?? 0, word: match[0] }));
  const ranges: FoundRange[] = [];
  for (let index = 0; index + terms.length <= tokens.length; index += 1) {
    if (terms.every((term, offset) => tokens[index + offset]!.word.startsWith(term))) {
      const last = tokens[index + terms.length - 1]!;
      ranges.push({ start: tokens[index]!.start, end: last.start + last.word.length });
      index += terms.length - 1;
    }
  }
  return ranges;
}

/** Every place of a chapter in reading order. */
export function findInChapter(chapterId: string, paragraphs: readonly { id: string; text: string }[], terms: readonly string[]): FoundPlace[] {
  return paragraphs.flatMap((paragraph, index) => findInText(paragraph.text, terms)
    .map((range) => ({ ...range, chapterId, paragraphId: paragraph.id, paragraph: index + 1 })));
}

export function samePlace(left: FoundPlace | null | undefined, right: FoundPlace | null | undefined) {
  return Boolean(left && right && left.chapterId === right.chapterId && left.paragraphId === right.paragraphId && left.start === right.start);
}

/** A piece of the paragraph around a place for the list «Найдено»: the words before, the found words and after. */
export function snippet(text: string, range: FoundRange, around = 60) {
  const from = Math.max(0, text.lastIndexOf(' ', Math.max(0, range.start - around)) + 1);
  const toSpace = text.indexOf(' ', Math.min(text.length, range.end + around));
  const to = toSpace < 0 ? text.length : toSpace;
  return {
    before: `${from > 0 ? '…' : ''}${text.slice(from, range.start)}`,
    found: text.slice(range.start, range.end),
    after: `${text.slice(range.end, to)}${to < text.length ? '…' : ''}`,
  };
}
