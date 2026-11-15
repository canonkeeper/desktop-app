// SPDX-License-Identifier: AGPL-3.0-only
import { useMutation, useMutationState, useQuery, useQueryClient } from '@tanstack/react-query';
import { canonKeeperQueryKeys } from '../../../../entities/session/api';
import type { CanonKeeperApiClient, OperationResponse } from '../../../../shared/api';
import { saveFile } from '../../../../ui/save-file';
import { desktopStateQueryKey } from '../desktop-state';

export type ProgramAccess = OperationResponse<'getProgramAccess', '200'>;

/** The operations of the reading by the programs of the author (specs/2026-11-15-desk-mcp.md MC3). */
export type ProgramsClient = Pick<CanonKeeperApiClient, 'getProgramAccess' | 'updateProgramAccess' | 'getClaudeDesktopExtension'>;

const programKeys = {
  access: [...canonKeeperQueryKeys.all, 'desktop-programs'] as const,
  extension: [...canonKeeperQueryKeys.all, 'desktop-programs', 'extension'] as const,
};

function clientOf(meta: unknown) {
  const api = (meta as { programsApi?: Pick<ProgramsClient, 'getProgramAccess'> } | undefined)?.programsApi;
  if (!api) throw new Error('«Свой Claude или Codex» is missing the CanonKeeper API client metadata.');
  return api;
}

/** The one query of the block: its own, apart from the settings of the helper — it stands while they load or fail. */
export function useProgramAccess(api: Pick<ProgramsClient, 'getProgramAccess'>, enabled = true) {
  return useQuery({
    enabled,
    queryKey: programKeys.access,
    queryFn: ({ meta, signal }) => clientOf(meta).getProgramAccess({ signal }),
    meta: { programsApi: api },
  });
}

/** The switch acts at once: the answer is the state after the door opened or closed; «О программе» reads it anew. */
export function useUpdateProgramAccess(api: ProgramsClient) {
  const queries = useQueryClient();
  return useMutation({
    mutationFn: (enabled: boolean) => api.updateProgramAccess({ enabled }),
    onSuccess: (state) => {
      queries.setQueryData(programKeys.access, state);
      void queries.invalidateQueries({ queryKey: desktopStateQueryKey });
    },
  });
}

/** «Сохранить файл расширения»: the file the server builds, saved as the file of a saga is; one save at a time. */
export function useSaveExtension(api: ProgramsClient) {
  const mutation = useMutation({
    mutationKey: programKeys.extension,
    mutationFn: async () => {
      const file = await api.getClaudeDesktopExtension();
      saveFile(file);
      return file.fileName;
    },
  });
  const going = useMutationState({ filters: { mutationKey: programKeys.extension, status: 'pending' } }).length > 0;
  return { ...mutation, going };
}
