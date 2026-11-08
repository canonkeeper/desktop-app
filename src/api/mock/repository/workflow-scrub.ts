import type { Schema } from '../../../shared/api';
import type { MockRepositoryContext } from './context';
import { createCanonicalSourceLocator } from './fixtures';

type CanonicalSourceLocator = Schema<'CanonicalChapterSourceLocatorV1'>;

export type WorkflowSourceScope = {
  projectId: string;
  bookIds?: ReadonlySet<string>;
  chapterIds?: ReadonlySet<string>;
  sourceView?: 'draft' | 'published';
};

function sourceIntersects(locator: CanonicalSourceLocator, scope: WorkflowSourceScope) {
  if (locator.projectId !== scope.projectId) return false;
  if (scope.sourceView && locator.sourceView !== scope.sourceView) return false;
  if (!scope.bookIds && !scope.chapterIds) return true;
  return Boolean(scope.bookIds?.has(locator.bookId) || scope.chapterIds?.has(locator.chapterId));
}

/** Metadata ChatSession сохраняется, а Job bindings удалённого source не переживают restore. */
export function scrubAgentWorkflows(context: MockRepositoryContext, scope: WorkflowSourceScope) {
  const { state } = context;
  const projectWide = !scope.bookIds && !scope.chapterIds;
  const removedJobIds = new Set(
    state.jobs
      .filter((job) => (
        job.subject.projectId === scope.projectId
        && job.subject.type === 'research_chat_turn'
        && (projectWide || sourceIntersects(createCanonicalSourceLocator(job.subject.sourceView), scope))
      ))
      .map((job) => job.id),
  );
  state.jobs = state.jobs.filter((job) => !removedJobIds.has(job.id));
  for (const jobId of removedJobIds) context.jobCreators.delete(jobId);
}
