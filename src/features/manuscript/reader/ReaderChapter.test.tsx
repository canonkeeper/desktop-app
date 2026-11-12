import { fireEvent, render, screen } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../../api/mock';
import { ChapterTurn } from './ReaderChapter';

// specs/2026-11-10-ux7-reader.md RD1.3: the step between chapters at the end of the text.
it('names the neighbouring chapters and their place in the book, and keeps the steps at the ends with their reasons', async () => {
  const chapter = await createMockApiClient().getChapter('chapter-12', 'published');
  const onOpenChapter = vi.fn();
  const { rerender } = render(<ChapterTurn chapter={chapter} onOpenChapter={onOpenChapter} />);
  const turn = screen.getByRole('navigation', { name: 'Навигация по главам' });
  expect([...turn.children].map((item) => item.textContent)).toEqual(['Глава 11', '12 из 48', 'Глава 13']);
  fireEvent.click(screen.getByRole('button', { name: 'Предыдущая глава: глава 11' }));
  fireEvent.click(screen.getByRole('button', { name: 'Следующая глава: глава 13' }));
  expect(onOpenChapter.mock.calls).toEqual([['chapter-11'], ['chapter-13']]);
  onOpenChapter.mockClear();

  rerender(<ChapterTurn chapter={{ ...chapter, navigation: { ...chapter.navigation, previous: null, previousChapterId: null, next: null, nextChapterId: null } }}
    onOpenChapter={onOpenChapter} />);
  // specs/2026-11-10-ux2-patterns-refusals.md PR1.3: at the ends of the book the steps stay and say why.
  for (const button of screen.getAllByRole('button')) {
    expect(button.getAttribute('aria-disabled')).toBe('true');
    fireEvent.click(button);
  }
  expect(screen.getByText('Это последняя глава книги.')).toBeTruthy();
  expect(onOpenChapter).not.toHaveBeenCalled();
});
