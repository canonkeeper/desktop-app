import { useMemo } from 'react';
import type { ManuscriptApiClient } from '../../../entities/manuscript/api';
import { useImportConstraintsQuery } from '../api';
import { screenImportFiles, type LocalQueueFile } from './import-queue';

/**
 * UC3.1: the chosen files of the import are checked against the server constraints; a refused
 * file never enters the list and is never transferred. Until the constraints arrive nothing is
 * accepted, so there is no unchecked path (R3.1.2).
 */
export function useImportScreening(
  api: Pick<ManuscriptApiClient, 'getImportConstraints'>,
  projectId: string | null,
  enabled: boolean,
  localFiles: readonly LocalQueueFile[],
) {
  const query = useImportConstraintsQuery(api, projectId ?? '', enabled);
  const constraints = query.data ?? null;
  const screening = useMemo(
    () => constraints ? screenImportFiles(localFiles.map((local) => local.file), constraints) : null,
    [constraints, localFiles],
  );
  const acceptedFiles = useMemo(
    () => screening ? localFiles.filter((local) => screening.accepted.includes(local.file)) : [],
    [localFiles, screening],
  );
  return {
    acceptedFiles,
    constraints,
    refetch: query.refetch,
    refreshing: query.isFetching,
    rejectedFiles: screening?.rejected.map(({ file, message }) => ({ fileName: file.name, message })) ?? [],
    screened: screening !== null,
    status: constraints ? 'ready' as const : query.isError ? 'error' as const : 'pending' as const,
  };
}
