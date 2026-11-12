import { useMutation, useQueryClient } from '@tanstack/react-query';
import { publicApiErrorMessage } from '../../entities/api-errors';
import { sagaQueryKeys, type SagaApiClient, type SagaMapCategory, type SagaMapHiddenItem, type SourceView } from '../../entities/saga/api';
import { Button } from '../../ui/controls/Button';
import { useToasts } from '../../ui/feedback/useToastQueue';

/** How a decision of the author reads: a name, or the two ends of a link. */
export function hiddenLabel(item: SagaMapHiddenItem) {
  return item.otherName ? `${item.nodeName} — ${item.otherName}` : item.nodeName;
}

/** What a decision about the map changes and what it does not: the hint of the buttons and the second line of their result (MP3.1). */
export const onlyOnMap = 'Только на карте. Книга и ответы помощника не меняются';

/**
 * The line of a name taken off the map (MP3.2): the page does not know the gender of a name, so the line names its kind,
 * «Герой «Мара» убран с карты».
 */
function nodeOffLine(name: string, category: SagaMapCategory | null | undefined) {
  switch (category) {
    case 'character': return `Герой «${name}» убран с карты`;
    case 'place': return `Место «${name}» убрано с карты`;
    case 'faction': return `Фракция «${name}» убрана с карты`;
    case 'item': return `Предмет «${name}» убран с карты`;
    default: return `Имя «${name}» убрано с карты`;
  }
}

/**
 * «Убрать с карты» and «Убрать связь» (specs/2026-11-04-saga-map.md, R11.3; specs/2026-11-11-ux8-map.md MP3.1, MP3.2): the
 * server takes the decision out of the rows when it reads them, so the map, the cards and the list come again at once. The
 * result is a line of the one area of the toasts with «Отменить».
 */
export function useMapDecisions(api: SagaApiClient, projectId: string, sourceView: SourceView) {
  const queryClient = useQueryClient();
  const toasts = useToasts();
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
    mutationFn: (item: { nodeKey: string; otherKey?: string; category?: SagaMapCategory | null }) =>
      api.hideSagaMapItem(projectId, sourceView, { nodeKey: item.nodeKey, ...(item.otherKey ? { otherKey: item.otherKey } : {}) }),
    onSuccess: async (kept, item) => {
      await refresh();
      const id = toasts.push({
        action: <Button onClick={() => { toasts.dismiss(id); restore.mutate(kept.id); }} size="sm" variant="ghost">Отменить</Button>,
        detail: onlyOnMap,
        message: kept.otherName ? `Связь ${hiddenLabel(kept)} убрана с карты` : nodeOffLine(kept.nodeName, item.category),
        tone: 'neutral',
      });
    },
    onError: (error) => failed(error, 'Не удалось убрать с карты.'),
  });
  return {
    hideLink: (nodeKey: string, otherKey: string) => hide.mutate({ nodeKey, otherKey }),
    /** The node leaves the map, and its card closes with it: «Отменить» of the line is the one way back now (MP3.2). */
    hideNode: (nodeKey: string, category: SagaMapCategory | null, onHidden: () => void) => hide.mutate({ nodeKey, category }, { onSuccess: onHidden }),
    pending: hide.isPending || restore.isPending,
    restore: (id: string) => restore.mutate(id),
  };
}

export type MapDecisions = ReturnType<typeof useMapDecisions>;
