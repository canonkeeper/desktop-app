import { existsSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
import { hiddenIn } from '../../shared/copy/dictionary';
import type { AccountQuota } from './api';
import { refusalLine, refusalWords } from './refusal-words';

const quota: AccountQuota = {
  day: '2026-11-11',
  resetsAt: '2026-11-11T21:00:00Z',
  chatTurns: { used: 5, limit: 5 },
  activeTurns: { used: 2, limit: 2 },
  imports: { used: 30, limit: 30 },
  images: { used: 20, limit: 20 },
  imageUploads: { used: 30, limit: 30 },
  chapterDrafts: { used: 20, limit: 20 },
  drawingTurns: { used: 60, limit: 60 },
  draftTurns: { used: 60, limit: 60 },
  draftRewrites: { used: 200, limit: 200 },
  projects: { used: 3, limit: 3 },
  account: { books: { used: 12, limit: 12 }, chapters: { used: 900, limit: 5000 }, characters: { used: 1_000_000, limit: 25_000_000 } },
  ownedProjects: [{ projectId: 'p1', sources: { books: { used: 60, limit: 60 }, chapters: { used: 10, limit: 3000 }, characters: { used: 10, limit: 15_000_000 } } }],
};

/**
 * The words the author never reads in a refusal (specs/2026-11-10-ux2-patterns-refusals.md PR2.3): the hidden words of the
 * dictionary — «лимит», «квота», «исчерпан», «проект», «ход», «ИИ», «модель» and the rest — and the old advice.
 */
function banned(words: string) {
  return [...hiddenIn(words, 'src/entities/account-quota/refusal-words.ts').map((word) => word.says), ...(/Удалите ненужное/iu.test(words) ? ['«Удалите ненужное»'] : [])];
}

/**
 * Every refusal code of the fence of limits the contracts name: the site's and the desktop's (the open tree carries only
 * the desktop one).
 */
function contractCodes() {
  const contracts = ['contracts/openapi.json', 'contracts/openapi.desktop.json'].map((path) => join(process.cwd(), path)).filter((path) => existsSync(path));
  return [...new Set(contracts.flatMap((path) => readFileSync(path, 'utf8').match(/quota\.[a-z_]+/gu) ?? []))].sort();
}

describe('the words of a refusal by a limit (PR2.1, PR2.3)', () => {
  it('has the words for every code of the contract, with and without the numbers of the account', () => {
    const codes = contractCodes();
    expect(codes.length).toBeGreaterThanOrEqual(11);
    for (const code of [...codes, 'illustrations.locked']) {
      for (const read of [quota, undefined]) {
        const words = refusalWords(code, read);
        expect(words, code).not.toBeNull();
        expect(banned(words!.ended), code).toEqual([]);
        expect(banned(words!.returns), code).toEqual([]);
        expect(words!.ended.trim().length, code).toBeGreaterThan(0);
      }
    }
  });

  it('says what ended with the numbers of the counter beside it and when it comes back', () => {
    expect(refusalWords('quota.chat_turns_daily', quota)).toEqual({ ended: 'Вопросы и уточнения на сегодня закончились — 5 из 5', returns: 'Новые — после 00:00 по Москве.' });
    expect(refusalWords('quota.chat_turns_daily', undefined)?.ended).toBe('Вопросы и уточнения на сегодня закончились');
    expect(refusalWords('quota.images_daily', quota)?.ended).toBe('Рисунки на сегодня закончились — 20 из 20');
    expect(refusalWords('quota.account_books', quota)?.ended).toBe('Место для книг закончилось — 12 из 12 во всех сагах');
    expect(refusalWords('quota.projects', quota)?.ended).toBe('Место для новых саг закончилось — 3 из 3');
    expect(refusalWords('illustrations.locked', quota)?.returns).toMatch(/^Пробные не возвращаются\./u);
    expect(refusalWords('quota.chapter_drafts_daily', quota)?.ended).toBe('Письмо, проверка и вычитка глав на сегодня закончились — 20 из 20');
  });

  it('names no refusal of another kind', () => {
    expect(refusalWords('auth.email_unverified', quota)).toBeNull();
    expect(refusalWords(undefined, quota)).toBeNull();
  });

  it('says a refusal in one line where a line is all there is', () => {
    expect(refusalLine('quota.imports_daily', quota)).toBe('Загрузки книг на сегодня закончились — 30 из 30. Новые — после 00:00 по Москве.');
  });
});
