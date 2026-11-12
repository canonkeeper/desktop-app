import { ChevronDown, Menu, Search, StickyNote, type LucideIcon } from 'lucide-react';
import type { ComponentPropsWithRef } from 'react';
import { Button } from '../controls/Button';
import { IconButton } from '../controls/IconButton';
import { Badge } from '../feedback/Badge';
import { AccountActingMark, useAccountButtonLabel } from '../navigation/AccountSession';
import { TopBarTalk } from '../navigation/TopBarStrip';
import { ShellAccount, type ShellAccountProps } from './ShellAccount';
import styles from './Shell.module.css';

/** The saga of the page in the top bar (specs/2026-11-10-ux2-shell.md S3.1): its name opens the one menu of the saga. */
export interface ShellSaga {
  name: string;
  /** The saga-example of the site (specs/2026-11-09-demo-saga.md DS4.1): «Пример» by its name. */
  demo: boolean;
  menuOpen: boolean;
  /** Opens the menu under the name pressed, or as a sheet without one; closes it when it is open. */
  onOpenMenu: (trigger?: HTMLElement) => void;
}

/** The three widths of the shell (S1.3): from 1100 px, 768–1099 px, below 768 px. */
export type ShellWidth = 'wide' | 'compact' | 'phone';

export interface ShellTopBarProps {
  width: ShellWidth;
  saga: ShellSaga | null;
  /**
   * The word in place of a saga that is not there: «Первая сага» of an author without one (board 14·04), «Админка» of the
   * admin panel. The section is never here: the menu and the heading of the page name it (specs/2026-11-11-ux2-polish.md
   * PL1.1).
   */
  place?: string | undefined;
  /** «Поиск по книгам» of a page of the saga (S1.5). */
  onOpenSearch?: () => void;
  searchRef?: ComponentPropsWithRef<'button'>['ref'];
  /** «Заметка»: a new note over the page. */
  onNote?: () => void;
  /** «Поговорить» of the edition (specs/2026-11-03-voice-assistant.md V1.1). */
  talk?: boolean;
  account?: ShellAccountProps;
  /** The drawer of a phone and its mark: a reply of support or a ready answer of the chat waits inside. */
  onOpenMenu?: () => void;
  menuMark?: boolean;
}

/**
 * One top bar of a signed-in author (S1.2; board 14, `Pattern/Shell/Top Bar`): the name of the saga — «Поиск по книгам» —
 * «Поговорить» · «Заметка» · «Тариф» · the account. The section is named once, by the menu and the heading of the page
 * (specs/2026-11-11-ux2-polish.md PL1.1). On 768–1099 px the search and «Заметка» are buttons with a word under the icon;
 * below 768 px — «Меню» · the saga · «Поиск» · «Поговорить» · «Заметка», the rest is in the drawer (Decision log of the
 * wave). The name of the product is not here: it is in the tab.
 */
export function ShellTopBar({ account, menuMark = false, onNote, onOpenMenu, onOpenSearch, place, saga, searchRef, talk = false, width }: ShellTopBarProps) {
  // Below 768 px the account is in the drawer: «Меню» says what its button said — the admin working as another user, a
  // reply of support (Decision log of the wave).
  const accountLabel = useAccountButtonLabel();
  if (width === 'phone') {
    return (
      <header className={styles.bar} data-shell-top-bar="" data-width="phone">
        {onOpenMenu ? (
          <IconButton aria-label={`Открыть меню${accountLabel.slice('Аккаунт'.length)}`} className={styles.menuButton} onClick={onOpenMenu} size="touch">
            <Menu aria-hidden="true" size={22} />
            {menuMark ? <span aria-hidden="true" className={styles.menuMark} data-menu-mark="" /> : null}
            <AccountActingMark />
          </IconButton>
        ) : null}
        <p className={styles.phonePlace}>
          {saga ? <><span className={styles.visuallyHidden}>Сага: </span>{saga.name}</> : place}
        </p>
        {onOpenSearch ? <CaptionButton Icon={Search} label="Поиск" name="Поиск по книгам" onClick={onOpenSearch} ref={searchRef} /> : null}
        {talk ? <TopBarTalk /> : null}
        {onNote ? <CaptionButton Icon={StickyNote} label="Заметка" onClick={onNote} /> : null}
        {/* A page without the drawer — the admin panel — keeps the account in its top bar. */}
        {!onOpenMenu && account ? <ShellAccount {...account} /> : null}
      </header>
    );
  }
  const compact = width === 'compact';
  return (
    <header className={styles.bar} data-shell-top-bar="" data-width={width}>
      <div className={styles.place}>
        {saga ? (
          <button aria-expanded={saga.menuOpen} aria-haspopup="menu" className={styles.saga} onClick={(event) => saga.onOpenMenu(event.currentTarget)}
            title={saga.name} type="button">
            {/* The name of the button is its words: «Сага: …» for the reader of the screen, the name itself on the screen. */}
            <span className={styles.sagaName}><span className={styles.visuallyHidden}>Сага: </span>{saga.name}</span>
            <ChevronDown aria-hidden="true" size={14} />
          </button>
        ) : null}
        {saga?.demo ? <Badge className={styles.demo}>Пример</Badge> : null}
        {!saga && place ? <span className={styles.placeWord}>{place}</span> : null}
      </div>
      <div className={styles.find}>
        {onOpenSearch ? (compact
          ? <CaptionButton Icon={Search} label="Поиск" name="Поиск по книгам" onClick={onOpenSearch} ref={searchRef} />
          : (
            <button aria-haspopup="dialog" aria-label="Поиск по книгам" className={styles.searchField} onClick={onOpenSearch} ref={searchRef} type="button">
              <Search aria-hidden="true" size={16} />
              <span>Поиск по книгам</span>
            </button>
          )) : null}
      </div>
      <div className={styles.actions}>
        {talk ? <TopBarTalk /> : null}
        {onNote ? (compact
          ? <CaptionButton Icon={StickyNote} label="Заметка" onClick={onNote} />
          : <Button className={styles.noteButton} onClick={onNote} size="touch" variant="ghost"><StickyNote aria-hidden="true" size={16} />Заметка</Button>) : null}
        {account ? <ShellAccount {...account} /> : null}
      </div>
    </header>
  );
}

/** A button with its word under the icon, a target of 44×44 (`Button/Ghost Caption`). */
function CaptionButton({ Icon, label, name, ...props }: Omit<ComponentPropsWithRef<'button'>, 'children'> & { Icon: LucideIcon; label: string; name?: string }) {
  return (
    <button {...props} aria-haspopup={name ? 'dialog' : undefined} aria-label={name} className={styles.captionButton} type="button">
      <Icon aria-hidden="true" size={20} />
      <span>{label}</span>
    </button>
  );
}
