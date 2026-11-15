import { describe, expect, it } from 'vitest';
import titles from '../../../../backend/internal/domain/sagamemory/testdata/chapter-titles.json';
import table from '../../../../backend/internal/domain/sagamemory/testdata/places.json';
import { createMockApiClient } from '../repository';
import { createChapterOperations } from './chapters';
import { createMockRepositoryContext } from './context';
import { chapterPlace, linkedKeys, numberedTitle, titledNumber } from './places';

// specs/2026-11-10-ux1-names.md N1.1: the mock names a chapter exactly as the server does — one table for both.
describe('the place of a chapter in the mock', () => {
  it.each(table)('names «$title» as the server does', ({ book, order, title, place }) => {
    expect(chapterPlace(book, order, title)).toBe(place);
  });

  it('finds the chapter keys of the source links of a text', () => {
    expect(linkedKeys('Опора: [«Белый порт», ¶3](source:b2c12/3) и [гл. 481](source:481).')).toEqual(['b2c12', '481']);
    expect(linkedKeys(undefined)).toEqual([]);
  });
});

// specs/2026-11-15-reader-chapter-number.md RN1.1, RN1.5: the number of a chapter and its title without it by the table of
// the server (`sagamemory.TitledNumber`, `NumberedTitle`); a chapter without a place has no number but the one its title writes.
describe('the number of a chapter in the mock', () => {
  it.each(titles)('reads «$title» at $inBook as the server does', ({ title, inBook, number, name, place }) => {
    expect(inBook ? numberedTitle(title, inBook) : titledNumber(title)).toEqual({ number, name });
    if (place) expect(chapterPlace(1, inBook, title)).toBe(place);
  });

  // RN1.2, RN1.4: a deleted chapter leaves no gap — the contents, the chapter and its neighbours, the search of chapters and
  // the place of the next chapter say one number; «Недавно удалённые» name it without a place; «Вернуть» gives it back.
  it('numbers the chapters of a book without the gap of a deleted one', async () => {
    const api = createMockApiClient();
    const numbers = async () => (await api.listChapters('book-02')).data.map(({ id, displayNumber, displayTitle }) => `${id}:${displayNumber} ${displayTitle}`);
    const label = async () => (await api.getSagaContents('project-white-port', { sourceView: 'draft', bookId: 'book-02' })).chapters
      .find((item) => item.chapter.chapterId === 'chapter-16')?.chapter.label;
    expect(await numbers()).toEqual(['chapter-12:12 Белый порт', 'chapter-16:16 Возвращение к записи']);

    await api.deleteChapter('chapter-12');
    expect(await numbers()).toEqual(['chapter-16:15 Возвращение к записи']);
    const chapter = await api.getChapter('chapter-16', 'draft');
    expect(chapter.navigation).toMatchObject({ displayNumber: '15', displayTitle: 'Возвращение к записи', position: 15 });
    expect(chapter.navigation.previous).toMatchObject({ displayNumber: '14', displayTitle: 'Перед судом' });
    expect((await api.searchChapters({ query: '15', sourceView: 'draft', limit: 10 })).data).toEqual([
      expect.objectContaining({ chapterNumber: '15', chapterDisplayTitle: 'Возвращение к записи' }),
    ]);
    expect((await api.searchChapters({ query: '16', sourceView: 'draft', limit: 10 })).data).toEqual([]);
    expect(await label()).toBe('Книга 2, гл. 15 „Возвращение к записи“');
    expect((await api.listRecentlyDeleted()).chapters.items).toEqual([
      expect.objectContaining({ id: 'chapter-12', displayNumber: '', displayTitle: 'Белый порт' }),
    ]);

    await api.restoreChapter('chapter-12');
    expect(await numbers()).toEqual(['chapter-12:12 Белый порт', 'chapter-16:16 Возвращение к записи']);
    expect(await label()).toBe('Книга 2, гл. 16 „Возвращение к записи“');
  });

  // RN1.2: a living chapter without a published text — a chapter of a saga file without its published order — is not in
  // the contents of the reader and takes no place there: the chapter after it is named as the server names it.
  it('numbers a chapter after one without a published text by its place in the contents of the reader', async () => {
    const context = createMockRepositoryContext();
    const api = createChapterOperations(context);
    const published = context.state.chapters.find((item) => item.id === 'chapter-16')!;
    published.status = 'published';
    context.state.chapters.push({ ...published, id: 'chapter-14', title: 'Застава', order: 14, status: 'draft' });
    expect((await api.listChapters('book-02')).data.map(({ id, displayNumber }) => `${id}:${displayNumber}`))
      .toEqual(['chapter-12:12', 'chapter-14:14', 'chapter-16:15']);
    expect((await api.getChapter('chapter-16', 'draft')).navigation).toMatchObject({ displayNumber: '15', position: 15 });
  });
});
