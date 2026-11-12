import { useEffect, useState } from 'react';
import { usePersonalNote, type NotesApiClient } from '../../entities/notes/api';
import type { AppRoute, RouteNavigate } from '../../shared/navigation/app-route';
import { Alert } from '../../ui/feedback/Alert';
import { Button } from '../../ui/controls/Button';
import styles from './NotesPanel.module.css';

/**
 * DOM ranges preserve Markdown rendering; no HTML replacement or timeline copy. Noted text taken
 * before an answer showed the addresses of its links (`data-link-address`) is matched without them.
 */
export function quoteRanges(content: HTMLElement, quote: string) {
  if (!quote) return [];
  const textNodes: Node[] = [];
  const walker = document.createTreeWalker(content, NodeFilter.SHOW_TEXT);
  while (walker.nextNode()) textNodes.push(walker.currentNode);
  const found = rangesOf(textNodes, quote);
  return found.length > 0
    ? found
    : rangesOf(textNodes.filter((node) => !node.parentElement?.closest('[data-link-address]')), quote);
}

function rangesOf(textNodes: readonly Node[], quote: string) {
  const text = textNodes.map((node) => node.textContent ?? '').join('');
  const ranges: Range[] = [];
  for (let offset = text.indexOf(quote); offset >= 0; offset = text.indexOf(quote, offset + quote.length)) {
    let position = 0;
    let begin: { node: Node; offset: number } | undefined;
    for (const node of textNodes) {
      const end = position + (node.textContent?.length ?? 0);
      if (!begin && offset >= position && offset < end) begin = { node, offset: offset - position };
      if (begin && offset + quote.length <= end) {
        const range = document.createRange();
        range.setStart(begin.node, begin.offset);
        range.setEnd(node, offset + quote.length - position);
        ranges.push(range);
        break;
      }
      position = end;
    }
  }
  return ranges;
}

/** A note on a narration line of a finished turn lands inside its folded «Как я искал». */
export function revealFolded(element: HTMLElement) {
  for (let fold = element.closest('details'); fold; fold = fold.parentElement?.closest('details') ?? null) fold.open = true;
}

export function ChatNoteLanding({ api, accountId, route, navigate }: {
  api: NotesApiClient;
  accountId: string;
  route: AppRoute;
  navigate: RouteNavigate;
}) {
  const noteId = route.query.get('noteId') ?? '';
  const chatId = route.query.get('chatId');
  const query = usePersonalNote(api, accountId, noteId);
  const source = query.data?.source;
  const [located, setLocated] = useState(false);
  const [quoteMissing, setQuoteMissing] = useState(false);
  useEffect(() => {
    setLocated(false); setQuoteMissing(false);
    if (source?.kind !== 'chat' || source.chatId !== chatId) return;
    let target: HTMLElement | undefined;
    const locate = () => {
      if (target) return;
      const section = [...document.querySelectorAll<HTMLElement>('[data-activity-kind="assistant"]')].find((node) => node.dataset.providerTurn === source.providerTurnId && node.dataset.providerItem === source.providerItemId);
      const content = section?.querySelector<HTMLElement>('[data-assistant-content]');
      if (!section || !content) return;
      target = section;
      const ranges = quoteRanges(content, source.quote);
      target.dataset.noteTarget = '';
      revealFolded(target);
      target.scrollIntoView?.({ block: 'center' });
      target.focus({ preventScroll: true });
      if (typeof CSS !== 'undefined' && 'highlights' in CSS && typeof Highlight !== 'undefined' && ranges.length) CSS.highlights.set('note-source', new Highlight(...ranges));
      setQuoteMissing(ranges.length === 0);
      setLocated(true);
    };
    locate();
    const observer = new MutationObserver(locate);
    observer.observe(document.body, { childList: true, subtree: true });
    return () => { observer.disconnect(); if (target) delete target.dataset.noteTarget; if (typeof CSS !== 'undefined' && 'highlights' in CSS) CSS.highlights.delete('note-source'); };
  }, [chatId, source]);
  function dismiss() {
    const query = Object.fromEntries(route.query);
    delete query.noteId;
    navigate('/chat', query, { replace: true });
  }
  if (located && !quoteMissing) return null;
  const wrongChat = source && (source.kind !== 'chat' || source.chatId !== chatId);
  return <div className={styles.landing}><Alert tone={query.isError || wrongChat ? 'error' : 'info'}>
    {query.isError ? 'Не удалось загрузить заметку.' : wrongChat ? 'Эта заметка не из этого чата.' : quoteMissing ? 'Сообщение найдено, но сохранённый текст не совпадает с текущим. Заметка не изменена.' : 'Ищем исходное сообщение. Если оно недоступно, сохранённый текст останется в заметке.'}
    {query.isError ? <Button onClick={() => void query.refetch()} variant="outline">Повторить</Button> : null}<Button onClick={dismiss} variant="ghost">Закрыть</Button>
  </Alert></div>;
}
