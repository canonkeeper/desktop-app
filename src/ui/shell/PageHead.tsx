import type { ComponentPropsWithoutRef, ReactNode } from 'react';
import type { Surface } from '../../shared/navigation/app-route';
import { sectionTitles } from '../../shared/navigation/page-title';
import { sectionWord } from './shell-routes';
import styles from './PageHead.module.css';

/**
 * The heading of a page: the word of its menu item, the same word the menu and the tab name it by (S1.2, SH1.2). A page does
 * not name itself: its surface does, by the one table.
 */
export function pageWord(surface: Surface) {
  return sectionWord({ surface }, sectionTitles[surface]) ?? '';
}

export interface PageHeadProps {
  /** The page the heading names: a section of the menu, «Тариф», «Аккаунт», «Страница не найдена». */
  surface: Surface;
  /** One line under the heading (PS1.3): what the page is for in the author's words, never how it is built. */
  line?: ReactNode;
  /** The one main action of the page, on the right (PS1.1). */
  action?: ReactNode;
  /** «⋯» with the rest of the actions, after the main one. */
  more?: ReactNode;
  /** The id of the heading, for a region the page names by it. */
  headingId?: string;
}

/**
 * The top of a page (specs/2026-11-10-ux2-patterns-states.md PS1.1, П-2; master `Page Header`): the heading by the word of
 * the menu, one line, one main action on the right. The heading takes the focus after a change of the page
 * (`data-page-heading`, specs/2026-11-10-ux1-shell.md SH2). A workplace — the chat, the map — puts its heading into the row
 * of its own panel (`WorkplaceHeading`); the reader, a chapter of «Письмо» and a drawing name themselves in their strip.
 */
export function PageHead({ action, headingId, line, more, surface }: PageHeadProps) {
  return (
    // Not a <header>: the top of a page is no landmark, the top bar of the shell is the banner.
    <div className={styles.head} data-page-head="">
      <div className={styles.words}>
        <h1 className={styles.title} data-page-heading="" id={headingId} tabIndex={-1}>{pageWord(surface)}</h1>
        {line ? <p className={styles.line}>{line}</p> : null}
      </div>
      {action || more ? <div className={styles.actions}>{action}{more}</div> : null}
    </div>
  );
}

/**
 * The heading of a workplace (specs/2026-11-11-ux2-polish.md PL2): the chat and the map do not pay a block of height for it —
 * the same word of the menu stands, seen, first in the row of their own panel, beside its controls. It takes the focus after
 * a change of the page as the top of any page does, and stays the same element while the workplace loads (PL2.4).
 */
export function WorkplaceHeading({ surface, ...props }: Omit<ComponentPropsWithoutRef<'h1'>, 'children' | 'className' | 'tabIndex'> & { surface: Surface }) {
  return <h1 {...props} className={styles.workplace} data-page-heading="" tabIndex={-1}>{pageWord(surface)}</h1>;
}
