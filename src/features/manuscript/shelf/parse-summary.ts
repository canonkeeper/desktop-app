import { fileStem } from './upload-files';

/**
 * What the helper takes for no chapter of a book (specs/2026-11-11-ux7-upload.md UP4.2): the pages a publisher puts
 * around a text — about the edition, the annotation, the contents, the imprint. The page only offers «Убрать из книги»;
 * the author decides.
 */
const servicePages = [
  /^об этом электронном издании/u, /^об электронн(ом|ой) (издании|версии)/u, /^аннотаци/u, /^оглавлени/u, /^содержание$/u,
  /^от издательства/u, /^от издателя/u, /^выходные данные/u, /^информация (о|об) (книге|издании)/u, /^copyright/u,
  /^about (this|the) (e-?book|edition)/u, /^table of contents/u,
];

/** The title of a chapter without its number: «18. Об этом электронном издании» → «об этом электронном издании». */
function bareTitle(title: string) {
  return title.toLowerCase().replace(/ё/gu, 'е').replace(/^\s*(глава\s+)?[\divxlc]+[.)]?\s+/u, '').trim();
}

export function looksLikeNoChapter(title: string) {
  const bare = bareTitle(title);
  return servicePages.some((page) => page.test(bare));
}

/**
 * A book named by its file (UP4.2): the title is the name of the file it came from, or looks like the name of a file —
 * «03-severny-sud», «tom_5». The page offers «Переименовать».
 */
export function namedByFile(title: string, sourceFileName?: string | null) {
  if (sourceFileName && title.trim() === fileStem(sourceFileName)) return true;
  return title.includes('_') || (/^[a-z0-9-]+$/iu.test(title) && /\d/u.test(title));
}
