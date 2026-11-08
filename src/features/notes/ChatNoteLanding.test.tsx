import { render } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { MarkdownContent } from '../../ui/MarkdownContent';
import { captureMessageText } from '../../shared/notes/note-capture';
import { quoteRanges, revealFolded } from './ChatNoteLanding';

describe('revealFolded', () => {
  it('opens the folded work that holds a noted narration line', () => {
    const fold = document.createElement('details');
    const rows = document.createElement('div');
    const line = document.createElement('section');
    rows.append(line);
    fold.append(rows);
    document.body.append(fold);

    revealFolded(line);

    expect(fold.open).toBe(true);
    fold.remove();
  });
});

// SEC-16 shows the address of a link in an answer as text (security-hardening UC3): a note keeps its
// highlight whether its quote was taken before that, without the address, or after, with it.
describe('quoteRanges over an answer with an outside link', () => {
  const answer = 'Сессия истекла. [Войдите заново](https://evil.example/login), чтобы продолжить.';

  it('finds a quote taken from the answer as it is shown now', () => {
    const { container } = render(<MarkdownContent>{answer}</MarkdownContent>);
    const { quote } = captureMessageText(container);

    expect(quote).toContain('(https://evil.example/login)');
    expect(quoteRanges(container, quote).map((range) => range.toString())).toEqual([quote]);
  });

  it('finds a quote taken before the address was shown', () => {
    const { container } = render(<MarkdownContent>{answer}</MarkdownContent>);
    const notedBefore = 'Войдите заново, чтобы продолжить';

    const ranges = quoteRanges(container, notedBefore);
    expect(ranges).toHaveLength(1);
    expect(ranges[0]!.toString()).toBe('Войдите заново (https://evil.example/login), чтобы продолжить');
  });
});
