import { bookFileKinds } from '../../../entities/manuscript/book-file-kinds';

const epubUnreadable = 'EPUB не читается: файл повреждён. Сохраните книгу заново или загрузите Word (.docx).';

const importFailureMessages: Record<string, string> = {
  'import.unsupported_media_type': `Формат файла не поддерживается. Подходят ${bookFileKinds}.`,
  'import.unsupported_encoding': 'Файл в кодировке, которую мы не читаем. Сохраните его в UTF-8 (в Блокноте: «Сохранить как» → «Кодировка: UTF-8») или загрузите Word (.docx).',
  invalid_fb2_document: 'FB2 не читается: в разметке файла ошибка. Пересохраните его в редакторе FB2 или загрузите Word (.docx).',
  invalid_epub_document: epubUnreadable,
  invalid_epub_container: epubUnreadable,
  invalid_docx_document: 'Word (.docx) не читается. Откройте файл в Word и сохраните заново.',
  invalid_rtf_document: 'RTF не читается: файл повреждён. Откройте его в Word или LibreOffice и сохраните заново — как RTF или Word (.docx).',
  invalid_odt_document: 'ODT не читается: файл повреждён или защищён паролем. Откройте его в LibreOffice, снимите пароль, если он есть, и сохраните заново.',
  invalid_fb2zip_document: 'В архиве должен быть один файл .fb2. Распакуйте архив и загрузите файл книги.',
  // The limit is the limit of a book file of the server (`getImportConstraints.maxFileSizeBytes`, a constant of the contract).
  'import.fb2zip_too_large': 'Книга в архиве больше 50 МБ. Разделите книгу на части или сохраните её без картинок.',
  'import.content_mismatch': `Содержимое файла не совпадает с его расширением. Откройте файл там, где вы его писали, и сохраните заново: ${bookFileKinds}.`,
  empty_import_content: 'В файле не нашлось текста глав.',
  'import.too_many_chapters': 'В файле больше 2000 глав — столько в одну книгу не поместить. Разделите его на тома.',
  'import.too_large': 'В файле больше 10 миллионов знаков — столько в одну книгу не поместить. Разделите его на тома.',
};

/**
 * The reason of a failed import and how the author fixes the file, by the code of the queue
 * preflight or of the import Job — one code for both (specs/2026-10-28-writer-first-success.md,
 * UC3.4; book limits: specs/2026-10-27-stage-0-protection.md, R2.1). A retry does not help any of
 * them; other codes are transient failures and have no reason here.
 */
export function importFailureMessage(code: string | null | undefined) {
  return code && Object.hasOwn(importFailureMessages, code) ? importFailureMessages[code] : undefined;
}

/** UC3.4: the author fixes the file (format, encoding, empty or damaged file, limits); a retry does not help (R3.4.3). */
export function isAuthorFixableImportFailure(code: string | null | undefined) {
  return importFailureMessage(code) !== undefined;
}

export type ImportFileConstraints = Readonly<{
  allowedExtensions: readonly string[];
  maxFileSizeBytes: number;
}>;

const archiveExtensions = new Set(['.zip', '.rar', '.7z']);

function fileExtension(fileName: string) {
  const dot = fileName.lastIndexOf('.');
  return dot > 0 ? fileName.slice(dot).trim().toLowerCase() : '';
}

/**
 * K1.1, K1.3: the kind of a book file is the end of its name among the kinds of the server
 * (`getImportConstraints.allowedExtensions`), the longest first — `.fb2.zip` before `.zip`; null when it ends in none.
 */
export function importFileKind(fileName: string, allowedExtensions: readonly string[]) {
  const name = fileName.trim().toLowerCase();
  let found: string | null = null;
  for (const allowed of allowedExtensions) {
    const kind = allowed.toLowerCase();
    if (name.endsWith(kind) && kind.length > (found?.length ?? 0)) found = kind;
  }
  return found;
}

/**
 * UC3.1: why a file of this kind is not taken before any transfer, and what to do with it, or null for a kind the
 * server accepts (K1.3).
 */
export function unsupportedFileMessage(fileName: string, constraints: ImportFileConstraints) {
  if (importFileKind(fileName, constraints.allowedExtensions)) return null;
  const extension = fileExtension(fileName);
  if (extension === '.pdf') {
    return 'PDF не поддерживается: из него нельзя надёжно достать главы. Откройте рукопись в Word или LibreOffice → «Сохранить как» → Word (.docx), RTF или ODT.';
  }
  if (extension === '.doc') {
    return 'Старый формат Word (.doc) не читается. Откройте файл в Word или LibreOffice → «Сохранить как» → Word (.docx), RTF или ODT.';
  }
  if (archiveExtensions.has(extension)) {
    return `Из архивов читается только книга FB2 в .fb2.zip. Распакуйте архив и загрузите файл книги: ${bookFileKinds}.`;
  }
  return extension
    ? `Формат «${extension}» не поддерживается. Подходят ${bookFileKinds}.`
    : `Формат файла не поддерживается. Подходят ${bookFileKinds}.`;
}

/** UC3.1: the reason a file over the size limit of the server is not taken. */
export function oversizedFileMessage(constraints: ImportFileConstraints) {
  return `Файл больше ${Math.round(constraints.maxFileSizeBytes / 1024 / 1024)} МБ. Разделите книгу на части или сохраните её без картинок.`;
}
