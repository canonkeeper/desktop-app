// SPDX-License-Identifier: AGPL-3.0-only
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { isApiStatusFailure } from '../../../entities/api-errors';
import { canonKeeperQueryKeys } from '../../../entities/session/api';
import type { CanonKeeperApiClient } from '../../../shared/api';
import { useToasts } from '../../../ui/feedback/useToastQueue';
import { forgetHiddenFailures } from './hidden-strips';
import { updateWords, type AppUpdate } from './update-words';

type UpdateClient = Pick<CanonKeeperApiClient, 'getAppUpdate'>;
export type UpdateActionsClient = UpdateClient & Pick<CanonKeeperApiClient, 'checkAppUpdate' | 'installAppUpdate' | 'cancelAppUpdate' | 'updateAppUpdateSettings'>;

/** The one query of the update (specs/2026-11-15-desk-updates-web.md UW2.4): the part, the strip and «Спросить» read it. */
export const appUpdateKey = [...canonKeeperQueryKeys.all, 'app-update'] as const;

/** How often the update is read: every second while a check, a download, the wait or the installation goes, else a minute. */
export const updatePollMs = { going: 1_000, calm: 60_000 } as const;
const goingPhases: ReadonlySet<AppUpdate['phase']> = new Set(['checking', 'downloading', 'waiting_turn', 'installing']);

function updateApi(meta: unknown) {
  const api = (meta as { canonKeeperApi?: UpdateClient } | undefined)?.canonKeeperApi;
  if (!api) throw new Error('The update of the app is missing the CanonKeeper API client metadata.');
  return api;
}

/**
 * The update of the app as the backend holds it (UW2.4). One reader follows it — the edition around the pages, by the
 * times of `updatePollMs`; any other reader takes the same answer and reads nothing on its own, except the part
 * «Обновления», which reads it again when the author enters it (`enter`).
 */
export function useAppUpdate(api: UpdateClient, { follow = false, enter = false }: { follow?: boolean; enter?: boolean } = {}) {
  return useQuery({
    queryKey: appUpdateKey,
    queryFn: ({ meta, signal }) => updateApi(meta).getAppUpdate({ signal }),
    meta: { canonKeeperApi: api },
    refetchInterval: follow ? (query) => (goingPhases.has(query.state.data?.phase as AppUpdate['phase']) ? updatePollMs.going : updatePollMs.calm) : false,
    refetchOnWindowFocus: follow,
    refetchOnReconnect: follow,
    refetchOnMount: enter ? 'always' : follow,
    staleTime: follow ? undefined : Infinity,
  });
}

/** The backend refused the action for the phase it is in: the page reads the phase again and says nothing (UW2.4). */
function phaseMoved(error: unknown) {
  return isApiStatusFailure(error) && error.status === 409 && error.problem.code === 'desktop.update_state';
}

/**
 * The actions of the part and the strip (UW2.4): each answer of the backend is the update itself and goes into the one
 * query; 409 reads the phase again without words; any other refusal is the one line «Не получилось — попробуйте ещё раз»,
 * and the switch keeps showing the value of the server. A retry and a check the backend took are a new attempt: the
 * failures the author hid show again when they come back (UW3.2, UW3.5).
 */
export function useUpdateActions(api: UpdateActionsClient) {
  const queryClient = useQueryClient();
  const toasts = useToasts();
  const options = {
    onSuccess: (update: AppUpdate) => queryClient.setQueryData(appUpdateKey, update),
    onError: (error: unknown) => {
      if (phaseMoved(error)) {
        void queryClient.invalidateQueries({ queryKey: appUpdateKey });
        return;
      }
      toasts.push({ message: updateWords.failed, tone: 'error' });
    },
  };
  const attempt = {
    ...options,
    onSuccess: (update: AppUpdate) => {
      forgetHiddenFailures();
      options.onSuccess(update);
    },
  };
  const check = useMutation({ mutationFn: () => api.checkAppUpdate(), ...attempt });
  const install = useMutation({ mutationFn: () => api.installAppUpdate(), ...attempt });
  const cancel = useMutation({ mutationFn: () => api.cancelAppUpdate(), ...options });
  const daily = useMutation({ mutationFn: (value: boolean) => api.updateAppUpdateSettings({ daily: value }), ...options });
  return { check, install, cancel, daily };
}
