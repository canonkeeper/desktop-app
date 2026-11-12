import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { SagaChapterRef } from '../../entities/saga/api';
import { TimeScale } from './TimeScale';

afterEach(cleanup);

/** Three books: 3, 4 and 2 chapters. */
const chapters: SagaChapterRef[] = [[1, 3, 'Туманный берег'], [2, 4, 'Карта приливов'], [3, 2, 'Северный суд']].flatMap(([book, count, title]) =>
  Array.from({ length: count as number }, (_, index) => ({
    key: `b${book}c${index + 1}`, label: `Книга ${book}, гл. ${index + 1} „Глава ${index + 1}“`, chapterId: `c-${book}-${index}`,
    bookId: `book-${book}`, bookOrder: book as number, bookTitle: title as string, title: `Глава ${index + 1}`,
  })));

function renderScale(value: number) {
  const onMove = vi.fn();
  render(<TimeScale chapters={chapters} onMove={onMove} value={value} />);
  return { onMove, slider: screen.getByRole('slider', { name: 'Сага на момент главы' }) };
}

// specs/2026-11-11-ux8-map.md MP2: the scale has a name, says what it does and names its value by the place of the chapter.
describe('the scale «Сага на момент главы»', () => {
  it('names itself, says what it shows and reads its value as the place of the chapter, never its key', () => {
    const { slider } = renderScale(4);
    expect(screen.getByText('Что известно к этой главе — без того, что случилось позже')).toBeTruthy();
    expect(slider.getAttribute('aria-valuetext')).toBe('Книга 2, гл. 2 „Глава 2“');
    expect(screen.getByText('Книга 2, гл. 2 „Глава 2“')).toBeTruthy();
    expect(document.body.textContent).not.toMatch(/b\d+c\d+|Том/u);
    cleanup();
    const whole = renderScale(chapters.length - 1);
    expect(whole.slider.getAttribute('aria-valuetext')).toBe('Вся сага');
  });

  it('marks the start of every book; a press on a mark puts the scale on the first chapter of that book', () => {
    const { onMove } = renderScale(8);
    const marks = within(screen.getByRole('list', { name: 'Начала книг' })).getAllByRole('button');
    expect(marks.map((mark) => mark.textContent)).toEqual(['1', '2', '3']);
    expect(marks[1]!.getAttribute('title')).toBe('Книга 2 «Карта приливов»');
    fireEvent.click(marks[1]!);
    expect(onMove).toHaveBeenLastCalledWith(3);
    fireEvent.click(marks[2]!);
    expect(onMove).toHaveBeenLastCalledWith(7);
  });

  it('moves by a book with PageDown and PageUp, and to the start and the whole saga with Home and End', () => {
    const { onMove, slider } = renderScale(4);
    fireEvent.keyDown(slider, { key: 'PageDown' });
    expect(onMove).toHaveBeenLastCalledWith(7);
    fireEvent.keyDown(slider, { key: 'PageUp' });
    expect(onMove).toHaveBeenLastCalledWith(3);
    fireEvent.keyDown(slider, { key: 'Home' });
    expect(onMove).toHaveBeenLastCalledWith(0);
    fireEvent.keyDown(slider, { key: 'End' });
    expect(onMove).toHaveBeenLastCalledWith(8);
    cleanup();
    // From the start of a book PageUp goes to the start of the one before; from the last book PageDown goes to the whole saga.
    const atStart = renderScale(3);
    fireEvent.keyDown(atStart.slider, { key: 'PageUp' });
    expect(atStart.onMove).toHaveBeenLastCalledWith(0);
    cleanup();
    const inLast = renderScale(7);
    fireEvent.keyDown(inLast.slider, { key: 'PageDown' });
    expect(inLast.onMove).toHaveBeenLastCalledWith(8);
  });
});

// specs/2026-11-11-ux9-shelf-chat.md SC2.1 (К-03): the thumb and the marks of the books are targets of 44 px under a finger,
// by the rules of the scale itself.
describe('the targets of the scale under a finger', () => {
  it('makes the slider and a mark of a book 44 px high, a mark 44 px wide', () => {
    renderScale(4);
    const coarse = (element: Element, property: string) => [...document.styleSheets]
      .flatMap((sheet) => [...sheet.cssRules])
      .filter((rule): rule is CSSMediaRule => rule instanceof CSSMediaRule && rule.conditionText === '(pointer: coarse), (max-width: 899px)')
      .flatMap((rule) => [...rule.cssRules])
      .filter((rule): rule is CSSStyleRule => rule instanceof CSSStyleRule && element.matches(rule.selectorText))
      .map((rule) => rule.style.getPropertyValue(property))
      .find(Boolean);
    expect(coarse(screen.getByRole('slider', { name: 'Сага на момент главы' }), 'height')).toBe('44px');
    const mark = within(screen.getByRole('list', { name: 'Начала книг' })).getAllByRole('button')[0]!;
    expect(coarse(mark, 'height')).toBe('44px');
    expect(coarse(mark, 'min-width')).toBe('44px');
  });
});
