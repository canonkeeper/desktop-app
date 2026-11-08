import { createElement, useEffect, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { isApiStatusFailure, publicApiErrorMessage } from '../../entities/api-errors';
import {
  type ChatApiClient,
  chatQueryKeys,
  type ChatScope,
  type ChatSession,
} from '../../entities/chat/api';
import { SupportMailLink } from '../../ui/feedback/SupportReference';
import { useToastQueue } from '../../ui/feedback/useToastQueue';
import { chatErrorRequestId, useDeleteChatSessionMutation } from './api';

interface ResearchChatDeleteOptions {
  api: ChatApiClient;
  isCurrentChat: () => boolean;
  onAuthorityLost: () => void;
  onDeleted: (nextChatId: string) => void;
  scope: ChatScope;
  selectedChatId: string;
  sessions: ChatSession[];
}

export function useResearchChatDelete({
  api,
  isCurrentChat,
  onAuthorityLost,
  onDeleted,
  scope,
  selectedChatId,
  sessions,
}: ResearchChatDeleteOptions) {
  const [open, setOpen] = useState(false);
  const [deleteFailure, setDeleteFailure] = useState<{ message: string; code: string } | null>(null);
  const mutation = useDeleteChatSessionMutation(api, scope);
  const queryClient = useQueryClient();
  const toasts = useToastQueue();
  const selected = sessions.find((session) => session.id === selectedChatId);

  useEffect(() => {
    setOpen(false);
    setDeleteFailure(null);
  }, [selectedChatId]);

  function dismiss() {
    if (mutation.isPending) return;
    setOpen(false);
    setDeleteFailure(null);
  }

  function show() {
    if (!selected || mutation.isPending) return;
    setDeleteFailure(null);
    setOpen(true);
  }

  function finish(alreadyDeleted: boolean) {
    const nextChatId = sessions.find((session) => session.id !== selectedChatId)?.id ?? '';
    setOpen(false);
    setDeleteFailure(null);
    onDeleted(nextChatId);
    toasts.push({
      message: alreadyDeleted
        ? 'Чат уже удалён. Список обновлён.'
        : `Чат удалён${nextChatId ? ' · открыт следующий чат' : ''}.`,
      tone: 'neutral',
    });
  }

  async function confirm() {
    if (!selectedChatId || mutation.isPending) return;
    setDeleteFailure(null);
    try {
      await mutation.mutateAsync(selectedChatId);
      if (isCurrentChat()) finish(false);
    } catch (failure) {
      if (isApiStatusFailure(failure) && failure.status === 404) {
        queryClient.setQueryData<Awaited<ReturnType<ChatApiClient['listChatSessions']>>>(
          chatQueryKeys.sessions(scope),
          (current) => current
            ? { ...current, data: current.data.filter((session) => session.id !== selectedChatId) }
            : current,
        );
        queryClient.removeQueries({ queryKey: chatQueryKeys.session(scope, selectedChatId), exact: true });
        void queryClient.invalidateQueries({ queryKey: chatQueryKeys.sessions(scope), exact: true });
        if (isCurrentChat()) finish(true);
        return;
      }
      if (!isCurrentChat()) return;
      const message = publicApiErrorMessage(failure, 'Не удалось удалить чат.', {
        network: 'Не удалось удалить чат.',
        server: 'Не удалось удалить чат.',
      });
      const requestId = chatErrorRequestId(failure);
      if (isApiStatusFailure(failure) && failure.status === 403) {
        setOpen(false);
        onAuthorityLost();
        // The notice offers the letter with the code as its action (R4.2.1).
        toasts.push({ action: requestId ? createElement(SupportMailLink, { code: requestId }) : undefined, message, tone: 'error' });
        return;
      }
      setDeleteFailure({ message, code: requestId });
    }
  }

  return {
    confirm,
    dismiss,
    error: deleteFailure?.message ?? '',
    errorCode: deleteFailure?.code,
    isPending: mutation.isPending,
    open,
    selectedTitle: selected?.title ?? '',
    show,
    toasts,
  };
}
