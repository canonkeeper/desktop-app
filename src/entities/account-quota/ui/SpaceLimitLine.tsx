import type { ReturnPlace } from '../../../shared/navigation/app-route';
import type { AccountQuota, QuotaCounter } from '../api';
import { useQuotaNote } from '../quota-note';
import styles from './quota-refusal.module.css';

type SpaceCode = 'quota.project_books' | 'quota.projects';

/**
 * The counter of a limit of space a quiet line names: «Книг … в этой саге» — the books of the saga on screen, of which the
 * author is the owner (specs/2026-11-11-tier-limits.md TL4.2; the owner, 11.11: the books count in every saga), «Саг».
 */
const spaces: Record<SpaceCode, { word: string; after: string; counter: (quota: AccountQuota, projectId: string | null) => QuotaCounter | undefined }> = {
  'quota.project_books': {
    word: 'Книг', after: ' в этой саге',
    counter: (quota, projectId) => quota.ownedProjects.find((project) => project.projectId === projectId)?.sources.books,
  },
  'quota.projects': { word: 'Саг', after: '', counter: (quota) => quota.projects },
};

/** The place of space is taken by its counter: a book in this saga or a saga more would be refused. */
export function spaceTaken(code: SpaceCode, quota: AccountQuota | undefined, projectId: string | null = null) {
  const counter = quota ? spaces[code].counter(quota, projectId) : undefined;
  return Boolean(counter && counter.limit > 0 && counter.used >= counter.limit);
}

/**
 * One quiet line at an action whose place of space is taken (specs/2026-11-10-ux2-patterns-refusals.md PR2.7): «Книг 12 из
 * 12 в этой саге на тарифе „Писатель“ · больше — на „Профи“». A saga full of books is the ordinary state of a new author,
 * not a refusal: the one block of refusals comes after the press, when the server refused. While there is room, nothing.
 */
export function SpaceLimitLine({ as: Tag = 'p', className, code, place, projectId = null, quota }: {
  /** `span` inside a control: the line of «Новая сага» of the menu of the saga. */
  as?: 'p' | 'span';
  className?: string | undefined;
  code: SpaceCode;
  place?: ReturnPlace | undefined;
  /** The saga on screen, for the books of a saga. */
  projectId?: string | null;
  quota: AccountQuota | undefined;
}) {
  const note = useQuotaNote()(code, place);
  const counter = quota ? spaces[code].counter(quota, projectId) : undefined;
  if (!counter || !spaceTaken(code, quota, projectId)) return null;
  const { after, word } = spaces[code];
  return <Tag className={`${styles.line}${className ? ` ${className}` : ''}`} data-space-limit="">{word} {counter.used} из {counter.limit}{after}{note?.line}</Tag>;
}
