import type { ChapterNavigationLocator, SearchResult } from '../../entities/manuscript/api';
import { manuscriptSourceDestination } from '../../shared/navigation/manuscript-route';

export function manuscriptSearchResultDestination(result: SearchResult) {
  return manuscriptSourceDestination(result.locator);
}

export function chapterSearchDestination(locator: ChapterNavigationLocator) {
  const destination = manuscriptSourceDestination(locator);
  return { path: destination.path, query: { ...destination.query, mode: 'read' } };
}
