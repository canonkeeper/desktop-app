import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { DownloadDialog, type DownloadDrawings } from './BookDownload';

function renderDialog(drawings: DownloadDrawings | null) {
  const onDownload = vi.fn();
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<QueryClientProvider client={client}>
    <DownloadDialog busy={false} drawings={drawings} heading="Скачать книгу «Порт»" onClose={vi.fn()} onDownload={onDownload} />
  </QueryClientProvider>);
  return { onDownload, window: screen.getByRole('dialog', { name: 'Скачать книгу «Порт»' }) };
}

afterEach(() => cleanup());

// specs/2026-11-12-ux10-book-pictures.md BP3.1, BP2.2: the window of a build without drawings says nothing of them and
// asks the file without them; when the number cannot be read, the choice stands without it and the drawings go first.
describe('the window «Скачать книгу…»', () => {
  it('says nothing of drawings in a build without them', () => {
    const { onDownload, window } = renderDialog(null);
    expect(within(window).queryByText(/рисун/iu)).toBeNull();
    fireEvent.click(within(window).getByRole('button', { name: 'Скачать' }));
    expect(onDownload).toHaveBeenCalledWith('docx', false);
  });

  it('offers the drawings without a number it could not read', async () => {
    const api = { getBookDrawingCounts: vi.fn(() => Promise.reject(new Error('offline'))) };
    const { onDownload, window } = renderDialog({ api, projectId: 'project', bookIds: ['book'] });
    expect(await within(window).findByRole('radio', { name: 'С рисунками' })).toHaveProperty('checked', true);
    fireEvent.click(within(window).getByRole('radio', { name: /^FB2/u }));
    fireEvent.click(within(window).getByRole('button', { name: 'Скачать' }));
    expect(onDownload).toHaveBeenCalledWith('fb2', true);
  });
});
