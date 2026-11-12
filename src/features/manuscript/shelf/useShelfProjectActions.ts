import { useState } from 'react';
import type { RefusalWords } from '../../../entities/account-quota/refusal-words';
import type { ManuscriptApiClient } from '../../../entities/manuscript/api';
import { useCreateSaga } from './useSagaActions';

interface ShelfProjectActionsOptions {
  api: ManuscriptApiClient;
  navigate: (path: string, query?: Record<string, string | undefined>) => void;
  setNotice: (notice: string) => void;
}

/**
 * The saga of the first steps of the shelf (R2.1.2): «Создать сагу» opens the shelf of the new saga, a refusal is said in the
 * notice of the shelf. «Новая сага», «Настроить сагу» and «Недавно удалённые» are the shell's
 * (specs/2026-11-10-ux2-shell-core.md UC3).
 */
export function useShelfProjectActions({ api, navigate, setNotice }: ShelfProjectActionsOptions) {
  const createSaga = useCreateSaga(api);
  // The refusal of a new saga by a quota, with its notice: the shelf says it as the one block of refusals (PR2.2).
  const [quotaRefusal, setQuotaRefusal] = useState<{ notice: string; code: string; words: RefusalWords } | null>(null);

  async function createFirstProject(titleInput: string) {
    if (!titleInput.trim()) {
      setNotice('Введите название саги.');
      return false;
    }
    setNotice('Создаём сагу…');
    const created = await createSaga.create(titleInput);
    if ('refusal' in created) {
      const { code, message, words } = created.refusal;
      setQuotaRefusal(code && words ? { notice: message, code, words } : null);
      setNotice(message);
      return false;
    }
    // The shelf of the new saga starts fresh; its «Три шага» confirm the saga (R2.1.1).
    setNotice('');
    navigate('/manuscript/books', { projectId: created.workspace.project.id });
    return true;
  }

  /**
   * «Моя сага» of «Вычитать мою главу» on the first screen (specs/2026-11-10-ux3-first-screen.md FS2.1): the same operation
   * and refusals as «Создать сагу», without leaving the page — the card goes on with the chapter in the saga.
   */
  async function foundFirstSaga(title: string) {
    const created = await createSaga.create(title);
    if ('refusal' in created) {
      const { code, message, words } = created.refusal;
      setQuotaRefusal(code && words ? { notice: message, code, words } : null);
      setNotice(message);
      return null;
    }
    return created.workspace.project.id;
  }

  return {
    createFirstProject,
    foundFirstSaga,
    creatingFirstProject: createSaga.creating,
    /** The refusal by a limit the shelf notice says, while it says it. */
    quotaRefusalOfNotice: (notice: string) => (quotaRefusal?.notice === notice ? { code: quotaRefusal.code, words: quotaRefusal.words } : null),
  };
}
