import { describe, expect, it } from 'vitest';
import type { AccountQuota } from '../../../entities/account-quota/api';
import { formatCharacters, projectTextQuotaHint, sourceQuotaMessage, uploadsQuotaHint } from './quota-messages';

const counter = (used: number, limit: number) => ({ used, limit });
const quota: AccountQuota = {
  day: '2026-10-28',
  resetsAt: '2026-10-28T21:00:00Z',
  chatTurns: counter(4, 30),
  activeTurns: counter(0, 2),
  imports: counter(25, 30),
  images: counter(0, 30),
  projects: counter(3, 3),
  account: { books: counter(13, 100), chapters: counter(641, 5000), characters: counter(12_400_000, 25_000_000) },
  ownedProjects: [{
    projectId: 'project-1',
    sources: { books: counter(13, 60), chapters: counter(641, 3000), characters: counter(12_400_000, 15_000_000) },
  }],
};

describe('shelf quota texts (quota spec UC5)', () => {
  it('names the writer limits and the support address in refusals', () => {
    expect(sourceQuotaMessage('quota.project_characters', quota)).toBe(
      'Книга не поместилась: текст саги превысил бы 15 млн знаков. Удалите ненужные книги или напишите на support@canonkeeper.ru.',
    );
    expect(sourceQuotaMessage('quota.project_books', quota)).toContain('В саге уже 60 книг');
    expect(sourceQuotaMessage('quota.project_chapters', quota)).toContain('больше 3000 глав');
    expect(sourceQuotaMessage('quota.account_books', quota)).toContain('больше 100 книг');
    expect(sourceQuotaMessage('quota.account_characters', quota)).toContain('больше 25 млн знаков');
    expect(sourceQuotaMessage('quota.projects', quota)).toBe(
      'У вас уже 3 саги — это предел. Удалите ненужную сагу или напишите на support@canonkeeper.ru.',
    );
    expect(sourceQuotaMessage('quota.imports_daily', quota)).toBe(
      'Сегодня загружено 30 файлов — это дневной лимит. Загрузить ещё можно после 00:00 по Москве.',
    );
    expect(sourceQuotaMessage('quota.imports_daily', quota, { volumes: 13 })).toBe(
      'В очереди 13 книг, а сегодня можно загрузить ещё 5. Уберите лишние книги или загрузите их завтра.',
    );
    expect(sourceQuotaMessage('import.too_large', quota)).toBeUndefined();
  });

  it('hints from 80 % of a limit', () => {
    expect(uploadsQuotaHint(quota)).toBe('Сегодня можно загрузить ещё 5 файлов из 30.');
    expect(uploadsQuotaHint({ ...quota, imports: counter(23, 30) })).toBe('');
    expect(projectTextQuotaHint(quota, 'project-1')).toBe('Текст саги: 12,4 млн знаков из 15 млн.');
    expect(projectTextQuotaHint(quota, 'project-2')).toBe('');
    expect(formatCharacters(800)).toBe('800');
  });
});
