import { fireEvent, render, screen, within } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../../api/mock';
import { ChapterNavigation, EditChapterButton, ReaderChapterHeader } from './ReaderChapter';

it('renders outlined Reader controls and preserves neighbor guards', async () => {
  const chapter = await createMockApiClient().getChapter('chapter-12', 'published');
  const onOpenChapter = vi.fn();
  const { rerender } = render(
    <>
      <ReaderChapterHeader
        bookLabel="Книга II"
        chapter={chapter}
        displaySize="normal"
        displayTheme="light"
        focusMode={false}
        onOpenBook={vi.fn()}
        onSetDisplaySize={vi.fn()}
        onSetDisplayTheme={vi.fn()}
        onToggleFocus={vi.fn()}
      />
      <ChapterNavigation chapter={chapter} onOpenChapter={onOpenChapter} />
    </>,
  );
  const previous = screen.getByRole('button', { name: 'Предыдущая глава' });
  const next = screen.getByRole('button', { name: 'Следующая глава' });
  expect(previous.getAttribute('data-variant')).toBe('outline');
  expect(next.getAttribute('data-variant')).toBe('outline');
  expect(screen.getByText('Размер текста').closest('button')?.getAttribute('data-variant')).toBe('outline');
  expect.soft(getComputedStyle(previous).color).toBe('rgb(87, 83, 78)');
  expect.soft(getComputedStyle(next).color).toBe('rgb(41, 37, 36)');
  expect.soft(getComputedStyle(screen.getByText('Размер текста').closest('button')!).color).toBe('var(--ck-foreground)');
  expect.soft(getComputedStyle(screen.getByText(`${chapter.navigation.position} / ${chapter.navigation.total}`)).color).toBe('rgb(120, 113, 108)');

  fireEvent.click(previous);
  fireEvent.click(next);
  expect(onOpenChapter.mock.calls).toEqual([['chapter-11'], ['chapter-13']]);
  onOpenChapter.mockClear();

  rerender(<ChapterNavigation chapter={{
    ...chapter,
    navigation: {
      ...chapter.navigation,
      previous: null,
      previousChapterId: null,
      next: null,
      nextChapterId: null,
    },
  }} onOpenChapter={onOpenChapter} />);
  for (const button of screen.getAllByRole('button')) {
    expect(button.hasAttribute('disabled')).toBe(true);
    fireEvent.click(button);
  }
  expect(onOpenChapter).not.toHaveBeenCalled();
});

it('uses book breadcrumbs without changing their callback or the main chapter heading', async () => {
  const chapter = await createMockApiClient().getChapter('chapter-12', 'published');
  const onOpenBook = vi.fn();
  render(<ReaderChapterHeader
    bookLabel="Книга II. Карта приливов"
    chapter={chapter}
    displaySize="normal"
    displayTheme="light"
    focusMode={false}
    onOpenBook={onOpenBook}
    onSetDisplaySize={vi.fn()}
    onSetDisplayTheme={vi.fn()}
    onToggleFocus={vi.fn()}
  />);

  const breadcrumbNavigation = screen.getByRole('navigation', { name: 'Хлебные крошки' });
  const breadcrumbs = within(breadcrumbNavigation);
  expect.soft(getComputedStyle(breadcrumbNavigation).color).toBe('rgb(120, 113, 108)');
  expect.soft(getComputedStyle(breadcrumbNavigation.querySelector('ol')!).color).toBe('inherit');
  for (const button of breadcrumbs.getAllByRole('button')) {
    expect.soft(getComputedStyle(button).color).toBe('inherit');
  }
  expect.soft(getComputedStyle(breadcrumbs.getByText('Глава 12')).color).toBe('rgb(28, 25, 23)');
  expect.soft(getComputedStyle(screen.getByRole('heading', { level: 1 })).color).toBe('rgb(28, 25, 23)');
  expect.soft(getComputedStyle(screen.getByText(/^[\d\s]+ слов\S* · \d+ мин$/u)).color).toBe('rgb(120, 113, 108)');
  expect(screen.queryByText(/ревизия/iu)).toBeNull();
  expect(screen.getByRole('radio', { name: 'Сепия' }).getAttribute('aria-checked')).toBe('false');
  expect(screen.getByRole('radio', { name: 'Тёмная' }).getAttribute('aria-checked')).toBe('false');
  fireEvent.click(breadcrumbs.getByRole('button', { name: 'Мои книги' }));
  fireEvent.click(breadcrumbs.getByRole('button', { name: 'Книга II. Карта приливов' }));
  expect(onOpenBook).toHaveBeenCalledTimes(2);
  expect(screen.getByRole('heading', { level: 1 }).textContent).toBe('Глава 12. Белый порт');
});

// specs/2026-11-05-voice-full-access.md A8.7: «Править главу» names its chapter, so a screen reader and the voice find it.
it('names «Править главу» by its chapter', async () => {
  const chapter = await createMockApiClient().getChapter('chapter-12', 'published');
  const run = vi.fn();
  render(<EditChapterButton chapter={chapter} edit={{ pending: false, run }} />);
  fireEvent.click(screen.getByRole('button', { name: `Править главу «${chapter.title}»` }));
  expect(run).toHaveBeenCalledTimes(1);
});
