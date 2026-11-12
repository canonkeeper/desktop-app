import { Badge } from '../feedback/Badge';
import styles from './Shell.module.css';

/** The role of the author in a saga that is not his own (specs/2026-11-10-ux2-patterns-refusals.md PR3.4). */
export type LimitedRole = 'viewer' | 'editor';

const lines: Record<LimitedRole, { badge: string; name: string; text: string }> = {
  viewer: { badge: 'читатель', name: 'Вы читаете эту сагу', text: 'Вы читаете эту сагу. Менять книги и главы может владелец.' },
  editor: { badge: 'редактор', name: 'Вы редактор этой саги', text: 'Вы редактор этой саги — удалять и настраивать её может владелец.' },
};

/**
 * The one line under the top bar of a saga that is not the author's own (specs/2026-11-10-ux2-shell.md S1.4, S6.6; П-22;
 * board 14·05, 15·06): his role and who changes the saga, once on every page of it. The answer of the shelf does not
 * name the owner, so the line does not either (Open questions).
 */
export function ReaderLine({ role = 'viewer' }: { role?: LimitedRole }) {
  const line = lines[role];
  return (
    <p aria-label={line.name} className={styles.readerLine} role="note">
      <Badge>{line.badge}</Badge>
      <span>{line.text}</span>
    </p>
  );
}
