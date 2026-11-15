import { useMutation, useMutationState, useQuery } from '@tanstack/react-query';
import { RotateCcw } from 'lucide-react';
import type { CanonKeeperApiClient, OperationResponse, Schema } from '../../shared/api';
import { Button } from '../../ui/controls/Button';
import { useToasts } from '../../ui/feedback/useToastQueue';
import { saveFile } from '../../ui/save-file';
import { isApiStatusFailure } from '../api-errors';

/**
 * The saga file of the pages (specs/2026-11-13-desk-sagafile-web.md; the format and the operations —
 * specs/2026-11-13-desk-sagafile.md SF1–SF3): «Сохранить сагу в файл» of the app and «Скачать сагу файлом» of the site save
 * one file of a saga by the same operation; the app opens such a file as a new saga.
 */
export type SagaFileClient = Pick<CanonKeeperApiClient, 'getSagaArchive'>;
export type SagaArchiveOpened = OperationResponse<'openSagaArchive', '201'>;
export type SagaArchiveReport = Schema<'SagaArchiveReport'>;
/** The chats a saga file brought into the saga, to read (specs/2026-11-13-desk-sagafile.md SF3.8). */
export type SagaTranscriptClient = Pick<CanonKeeperApiClient, 'listSagaTranscripts' | 'getSagaTranscript'>;
export type SagaTranscriptSummary = Schema<'SagaTranscriptSummary'>;
export type SagaTranscriptTurn = Schema<'SagaTranscriptTurn'>;

/**
 * The work of a saga file lives in the cache of the mutations, not in a page (SW1.2): a page that comes back sees the save
 * that goes on and can stop it, and its line of the end comes to the one area of the toasts wherever the author is.
 */
export const sagaFileKeys = {
  save: ['canonkeeper', 'saga-file', 'save'] as const,
  open: ['canonkeeper', 'saga-file', 'open'] as const,
  /** Under the product root: a change of the author drops them. */
  transcripts: (projectId: string) => ['canonkeeper', 'saga-file', 'transcripts', projectId] as const,
  transcript: (projectId: string, transcriptId: string) => [...sagaFileKeys.transcripts(projectId), transcriptId] as const,
};

function transcriptsApi(meta: unknown) {
  const api = (meta as { transcripts?: SagaTranscriptClient } | undefined)?.transcripts;
  if (!api) throw new Error('The chats of a saga file are read without the client of the API.');
  return api;
}

/** The chats a saga file brought into the saga, the newest first (SW3.1); a saga without them answers an empty list. */
export function useSagaTranscriptsQuery(api: SagaTranscriptClient, projectId: string, enabled = true) {
  return useQuery({
    queryKey: sagaFileKeys.transcripts(projectId),
    queryFn: ({ meta, signal }) => transcriptsApi(meta).listSagaTranscripts(projectId, { signal }),
    meta: { transcripts: api },
    enabled: enabled && projectId.length > 0,
    staleTime: 60_000,
  });
}

/** One chat of a saga file, with the places of the chapters its answers cite as the saga names them now (SW3.2). */
export function useSagaTranscriptQuery(api: SagaTranscriptClient, projectId: string, transcriptId: string) {
  return useQuery({
    queryKey: sagaFileKeys.transcript(projectId, transcriptId),
    queryFn: ({ meta, signal }) => transcriptsApi(meta).getSagaTranscript(projectId, transcriptId, { signal }),
    meta: { transcripts: api },
    enabled: projectId.length > 0 && transcriptId.length > 0,
  });
}

/** One save: the saga, and the hand that breaks its request off — «Остановить». */
export interface SagaFileSave {
  projectId: string;
  title: string;
  stop: AbortController;
}

/** The words of a save, by the edition: the app saves a saga into a file, the site downloads it (boards 44·30, 44·33). */
export interface SagaFileSaveWords {
  /** The line of the end with the name the server gave the file. */
  done: (fileName: string) => string;
  /** The line of a refusal or of a broken file, with «Повторить». */
  failed: string;
  /** The answer 429: a save of this author goes on already. */
  busy: string;
}

/**
 * Saves a saga into its file (SW1.2, SW1.3, SW4.2): the file goes to the browser as the file of a book does, the name is
 * the server's. «Остановить» breaks the request off and says nothing — the author sees the line go; a refusal and a file
 * broken on the way say so with «Повторить», which asks the same saga again.
 */
export function useSagaFileSave(api: SagaFileClient, words: SagaFileSaveWords) {
  const toasts = useToasts();
  const mutation = useMutation({
    mutationKey: sagaFileKeys.save,
    mutationFn: async ({ projectId, stop }: SagaFileSave) => {
      const file = await api.getSagaArchive(projectId, { signal: stop.signal });
      saveFile(file);
      return file.fileName;
    },
    onSuccess: (fileName) => toasts.push({ message: words.done(fileName), tone: 'success' }),
    onError: (error, save) => {
      if (save.stop.signal.aborted) return;
      if (isApiStatusFailure(error) && error.status === 429) {
        toasts.push({ message: words.busy, tone: 'warning' });
        return;
      }
      toasts.push({
        message: words.failed, tone: 'error',
        action: <Button onClick={() => start(save)} size="sm" variant="ghost"><RotateCcw aria-hidden="true" size={14} />Повторить</Button>,
      });
    },
  });
  const going = useMutationState({
    filters: { mutationKey: sagaFileKeys.save, status: 'pending' },
    select: (pending) => pending.state.variables as SagaFileSave,
  });
  const saving = going[0] ?? null;
  function start(saga: { projectId: string; title: string }) {
    mutation.mutate({ projectId: saga.projectId, title: saga.title, stop: new AbortController() });
  }
  return {
    /** The save that goes on, from any page; null when none. */
    saving,
    start,
    stop: () => saving?.stop.abort(),
  };
}
