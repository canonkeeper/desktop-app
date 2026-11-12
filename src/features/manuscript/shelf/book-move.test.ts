import { describe, expect, it } from 'vitest';
import { afterOf, dropPlace, movePlaces, moveReasonOf } from './book-move';

const books = [
  { id: 'b1', title: 'Туманный берег' },
  { id: 'b2', title: 'Белый порт' },
  { id: 'b3', title: 'Северный суд' },
  { id: 'b4', title: 'Последний свет' },
];

describe('the move of a volume (specs/2026-11-11-ux7-reorder.md RK2.1, RK2.2)', () => {
  it('offers the start and a place after every other book; the place it stands at is said, not chosen', () => {
    expect(movePlaces(books, 'b2')).toEqual([
      { afterBookId: null, label: 'В начало', current: false },
      { afterBookId: 'b1', label: 'После «Туманный берег» — сейчас здесь', current: true },
      { afterBookId: 'b3', label: 'После «Северный суд»', current: false },
      { afterBookId: 'b4', label: 'После «Последний свет» — в конец', current: false },
    ]);
    expect(movePlaces(books, 'b1')[0]).toEqual({ afterBookId: null, label: 'В начало — сейчас здесь', current: true });
    expect(movePlaces(books, 'b4').at(-1)).toEqual({ afterBookId: 'b3', label: 'После «Северный суд» — сейчас здесь', current: true });
  });

  it('puts a dragged book before or after the book it is let go over, and says when it stays', () => {
    expect(dropPlace(books, 'b2', 'b4', 'before')).toBe('b3');
    expect(dropPlace(books, 'b2', 'b4', 'after')).toBe('b4');
    expect(dropPlace(books, 'b3', 'b1', 'before')).toBeNull();
    expect(dropPlace(books, 'b2', 'b3', 'before')).toBeUndefined();
    expect(dropPlace(books, 'b2', 'b1', 'after')).toBeUndefined();
    expect(dropPlace(books, 'b2', 'b2', 'after')).toBeUndefined();
    expect(dropPlace(books, 'b9', 'b2', 'after')).toBeUndefined();
  });

  it('knows where «Отменить» puts a book back', () => {
    expect(afterOf(books, 'b1')).toBeNull();
    expect(afterOf(books, 'b3')).toBe('b2');
  });

  it('stands the volumes still while books are uploaded or read', () => {
    expect(moveReasonOf({ reading: false, uploading: false })).toBeNull();
    expect(moveReasonOf({ reading: true, uploading: false })).toBe('Пока помощник читает книги, переставить нельзя.');
    expect(moveReasonOf({ reading: true, uploading: true })).toBe('Пока идёт загрузка книг, переставить нельзя.');
  });
});
