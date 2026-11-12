import { afterEach, describe, expect, it } from 'vitest';
import { readingPlaceOf, rememberReadingPlace } from './reading-place';

// specs/2026-11-11-ux7-shelf.md SF1.1: the reader remembers in this browser the chapter of each book the author read last;
// the card of the shelf opens the book there.
describe('the place of reading of a book', () => {
  afterEach(() => window.localStorage.clear());

  it('remembers the last chapter of each book apart', () => {
    expect(readingPlaceOf('book-1')).toBeNull();
    rememberReadingPlace('book-1', 'chapter-3');
    rememberReadingPlace('book-2', 'chapter-9');
    rememberReadingPlace('book-1', 'chapter-4');
    expect(readingPlaceOf('book-1')).toBe('chapter-4');
    expect(readingPlaceOf('book-2')).toBe('chapter-9');
  });

  it('keeps the latest books only and survives a storage it cannot read', () => {
    for (let index = 0; index < 205; index += 1) rememberReadingPlace(`book-${index}`, `chapter-${index}`);
    expect(readingPlaceOf('book-0')).toBeNull();
    expect(readingPlaceOf('book-204')).toBe('chapter-204');
    window.localStorage.setItem('canonkeeper.reader.places.v1', '{not json');
    expect(readingPlaceOf('book-204')).toBeNull();
    rememberReadingPlace('book-1', 'chapter-1');
    expect(readingPlaceOf('book-1')).toBe('chapter-1');
  });
});
