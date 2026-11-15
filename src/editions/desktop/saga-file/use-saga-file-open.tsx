// SPDX-License-Identifier: AGPL-3.0-only
import { useMutation, useMutationState, useQueryClient } from '@tanstack/react-query';
import { RotateCcw } from 'lucide-react';
import { isApiStatusFailure, isNetworkFailure, loadFailureWords } from '../../../entities/api-errors';
import { manuscriptQueryKeys } from '../../../entities/manuscript/api';
import { sagaFileKeys } from '../../../entities/saga-file/api';
import { createIdempotencyKey, type CanonKeeperApiClient } from '../../../shared/api';
import { Button } from '../../../ui/controls/Button';
import { useWriteToUsAction } from '../../../ui/feedback/SupportReference';
import { useToasts, type ToastInput } from '../../../ui/feedback/useToastQueue';
import { openingAlready, openRefusals, sagaFileWords } from './words';

/** One opening: the file the author chose and its one key of a repeat (SW2.2). */
export interface SagaFileOpening {
  file: File;
  key: string;
}

/**
 * «Открыть файл саги…» of the app (SW2.1, SW2.2, SW2.4): the file goes with a key of a repeat that is one for the chosen
 * file — «Повторить» after a broken connection sends the same, a new choice a new one. The opening lives in the cache of
 * the mutations: «Данные» and the first screen both see it go, and the window «Сага открыта» takes its end wherever the
 * author is (`SagaOpened`). A refusal is one line of the one area of the toasts by its code.
 */
export function useSagaFileOpen(api: Pick<CanonKeeperApiClient, 'openSagaArchive'>) {
  const queryClient = useQueryClient();
  const toasts = useToasts();
  const writeToUs = useWriteToUsAction();
  const mutation = useMutation({
    mutationKey: sagaFileKeys.open,
    mutationFn: ({ file, key }: SagaFileOpening) => api.openSagaArchive(file, { idempotencyKey: key }),
    // The new saga is in the list of sagas at once; the window says the rest.
    onSuccess: () => queryClient.invalidateQueries({ queryKey: manuscriptQueryKeys.all }),
    onError: (error, opening) => toasts.push(refusalLine(error, opening)),
  });
  const going = useMutationState({
    filters: { mutationKey: sagaFileKeys.open, status: 'pending' },
    select: (pending) => pending.state.variables as SagaFileOpening,
  });

  function refusalLine(error: unknown, opening: SagaFileOpening): ToastInput {
    if (isNetworkFailure(error)) {
      return {
        message: sagaFileWords.openFailed, detail: loadFailureWords(error).cause, tone: 'error',
        action: <Button onClick={() => mutation.mutate(opening)} size="sm" variant="ghost"><RotateCcw aria-hidden="true" size={14} />Повторить</Button>,
      };
    }
    if (isApiStatusFailure(error) && error.status === 429) return { message: openingAlready, tone: 'warning' };
    const code = isApiStatusFailure(error) ? error.problem.code ?? '' : '';
    const refusal = openRefusals[code] ?? (isApiStatusFailure(error) && error.status === 413 ? openRefusals['saga_archive.too_large'] : undefined);
    if (refusal) {
      return { message: refusal.message, tone: 'error', ...(refusal.write ? { action: writeToUs({ topic: 'bug' }) } : {}) };
    }
    // A refusal the page has no words of its own for: the general line with the code for support (SW2.4).
    const { cause, code: supportCode } = loadFailureWords(error);
    return {
      message: sagaFileWords.openFailed, tone: 'error',
      detail: [cause, supportCode ? `Код для поддержки: ${supportCode}` : ''].filter(Boolean).join(' '),
      action: writeToUs({ topic: 'bug', ...(supportCode ? { code: supportCode } : {}) }),
    };
  }

  return {
    /** The file being opened, from any page; null when none. */
    opening: going[0] ?? null,
    /** A file the author chose: a new key of a repeat. */
    open: (file: File) => mutation.mutate({ file, key: createIdempotencyKey('openSagaArchive') }),
  };
}
