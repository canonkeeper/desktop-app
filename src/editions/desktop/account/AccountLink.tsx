// SPDX-License-Identifier: AGPL-3.0-only
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { RotateCcw, UserRoundCheck } from 'lucide-react';
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { isApiStatusFailure, publicApiErrorMessage } from '../../../entities/api-errors';
import type { CanonKeeperApiClient } from '../../../shared/api';
import type { AppRoute } from '../../../shared/navigation/app-route';
import { desktopShell } from '../../../shared/desktop/shell';
import { Button } from '../../../ui/controls/Button';
import { useToasts } from '../../../ui/feedback/useToastQueue';
import { atHelperPart, chooseWords, choiceRefusal, focusSourceChoice, useChooseHelper } from '../settings/helper/choose-helper';
import { aiSettingsQueryOptions } from '../settings/helper/helper-api';
import { accountQueryKey, accountQueryOptions, afterLinkChange, useDesktopAccount, type DesktopAccount } from './account-api';
import { accountWords, failureLine, linkedLine } from './account-words';
import { LinkWindow } from './LinkWindow';

/** What the pages need of the link of the app: the client of its operations and the edition's way to start and show it. */
export type AccountLinkClient = Pick<CanonKeeperApiClient,
  'getAccount' | 'startAccountLink' | 'cancelAccountLink' | 'unlinkAccount' | 'getAiSettings' | 'updateAiSettings'>;

export interface AccountLink {
  api: AccountLinkClient;
  /**
   * «Связать с аккаунтом», «Связать снова» (AW1.1): one start from every place — with the intent to choose the helper of the
   * account from the window «Подключить помощника» and the choice of «Помощник» (AW3.2). `shown` runs once the window of the
   * waiting opens: a window the link was started from closes, and one window stands over the page.
   */
  start: (chooseHelper: boolean, shown?: () => void) => void;
  starting: boolean;
  /** The build cannot be linked: the server said so to a start (409 account.link_unsupported, AW2.1). */
  unsupported: boolean;
  /** «Открыть окно связи»: the window of the waiting over the page again (AW1.2). */
  openWindow: () => void;
}

const AccountLinkContext = createContext<AccountLink | null>(null);

/**
 * «Связать снова» where the lost link of the helper of the account shows it — the block of a refused question, a failed turn,
 * the line of the reading and of the preparation, the window of the consent (decision of the coordinator 15.11): one start in
 * every place, with the intent to bring the helper of the account back (AW3.2).
 */
export function relinkHelper(link: Pick<AccountLink, 'start'>, shown?: () => void) {
  link.start(true, shown);
}

/** The link of the edition; null outside the app (the site, a test without it). */
export function useAccountLink() {
  return useContext(AccountLinkContext);
}

function codeOf(error: unknown) {
  return isApiStatusFailure(error) ? error.problem.code ?? null : null;
}

/**
 * The link of the app with an account over all its pages (specs/2026-11-15-desk-account-link-web.md AW1): the one start —
 * the address of /link in the browser of the system and the window of the waiting over the page it was started from —, the
 * window itself, and the observer of the end of the waiting, which says it in the one area of the toasts on whatever page the
 * author is. Nothing of the link is the page's: what it shows is the answer of getAccount, read again while the link waits;
 * the window open and whether the browser opened are facts of this screen only. The address the author stands at decides what
 * «Выбрать помощника CanonKeeper» of the line of the end does (AW3.2).
 */
export function AccountLinkProvider({ api, children, route }: { api: AccountLinkClient; children?: ReactNode; route?: AppRoute }) {
  const client = useQueryClient();
  const toasts = useToasts();
  const account = useDesktopAccount(api);
  // Read at the press of the line of the end, which may come on another page than the end.
  const helperPart = useRef(atHelperPart(route));
  useEffect(() => {
    helperPart.current = atHelperPart(route);
  }, [route]);
  const standsAtHelperPart = useCallback(() => helperPart.current, []);
  const [open, setOpen] = useState(false);
  // The shell refused to open the browser, or there is none: «Браузер не открылся…» stands at once (AW1.1).
  const [browserFailed, setBrowserFailed] = useState(() => desktopShell() === null);

  const openBrowser = useCallback((address: string) => {
    const shell = desktopShell();
    if (!shell) {
      setBrowserFailed(true);
      return;
    }
    setBrowserFailed(false);
    void shell.openExternal(address).catch(() => setBrowserFailed(true));
  }, []);

  const { error: startError, isPending: starting, mutate: startLink } = useMutation({
    mutationFn: (chooseHelper: boolean) => api.startAccountLink({ chooseHelper }),
  });
  const start = useCallback((chooseHelper: boolean, shown?: () => void) => {
    if (starting) return;
    startLink(chooseHelper, {
      onSuccess: (state) => {
        client.setQueryData(accountQueryKey, state);
        afterLinkChange(client);
        setOpen(true);
        shown?.();
        if (state.link) openBrowser(state.link.address);
      },
      onError: (error) => {
        const code = codeOf(error);
        // A link waits already, or the app is linked: what getAccount says is shown — its window, or the linked part (AW1.2).
        if (code === 'account.link_unavailable') {
          void client.fetchQuery({ ...accountQueryOptions(api), staleTime: 0 })
            .then((state) => {
              if (state.state !== 'waiting') return;
              setOpen(true);
              shown?.();
            }, () => undefined);
          return;
        }
        // The places of «Связать…» say it in its place (AW2.1).
        if (code === 'account.link_unsupported') return;
        toasts.push({ message: accountWords.startFailed, detail: publicApiErrorMessage(error, ''), tone: 'error' });
      },
    });
  }, [api, client, openBrowser, startLink, starting, toasts]);

  const unsupported = codeOf(startError) === 'account.link_unsupported';
  const value = useMemo<AccountLink>(() => ({
    api, start, starting, unsupported, openWindow: () => setOpen(true),
  }), [api, start, starting, unsupported]);

  const waiting = account.data?.state === 'waiting' ? account.data : null;
  return (
    <AccountLinkContext.Provider value={value}>
      {children}
      {open && waiting ? (
        <LinkWindow
          account={waiting}
          browserFailed={browserFailed}
          onCancel={() => {
            setOpen(false);
            void api.cancelAccountLink()
              .catch(() => { toasts.push({ message: accountWords.actionFailed, tone: 'error' }); })
              .finally(() => {
                void client.invalidateQueries({ queryKey: accountQueryKey });
                afterLinkChange(client);
              });
          }}
          onHide={() => setOpen(false)}
          onOpenAgain={() => openBrowser(waiting.link!.address)}
        />
      ) : null}
      <LinkOutcome account={account.data} api={api} atHelperPart={standsAtHelperPart} onRelink={() => start(false)} />
    </AccountLinkContext.Provider>
  );
}

/**
 * The end of a waiting in the one area of the toasts, on any page (AW1.3): a link made — the address of the account with its
 * mask and who answers now, by the settings the server chose (AW3.2), with «Выбрать помощника CanonKeeper» when another one
 * answers — on «Настройки → Помощник» it brings the author to the choice of the part, which asks its own «Не сохранять?» before
 * unsaved edits of «Свой ИИ» go; a link that failed — why, with «Связать снова». The observer sees the answers of the one query
 * change from `waiting`: a page opened after the end says nothing.
 */
function LinkOutcome({ account, api, atHelperPart: standsAtHelperPart, onRelink }: {
  account: DesktopAccount | undefined;
  api: AccountLinkClient;
  atHelperPart: () => boolean;
  onRelink: () => void;
}) {
  const client = useQueryClient();
  const toasts = useToasts();
  const choose = useChooseHelper(api);
  const before = useRef<DesktopAccount['state'] | undefined>(undefined);

  useEffect(() => {
    const was = before.current;
    before.current = account?.state;
    if (was !== 'waiting' || !account || account.state === 'waiting') return;
    // Any end changes where the app goes in the network (AW3.6) and what the pages say of the helper.
    afterLinkChange(client);
    if (account.state === 'failed' && account.failure) {
      const id = toasts.push({
        message: failureLine(account.failure, account.passStore), tone: 'error',
        action: <Button onClick={() => { toasts.dismiss(id); onRelink(); }} size="sm" variant="ghost"><RotateCcw aria-hidden="true" size={14} />{accountWords.relink}</Button>,
      });
      return;
    }
    if (account.state !== 'linked') return;
    const email = account.account?.email ?? null;
    void client.fetchQuery({ ...aiSettingsQueryOptions(api), staleTime: 0 }).then((state) => {
      const source = state.settings.source;
      const pick = () => choose.mutate('account', {
        onError: (error) => {
          if (choiceRefusal(error) !== 'ai.account_unreachable') {
            toasts.push({ message: accountWords.actionFailed, tone: 'error' });
            return;
          }
          toasts.push({ message: chooseWords.unreachable, tone: 'error', action: <Button onClick={pick} size="sm" variant="ghost">{chooseWords.retry}</Button> });
        },
      });
      const id = toasts.push({
        message: linkedLine(email, source), tone: 'success',
        ...(source === 'account' ? {} : {
          action: (
            <Button
              onClick={() => {
                toasts.dismiss(id);
                if (standsAtHelperPart()) focusSourceChoice();
                else pick();
              }}
              size="sm"
              variant="ghost"
            >
              <UserRoundCheck aria-hidden="true" size={14} />{accountWords.chooseHelper}
            </Button>
          ),
        }),
      });
    }, () => toasts.push({ message: linkedLine(email, 'none'), tone: 'success' }));
  }, [account, api, choose, client, onRelink, standsAtHelperPart, toasts]);
  return null;
}
