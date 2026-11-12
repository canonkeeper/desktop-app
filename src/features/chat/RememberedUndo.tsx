import { useMutation, useQueryClient } from '@tanstack/react-query';
import { createContext, useContext, type MouseEvent } from 'react';
import { authorMemoryQueryKeys, type AuthorMemoryApiClient } from '../../entities/author-memory/api';
import type { FeedAction, FeedTurn } from './feed/feed-model';
import styles from './ResearchChatPanel.module.css';

/** What the line of a remembered record needs from the page. */
export interface ChatMemoryHost {
  api: Pick<AuthorMemoryApiClient, 'listAuthorMemory' | 'deleteAuthorMemory'>;
  projectId: string;
  /** An editor of the saga: may undo what the chat remembered. */
  canEdit: boolean;
  /** «Что помнит помощник» of the saga (specs/2026-11-10-ux1-chat.md CH4.2); the feed of a workshop's helper has none. */
  memoryLink?: { href: string; onOpen: () => void } | undefined;
}

const ChatMemoryContext = createContext<ChatMemoryHost | null>(null);

export const ChatMemoryProvider = ChatMemoryContext.Provider;

/**
 * «Отменить» and «Что помнит помощник» of the line «Запомнил: …» under an answer (specs/2026-11-02-writer-workshop.md,
 * Decision log 03.11; specs/2026-11-10-ux1-chat.md CH4.2): the record the turn remembered is found among the records of
 * its Job and removed by the same API as on its page.
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
  if (!host || !remembered || action.status !== 'done') return null;
  const link = host.memoryLink;
  // A real link: a modified click opens the page of the records in a new tab.
  const follow = (event: MouseEvent<HTMLAnchorElement>) => {
    if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey || event.button !== 0) return;
    event.preventDefault();
    link?.onOpen();
  };
  const canUndo = host.canEdit && Boolean(jobId);
  if (!canUndo && !link) return null;
  return (
    <span className={styles.rememberedNote}>
      {undo.isSuccess ? <span role="status">{undo.data ? 'Забыл эту запись.' : 'Этой записи уже нет.'}</span>
        : canUndo ? <button className={styles.rememberedUndo} disabled={undo.isPending} onClick={() => undo.mutate()} type="button">Отменить</button> : null}
      {undo.isError ? <span role="alert">Не получилось — попробуйте ещё раз.</span> : null}
      {link ? <a className={styles.rememberedLink} href={link.href} onClick={follow}>Что помнит помощник</a> : null}
    </span>
  );
}
