// SPDX-License-Identifier: AGPL-3.0-only
import { ChevronDown, ChevronRight, ExternalLink, Link2Off, Lock, Monitor, RefreshCw } from 'lucide-react';
import { useId, useState, type ReactNode } from 'react';
import { loadFailureWords, publicApiErrorMessage } from '../../../entities/api-errors';
import { desktopShell } from '../../../shared/desktop/shell';
import { Button, ButtonLink } from '../../../ui/controls/Button';
import { Alert } from '../../../ui/feedback/Alert';
import { ErrorBlock } from '../../../ui/feedback/ErrorBlock';
import { SkeletonArea } from '../../../ui/feedback/Skeleton';
import { useToasts } from '../../../ui/feedback/useToastQueue';
import { Confirm } from '../../../ui/overlays/Confirm';
import { MoreMenu } from '../../../ui/overlays/MoreMenu';
import { external } from '../external';
import type { SettingsPartProps } from '../settings/part-props';
import { useDesktopAccount, useRefreshAccount, useUnlinkAccount, type AccountInfo, type DesktopAccount } from './account-api';
import {
  accountWords, computerOnSite, failureLine, freshLine, headLine, offlineLine, questionsLine, readingLine, tierLine,
} from './account-words';
import { LinkStart } from './LinkStart';
import { useClock } from './LinkWindow';
import styles from './Account.module.css';

/**
 * «Настройки → Аккаунт» (specs/2026-11-15-desk-account-link-web.md AW2; board 44·6–44·8, `Pattern/Desktop/Settings Account`):
 * every word is of getAccount. Not linked — what the account gives in this build, «Связать с аккаунтом» and «Что проходит
 * через сервер»; the link waits — its line with «Открыть окно связи»; it failed — why, with «Связать снова»; linked — who, the
 * tier, what is left, when the site told it and «Обновить сведения», this computer on the site, where the link lies, the pages
 * of the site and «Отвязать приложение…»; the link broken — why, with «Связать снова».
 */
export function AccountPart({ api }: SettingsPartProps) {
  const account = useDesktopAccount(api);
  if (account.isPending) return <SkeletonArea count={4} shape="lines" />;
  if (account.isError) {
    return <ErrorBlock {...loadFailureWords(account.error)} onRetry={() => void account.refetch()} retrying={account.isRefetching} title={accountWords.loadFailed} />;
  }
  const state = account.data;
  if (state.state === 'linked') return <Linked account={state} api={api} />;
  if (state.state === 'broken') {
    return (
      <div className={styles.part}>
        <Alert role="note" tone="warning">{accountWords.broken}</Alert>
        <div className={styles.actions}><LinkStart account={state} chooseHelper={false} /></div>
      </div>
    );
  }
  return <NotLinked account={state} />;
}

/** Not linked, waiting or failed (AW2.1, AW2.4): the words of the build, the place of «Связать…», what goes through the server. */
function NotLinked({ account }: { account: DesktopAccount }) {
  const [open, setOpen] = useState(false);
  const server = useId();
  return (
    <div className={styles.part}>
      <p className={styles.text}>{accountWords.intro}</p>
      {account.state === 'failed' && account.failure ? <Alert role="note" tone="error">{failureLine(account.failure, account.passStore)}</Alert> : null}
      <div className={styles.actions}>
        <LinkStart account={account} chooseHelper={false} />
        <Button aria-controls={server} aria-expanded={open} onClick={() => setOpen((shown) => !shown)} size="sm" variant="ghost">
          {open ? <ChevronDown aria-hidden="true" size={14} /> : <ChevronRight aria-hidden="true" size={14} />}{accountWords.server}
        </Button>
      </div>
      <div hidden={!open} id={server}>
        <Alert role="note" tone="info">{accountWords.serverLine}</Alert>
      </div>
    </div>
  );
}

/**
 * Linked (AW2.2, AW2.3): what the site told with its time; no connection — the same with «Нет связи…»; nothing told yet — the
 * link and «Обновить сведения». «Отвязать приложение…» in «⋯» asks first and says what the unlinking reached (AW1.5).
 */
function Linked({ account, api }: { account: DesktopAccount; api: SettingsPartProps['api'] }) {
  const toasts = useToasts();
  const now = useClock();
  const shell = desktopShell();
  const refresh = useRefreshAccount(api);
  const unlink = useUnlinkAccount(api);
  const [asking, setAsking] = useState(false);
  const info = account.account;
  const refreshButton = (
    <Button loading={refresh.isPending} onClick={() => refresh.mutate(undefined, {
      onError: (error) => toasts.push({ message: publicApiErrorMessage(error, accountWords.refreshFailed), tone: 'error' }),
    })} size="sm" variant="ghost">
      <RefreshCw aria-hidden="true" size={14} />{accountWords.refresh}
    </Button>
  );
  const apps = (
    <ButtonLink {...external(account.pages.apps, shell, toasts)} size="sm" variant="outline"><ExternalLink aria-hidden="true" size={14} />{accountWords.apps}</ButtonLink>
  );
  const more = (
    <MoreMenu items={[{ label: accountWords.unlinkItem, icon: Link2Off, run: () => setAsking(true) }]} label={accountWords.more} />
  );
  const confirmUnlink = () => unlink.mutate(undefined, {
    onSuccess: (result) => {
      setAsking(false);
      if (result.siteUnlinked) {
        toasts.push({ message: accountWords.unlinked, tone: 'success' });
        return;
      }
      toasts.push({ message: accountWords.unlinkedHere, tone: 'info', persistent: true, action: apps });
    },
  });
  return (
    <div className={styles.part}>
      {info === null ? (
        <Alert className={styles.offline} role="note" tone="info">
          <span>{accountWords.offlineEmpty}</span>
          <span className={styles.offlineActions}>{refreshButton}{more}</span>
        </Alert>
      ) : (
        <>
          {account.unreachable ? (
            <Alert className={styles.offline} role="note" tone="warning">
              <span>{offlineLine(info.at, now)}</span>
              <span className={styles.offlineActions}>{refreshButton}</span>
            </Alert>
          ) : null}
          <AccountFacts account={account} apps={apps} info={info} more={more} shell={shell} />
          {account.unreachable ? null : <p className={styles.fresh}><span className={styles.note}>{freshLine(info.at, now)}</span>{refreshButton}</p>}
          <p className={styles.fact}><Monitor aria-hidden="true" className={styles.factIcon} size={16} />{computerOnSite(account.computer)}</p>
          <p className={styles.fact}>
            <Lock aria-hidden="true" className={styles.factIcon} size={16} />
            <span className={styles.note}>{account.passStore === 'file' ? accountWords.storeFile : accountWords.storeSystem}</span>
          </p>
        </>
      )}
      {asking ? (
        <Confirm
          action={accountWords.unlinkAction}
          busy={unlink.isPending}
          error={unlink.isError ? accountWords.unlinkFailed : undefined}
          onCancel={() => { setAsking(false); unlink.reset(); }}
          onConfirm={confirmUnlink}
          title={accountWords.unlinkTitle}
        >
          {accountWords.unlinkLine}
        </Confirm>
      ) : null}
    </div>
  );
}

function AccountFacts({ account, apps, info, more, shell }: {
  account: DesktopAccount;
  info: AccountInfo;
  apps: ReactNode;
  more: ReactNode;
  shell: ReturnType<typeof desktopShell>;
}) {
  const toasts = useToasts();
  return (
    <>
      <div className={styles.headRow}>
        <p className={styles.who}>{headLine(info)}</p>
        {more}
      </div>
      <p className={styles.text}>{tierLine(info.tier)}</p>
      <div className={styles.actions}>
        <ButtonLink {...external(account.pages.tariffs, shell, toasts)} size="sm" variant="outline"><ExternalLink aria-hidden="true" size={14} />{accountWords.tariffs}</ButtonLink>
        {apps}
      </div>
      <div className={styles.card}>
        {info.questions ? <p className={styles.text}>{questionsLine(info.questions)}</p> : null}
        <p className={styles.text}>{readingLine(info.reading)}</p>
      </div>
    </>
  );
}
