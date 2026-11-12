import { fireEvent, render, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { LongWork, longWorkLine } from './LongWork';

// specs/2026-11-10-ux2-patterns-states.md PS3.3: what is happening · the step · the term of the server · may leave · one action.
describe('LongWork', () => {
  it('says what is happening, the step, the term and what comes if the author leaves, with one action and its bar', () => {
    const onStop = vi.fn();
    render(<LongWork action={<button onClick={onStop} type="button">Остановить</button>} announcement="Помощник пишет главу"
      detail={longWorkLine({ leave: 'написанное останется в главе', step: { done: 2, of: 4, word: 'Сцена' }, term: 'обычно 3–5 минут' })}
      label="Письмо главы" progress={0.5} title="Помощник пишет главу «Встреча у ворот»" />);
    const work = screen.getByRole('region', { name: 'Письмо главы' });
    expect(within(work).getByText('Помощник пишет главу «Встреча у ворот»')).toBeTruthy();
    expect(within(work).getByText('Сцена 2 из 4 · обычно 3–5 минут · можно уйти — написанное останется в главе')).toBeTruthy();
    expect(within(work).getAllByRole('button')).toHaveLength(1);
    fireEvent.click(within(work).getByRole('button', { name: 'Остановить' }));
    expect(onStop).toHaveBeenCalledOnce();
    expect((within(work).getByRole('progressbar') as HTMLProgressElement).value).toBe(0.5);
  });

  it('makes up no term, no step and no bar the server did not give', () => {
    expect(longWorkLine({})).toBe('');
    expect(longWorkLine({ leave: 'пришлём письмо, когда сага будет готова' })).toBe('можно уйти — пришлём письмо, когда сага будет готова');
    expect(longWorkLine({ step: { done: 3, of: 12 } })).toBe('3 из 12');
    render(<LongWork label="Карта обновляется" title="Карта обновляется" />);
    const work = screen.getByRole('region', { name: 'Карта обновляется' });
    expect(within(work).queryByRole('button')).toBeNull();
    expect(within(work).queryByRole('progressbar')).toBeNull();
  });
});
