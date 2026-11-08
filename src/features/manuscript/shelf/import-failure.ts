const epubUnreadable = 'EPUB не читается: файл повреждён. Сохраните книгу заново или загрузите DOCX.';

const importFailureMessages: Record<string, string> = {
  'import.unsupported_media_type': 'Формат файла не поддерживается. Подходят FB2, EPUB и DOCX.',
  'import.unsupported_encoding': 'Файл в кодировке, которую мы не читаем. Сохраните его в UTF-8 или загрузите DOCX.',
  invalid_fb2_document: 'FB2 не читается: в разметке файла ошибка. Пересохраните его в редакторе FB2 или загрузите DOCX.',
  invalid_epub_document: epubUnreadable,
  invalid_epub_container: epubUnreadable,
  invalid_docx_document: 'DOCX не читается. Откройте файл в Word и сохраните заново.',
  'import.content_mismatch': 'Содержимое файла не совпадает с его расширением. Проверьте, что это действительно FB2, EPUB или DOCX.',
  empty_import_content: 'В файле не нашлось текста глав.',
  'import.too_many_chapters': 'В файле больше 2000 глав — столько в одну книгу не поместить. Разделите его на тома.',
  'import.too_large': 'В файле больше 10 миллионов знаков — столько в одну книгу не поместить. Разделите его на тома.',
};

/** A transient import failure: the author retries it, and support gets its code (UC3.4). */
export const transientImportFailureMessage = 'Не удалось загрузить книгу. Повторите — если не получится, напишите в поддержку.';

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
const wordProcessorExtensions = new Set(['.doc', '.rtf', '.odt']);

export function fileExtension(fileName: string) {
  const dot = fileName.lastIndexOf('.');
  return dot > 0 ? fileName.slice(dot).toLowerCase() : '';
}

/**
 * UC3.1: why a file of this format is not taken before any transfer, or null for a format the
 * server accepts (`getImportConstraints.allowedExtensions`).
 */
export function unsupportedFileMessage(fileName: string, constraints: ImportFileConstraints) {
  const extension = fileExtension(fileName);
  if (constraints.allowedExtensions.some((allowed) => allowed.toLowerCase() === extension)) return null;
  if (extension === '.pdf') {
    return 'PDF не поддерживается: из него нельзя надёжно достать главы. Сохраните рукопись как DOCX (Word: Файл → Сохранить как → Документ Word) или загрузите FB2/EPUB.';
  }
  if (wordProcessorExtensions.has(extension)) {
    return 'Этот формат не поддерживается. Откройте файл в Word или LibreOffice и сохраните как DOCX.';
  }
  if (extension === '.txt') {
    return 'TXT не поддерживается: в нём нет разметки глав. Сохраните текст как DOCX — главы с заголовками «Глава N» распознаются.';
  }
  if (archiveExtensions.has(extension)) {
    return 'Архивы не поддерживаются. Распакуйте архив и загрузите файл .fb2, .epub или .docx.';
  }
  const formats = constraints.allowedExtensions.map((allowed) => allowed.replace(/^\./, '').toUpperCase());
  const allowed = formats.length > 1 ? `${formats.slice(0, -1).join(', ')} и ${formats.at(-1)}` : formats.join('');
  return extension
    ? `Формат «${extension}» не поддерживается. Подходят ${allowed}.`
    : `Формат файла не поддерживается. Подходят ${allowed}.`;
}

/** UC3.1: the reason a file over the size limit of the server is not taken. */
export function oversizedFileMessage(constraints: ImportFileConstraints) {
  return `Файл больше ${Math.round(constraints.maxFileSizeBytes / 1024 / 1024)} МБ. Разделите книгу на части или сохраните её без картинок.`;
}
