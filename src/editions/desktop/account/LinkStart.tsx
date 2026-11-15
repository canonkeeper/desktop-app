// SPDX-License-Identifier: AGPL-3.0-only
import { Clock, ExternalLink, Link2 } from 'lucide-react';
import { Button, type ControlSize } from '../../../ui/controls/Button';
import { Alert } from '../../../ui/feedback/Alert';
import type { DesktopAccount } from './account-api';
import { accountWords, waitingLine } from './account-words';
import { useAccountLink } from './AccountLink';
import { useClock } from './LinkWindow';
import styles from './Account.module.css';

/** «Ждёт подтверждения в браузере · Осталось 9 минут» with «Открыть окно связи» (AW1.2; board 44·4, 44·6). */
export function LinkWaiting({ expiresAt, size = 'sm' }: { expiresAt: string; size?: ControlSize }) {
  const link = useAccountLink();
  const now = useClock();
  return (
    <span className={styles.waiting}>
      <span className={styles.waitingLine}><Clock aria-hidden="true" className={styles.factIcon} size={14} />{waitingLine(expiresAt, now)}</span>
      <Button onClick={() => link?.openWindow()} size={size} variant="outline"><ExternalLink aria-hidden="true" size={14} />{accountWords.openWindow}</Button>
    </span>
  );
}

/**
 * The place of «Связать…» (specs/2026-11-15-desk-account-link-web.md AW1.1, AW1.2, AW2.1): «Связать с аккаунтом» the first
 * time, «Связать снова» after a failure or a broken link; while the link waits — the line of the waiting with «Открыть окно
 * связи»; a build that cannot be linked — its line and no button. One start for every place, with the intent of the place; a
 * place in a window closes it once the window of the waiting opens (`onShown`).
 */
export function LinkStart({ account, chooseHelper, onShown, size = 'md' }: {
  account: DesktopAccount;
  chooseHelper: boolean;
  onShown?: () => void;
  size?: ControlSize;
}) {
  const link = useAccountLink();
  if (!link) return null;
  if (link.unsupported) return <Alert role="note" tone="info">{accountWords.unsupported}</Alert>;
  if (account.state === 'waiting' && account.link) return <LinkWaiting expiresAt={account.link.expiresAt} size={size === 'md' ? 'sm' : size} />;
  return (
    <Button loading={link.starting} onClick={() => link.start(chooseHelper, onShown)} size={size} variant="primary">
      <Link2 aria-hidden="true" size={16} />{account.state === 'none' ? accountWords.link : accountWords.relink}
    </Button>
  );
}
