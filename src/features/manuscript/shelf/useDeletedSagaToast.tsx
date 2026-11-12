import { useQueryClient } from '@tanstack/react-query';
import { RotateCcw } from 'lucide-react';
import { useRef } from 'react';
import { invalidateAccountQuota, useAccountQuota } from '../../../entities/account-quota/api';
import { publicApiErrorMessage } from '../../../entities/api-errors';
import type { ManuscriptApiClient } from '../../../entities/manuscript/api';
import type { RouteNavigate } from '../../../shared/navigation/app-route';
import { Button } from '../../../ui/controls/Button';
import type { Toasts } from '../../../ui/feedback/useToastQueue';
import { deletionLines } from '../../../ui/overlays/Confirm';
import { useRestoreDeletedSourceMutation } from '../api';
import { quotaRefusal } from './restore-words';

/** What was deleted and comes back from «Недавно удалённые»: a saga, or a book of the saga open. */
export type DeletedSource =
  | { kind: 'project'; id: string; title: string }
  | { kind: 'book'; id: string; projectId: string; title: string };

const words = {
  project: { gone: (title: string) => `Сага «${title}» удалена`, back: (title: string) => `Сага «${title}» возвращена.`, later: 'Сагу можно вернуть в «Недавно удалённых».' },
  book: { gone: (title: string) => `Книга «${title}» удалена`, back: (title: string) => `Книга «${title}» возвращена.`, later: 'Книгу можно вернуть в «Недавно удалённых».' },
} as const;

/**
 * The line after the deletion of a saga or a book (specs/2026-11-10-ux1-shelf.md UXS4.2,
 * specs/2026-11-10-ux2-patterns-actions.md PA3.3): «<Что> удалена» with «Вернуть», until the author closes it. «Вернуть»
 * returns it by the operation of «Недавно удалённые» and opens its shelf; a refusal over the plan says why in the words
 * of «Недавно удалённые». It lives in the one area of the toasts, above the shelf the deletion leaves.
 */
export function useDeletedSagaToast({ api, navigate, toasts }: {
  api: ManuscriptApiClient;
  navigate: RouteNavigate;
  toasts: Toasts;
}) {
  const queryClient = useQueryClient();
  const restore = useRestoreDeletedSourceMutation(api);
  const quota = useAccountQuota(api).data;
  const pending = useRef(false);

  async function giveBack(source: DeletedSource, toastId: number) {
    if (pending.current) return;
    pending.current = true;
    try {
      await restore.mutateAsync({ id: source.id, kind: source.kind });
      toasts.dismiss(toastId);
      toasts.push({ message: words[source.kind].back(source.title), tone: 'success' });
      navigate('/manuscript/books', source.kind === 'project' ? { projectId: source.id } : { projectId: source.projectId, bookId: source.id });
    } catch (error) {
      toasts.dismiss(toastId);
      const refusal = quotaRefusal(error, quota);
      if (refusal) void invalidateAccountQuota(queryClient);
      toasts.push(refusal
        ? { message: refusal.title, detail: refusal.detail, tone: 'error' }
        : { message: 'Не удалось вернуть.', detail: publicApiErrorMessage(error, words[source.kind].later), tone: 'error' });
    } finally {
      pending.current = false;
    }
  }

  return (source: DeletedSource) => {
    let toastId = 0;
    toastId = toasts.push({
      action: (
        <Button data-voice-consent="yes" onClick={() => void giveBack(source, toastId)} size="sm" variant="ghost">
          <RotateCcw aria-hidden="true" size={14} />Вернуть
        </Button>
      ),
      // The same phrase as the window of the deletion (specs/2026-11-11-ux7-trash.md TB2.1).
      detail: deletionLines.returns,
      message: words[source.kind].gone(source.title),
      persistent: true,
      tone: 'neutral',
    });
  };
}
