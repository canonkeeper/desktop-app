import { useQueryClient } from '@tanstack/react-query';
import { FileText, Paperclip, Plus } from 'lucide-react';
import { useRef, type ReactNode } from 'react';
import { loadFailureWords } from '../../../entities/api-errors';
import { manuscriptQueryKeys, textSourceView, type ReaderSourceClient } from '../../../entities/manuscript/api';
import { fetchSagaChapterRef, type SagaApiClient } from '../../../entities/saga/api';
import { useSagaTranscriptQuery, type SagaTranscriptClient, type SagaTranscriptSummary, type SagaTranscriptTurn } from '../../../entities/saga-file/api';
import type { RouteNavigate } from '../../../shared/navigation/app-route';
import type { SourceCitation } from '../../../shared/navigation/chat-source';
import { useNotes } from '../../../shared/notes/notes-context';
import { Button } from '../../../ui/controls/Button';
import { SourceLink } from '../../../ui/controls/SourceLink';
import { Alert } from '../../../ui/feedback/Alert';
import { ErrorBlock } from '../../../ui/feedback/ErrorBlock';
import { SkeletonArea } from '../../../ui/feedback/Skeleton';
import { useToasts } from '../../../ui/feedback/useToastQueue';
import { MarkdownContent } from '../../../ui/MarkdownContent';
import { TextSelectionMenu } from '../../../ui/TextSelectionMenu';
import { chatDate } from '../chat-days';
import { useMessageSelection } from '../ChatNoteAction';
import feed from '../feed/ChatFeed.module.css';
import list from '../ResearchChatSessions.module.css';
import { fileChatWords as words, turnEndWords } from './file-chat-words';
import styles from './FileChats.module.css';

/**
 * «Из файла саги» of the list of chats (specs/2026-11-13-desk-sagafile-web.md SW3.1; board 44·38, the part `From File` of
 * the master of the days of the chats): under the chats of the saga, the chats a saga file brought, each by its name and
 * the day of its last question. A saga without them has no such part.
 */
export function FileChatsSection({ chats, onOpen, selectedId }: {
  chats: readonly SagaTranscriptSummary[];
  onOpen: (transcriptId: string) => void;
  selectedId: string;
}) {
  if (chats.length === 0) return null;
  const now = new Date();
  return (
    <section aria-label={words.section} className={list.group}>
      <p aria-hidden="true" className={list.groupLabel}>{words.section}</p>
      {chats.map((chat) => (
        <div className={list.sessionRow} data-selected={chat.id === selectedId || undefined} key={chat.id}>
          <button aria-current={chat.id === selectedId ? 'page' : undefined} className={list.sessionSelect} onClick={() => onOpen(chat.id)}
            title={chat.title} type="button">
            <FileText aria-hidden="true" className={list.sessionIcon} size={16} />
            <span className={list.sessionTitle}>{chat.title}</span>
          </button>
          <span aria-hidden="true" className={list.sessionMeta}>{chatDate(new Date(chat.lastTurnAt), now)}</span>
        </div>
      ))}
    </section>
  );
}

type FileChatApi = SagaTranscriptClient & SagaApiClient & ReaderSourceClient;

/**
 * A place an answer of a chat of a saga file cites opens in the book (SW3.2): the key is read from the contents of the saga
 * now, the paragraph from the text of its chapter; the reader offers «← К чату «…»» back.
 */
function useOpenPlace(api: FileChatApi, projectId: string, title: string, navigate: RouteNavigate) {
  const queryClient = useQueryClient();
  const toasts = useToasts();
  return (citation: SourceCitation) => {
    void (async () => {
      const chapter = await fetchSagaChapterRef(queryClient, api, projectId, textSourceView, citation.chapterKey);
      if (!chapter) {
        toasts.push({ tone: 'warning', message: 'Этой главы в тексте саги сейчас нет.' });
        return;
      }
      let paragraphId: string | undefined;
      if (citation.from) {
        const text = await queryClient.fetchQuery({
          queryKey: manuscriptQueryKeys.sourceChapter(chapter.chapterId),
          queryFn: ({ meta }) => (meta as { reader: ReaderSourceClient }).reader.getChapter(chapter.chapterId, textSourceView),
          meta: { reader: api },
          staleTime: 30_000,
        });
        paragraphId = [...text.paragraphs].sort((left, right) => left.order - right.order)[citation.from - 1]?.id;
      }
      navigate('/manuscript/books', {
        projectId, bookId: chapter.bookId, chapterId: chapter.chapterId, mode: 'read', ...(paragraphId ? { paragraphId } : {}),
      }, { from: { section: 'chat', name: title } });
    })().catch(() => toasts.push({ tone: 'error', message: 'Не удалось открыть главу. Попробуйте ещё раз.' }));
  };
}

/**
 * An answer of a chat of a saga file, shown as the answer of a chat (SW3.2): the links to places in the words the server
 * named them; a note of a selection of it as in the chat, without a turn (SW3.3) — a note of its words.
 */
function FileAnswer({ answer, places, renderSourceLink }: {
  answer: string;
  places: Record<string, string>;
  renderSourceLink: (source: SourceCitation, label: ReactNode) => ReactNode;
}) {
  const notes = useNotes();
  const content = useRef<HTMLDivElement>(null);
  const { selection, clear } = useMessageSelection(content);
  const finish = (run: () => void) => {
    window.getSelection()?.removeAllRanges();
    clear();
    run();
  };
  return (
    <section className={feed.answer} data-answer="">
      <div ref={content}>
        <MarkdownContent className={feed.markdown} places={places} renderSourceLink={renderSourceLink}>{answer}</MarkdownContent>
      </div>
      {selection ? (
        <TextSelectionMenu onDismiss={clear} rect={selection.rect}
          onAddNote={notes ? () => finish(() => notes.capture({ body: selection.body })) : undefined}
          onCopy={() => finish(() => { void navigator.clipboard?.writeText(selection.body).catch(() => undefined); })} />
      ) : null}
    </section>
  );
}

function FileTurn({ places, renderSourceLink, turn }: {
  places: Record<string, string>;
  renderSourceLink: (source: SourceCitation, label: ReactNode) => ReactNode;
  turn: SagaTranscriptTurn;
}) {
  const end = turnEndWords(turn);
  return (
    <article aria-label="Вопрос и ответ" className={feed.turn}>
      <div aria-label="Вопрос автора" className={feed.question} role="note"><p>{turn.question}</p></div>
      {turn.attached ? (
        <p className={styles.line}><Paperclip aria-hidden="true" size={14} />{words.attached(turn.attached)}</p>
      ) : null}
      {end ? <p className={styles.line}>{end}</p> : <FileAnswer answer={turn.answer ?? ''} places={places} renderSourceLink={renderSourceLink} />}
    </article>
  );
}

/**
 * A chat of a saga file opened (SW3.2, SW3.3; board 44·38, `Pattern/Desktop/File Chat`): over the feed the line that it
 * came from a file and cannot go on, with «Новый чат»; the questions and answers as the chat shows them, the turns without an
 * answer in words, and under them the questions of the others the file did not carry. No field, no questions after the
 * answer, no «Запомнить», no correction or stop: nothing here asks the helper.
 */
export function FileChatView({ api, navigate, onNewChat, projectId, transcriptId }: {
  api: FileChatApi;
  navigate: RouteNavigate;
  onNewChat: () => void;
  projectId: string;
  transcriptId: string;
}) {
  const chat = useSagaTranscriptQuery(api, projectId, transcriptId);
  const openPlace = useOpenPlace(api, projectId, chat.data?.title ?? '', navigate);
  const renderSourceLink = (source: SourceCitation, label: ReactNode) => (
    <SourceLink onClick={() => openPlace(source)} title="Открыть место в книге">{label}</SourceLink>
  );
  if (chat.isPending) return <div className={styles.view}><SkeletonArea shape="cards" /></div>;
  if (chat.isError) {
    return (
      <div className={styles.view}>
        <ErrorBlock {...loadFailureWords(chat.error)} onRetry={() => void chat.refetch()} retrying={chat.isRefetching} title="Не открылся чат" />
      </div>
    );
  }
  const others = words.others(chat.data.othersTurns);
  return (
    <div className={styles.view}>
      <div className={feed.column}>
        <Alert aria-label={words.notice} className={styles.notice} role="note" tone="info">
          <span>{words.notice}</span>
          <Button onClick={onNewChat} size="sm" variant="outline"><Plus aria-hidden="true" size={14} />{words.newChat}</Button>
        </Alert>
        {chat.data.turns.map((turn, index) => (
          <FileTurn key={`${turn.askedAt}:${index}`} places={chat.data.places} renderSourceLink={renderSourceLink} turn={turn} />
        ))}
        {others ? <p className={styles.line}>{others}</p> : null}
      </div>
    </div>
  );
}
