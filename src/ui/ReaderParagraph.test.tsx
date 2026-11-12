import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ReaderParagraph, readerRuns } from './ReaderParagraph';

afterEach(cleanup);

// specs/2026-11-08-italic-bold-in-text.md TF5: a paragraph of the book shows its italic and bold by its Markdown subset;
// what is selected and linked goes by its text.
describe('a paragraph of the reader', () => {
  it('draws italic and bold by the Markdown of the paragraph, its words the text', () => {
    const onSelect = vi.fn();
    const { container } = render(<ReaderParagraph id="p-1" markdown="Он *шёл* домой **быстро** и ***тихо***." onSelect={onSelect} text="Он шёл домой быстро и тихо." />);
    expect(container.querySelector('em')?.textContent).toBe('шёл');
    expect(container.querySelector('strong')?.textContent).toBe('быстро');
    expect(container.querySelector('em > strong')?.textContent).toBe('тихо');
    const paragraph = container.querySelector('[data-paragraph-id="p-1"]')!;
    expect(paragraph.textContent).toBe('Он шёл домой быстро и тихо.');
    fireEvent.click(screen.getByRole('button', { pressed: false }));
    expect(onSelect).toHaveBeenCalledTimes(1);
  });

  it('shows as text a paragraph without faces, with escaped stars, or whose stars are its words', () => {
    expect(readerRuns('Просто текст.', 'Просто текст.')).toBeNull();
    expect(readerRuns('* * *', '\\* \\* \\*')).toBeNull();
    // A row the book wrote before faces: its stars are its words, so the text is shown as it is.
    expect(readerRuns('* * *', '* * *')).toBeNull();
    expect(readerRuns('5 *3* 2', '5 *3* 2')).toBeNull();
    expect(readerRuns('Глава 12. Белый порт', '## Глава 12. Белый порт')).toBeNull();
    const { container } = render(<ReaderParagraph highlighted id="p-2" markdown="5 *3* 2" text="5 *3* 2" />);
    expect(container.querySelector('em')).toBeNull();
    expect(container.querySelector('mark')?.textContent).toBe('5 *3* 2');
  });
});
