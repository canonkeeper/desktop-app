import { fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { TextBackgroundContext } from './text-background';
import { TextSelectionMenu } from './TextSelectionMenu';

afterEach(() => {
  document.querySelector('[data-reader-phone-bar]')?.remove();
});

function sizeMenu(width: number, height: number) {
  Object.defineProperty(HTMLElement.prototype, 'offsetWidth', { configurable: true, get() { return this.getAttribute('role') === 'toolbar' ? width : 0; } });
  Object.defineProperty(HTMLElement.prototype, 'offsetHeight', { configurable: true, get() { return this.getAttribute('role') === 'toolbar' ? height : 0; } });
}

// specs/2026-11-10-ux7-reader.md RD3.1: one row of words under icons over the selection.
it('stands in one row over the selection, centred on it, with the items each page gives', () => {
  sizeMenu(320, 56);
  Object.defineProperty(document.documentElement, 'clientWidth', { configurable: true, value: 1440 });
  Object.defineProperty(document.documentElement, 'clientHeight', { configurable: true, value: 900 });
  render(<TextSelectionMenu noteWord="Заметка" onAddNote={vi.fn()} onAsk={vi.fn()} onCopy={vi.fn()} onDismiss={vi.fn()} onDraw={vi.fn()} onFind={vi.fn()}
    rect={{ left: 400, right: 600, top: 300, bottom: 324 }} />);
  const menu = screen.getByRole('toolbar', { name: 'Действия с выделением' });
  expect([...menu.querySelectorAll('button')].map((button) => button.textContent)).toEqual(['Спросить', 'Заметка', 'Найти', 'Копировать', 'Нарисовать']);
  expect(menu.style.top).toBe(`${300 - 56 - 8}px`);
  expect(menu.style.left).toBe(`${500 - 160}px`);
});

it('stands under the selection when there is no room over it', () => {
  sizeMenu(320, 56);
  render(<TextSelectionMenu onCopy={vi.fn()} onDismiss={vi.fn()} rect={{ left: 40, right: 140, top: 20, bottom: 44 }} />);
  expect(screen.getByRole('toolbar').style.top).toBe('52px');
});

// specs/2026-11-11-ux2-polish.md PL4.1: over the selection only where there is room under the line of the reader and the title
// of the chapter; otherwise under it.
it('stands under the selection when the room over it is below the ceiling the page names', () => {
  sizeMenu(320, 56);
  Object.defineProperty(document.documentElement, 'clientHeight', { configurable: true, value: 900 });
  const { rerender } = render(<TextSelectionMenu ceiling={() => 173} onCopy={vi.fn()} onDismiss={vi.fn()} rect={{ left: 400, right: 600, top: 193, bottom: 220 }} />);
  expect(screen.getByRole('toolbar').style.top).toBe(`${220 + 8}px`);
  rerender(<TextSelectionMenu ceiling={() => 173} onCopy={vi.fn()} onDismiss={vi.fn()} rect={{ left: 400, right: 600, top: 300, bottom: 324 }} />);
  expect(screen.getByRole('toolbar').style.top).toBe(`${300 - 56 - 8}px`);
});

it('keeps the bottom bar of a phone free', () => {
  sizeMenu(300, 56);
  Object.defineProperty(document.documentElement, 'clientHeight', { configurable: true, value: 844 });
  const bar = document.createElement('nav');
  bar.setAttribute('data-reader-phone-bar', '');
  bar.getBoundingClientRect = () => ({ top: 790, bottom: 844, left: 0, right: 390, height: 54, width: 390, x: 0, y: 790, toJSON: () => ({}) });
  Object.defineProperty(bar, 'offsetParent', { configurable: true, value: document.body });
  document.body.append(bar);
  render(<TextSelectionMenu onCopy={vi.fn()} onDismiss={vi.fn()} rect={{ left: 40, right: 140, top: 20, bottom: 760 }} />);
  expect(screen.getByRole('toolbar').style.top).toBe(`${790 - 56 - 8}px`);
});

// The chat keeps its own items: «В заметки» and «Копировать».
it('keeps the items of the chat', () => {
  const onAddNote = vi.fn();
  render(<TextSelectionMenu onAddNote={onAddNote} onCopy={vi.fn()} onDismiss={vi.fn()} rect={{ left: 40, top: 300, bottom: 320 }} />);
  const buttons = [...screen.getByRole('toolbar').querySelectorAll('button')];
  expect(buttons.map((button) => button.textContent)).toEqual(['В заметки', 'Копировать']);
  fireEvent.click(buttons[0]!);
  expect(onAddNote).toHaveBeenCalled();
});

// specs/2026-11-12-ux11-dark-core.md TH1.4: over a sheet of text the row follows its background; elsewhere the site's.
describe('the row over a sheet of text', () => {
  it('is painted in the background of the text it was opened from', () => {
    const rect = { left: 10, top: 300, bottom: 320 };
    const { unmount } = render(<TextBackgroundContext.Provider value="sepia"><TextSelectionMenu onCopy={() => undefined} onDismiss={() => undefined} rect={rect} /></TextBackgroundContext.Provider>);
    expect(screen.getByRole('toolbar', { name: 'Действия с выделением' }).getAttribute('data-reading-theme')).toBe('sepia');
    unmount();
    render(<TextSelectionMenu onCopy={() => undefined} onDismiss={() => undefined} rect={rect} />);
    expect(screen.getByRole('toolbar', { name: 'Действия с выделением' }).hasAttribute('data-reading-theme')).toBe(false);
  });
});
