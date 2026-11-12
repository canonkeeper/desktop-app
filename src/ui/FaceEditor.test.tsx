import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import { useState } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { FaceEditor } from './FaceEditor';
import { richFieldOf } from './rich-field';

afterEach(cleanup);

/** The field over a piece of a working text, the piece kept by the page as the editor writes it. */
function renderField(initial: string, { open, readOnly = false }: { open?: string[]; readOnly?: boolean } = {}) {
  const changes: string[] = [];
  function Harness() {
    const [value, setValue] = useState(initial);
    return <FaceEditor label="Текст главы" onChange={(next) => { changes.push(next); setValue(next); }} readOnly={readOnly} value={value} {...(open ? { open } : {})} />;
  }
  render(<Harness />);
  const field = screen.getByRole('textbox', { name: 'Текст главы' });
  return { changes, field, rich: richFieldOf(field)!, last: () => changes.at(-1) };
}

/** A clipboard event with the given data, or one that collects what is put in it. */
function clipboardEvent(type: 'paste' | 'copy', data: Record<string, string> = {}) {
  const event = new Event(type, { bubbles: true, cancelable: true });
  Object.defineProperty(event, 'clipboardData', {
    value: { types: Object.keys(data), getData: (kind: string) => data[kind] ?? '', setData: (kind: string, value: string) => { data[kind] = value; }, clearData: () => undefined },
  });
  return { event, data };
}

// specs/2026-11-08-italic-bold-in-text.md TF6.1–TF6.5.
describe('the field of a chapter with its faces', () => {
  it('is a multiline text box of the author, its words read without marks', () => {
    const { field, rich } = renderField('Он ⟨шёл⟩ домой.\n\n⟦Ночь.⟧');
    expect(field.getAttribute('aria-multiline')).toBe('true');
    expect(field.hasAttribute('data-voice-authored')).toBe(true);
    expect(field.getAttribute('contenteditable')).toBe('true');
    expect(field.querySelector('em')?.textContent).toBe('шёл');
    expect(field.querySelector('[data-written]')?.textContent).toBe('Ночь.');
    expect(rich.text()).toBe('Он шёл домой.\n\nНочь.');
  });

  it('turns italic and bold on and off by Ctrl+I and Ctrl+B; undo takes it back', () => {
    const { field, rich, last } = renderField('Он шёл домой.');
    act(() => rich.select(3, 6));
    fireEvent.keyDown(field, { key: 'i', ctrlKey: true });
    expect(last()).toBe('Он ⟨шёл⟩ домой.');
    fireEvent.keyDown(field, { key: 'b', ctrlKey: true });
    expect(last()).toBe('Он ⟨⟪шёл⟫⟩ домой.');
    fireEvent.keyDown(field, { key: 'i', ctrlKey: true });
    expect(last()).toBe('Он ⟪шёл⟫ домой.');
    fireEvent.keyDown(field, { key: 'z', ctrlKey: true });
    expect(last()).toBe('Он ⟨⟪шёл⟫⟩ домой.');
  });

  it('breaks a line by Enter, a second one makes the next paragraph, Backspace takes one sign back', () => {
    const { field, rich, last } = renderField('Раздва');
    act(() => rich.select(3, 3));
    fireEvent.keyDown(field, { key: 'Enter' });
    expect(last()).toBe('Раз\nдва');
    fireEvent.keyDown(field, { key: 'Enter' });
    expect(last()).toBe('Раз\n\nдва');
    expect(field.querySelectorAll('p')).toHaveLength(2);
    fireEvent.keyDown(field, { key: 'Backspace' });
    expect(last()).toBe('Раз\nдва');
  });

  it('takes only italic and bold from pasted HTML, plain words from plain text, never a mark of the model', () => {
    const { field, rich, last } = renderField('Начало.  Конец.');
    act(() => rich.select(8, 8));
    act(() => {
      field.dispatchEvent(clipboardEvent('paste', {
        'text/html': '<b style="font-weight:normal" id="docs-internal-guid-1"><span style="font-style:italic">Тихо</span> <span style="color:red;font-size:30px">шёл</span> <strong>быстро</strong> <u>вдаль</u> <span data-written="">его</span>.</b>',
        'text/plain': 'Тихо шёл быстро вдаль его.',
      }).event);
    });
    expect(last()).toBe('Начало. ⟨Тихо⟩ шёл ⟪быстро⟫ вдаль его. Конец.');
    act(() => rich.select(0, 0));
    act(() => { field.dispatchEvent(clipboardEvent('paste', { 'text/plain': 'Строка ⟨скобки⟩\r\n\r\nабзац ' }).event); });
    expect(last()).toBe('Строка ‹скобки›\n\nабзац Начало. ⟨Тихо⟩ шёл ⟪быстро⟫ вдаль его. Конец.');
  });

  it('copies the faces as HTML and the plain words as text', () => {
    const { field, rich } = renderField('Он ⟨шёл⟩ ⟦домой⟧.\n\nДальше.');
    act(() => rich.select(0, 22));
    const { event, data } = clipboardEvent('copy');
    act(() => { field.dispatchEvent(event); });
    expect(data['text/plain']).toBe('Он шёл домой.\n\nДальше.');
    expect(data['text/html']).toContain('<em>шёл</em>');
  });

  it('is filled word by word: the faces around the words stay; a field that cannot be typed in is not filled', () => {
    const { rich, last } = renderField('Он ⟨шёл⟩ домой.');
    let filled = false;
    act(() => { filled = rich.fill('Он шёл домой быстро.'); });
    expect(filled).toBe(true);
    expect(last()).toBe('Он ⟨шёл⟩ домой быстро.');
    act(() => { rich.fill('Он шёл.'); });
    expect(last()).toBe('Он ⟨шёл⟩.');
    act(() => { rich.fill('Он шёл.\n\nНовый абзац.'); });
    expect(last()).toBe('Он ⟨шёл⟩.\n\nНовый абзац.');
    cleanup();
    const locked = renderField('Он шёл.', { readOnly: true });
    expect(locked.field.getAttribute('aria-readonly')).toBe('true');
    expect(locked.rich.fill('Другое.')).toBe(false);
    expect(locked.changes).toEqual([]);
  });

  it('starts inside the marks open before its piece and closes what an edit took away', () => {
    const { rich, last } = renderField('конец блока⟧ и своё', { open: ['⟦'] });
    act(() => rich.select(0, 12));
    act(() => rich.fill('и своё'));
    expect(last()).toBe('⟧и своё');
  });

  it('says the selection with its offsets in the piece and its faces', () => {
    const onSelection = vi.fn();
    render(<FaceEditor label="Поле" onChange={() => undefined} onSelection={onSelection} value="Он ⟨шёл⟩ домой." />);
    act(() => richFieldOf(screen.getByRole('textbox', { name: 'Поле' }))!.select(3, 6));
    expect(onSelection).toHaveBeenLastCalledWith(expect.objectContaining({ start: 4, end: 7, faces: { italic: true, bold: false } }));
  });
});
