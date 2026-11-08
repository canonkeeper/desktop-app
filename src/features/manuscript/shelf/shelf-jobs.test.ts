import { describe, expect, it } from 'vitest';
import type { IndexingStatus, ProjectJob } from '../../../entities/manuscript/api';
import { activeJobBookId, unfinishedIndexingBookId } from './shelf-jobs';

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
  it('takes unfinished book identity only from the selected envelope', () => {
    const status: IndexingStatus = {
      draft: {
        exists: true,
        context: {
          state: 'partial',
          currentBook: null,
          retryableFailedBook: { bookId: 'book-draft-failed' },
        },
      },
      published: {
        exists: true,
        context: {
          state: 'processing',
          currentBook: { bookId: 'book-published-current', position: 1, total: 2 },
          retryableFailedBook: { bookId: 'book-published-failed' },
        },
      },
      steps: [{
        bookId: 'book-stale-step',
        canRetry: true,
        kind: 'cards',
        progress: null,
        sourceView: 'draft',
        status: 'failed',
      }],
    };

    expect(unfinishedIndexingBookId(status, 'published')).toBe('book-published-current');
    expect(unfinishedIndexingBookId(status, 'draft')).toBe('book-draft-failed');
  });

  it('does not use project-wide steps as a per-view book identity', () => {
    const status: IndexingStatus = {
      published: {
        exists: true,
        context: { state: 'partial', currentBook: null, retryableFailedBook: null },
      },
      steps: [{
        bookId: 'book-stale-draft',
        canRetry: true,
        kind: 'cards',
        progress: null,
        sourceView: 'draft',
        status: 'failed',
      }],
    };

    expect(unfinishedIndexingBookId(status, 'published')).toBeNull();
  });

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
