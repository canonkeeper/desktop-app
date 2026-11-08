import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ScrollTopButton } from './ScrollTopButton';

afterEach(cleanup);

describe('ScrollTopButton (an author\'s review 03.11)', () => {
  it('appears once its column is scrolled a screen down and takes it back to the top', () => {
    render(<div data-testid="column" style={{ overflowY: 'auto' }}><p>Глава</p><ScrollTopButton /></div>);
    const column = screen.getByTestId('column');
    Object.defineProperty(column, 'clientHeight', { configurable: true, value: 800 });
    const scrollTo = vi.fn();
    column.scrollTo = scrollTo;
    expect(screen.queryByRole('button', { name: 'Наверх' })).toBeNull();
    column.scrollTop = 500;
    fireEvent.scroll(column);
    expect(screen.queryByRole('button', { name: 'Наверх' })).toBeNull();
    column.scrollTop = 900;
    fireEvent.scroll(column);
    fireEvent.click(screen.getByRole('button', { name: 'Наверх' }));
    expect(scrollTo).toHaveBeenCalledWith({ top: 0, behavior: 'smooth' });
  });
});
