import type { ProjectJob } from '../../../entities/manuscript/api';

const activeJobStatuses: readonly ProjectJob['status'][] = ['queued', 'running', 'canceling'];

export function activeJobBookId(job: ProjectJob) {
  switch (job.subject.type) {
    case 'import':
      return job.subject.bookId ?? (job.result?.type === 'import' ? job.result.bookId : null);
    default:
      return null;
  }
}

function isActiveJobStatus(status: ProjectJob['status']) {
  return activeJobStatuses.includes(status);
}

/** A Job the server still works on or stops. */
export function isActiveJob(job: ProjectJob) {
  return isActiveJobStatus(job.status);
}

// Only a running import blocks a new one: text indexes and cards of a version follow imports
// in the background.
export function isActiveImportJob(job: ProjectJob) {
  return job.kind === 'import' && isActiveJobStatus(job.status);
}

/**
 * A new book on its way to the shelf by a single import outside «Загрузка книг» — as the book of the example enters its
 * saga — in work while its book is not on the shelf yet (specs/2026-11-11-import-speed.md IS2.3). A volume of the window
 * is told by the window; an update of a book, by the word of its card.
 */
export function isNewBookOnItsWay(job: ProjectJob) {
  return isActiveImportJob(job) && job.subject.type === 'import'
    && !job.subject.bookId && !job.subject.preview && !job.subject.importQueueId;
}
