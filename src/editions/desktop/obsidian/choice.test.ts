// SPDX-License-Identifier: AGPL-3.0-only
import { describe, expect, it } from 'vitest';
import { choiceState, emptyMemory, ownChoicesBelow, pruneMemory, type ChoiceMemory } from './choice';
import type { ObsidianTreeFolder, ObsidianVaultBook } from './obsidian-api';
import { materialChoiceWords, words } from './words';

const folder = (path: string, extra: Partial<ObsidianTreeFolder> = {}): ObsidianTreeFolder => ({
  path, name: path === '.' ? 'Северный предел' : path.slice(path.lastIndexOf('/') + 1), depth: path === '.' ? 0 : path.split('/').length,
  notes: 3, below: 0, skipped: [], ...extra,
});
const liveBook = (bookId: string, title: string, at: string, composition: Partial<Pick<ObsidianVaultBook, 'excluded' | 'nested'>> = {}): ObsidianVaultBook => ({
  bookId, title, folder: at, loadedAt: null, presence: 'present', excluded: [], nested: [], ...composition,
});
const records = Object.values(materialChoiceWords);

// specs/2026-11-15-desk-obsidian-web.md OW2.3–OW2.5, OW2.7 by the rules of the server (the table «what a nested folder may» of
// the review): the variants, the closed ones with their reasons, the defaults and the request of the preview.
describe('the choice of the folders of a vault', () => {
  it('offers «Не брать», «Книга» and the six kinds of records, and «Вместе с «…»» first below a chosen folder', () => {
    const state = choiceState([folder('.'), folder('Книга 1', { hint: { kind: 'book' } }), folder('Книга 1/Часть 1', { hint: { kind: 'skip' } })], [], emptyMemory);
    expect(state.rows.get('Книга 1')!.options.map((option) => option.label)).toEqual([words.skip, words.book, ...records]);
    expect(state.rows.get('Книга 1/Часть 1')!.options.map((option) => option.label)).toEqual([words.together('Книга 1'), words.skip, words.book, ...records]);
  });

  it('closes the book variant inside a book and around one, chosen or of the saga, saying why', () => {
    const tree = [
      folder('.'), folder('Книги'), folder('Книги/Туманный берег', { book: { bookId: 'b1', title: 'Туманный берег' } }),
      folder('Книги/Туманный берег/Часть 1'), folder('Ледяной причал', { hint: { kind: 'book' } }), folder('Ледяной причал/Часть 1', { hint: { kind: 'skip' } }),
      folder('Чёрный маяк', { book: { bookId: 'b2', title: 'Чёрный маяк' } }), folder('Чёрный маяк/Черновики'),
    ];
    const state = choiceState(tree, [liveBook('b1', 'Туманный берег', 'Книги/Туманный берег')], emptyMemory);
    const reason = (path: string) => state.rows.get(path)!.options.find((option) => option.value === 'book')!.reason;
    expect(reason('Ледяной причал/Часть 1')).toBe(words.whyInside('Ледяной причал'));
    expect(reason('Книги')).toBe(words.whyHolds('Туманный берег'));
    expect(reason('.')).toBe(words.whyHolds('Туманный берег'));
    // A book of «Недавно удалённые» holds its folder on the server too: a folder inside it is no book.
    expect(reason('Чёрный маяк/Черновики')).toBe(words.whyInside('Чёрный маяк'));
    expect(reason('Чёрный маяк')).toBeNull();
    // Inside the folder of a book of the saga the choice does not change (OW2.5).
    expect(state.rows.get('Книги/Туманный берег/Часть 1')).toMatchObject({ fixed: true, choice: 'together' });
  });

  it('takes the remembered choice, then the book of the folder, then the hint; else nothing — or «вместе с» for a nested folder', () => {
    const tree = [
      folder('.'), folder('Застава', { book: { bookId: 'b3', title: 'Застава' } }), folder('Книга 5', { hint: { kind: 'book' } }),
      folder('Книга 5/Герои', { hint: { kind: 'materials', material: 'character' } }), folder('Книга 5/Часть 1', { hint: { kind: 'skip' } }),
      folder('Места', { hint: { kind: 'materials', material: 'place' } }), folder('Шаблоны', { hint: { kind: 'skip' } }), folder('Дневник'),
    ];
    const memory: ChoiceMemory = { kinds: { Места: 'materials:other', Дневник: 'book' }, notes: [] };
    const state = choiceState(tree, [liveBook('b3', 'Застава', 'Застава')], memory);
    const choice = (path: string) => state.rows.get(path)!.choice;
    expect(choice('Места')).toBe('materials:other');
    expect(choice('Дневник')).toBe('book');
    expect(choice('Застава')).toBe('book');
    expect(choice('Книга 5')).toBe('book');
    expect(choice('Книга 5/Герои')).toBe('materials:character');
    expect(choice('Книга 5/Часть 1')).toBe('together');
    expect(choice('Шаблоны')).toBe('skip');
    expect(choice('.')).toBe('skip');
    expect(state.request).toEqual({
      books: ['Застава', 'Книга 5', 'Дневник'],
      materials: [{ path: 'Книга 5/Герои', kind: 'character' }, { path: 'Места', kind: 'other' }],
    });
  });

  it('keeps the parts of a book with it: no exclusion for a nested folder hinted «skip» (a root-book and a book of the second level)', () => {
    const rootBook = choiceState([folder('.'), folder('Часть 1', { hint: { kind: 'skip' } }), folder('Часть 2', { hint: { kind: 'skip' } })], [], { kinds: { '.': 'book' }, notes: [] });
    expect(rootBook.request).toEqual({ books: ['.'] });
    const second = choiceState([folder('.'), folder('Книга', { hint: { kind: 'book' } }), folder('Книга/Часть', { hint: { kind: 'skip' } })], [], emptyMemory);
    expect(second.request).toEqual({ books: ['Книга'] });
    expect(second.rows.get('Книга/Часть')!.choice).toBe('together');
    // «Не брать» of a nested folder is the author's alone: then it is an exclusion.
    const left = choiceState([folder('.'), folder('Книга', { hint: { kind: 'book' } }), folder('Книга/Часть', { hint: { kind: 'skip' } })], [], { kinds: { 'Книга/Часть': 'skip' }, notes: [] });
    expect(left.request).toEqual({ books: ['Книга'], exclude: ['Книга/Часть'] });
  });

  it('closes «Книга» by the folders the live books hold, also those the look did not mark (Sm2 of the review)', () => {
    // The look was read before «Связать»: it marks no book; a live book also holds a folder not in the look.
    const tree = [folder('.'), folder('Книги'), folder('Книги/Застава'), folder('Книги/Застава/Часть 1'), folder('Архив'), folder('Черновики')];
    const books = [liveBook('b1', 'Застава', 'Книги/Застава'), liveBook('b2', 'Порт', 'Архив/Порт')];
    const state = choiceState(tree, books, emptyMemory);
    const reason = (path: string) => state.rows.get(path)!.options.find((option) => option.value === 'book')?.reason ?? null;
    expect(state.rows.get('Книги/Застава')!.book).toEqual({ bookId: 'b1', title: 'Застава', live: true });
    expect(state.rows.get('Книги/Застава')!.choice).toBe('book');
    expect(reason('Книги/Застава')).toBeNull();
    expect(reason('Книги')).toBe(words.whyHolds('Застава'));
    expect(reason('Архив')).toBe(words.whyHolds('Порт'));
    expect(reason('Черновики')).toBeNull();
  });

  it('asks a book of the saga by the composition its link keeps, not by the memory of the browser (SM1 of the review)', () => {
    const tree = [
      folder('.'), folder('Книга 5', { book: { bookId: 'b5', title: 'Долгая зима' } }), folder('Книга 5/Герои', { hint: { kind: 'materials', material: 'character' } }),
      folder('Книга 5/Герои/Злодеи'), folder('Книга 5/Часть 1', { hint: { kind: 'skip' } }), folder('Книга 5/Часть 2', { hint: { kind: 'skip' } }),
      folder('Книга 5/Часть 2/Черновики'),
    ];
    const book = liveBook('b5', 'Долгая зима', 'Книга 5', { excluded: ['Книга 5/Часть 2/Черновики', 'Книга 5/Часть 1/Пролог.md'], nested: ['Книга 5/Герои'] });
    // The memory of the browser is clean, or says other things of the folders of the book: neither changes its composition.
    for (const memory of [emptyMemory, { kinds: { 'Книга 5/Часть 1': 'skip' as const, 'Книга 5/Часть 2/Черновики': 'together' as const }, notes: ['Книга 5/Часть 1/01 Встреча.md'] }]) {
      const state = choiceState(tree, [book], memory);
      expect(state.request).toEqual({
        books: ['Книга 5'], materials: [{ path: 'Книга 5/Герои', kind: 'character' }], exclude: ['Книга 5/Часть 2/Черновики', 'Книга 5/Часть 1/Пролог.md'],
      });
      expect(state.rows.get('Книга 5/Часть 1')).toMatchObject({ fixed: true, fixedWord: words.fixedPart });
      expect(state.rows.get('Книга 5/Часть 2/Черновики')).toMatchObject({ fixed: true, fixedWord: words.fixedLeft });
      // A folder of records of the book keeps the choice of the kind of its records and «Не брать».
      expect(state.rows.get('Книга 5/Герои')!.fixed).toBe(false);
      expect(state.rows.get('Книга 5/Герои')!.options.map((option) => option.label)).toEqual([words.skip, ...records]);
    }
    // The kind the author chose, his «Не брать»; without a hint the folder of records goes without a kind.
    expect(choiceState(tree, [book], { kinds: { 'Книга 5/Герои': 'materials:faction' }, notes: [] }).request.materials).toEqual([{ path: 'Книга 5/Герои', kind: 'faction' }]);
    expect(choiceState(tree, [book], { kinds: { 'Книга 5/Герои': 'skip' }, notes: [] }).request).toEqual({
      books: ['Книга 5'], exclude: ['Книга 5/Герои', 'Книга 5/Часть 2/Черновики', 'Книга 5/Часть 1/Пролог.md'],
    });
    const plain = tree.map((one) => (one.path === 'Книга 5/Герои' ? folder('Книга 5/Герои') : one));
    expect(choiceState(plain, [book], emptyMemory).request.materials).toEqual([{ path: 'Книга 5/Герои' }]);
    // A note of the records of the book the author left out stays out; a folder inside its folder of records takes the usual choice.
    const leftOut = choiceState(tree, [book], { kinds: {}, notes: ['Книга 5/Герои/Тим.md'] });
    expect(leftOut.request.exclude).toContain('Книга 5/Герои/Тим.md');
    expect(leftOut.rows.get('Книга 5/Герои/Злодеи')).toMatchObject({ fixed: false, choice: 'together' });
  });

  it('counts what is chosen and says why «Показать, что получится» waits: nothing chosen, the limits of a request', () => {
    expect(choiceState([folder('.'), folder('Дневник')], [], emptyMemory).reason).toBe(words.whyNone);
    const many = Array.from({ length: 201 }, (_, index) => folder(`Книга ${index}`));
    expect(choiceState([folder('.'), ...many], [], { kinds: Object.fromEntries(many.map((one) => [one.path, 'book'])), notes: [] }).reason).toBe(words.whyLimit);
    const notes = Array.from({ length: 2001 }, (_, index) => `Книга/${index}.md`);
    const excluded = choiceState([folder('.'), folder('Книга', { hint: { kind: 'book' } })], [], { kinds: {}, notes });
    expect(excluded).toMatchObject({ books: 1, materials: 0, reason: words.whyExcl });
  });

  it('says how many folders below a folded one have a choice of their own', () => {
    const tree = [folder('.'), folder('Герои', { hint: { kind: 'materials', material: 'character' } }), folder('Герои/Главные'), folder('Герои/Главные/Старые'), folder('Герои/Враги')];
    const state = choiceState(tree, [], { kinds: { 'Герои/Главные/Старые': 'skip', 'Герои/Враги': 'materials:faction' }, notes: [] });
    expect(ownChoicesBelow(state, 'Герои')).toBe(2);
  });

  it('forgets the folders the tree no longer has and the notes of such folders', () => {
    const memory: ChoiceMemory = { kinds: { 'Книга 1': 'book', Пропала: 'book' }, notes: ['Книга 1/01.md', 'Пропала/02.md'] };
    expect(pruneMemory(memory, [folder('.'), folder('Книга 1')])).toEqual({ kinds: { 'Книга 1': 'book' }, notes: ['Книга 1/01.md'] });
  });
});
