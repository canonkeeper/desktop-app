import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { Book } from '../../../entities/manuscript/api';
import { ConfirmDelete, MobileMenu } from './ShelfLayers';

describe('ConfirmDelete', () => {
  it('предупреждает об очистке материалов агентов вместе с книгой', () => {
    render(
      <ConfirmDelete
        book={{ title: 'Белый порт' } as Book}
        isDeleting={false}
        onClose={vi.fn()}
        onDelete={vi.fn()}
        state="open"
      />,
    );

    expect(screen.getByText(/Чаты и заметки останутся/)).toBeTruthy();
  });

  it('blocks duplicate deletion while the request is pending', () => {
    const onDelete = vi.fn();
    render(
      <ConfirmDelete
        book={{ title: 'Белый порт' } as Book}
        isDeleting
        onClose={vi.fn()}
        onDelete={onDelete}
        state="open"
      />,
    );

    const deleteButton = screen.getByRole('button', { name: 'Удаляем...' });
    expect(deleteButton.getAttribute('aria-busy')).toBe('true');
    fireEvent.click(deleteButton);
    expect(onDelete).not.toHaveBeenCalled();
  });
});

describe('MobileMenu', () => {
  it('keeps the manuscript navigation in an accessible drawer', () => {
    const onClose = vi.fn();
    render(
      <MobileMenu
        onClose={onClose}
        sidebar={{
          activeSection: 'manuscript',
          onOpenChat: vi.fn(),
          onOpenManuscript: vi.fn(),
          projectMeta: '4 книги',
          projectTitle: 'Северный архив',
        }}
        state="open"
      />,
    );

    expect(screen.getByRole('dialog', { name: 'Меню' }).getAttribute('data-kind')).toBe('drawer');
    fireEvent.click(screen.getByRole('button', { name: 'Закрыть меню' }));
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
