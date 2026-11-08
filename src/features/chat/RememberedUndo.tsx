import { useMutation, useQueryClient } from '@tanstack/react-query';
import { createContext, useContext } from 'react';
import { authorMemoryQueryKeys, type AuthorMemoryApiClient } from '../../entities/author-memory/api';
import { Button } from '../../ui/controls/Button';
import type { FeedAction, FeedTurn } from './feed/feed-model';
import styles from './ResearchChatPanel.module.css';

/** What the undo of a remembered record needs from the chat page. */
export interface ChatMemoryHost {
  api: Pick<AuthorMemoryApiClient, 'listAuthorMemory' | 'deleteAuthorMemory'>;
  projectId: string;
  /** An editor of the saga: may undo what the chat remembered. */
  canEdit: boolean;
}

const ChatMemoryContext = createContext<ChatMemoryHost | null>(null);

export const ChatMemoryProvider = ChatMemoryContext.Provider;

/**
 * «Отменить» under «Запомнил: …» (specs/2026-11-02-writer-workshop.md, Decision log 03.11): the record the turn
 * remembered is found among the records of its Job and removed by the same API as on its page.
 */
export function RememberedUndo({ action, turn }: { action: FeedAction; turn: FeedTurn }) {
  const host = useContext(ChatMemoryContext);
  const queryClient = useQueryClient();
  const remembered = action.description.remembered;
  const jobId = turn.question?.jobId ?? turn.answer?.jobId;
  const undo = useMutation({
    mutationFn: async () => {
      if (!host || !remembered || !jobId) return false;
      const page = await host.api.listAuthorMemory(host.projectId, { jobId, limit: 100 });
      const record = page.data.find((item) => item.kind === remembered.kind && item.text === remembered.text);
      if (!record) return false;
      await host.api.deleteAuthorMemory(host.projectId, record.id, record.revision);
      return true;
    },
    onSuccess: () => host ? queryClient.invalidateQueries({ queryKey: authorMemoryQueryKeys.project(host.projectId) }) : undefined,
  });
  if (!host?.canEdit || !remembered || !jobId || action.status !== 'done') return null;
  if (undo.isSuccess) return <span className={styles.rememberedNote}>{undo.data ? 'Отменено: запись снята.' : 'Этой записи уже нет.'}</span>;
  return (
    <span className={styles.rememberedNote}>
      <Button disabled={undo.isPending} onClick={() => undo.mutate()} size="sm" variant="ghost">Отменить</Button>
      {undo.isError ? ' Не получилось — попробуйте ещё раз.' : null}
    </span>
  );
}
