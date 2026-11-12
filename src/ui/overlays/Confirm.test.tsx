import { fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { Confirm, deletionLines } from './Confirm';

const realMatchMedia = window.matchMedia;

/** A window `width` CSS pixels wide, for the media queries the kit reads. */
function windowOf(width: number) {
  window.matchMedia = ((query: string) => {
    const max = /max-width:\s*(\d+)px/u.exec(query);
    return {
      matches: max ? width <= Number(max[1]) : false,
      media: query, onchange: null,
      addEventListener: () => undefined, removeEventListener: () => undefined,
      addListener: () => undefined, removeListener: () => undefined, dispatchEvent: () => false,
    };
  }) as typeof window.matchMedia;
}

afterEach(() => {
  window.matchMedia = realMatchMedia;
});

function ask(props: Partial<Parameters<typeof Confirm>[0]> = {}) {
  const onCancel = vi.fn();
  const onConfirm = vi.fn();
  const title = props.title ?? 'Удалить книгу «Белый порт»?';
  render(<Confirm action="Удалить книгу" onCancel={onCancel} onConfirm={onConfirm} title={title} {...props}>{props.children ?? deletionLines.returns}</Confirm>);
  return { onCancel, onConfirm, dialog: screen.getByRole('dialog', { name: title }) };
}

// specs/2026-11-10-ux2-patterns-actions.md PA2.1: the question, one line, «Отмена» on the left and the action by the
// verb of the question on the right; red only for what has no way back; on a phone a sheet from below.
describe('Confirm', () => {
  it('asks the question with one line and «Отмена» before the action by the same verb', () => {
    windowOf(1440);
    const { dialog, onConfirm } = ask();
    expect(within(dialog).getByRole('heading', { name: 'Удалить книгу «Белый порт»?' })).toBeTruthy();
    expect(within(dialog).getByText('Можно вернуть 7 дней — в «Недавно удалённых».')).toBeTruthy();
    const buttons = [...dialog.querySelectorAll('[data-confirm] > div:last-child button')].map((button) => button.textContent);
    expect(buttons).toEqual(['Отмена', 'Удалить книгу']);
    fireEvent.click(within(dialog).getByRole('button', { name: 'Удалить книгу' }));
    expect(onConfirm).toHaveBeenCalledOnce();
  });

  it('is red only for what has no way back, and the voice asks the author before either', () => {
    windowOf(1440);
    ask();
    const kept = screen.getByRole('button', { name: 'Удалить книгу' });
    expect(kept.getAttribute('data-variant')).toBe('primary');
    expect(kept.getAttribute('data-voice-consent')).toBe('yes');
    // A way back: the focus is on the action.
    expect(document.activeElement).toBe(kept);
  });

  it('puts the focus on «Отмена» when there is no way back', () => {
    windowOf(1440);
    ask({ action: 'Удалить чат', irreversible: true, title: 'Удалить чат «Мара»?', children: deletionLines.gone });
    const gone = screen.getByRole('button', { name: 'Удалить чат' });
    expect(gone.getAttribute('data-variant')).toBe('danger');
    expect(document.activeElement).toBe(screen.getByRole('button', { name: 'Отмена' }));
  });

  it('is a sheet from below at the width of a phone', () => {
    windowOf(390);
    const { dialog } = ask();
    expect(dialog.getAttribute('data-kind')).toBe('sheet');
    windowOf(1440);
  });

  it('is a card at the width of a computer', () => {
    windowOf(1440);
    const { dialog } = ask();
    expect(dialog.getAttribute('data-kind')).toBe('dialog');
  });

  it('cancels by Esc and by «Отмена», and not while the action runs', () => {
    windowOf(1440);
    const { dialog, onCancel } = ask();
    fireEvent.keyDown(dialog, { key: 'Escape' });
    expect(onCancel).toHaveBeenCalledOnce();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Отмена' }));
    expect(onCancel).toHaveBeenCalledTimes(2);
  });

  it('keeps the window and says why when the action did not happen; busy is on the action', () => {
    windowOf(1440);
    const { dialog, onCancel } = ask({ busy: true, error: 'Не удалось удалить книгу.' });
    expect(within(dialog).getByRole('alert').textContent).toBe('Не удалось удалить книгу.');
    expect(within(dialog).getByRole('button', { name: 'Удалить книгу' }).getAttribute('aria-busy')).toBe('true');
    fireEvent.keyDown(dialog, { key: 'Escape' });
    expect(onCancel).not.toHaveBeenCalled();
  });
});
