import { describe, expect, it } from 'vitest';
import { findInChapter, findInText, findTerms, snippet } from './reader-find';

// specs/2026-11-10-ux7-reader.md RD2.1: the words found in a chapter, by their forms, in reading order.
describe('the words found in the text', () => {
  it('finds a word by its forms, with ё as е and any case', () => {
    const terms = findTerms('Порт');
    expect(findInText('В порту стояла белая пыль. Над портом — дым. Портовые краны, опора.', terms).map(({ end, start }) => [start, end]))
      .toEqual([[2, 7], [31, 37], [45, 53]]);
    expect(findInText('Ещё одна ЁЛКА', findTerms('елка'))).toEqual([{ start: 9, end: 13 }]);
  });

  it('finds the words of a phrase standing one after another', () => {
    const terms = findTerms('северной пристани');
    expect(findInText('Дым с северной пристани и южной пристани, северный ветер.', terms)).toEqual([{ start: 6, end: 23 }]);
  });

  it('numbers the places of a chapter by their paragraphs, from one', () => {
    const places = findInChapter('chapter-12', [{ id: 'a', text: 'Мара шла.' }, { id: 'b', text: 'Мары не было, Маре писали.' }], findTerms('Мара'));
    expect(places.map(({ paragraph, paragraphId, start }) => [paragraphId, paragraph, start])).toEqual([['a', 1, 0], ['b', 2, 0], ['b', 2, 14]]);
  });

  it('cuts a piece of the paragraph around a place for «Найдено»', () => {
    const text = 'Над портом стояла белая пыль, и мокрые канаты пахли солью сильнее обычного, а Мара первой заметила дым.';
    const start = text.indexOf('Мара');
    expect(snippet(text, { start, end: start + 4 }, 20)).toEqual({ before: '…сильнее обычного, а ', found: 'Мара', after: ' первой заметила дым.' });
  });
});
