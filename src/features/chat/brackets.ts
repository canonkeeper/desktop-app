/**
 * The words in brackets of an example of a question — «Что [имя героя] знает о [событии]…» — the author replaces with his
 * own (specs/2026-11-11-ux9-shelf-chat.md SC3.3, Ч-01): a question with them does not go as it is, and Tab steps from one
 * to the next. The field knows the brackets it put itself (specs/2026-11-12-ux12-shell.md SH1.1): brackets of the author —
 * a footnote «[1]», «[так в тексте]», a pasted scene — are his text and hold nothing.
 */
const bracket = /\[[^\]\n]*\]/gu;

/** A word in brackets an example put into the field: its words and where they stand. */
export interface Blank {
  text: string;
  start: number;
}

/** The blanks of an example as it goes into the field. */
export function blanksOf(example: string): Blank[] {
  return [...example.matchAll(bracket)].map((match) => ({ text: match[0], start: match.index }));
}

/**
 * The blanks after the text changed from `before` to `after`: one before the change stays, one after it moves with the
 * text, one the change touched keeps its place — it stands as it was again only when the same words come back there, as
 * Ctrl+Z brings them.
 */
export function shiftBlanks(before: string, after: string, blanks: readonly Blank[]): Blank[] {
  if (before === after || blanks.length === 0) return [...blanks];
  const shortest = Math.min(before.length, after.length);
  let prefix = 0;
  while (prefix < shortest && before[prefix] === after[prefix]) prefix += 1;
  let suffix = 0;
  while (suffix < shortest - prefix && before[before.length - 1 - suffix] === after[after.length - 1 - suffix]) suffix += 1;
  const changeEnd = before.length - suffix;
  const moved = after.length - before.length;
  return blanks.map((blank) => {
    if (blank.start + blank.text.length <= prefix) return blank;
    if (blank.start >= changeEnd) return { ...blank, start: blank.start + moved };
    return { ...blank, start: Math.min(blank.start, prefix) };
  });
}

/** The places of the blanks that stand in the text as the example put them, in order: the author has not replaced them. */
export function standingBlanks(text: string, blanks: readonly Blank[]): Array<[number, number]> {
  return blanks.filter((blank) => text.startsWith(blank.text, blank.start))
    .map((blank): [number, number] => [blank.start, blank.start + blank.text.length])
    .sort((one, other) => one[0] - other[0]);
}

/** The blank after the selection, or before it going back; around the text when there is none that way. */
export function nextBlank(places: ReadonlyArray<[number, number]>, selection: [number, number], back = false): [number, number] | null {
  if (places.length === 0) return null;
  if (back) return [...places].reverse().find(([start]) => start < selection[0]) ?? places.at(-1)!;
  return places.find(([start]) => start >= selection[1]) ?? places[0]!;
}

export const replaceBracketsReason = 'Замените слова в скобках своими';
