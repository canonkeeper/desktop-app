import { BookOpen, LocateFixed, MessageCircleQuestion, X } from 'lucide-react';
import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react';
import { isApiStatusFailure } from '../../entities/api-errors';
import type { ChatApiClient, SourceView } from '../../entities/chat/api';
import { Button, ButtonLink } from '../../ui/controls/Button';
import { IconButton } from '../../ui/controls/IconButton';
import { toHref } from '../../shared/navigation/app-route';
import { Alert } from '../../ui/feedback/Alert';
import { Skeleton } from '../../ui/feedback/Skeleton';
import { SegmentedControl } from '../../ui/navigation/SegmentedControl';
import { Drawer } from '../../ui/overlays/Drawer';
import { ReaderParagraph, ReaderPassage } from '../../ui/ReaderParagraph';
import { useChatAttachmentQuery, useChatChapterKeyQuery, useChatSourceChapterQuery } from './api';
import { attachmentNumber } from '../../shared/navigation/chat-source';
import { paragraphsLabel } from '../../shared/text/place';
import { phraseMarks } from './answer-phrase';
import type { ChatSourceNavigation } from './use-chat-source-navigation';
import styles from './ResearchChatSourceInspector.module.css';

interface SourceInspectorProps {
  api: Pick<ChatApiClient, 'resolveChatChapterKey' | 'getChapter' | 'getChatAttachment'>;
  chatId: string;
  projectId: string;
  sourceView: SourceView;
  navigation: ChatSourceNavigation;
  /** The question of the chat: «← К чату «…»» of the reader names it. */
  chatTitle?: string | undefined;
  /** «Спросить, откуда это» of a link to a place the book does not have: the question goes into the field (AA1.6). */
  onAskWhere?: (() => void) | undefined;
  /** A panel over the page at any width: «Письмо» has no column for it (specs/2026-11-11-ux9-writing-b.md WB1.6). */
  modal?: boolean;
  /** What the panel is named, by the place it opened from. */
  label?: string;
}

/**
 * Shows the chapter a chat answer cites, beside the chat, with the cited paragraphs highlighted — a chapter of the book,
 * or the chapter of the author attached to a question (specs/2026-11-07-chat-attach-chapter.md CA4.5), signed as his.
 */
export function ResearchChatSourceInspector({ api, chatId, chatTitle, label = 'Место из ответа', modal: always = false, onAskWhere, projectId, sourceView, navigation }: SourceInspectorProps) {
  const target = navigation.target!;
  const [narrow, setModal] = useState(() => window.innerWidth < 1100);
  const modal = always || narrow;
  const bodyRef = useRef<HTMLDivElement>(null);
  const headingRef = useRef<HTMLHeadingElement>(null);
  const closeRef = useRef(navigation.close);
  closeRef.current = navigation.close;
  const attachment = attachmentNumber(target);
  const ofBook = attachment === null;
  const location = useChatChapterKeyQuery(api, chatId, target.chapterKey, ofBook);
  const chapter = useChatSourceChapterQuery(api, ofBook ? location.data?.chapterId : undefined, sourceView);
  const attached = useChatAttachmentQuery(api, chatId, attachment);
  const failed = ofBook ? location.error ?? chapter.error : attached.error;
  const unavailable = isApiStatusFailure(failed) && [401, 403, 404].includes(failed.status);
  // A link to a place the book does not have: most often the helper's mistake, not a deletion (AA1.6, Ч-42).
  const notFound = ofBook && isApiStatusFailure(failed) && failed.status === 404;
  const loaded = ofBook ? chapter.data : attached.data;
  const paragraphs = ofBook
    ? [...(chapter.data?.paragraphs ?? [])].sort((left, right) => left.order - right.order).map(({ id, markdown, text }): { id: string; text: string; markdown?: string } => ({ id, text, markdown }))
    : (attached.data?.paragraphs ?? []).map((text, index): { id: string; text: string; markdown?: string } => ({ id: `${target.chapterKey}-${index + 1}`, text }));
  const ranged = target.from !== undefined;
  const from = target.from ?? 0;
  const to = target.to ?? from;
  const mode = target.mode ?? (ranged ? 'fragment' : 'chapter');
  const cited = (position: number) => ranged && position >= from && position <= to;
  const citedIds = paragraphs.filter((_, index) => cited(index + 1)).map((paragraph) => paragraph.id);
  const rangeMissing = ranged && Boolean(loaded) && citedIds.length === 0;
  // The chapter is named by its title; its key is the address of the model and is never shown (ux1-names N2.5).
  const heading = notFound ? 'Место не найдено' : ofBook ? location.data?.title ?? 'Глава' : attached.data?.title || 'Глава без названия';
  const range = ranged ? paragraphsLabel(from, to) : '';
  const meta = notFound ? '' : [ofBook ? location.data?.bookTitle : 'Глава автора, в книге её ещё нет', range].filter(Boolean).join(' · ');

  useEffect(() => {
    const resize = () => setModal(window.innerWidth < 1100);
    window.addEventListener('resize', resize);
    return () => window.removeEventListener('resize', resize);
  }, []);
  useLayoutEffect(() => {
    if (!modal) headingRef.current?.focus();
    return () => {
      if (!navigation.returnFocusRef.current?.isConnected) {
        navigation.returnFocusRef.current = document.querySelector<HTMLElement>('[data-chat-heading]');
      }
      // Modal Overlay restores focus after releasing the inert background.
      if (!modal) navigation.returnFocusRef.current?.focus();
    };
  }, [modal, navigation.returnFocusRef]);

  function scrollToPassage() {
    const first = citedIds[0];
    const element = first ? [...(bodyRef.current?.querySelectorAll<HTMLElement>('[data-paragraph-id]') ?? [])].find((node) => node.dataset.paragraphId === first) : undefined;
    element?.scrollIntoView({ block: 'center' });
  }
  // Scroll once per loaded chapter, mode or cited range.
  useLayoutEffect(() => {
    if (mode === 'chapter' && citedIds.length) scrollToPassage();
    else if (bodyRef.current) bodyRef.current.scrollTop = 0;
  }, [loaded, mode, from, to]);

  const content: ReactNode[] = [];
  let passage: ReactNode[] = [];
  const flush = () => {
    if (passage.length) content.push(<ReaderPassage key={`passage-${content.length}`}>{passage}</ReaderPassage>);
    passage = [];
  };
  paragraphs.forEach((paragraph, index) => {
    const isCited = cited(index + 1);
    // «Это место» is the cited paragraphs with one quiet paragraph before and after them (AA1.7, Ч-43).
    const beside = index + 1 === from - 1 || index + 1 === to + 1;
    if (mode === 'fragment' && !isCited && !beside && !rangeMissing) return;
    // The words of the answer inside a cited paragraph (specs/2026-11-11-ux9-shelf-chat.md SC3.7, J-25).
    const marks = isCited ? phraseMarks(paragraph.text, navigation.phrase) : [];
    const node = <ReaderParagraph id={paragraph.id} key={paragraph.id} text={paragraph.text} size="compact" muted={ranged && !rangeMissing && !isCited}
      {...(paragraph.markdown === undefined ? {} : { markdown: paragraph.markdown })} {...(marks.length ? { marks } : {})} />;
    if (isCited) passage.push(node);
    else { flush(); content.push(node); }
  });
  flush();

  const readerQuery = ofBook && location.data ? {
    projectId, bookId: location.data.bookId, chapterId: location.data.chapterId, mode: 'read',
    ...(citedIds[0] ? { paragraphId: citedIds[0] } : {}),
  } : undefined;
  const readerHref = readerQuery ? toHref('/manuscript/books', readerQuery) : undefined;
  const loading = !failed && (ofBook ? location.isPending || chapter.isPending : attached.isPending);

  const body = <div className={styles.inspector} onKeyDown={(event) => {
    if (!modal && event.key === 'Escape') { event.stopPropagation(); closeRef.current(); }
  }}>
    <header className={styles.header}>
      <div className={styles.topbar}>
        <h2 ref={headingRef} tabIndex={-1} title={heading}>{heading}</h2>
        {/* The same tab, a word on the button; the reader leads back to this answer (W-5B, W-63; PS4.1). */}
        {readerQuery && readerHref && !unavailable ? <ButtonLink className={styles.openInBook} href={readerHref} onClick={(event) => {
          if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
          event.preventDefault();
          navigation.openInBook(readerQuery, chatTitle);
        }} size="touch" variant="ghost"><BookOpen aria-hidden="true" size={16} />Открыть в книге</ButtonLink> : null}
        <IconButton aria-label="Закрыть место из ответа" onClick={navigation.close} size="touch"><X aria-hidden="true" size={16} /></IconButton>
      </div>
      {meta ? <small className={styles.bookLabel} title={meta}>{meta}</small> : null}
      {ranged && !rangeMissing && loaded ? <div className={styles.modeRow}>
        <SegmentedControl fullWidth size="touch" label="Сколько показать" onValueChange={navigation.setMode} options={[{ value: 'fragment', label: 'Это место' }, { value: 'chapter', label: 'Вся глава' }]} value={mode} />
        {mode === 'chapter' ? <IconButton aria-label="К этому месту" title="К этому месту" onClick={scrollToPassage} size="touch"><LocateFixed aria-hidden="true" size={16} /></IconButton> : null}
      </div> : null}
    </header>
    <div className={styles.body} ref={bodyRef} role="document" aria-label="Текст главы">
      {notFound ? <>
        <Alert>Не нашёл это место в книге. Возможно, помощник ошибся ссылкой или глава изменилась.</Alert>
        {onAskWhere ? <Button className={styles.askWhere} onClick={onAskWhere} size="touch" variant="outline">
          <MessageCircleQuestion aria-hidden="true" size={16} />Спросить, откуда это
        </Button> : null}
      </> : unavailable ? <Alert>Это место недоступно. Возможно, глава удалена или у вас нет доступа.</Alert>
        : loading ? <div aria-label="Загрузка главы" role="status"><Skeleton style={{ height: 80 }} /><Skeleton style={{ height: 120, marginTop: 16 }} /></div>
          : failed ? <><Alert tone="error">Не удалось загрузить главу.</Alert><Button onClick={() => {
            if (!ofBook) void attached.refetch();
            else { void location.refetch(); void chapter.refetch(); }
          }} size="touch">Повторить</Button></>
            : <>{rangeMissing ? <Alert>В текущем тексте главы {to !== from ? `нет абзацев ${from}–${to}` : `нет абзаца ${from}`}. Показана вся глава.</Alert> : null}{content}</>}
    </div>
  </div>;

  return modal
    // The panel lives in the address (`source=`): its own «Назад» closes it (specs/2026-11-10-ux2-patterns-actions.md PA4.2).
    ? <Drawer canDismiss history={false} initialFocusRef={headingRef} label={label} onDismiss={navigation.close} returnFocusRef={navigation.returnFocusRef} side="right" size="reading">{body}</Drawer>
    : <aside aria-label={label} className={styles.aside}>{body}</aside>;
}
