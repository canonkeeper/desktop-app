import { describe, expect, it } from 'vitest';
import { manuscriptQueryKeys } from './index';

describe('manuscriptQueryKeys', () => {
  it('keeps workspace and resource identities exact', () => {
    expect(manuscriptQueryKeys.shelf({ projectId: 'project-1' })).toEqual([
      'canonkeeper', 'manuscript', 'shelf', 'project-1',
    ]);
    expect(manuscriptQueryKeys.readerWorkspace({
      projectId: 'project-1', bookId: 'book-1', chapterId: 'chapter-1',
    })).toEqual([
      'canonkeeper', 'manuscript', 'reader-workspace', 'project-1', 'book-1', 'chapter-1',
    ]);
  });

  it('separates queue, job, source and deleted resources', () => {
    expect(manuscriptQueryKeys.importQueue('project-1', 'queue-1')).toEqual([
      ...manuscriptQueryKeys.importQueues('project-1'), 'queue-1',
    ]);
    expect(manuscriptQueryKeys.job('job-1')).not.toEqual(manuscriptQueryKeys.sourceChapter('job-1'));
    expect(manuscriptQueryKeys.recentlyDeleted()).not.toEqual(manuscriptQueryKeys.shelf({ projectId: undefined }));
  });
});
