import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';
import { importFailureMessage, isAuthorFixableImportFailure } from './import-failure';

// The limit of a book file is the constant of the contract the server holds (MaxImportFileSizeBytes); the contract of the
// open core is generated from the whole one and is in both trees, the site and the open source.
const contract = JSON.parse(readFileSync(resolve(__dirname, '../../../../contracts/openapi.desktop.json'), 'utf8')) as {
  components: { schemas: { ImportConstraints: { properties: { maxFileSizeBytes: { const: number } } } } };
};
const maxFileMegabytes = contract.components.schemas.ImportConstraints.properties.maxFileSizeBytes.const / 1024 / 1024;

describe('the reasons of a book file the import cannot read (specs/2026-11-10-book-file-kinds.md K6.1)', () => {
  it.each([
    ['invalid_rtf_document', 'RTF не читается: файл повреждён. Откройте его в Word или LibreOffice и сохраните заново — как RTF или Word (.docx).'],
    ['invalid_odt_document', 'ODT не читается: файл повреждён или защищён паролем. Откройте его в LibreOffice, снимите пароль, если он есть, и сохраните заново.'],
    ['invalid_fb2zip_document', 'В архиве должен быть один файл .fb2. Распакуйте архив и загрузите файл книги.'],
    ['import.fb2zip_too_large', `Книга в архиве больше ${maxFileMegabytes} МБ. Разделите книгу на части или сохраните её без картинок.`],
    ['import.unsupported_encoding', 'Файл в кодировке, которую мы не читаем. Сохраните его в UTF-8 (в Блокноте: «Сохранить как» → «Кодировка: UTF-8») или загрузите Word (.docx).'],
    ['empty_import_content', 'В файле не нашлось текста глав.'],
  ])('names %s with a step for the author, and a retry does not help', (code, message) => {
    expect(importFailureMessage(code)).toBe(message);
    expect(isAuthorFixableImportFailure(code)).toBe(true);
  });

  it('keeps a transient failure without a reason of the file', () => {
    expect(importFailureMessage('worker.handler_failed')).toBeUndefined();
    expect(isAuthorFixableImportFailure('worker.handler_failed')).toBe(false);
  });
});
