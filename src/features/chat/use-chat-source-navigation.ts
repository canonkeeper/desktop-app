import { useRef } from 'react';
import { toHref, type AppRoute, type RouteNavigate } from '../../shared/navigation/app-route';
import { chatSourceQuery, parseChatSourceTarget, type ChatSourceTarget } from '../../shared/navigation/chat-source';

export function useChatSourceNavigation(route: AppRoute, navigate: RouteNavigate) {
  const returnFocusRef = useRef<HTMLElement | null>(null);
  const target = parseChatSourceTarget(route.query);
  const baseQuery = Object.fromEntries([...route.query.entries()].filter(([key]) => key !== 'source' && key !== 'sourceMode'));
  const baseHref = toHref('/chat', baseQuery);

  return {
    target,
    returnFocusRef,
    open(source: ChatSourceTarget, trigger: HTMLElement) {
      if (!target) returnFocusRef.current = trigger;
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
    setMode(mode: NonNullable<ChatSourceTarget['mode']>) {
      if (target) navigate('/chat', { ...baseQuery, ...chatSourceQuery({ ...target, mode }) }, { replace: true });
    },
  };
}

export type ChatSourceNavigation = ReturnType<typeof useChatSourceNavigation>;
