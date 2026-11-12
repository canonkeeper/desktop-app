import { MessageCircle } from 'lucide-react';
import { useId } from 'react';
import { Button } from '../../ui/controls/Button';
import type { AccountQuota } from '../../entities/account-quota/api';
import { refusalWords } from '../../entities/account-quota/refusal-words';
import { QuotaRefusal } from '../../entities/account-quota/ui/QuotaRefusal';
import { chatLimitNotice, chatQuotaHint, questionLengthHint, type ChatFieldMode, type ChatLimitNoticeCopy } from './chat-quota';
import { ChatQuotaHint } from './ChatQuotaHint';
import styles from './ChatQuotaHint.module.css';

/** A chat whose question is being answered: «идут два ответа» names it and leads to it (AA2.7). */
export interface RunningChat {
  id: string;
  title: string;
}

interface ChatLimitNoticeProps {
  /** The question field is described by the block. */
  id?: string;
  notice: ChatLimitNoticeCopy;
  quota: AccountQuota | undefined;
  running?: readonly RunningChat[] | undefined;
  onOpenChat?: ((chatId: string) => void) | undefined;
}

function clipTitle(title: string) {
  const signs = [...title.replace(/\s+/gu, ' ').trim()];
  return signs.length > 40 ? `${signs.slice(0, 39).join('').trimEnd()}…` : signs.join('');
}

/**
 * The block of a refusal by a limit above the question field (specs/2026-11-10-ux1-chat.md CH3.3–CH3.5): the one block
 * of every section (specs/2026-11-10-ux2-patterns-refusals.md PR2.1) with what became of the typed text. It is announced
 * when the server refused; standing in advance, it is a quiet note. «Тариф» leads back to the question (PR2.4).
 */
export function ChatLimitNotice({ id, notice, onOpenChat, quota, running = [] }: ChatLimitNoticeProps) {
  const words = refusalWords(notice.code, quota);
  if (!words) return null;
  // «Идут два ответа» names those questions and leads to their chats: the list may be hidden behind «Чаты» (AA2.7, Ч-36).
  const answering = notice.code === 'quota.active_turns' && onOpenChat ? running : [];
  return (
    <QuotaRefusal announce={notice.refused} className={styles.limit} code={notice.code} id={id} place="question" words={words}
      actions={answering.length ? answering.map((chat) => (
        <Button key={chat.id} onClick={() => onOpenChat?.(chat.id)} size="sm" title={chat.title} variant="outline">
          <MessageCircle aria-hidden="true" size={14} />{`«${clipTitle(chat.title)}»`}
        </Button>
      )) : undefined}>
      <p>{notice.kept}</p>
      {answering.length ? <p>{answering.length === 1 ? 'Готовится ответ на вопрос:' : 'Готовятся ответы на вопросы:'}</p> : null}
    </QuotaRefusal>
  );
}

interface ChatFieldNotesInput {
  quota: AccountQuota | undefined;
  /** The chats whose questions are being answered, for «идут два ответа» (AA2.7). */
  running?: readonly RunningChat[];
  onOpenChat?: (chatId: string) => void;
  /** The `quota.*` code of the last refused message; '' without one. */
  refusedCode: string;
  /** A turn of this chat works. */
  turnActive: boolean;
  mode: ChatFieldMode;
  text: string;
}

/**
 * What stands above the question field (CH3, CH5.1, CH1.7): the block of a limit — or the line of the count and the
 * price — and the count of signs of a long text. The field is described by what is shown.
 */
export function useChatFieldNotes({ onOpenChat, quota, refusedCode, running, turnActive, mode, text }: ChatFieldNotesInput) {
  const id = useId();
  const limit = chatLimitNotice({ quota, refusedCode, turnActive });
  const hint = limit ? '' : chatQuotaHint(quota, mode);
  const length = questionLengthHint(text, mode !== 'question');
  return {
    limit,
    above: <>
      {limit ? <ChatLimitNotice id={`${id}-limit`} notice={limit} onOpenChat={onOpenChat} quota={quota} running={running} /> : null}
      <ChatQuotaHint id={`${id}-count`} text={hint} />
      <ChatQuotaHint id={`${id}-length`} quiet text={length} />
    </>,
    describedBy: [limit ? `${id}-limit` : '', hint ? `${id}-count` : '', length ? `${id}-length` : ''].filter(Boolean).join(' ') || undefined,
  };
}
