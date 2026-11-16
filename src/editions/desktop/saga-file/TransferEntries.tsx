// SPDX-License-Identifier: AGPL-3.0-only
import { Link2, MonitorDown, Square } from 'lucide-react';
import type { SagaSettingsBlock } from '../../../app/edition';
import { Button } from '../../../ui/controls/Button';
import { LongWork } from '../../../ui/feedback/LongWork';
import type { DesktopAccount } from '../account/account-api';
import { FileButton } from './FileButton';
import { useSiteTransfer } from './SiteTransfer';
import { transferRuns } from './transfer-api';
import { transferWords as words } from './transfer-words';
import styles from './SagaFile.module.css';

/**
 * «Перенести с сайта…» of the card of the first screen (the main button) and «Перенести сагу с сайта…» of «Данные»
 * (specs/2026-11-16-desk-site-saga.md TR6.1; board 44·77, 44·78): only in a linked app; while a transfer downloads or opens it
 * stays in its place with «Сага уже переносится» — the backend would start no second one.
 */
export function TransferButton({ label, variant }: { label: string; variant: 'primary' | 'outline' }) {
  const control = useSiteTransfer();
  if (!control || control.account?.state !== 'linked') return null;
  return (
    <FileButton icon={<MonitorDown aria-hidden="true" size={16} />} label={label} loading={control.starting} onClick={control.start}
      reason={transferRuns(control.transfer) ? words.goingReason : null} variant={variant} />
  );
}

/** The link of an app that cannot transfer yet — not linked, waiting, broken —; null in a linked app or outside the app. */
export function useTransferNeedsLink(): DesktopAccount | null {
  const control = useSiteTransfer();
  if (!control?.account || control.account.state === 'linked') return null;
  return control.account;
}

/** «Чтобы перенести сагу с сайта, свяжите приложение с аккаунтом CanonKeeper» (TR6.1; board 44·77, 44·78 «без связи»). */
export function NeedLinkLine() {
  return <p className={styles.needLink}><Link2 aria-hidden="true" size={16} />{words.needLink}</p>;
}

/**
 * The line of the work of a transfer in the place of the notices of the shell, on every page (TR6.2; board 44·80): «Сайт
 * готовит сагу…» until the first byte, «Скачиваю сагу «…» · N МБ» — both with «Остановить» —, «Открываю сагу «…»» with
 * «Остановить» closed by «Сага уже открывается». The bytes are the backend's; the site names no length, so no bar.
 */
function TransferLine() {
  const control = useSiteTransfer();
  const transfer = control?.transfer;
  if (!control || !transfer || !transferRuns(transfer)) return null;
  const opening = transfer.phase === 'opening';
  const preparing = !opening && !transfer.bytes;
  const title = opening ? words.opening(transfer.name) : preparing ? words.preparing : words.downloading(transfer.name, transfer.bytes ?? 0);
  // What a screen reader hears once a state of the work, not at every megabyte.
  const now = opening ? title : preparing ? words.preparing : words.downloadingSaga(transfer.name);
  return (
    <div className={styles.notice} ref={control.line} tabIndex={-1}>
      <LongWork
        action={(
          <Button disabledReason={opening ? words.openingReason : null} loading={control.stopping} onClick={control.stop} size="sm" variant="outline">
            <Square aria-hidden="true" size={14} />{words.stop}
          </Button>
        )}
        announcement={now}
        detail={preparing ? words.preparingLong : undefined}
        label={now}
        title={title}
      />
    </div>
  );
}

/**
 * What the transfer puts around the pages of the app by the seam of the edition (`Edition.useSagaSettingsBlock`): its line in
 * the place of the notices of the shell, and «Перенести сагу с сайта…» under «Новая сага» of the menu of sagas of a linked app
 * (TR6.1; board 44·78) — without a link the item is not there: the card of the first screen and «Данные» lead to the link;
 * while a transfer downloads or opens it stays closed by «Сага уже переносится», as the buttons of the card and of «Данные».
 */
export function useDesktopSagaBlock(): SagaSettingsBlock {
  const control = useSiteTransfer();
  const linked = control?.account?.state === 'linked';
  return {
    item: null,
    notice: <TransferLine />,
    overlay: null,
    menuItem: control && linked
      ? { label: words.entry, icon: MonitorDown, onSelect: control.start, reason: transferRuns(control.transfer) ? words.goingReason : null }
      : null,
  };
}
