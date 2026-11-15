// SPDX-License-Identifier: AGPL-3.0-only
import { describe, expect, it } from 'vitest';
import type { SagaArchiveOpened, SagaArchiveReport } from '../../../entities/saga-file/api';
import { openedWords } from './words';

const clean: SagaArchiveReport = {
  books: 13, chapters: 612, cards: 612, chaptersWithoutCards: { count: 0, places: [] }, namesFresh: true, threads: true,
  materials: 4, memory: 9, notes: 3, chats: 6, chatsNotRead: [], coversSkipped: [],
  leftOut: { drafts: 0, drawings: 0, trashedBooks: 0, trashedChapters: 0, othersTurns: 0 }, sections: [],
};

const opened = (report: Partial<SagaArchiveReport>, more: Partial<SagaArchiveOpened> = {}): SagaArchiveOpened => ({
  projectId: 'project-9', name: 'Хроники Белого порта', renamed: false, report: { ...clean, ...report }, ...more,
});

const left = (leftOut: Partial<SagaArchiveReport['leftOut']>) => ({ leftOut: { ...clean.leftOut, ...leftOut } });

// specs/2026-11-13-desk-sagafile-web.md SW2.3 (board 44·31, the words of BZ1.3 of specs/2026-11-13-desk-boards4.md).
describe('the window «Сага открыта» says', () => {
  it('the saga by its name and its numbers, and that the helper knows it, without a note when the report gives no cause', () => {
    expect(openedWords(opened({}))).toEqual({
      message: 'Хроники Белого порта: 13 книг, 612 глав. Помощник уже знает эту сагу — читать книги заново не нужно',
      notes: [],
    });
  });

  it('agrees the numbers with their nouns', () => {
    expect(openedWords(opened({ books: 1, chapters: 1 })).message).toMatch(/^Хроники Белого порта: 1 книга, 1 глава\./u);
    expect(openedWords(opened({ books: 2, chapters: 2 })).message).toMatch(/: 2 книги, 2 главы\./u);
    expect(openedWords(opened({ books: 5, chapters: 5 })).message).toMatch(/: 5 книг, 5 глав\./u);
    expect(openedWords(opened({ books: 21, chapters: 11 })).message).toMatch(/: 21 книга, 11 глав\./u);
  });

  it('nothing of the helper when the file brought nothing he knows', () => {
    expect(openedWords(opened({ cards: 0, chaptersWithoutCards: { count: 612, places: [] } })).message).toBe('Хроники Белого порта: 13 книг, 612 глав');
  });

  it('each note alone, by its cause, in the words of the board', () => {
    const one = (report: Partial<SagaArchiveReport>, more: Partial<SagaArchiveOpened> = {}) => openedWords(opened(report, more)).notes.map((note) => note.text);
    expect(one({}, { renamed: true, name: 'Хроники Белого порта — из файла от 13 ноября' })).toEqual([
      'Сага с таким названием уже есть — эта названа „Хроники Белого порта — из файла от 13 ноября“',
    ]);
    expect(one({ chaptersWithoutCards: { count: 5, places: ['Книга 1, гл. 1 „Туманный берег“'] } })).toEqual([
      'Помощник ещё не знает 5 глав: он прочитает их, когда вы подключите помощника',
    ]);
    expect(one({ chatsNotRead: ['Кто убил капитана', 'Хронология осады'] })).toEqual([
      'Не перенеслись 2 чата: „Кто убил капитана“, „Хронология осады“ — их не удалось прочитать, когда сага сохранялась в файл',
    ]);
    expect(one({ coversSkipped: ['Белый порт'] })).toEqual(['Обложка книги „Белый порт“ не перенеслась']);
    expect(one(left({ drafts: 3, drawings: 12 }))).toEqual(['В файл не вошли 3 главы в работе и 12 рисунков — они остались там, где сага сохранялась']);
    expect(one(left({ trashedBooks: 1, trashedChapters: 4 }))).toEqual(['В файл не вошли 1 книга и 4 главы из „Недавно удалённых“']);
    expect(one(left({ othersTurns: 14 }))).toEqual(['В файл не вошли 14 вопросов других участников саги']);
    expect(one({ sections: ['workshops/writing/drafts.jsonl'] })).toEqual(['В файле есть главы в работе и рисунки. Свяжите приложение с аккаунтом, чтобы открыть их']);
  });

  it('one of a thing in the singular', () => {
    const one = (report: Partial<SagaArchiveReport>) => openedWords(opened(report)).notes.map((note) => note.text);
    expect(one({ chaptersWithoutCards: { count: 1, places: [] } })).toEqual(['Помощник ещё не знает 1 главу: он прочитает её, когда вы подключите помощника']);
    expect(one({ chaptersWithoutCards: { count: 2, places: [] } })).toEqual(['Помощник ещё не знает 2 главы: он прочитает их, когда вы подключите помощника']);
    expect(one({ chatsNotRead: ['Кто убил капитана'] })).toEqual(['Не перенёсся 1 чат: „Кто убил капитана“ — его не удалось прочитать, когда сага сохранялась в файл']);
    expect(one({ coversSkipped: ['Белый порт', 'Северный суд'] })).toEqual(['Обложки книг „Белый порт“, „Северный суд“ не перенеслись']);
    expect(one(left({ drafts: 1 }))).toEqual(['В файл не вошла 1 глава в работе — она осталась там, где сага сохранялась']);
    expect(one(left({ drawings: 1 }))).toEqual(['В файл не вошёл 1 рисунок — он остался там, где сага сохранялась']);
    expect(one(left({ drawings: 2 }))).toEqual(['В файл не вошли 2 рисунка — они остались там, где сага сохранялась']);
    expect(one(left({ trashedBooks: 1 }))).toEqual(['В файл не вошла 1 книга из „Недавно удалённых“']);
    expect(one(left({ trashedChapters: 4 }))).toEqual(['В файл не вошли 4 главы из „Недавно удалённых“']);
    expect(one(left({ othersTurns: 1 }))).toEqual(['В файл не вошёл 1 вопрос других участников саги']);
  });

  it('every note at once, in the order of the board, the places of the chapters the helper does not know with their note', () => {
    const { notes } = openedWords(opened({
      chaptersWithoutCards: { count: 5, places: ['Книга 1, гл. 1 „Туманный берег“', 'Книга 2, гл. 12 „Белый порт“'] },
      chatsNotRead: ['Кто убил капитана', 'Хронология осады'], coversSkipped: ['Белый порт'],
      ...left({ drafts: 3, drawings: 12, trashedBooks: 1, trashedChapters: 4, othersTurns: 14 }), sections: ['workshops/writing/drafts.jsonl'],
    }, { renamed: true, name: 'Хроники Белого порта — из файла от 13 ноября' }));
    expect(notes.map((note) => note.key)).toEqual(['name', 'unknown', 'chats', 'covers', 'workshops', 'trash', 'others', 'sections']);
    expect(notes[1]!.places).toEqual(['Книга 1, гл. 1 „Туманный берег“', 'Книга 2, гл. 12 „Белый порт“']);
  });
});
