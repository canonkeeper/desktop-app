import { useRef, useState } from 'react';
import { toHref, type AppRoute, type RouteNavigate } from '../../shared/navigation/app-route';
import { chatSourceQuery, parseChatSourceTarget, type ChatSourceTarget } from '../../shared/navigation/chat-source';

export function useChatSourceNavigation(route: AppRoute, navigate: RouteNavigate) {
  const returnFocusRef = useRef<HTMLElement | null>(null);
  const target = parseChatSourceTarget(route.query);
  // The words of the answer around the link opened, for the place it cites (specs/2026-11-11-ux9-shelf-chat.md SC3.7): an
  // ephemeral fact of this tab, kept by the source it belongs to; a source of the address alone has none.
  const [said, setSaid] = useState<{ source: string; words: string } | null>(null);
  const phrase = target && said?.source === chatSourceQuery(target).source ? said.words : null;
  const baseQuery = Object.fromEntries([...route.query.entries()].filter(([key]) => key !== 'source' && key !== 'sourceMode'));
  const baseHref = toHref('/chat', baseQuery);

  return {
    target,
    returnFocusRef,
    phrase,
    open(source: ChatSourceTarget, trigger: HTMLElement) {
      if (!target) returnFocusRef.current = trigger;
      const words = trigger.closest('p, li, td, blockquote')?.textContent ?? '';
      setSaid(words ? { source: chatSourceQuery(source).source, words } : null);
      navigate('/chat', { ...baseQuery, ...chatSourceQuery(source) }, {
        replace: Boolean(target),
        ...(!target ? { sourceReturnHref: baseHref } : {}),
      });
    },
    close() {
      if (!returnFocusRef.current?.isConnected) returnFocusRef.current = document.querySelector<HTMLElement>('[data-chat-heading]');
      if (route.sourceReturnHref === baseHref) navigate('/chat', baseQuery, { back: true });
      else navigate('/chat', baseQuery, { replace: true });
    },
    /**
     * «Открыть в книге»: the chapter in the reader of the same tab; the reader offers «← К чату «…»» back to the answer
     * with its source open (specs/2026-11-10-ux2-patterns-states.md PS4.1, PS4.2).
     */
    openInBook(query: Record<string, string | undefined>, chatTitle?: string) {
      navigate('/manuscript/books', query, { from: { section: 'chat', ...(chatTitle ? { name: chatTitle } : {}) } });
    },
    setMode(mode: NonNullable<ChatSourceTarget['mode']>) {
      if (target) navigate('/chat', { ...baseQuery, ...chatSourceQuery({ ...target, mode }) }, { replace: true });
    },
  };
}

export type ChatSourceNavigation = ReturnType<typeof useChatSourceNavigation>;
