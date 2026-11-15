// SPDX-License-Identifier: AGPL-3.0-only
import { queryOptions, useMutation, useQuery, useQueryClient, type QueryClient } from '@tanstack/react-query';
import { manuscriptQueryKeys } from '../../../../entities/manuscript/api';
import { canonKeeperQueryKeys } from '../../../../entities/session/api';
import type { CanonKeeperApiClient, OperationRequest, OperationResponse } from '../../../../shared/api';

export type AiSettingsState = OperationResponse<'getAiSettings', '200'>;
export type AiCheck = OperationResponse<'getAiCheck', '200'>;
export type AiModels = OperationResponse<'listAiModels', '200'>;

/** The operations of «Свой ИИ» of the contract of the app (contracts/openapi.desktop.json). */
export type HelperClient = Pick<CanonKeeperApiClient,
  'getAiSettings' | 'updateAiSettings' | 'setAiKey' | 'deleteAiKey' | 'listAiModels' | 'startAiCheck' | 'getAiCheck' | 'cancelAiCheck'>;

const helperKeys = {
  all: [...canonKeeperQueryKeys.all, 'desktop-ai'] as const,
  settings: () => [...helperKeys.all, 'settings'] as const,
  models: (provider: string) => [...helperKeys.all, 'models', provider] as const,
  check: () => [...helperKeys.all, 'check'] as const,
};

function clientOf(meta: unknown) {
  const api = (meta as { helperApi?: HelperClient } | undefined)?.helperApi as HelperClient | undefined;
  if (!api) throw new Error('«Свой ИИ» is missing the CanonKeeper API client metadata.');
  return api;
}

/** How often the page asks again while a new window waits for the answer of the helper (GW2.3), and while a check runs (HW2.1). */
const appliesPollMs = 3_000;
const checkPollMs = 1_000;

/** The one query of the settings of the helper: the part, the window «Подключить помощника» and the lines of the edition read it. */
export const aiSettingsQueryKey = helperKeys.settings();

/** The settings of the helper as one query reads them (getAiSettings); a choice reads them before it writes. */
export function aiSettingsQueryOptions(api: Pick<HelperClient, 'getAiSettings'>) {
  return queryOptions({
    queryKey: aiSettingsQueryKey,
    queryFn: ({ meta, signal }) => clientOf(meta).getAiSettings({ signal }),
    meta: { helperApi: api },
  });
}

/**
 * The settings of the helper — the source, the profile of «Свой ИИ», whether the key is kept, where, the last check, the helper
 * of the account (getAiSettings; the key is never in it).
 */
export function useAiSettings(api: Pick<HelperClient, 'getAiSettings'>, enabled = true) {
  return useQuery({
    ...aiSettingsQueryOptions(api),
    enabled,
    refetchInterval: (query) => (query.state.data?.appliesAfterAnswer ? appliesPollMs : false),
  });
}

/**
 * The models of the provider the server keeps (listAiModels, specs/2026-11-13-desk-probes.md PB1): asked only when the form
 * shows that provider and its key is in place — `provider` names what is asked, so another one is asked anew. Kept nowhere.
 */
export function useAiModels(api: HelperClient, provider: string | null) {
  return useQuery({
    queryKey: helperKeys.models(provider ?? ''),
    queryFn: ({ meta, signal }) => clientOf(meta).listAiModels({ signal }),
    meta: { helperApi: api },
    enabled: provider !== null,
    retry: false,
    refetchOnWindowFocus: false,
  });
}

/**
 * «Проверить связь» of this run (getAiCheck): asked again every second while it runs, so a page left and opened again shows
 * how far it went (HW2.1). Its end has the settings read again before it is shown — they keep the report with the verdicts
 * now, so the report of the check before never stands in its place.
 */
export function useAiCheck(api: HelperClient) {
  return useQuery({
    queryKey: helperKeys.check(),
    queryFn: async ({ client, meta, signal }) => {
      const before = client.getQueryData<AiCheck>(helperKeys.check())?.state;
      const check = await clientOf(meta).getAiCheck({ signal });
      if (before === 'running' && check.state !== 'running') await client.invalidateQueries({ queryKey: helperKeys.settings() });
      return check;
    },
    meta: { helperApi: api },
    refetchInterval: (query) => (query.state.data?.state === 'running' ? checkPollMs : false),
  });
}

/**
 * What a write of «Свой ИИ» changes beyond the part: whether the helper is connected — the line of the reading of every
 * saga and the field of the chat read it from getIndexingStatus (HW3.2) — and the list of models of the provider.
 */
function afterWrite(client: QueryClient) {
  void client.invalidateQueries({ queryKey: [...manuscriptQueryKeys.all, 'indexing-status'] });
  void client.invalidateQueries({ queryKey: [...helperKeys.all, 'models'] });
}

/** «Сохранить»: the settings whole; the answer is the state the part shows next. */
export function useSaveSettings(api: HelperClient) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (change: OperationRequest<'updateAiSettings'>) => api.updateAiSettings(change),
    onSuccess: (state) => {
      client.setQueryData(helperKeys.settings(), state);
      afterWrite(client);
    },
  });
}

/** The settings and the key were written: what they say is read again from the server. */
export function useAfterKey() {
  const client = useQueryClient();
  return () => {
    void client.invalidateQueries({ queryKey: helperKeys.settings() });
    afterWrite(client);
  };
}

/** «Проверить связь» started: its first state is the check of this run; then it is asked again while it runs. */
export function useStartCheck() {
  const client = useQueryClient();
  return (check: AiCheck) => client.setQueryData(helperKeys.check(), check);
}

/** «Остановить»: the check is read again — canceled — and the kept report stays as it was. */
export function useCancelCheck(api: HelperClient) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: () => api.cancelAiCheck(),
    onSettled: () => client.invalidateQueries({ queryKey: helperKeys.check() }),
  });
}
