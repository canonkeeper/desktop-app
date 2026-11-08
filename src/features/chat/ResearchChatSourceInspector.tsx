import { ExternalLink, LocateFixed, X } from 'lucide-react';
import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react';
import { isApiStatusFailure } from '../../entities/api-errors';
import type { ChatApiClient, SourceView } from '../../entities/chat/api';
import { Button } from '../../ui/controls/Button';
import { IconButton, IconLink } from '../../ui/controls/IconButton';
import { toHref } from '../../shared/navigation/app-route';
import { Alert } from '../../ui/feedback/Alert';
import { Skeleton } from '../../ui/feedback/Skeleton';
import { SegmentedControl } from '../../ui/navigation/SegmentedControl';
import { Drawer } from '../../ui/overlays/Drawer';
import { ReaderParagraph, ReaderPassage } from '../../ui/ReaderParagraph';
import { useChatAttachmentQuery, useChatChapterKeyQuery, useChatSourceChapterQuery } from './api';
import { attachmentNumber } from '../../shared/navigation/chat-source';
import type { ChatSourceNavigation } from './use-chat-source-navigation';
import styles from './ResearchChatSourceInspector.module.css';

interface SourceInspectorProps {
  api: Pick<ChatApiClient, 'resolveChatChapterKey' | 'getChapter' | 'getChatAttachment'>;
  chatId: string;
  projectId: string;
  sourceView: SourceView;
  navigation: ChatSourceNavigation;
}

/**
 * Shows the chapter a chat answer cites, beside the chat, with the cited paragraphs highlighted — a chapter of the book,
 * or the chapter of the author attached to a question (specs/2026-11-07-chat-attach-chapter.md CA4.5), signed as his.
 */
export function ResearchChatSourceInspector({ api, chatId, projectId, sourceView, navigation }: SourceInspectorProps) {
  const target = navigation.target!;
  const [modal, setModal] = useState(() => window.innerWidth < 1100);
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
  const loaded = ofBook ? chapter.data : attached.data;
  const paragraphs = ofBook
    ? [...(chapter.data?.paragraphs ?? [])].sort((left, right) => left.order - right.order).map(({ id, text }) => ({ id, text }))
    : (attached.data?.paragraphs ?? []).map((text, index) => ({ id: `${target.chapterKey}-${index + 1}`, text }));
  const ranged = target.from !== undefined;
  const from = target.from ?? 0;
  const to = target.to ?? from;
  const mode = target.mode ?? (ranged ? 'fragment' : 'chapter');
  const cited = (position: number) => ranged && position >= from && position <= to;
  const citedIds = paragraphs.filter((_, index) => cited(index + 1)).map((paragraph) => paragraph.id);
  const rangeMissing = ranged && Boolean(loaded) && citedIds.length === 0;
  const heading = ofBook ? location.data?.title ?? `Глава ${target.chapterKey}` : attached.data?.title || 'Глава без названия';
  const range = ranged ? `¶${from}${to !== from ? `–${to}` : ''}` : '';
  const meta = [ofBook ? location.data?.bookTitle : 'Глава автора, в книге её ещё нет', range].filter(Boolean).join(' · ');

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
    if (mode === 'fragment' && !isCited && !rangeMissing) return;
    const node = <ReaderParagraph id={paragraph.id} key={paragraph.id} text={paragraph.text} size="compact" muted={ranged && !rangeMissing && !isCited} />;
    if (isCited) passage.push(node);
    else { flush(); content.push(node); }
  });
  flush();

  const readerHref = ofBook && location.data ? toHref('/manuscript/books', {
    projectId, bookId: location.data.bookId, chapterId: location.data.chapterId, mode: 'read',
    ...(citedIds[0] ? { paragraphId: citedIds[0] } : {}),
  }) : undefined;
  const loading = !failed && (ofBook ? location.isPending || chapter.isPending : attached.isPending);

  const body = <div className={styles.inspector} onKeyDown={(event) => {
    if (!modal && event.key === 'Escape') { event.stopPropagation(); closeRef.current(); }
  }}>
    <header className={styles.header}>
      <div className={styles.topbar}>
        <h2 ref={headingRef} tabIndex={-1} title={heading}>{heading}</h2>
        {readerHref && !unavailable ? <IconLink
          aria-label="Открыть в читалке (в новой вкладке)" title="Открыть в читалке (в новой вкладке)"
          href={readerHref} target="_blank" rel="noopener noreferrer" size="touch"><ExternalLink aria-hidden="true" size={16} /></IconLink> : null}
        <IconButton aria-label="Закрыть источник" onClick={navigation.close} size="touch"><X aria-hidden="true" size={16} /></IconButton>
      </div>
      <small className={styles.bookLabel} title={meta}>{meta}</small>
      {ranged && !rangeMissing && loaded ? <div className={styles.modeRow}>
        <SegmentedControl fullWidth size="touch" label="Объём источника" onValueChange={navigation.setMode} options={[{ value: 'fragment', label: 'Фрагмент' }, { value: 'chapter', label: 'Вся глава' }]} value={mode} />
        {mode === 'chapter' ? <IconButton aria-label="К выделенному фрагменту" title="К выделенному фрагменту" onClick={scrollToPassage} size="touch"><LocateFixed aria-hidden="true" size={16} /></IconButton> : null}
      </div> : null}
    </header>
    <div className={styles.body} ref={bodyRef} role="document" aria-label="Текст источника">
      {unavailable ? <Alert>Источник недоступен. Возможно, глава удалена или у вас нет доступа.</Alert>
        : loading ? <div aria-label="Загрузка источника" role="status"><Skeleton style={{ height: 80 }} /><Skeleton style={{ height: 120, marginTop: 16 }} /></div>
          : failed ? <><Alert tone="error">Не удалось загрузить источник.</Alert><Button onClick={() => {
            if (!ofBook) void attached.refetch();
            else { void location.refetch(); void chapter.refetch(); }
          }} size="touch">Повторить</Button></>
            : <>{rangeMissing ? <Alert>В текущем тексте главы нет абзацев ¶{from}{to !== from ? `–${to}` : ''}. Показана вся глава.</Alert> : null}{content}</>}
    </div>
  </div>;

  return modal
    ? <Drawer canDismiss initialFocusRef={headingRef} label="Источник ответа" onDismiss={navigation.close} returnFocusRef={navigation.returnFocusRef} side="right" size="reading">{body}</Drawer>
    : <aside aria-label="Источник ответа" className={styles.aside}>{body}</aside>;
}
