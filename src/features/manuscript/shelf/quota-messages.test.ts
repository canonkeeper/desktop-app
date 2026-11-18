import { describe, expect, it } from 'vitest';
import type { AccountQuota } from '../../../entities/account-quota/api';
import { formatCharacters } from '../../../entities/account-quota/refusal-words';
import { projectTextQuotaHint, sourceQuotaMessage, uploadsQuotaHint } from './quota-messages';

const counter = (used: number, limit: number) => ({ used, limit });
const quota: AccountQuota = {
  day: '2026-10-28',
  resetsAt: '2026-10-28T21:00:00Z',
  chatTurns: counter(4, 30),
  activeTurns: counter(0, 2),
  imports: counter(25, 30),
  images: counter(0, 30),
  imageUploads: counter(0, 30),
  chapterDrafts: counter(0, 20),
  drawingTurns: counter(0, 60),
  draftTurns: counter(0, 60),
  draftRewrites: counter(0, 200),
  projects: counter(3, 3),
  account: { books: counter(13, 100), chapters: counter(641, 5000), characters: counter(12_400_000, 25_000_000) },
  ownedProjects: [{
    projectId: 'project-1',
    sources: { books: counter(13, 60), chapters: counter(641, 3000), characters: counter(12_400_000, 15_000_000) },
  }],
  bookReloads: true,
  booksLoaded: 13,
};

describe('shelf quota texts (quota spec UC5)', () => {
  it('says a refusal in the words of the one table of refusals, without «лимит» and «Удалите ненужное»', () => {
    expect(sourceQuotaMessage('quota.project_characters', quota)).toBe(
      'Книга не поместилась: текст саги стал бы больше 15 млн знаков. Книги саги на месте. Новую книгу можно загрузить в другую сагу.',
    );
    // specs/2026-11-11-tier-limits.md TL2.5: the books of a saga by the counter of the one saga the author owns.
    expect(sourceQuotaMessage('quota.project_books', quota)).toContain('Книги не поместились: в этой саге стало бы больше 60 книг');
    expect(sourceQuotaMessage('quota.project_books', { ...quota, ownedProjects: [{ ...quota.ownedProjects[0]!, sources: { ...quota.ownedProjects[0]!.sources, books: counter(60, 60) } }] }))
      .toContain('В этой саге уже 60 книг из 60');
    expect(sourceQuotaMessage('quota.project_chapters', quota)).toContain('больше 3000 глав');
    expect(sourceQuotaMessage('quota.account_books', quota)).toContain('больше 100 книг');
    expect(sourceQuotaMessage('quota.account_characters', quota)).toContain('больше 25 млн знаков');
    expect(sourceQuotaMessage('quota.projects', quota)).toBe(
      'Место для новых саг закончилось — 3 из 3. Ваши саги на месте. Новая поместится, когда саг станет меньше.',
    );
    expect(sourceQuotaMessage('quota.imports_daily', { ...quota, imports: counter(30, 30) })).toBe(
      'Загрузки книг на сегодня закончились — 30 из 30. Новые — после 00:00 по Москве.',
    );
    expect(sourceQuotaMessage('quota.imports_daily', quota, { volumes: 13 })).toBe(
      'Выбрано 13 книг, а сегодня можно загрузить ещё 5. Уберите лишние книги или загрузите их завтра.',
    );
    for (const code of ['quota.project_characters', 'quota.projects', 'quota.imports_daily', 'quota.account_books']) {
      expect(sourceQuotaMessage(code, quota)).not.toMatch(/лимит|Удалите ненужное|предел/u);
    }
    expect(sourceQuotaMessage('import.too_large', quota)).toBeUndefined();
  });

  // specs/2026-11-18-free-tier.md FT3.2: a refusal of a book loaded once is a refusal of the shelf — the window of a new
  // version, the line of the upload — in the two cases of the live books.
  it('says a book loaded once by its code', () => {
    const once = { ...quota, bookReloads: false, account: { ...quota.account, books: counter(0, 1) }, booksLoaded: 1 };
    expect(sourceQuotaMessage('quota.book_reloads', once)).toBe('Книга на вашем тарифе загружается один раз. Новую версию из файла и книгу вместо удалённой загрузить нельзя. Удалённую книгу можно вернуть в «Недавно удалённых» в течение 7 дней.');
    expect(sourceQuotaMessage('quota.book_reloads', { ...once, account: { ...once.account, books: counter(1, 1) } }))
      .toBe('На вашем тарифе одна книга, и загружается она один раз. Ваша книга на месте: читать, спрашивать и править главы можно.');
  });

  it('hints from 80 % of a limit', () => {
    expect(uploadsQuotaHint(quota)).toBe('Сегодня можно загрузить ещё 5 файлов из 30.');
    expect(uploadsQuotaHint({ ...quota, imports: counter(23, 30) })).toBe('');
    expect(projectTextQuotaHint(quota, 'project-1')).toBe('Текст саги: 12,4 млн знаков из 15 млн.');
    expect(projectTextQuotaHint(quota, 'project-2')).toBe('');
    expect(formatCharacters(800)).toBe('800');
  });
});
