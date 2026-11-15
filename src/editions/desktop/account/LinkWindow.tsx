// SPDX-License-Identifier: AGPL-3.0-only
import { Clock, Copy, ExternalLink, Monitor, X } from 'lucide-react';
import { useEffect, useState, type MouseEvent } from 'react';
import { desktopShell } from '../../../shared/desktop/shell';
import { Button } from '../../../ui/controls/Button';
import { IconButton } from '../../../ui/controls/IconButton';
import { LinkAction } from '../../../ui/controls/LinkAction';
import { Alert } from '../../../ui/feedback/Alert';
import { useToasts } from '../../../ui/feedback/useToastQueue';
import { Dialog } from '../../../ui/overlays/Dialog';
import { external } from '../external';
import type { DesktopAccount } from './account-api';
import { accountWords, computerLine, remainingWords } from './account-words';
import styles from './Account.module.css';

/** The time of the screen once a second while it shows a term: the term itself is the server's (`link.expiresAt`). */
export function useClock() {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 1_000);
    return () => window.clearInterval(timer);
  }, []);
  return now;
}

interface LinkWindowProps {
  account: DesktopAccount;
  browserFailed: boolean;
  onHide: () => void;
  onCancel: () => void;
  onOpenAgain: () => void;
}

/**
 * The window of the waiting (specs/2026-11-15-desk-account-link-web.md AW1.1, AW1.2; board 44·5, `Pattern/Desktop/Link
 * Waiting`): what to do, the address with «Скопировать адрес», what the site will see of this computer, the term, «Отменить»
 * and «Открыть ещё раз». ×, Esc and «Назад» hide it — the link waits on —; only «Отменить» cancels it. The browser did not open
 * (the shell refused it, or there is no shell) — «Браузер не открылся…» stands as a warning and «Открыть ещё раз» is a link.
 */
export function LinkWindow({ account, browserFailed, onCancel, onHide, onOpenAgain }: LinkWindowProps) {
  const toasts = useToasts();
  const now = useClock();
  const address = account.link!.address;
  const shell = desktopShell();
  const copy = () => {
    void navigator.clipboard.writeText(address).then(
      () => toasts.push({ message: accountWords.copied, tone: 'success' }),
      () => toasts.push({ message: accountWords.notCopied, tone: 'error' }),
    );
  };
  const opened = external(address, shell, toasts);
  return (
    <Dialog label={accountWords.waitingTitle} onDismiss={onHide} width={520}>
      <div className={styles.window}>
        <div className={styles.head}>
          <h2 data-overlay-heading="">{accountWords.waitingTitle}</h2>
          <IconButton aria-label={accountWords.close} onClick={onHide} size="sm"><X aria-hidden="true" size={16} /></IconButton>
        </div>
        <div className={styles.addressRow}>
          <input aria-label={accountWords.address} className={styles.address} onFocus={(event) => event.currentTarget.select()} readOnly value={address} />
          <Button onClick={copy} size="sm" variant="outline"><Copy aria-hidden="true" size={14} />{accountWords.copy}</Button>
        </div>
        {/* The shell refused after the window opened: the warning is announced; without a shell it stands with the window. */}
        {browserFailed
          ? <Alert role={shell ? 'alert' : 'note'} tone="warning">{accountWords.noBrowser}</Alert>
          : <p className={styles.hint}>{accountWords.noBrowser}</p>}
        <p className={styles.fact}><Monitor aria-hidden="true" className={styles.factIcon} size={16} />{computerLine(account.computer)}</p>
        <p className={styles.fact}><Clock aria-hidden="true" className={styles.factIcon} size={16} />{remainingWords(account.link!.expiresAt, now)}</p>
        <div className={styles.footer}>
          <Button onClick={onCancel} variant="outline">{accountWords.cancel}</Button>
          {browserFailed ? (
            <LinkAction {...opened} onClick={shell ? (event: MouseEvent<HTMLAnchorElement>) => { event.preventDefault(); onOpenAgain(); } : undefined}>
              {accountWords.openAgain}
            </LinkAction>
          ) : (
            <Button onClick={onOpenAgain} variant="primary"><ExternalLink aria-hidden="true" size={16} />{accountWords.openAgain}</Button>
          )}
        </div>
      </div>
    </Dialog>
  );
}
