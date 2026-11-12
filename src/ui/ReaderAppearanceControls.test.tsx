import { render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ReaderAppearanceControls } from './ReaderAppearanceControls';

function renderControls() {
  render(
    <ReaderAppearanceControls columnWidth="normal" onColumnWidth={vi.fn()} onOnlyText={vi.fn()} onStepSize={vi.fn()} onTheme={vi.fn()}
      onlyText={false} step={3} steps={7} textSize={20} theme="light" />,
  );
  return screen.getByRole('switch', { name: 'Только текст' });
}

/** A device whose media answer as the given queries do. */
function device(matching: (query: string) => boolean) {
  vi.stubGlobal('matchMedia', (query: string) => ({ matches: matching(query), media: query, addEventListener: vi.fn(), removeEventListener: vi.fn() }));
}

// specs/2026-11-12-ux12-shell.md SH4.1 (П-19): the key of «Только текст» is named only where a keyboard is at hand.
describe('«Только текст» of the look of the text', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('names Esc with a mouse and a keyboard at hand', () => {
    device((query) => query === '(hover: hover) and (pointer: fine)');
    const toggle = renderControls();
    expect(document.getElementById(toggle.getAttribute('aria-describedby') ?? '')?.textContent).toBe('Шапка и панели уходят. Вернуть — Esc или эта же кнопка');
  });

  it('names only the button under a finger', () => {
    device((query) => query === '(pointer: coarse)');
    const toggle = renderControls();
    expect(document.getElementById(toggle.getAttribute('aria-describedby') ?? '')?.textContent).toBe('Шапка и панели уходят. Вернуть — эта же кнопка');
  });
});
