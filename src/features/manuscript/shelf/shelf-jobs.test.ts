import { describe, expect, it } from 'vitest';
import type { ProjectJob } from '../../../entities/manuscript/api';
import { activeJobBookId, isNewBookOnItsWay } from './shelf-jobs';

function makeJob(overrides: Partial<ProjectJob> = {}): ProjectJob {
  return {
    id: 'import-job',
    kind: 'import',
    status: 'running',
    progress: 0.25,
    phase: null,
    subject: { type: 'import', id: 'import-job', projectId: 'project-1' },
    result: null,
    error: null,
    canCancel: true,
    expiresAt: null,
    createdAt: '2026-08-09T00:00:00.000Z',
    updatedAt: '2026-08-09T00:00:00.000Z',
    links: { self: '/api/v1/jobs/import-job', cancel: '/api/v1/jobs/import-job/cancel', timeline: null },
    ...overrides,
  };
}

describe('shelf Jobs reconciliation', () => {
  it('links an active import to its book card and leaves system cards Jobs unbound', () => {
    const importJob = makeJob({
      result: {
        type: 'import', bookId: 'book-1', chapterCount: 2, paragraphCount: 3,
        importMode: 'new_book', sourceFileName: 'book.epub',
      },
    });
    const cardsJob = makeJob({
      kind: 'chapter_cards',
      subject: { type: 'chapter_cards', projectId: 'project-1', sourceView: 'draft' },
    });

    expect(activeJobBookId(importJob)).toBe('book-1');
    expect(activeJobBookId(cardsJob)).toBeNull();
  });

});

// specs/2026-11-08-deletion-stops-processing.md R3.1, R3.2: one row per kind of work, the stopping Jobs apart.

// specs/2026-11-11-import-speed.md IS2.3: a new book on its way to the shelf by a single import, while it is in work.
describe('a new book on its way to the shelf', () => {
  it('is a single import of a new book in work', () => {
    expect(isNewBookOnItsWay(makeJob())).toBe(true);
    expect(isNewBookOnItsWay(makeJob({ status: 'queued' }))).toBe(true);
    expect(isNewBookOnItsWay(makeJob({ status: 'succeeded' }))).toBe(false);
    expect(isNewBookOnItsWay(makeJob({ subject: { type: 'import', id: 'import-job', projectId: 'project-1', bookId: 'book-1' } }))).toBe(false);
    expect(isNewBookOnItsWay(makeJob({
      subject: { type: 'import', id: 'import-job', projectId: 'project-1', importQueueId: 'queue-1', importQueueItemId: 'item-1' },
    }))).toBe(false);
  });
});
