import { fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../../api/mock';
import { ReaderCore } from './ReaderCore';
import { buildReaderLocator } from './reader-locator';

async function setup(extra: Partial<Parameters<typeof ReaderCore>[0]> = {}) {
  const chapter = await createMockApiClient().getChapter('chapter-12', 'published');
  const props = {
    chapter,
    title: 'Глава 12. Белый порт',
    locator: buildReaderLocator('project-white-port', 'book-02', chapter.id, '', '', chapter.revision),
    onCopySelection: vi.fn(),
    onFindSelection: vi.fn(),
    onCaptureSelection: vi.fn(),
    ...extra,
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

// specs/2026-11-10-ux7-reader.md RD1.1: the heading of the chapter stands once, over the text; the heading paragraph is not repeated.
it('shows the heading of the chapter once and its paragraphs as text', async () => {
  const { chapter } = await setup();
  expect(screen.getAllByRole('heading')).toHaveLength(1);
  expect(screen.getByRole('heading', { level: 1 }).textContent).toBe('Глава 12. Белый порт');
  expect(document.querySelectorAll('p[data-paragraph-id]')).toHaveLength(chapter.paragraphs.filter((item) => item.kind !== 'heading').length);
  expect(screen.queryByText(/ревизия/iu)).toBeNull();
});

// RD2.1: the words found are lit, the current one stronger; a link does not light its paragraph over them.
it('lights the words found and the current one stronger', async () => {
  await setup({ activeParagraphId: 'p-12-03', marks: new Map([['p-12-03', [{ start: 0, end: 4, current: true }, { start: 30, end: 33 }]]]) });
  const paragraph = document.querySelector('[data-paragraph-id="p-12-03"]')!;
  const marks = [...paragraph.querySelectorAll('mark')];
  expect(marks.map((mark) => mark.textContent)).toEqual(['Мара', paragraph.textContent!.slice(30, 33)]);
  expect(marks[0]!.hasAttribute('data-find-current')).toBe(true);
  expect(marks[1]!.hasAttribute('data-find-current')).toBe(false);
});

// RD3.3: a note of the chapter has a sign on the margin of its paragraph.
it('puts the sign of a note on the margin of its paragraph', async () => {
  const onOpenNote = vi.fn();
  await setup({ notes: [{ id: 'note-1', paragraphId: 'p-12-04', title: 'Проверить протокол' }], onOpenNote });
  const sign = screen.getByRole('button', { name: 'Заметка: Проверить протокол' });
  expect(sign.closest('div')!.parentElement!.querySelector('p')!.dataset.paragraphId).toBe('p-12-04');
  fireEvent.click(sign);
  expect(onOpenNote).toHaveBeenCalledWith('note-1');
});

describe('Reader native selection', () => {
  it('captures the complete selection with its first paragraph', async () => {
    const { article, paragraphs, onCaptureSelection, chapter } = await setup();
    const text = selectText(paragraphs[0]!, paragraphs[1]!);
    fireEvent.pointerUp(article);
    fireEvent.click(within(screen.getByRole('toolbar', { name: 'Действия с выделением' })).getByRole('button', { name: 'Заметка' }));
    const first = chapter.paragraphs.find((paragraph) => paragraph.id === paragraphs[0]!.dataset.paragraphId)!;
    expect(onCaptureSelection).toHaveBeenCalledWith(text, expect.objectContaining({
      chapterId: chapter.id,
      paragraphId: first.id,
      range: { startOffset: 0, endOffset: first.text.length, quote: first.text },
    }));
    expect(screen.queryByRole('toolbar')).toBeNull();
  });

  it('gives «Нарисовать» the words, their paragraphs and their place on the screen', async () => {
    const onDrawSelection = vi.fn();
    const { article, paragraphs } = await setup({ onDrawSelection });
    const text = selectText(paragraphs[0]!, paragraphs[1]!);
    fireEvent.pointerUp(article);
    fireEvent.click(screen.getByRole('button', { name: 'Нарисовать' }));
    expect(onDrawSelection).toHaveBeenCalledWith(expect.objectContaining({ text, paragraphIds: [paragraphs[0]!.dataset.paragraphId, paragraphs[1]!.dataset.paragraphId] }));
  });

  it('supports keyboard selection and dismisses stale actions when scrolling', async () => {
    const { article, paragraphs } = await setup();
    selectText(paragraphs[0]!);
    fireEvent.keyUp(article, { key: 'Shift' });
    expect(screen.getByRole('button', { name: 'Заметка' })).toBeTruthy();
    fireEvent.scroll(screen.getByRole('toolbar', { name: 'Действия с выделением' }));
    expect(screen.getByRole('button', { name: 'Заметка' })).toBeTruthy();
    fireEvent.scroll(window);
    expect(screen.queryByRole('button', { name: 'Заметка' })).toBeNull();
  });

  it('tracks long-press selection changes and removes the menu when the range is cleared', async () => {
    const { paragraphs } = await setup();
    selectText(paragraphs[0]!);
    fireEvent(document, new Event('selectionchange'));
    expect(screen.getByRole('button', { name: 'Заметка' })).toBeTruthy();
    window.getSelection()?.removeAllRanges();
    fireEvent(document, new Event('selectionchange'));
    expect(screen.queryByRole('button', { name: 'Заметка' })).toBeNull();
  });
});
