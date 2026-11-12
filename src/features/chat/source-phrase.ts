/**
 * The beginning of the phrase of an answer at a link of it (specs/2026-11-11-ux4-after-answer.md AA1.6): «Спросить, откуда
 * это» asks the helper where it took that phrase from when the place the link names is not in the book.
 */
const blockSelector = 'p, li, blockquote, td, th, h1, h2, h3, h4, h5, h6';
const phraseSigns = 80;

export function phraseAt(link: Element): string {
  const block = link.closest(blockSelector) ?? link.parentElement;
  if (!block) return '';
  const before = document.createRange();
  before.setStart(block, 0);
  before.setEndBefore(link);
  const sentence = before.toString().split(/(?<=[.!?…])\s+/u).at(-1)?.replace(/[\s,;:—–-]+$/u, '').trim() ?? '';
  if (sentence) return clip(sentence);
  // A link at the beginning of its phrase: the phrase is what follows it.
  const after = document.createRange();
  after.setStartAfter(link);
  after.setEnd(block, block.childNodes.length);
  return clip(after.toString().trim().split(/(?<=[.!?…])\s+/u)[0]?.trim() ?? '');
}

function clip(text: string) {
  const signs = [...text.replace(/\s+/gu, ' ')];
  if (signs.length <= phraseSigns) return signs.join('');
  const cut = signs.slice(0, phraseSigns).join('');
  return `${cut.slice(0, Math.max(cut.lastIndexOf(' '), phraseSigns / 2)).trimEnd()}…`;
}

/** The question «Спросить, откуда это» puts into the field. */
export function askWhereQuestion(phrase: string) {
  return phrase ? `Покажи точное место в тексте для: „${phrase}“` : 'Покажи точное место в тексте для этой ссылки.';
}
