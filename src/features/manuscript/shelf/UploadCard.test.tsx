import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { AccountQuota } from '../../../entities/account-quota/api';
import type { ManuscriptApiClient } from '../../../entities/manuscript/api';
import { UploadCard } from './UploadCard';
import type { VolumeView } from './upload-files';

const waiting: VolumeView = {
  key: 'client-5', itemId: 'item-5', position: 1, fileName: 'Том 5.fb2', state: 'failed',
  progress: null, reasonCode: null, failedJobId: 'job-5',
};

function card(error: { code: string; requestId: string }, quota?: AccountQuota) {
  const api = { getJob: vi.fn(async () => ({ id: 'job-5', status: 'failed', error })) } as unknown as Pick<ManuscriptApiClient, 'getJob'>;
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      <UploadCard api={api} onFile={vi.fn()} onSkip={vi.fn()} quota={quota} shape="card" skipping={false} volume={waiting} />
    </QueryClientProvider>,
  );
  return screen.getByRole('article', { name: 'Том 5.fb2' });
}

// specs/2026-11-11-ux7-upload.md UP3.2, UP3.4: a volume the server refused after it read the file waits at its place with
// the one block of refusals and «Заменить файл» · «Пропустить»; a failure of ours says so with the code for the support.
describe('a volume that waits on its card', () => {
  it('says a limit the server met after the start as the block of refusals', async () => {
    const volume = card({ code: 'quota.project_books', requestId: 'req-books' });
    expect(await within(volume).findByRole('note')).toBeTruthy();
    expect(within(volume).getByText('Не загрузилась')).toBeTruthy();
    expect(within(volume).getByLabelText('Заменить файл «Том 5.fb2»')).toBeTruthy();
    expect(within(volume).getByRole('button', { name: 'Пропустить' })).toBeTruthy();
    expect(volume.textContent).not.toMatch(/сбой на нашей стороне/u);
  });

  // specs/2026-11-18-free-tier.md FT1.3, FT3.2 (board 35·31): another file meets the same refusal — the reason in one line of
  // the words of the one table, and «Пропустить» the only way on.
  it('offers only «Пропустить» to a volume refused because a book is loaded once', async () => {
    const quota = { account: { books: { used: 1, limit: 1 } }, bookReloads: false, booksLoaded: 1 } as unknown as AccountQuota;
    const volume = card({ code: 'quota.book_reloads', requestId: 'req-once' }, quota);
    expect(await within(volume).findByText('На вашем тарифе одна книга, и загружается она один раз. Ваша книга на месте: читать, спрашивать и править главы можно.')).toBeTruthy();
    expect(within(volume).getByRole('button', { name: 'Пропустить' })).toBeTruthy();
    expect(within(volume).queryByLabelText('Заменить файл «Том 5.fb2»')).toBeNull();
    expect(within(volume).queryByRole('note')).toBeNull();
    expect(volume.textContent).not.toMatch(/сбой на нашей стороне/u);
  });

  it('says a failure of ours with the code for the support', async () => {
    const volume = card({ code: 'unexpected_deterministic_failure', requestId: 'req-ours' });
    expect(await within(volume).findByText(/сбой на нашей стороне/u)).toBeTruthy();
    expect(within(volume).queryByRole('note')).toBeNull();
  });
});
