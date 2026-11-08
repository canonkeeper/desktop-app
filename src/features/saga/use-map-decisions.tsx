import { useMutation, useQueryClient } from '@tanstack/react-query';
import { publicApiErrorMessage } from '../../entities/api-errors';
import { sagaQueryKeys, type SagaApiClient, type SagaMapHiddenItem, type SourceView } from '../../entities/saga/api';
import { Button } from '../../ui/controls/Button';
import { useToastQueue } from '../../ui/feedback/useToastQueue';

/** How a decision of the author reads: a name, or the two ends of a link. */
export function hiddenLabel(item: SagaMapHiddenItem) {
  return item.otherName ? `${item.nodeName} — ${item.otherName}` : item.nodeName;
}

/**
 * «Убрать с карты» and «Это не связь» (specs/2026-11-04-saga-map.md, R11.3): the server takes the decision out of the rows when it
 * reads them, so the map, the cards and the list come again at once. A decision offers «Отменить» until it is closed.
 */
export function useMapDecisions(api: SagaApiClient, projectId: string, sourceView: SourceView) {
  const queryClient = useQueryClient();
  const toasts = useToastQueue();
  const refresh = () => Promise.all([
    queryClient.invalidateQueries({ queryKey: sagaQueryKeys.projectMaps(projectId) }),
    queryClient.invalidateQueries({ queryKey: sagaQueryKeys.projectNodes(projectId) }),
    queryClient.invalidateQueries({ queryKey: sagaQueryKeys.projectMapHidden(projectId) }),
  ]);
  const failed = (error: unknown, fallback: string) => { toasts.push({ message: publicApiErrorMessage(error, fallback), tone: 'error' }); };
  const restore = useMutation({
    mutationFn: (id: string) => api.restoreSagaMapItem(projectId, id),
    onSuccess: refresh,
    onError: (error) => failed(error, 'Не удалось вернуть на карту.'),
  });
  const hide = useMutation({
    mutationFn: (item: { nodeKey: string; otherKey?: string }) => api.hideSagaMapItem(projectId, sourceView, item),
    onSuccess: async (kept) => {
      await refresh();
      const id = toasts.push({
        action: <Button onClick={() => { toasts.dismiss(id); restore.mutate(kept.id); }} size="sm" variant="ghost">Отменить</Button>,
        message: kept.otherName ? `Это не связь: ${hiddenLabel(kept)}` : `Убрано с карты: ${kept.nodeName}`,
        persistent: true,
        tone: 'neutral',
      });
    },
    onError: (error) => failed(error, 'Не удалось убрать с карты.'),
  });
  return {
    hideLink: (nodeKey: string, otherKey: string) => hide.mutate({ nodeKey, otherKey }),
    hideNode: (nodeKey: string) => hide.mutate({ nodeKey }),
    pending: hide.isPending || restore.isPending,
    restore: (id: string) => restore.mutate(id),
    toasts,
  };
}

export type MapDecisions = ReturnType<typeof useMapDecisions>;
