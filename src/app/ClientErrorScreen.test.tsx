import { render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { setClientErrorReporter } from './client-errors';
import { ClientErrorScreen } from './ClientErrorScreen';

afterEach(() => {
  setClientErrorReporter(null);
  window.sessionStorage.clear();
});

describe('ClientErrorScreen (R5.2)', () => {
  it('reloads once after a failed chunk and then shows the screen', async () => {
    const report = vi.fn();
    setClientErrorReporter({ report });
    const reload = vi.fn();
    const chunk = new TypeError('Failed to fetch dynamically imported module: /assets/ChatPage-old.js');

    const first = render(<ClientErrorScreen error={chunk} reload={reload} />);
    expect(reload).toHaveBeenCalledTimes(1);
    expect(report).toHaveBeenCalledWith('chunk', chunk);
    first.unmount();

    render(<ClientErrorScreen error={chunk} reload={reload} />);
    expect(reload).toHaveBeenCalledTimes(1);
    expect(await screen.findByRole('heading', { name: 'Что-то пошло не так' })).toBeTruthy();
  });

  it('does not reload by itself after a render error', () => {
    const reload = vi.fn();
    render(<ClientErrorScreen error={new TypeError('x')} reload={reload} />);
    expect(reload).not.toHaveBeenCalled();
  });
});
