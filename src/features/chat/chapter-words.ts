import { bytesOf, zipText } from '../../shared/files/zip';

/**
 * The words of a chapter the author attaches, counted in the browser before it goes (specs/2026-11-11-ux4-after-answer.md
 * AA4.2): the card says its size in words, not in bytes. A text file is read as it is; a Word file — the text of its body,
 * read out of the zip without a library. Nothing is sent: the server reads the chapter itself when the question goes.
 */
export const wordsOf = (text: string) => text.split(/[^\p{L}\p{N}\p{M}]+/u).filter(Boolean).length;

/** «≈ 2 400»: a size, not a count — round to tens, from a thousand to hundreds. */
export function roundWords(words: number) {
  if (words < 100) return words;
  return words < 1_000 ? Math.round(words / 10) * 10 : Math.round(words / 100) * 100;
}

export async function chapterFileWords(file: File): Promise<number | undefined> {
  try {
    if (/\.docx$/iu.test(file.name)) {
      const xml = await zipText(await bytesOf(file), 'word/document.xml');
      if (xml === undefined) return undefined;
      // The text of a Word body is in its runs; a paragraph ends a word.
      const text = [...xml.matchAll(/<w:t(?:\s[^>]*)?>([^<]*)<\/w:t>|<\/w:p>/gu)].map((match) => match[1] ?? '\n').join('');
      return wordsOf(text.replace(/&[a-z]+;|&#\d+;/gu, ' '));
    }
    return wordsOf(await file.text());
  } catch {
    return undefined;
  }
}
