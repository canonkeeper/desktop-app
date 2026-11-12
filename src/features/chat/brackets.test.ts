import { describe, expect, it } from 'vitest';
import { blanksOf, nextBlank, shiftBlanks, standingBlanks } from './brackets';

// specs/2026-11-12-ux12-shell.md SH1.1: the field knows the words in brackets it put itself, not every «[…]» of the text.
describe('the blanks of an example in the question field', () => {
  const example = 'Что [имя героя] знает о [событии] к главе [номер]?';
  const standing = (text: string, blanks: ReturnType<typeof blanksOf>) => standingBlanks(text, blanks).map(([start, end]) => text.slice(start, end));

  it('are the brackets of the example where it put them; the brackets of the author are none of them', () => {
    const blanks = blanksOf(example);
    expect(standing(example, blanks)).toEqual(['[имя героя]', '[событии]', '[номер]']);
    expect(standingBlanks('Что значит сноска [1] в главе 3?', [])).toEqual([]);
  });

  it('move with the text written before them and go when they are typed over', () => {
    let text = example;
    let blanks = blanksOf(example);
    const write = (next: string) => {
      blanks = shiftBlanks(text, next, blanks);
      text = next;
    };
    write(`Скажи: ${text}`);
    expect(standing(text, blanks)).toEqual(['[имя героя]', '[событии]', '[номер]']);
    write(text.replace('[имя героя]', 'М'));
    write(text.replace('Что М ', 'Что Мара '));
    expect(standing(text, blanks)).toEqual(['[событии]', '[номер]']);
    // Words written inside the brackets replace the blank as well: what stands there is the author's.
    write(text.replace('[событии]', '[событии с кинжалом]'));
    expect(standing(text, blanks)).toEqual(['[номер]']);
    // A footnote of his own after the example is no blank.
    write(`${text.replace('[номер]', '12')} Сноска [1].`);
    expect(standing(text, blanks)).toEqual([]);
  });

  it('come back with the words the author took back (Ctrl+Z), and only with them', () => {
    const blanks = blanksOf(example);
    const typed = example.replace('[имя героя]', 'Мара');
    const after = shiftBlanks(example, typed, blanks);
    expect(standing(typed, after)).toEqual(['[событии]', '[номер]']);
    const undone = shiftBlanks(typed, example, after);
    expect(standing(example, undone)).toEqual(['[имя героя]', '[событии]', '[номер]']);
    const erased = shiftBlanks(example, '', blanks);
    expect(standing('', erased)).toEqual([]);
  });

  it('are the only stops of Tab, around the text', () => {
    const text = `${example} Сноска [1].`;
    const ranges = standingBlanks(text, blanksOf(example));
    const word = (range: [number, number] | null) => (range ? text.slice(...range) : null);
    expect(word(nextBlank(ranges, [0, 0]))).toBe('[имя героя]');
    expect(word(nextBlank(ranges, ranges[2]!))).toBe('[имя героя]');
    expect(word(nextBlank(ranges, ranges[0]!, true))).toBe('[номер]');
    expect(nextBlank([], [0, 0])).toBeNull();
  });
});
