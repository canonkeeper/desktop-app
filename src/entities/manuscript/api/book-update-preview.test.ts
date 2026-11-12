import { describe, expect, it } from 'vitest';
import { createMockApiClient } from '../../../api/mock';
import { bookId, projectId } from '../../../api/mock/repository/fixtures';
import { isBookUpdateJob, isBookUpdatePreview, isSingleImportJob } from './queries';

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
    expect(isSingleImportJob(preview.job)).toBe(false);
    expect(isSingleImportJob(update.job)).toBe(true);

    const finished = await api.getJob(preview.job.id);
    expect(finished.result?.type === 'import' && finished.result.preview?.chapters[0]?.places?.length).toBe(1);
  });
});

// specs/2026-11-11-import-speed.md IS2.2: the shelf follows every single import that writes — a book update and a new book
// outside the import queue, as the book of the example — and leaves a volume of the queue to the queue.
describe('the single imports the shelf follows', () => {
  it('are the imports outside the import queue that write', () => {
    const job = (subject: Record<string, unknown>) => ({ subject: { type: 'import', id: 'job-1', projectId, ...subject } }) as never;
    expect(isSingleImportJob(job({}))).toBe(true);
    expect(isSingleImportJob(job({ bookId }))).toBe(true);
    expect(isSingleImportJob(job({ importQueueId: 'queue-1', importQueueItemId: 'item-1' }))).toBe(false);
    expect(isSingleImportJob(job({ bookId, preview: true }))).toBe(false);
  });
});
