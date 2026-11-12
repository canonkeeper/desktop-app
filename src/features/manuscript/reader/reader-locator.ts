import type { ReaderChapter, ReaderLocator, SourceView } from '../../../entities/manuscript/api';
import type { AppRoute } from '../../../shared/navigation/app-route';

export type ReaderSourceProblem = 'invalid' | 'unavailable' | 'version' | 'paragraph';

export function readerSourceLanding(route: AppRoute, chapter: ReaderChapter | null, sourceView: SourceView) {
  const expectedRevision = route.query.get('expectedRevision');
  const paragraphId = route.query.get('paragraphId');
  const exact = expectedRevision !== null || Boolean(paragraphId);
  let problem: ReaderSourceProblem | null = null;
  if (route.invalidQuery) problem = 'invalid';
  else if (exact && (!chapter
    || (route.query.get('chapterId') && route.query.get('chapterId') !== chapter.id)
    || (route.query.get('bookId') && route.query.get('bookId') !== chapter.bookId))) problem = 'unavailable';
  else if (chapter && (chapter.contentVariant !== sourceView
    || (expectedRevision !== null && Number(expectedRevision) !== chapter.revision))) problem = 'version';
  else if (chapter && paragraphId && !chapter.paragraphs.some((paragraph) => paragraph.id === paragraphId)) problem = 'paragraph';

  // Only a link lights a paragraph: an open chapter without one starts from its top, nothing lit (RD1.2).
  const paragraph = problem || !paragraphId ? null : chapter?.paragraphs.find((item) => item.id === paragraphId) ?? null;
  return { problem, paragraph };
}

export function buildReaderLocator(
  projectId: string,
  bookId: string,
  chapterId: string,
  paragraphId: string,
  quote: string,
  revision: number,
  targetView: SourceView = 'published',
): ReaderLocator {
  return {
    projectId,
    bookId,
    chapterId,
    paragraphId: paragraphId || undefined,
    targetView,
    revision,
    range: paragraphId ? { startOffset: 0, endOffset: quote.length, quote } : undefined,
  };
}
