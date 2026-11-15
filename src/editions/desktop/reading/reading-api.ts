// SPDX-License-Identifier: AGPL-3.0-only
import { useMutation, useQuery, useQueryClient, type QueryClient } from '@tanstack/react-query';
import { createContext, useContext } from 'react';
import { manuscriptQueryKeys } from '../../../entities/manuscript/api';
import { canonKeeperQueryKeys } from '../../../entities/session/api';
import type { CanonKeeperApiClient, OperationRequest, OperationResponse } from '../../../shared/api';

export type ReadingEstimate = OperationResponse<'getReadingEstimate', '200'>;
export type ReadingSample = OperationResponse<'getReadingSample', '200'>;
export type ConsentChange = OperationRequest<'setReadingConsent'>;

/** The operations of the reading of the books by «Свой ИИ» of the contract of the app (specs/2026-11-13-desk-consent.md). */
export type ReadingClient = Pick<CanonKeeperApiClient,
  'getReadingEstimate' | 'startReadingSample' | 'getReadingSample' | 'cancelReadingSample' | 'setReadingConsent'>;

const ReadingClientContext = createContext<ReadingClient | null>(null);

/** The client of the reading, given once over all the pages by the edition with its slot (`wrapPages`). */
export const ReadingClientProvider = ReadingClientContext.Provider;

/** The client of the reading; none outside the pages of the app — then nothing of the reading is asked or written. */
function useReadingClient() {
  return useContext(ReadingClientContext) ?? null;
}

function required(api: ReadingClient | null) {
  if (!api) throw new Error('The line of the reading needs the client of the API (ReadingClientProvider).');
  return api;
}

const readingKeys = {
  all: [...canonKeeperQueryKeys.all, 'desktop-reading'] as const,
  estimate: (projectId: string) => [...readingKeys.all, 'estimate', projectId] as const,
  sample: (projectId: string) => [...readingKeys.all, 'sample', projectId] as const,
};

function clientOf(meta: unknown) {
  const api = (meta as { readingApi?: ReadingClient } | undefined)?.readingApi;
  if (!api) throw new Error('The reading is missing the CanonKeeper API client metadata.');
  return api;
}

/** How often a sample that runs is asked again: the window and the line say how far it went (RW2.2). */
const samplePollMs = 1_500;

/** The estimate of the reading of the rest of a saga (getReadingEstimate): read while its window is open. */
export function useReadingEstimate(projectId: string, enabled: boolean) {
  const api = useReadingClient();
  return useQuery({
    queryKey: readingKeys.estimate(projectId),
    queryFn: ({ meta, signal }) => clientOf(meta).getReadingEstimate(projectId, { signal }),
    meta: { readingApi: api },
    enabled: enabled && projectId !== '' && api !== null,
    retry: false,
  });
}

/**
 * The sample of three chapters of a saga (getReadingSample): asked again while it waits or runs, so a window closed and opened
 * again shows how far it went (RW2.2). Its end has the estimate read again — after a sample it is «по пробе».
 */
export function useReadingSample(projectId: string, enabled: boolean) {
  const api = useReadingClient();
  return useQuery({
    queryKey: readingKeys.sample(projectId),
    queryFn: async ({ client, meta, signal }) => {
      const before = client.getQueryData<ReadingSample>(readingKeys.sample(projectId))?.state;
      const sample = await clientOf(meta).getReadingSample(projectId, { signal });
      if ((before === 'queued' || before === 'running') && sample.state !== 'queued' && sample.state !== 'running') {
        await client.invalidateQueries({ queryKey: readingKeys.estimate(projectId) });
      }
      return sample;
    },
    meta: { readingApi: api },
    enabled: enabled && projectId !== '' && api !== null,
    refetchInterval: (query) => (query.state.data?.state === 'queued' || query.state.data?.state === 'running' ? samplePollMs : false),
  });
}

/** The status of the saga and its estimate read again: what a write of the reading waits for before it is over (RW1.3). */
async function afterWrite(client: QueryClient, projectId: string) {
  await Promise.all([
    client.invalidateQueries({ queryKey: manuscriptQueryKeys.indexingStatus(projectId) }),
    client.invalidateQueries({ queryKey: readingKeys.estimate(projectId) }),
  ]);
}

/**
 * A write of the consent of a saga (setReadingConsent): «Прочитать всё», «Пауза», «Продолжить», «Продолжить ещё на X»,
 * «Изменить сумму…», «Повторить». It is over once the new status of the server is read: until then its button is busy, and
 * the line says what the server says, never what the page expects (RW1.3).
 */
export function useSetConsent(projectId: string) {
  const api = useReadingClient();
  const client = useQueryClient();
  return useMutation({
    mutationFn: async ({ change }: { change: ConsentChange; label: string }) => {
      const consent = await required(api).setReadingConsent(projectId, change);
      await afterWrite(client, projectId);
      return consent;
    },
  });
}

/** «Попробовать на 3 главах»: the sample starts with its key of a repeat; then it is asked again while it runs. */
export function useStartSample(projectId: string) {
  const api = useReadingClient();
  const client = useQueryClient();
  return useMutation({
    mutationFn: (idempotencyKey: string) => required(api).startReadingSample(projectId, { idempotencyKey }),
    onSuccess: (sample) => client.setQueryData(readingKeys.sample(projectId), sample),
  });
}

/** «Остановить» of a sample: it is read again — stopped, with the chapters it read. */
export function useCancelSample(projectId: string) {
  const api = useReadingClient();
  const client = useQueryClient();
  return useMutation({
    mutationFn: () => required(api).cancelReadingSample(projectId),
    onSettled: () => client.invalidateQueries({ queryKey: readingKeys.sample(projectId) }),
  });
}
