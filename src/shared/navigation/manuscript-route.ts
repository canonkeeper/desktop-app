import type { Schema } from '../api';

type ManuscriptSourceLocator =
  | Schema<'CanonicalChapterSourceLocatorV1'>
  | Schema<'ChapterSearchLocator'>
  | Schema<'ReaderLocator'>
  | Schema<'SourceNavigationAnchor'>;

export function manuscriptSourceDestination(source: ManuscriptSourceLocator) {
  const anchor = 'sourceAnchor' in source ? source.sourceAnchor : 'kind' in source ? source : null;
  const locator = anchor ? anchor.locator : source as Exclude<ManuscriptSourceLocator, Schema<'SourceNavigationAnchor'>>;
  const readerLocator = 'targetView' in locator ? locator : null;
  const revision = 'targetView' in locator ? locator.revision : locator.sourceRevision;
  return {
    path: '/manuscript/books',
    query: {
      projectId: locator.projectId,
      bookId: locator.bookId,
      mode: 'read',
      chapterId: locator.chapterId,
      expectedRevision: String(revision),
      paragraphId: anchor?.kind === 'chapter' ? undefined : readerLocator?.paragraphId ?? undefined,
    },
  };
}
