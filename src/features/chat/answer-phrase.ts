import type { TextMark } from '../../ui/ReaderParagraph';

/**
 * The words of an answer in the paragraph it cites (specs/2026-11-11-ux9-shelf-chat.md SC3.7, J-25): the panel of the source
 * marks them inside a long paragraph, so the author sees where it is said. Words the answer cites in «…» are marked as they
 * stand; without them, the longest run of the same words, at least three, is the place. Nothing is marked when the answer
 * says it in words of its own.
 */
const shortestRun = 3;
const quoted = /[«"“]([^«»"“”\n]{6,})[»"”]/gu;
const word = /[\p{L}\p{N}]+/gu;
const plain = (text: string) => text.toLowerCase().replace(/ё/gu, 'е');

export function phraseMarks(paragraph: string, answer: string | null | undefined): TextMark[] {
  if (!answer?.trim()) return [];
  const text = plain(paragraph);
  const quotes = [...answer.matchAll(quoted)].map((match) => plain(match[1]!.trim()))
    .map((quote) => ({ quote, at: text.indexOf(quote) })).filter(({ at }) => at >= 0);
  if (quotes.length) return quotes.map(({ at, quote }) => ({ start: at, end: at + quote.length }));
  const words = [...paragraph.matchAll(word)].map((match) => ({ word: plain(match[0]), start: match.index, end: match.index + match[0].length }));
  const said = [...answer.matchAll(word)].map((match) => plain(match[0]));
  // The longest run of the same words of both, by the table of the runs that end at each pair of words.
  let best = { length: 0, end: -1 };
  let previous = new Array<number>(said.length + 1).fill(0);
  for (let index = 1; index <= words.length; index += 1) {
    const current = new Array<number>(said.length + 1).fill(0);
    for (let other = 1; other <= said.length; other += 1) {
      if (words[index - 1]!.word !== said[other - 1]) continue;
      current[other] = previous[other - 1]! + 1;
      if (current[other]! > best.length) best = { length: current[other]!, end: index - 1 };
    }
    previous = current;
  }
  if (best.length < shortestRun) return [];
  return [{ start: words[best.end - best.length + 1]!.start, end: words[best.end]!.end }];
}
