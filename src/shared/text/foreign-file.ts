/**
 * A file of a kind a place does not read, in the same words wherever the author drops it (specs/2026-11-11-ux4-after-answer.md
 * AA4.3; Ч-51): the chapter of the chat and the materials of the author say a book, a PDF and a picture alike. Each place
 * adds what it reads instead; the first words are this one table.
 */
export type ForeignFile = 'book' | 'pdf' | 'picture';

const kinds: readonly [RegExp, ForeignFile][] = [
  [/\.(fb2|epub|fb2\.zip)$/iu, 'book'],
  [/\.pdf$/iu, 'pdf'],
  [/\.(png|jpe?g|gif|webp|heic|heif|bmp|tiff?|avif)$/iu, 'picture'],
];

/** The foreign kind of a file by the end of its name; null for a kind the place may read. */
export function foreignFileOf(name: string): ForeignFile | null {
  return kinds.find(([ending]) => ending.test(name))?.[1] ?? null;
}

export const foreignFileWords: Readonly<Record<ForeignFile, string>> = {
  book: 'Это файл книги: книги загружаются в «Книги».',
  pdf: 'PDF не разбирается. Сохраните исходный файл как .docx.',
  picture: 'Текст с картинки не читается.',
};
