import { useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { quotaFailureCode, useAccountQuota } from '../../../entities/account-quota/api';
import { publicApiErrorMessage } from '../../../entities/api-errors';
import type {
  ManuscriptApiClient,
  Project,
  RestoredSource,
} from '../../../entities/manuscript/api';
import { manuscriptQueryKeys } from '../../../entities/manuscript/api';
import * as manuscriptFeatureApi from '../api';
import { sourceQuotaMessage } from './quota-messages';

interface ShelfProjectActionsOptions {
  api: ManuscriptApiClient;
  navigate: (path: string, query?: Record<string, string | undefined>) => void;
  project: Project | null;
  resetForProject: (canEdit: boolean) => void;
  selectedBookId: string;
  setNotice: (notice: string) => void;
  setProjectMenuOpen: (open: boolean) => void;
  setSelectedBookId: (bookId: string) => void;
}

export function useShelfProjectActions({
  api,
  navigate,
  project,
  resetForProject,
  selectedBookId,
  setNotice,
  setProjectMenuOpen,
  setSelectedBookId,
}: ShelfProjectActionsOptions) {
  const queryClient = useQueryClient();
  const [deletingProject, setDeletingProject] = useState(false);
  const createFirstProjectMutation = manuscriptFeatureApi.useCreateFirstProjectWorkspaceMutation(api);
  const quota = useAccountQuota(api).data;

  async function selectProject(projectId: string) {
    resetForProject(false);
    setProjectMenuOpen(false);
    navigate('/manuscript/books', { projectId });
  }

  async function handleRestoredSource(source: RestoredSource) {
    switch (source.kind) {
      case 'project':
        setSelectedBookId('');
        navigate('/manuscript/books', { projectId: source.resource.id });
        return;
      case 'book':
        if (project?.id !== source.resource.projectId) return;
        setSelectedBookId(source.resource.id);
        navigate('/manuscript/books', { projectId: project.id, bookId: source.resource.id });
        return;
      case 'chapter':
        if (selectedBookId !== source.resource.bookId || !project) return;
        navigate('/manuscript/books', {
          projectId: project.id,
          bookId: source.resource.bookId,
          chapterId: source.resource.id,
          mode: 'read',
          sourceView: 'published',
        });
    }
  }

  async function createFirstProject(titleInput: string) {
    const title = titleInput.trim();
    if (!title) {
      setNotice('Введите название саги.');
      return false;
    }

    setNotice('Создаём сагу…');
    try {
      const workspace = await createFirstProjectMutation.mutateAsync({ title });
      setSelectedBookId(workspace.books[0]?.id ?? '');
      setProjectMenuOpen(false);
      // The shelf of the new saga starts fresh; its «Три шага» confirm the saga (R2.1.1).
      setNotice('');
      navigate('/manuscript/books', { projectId: workspace.project.id });
      return true;
    } catch (error) {
      setNotice(sourceQuotaMessage(quotaFailureCode(error), quota) ?? publicApiErrorMessage(error, 'Не удалось создать сагу.'));
      return false;
    }
  }

  async function deleteCurrentProject() {
    if (!project) return false;
    setDeletingProject(true);
    try {
      await api.deleteProject(project.id);
      navigate('/manuscript/books');
      return true;
    } catch (error) {
      setNotice(publicApiErrorMessage(error, 'Не удалось удалить сагу.'));
      return false;
    } finally {
      setDeletingProject(false);
    }
  }

  async function renameCurrentProject(titleInput: string) {
    const title = titleInput.trim();
    if (!project || !title) return false;
    try {
      await api.updateProject(project.id, { title });
      await queryClient.invalidateQueries({
        queryKey: manuscriptQueryKeys.shelf({ projectId: project.id }),
        exact: true,
      });
      setNotice('Сага переименована.');
      return true;
    } catch (error) {
      setNotice(publicApiErrorMessage(error, 'Не удалось переименовать сагу.'));
      return false;
    }
  }

  return {
    createFirstProject,
    creatingFirstProject: createFirstProjectMutation.isPending,
    deleteCurrentProject,
    deletingProject,
    handleRestoredSource,
    renameCurrentProject,
    selectProject,
  };
}
