import { FileText, PanelRightOpen } from 'lucide-react';
import { createContext, useContext } from 'react';
import type { ChatAttachmentSummary } from '../../entities/chat/api';
import { attachmentKey } from '../../shared/navigation/chat-source';
import { Button } from '../../ui/controls/Button';
import { formatCount } from '../../ui/copy';
import { useChatSlot, type ChatAttachmentHost } from './chat-slot';
import styles from './ChatChapter.module.css';

const HostContext = createContext<ChatAttachmentHost | null>(null);
export const ChatAttachmentHostProvider = HostContext.Provider;

/** The card without the proofreading of the site: no button, no problem. */
function noProofread(_host: ChatAttachmentHost, _number: number) {
  return { action: null, problem: null };
}

/** The chapter of the author at his question (CA4.2): its title and words, its text beside the chat, its proofreading. */
export function ChatAttachmentCard({ attachment }: { attachment: ChatAttachmentSummary }) {
  const host = useContext(HostContext);
  if (!host) return null;
  return <Card attachment={attachment} host={host} />;
}

function Card({ attachment, host }: { attachment: ChatAttachmentSummary; host: ChatAttachmentHost }) {
  const useProofread = useChatSlot()?.proofread?.useProofread ?? noProofread;
  const proofread = useProofread(host, attachment.number);
  const title = attachment.title || 'Глава без названия';
  return (
    <section aria-label={`Приложенная глава «${title}»`} className={styles.card}>
      <div className={styles.cardHead}>
        <FileText aria-hidden="true" size={18} />
        <span className={styles.cardTitle}>
          <strong>{title}</strong>
          <small>Глава автора · {formatCount(attachment.words, 'слово', 'слова', 'слов')} · в книге её ещё нет</small>
        </span>
      </div>
      <div className={styles.cardActions}>
        <Button onClick={(click) => host.openSource({ chapterKey: attachmentKey(attachment.number), mode: 'chapter' }, click.currentTarget)}
          size="sm" variant="outline">
          <PanelRightOpen aria-hidden="true" size={15} />Показать текст
        </Button>
        {proofread.action}
      </div>
      {proofread.problem}
    </section>
  );
}
