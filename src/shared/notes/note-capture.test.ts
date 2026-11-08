import { describe, expect, it } from 'vitest';
import { captureMessageText } from './note-capture';

describe('captureMessageText', () => {
  it('copies only the visible answer, or a selection wholly inside it', () => {
    document.body.innerHTML = '<div><section id="answer">Первый <strong>ответ</strong>.</section><button>В заметки</button><p id="other">Другой ответ</p></div>';
    const content = document.getElementById('answer')!;
    const selection = window.getSelection()!;
    selection.removeAllRanges();
    expect(captureMessageText(content)).toEqual({ body: 'Первый ответ.', quote: 'Первый ответ.' });
    const range = document.createRange();
    range.selectNodeContents(content.querySelector('strong')!);
    selection.addRange(range);
    expect(captureMessageText(content)).toEqual({ body: 'ответ', quote: 'ответ' });
    selection.removeAllRanges();
    range.selectNodeContents(document.getElementById('other')!);
    selection.addRange(range);
    expect(captureMessageText(content)).toEqual({ body: 'Первый ответ.', quote: 'Первый ответ.' });
    selection.removeAllRanges();
    document.body.innerHTML = '';
  });
});
