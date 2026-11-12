import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { ManuscriptApiClient } from '../../../entities/manuscript/api';
import { UploadCard } from './UploadCard';
import type { VolumeView } from './upload-files';

const waiting: VolumeView = {
  key: 'client-5', itemId: 'item-5', position: 1, fileName: 'Том 5.fb2', state: 'failed',
  progress: null, reasonCode: null, failedJobId: 'job-5',
};

function card(error: { code: string; requestId: string }) {
  const api = { getJob: vi.fn(async () => ({ id: 'job-5', status: 'failed', error })) } as unknown as Pick<ManuscriptApiClient, 'getJob'>;
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      <UploadCard api={api} onFile={vi.fn()} onSkip={vi.fn()} quota={undefined} shape="card" skipping={false} volume={waiting} />
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

  it('says a failure of ours with the code for the support', async () => {
    const volume = card({ code: 'unexpected_deterministic_failure', requestId: 'req-ours' });
    expect(await within(volume).findByText(/сбой на нашей стороне/u)).toBeTruthy();
    expect(within(volume).queryByRole('note')).toBeNull();
  });
});
