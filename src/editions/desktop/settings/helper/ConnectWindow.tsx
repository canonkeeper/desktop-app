// SPDX-License-Identifier: AGPL-3.0-only
import { Check, KeyRound, RotateCcw, SlidersHorizontal, UserRound, X } from 'lucide-react';
import { useEffect } from 'react';
import type { RouteNavigate } from '../../../../shared/navigation/app-route';
import { Button } from '../../../../ui/controls/Button';
import { IconButton } from '../../../../ui/controls/IconButton';
import { Alert } from '../../../../ui/feedback/Alert';
import { Dialog } from '../../../../ui/overlays/Dialog';
import { useDesktopAccount } from '../../account/account-api';
import { useAccountLink } from '../../account/AccountLink';
import { LinkStart } from '../../account/LinkStart';
import { helperPartKey } from '../part-props';
import { chooseWords, choiceRefusal, useChooseHelper } from './choose-helper';
import { useAiSettings } from './helper-api';
import styles from './Helper.module.css';

/** The words of the window «Подключить помощника» (Data of specs/2026-11-15-desk-account-link-web.md; board 44·4). */
export const connectWords = {
  title: 'Подключить помощника',
  close: 'Закрыть',
  account: 'Аккаунт CanonKeeper',
  accountLine: 'Помощник CanonKeeper по вашему тарифу. Настраивать нечего',
  choose: 'Выбрать',
  own: 'Свой ИИ',
  ownLine: 'Ключ и адрес вашего провайдера. Тариф не тратится',
  setUp: 'Настроить',
} as const;

/**
 * «Подключить помощника» of the line of the reading (specs/2026-11-15-desk-account-link-web.md AW3.3; board 44·4,
 * `Pattern/Desktop/Connect Dialog`, `Helper Choice`): two cards by the words of the build. «Аккаунт CanonKeeper» — «Связать
 * с аккаунтом» with the intent to choose its helper (AW3.2), its waiting while the link waits, «Выбрать» once linked, and the
 * refusal of the choice with «Повторить»; «Свой ИИ» — «Настроить» opens «Настройки → Помощник». A link started from its card
 * closes it: one window, the window of the waiting, stands over the page (AW1.1). It closes itself once the helper of the account
 * answers.
 */
export function ConnectWindow({ navigate, onClose }: { navigate: RouteNavigate; onClose: () => void }) {
  const link = useAccountLink();
  // Outside the link of the edition the queries never run.
  const api = link?.api as NonNullable<typeof link>['api'];
  const account = useDesktopAccount(api, link !== null).data;
  const settings = useAiSettings(api, link !== null).data;
  const choose = useChooseHelper(api);
  const answers = account?.state === 'linked' && settings?.settings.source === 'account';
  useEffect(() => {
    if (answers) onClose();
  }, [answers, onClose]);
  // A refusal of the choice changes nothing: 409 has the link read again, which shows «Связать с аккаунтом»; the rest says why.
  const code = choose.isError ? choiceRefusal(choose.error) : 'none';
  const refused = code === 'ai.account_unreachable' ? chooseWords.unreachable : code !== 'none' && code !== 'helper.account_unlinked' ? chooseWords.failed : null;
  return (
    <Dialog label={connectWords.title} onDismiss={onClose} width={600}>
      <div className={styles.connect}>
        <div className={styles.connectHead}>
          <h2 data-overlay-heading="">{connectWords.title}</h2>
          <IconButton aria-label={connectWords.close} onClick={onClose} size="sm"><X aria-hidden="true" size={16} /></IconButton>
        </div>
        <div className={styles.cards}>
          <section aria-label={connectWords.account} className={styles.card}>
            <UserRound aria-hidden="true" className={styles.cardIcon} size={18} />
            <h3>{connectWords.account}</h3>
            <p className={styles.note}>{connectWords.accountLine}</p>
            {account?.state === 'linked' ? (
              <Button loading={choose.isPending} onClick={() => choose.mutate('account')} size="sm" variant="primary">
                <Check aria-hidden="true" size={14} />{connectWords.choose}
              </Button>
            ) : account ? <LinkStart account={account} chooseHelper onShown={onClose} size="sm" /> : null}
          </section>
          <section aria-label={connectWords.own} className={styles.card}>
            <KeyRound aria-hidden="true" className={styles.cardIcon} size={18} />
            <h3>{connectWords.own}</h3>
            <p className={styles.note}>{connectWords.ownLine}</p>
            <Button onClick={() => { onClose(); navigate('/settings', { part: helperPartKey }); }} size="sm" variant="outline">
              <SlidersHorizontal aria-hidden="true" size={14} />{connectWords.setUp}
            </Button>
          </section>
        </div>
        {refused ? (
          <Alert className={styles.refused} tone="error">
            <span>{refused}</span>
            <Button onClick={() => choose.mutate('account')} size="sm" variant="ghost"><RotateCcw aria-hidden="true" size={14} />{chooseWords.retry}</Button>
          </Alert>
        ) : null}
      </div>
    </Dialog>
  );
}
