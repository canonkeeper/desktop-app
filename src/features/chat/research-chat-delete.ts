import { createElement, useEffect, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { isApiStatusFailure, publicApiErrorMessage } from '../../entities/api-errors';
import {
  type ChatApiClient,
  chatQueryKeys,
  type ChatScope,
  type ChatSession,
} from '../../entities/chat/api';
import { WriteToUs } from '../../ui/feedback/SupportReference';
import { useToasts } from '../../ui/feedback/useToastQueue';
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
  // The chat «Удалить чат…» was pressed for: the open one, or any row of the list (specs/2026-11-11-ux4-after-answer.md AA3.3).
  const [targetId, setTargetId] = useState<string | null>(null);
  const open = targetId !== null;
  const [deleteFailure, setDeleteFailure] = useState<{ message: string; code: string } | null>(null);
  const mutation = useDeleteChatSessionMutation(api, scope);
  const queryClient = useQueryClient();
  const toasts = useToasts();
  const selected = sessions.find((session) => session.id === selectedChatId);
  const target = sessions.find((session) => session.id === targetId);

  useEffect(() => {
    setTargetId(null);
    setDeleteFailure(null);
  }, [selectedChatId]);

  function dismiss() {
    if (mutation.isPending) return;
    setTargetId(null);
    setDeleteFailure(null);
  }

  function show(chatId: string = selectedChatId) {
    if (!sessions.some((session) => session.id === chatId) || mutation.isPending) return;
    setDeleteFailure(null);
    setTargetId(chatId);
  }

  /**
   * After the deletion of the open chat the author stands in a new chat; another row goes from the list and he stays where
   * he is. The line says which chat went (PA3.3, AA3.5).
   */
  function finish(chatId: string, title: string, alreadyDeleted: boolean) {
    setTargetId(null);
    setDeleteFailure(null);
    if (chatId === selectedChatId) onDeleted('');
    toasts.push({
      message: alreadyDeleted ? `Чат «${title}» уже удалён.` : `Чат «${title}» удалён.`,
      tone: 'neutral',
    });
  }

  async function confirm() {
    const chatId = targetId;
    if (!chatId || mutation.isPending) return;
    const title = target?.title ?? '';
    setDeleteFailure(null);
    try {
      await mutation.mutateAsync(chatId);
      if (isCurrentChat()) finish(chatId, title, false);
    } catch (failure) {
      if (isApiStatusFailure(failure) && failure.status === 404) {
        queryClient.setQueryData<Awaited<ReturnType<ChatApiClient['listChatSessions']>>>(
          chatQueryKeys.sessions(scope),
          (current) => current
            ? { ...current, data: current.data.filter((session) => session.id !== chatId) }
            : current,
        );
        queryClient.removeQueries({ queryKey: chatQueryKeys.session(scope, chatId), exact: true });
        void queryClient.invalidateQueries({ queryKey: chatQueryKeys.sessions(scope), exact: true });
        if (isCurrentChat()) finish(chatId, title, true);
        return;
      }
      if (!isCurrentChat()) return;
      const message = publicApiErrorMessage(failure, 'Не удалось удалить чат.', {
        network: 'Не удалось удалить чат.',
        server: 'Не удалось удалить чат.',
      });
      const requestId = chatErrorRequestId(failure);
      if (isApiStatusFailure(failure) && failure.status === 403) {
        setTargetId(null);
        onAuthorityLost();
        // The notice offers the letter with the code as its action (R4.2.1).
        toasts.push({ action: requestId ? createElement(WriteToUs, { code: requestId, topic: 'bug' }) : undefined, message, tone: 'error' });
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
    /** The title of the open chat: «⋯» over the feed names it. */
    selectedTitle: selected?.title ?? '',
    /** The title of the chat the window asks about. */
    targetTitle: target?.title ?? '',
    show,
  };
}
