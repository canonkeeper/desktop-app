import { LogOut, Menu, Search } from 'lucide-react';
import { useRef, type ReactNode, type RefObject } from 'react';
import { Button } from '../controls/Button';
import { IconButton } from '../controls/IconButton';
import { NotesButton } from '../NotesButton';
import { Popover } from '../overlays/Popover';
import { AccountActingMark, AccountSessionItems, AccountUnreadMark, useAccountButtonLabel } from './AccountSession';
import { TopBarStrip } from './TopBarStrip';
import styles from './WorkspaceTopBar.module.css';

export interface WorkspaceTopBarProps {
  compact?: boolean;
  title: string;
  accountMenuOpen: boolean;
  onDismissAccountMenu: () => void;
  /** Signing out, where the edition signs in: the account menu stands only then (specs/2026-11-07-desktop-app.md DA2.7). */
  onLogout?: () => void;
  /** The section menu below desktop width; a page without sections (the admin panel) has none. */
  onOpenMobileMenu?: () => void;
  onOpenSearch?: () => void;
  onToggleAccountMenu: () => void;
  searchTriggerRef?: RefObject<HTMLButtonElement | null>;
  /** «Поговорить» of the voice co-author where the page has one (specs/2026-11-03-voice-assistant.md V1.1). */
  talk?: ReactNode;
  userName: string;
}

export function WorkspaceTopBar({
  title, accountMenuOpen, onDismissAccountMenu, onLogout, onOpenMobileMenu,
  onOpenSearch, onToggleAccountMenu, searchTriggerRef, talk, userName, compact = false,
}: WorkspaceTopBarProps) {
  const accountButtonRef = useRef<HTMLButtonElement>(null);
  const accountLabel = useAccountButtonLabel();
  return (<>
    <header className={styles.bar} data-search={Boolean(onOpenSearch)} data-compact={compact || undefined} data-menu={Boolean(onOpenMobileMenu)}>
      {onOpenMobileMenu ? (
        <IconButton aria-label="Открыть меню" className={styles.menuButton} onClick={onOpenMobileMenu} size="touch">
          <Menu aria-hidden="true" size={20} />
        </IconButton>
      ) : null}
      <p className={styles.brand}>
        <span className={styles.desktopBrand}>Canon Keeper</span>
        <span className={styles.mobileTitle}>{title}</span>
      </p>
      {onOpenSearch ? (
        <Button aria-label="Глобальный поиск" className={styles.searchButton} onClick={onOpenSearch}
          ref={searchTriggerRef} variant="outline">
          <Search aria-hidden="true" size={16} />
          <span className={styles.searchLabel}>Глобальный поиск</span>
        </Button>
      ) : null}
      <div className={styles.actions}>
        {talk}
        <NotesButton />
        {onLogout ? (
          <IconButton aria-expanded={accountMenuOpen} aria-haspopup="dialog" aria-label={accountLabel}
            className={styles.accountButton} onClick={onToggleAccountMenu} ref={accountButtonRef} size="touch">
            <span aria-hidden="true" className={styles.avatar}>{userName.slice(0, 1)}</span>
            <AccountActingMark />
            <AccountUnreadMark />
          </IconButton>
        ) : null}
      </div>
      {onLogout ? (
        <Popover className={styles.accountMenu} label="Меню аккаунта" onDismiss={onDismissAccountMenu} open={accountMenuOpen}>
          <p className={styles.accountName}>{userName}</p>
          <AccountSessionItems onDone={() => { onDismissAccountMenu(); accountButtonRef.current?.focus(); }} />
          <Button className={styles.logoutButton} onClick={onLogout} size="touch" variant="ghost">
            <LogOut aria-hidden="true" size={17} />Выйти
          </Button>
        </Popover>
      ) : null}
    </header>
    <TopBarStrip />
  </>);
}
