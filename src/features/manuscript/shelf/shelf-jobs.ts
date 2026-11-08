import type { IndexingStatus, ProjectJob, SourceView } from '../../../entities/manuscript/api';

const activeJobStatuses: readonly ProjectJob['status'][] = ['queued', 'running', 'canceling'];

export function activeJobBookId(job: ProjectJob) {
  switch (job.subject.type) {
    case 'import':
      return job.subject.bookId ?? (job.result?.type === 'import' ? job.result.bookId : null);
    default:
      return null;
  }
}

export function unfinishedIndexingBookId(status: IndexingStatus | undefined, sourceView: SourceView) {
  if (!status) return null;
  const envelope = sourceView === 'draft' ? status.draft : status.published;
  const context = envelope?.exists ? envelope.context : null;
  if (context?.state === 'ready') return null;

  return context?.currentBook?.bookId ?? context?.retryableFailedBook?.bookId ?? null;
}

function isActiveJobStatus(status: ProjectJob['status']) {
  return activeJobStatuses.includes(status);
}

// Only a running import blocks a new one: text indexes and cards of a version follow imports
// in the background.
export function isActiveImportJob(job: ProjectJob) {
  return job.kind === 'import' && isActiveJobStatus(job.status);
}
