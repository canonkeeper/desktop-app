import { readFileSync } from 'node:fs';
import path from 'node:path';
import { render } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { MarkdownContent } from './MarkdownContent';

// specs/2026-11-11-ux9-shelf-chat.md SC3.7 (J-25): a wide table of an answer scrolls inside its own frame, and a shadow at the
// edge says there is more of it. The browser of the tests draws no backgrounds: the rule of the frame is read as written.
describe('a wide table of an answer', () => {
  it('scrolls inside its frame with a shadow at the edge that moves with the scroll', () => {
    const { container } = render(<MarkdownContent>{'| Глава | Кто | Что |\n| --- | --- | --- |\n| 12 | Мара | кинжал |'}</MarkdownContent>);
    const frame = container.querySelector('table')!.parentElement!;
    expect(getComputedStyle(frame).overflowX).toBe('auto');
    const css = readFileSync(path.join(import.meta.dirname, 'MarkdownContent.module.css'), 'utf8');
    const rule = /\.tableScroll \{([^}]*)\}/u.exec(css)?.[1] ?? '';
    expect(rule).toMatch(/radial-gradient/u);
    expect(rule).toMatch(/background-attachment: local, local, scroll, scroll;/u);
  });
});
