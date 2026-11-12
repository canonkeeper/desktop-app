import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { quotaFailureCode, useAccountQuota } from '../../../entities/account-quota/api';
import { publicApiErrorMessage } from '../../../entities/api-errors';
import { manuscriptQueryKeys, type FirstProjectWorkspace, type ManuscriptApiClient, type Project } from '../../../entities/manuscript/api';
import * as manuscriptFeatureApi from '../api';
import { refusalWords, type RefusalWords } from '../../../entities/account-quota/refusal-words';
import { sourceQuotaMessage } from './quota-messages';

/**
 * Why a saga was not created: the words for the author, and for a refusal by a limit its code and the words of the one
 * block of refusals (specs/2026-11-10-ux2-patterns-refusals.md PR2.2).
 */
export interface SagaRefusal {
  message: string;
  code: string | null;
  words?: RefusalWords | null;
}

/**
 * «Создать сагу» of the first steps of the shelf and «Новая сага» of the menu of the saga (specs/2026-11-10-ux2-shell.md
 * S3.1): one way to create a saga, with the words of a refusal by the plan.
 */
export function useCreateSaga(api: ManuscriptApiClient) {
  const mutation = manuscriptFeatureApi.useCreateFirstProjectWorkspaceMutation(api);
  const quota = useAccountQuota(api).data;
  async function create(titleInput: string): Promise<{ workspace: FirstProjectWorkspace } | { refusal: SagaRefusal }> {
    const title = titleInput.trim();
    if (!title) return { refusal: { message: 'Введите название саги.', code: null } };
    try {
      return { workspace: await mutation.mutateAsync({ title }) };
    } catch (error) {
      const code = quotaFailureCode(error);
      const refusal = sourceQuotaMessage(code, quota);
      return { refusal: refusal ? { message: refusal, code, words: refusalWords(code, quota) } : { message: publicApiErrorMessage(error, 'Не удалось создать сагу.'), code: null } };
    }
  }
  return { create, creating: mutation.isPending };
}

type MembersClient = Pick<ManuscriptApiClient, 'listProjectMembers'>;

/**
 * Whether the author is alone in his saga (specs/2026-11-11-ux9-shelf-chat.md SC1.1): the first two members, read only for
 * the one who may list them — the owner or an administrator — while «Настроить сагу» is open. Undefined while unknown.
 */
export function useAloneInSaga(api: MembersClient, project: Project | null, open: boolean) {
  const role = project?.currentMembership.role;
  const lists = role === 'owner' || role === 'admin';
  const projectId = project?.id ?? '';
  const members = useQuery({
    queryKey: manuscriptQueryKeys.members(projectId),
    queryFn: ({ meta }) => (meta as { membersApi: MembersClient }).membersApi.listProjectMembers(projectId, { limit: 2 }),
    meta: { membersApi: api },
    enabled: Boolean(project) && lists && open,
    staleTime: 60_000,
  }).data;
  if (!lists) return false;
  return members ? members.data.filter((member) => member.status === 'active').length <= 1 : undefined;
}

/**
 * «Настроить сагу» (specs/2026-11-10-ux2-shell.md S3.2): the new name by the operation of the saga, and «Удалить сагу» —
 * the saga goes to «Недавно удалённые»; the page is told so it offers «Вернуть» (specs/2026-11-10-ux1-shelf.md UXS4.2).
 */
export function useSagaSettings({ api, navigate, onDeleted }: {
  api: ManuscriptApiClient;
  navigate: (path: string, query?: Record<string, string | undefined>) => void;
  onDeleted: (project: Project) => void;
}) {
  const queryClient = useQueryClient();
  const [renaming, setRenaming] = useState(false);
  const [deleting, setDeleting] = useState(false);

  /** The new name, or the words of why it was not given. */
  async function rename(project: Project, titleInput: string): Promise<string | null> {
    const title = titleInput.trim();
    if (!title) return 'Введите название саги.';
    if (title === project.title) return null;
    setRenaming(true);
    try {
      await api.updateProject(project.id, { title });
      // Every shelf read before names the saga by its new name: the menu of the saga lists them all.
      await queryClient.invalidateQueries({ queryKey: manuscriptQueryKeys.all });
      return null;
    } catch (error) {
      return publicApiErrorMessage(error, 'Не удалось переименовать сагу.', { forbidden: 'Переименовать сагу может её владелец или администратор.' });
    } finally {
      setRenaming(false);
    }
  }

  /** The saga goes to «Недавно удалённые»; the words of a failure, or null. */
  async function remove(project: Project): Promise<string | null> {
    setDeleting(true);
    try {
      await api.deleteProject(project.id);
      // Every shelf read before — the default one too — and the list of the deleted sagas are read again.
      void queryClient.invalidateQueries({ queryKey: manuscriptQueryKeys.all });
      navigate('/manuscript/books');
      onDeleted(project);
      return null;
    } catch (error) {
      return publicApiErrorMessage(error, 'Не удалось удалить сагу.', { forbidden: 'Удалить сагу может её владелец или администратор.' });
    } finally {
      setDeleting(false);
    }
  }

  return { deleting, remove, rename, renaming };
}
