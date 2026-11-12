import { fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { AccountSessionContext } from '../shared/session/account-session';
import { clientErrorCode, setClientErrorReporter } from './client-errors';
import { ClientErrorScreen } from './ClientErrorScreen';
import { PageErrorBoundary } from './PageErrorBoundary';

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

// specs/2026-11-11-ux9-account.md AC3.2: when «Обновить» did not help — «К книгам», «Написать нам» and the code under them.
describe('ClientErrorScreen ways on (AC3.2)', () => {
  const environment = { origin: 'https://canonkeeper.test', pathname: '/chat', entryScript: 'https://canonkeeper.test/assets/index-Bq3x.js', userAgent: 'x' };

  it('names the class and the place in the build as the code, no text of the error', () => {
    const error = new TypeError('Cannot read properties of undefined (reading «Мара»)');
    error.stack = 'TypeError: Cannot read…\n    at render (https://canonkeeper.test/assets/ChatPage-D4f2.js:1:2345)';
    expect(clientErrorCode(error, environment)).toBe('TypeError:ChatPage-D4f2.js:1:2345');
    expect(clientErrorCode('broken', environment)).toBe('NonError:index-Bq3x.js');
  });

  it('offers «К книгам» and «Написать нам» with the code for support', () => {
    render(<ClientErrorScreen error={new TypeError('x')} reload={vi.fn()} />);
    expect(screen.getByRole('link', { name: 'К книгам' }).getAttribute('href')).toBe('/manuscript/books');
    expect(screen.getByText('Код для поддержки:').textContent).toMatch(/^Код для поддержки: TypeError:/u);
    expect(screen.getByRole('link', { name: 'Написать нам' }).getAttribute('href')).toMatch(/^mailto:/u);
  });

  it('keeps an error of a page in the shell: «Написать нам» opens the window with the code', () => {
    const onOpenContact = vi.fn();
    const Broken = () => {
      throw new TypeError('x');
    };
    vi.spyOn(console, 'error').mockImplementation(() => undefined);
    render(
      <AccountSessionContext.Provider value={{ busy: false, onOpenContact }}>
        <PageErrorBoundary><Broken /></PageErrorBoundary>
      </AccountSessionContext.Provider>,
    );
    expect(screen.getByRole('heading', { level: 2, name: 'Что-то пошло не так' })).toBeTruthy();
    expect(document.querySelector('main')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Написать нам' }));
    expect(onOpenContact).toHaveBeenCalledWith({ topic: 'bug', code: expect.stringMatching(/^TypeError:/u) });
    vi.restoreAllMocks();
  });
});
