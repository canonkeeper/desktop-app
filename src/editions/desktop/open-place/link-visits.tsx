// SPDX-License-Identifier: AGPL-3.0-only
import { useEffect, useRef, type ReactNode } from 'react';
import type { RouteNavigate } from '../../../shared/navigation/app-route';
import { openPlacePath } from './OpenPlace';

/**
 * The event by which the shell gives the loaded page the place of a link (specs/2026-11-16-desk-app-links.md AL1.4;
 * desktop/src-tauri/crates/core/src/visit.js): its detail is the address of the page of the edition the core of the shell
 * built of the checked values of the link.
 */
export const linkEvent = 'canonkeeper:link';

/** The address of the place of a link, of exactly the form of the shell — the same as in visit.js and exchange.js. */
const place = /^\/manuscript\/open\?(?:saga=[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}&chapter=[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}(?:&p=[1-9][0-9]{0,4})?(?:&rev=[1-9][0-9]{0,15})?|refused=1)$/u;

/** The place a link gave before the pages were there to follow it, and the way to follow one while they are. */
let pending: string | null = null;
let follow: ((to: string) => void) | null = null;

/** Takes the place of a link of its form — the shell hears that the page took it — and follows it, now or once it can. */
function heard(event: Event) {
  const to: unknown = (event as CustomEvent<unknown>).detail;
  if (typeof to !== 'string' || !place.test(to)) return;
  event.preventDefault();
  if (follow) follow(to);
  else pending = to;
}

// The page listens from its first script: a link the shell gives once the page is loaded is not lost while the app still
// learns who its author is.
if (typeof window !== 'undefined') window.addEventListener(linkEvent, heard);

/**
 * The pages of the app follow the place of a link by the navigation of the router, as a link pressed inside the app
 * (AL1.4): a page with unsaved words asks «Не сохранять?» first, and «остаться» keeps the page and its address.
 */
export function LinkVisits({ children, navigate }: { children?: ReactNode; navigate: RouteNavigate }) {
  const latest = useRef(navigate);
  useEffect(() => {
    latest.current = navigate;
  });
  useEffect(() => {
    follow = (to) => latest.current(openPlacePath, Object.fromEntries(new URLSearchParams(to.slice(to.indexOf('?') + 1))));
    const waiting = pending;
    pending = null;
    if (waiting) follow(waiting);
    return () => {
      follow = null;
    };
  }, []);
  return children;
}
