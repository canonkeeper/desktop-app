import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { ReaderMobileMenu } from './reader/ReaderLayers';
import { MobileMenu as ShelfMobileMenu } from './shelf/ShelfLayers';
import type { ProjectSidebarProps } from '../../ui/ProjectSidebar';

const sidebar: ProjectSidebarProps = {
  activeSection: 'manuscript',
  onOpenChat: vi.fn(),
  onOpenManuscript: vi.fn(),
  projectMeta: '2 книги',
  projectTitle: 'Белый порт',
};

describe('mobile project menus', () => {
  it.each([
    ['shelf', ShelfMobileMenu],
    ['reader', ReaderMobileMenu],
  ])('closes the %s drawer before invoking the shared shortcuts', (_name, MobileProjectMenu) => {
    const onClose = vi.fn();
    const onOpenReader = vi.fn();
    const onOpenNotes = vi.fn();
    const onCreateNote = vi.fn();
    render(<MobileProjectMenu onClose={onClose} state="open"
      sidebar={{ ...sidebar, onOpenReader, onOpenNotes, onCreateNote }} />);
    for (const name of ['Чтение', 'Заметки', 'Создать заметку']) fireEvent.click(screen.getByRole('button', { name }));
    expect(onClose).toHaveBeenCalledTimes(3);
    expect(onOpenReader).toHaveBeenCalledOnce();
    expect(onOpenNotes).toHaveBeenCalledOnce();
    expect(onCreateNote).toHaveBeenCalledOnce();
    expect(onClose.mock.invocationCallOrder[0]).toBeLessThan(onOpenReader.mock.invocationCallOrder[0]!);
  });

  it.each([
    ['shelf', ShelfMobileMenu],
    ['reader', ReaderMobileMenu],
  ])('keeps the current project button actionable in the %s drawer', (_name, MobileProjectMenu) => {
    const onOpenProjectMenu = vi.fn();
    render(
      <MobileProjectMenu
        onClose={vi.fn()}
        state="open"
        sidebar={{ ...sidebar, onOpenProjectMenu }}
      />,
    );

    const projectButton = screen.getByRole('button', { name: 'Белый порт' });
    expect(projectButton.hasAttribute('disabled')).toBe(false);
    fireEvent.click(projectButton);

    expect(onOpenProjectMenu).toHaveBeenCalledOnce();
  });
});
