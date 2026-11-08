import { describe, expect, it } from 'vitest';
import { createMockApiClient } from '../../../api/mock';
import { bookId, projectId } from '../../../api/mock/repository/fixtures';
import { isBookUpdateJob, isBookUpdatePreview } from './queries';

// specs/2026-10-27-update-book-from-file.md R6.7: the preview of an update writes nothing, so the shelf does not
// follow it as work on the book; R6.3: its places are for its creator only.
describe('the preview of a book update', () => {
  it('is not a book update the shelf follows', async () => {
    const api = createMockApiClient();
    const file = new File(['<FictionBook>том</FictionBook>'], 'том.fb2', { type: 'application/x-fictionbook+xml' });
    const preview = await api.importBookFile(projectId, { file, options: { importMode: 'update_preview', bookId } }, { idempotencyKey: 'idem-update-preview' });
    const update = await api.importBookFile(projectId, { file, options: { importMode: 'update_book', bookId } }, { idempotencyKey: 'idem-update-book' });

    expect(preview.job.subject).toMatchObject({ type: 'import', bookId, preview: true });
    expect(isBookUpdatePreview(preview.job)).toBe(true);
    expect(isBookUpdateJob(preview.job)).toBe(false);
    expect(isBookUpdatePreview(update.job)).toBe(false);
    expect(isBookUpdateJob(update.job)).toBe(true);

    const finished = await api.getJob(preview.job.id);
    expect(finished.result?.type === 'import' && finished.result.preview?.chapters[0]?.places?.length).toBe(1);
  });
});
