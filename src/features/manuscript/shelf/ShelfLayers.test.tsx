import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { Book, Project } from '../../../entities/manuscript/api';
import { ConfirmDelete, ConfirmProjectDelete } from './ShelfLayers';

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

    const deleteButton = screen.getByRole('button', { name: 'Удалить книгу' });
    expect(deleteButton.getAttribute('aria-busy')).toBe('true');
    fireEvent.click(deleteButton);
    expect(onDelete).not.toHaveBeenCalled();
  });
});

// specs/2026-11-10-ux1-shelf.md UXS4.3 with the one phrase of specs/2026-11-10-ux2-patterns-actions.md PA3.1: the window
// says the term and the place, and one line of what does not come back — the table of UXS4.4.
describe('ConfirmProjectDelete', () => {
  it('says what comes back with the saga within 7 days and what is gone for good', () => {
    render(<ConfirmProjectDelete isDeleting={false} onClose={vi.fn()} onDelete={vi.fn()} project={{ title: 'Северный архив' } as Project} state="open" />);

    const dialog = screen.getByRole('dialog', { name: 'Удалить сагу «Северный архив»?' });
    expect(dialog.textContent).toContain('Можно вернуть 7 дней — в «Недавно удалённых».');
    // specs/2026-11-11-ux7-trash.md TB2.1, TB2.3: what comes back with the saga and what does not, by the table of the server.
    expect(dialog.textContent).toContain('Вернутся книги, главы «Письма», рисунки, карта, «Что помнит помощник» и ваши материалы.');
    expect(dialog.textContent).toContain('Чаты и разговоры с помощником удалятся сразу и не вернутся. Заметки останутся.');
    expect(screen.getByRole('button', { name: 'Удалить сагу' }).getAttribute('data-variant')).toBe('primary');
    expect(screen.queryByRole('textbox')).toBeNull();
  });

  it('says why the saga was not deleted in the window', () => {
    render(<ConfirmProjectDelete error="Не удалось удалить сагу." isDeleting={false} onClose={vi.fn()} onDelete={vi.fn()} project={{ title: 'Северный архив' } as Project} state="open" />);
    expect(screen.getByRole('alert').textContent).toBe('Не удалось удалить сагу.');
  });
});
