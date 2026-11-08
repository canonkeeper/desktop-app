import { describe, expect, it } from 'vitest';
import {
  createImportQueueItems,
  detectImportVolume,
  formatImportedBookCount,
  formatImportSagaGuard,
  moveImportQueueItem,
  moveLocal,
  numberLocalRows,
  screenImportFiles,
  stageFiles,
  validateImportQueue,
  type ImportQueueItem,
} from './import-queue';

const constraints = {
  allowedExtensions: ['.fb2', '.epub', '.docx'],
  maxFileSizeBytes: 50 * 1024 * 1024,
};

function file(name: string, content = 'book') {
  return new File([content], name);
}

function sized(name: string, size: number) {
  const selected = file(name);
  Object.defineProperty(selected, 'size', { value: size });
  return selected;
}

function restoredItem(item: ImportQueueItem): ImportQueueItem {
  const { file: _file, ...restored } = item;
  return restored;
}

describe('import queue model', () => {
  it.each([
    ['Том 3.fb2', 3],
    ['volume-04.epub', 4],
    ['vol_5.docx', 5],
    ['Книга 6.fb2', 6],
    ['03-северный-суд.docx', 3],
  ])('detects an explicit volume in %s', (name, expected) => {
    expect(detectImportVolume(name)).toBe(expected);
  });

  it('creates sorted metadata for every selected browser File', () => {
    const items = createImportQueueItems([
      file('Том 5.fb2'),
      file('Том 4.fb2'),
      file('Пролог.fb2'),
    ]);

    expect(items.map((item) => item.volume)).toEqual([4, 5, null]);
    expect(items.map((item) => item.fileName)).toEqual(['Том 4.fb2', 'Том 5.fb2', 'Пролог.fb2']);
    expect(items.every((item) => item.file instanceof File)).toBe(true);
  });

  it('validates contiguous restored rows without a browser File', () => {
    const items = createImportQueueItems([file('Том 4.fb2'), file('Том 5.epub')]).map(restoredItem);

    expect(validateImportQueue(items, 4)).toMatchObject({ valid: true, issues: [] });
  });

  it('blocks unknown, broken and repeated numbers and gaps in the writer words', () => {
    const unknown = createImportQueueItems([file('Пролог.fb2')]).map(restoredItem);
    const duplicate = createImportQueueItems([file('Том 4.fb2'), file('Книга 4.epub')]).map(restoredItem);
    const gap = createImportQueueItems([file('Том 4.fb2'), file('Том 6.epub')]).map(restoredItem);

    expect(validateImportQueue(unknown, 4).message).toBe('Укажите номер книги в серии.');
    expect(validateImportQueue([{ id: 'zero', volume: 0 }], 4).message).toBe('Номер книги — целое число от 1.');
    expect(validateImportQueue(duplicate, 4).message).toBe('Номер 4 уже у другой книги.');
    expect(validateImportQueue(gap, 4).message).toBe('Не хватает книги №5: номера должны идти подряд.');
    expect(validateImportQueue([{ id: 'old', volume: 2 }], 4).message).toBe('Книга №2 уже есть в саге — здесь нужен номер 4.');
  });

  it('uses the same move primitive for drag-and-drop and keyboard buttons', () => {
    const items = createImportQueueItems([file('Том 4.fb2'), file('Том 5.epub'), file('Том 6.epub')]);
    const moved = moveImportQueueItem(items, items[2]!.id, items[0]!.id);

    expect(moved.map((item) => item.volume)).toEqual([6, 4, 5]);
  });

  it('formats the saga rule and the completion in the writer words', () => {
    expect(formatImportSagaGuard(1)).toBe('Порядок книг нужен для хронологии саги. В саге пока нет книг — первая получит номер 1.');
    expect(formatImportSagaGuard(4)).toBe('В саге уже есть книги 1–3. Следующая получит номер 4, номера идут подряд.');
    expect(formatImportedBookCount(1)).toBe('1 книга добавлена и подготовлена.');
    expect(formatImportedBookCount(3)).toBe('3 книги добавлены и подготовлены.');
    expect(formatImportedBookCount(12)).toBe('12 книг добавлены и подготовлены.');
  });
});

describe('screening files before the transfer (UC3.1)', () => {
  // R3.1.1: a refused file gets the reason of its format or size and never enters the list.
  it('refuses PDF, DOC, archives and oversized files with their reasons and keeps the rest', () => {
    const good = file('Пламя Севера.docx');
    const { accepted, rejected } = screenImportFiles([
      file('rukopis.pdf'),
      file('chernovik.doc'),
      file('kniga.fb2.zip'),
      sized('большая.docx', 60 * 1024 * 1024),
      good,
    ], constraints);

    expect(accepted).toEqual([good]);
    expect(rejected.map(({ file: refused, message }) => [refused.name, message])).toEqual([
      ['rukopis.pdf', 'PDF не поддерживается: из него нельзя надёжно достать главы. Сохраните рукопись как DOCX (Word: Файл → Сохранить как → Документ Word) или загрузите FB2/EPUB.'],
      ['chernovik.doc', 'Этот формат не поддерживается. Откройте файл в Word или LibreOffice и сохраните как DOCX.'],
      ['kniga.fb2.zip', 'Архивы не поддерживаются. Распакуйте архив и загрузите файл .fb2, .epub или .docx.'],
      ['большая.docx', 'Файл больше 50 МБ. Разделите книгу на части или сохраните её без картинок.'],
    ]);
  });

  it('names TXT, RTF and an unknown format', () => {
    const { rejected } = screenImportFiles([file('заметки.txt'), file('глава.rtf'), file('книга.mobi')], constraints);

    expect(rejected.map(({ message }) => message)).toEqual([
      'TXT не поддерживается: в нём нет разметки глав. Сохраните текст как DOCX — главы с заголовками «Глава N» распознаются.',
      'Этот формат не поддерживается. Откройте файл в Word или LibreOffice и сохраните как DOCX.',
      'Формат «.mobi» не поддерживается. Подходят FB2, EPUB и DOCX.',
    ]);
  });
});

describe('book numbers of the local list (UC3.2)', () => {
  // R3.2.1: the TEST 28.10 scenario — a single novel without a number in its name.
  it('gives a single book without a number the next number of the saga', () => {
    const rows = numberLocalRows(stageFiles([file('Пламя Севера.docx')]), 1, {});

    expect(rows.map((row) => [row.volume, row.volumeSource])).toEqual([[1, 'order']]);
    expect(validateImportQueue(rows, 1).valid).toBe(true);
  });

  it('numbers files without numbers in list order and renumbers them on a move', () => {
    const staged = stageFiles([file('Начало.docx'), file('Середина.docx'), file('Конец.docx')]);

    expect(numberLocalRows(staged, 4, {}).map((row) => [row.fileName, row.volume])).toEqual([
      ['Начало.docx', 4], ['Середина.docx', 5], ['Конец.docx', 6],
    ]);
    const moved = moveLocal(staged, staged[2]!.key, staged[0]!.key);
    expect(numberLocalRows(moved, 4, {}).map((row) => [row.fileName, row.volume])).toEqual([
      ['Конец.docx', 4], ['Начало.docx', 5], ['Середина.docx', 6],
    ]);
  });

  it('leaves a file without a number empty when another file names one, until the author types it', () => {
    const staged = stageFiles([file('Том 2.fb2'), file('черновик.docx')]);
    const untyped = numberLocalRows(staged, 2, {});

    expect(untyped.map((row) => [row.volume, row.volumeSource])).toEqual([[2, 'file'], [null, 'file']]);
    expect(validateImportQueue(untyped, 2).issues).toEqual([
      { code: 'unknown_volume', itemId: staged[1]!.key, message: 'Укажите номер книги в серии.' },
    ]);

    const typed = numberLocalRows(staged, 2, { [staged[1]!.key]: 3 });
    expect(typed.map((row) => [row.volume, row.volumeSource])).toEqual([[2, 'file'], [3, 'manual']]);
    expect(validateImportQueue(typed, 2).valid).toBe(true);
  });

  it('names the missing first book of an empty saga', () => {
    const rows = numberLocalRows(stageFiles([file('Том 5.fb2')]), 1, {});

    expect(validateImportQueue(rows, 1).message).toBe('Не хватает книги №1: номера должны идти подряд.');
  });
});
