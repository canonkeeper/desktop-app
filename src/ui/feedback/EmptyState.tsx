import type { HTMLAttributes, ReactNode } from 'react';
import styles from './EmptyState.module.css';

export interface EmptyStateProps extends Omit<HTMLAttributes<HTMLDivElement>, 'title' | 'children'> {
  /** The icon of the section (П-15), drawn for the eye only. */
  icon?: ReactNode;
  /** What will appear here: «Здесь будут главы в работе». */
  title: string;
  /** One phrase — why it is worth it to the author. */
  why?: string;
  /** The one button of the first step. */
  action?: ReactNode;
  /**
   * Where the first step cannot be taken here (a reader of the saga, no book yet): one line of what is needed, in place of
   * a button that would do nothing — «Сначала загрузите книгу» with a link to the shelf.
   */
  need?: ReactNode;
}

/**
 * An empty section (specs/2026-11-10-ux2-patterns-states.md PS2.1, П-6; master `Pattern/Common/Empty Block`, board 15·07):
 * three parts and only them — what will appear here · one phrase why · one button of the first step. It tells nothing of
 * other sections.
 */
export function EmptyState({ action, className, icon, need, title, why, ...props }: EmptyStateProps) {
  return (
    <div {...props} className={`${styles.empty}${className ? ` ${className}` : ''}`} data-empty="">
      {icon ? <span aria-hidden="true" className={styles.icon}>{icon}</span> : null}
      <p className={styles.title}>{title}</p>
      {why ? <p className={styles.why}>{why}</p> : null}
      {action ?? (need ? <p className={styles.need}>{need}</p> : null)}
    </div>
  );
}

/**
 * Nothing by the filter of a section that is not empty (PS2.3): it says so and gives the filter back — not the empty
 * section, whose first step the author has already taken.
 */
export function EmptyFilter({ onReset, reset = 'Сбросить отбор', words = 'По этому отбору ничего нет' }: {
  onReset: () => void;
  /** «Сбросить поиск» of a search that found nothing. */
  reset?: string;
  /** «Ничего не нашлось по «…»» of a search. */
  words?: string | undefined;
}) {
  return (
    <div className={styles.filter} data-empty="filter">
      <p>{words}</p>
      <button className={styles.reset} onClick={onReset} type="button">{reset}</button>
    </div>
  );
}
