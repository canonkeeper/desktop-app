// SPDX-License-Identifier: AGPL-3.0-only
import type { RefObject } from 'react';
import { Clock, ExternalLink, X } from 'lucide-react';
import { desktopShell } from '../../../shared/desktop/shell';
import { Button, ButtonLink } from '../../../ui/controls/Button';
import { IconButton } from '../../../ui/controls/IconButton';
import { useToasts } from '../../../ui/feedback/useToastQueue';
import { Dialog } from '../../../ui/overlays/Dialog';
import { external } from '../external';
import type { SiteSagaTransfer } from './transfer-api';
import { transferWords as words } from './transfer-words';
import styles from './SagaFile.module.css';

interface TransferWindowProps {
  transfer: SiteSagaTransfer;
  onHide: () => void;
  onCancel: () => void;
  /** Where the focus goes once the window closes, when it stands: the line of the work of a saga that began to come. */
  returnFocusRef?: RefObject<HTMLElement | null>;
}

/**
 * «Перенесите сагу на сайте» (specs/2026-11-16-desk-site-saga.md TR6.2; board 44·79, `Pattern/Desktop/Transfer Waiting`): what
 * to do on the page of the site, the account to sign in to and the term of the link, all from the server; «Открыть страницу
 * снова» — the shell opens the page in the browser of the system, without it it is an ordinary link to a new tab — and
 * «Отменить». ×, Esc and «Назад» hide it — the transfer waits on —; only «Отменить» stops it.
 */
export function TransferWindow({ onCancel, onHide, returnFocusRef, transfer }: TransferWindowProps) {
  const toasts = useToasts();
  const link = external(transfer.address ?? '', desktopShell(), toasts);
  return (
    <Dialog label={words.waitingTitle} onDismiss={onHide} returnFocusRef={returnFocusRef} width={520}>
      <section className={styles.opened}>
        <div className={styles.head}>
          <h2 data-overlay-heading="">{words.waitingTitle}</h2>
          <IconButton aria-label={words.close} onClick={onHide} size="sm"><X aria-hidden="true" size={16} /></IconButton>
        </div>
        <p className={styles.message}>{words.waitingText(transfer.email)}</p>
        {transfer.expiresAt ? (
          <p className={styles.term}><Clock aria-hidden="true" size={16} />{words.until(transfer.expiresAt)}</p>
        ) : null}
        <footer className={styles.footer}>
          <Button onClick={onCancel} variant="outline">{words.cancel}</Button>
          <ButtonLink {...link} variant="primary"><ExternalLink aria-hidden="true" size={16} />{words.openAgain}</ButtonLink>
        </footer>
      </section>
    </Dialog>
  );
}
