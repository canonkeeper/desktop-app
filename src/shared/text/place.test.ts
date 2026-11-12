import { describe, expect, it } from 'vitest';
import { missingPlace, paragraphsLabel, placeLabel, plainPlaces, withoutDeviceWords } from './place';

describe('the place of a book as the author reads it (ux1-names N1)', () => {
  it('says the paragraphs in words', () => {
    expect(paragraphsLabel()).toBe('');
    expect(paragraphsLabel(3)).toBe('абзац 3');
    expect(paragraphsLabel(3, 3)).toBe('абзац 3');
    expect(paragraphsLabel(3, 5)).toBe('абзацы 3–5');
  });

  it('joins the name the server gave with the paragraphs', () => {
    expect(placeLabel('Книга 2, гл. 12 „Белый порт“')).toBe('Книга 2, гл. 12 „Белый порт“');
    expect(placeLabel('Книга 2, гл. 12 „Белый порт“', 3, 5)).toBe('Книга 2, гл. 12 „Белый порт“, абзацы 3–5');
  });

  it('never falls back to the key of a chapter the text does not have', () => {
    expect(placeLabel(undefined)).toBe(missingPlace);
    expect(placeLabel(undefined, 4)).toBe(`${missingPlace}, абзац 4`);
  });

  it('replaces the links written into plain words by their places', () => {
    const places = { b1c14: 'Книга 1, гл. XIV', 481: 'Книга 7, гл. 481 „Северная пристань“' };
    expect(plainPlaces('Нарисуй сцену [т. 1, «Глава XIV» ¶33–35](source:b1c14/33-35): кто в кадре.', places))
      .toBe('Нарисуй сцену Книга 1, гл. XIV, абзацы 33–35: кто в кадре.');
    expect(plainPlaces('Смотри [гл. 481 ¶2](source:481/2) и [гл. 9](source:b9c99).', places))
      .toBe(`Смотри Книга 7, гл. 481 „Северная пристань“, абзац 2 и ${missingPlace}.`);
    expect(plainPlaces('Без ссылок [как есть] (и скобки).', places)).toBe('Без ссылок [как есть] (и скобки).');
  });

  it('says the words of the model without «¶» and keys while its place is not named yet', () => {
    expect(withoutDeviceWords('гл. 14 ¶33')).toBe('гл. 14 абзац 33');
    expect(withoutDeviceWords('т. 1, «Глава V» ¶5–6')).toBe('т. 1, «Глава V» абзацы 5–6');
    expect(withoutDeviceWords('т. 2, «Белый порт» [b2c12] ¶3')).toBe('т. 2, «Белый порт» абзац 3');
    expect(withoutDeviceWords('40 ¶')).toBe('40');
    expect(withoutDeviceWords('b9c99 ¶3').trim()).toBe('абзац 3');
    expect(withoutDeviceWords('abc1c2 и b2c')).toBe('abc1c2 и b2c');
  });
});
