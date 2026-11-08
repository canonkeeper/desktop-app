import { fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../../api/mock';
import { ReaderCore } from './ReaderCore';
import { buildReaderLocator } from './reader-locator';

async function setup(canEditManuscript = false) {
  const chapter = await createMockApiClient().getChapter('chapter-12', 'published');
  const props = {
    chapter,
    locator: buildReaderLocator('project-white-port', 'book-02', chapter.id, '', '', chapter.revision),
    canEditManuscript,
    onCopySelection: vi.fn(),
    onFindInBook: vi.fn(),
    onParagraphSelect: vi.fn(),
    onCaptureSelection: vi.fn(),
  };
  const { container } = render(<ReaderCore {...props} />);
  const paragraphs = [...container.querySelectorAll<HTMLElement>('p[data-paragraph-id]')];
  return { ...props, paragraphs, article: container.querySelector('article')! };
}

function selectText(start: HTMLElement, end = start) {
  const first = document.createTreeWalker(start, NodeFilter.SHOW_TEXT).nextNode()!;
  const last = document.createTreeWalker(end, NodeFilter.SHOW_TEXT).nextNode()!;
  const range = document.createRange();
  range.setStart(first, 0);
  range.setEnd(last, Math.min(last.textContent!.length, 20));
  Object.defineProperty(range, 'getBoundingClientRect', { value: () => new DOMRect(80, 240, 160, 40) });
  const selection = window.getSelection()!;
  selection.removeAllRanges();
  selection.addRange(range);
  return selection.toString();
}

afterEach(() => window.getSelection()?.removeAllRanges());

it('shows the chapter position without its revision (R5.1)', async () => {
  const { article, chapter } = await setup();
  expect(article.querySelector('footer')?.textContent?.trim()).toBe(`${chapter.navigation.position} / ${chapter.navigation.total}`);
  expect(screen.queryByText(/ревизия/iu)).toBeNull();
});

describe('Reader native selection', () => {
  it('renders paragraphs as selectable text, not navigation buttons', async () => {
    const { paragraphs, onParagraphSelect } = await setup(true);
    expect(paragraphs[0]!.querySelector('button')).toBeNull();
    fireEvent.click(paragraphs[0]!);
    expect(onParagraphSelect).not.toHaveBeenCalled();
  });

  it.each([false, true])('captures the complete selection with its first paragraph, edit permission=%s', async (canEdit) => {
    const { article, paragraphs, onCaptureSelection, chapter, onParagraphSelect } = await setup(canEdit);
    const text = selectText(paragraphs[0]!, paragraphs[1]!);
    fireEvent.pointerUp(article);
    fireEvent.click(screen.getByRole('button', { name: 'В заметки' }));
    const first = chapter.paragraphs.find((paragraph) => paragraph.id === paragraphs[0]!.dataset.paragraphId)!;
    expect(onCaptureSelection).toHaveBeenCalledWith(text, expect.objectContaining({
      chapterId: chapter.id,
      paragraphId: first.id,
      range: { startOffset: 0, endOffset: first.text.length, quote: first.text },
    }));
    expect(onParagraphSelect).not.toHaveBeenCalled();
    expect(screen.queryByRole('button', { name: 'В заметки' })).toBeNull();
  });

  it('supports keyboard selection and dismisses stale actions when scrolling', async () => {
    const { article, paragraphs } = await setup();
    selectText(paragraphs[0]!);
    fireEvent.keyUp(article, { key: 'Shift' });
    expect(screen.getByRole('button', { name: 'В заметки' })).toBeTruthy();
    fireEvent.scroll(screen.getByRole('toolbar', { name: 'Действия с выделением' }));
    expect(screen.getByRole('button', { name: 'В заметки' })).toBeTruthy();
    fireEvent.scroll(window);
    expect(screen.queryByRole('button', { name: 'В заметки' })).toBeNull();
  });

  it('tracks long-press selection changes and removes the menu when the range is cleared', async () => {
    const { paragraphs } = await setup();
    selectText(paragraphs[0]!);
    fireEvent(document, new Event('selectionchange'));
    expect(screen.getByRole('button', { name: 'В заметки' })).toBeTruthy();
    window.getSelection()?.removeAllRanges();
    fireEvent(document, new Event('selectionchange'));
    expect(screen.queryByRole('button', { name: 'В заметки' })).toBeNull();
  });
});
