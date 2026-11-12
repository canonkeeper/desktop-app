import { describe, expect, it } from 'vitest';
import type { AccountQuota } from '../../../entities/account-quota/api';
import type { ImportQueue, ImportQueueItem } from '../../../entities/manuscript/api';
import {
  fitWords,
  localFiles,
  moveRow,
  orderRows,
  screenFiles,
  shelfEntries,
  similarBook,
  uploadEndWords,
  uploadLine,
  uploadRoom,
  volumeViews,
  volumeWord,
} from './upload-files';

/** The words the upload never says to the author (UP3.1): the machinery behind it. */
const machineryWords = ['очеред', 'импорт', 'передач', 'предпровер', 'автозапуск', 'текущий файл'];
const constraints = { allowedExtensions: ['.fb2', '.epub', '.docx', '.txt', '.md', '.rtf', '.odt', '.fb2.zip'], maxFileSizeBytes: 52_428_800 };
const file = (name: string, body = 'book', type = '') => new File([body], name, { type });

function item(position: number, patch: Partial<ImportQueueItem> = {}): ImportQueueItem {
  return {
    id: `item-${position}`, clientItemId: `client-${position}`, position, sourceFileName: `Том ${position}.fb2`,
    mimeType: null, sizeBytes: 4, preflightStatus: 'ready', preflightError: null, processingStatus: 'queued',
    failureClass: null, failedStep: null, importJobId: `job-${position}`, bookId: null, updatedAt: '2026-11-11T10:00:00Z',
    links: { status: null }, ...patch,
  };
}

function queue(items: ImportQueueItem[], status: ImportQueue['status'] = 'running'): ImportQueue {
  return {
    id: 'queue-1', projectId: 'project-1', status, version: 3, intendedItemCount: items.length, currentItemId: null, items,
    createdAt: '2026-11-11T10:00:00Z', updatedAt: '2026-11-11T10:00:00Z', startedAt: null, completedAt: null,
    links: { self: '/q', cancel: null },
  };
}

describe('the check of the files before any transfer (UP1.2)', () => {
  it('names the reason of each file that does not fit and keeps the files that do', () => {
    const { chosen, refused } = screenFiles(localFiles([
      file('Том 1.fb2'), file('рукопись.pdf'), file('пустой.docx', ''), file('Том 2.epub'), file('архив.rar'),
    ]), constraints);
    expect(chosen.map((each) => each.file.name)).toEqual(['Том 1.fb2', 'Том 2.epub']);
    expect(refused.map((each) => [each.file.name, each.reason.split(':')[0]])).toEqual([
      ['рукопись.pdf', 'PDF не поддерживается'],
      ['пустой.docx', 'Файл пустой — в нём нет текста. Выберите файл книги.'],
      ['архив.rar', 'Из архивов читается только книга FB2 в .fb2.zip. Распакуйте архив и загрузите файл книги'],
    ]);
  });
});

describe('the order of the volumes (UP2.3)', () => {
  it('puts the numbered files by their numbers and the others below to be checked; a move is the only order', () => {
    const { chosen } = screenFiles(localFiles([file('черновик.docx'), file('Том 3.fb2'), file('01-tuman.epub')]), constraints);
    const rows = orderRows(chosen);
    expect(rows.map((row) => [row.file.name, row.check])).toEqual([['01-tuman.epub', false], ['Том 3.fb2', false], ['черновик.docx', true]]);
    const moved = moveRow(rows, rows[2]!.key, rows[0]!.key);
    expect(moved.map((row) => row.file.name)).toEqual(['черновик.docx', '01-tuman.epub', 'Том 3.fb2']);
  });

  it('checks nothing when no file names a number', () => {
    const { chosen } = screenFiles(localFiles([file('Северный суд.docx'), file('Белый порт.fb2')]), constraints);
    expect(orderRows(chosen).map((row) => row.check)).toEqual([false, false]);
  });
});

describe('a file like a book of the shelf (UP2.2)', () => {
  const books = [{ id: 'b1', title: 'Туманный берег', place: 1 }, { id: 'b2', title: 'Том 5', place: 2 }];

  it('is the book of the same title, of the same place or of the same number in its title', () => {
    expect(similarBook('туманный берег.fb2', books)?.id).toBe('b1');
    expect(similarBook('Туманный_берег (исправлено).docx', books)?.id).toBe('b1');
    expect(similarBook('tom-2.epub', books)?.id).toBe('b2');
    expect(similarBook('Том 5. Правка.fb2', books)?.id).toBe('b2');
    expect(similarBook('Северный суд.fb2', books)).toBeNull();
  });

  // specs/2026-11-11-ux9-shelf-chat.md SC1.3 (L-51, Ф-2): a new version under another name is the book that begins so.
  it('is the book whose beginning the file begins with, whatever its name', () => {
    const opened = [
      { id: 'b1', title: 'Туманный берег', place: 1, opening: 'Туман лежал на Белом порту, и Мара ждала у воды. Ветер нёс соль.' },
      { id: 'b2', title: 'Том 5', place: 2, opening: 'Утро.' },
    ];
    const text = 'Хроники\nКнига первая\nГлава 1\nТУМАН лежал на «Белом» порту — и Мара ждала у воды.\nВетер нёс соль и ещё что-то.';
    expect(similarBook('рукопись-исправленная.docx', opened, text)?.id).toBe('b1');
    // Too short a beginning tells nothing; another text is no book of the shelf.
    expect(similarBook('рукопись.docx', [opened[1]!], 'Утро. Дальше другое.')).toBeNull();
    expect(similarBook('рукопись.docx', opened, 'Совсем другая книга о другом море и других людях.')).toBeNull();
    expect(similarBook('рукопись.docx', opened, null)).toBeNull();
  });
});

describe('what the tier takes before any transfer (UP1.3)', () => {
  const quota = {
    imports: { used: 1, limit: 30 },
    account: { books: { used: 4, limit: 30 } },
    ownedProjects: [{ projectId: 'project-1', sources: { books: { used: 4, limit: 6 } } }],
  } as unknown as AccountQuota;

  it('is the least room of the saga, of all the sagas and of the day, with the words of what fits', () => {
    const room = uploadRoom(quota, 'project-1');
    expect(room).toEqual({ room: 2, code: 'quota.project_books' });
    expect(fitWords(room!, 5)).toEqual({
      ended: 'В этой саге поместится 2 из 5 книг', returns: 'Остальные можно загрузить в другую сагу.',
    });
    expect(fitWords({ room: 1, code: 'quota.imports_daily' }, 3).ended).toBe('Сегодня можно загрузить ещё 1 книгу из 3');
    expect(uploadRoom(undefined, 'project-1')).toBeNull();
  });
});

describe('the upload on the shelf (UP3.1, UP3.2)', () => {
  it('says the state of each volume by the queue and the file of this tab, in the words of the author', () => {
    const running = queue([
      item(1, { processingStatus: 'succeeded', bookId: 'book-1' }),
      item(2, { processingStatus: 'failed', failedStep: 'import', failureClass: 'unexpected_deterministic_failure' }),
      item(3, { processingStatus: 'importing' }),
      item(4, { processingStatus: 'queued' }),
    ]);
    const views = volumeViews(running, {}, new Set(['book-1']));
    expect(views.map((view) => [view.position, volumeWord(view)])).toEqual([[2, 'Не загрузилась'], [3, 'Загружается'], [4, 'Ждёт загрузки']]);
    expect(views[0]).toMatchObject({ failedJobId: 'job-2' });

    const sending = queue([item(1, { preflightStatus: 'uploading', processingStatus: 'uploading', importJobId: null }),
      item(2, { preflightStatus: 'uploading', processingStatus: 'uploading', importJobId: null })], 'uploading');
    const sent = volumeViews(sending, { 'client-1': { state: 'sending', sent: 40, total: 100 }, 'client-2': { state: 'waiting', sent: 0, total: null } }, new Set());
    expect(sent.map(volumeWord)).toEqual(['Загружается · 40 %', 'Ждёт загрузки']);
    // After a reload this tab has no file: the volume says so on its card.
    expect(volumeViews(sending, {}, new Set()).map(volumeWord)).toEqual(['Файл не дошёл', 'Файл не дошёл']);
  });

  it('keeps a volume that waits at its place between the books of the upload', () => {
    const running = queue([
      item(1, { processingStatus: 'succeeded', bookId: 'book-1' }),
      item(2, { processingStatus: 'failed', preflightStatus: 'invalid', preflightError: { code: 'invalid_epub_container', message: 'x' }, failedStep: 'preflight', importJobId: null }),
      item(3, { processingStatus: 'indexing', bookId: 'book-3' }),
    ]);
    const books = [{ id: 'old' }, { id: 'book-1' }, { id: 'book-3' }];
    const views = volumeViews(running, {}, new Set(books.map((book) => book.id)));
    const entries = shelfEntries(books, running, views);
    expect(entries.map((entry) => entry.kind === 'book' ? entry.book.id : `card ${entry.volume.position}`))
      .toEqual(['old', 'book-1', 'card 2', 'book-3']);
    expect(views[0]).toMatchObject({ reasonCode: 'invalid_epub_container', failedJobId: null });
  });

  it('says the line of the saga with the count and the volume that goes, without the words of the machinery', () => {
    const running = queue([
      item(1, { processingStatus: 'succeeded', bookId: 'book-1' }), item(2, { processingStatus: 'succeeded', bookId: 'book-2' }),
      item(3, { processingStatus: 'importing', sourceFileName: 'Том 8. Ледяной причал.fb2' }), item(4), item(5),
    ]);
    const views = volumeViews(running, {}, new Set(['book-1', 'book-2']));
    const line = uploadLine(running, views, false);
    expect(line).toMatchObject({ title: 'Загружаю книги: 2 из 5 готово, идёт «Том 8. Ледяной причал»', remaining: 3, progress: 0.4 });
    const waiting = queue([item(1, { processingStatus: 'succeeded', bookId: 'book-1' }), item(2, { processingStatus: 'failed', failedStep: 'import' })]);
    const waitingLine = uploadLine(waiting, volumeViews(waiting, {}, new Set(['book-1'])), false);
    expect(waitingLine.title).toBe('Загружаю книги: 1 из 2 готово · «Том 2» ждёт вашего решения');
    expect(waitingLine.remaining).toBe(0);
    for (const words of [line.title, line.detail, waitingLine.title, waitingLine.detail, ...views.map(volumeWord)]) {
      for (const machinery of machineryWords) expect(words.toLowerCase()).not.toContain(machinery);
    }
  });
});

describe('the end of an upload (UP4.1, UP4.3)', () => {
  it('counts books and chapters and says no «готова» while the saga is read', () => {
    expect(uploadEndWords(12, 214, true)).toEqual({
      message: 'Загружено 12 книг: 214 глав',
      detail: 'Их уже можно читать и спрашивать. Карта саги будет через несколько минут',
      look: 'Посмотреть, как разобрались',
    });
    const one = uploadEndWords(1, 18, true);
    expect(one.message).toBe('Книга загружена: 18 глав');
    expect(`${one.message} ${one.detail}`).not.toMatch(/готов/u);
    expect(uploadEndWords(1, 18, false).detail).toBe('Помощник прочитал её — можно спрашивать');
  });

  // specs/2026-11-12-desk-window.md WN3.2: while the reading waits for the helper the end says the line of the reading.
  it('says the line of the reading that waits for the helper', () => {
    const line = 'Помощник ещё не подключён. Читать книги и искать по ним можно уже сейчас';
    expect(uploadEndWords(1, 18, false, line)).toEqual({ message: 'Книга загружена: 18 глав', detail: line, look: 'Как разобралась книга' });
    expect(uploadEndWords(3, 40, false, line).detail).toBe(line);
  });
});
