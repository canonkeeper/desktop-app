import { describe, expect, it } from 'vitest';
import type { BookRevertPreview } from '../../../entities/manuscript/api';
import { bookUpdateSummary, revertQuestion, revertUntilWords, updateLine } from './book-revert-words';

const preview = (parts: Partial<BookRevertPreview> = {}): BookRevertPreview => ({
  bookId: 'book', bookTitle: 'Туманный берег', updatedAt: '2026-11-12T10:00:00.000Z', revertUntil: '2026-11-19T10:00:00.000Z',
  fingerprint: '0'.repeat(32), text: [], place: [], returned: [], removed: [], kept: [], ...parts,
});

const chapter = (title: string, stay?: 'edited' | 'deleted' | 'returned') => ({ id: title, title, ...(stay ? { stay } : {}) });

// specs/2026-11-12-ux10-book-revert.md BR3.1: when the book was updated and until when the version before can come back.
describe('the words of an update of a book', () => {
  it('says when the book was updated, what changed and until when it can be returned', () => {
    const now = new Date(2026, 10, 12, 18, 0).getTime();
    expect(updateLine({ at: new Date(2026, 10, 12, 14, 20).toISOString() }, now)).toBe('Обновлена из файла сегодня в 14:20');
    expect(updateLine({ at: new Date(2026, 10, 3, 9, 0).toISOString() }, now)).toBe('Обновлена из файла 3 ноября');
    expect(revertUntilWords({ revertUntil: new Date(2026, 10, 19, 14, 20).toISOString() })).toBe('можно вернуть прежнюю версию до 19 ноября');
    expect(bookUpdateSummary({ changed: 2, added: 1, removed: 5 })).toBe('изменены 2 главы, добавлена 1 глава, удалено 5 глав');
    expect(bookUpdateSummary({ changed: 0, added: 0, removed: 0 })).toBe('без изменений');
  });
});

// BR3.2: the question of the return says what comes back, what goes, what stays and why.
describe('the question of a return', () => {
  it('names what comes back, what the file added and what stays', () => {
    const question = revertQuestion(preview({
      text: [chapter('Глава 2'), chapter('Глава 6')], place: [chapter('Глава третья')], returned: [chapter('Глава 5')],
      removed: [chapter('Новая'), chapter('Ещё новая')], kept: [chapter('Глава 1', 'edited'), chapter('Глава 7', 'deleted')],
    }));
    expect(question.title).toBe('Вернуть «Туманный берег» к версии до обновления?');
    expect(question.line).toBe('Вернутся: текст 2 глав, названия и места 1 главы, 1 убранная глава. '
      + 'Главы, которые добавил файл (2), уйдут в «Недавно удалённые».');
    expect(question.also).toBe('Останутся как сейчас: «Глава 1» — вы правили её после обновления; «Глава 7» — удалена после обновления. '
      + 'Помощник перечитает возвращённые главы.');
  });

  it('names the one chapter of the file and many kept chapters by a few names', () => {
    const question = revertQuestion(preview({
      place: [chapter('Глава 1')], removed: [chapter('Новая')],
      kept: ['А', 'Б', 'В', 'Г'].map((title) => chapter(title, 'edited')),
    }));
    expect(question.line).toBe('Вернутся: названия и места 1 главы. В «Недавно удалённые» уйдёт глава «Новая» — её добавил файл.');
    expect(question.also).toBe('Останутся как сейчас: «А», «Б», «В» и ещё 1 — вы правили их после обновления.');
  });

  it('says plainly when nothing would change', () => {
    const question = revertQuestion(preview({ kept: [chapter('Глава 1', 'returned')] }));
    expect(question.line).toBe('Всё, что изменил файл, уже поменяли после обновления — книга останется как сейчас.');
    expect(question.also).toBe('Останутся как сейчас: «Глава 1» — уже возвращена.');
  });
});
