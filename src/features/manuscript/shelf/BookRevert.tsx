import { useQuery } from '@tanstack/react-query';
import { useEffect, useState } from 'react';
import { quotaFailureCode, useAccountQuota } from '../../../entities/account-quota/api';
import { refusalWords } from '../../../entities/account-quota/refusal-words';
import { QuotaRefusal } from '../../../entities/account-quota/ui/QuotaRefusal';
import { isApiStatusFailure } from '../../../entities/api-errors';
import { manuscriptQueryKeys, type ManuscriptApiClient } from '../../../entities/manuscript/api';
import type { Toasts } from '../../../ui/feedback/useToastQueue';
import { Confirm } from '../../../ui/overlays/Confirm';
import { useRevertBookUpdateMutation } from '../api';
import { revertAction, revertQuestion } from './book-revert-words';

/** The book whose return the author asked for. */
export interface RevertTarget {
  bookId: string;
  title: string;
}

/** Why a return or its preview did not happen, by the code of the server. */
function refusedWords(error: unknown): { gone: boolean; words: string } {
  const code = isApiStatusFailure(error) ? error.problem.code ?? '' : '';
  switch (code) {
    case 'book.update_gone':
      return { gone: true, words: 'Вернуть прежнюю версию уже нельзя: прошло 7 дней или книгу уже вернули.' };
    case 'book.busy':
      return { gone: false, words: 'Книга сейчас обновляется из файла — вернуть прежнюю версию можно будет после этого.' };
    case 'book.update_changed':
      return { gone: false, words: 'Книга изменилась, пока вы читали, — посмотрите, что вернётся теперь.' };
    case 'demo_saga.read_only':
      return { gone: true, words: 'Книгу саги-примера не меняют.' };
    default:
      return { gone: false, words: 'Не удалось вернуть прежнюю версию. Попробуйте ещё раз.' };
  }
}

/**
 * «Вернуть прежнюю версию…» of a book after its update from a file (specs/2026-11-12-ux10-book-revert.md BR3.2): the preview of
 * the server is read first, the one question says its words, «Вернуть версию до обновления» returns the book with the
 * fingerprint of that preview, and the line of the root says it is done. A book changed in between is read again and the
 * question says what comes back now; a refusal by the tier is the one block of refusals, as for an upload. The shell holds it:
 * «Что изменилось» and «Недавно удалённые» ask through it.
 */
export function useBookRevert({ api, onReturned, toasts }: {
  api: Pick<ManuscriptApiClient, 'getAccountQuota' | 'previewBookUpdateRevert' | 'revertBookUpdate'>;
  /** The book came back: what showed its update steps aside. */
  onReturned?: ((bookId: string) => void) | undefined;
  toasts: Pick<Toasts, 'push'>;
}) {
  const [target, setTarget] = useState<RevertTarget | null>(null);
  const [error, setError] = useState('');
  const [refused, setRefused] = useState('');
  const revert = useRevertBookUpdateMutation(api);
  // Once the return is asked for, the preview has had its say: the books are read anew before the question closes, and
  // the preview of an update already returned answers «gone».
  const returning = revert.isPending || revert.isSuccess;
  const preview = useQuery({
    queryKey: [...manuscriptQueryKeys.bookRevert(target?.bookId ?? ''), api] as const,
    queryFn: () => api.previewBookUpdateRevert(target!.bookId),
    enabled: Boolean(target) && !returning,
    staleTime: 0,
    gcTime: 0,
    retry: false,
  });
  const quota = useAccountQuota(api, Boolean(target)).data;

  // A preview the server refuses — the update is gone, the book is being updated — closes and says why.
  // That «gone» is no refusal: it stood beside the line of the success (the walk of TEST 12.11).
  const previewError = target && preview.isError && !returning ? preview.error : null;
  useEffect(() => {
    if (!previewError || !target) return;
    toasts.push({ message: refusedWords(previewError).words, tone: 'neutral' });
    setTarget(null);
  }, [previewError, target, toasts]);

  function close() {
    if (revert.isPending) return;
    setTarget(null);
    setError('');
    setRefused('');
    revert.reset();
  }

  const shown = target && preview.data?.bookId === target.bookId ? preview.data : null;

  function confirm() {
    if (!shown || !target) return;
    setError('');
    setRefused('');
    revert.mutate({ bookId: shown.bookId, fingerprint: shown.fingerprint }, {
      onSuccess: () => {
        toasts.push({
          message: `Книга «${shown.bookTitle}» возвращена к версии до обновления`,
          ...(shown.text.length || shown.returned.length ? { detail: 'Помощник перечитает возвращённые главы.' } : {}),
          tone: 'success',
        });
        setTarget(null);
        onReturned?.(shown.bookId);
      },
      onError: (failure) => {
        const code = quotaFailureCode(failure);
        if (code) {
          setRefused(code);
          return;
        }
        const refusal = refusedWords(failure);
        if (refusal.gone) {
          toasts.push({ message: refusal.words, tone: 'neutral' });
          setTarget(null);
          return;
        }
        setError(refusal.words);
        // The book changed: the question reads the preview again and says what comes back now.
        if (isApiStatusFailure(failure) && failure.problem.code === 'book.update_changed') void preview.refetch();
      },
    });
  }

  const question = shown ? revertQuestion(shown) : null;
  const words = refused ? refusalWords(refused, quota) : null;
  return {
    /** The book whose question is being read, for the press that opened it to show its wait. */
    asking: target && !shown ? target.bookId : null,
    ask(next: RevertTarget) {
      setError('');
      setRefused('');
      // A new question starts clean: the return before it is over.
      revert.reset();
      setTarget(next);
    },
    element: question ? (
      <Confirm action={revertAction} also={question.also || undefined} busy={revert.isPending || preview.isFetching} error={error || undefined}
        onCancel={close} onConfirm={confirm} title={question.title}>
        <p>{question.line}</p>
        {words ? (
          <QuotaRefusal announce code={refused} place="shelf" words={words}>
            <p>Книга осталась как сейчас.</p>
          </QuotaRefusal>
        ) : null}
      </Confirm>
    ) : null,
  };
}
