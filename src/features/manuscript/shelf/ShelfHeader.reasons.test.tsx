import { fireEvent, render, screen } from '@testing-library/react';
import { createRef } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { reasonOf, shownReason } from '../../../test/disabled-reason';
import { FirstProjectPanel, ShelfHeader } from './ShelfHeader';

/** The shelf of a saga whose upload is closed now (specs/2026-11-10-ux2-patterns-refusals.md PR1.3). */
function header(importReason: string | undefined) {
  const choose = vi.fn();
  render(
    <ShelfHeader canEditManuscript fileInputRef={createRef()} importBlocked importReason={importReason} onChooseImportFile={choose}
      onImportInputChange={vi.fn()} projectExists projectMeta="2 книги" />,
  );
  return { choose, upload: screen.getByRole('button', { name: 'Добавить книги' }) };
}

describe('the closed buttons of the shelf say why', () => {
  // specs/2026-11-11-ux7-upload.md UP3.5: while books are uploaded «Добавить книги» says how far it went.
  it('keeps «Добавить книги» in place while books are uploaded and says why by a press', () => {
    const reason = 'Дождитесь конца загрузки: идёт 3 из 12.';
    const { choose, upload } = header(reason);
    expect(upload.getAttribute('aria-disabled')).toBe('true');
    expect(reasonOf(upload)).toBe(reason);
    fireEvent.click(upload);
    expect(shownReason(upload)).toBe(reason);
    expect(choose).not.toHaveBeenCalled();
  });

  it('keeps «Создать сагу» in place without a name and says why', async () => {
    const create = vi.fn(async () => true);
    render(<FirstProjectPanel creating={false} onCreate={create} />);
    const button = screen.getByRole('button', { name: 'Создать сагу' });
    expect(reasonOf(button)).toBe('Сначала напишите название саги.');
    fireEvent.click(button);
    expect(shownReason(button)).toBe('Сначала напишите название саги.');
    expect(create).not.toHaveBeenCalled();
    expect(document.activeElement).toBe(screen.getByRole('textbox', { name: 'Название саги' }));
    fireEvent.change(screen.getByRole('textbox', { name: 'Название саги' }), { target: { value: 'Северный ветер' } });
    fireEvent.click(button);
    expect(create).toHaveBeenCalledWith('Северный ветер');
  });
});
