import { QueryClient } from '@tanstack/react-query';
import { createMemoryHistory } from '@tanstack/react-router';
import { fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../api/mock';
import { setClientErrorReporter } from '../app/client-errors';
import { CanonKeeperRouterProvider, createCanonKeeperRouter } from './router';

vi.mock('../App', () => ({
  App: () => {
    throw new TypeError('render of «Глава 7» broke');
  },
}));

beforeAll(() => {
  vi.stubGlobal('scrollTo', vi.fn());
});

afterEach(() => {
  setClientErrorReporter(null);
  window.sessionStorage.clear();
  vi.restoreAllMocks();
});

describe('router error screen (R5.1)', () => {
  it('shows a Russian error screen with a reload button and reports the render error', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => undefined);
    vi.spyOn(console, 'warn').mockImplementation(() => undefined);
    const report = vi.fn();
    setClientErrorReporter({ report });
    const reload = vi.fn();
    const router = createCanonKeeperRouter(
      { api: createMockApiClient(), queryClient: new QueryClient() },
      createMemoryHistory({ initialEntries: ['/chat'] }),
      { reload },
    );
    render(<CanonKeeperRouterProvider router={router} />);

    expect(await screen.findByRole('heading', { name: 'Что-то пошло не так' })).toBeTruthy();
    expect(screen.queryByText(/Something went wrong/u)).toBeNull();
    expect(screen.queryByText(/Глава 7/u)).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Обновить' }));
    expect(reload).toHaveBeenCalledTimes(1);
    expect(report).toHaveBeenCalledWith('render', expect.any(TypeError));
  });
});
