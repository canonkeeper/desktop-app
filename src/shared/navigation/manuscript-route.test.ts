import { describe, expect, it } from 'vitest';
import type { Schema } from '../api';
import { manuscriptSourceDestination } from './manuscript-route';

function locator(sourceView: 'draft' | 'published'): Schema<'CanonicalChapterSourceLocatorV1'> {
  return {
    projectId: 'project-1',
    bookId: 'book-1',
    chapterId: 'chapter-1',
    sourceRef: 'chapter-1',
    sourceView,
    sourceRevision: 3,
    sourceGeneration: 'generation-1',
    contentHash: 'sha256:source',
  };
}

describe('manuscriptSourceDestination', () => {
  it.each(['published', 'draft'] as const)('opens the exact server-owned %s paragraph anchor', (sourceView) => {
    const source = locator(sourceView);
    const sourceAnchor: Schema<'SourceNavigationAnchor'> = {
      kind: 'paragraph',
      label: 'Глава 12 · абзац 18',
      locator: {
        projectId: source.projectId, bookId: source.bookId, chapterId: source.chapterId,
        targetView: sourceView, revision: source.sourceRevision, paragraphId: 'paragraph-18',
      },
    };
    const chapterSearchLocator: Schema<'ChapterSearchLocator'> = { ...source, sourceAnchor };
    for (const value of [chapterSearchLocator, sourceAnchor]) {
      expect(manuscriptSourceDestination(value).query).toEqual({
        projectId: source.projectId, bookId: source.bookId, chapterId: source.chapterId,
        mode: 'read', paragraphId: 'paragraph-18',
        expectedRevision: '3',
      });
    }
    const chapterAnchor: Schema<'SourceNavigationAnchor'> = {
      kind: 'chapter', label: 'Глава 12',
      locator: { projectId: source.projectId, bookId: source.bookId, chapterId: source.chapterId, targetView: sourceView, revision: source.sourceRevision },
    };
    expect(manuscriptSourceDestination(chapterAnchor).query.paragraphId).toBeUndefined();
  });

  it.each(['published', 'draft'] as const)('opens the %s citation in the Reader without a text version', (sourceView) => {
    expect(manuscriptSourceDestination(locator(sourceView))).toEqual({
      path: '/manuscript/books',
      query: {
        projectId: 'project-1',
        bookId: 'book-1',
        mode: 'read',
        chapterId: 'chapter-1',
        paragraphId: undefined,
        expectedRevision: '3',
      },
    });
  });

  it('routes a Reader locator to its exact draft paragraph', () => {
    const readerLocator: Schema<'ReaderLocator'> = {
      projectId: 'project-1',
      bookId: 'book-1',
      chapterId: 'chapter-1',
      targetView: 'draft',
      revision: 7,
      paragraphId: 'paragraph-2',
    };

    expect(manuscriptSourceDestination(readerLocator)).toEqual({
      path: '/manuscript/books',
      query: {
        projectId: 'project-1',
        bookId: 'book-1',
        mode: 'read',
        chapterId: 'chapter-1',
        paragraphId: 'paragraph-2',
        expectedRevision: '7',
      },
    });
  });
});
