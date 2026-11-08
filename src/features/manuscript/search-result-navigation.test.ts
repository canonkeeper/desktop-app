import { describe, expect, it } from 'vitest';
import { createMockApiClient } from '../../api/mock';
import { chapterSearchDestination, manuscriptSearchResultDestination } from './search-result-navigation';

describe('manuscriptSearchResultDestination', () => {
  it.each(['draft', 'published'] as const)('opens a chapter metadata result in read-only %s Reader', (targetView) => {
    const destination = chapterSearchDestination({ projectId: 'other-saga', bookId: 'volume-9', chapterId: 'chapter-458', targetView, revision: 7 });
    expect(destination.query).toMatchObject({ projectId: 'other-saga', bookId: 'volume-9', chapterId: 'chapter-458', mode: 'read', expectedRevision: '7' });
    expect(destination.query).not.toHaveProperty('sourceView');
    expect(destination.query.paragraphId).toBeUndefined();
  });
  it('opens draft and published locators in the reader without a text version', async () => {
    const api = createMockApiClient();
    api.setScenario({ actorRole: 'editor', preset: 'normal' });
    const published = await api.searchProject('project-white-port', 'порт', 'published');
    const draft = await api.searchProject('project-white-port', 'порт', 'draft');
    const publishedChapter = published.data.find((result) => result.kind === 'chapter');
    const draftChapter = draft.data.find((result) => result.kind === 'chapter');

    const publishedDestination = publishedChapter && manuscriptSearchResultDestination(publishedChapter);
    const draftDestination = draftChapter && manuscriptSearchResultDestination(draftChapter);
    expect(publishedDestination?.query).toMatchObject({ mode: 'read', paragraphId: publishedChapter?.locator.sourceAnchor.locator.paragraphId, expectedRevision: '7' });
    expect(draftDestination?.query).not.toHaveProperty('sourceView');
    expect(draftDestination?.query).toMatchObject({ mode: 'read', paragraphId: draftChapter?.locator.sourceAnchor.locator.paragraphId, expectedRevision: '8' });
    expect(publishedDestination?.query).not.toHaveProperty('annotationId');
    expect(draftDestination?.query).not.toHaveProperty('pane');
  });
});
