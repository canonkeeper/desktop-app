import { describe, expect, it } from 'vitest';
import table from '../../../../backend/internal/domain/sagamemory/testdata/places.json';
import { chapterPlace, linkedKeys } from './places';

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
