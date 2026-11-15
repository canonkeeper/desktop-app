// SPDX-License-Identifier: AGPL-3.0-only
import { focusManager, type InfiniteData, type QueryClient, useInfiniteQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { useEffect } from 'react';
import { canonKeeperQueryKeys } from '../../../../entities/session/api';
import type { CanonKeeperApiClient, OperationResponse } from '../../../../shared/api';
import { saveFile } from '../../../../ui/save-file';
import type { SendLogKind } from './log-words';

type LogClient = Pick<CanonKeeperApiClient, 'listSendLog' | 'listSendLogSendings' | 'getSendLogFile' | 'clearSendLog'>;

/** The zone of time of the computer of the author: the days of the rows are its days (JR2.1). */
export function zoneOfTheComputer(): string {
  return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
}

/** How often the open part asks the first page again (JR3.1). */
const rereadMs = 30_000;

export const sendLogKey = [...canonKeeperQueryKeys.all, 'send-log'] as const;

function client(meta: unknown) {
  const api = (meta as { canonKeeperApi?: LogClient } | undefined)?.canonKeeperApi;
  if (!api) throw new Error('The journal is missing the CanonKeeper API client metadata.');
  return api;
}

type Page = OperationResponse<'listSendLog', '200'>;

/**
 * The first page read again into the rows shown (JR3.1): its rows take the place of the first page; the rows newer ones
 * pushed out of it stay above the pages «Показать ещё» loaded, which keep their cursor — those pages are not read again.
 */
export async function rereadFirstPage(queries: QueryClient, key: readonly unknown[], read: () => Promise<Page>) {
  const fresh = await read();
  queries.setQueryData<InfiniteData<Page, string>>(key, (data) => {
    if (!data) return data;
    const [first, ...rest] = data.pages;
    if (!first || rest.length === 0) return { pages: [fresh], pageParams: [''] };
    const shown = new Set(fresh.rows.map((row) => row.key));
    const pushed = first.rows.filter((row) => !shown.has(row.key));
    return { ...data, pages: [{ ...fresh, rows: [...fresh.rows, ...pushed], next: first.next }, ...rest] };
  });
}

/**
 * The rows of the journal of a choice, page after page (JR3.1): while the part is open, the first page is read again every
 * 30 s, on the return to the window and when the part opens again — the pages «Показать ещё» loaded are not. A read again
 * that failed leaves the rows as they were.
 */
export function useSendLog(api: LogClient, kind: SendLogKind | null, zone: string) {
  const queries = useQueryClient();
  const key = [...sendLogKey, 'rows', kind ?? 'all', zone] as const;
  const query = useInfiniteQuery({
    queryKey: key,
    queryFn: ({ meta, pageParam, signal }) => client(meta).listSendLog({ zone, ...(kind ? { kind } : {}), ...(pageParam ? { cursor: pageParam } : {}) }, { signal }),
    initialPageParam: '',
    getNextPageParam: (page) => page.next ?? undefined,
    meta: { canonKeeperApi: api },
    staleTime: 0,
    refetchOnMount: false,
    refetchOnWindowFocus: false,
  });
  useEffect(() => {
    const reread = () => {
      if (!queries.getQueryData(key)) return;
      rereadFirstPage(queries, key, () => api.listSendLog({ zone, ...(kind ? { kind } : {}) })).catch(() => undefined);
    };
    reread();
    const timer = window.setInterval(reread, rereadMs);
    const unsubscribe = focusManager.subscribe((focused) => {
      if (focused) reread();
    });
    return () => {
      window.clearInterval(timer);
      unsubscribe();
    };
  }, [api, kind, zone, queries]);
  return query;
}

/**
 * The sendings of a row, page after page, while it is open (JR3.2): a row that goes on reads them again when the count of
 * its sendings changes, so the head of the row and its list say the same.
 */
export function useSendLogSendings(api: LogClient, row: Pick<Page['rows'][number], 'key' | 'sendings'>, zone: string) {
  const query = useInfiniteQuery({
    queryKey: [...sendLogKey, 'sendings', row.key, zone],
    queryFn: ({ meta, pageParam, signal }) => client(meta).listSendLogSendings({ row: row.key, zone, ...(pageParam ? { cursor: pageParam } : {}) }, { signal }),
    initialPageParam: '',
    getNextPageParam: (page) => page.next ?? undefined,
    meta: { canonKeeperApi: api },
    staleTime: 0,
  });
  const { refetch, isSuccess } = query;
  const shown = query.data?.pages.reduce((count, page) => count + page.sendings.length, 0) ?? 0;
  const complete = isSuccess && !query.hasNextPage;
  useEffect(() => {
    if (complete && shown !== row.sendings) void refetch();
  }, [complete, shown, row.sendings, refetch]);
  return query;
}

/** «Сохранить в файл…» (JR3.3): the text of the server saved by the saving of a file of the window. */
export function useSaveSendLog(api: LogClient, kind: SendLogKind | null, zone: string) {
  return useMutation({
    mutationFn: async () => {
      const file = await api.getSendLogFile({ zone, ...(kind ? { kind } : {}) });
      saveFile({ blob: new Blob([file.text], { type: 'text/plain;charset=utf-8' }), fileName: file.fileName });
      return file.fileName;
    },
  });
}

/** «Очистить журнал» (JR2.6): one operation, and the rows are read again. */
export function useClearSendLog(api: LogClient) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: () => api.clearSendLog(),
    onSuccess: () => queries.invalidateQueries({ queryKey: sendLogKey }),
  });
}
