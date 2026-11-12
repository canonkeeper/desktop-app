import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { ErrorBlock } from './ErrorBlock';

/** The words an author never reads in an error (specs/2026-11-10-ux2-patterns-refusals.md PR3.3). */
const technical = /ошибка сервера|запрос|статус|server|request|status|500|503/iu;

describe('the one block of an error (PR3.1)', () => {
  it('says what did not open, that the books are safe, and offers «Повторить» with the code for support', () => {
    const retry = vi.fn();
    render(<ErrorBlock cause="Нет связи с сервером. Проверьте интернет." code="req-7f3a91" onRetry={retry} title="Не открылась карта саги" />);
    const block = screen.getByRole('alert', { name: 'Не открылась карта саги' });
    expect(block.textContent).toContain('Нет связи с сервером. Проверьте интернет. Ваши книги и тексты на месте.');
    expect(block.textContent).toContain('Код для поддержки: req-7f3a91');
    expect(screen.getByRole('link', { name: /Написать/u })).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Повторить' }));
    expect(retry).toHaveBeenCalledTimes(1);
    expect(block.textContent?.replace('req-7f3a91', '')).not.toMatch(technical);
  });

  it('offers one way on where the page is not there (PR3.5)', () => {
    const toBooks = vi.fn();
    render(<ErrorBlock action={{ label: 'К книгам', onClick: toBooks }} cause="Она удалена или ссылка устарела." title="Сага не найдена" />);
    expect(screen.queryByRole('button', { name: 'Повторить' })).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'К книгам' }));
    expect(toBooks).toHaveBeenCalledTimes(1);
  });
});
