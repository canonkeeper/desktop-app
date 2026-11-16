// SPDX-License-Identifier: AGPL-3.0-only
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { RotateCcw } from 'lucide-react';
import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode, type RefObject } from 'react';
import { isApiStatusFailure, isNetworkFailure, loadFailureWords } from '../../../entities/api-errors';
import { manuscriptQueryKeys } from '../../../entities/manuscript/api';
import type { CanonKeeperApiClient } from '../../../shared/api';
import { desktopShell } from '../../../shared/desktop/shell';
import type { RouteNavigate } from '../../../shared/navigation/app-route';
import { Button } from '../../../ui/controls/Button';
import { useWriteToUsAction } from '../../../ui/feedback/SupportReference';
import { useToasts, type ToastInput } from '../../../ui/feedback/useToastQueue';
import { accountQueryKey, useDesktopAccount, type DesktopAccount } from '../account/account-api';
import { OpenedDialog } from './SagaOpened';
import { siteTransferKey, useSiteSagaTransfer, type SiteSagaTransfer, type TransferClient } from './transfer-api';
import { transferRefusals, transferWords as words } from './transfer-words';
import { TransferWindow } from './TransferWindow';

/** What the pages of the app need of the transfer: the client of its operations and of the link it needs. */
export type SiteTransferClient = TransferClient & Pick<CanonKeeperApiClient, 'getAccount'>;

/** The transfer of a saga from the site as its entries and its line see it. */
export interface SiteTransferControl {
  /** The transfer as the backend holds it; undefined until it is read. */
  transfer: SiteSagaTransfer | undefined;
  /** The link of the app with an account: only a linked app transfers (TR2.1); undefined until it is read. */
  account: DesktopAccount | undefined;
  /**
   * «Перенести с сайта…» of every entry: one start, the page of the site in the browser and the window of the waiting; while a
   * transfer waits, its window again — with «Открыть страницу снова» and «Отменить» — and no new start.
   */
  start: () => void;
  starting: boolean;
  /** «Остановить» of the line of the work and «Отменить» of the window of the waiting: the transfer they show, by its id. */
  stop: () => void;
  stopping: boolean;
  /**
   * The line of the work under the top bar: the window of the waiting that closes because the saga began to come gives the
   * focus to it — the entry the author pressed may stand in a menu that is gone.
   */
  line: RefObject<HTMLDivElement | null>;
}

const SiteTransferContext = createContext<SiteTransferControl | null>(null);

/** The transfer of the edition; null outside the app (the site, a test without it): its entries are not shown then. */
export function useSiteTransfer() {
  return useContext(SiteTransferContext);
}

function codeOf(error: unknown) {
  return isApiStatusFailure(error) ? error.problem.code ?? null : null;
}

interface SiteTransferProviderProps {
  api: SiteTransferClient;
  navigate: RouteNavigate;
  children?: ReactNode;
}

/**
 * The transfer of a saga from the site over all the pages of the app (specs/2026-11-16-desk-site-saga.md TR6): the one reader
 * of `getSiteSagaTransfer` (TR6.3), the one start — the page /transfer of the site in the browser of the system and the window
 * of the waiting over the page it was started from —, the stop, and the end said once a transfer, on whatever page the author
 * is: «Сага открыта» by the window of a saga file, a failure by one line of the one area of the toasts in the words of its code
 * (TR6.2). An end said is put away on the backend (`cancelSiteSagaTransfer` of a final phase, by its id — a transfer started
 * meanwhile stays): a page opened later says nothing of it. Nothing of the transfer is the page's: which window is open and
 * which ends were said are facts of this screen only.
 */
export function SiteTransferProvider({ api, children, navigate }: SiteTransferProviderProps) {
  const client = useQueryClient();
  const toasts = useToasts();
  const writeToUs = useWriteToUsAction();
  const transfer = useSiteSagaTransfer(api).data;
  const account = useDesktopAccount(api).data;
  // The transfer whose window of the waiting is open: × hides it, a new start opens it for the new one.
  const [shownFor, setShownFor] = useState<string | null>(null);
  // The ends of this screen already said: an answer read again does not say them twice.
  const said = useRef(new Set<string>());
  const [closed, setClosed] = useState<ReadonlySet<string>>(() => new Set());
  const line = useRef<HTMLDivElement>(null);

  const putAway = useCallback((id: string) => {
    void api.cancelSiteSagaTransfer({ id }).then((next) => client.setQueryData(siteTransferKey, next), () => undefined);
  }, [api, client]);

  const openPage = useCallback((address: string) => {
    void desktopShell()?.openExternal(address).catch(() => toasts.push({ message: 'Не удалось открыть ссылку', tone: 'error' }));
  }, [toasts]);

  const { isPending: starting, mutate: startTransfer } = useMutation({ mutationFn: () => api.startSiteSagaTransfer() });
  const { isPending: stopping, mutate: stopTransfer } = useMutation({ mutationFn: (id: string) => api.cancelSiteSagaTransfer({ id }) });

  function startRefusal(error: unknown, retry: () => void): ToastInput {
    const code = codeOf(error);
    if (code === 'desktop.updating') return { message: words.updating, tone: 'warning' };
    if (code === 'saga_transfer.running') {
      void client.invalidateQueries({ queryKey: siteTransferKey });
      return { message: words.goingReason, tone: 'warning' };
    }
    if (code === 'account.not_linked') {
      void client.invalidateQueries({ queryKey: accountQueryKey });
      return { message: words.needLink, tone: 'warning' };
    }
    const { cause, code: supportCode } = loadFailureWords(error);
    if (isNetworkFailure(error)) {
      return {
        message: words.startFailed, detail: cause, tone: 'error',
        action: <Button onClick={retry} size="sm" variant="ghost"><RotateCcw aria-hidden="true" size={14} />Повторить</Button>,
      };
    }
    return {
      message: words.startFailed, tone: 'error',
      detail: [cause, supportCode ? `Код для поддержки: ${supportCode}` : ''].filter(Boolean).join(' '),
      action: writeToUs({ topic: 'bug', ...(supportCode ? { code: supportCode } : {}) }),
    };
  }

  function start() {
    if (starting) return;
    // A transfer that waits for the site is shown again, not started anew: its page is open, its term goes on (the decision
    // of the pages, 16.11).
    if (transfer?.phase === 'waiting' && transfer.id) {
      setShownFor(transfer.id);
      return;
    }
    startTransfer(undefined, {
      onSuccess: (next) => {
        client.setQueryData(siteTransferKey, next);
        if (next.phase !== 'waiting' || !next.id) return;
        setShownFor(next.id);
        if (next.address) openPage(next.address);
      },
      onError: (error) => toasts.push(startRefusal(error, start)),
    });
  }

  function stop() {
    if (!transfer?.id) return;
    stopTransfer(transfer.id, {
      onSuccess: (next) => client.setQueryData(siteTransferKey, next),
      onError: (error) => {
        void client.invalidateQueries({ queryKey: siteTransferKey });
        toasts.push(codeOf(error) === 'saga_transfer.opening'
          ? { message: words.openingReason, tone: 'warning' }
          : { message: words.stopFailed, tone: 'error' });
      },
    });
  }

  // The end of a transfer, once: a failure in its words — the author's own stop says nothing — and put away; the new saga of
  // one that is done in the list of sagas at once, its window until the author closes it.
  useEffect(() => {
    if (!transfer?.id || said.current.has(transfer.id)) return;
    if (transfer.phase === 'done') {
      said.current.add(transfer.id);
      void client.invalidateQueries({ queryKey: manuscriptQueryKeys.all });
      return;
    }
    if (transfer.phase !== 'failed') return;
    said.current.add(transfer.id);
    const refusal = transfer.code ? transferRefusals[transfer.code] : { message: words.failed, write: true as const };
    if (refusal) {
      toasts.push({ message: refusal.message, tone: 'error', ...(refusal.write ? { action: writeToUs({ topic: 'bug' }) } : {}) });
    }
    putAway(transfer.id);
  }, [client, putAway, toasts, transfer, writeToUs]);

  const value: SiteTransferControl = { transfer, account, start, starting, stop, stopping, line };
  const waiting = transfer?.phase === 'waiting' && transfer.id === shownFor ? transfer : null;
  const done = transfer?.phase === 'done' && transfer.id && transfer.opened && !closed.has(transfer.id) ? transfer : null;
  return (
    <SiteTransferContext.Provider value={value}>
      {children}
      {waiting ? (
        <TransferWindow
          onCancel={() => {
            setShownFor(null);
            stop();
          }}
          onHide={() => setShownFor(null)}
          returnFocusRef={line}
          transfer={waiting}
        />
      ) : null}
      {done ? (
        <OpenedDialog
          from="site"
          navigate={navigate}
          onClose={() => {
            setClosed(new Set([...closed, done.id!]));
            putAway(done.id!);
          }}
          opened={done.opened!}
        />
      ) : null}
    </SiteTransferContext.Provider>
  );
}
