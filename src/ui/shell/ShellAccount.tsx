import { LogOut } from 'lucide-react';
import { useRef, useState } from 'react';
import { useAccountSession } from '../../shared/session/account-session';
import { Button } from '../controls/Button';
import { IconButton } from '../controls/IconButton';
import { AccountActingMark, AccountPlanBadge, AccountSessionItems, AccountUnreadMark, useAccountButtonLabel } from '../navigation/AccountSession';
import { onMenuButtonKey, Popover } from '../overlays/Popover';
import { SiteThemeChoice } from '../SiteThemeChoice';
import styles from './Shell.module.css';

/** The author of the edition that signs in: his name, his address and signing out (specs/2026-11-07-desktop-app.md DA2.7). */
export interface ShellAccountProps {
  userName: string;
  /** Under the name, small (specs/2026-11-10-ux2-tariff-account.md TA1.1, А-51). */
  userEmail?: string;
  onLogout: () => void;
}

/**
 * «Тариф» and the account at the end of the top bar (specs/2026-11-10-ux2-shell.md S1.2, S1.6, S4.1): the name of the
 * tier in force leads to «Тариф», the account button opens the menu of the account in five lines.
 */
export function ShellAccount({ onLogout, userEmail, userName }: ShellAccountProps) {
  const [open, setOpen] = useState(false);
  const button = useRef<HTMLButtonElement>(null);
  const label = useAccountButtonLabel();
  return (
    <>
      <AccountPlanBadge />
      <IconButton aria-expanded={open} aria-haspopup="dialog" aria-label={label} className={styles.accountButton}
        onClick={() => setOpen((shown) => !shown)} onKeyDown={onMenuButtonKey(open, () => setOpen(true))} ref={button} size="touch">
        <span aria-hidden="true" className={styles.avatar}>{userName.slice(0, 1)}</span>
        <AccountActingMark />
        <AccountUnreadMark />
      </IconButton>
      {/* Walked by the keys as a menu: the first line takes the focus, ↑↓ move, Esc gives it back (specs/2026-11-11-ux9-account.md AC3.1). */}
      <Popover actions className={styles.accountMenu} label="Меню аккаунта" onDismiss={() => setOpen(false)} open={open}>
        <AccountBlock onDone={() => { setOpen(false); button.current?.focus(); }} onLogout={onLogout} userEmail={userEmail} userName={userName} />
      </Popover>
    </>
  );
}

/**
 * What the menu of the account holds (specs/2026-11-10-ux2-tariff-account.md TA1.1): the name and under it the address,
 * «Тариф · …», «Аккаунт», «Написать нам», «Тема: Светлая · Тёмная · Как в системе» (specs/2026-11-12-ux11-dark-core.md
 * TH1.2) and «Выйти». The drawer of a phone holds the same, without «Написать нам» when «Помощь» stands above it (S1.3)
 * and without «Тема»: its row would take two sections out of sight in a browser of a phone, the theme is on «Аккаунт»
 * («Вид сайта») and beside the text («Вид текста»).
 */
export function AccountBlock({ onDone, onLogout, support = true, theme = true, userEmail, userName }: ShellAccountProps & { onDone?: () => void; support?: boolean; theme?: boolean }) {
  const session = useAccountSession();
  // The line of an administrator working as the author and the line of an unverified address name the address themselves.
  const email = session?.actingAs || session?.emailVerification ? null : userEmail;
  return (
    <>
      <div className={styles.accountIdentity}>
        <p className={styles.accountName}>{userName}</p>
        {email ? <p className={styles.accountEmail}>{email}</p> : null}
      </div>
      <AccountSessionItems onDone={onDone} support={support} />
      {theme ? (
        <div className={styles.themeRow}>
          <span aria-hidden="true" className={styles.themeLabel}>Тема</span>
          <SiteThemeChoice fullWidth />
        </div>
      ) : null}
      <Button className={styles.logoutButton} onClick={onLogout} size="touch" variant="ghost">
        <LogOut aria-hidden="true" size={17} />Выйти
      </Button>
    </>
  );
}

/**
 * «Помощь»: the window «Написать нам» of the edition (specs/2026-11-10-ux2-contact.md CT2.1) — on «Ответы» while a reply
 * waits — not while the admin works as another user (contacts R4.2.6).
 */
export function useShellHelp() {
  const session = useAccountSession();
  const open = session?.onOpenContact;
  return open && !session.actingAs ? () => open() : null;
}

/** The replies of support the author has not opened yet: the mark of «Помощь» and of the menu of a phone (F3.2). */
export function useShellHelpReplies() {
  return useAccountSession()?.contactUnread ?? 0;
}
