// SPDX-License-Identifier: AGPL-3.0-only
import { useQuery } from '@tanstack/react-query';
import type { ReactNode } from 'react';
import type { CanonKeeperApiClient } from '../../../shared/api';
import { desktopStateQueryKey, stateApi } from '../settings/desktop-state';

type WelcomeClient = Pick<CanonKeeperApiClient, 'getDesktopState'>;

/**
 * Whether «Добро пожаловать» stands (specs/2026-11-15-desk-updates-web.md UW1.1, UW1.4): the one sign is `welcome` of
 * `getDesktopState`. For the window the state is read once a load of the page — not again when the window gets the focus
 * back and not by a poll: the read walks the data directory. Its write reads it again.
 */
export function useWelcomeState(api: WelcomeClient) {
  return useQuery({
    queryKey: desktopStateQueryKey,
    queryFn: ({ meta, signal }) => stateApi(meta).getDesktopState({ signal }),
    meta: { canonKeeperApi: api },
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    refetchOnMount: false,
    staleTime: Infinity,
  });
}

/**
 * The lines and windows of the edition that open by themselves — the notes of a launch, «Что нового», «Сага открыта» — wait
 * until «Добро пожаловать» is closed (UW1.4); while its state is read they wait too. A read that failed shows no window,
 * and they do not wait for it.
 */
export function AfterWelcome({ api, children }: { api: WelcomeClient; children?: ReactNode }) {
  const state = useWelcomeState(api);
  if (state.isPending || state.data?.welcome === true) return null;
  return <>{children}</>;
}
