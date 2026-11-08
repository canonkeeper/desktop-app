import { useMutation, useQueryClient } from '@tanstack/react-query';
import { authorMaterialQueryKeys } from '../../../entities/author-materials/api';
import { sagaQueryKeys, type SagaMapNodeCard } from '../../../entities/saga/api';

/**
 * A write of the materials: the list comes from the server again and the map finds out it is behind and asks until the new one is
 * built (specs/2026-11-04-saga-map.md, R9.5). The cards come from the rows of that build, so what the server answered goes to the
 * cards already read: the card shows the new record and its revision at once.
 */
export function useMaterialsMutation<TInput, TOutput>(
  projectId: string,
  write: (input: TInput) => Promise<TOutput>,
  onCard?: (card: SagaMapNodeCard, output: TOutput) => SagaMapNodeCard,
) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: write,
    onSuccess: (output) => {
      if (onCard) queryClient.setQueriesData<SagaMapNodeCard>({ queryKey: sagaQueryKeys.projectNodes(projectId) }, (card) => (card ? onCard(card, output) : card));
      return Promise.all([
        queryClient.invalidateQueries({ queryKey: authorMaterialQueryKeys.project(projectId) }),
        queryClient.invalidateQueries({ queryKey: sagaQueryKeys.projectMaps(projectId) }),
      ]);
    },
  });
}
