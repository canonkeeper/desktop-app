import { useEffect, useRef } from 'react';

/**
 * Where the focus goes after the author changes the page (specs/2026-11-10-ux1-shell.md SH2): every shell marks its content —
 * the page without its top bar and its column of sections — `data-page-content`, and the heading of a page with one
 * `data-page-heading`; the root moves the focus there by these marks, not each shell by itself.
 */

/** How long a new page may take to stand and a closing menu to close before the focus is left where it is. */
const settleMs = 2000;

/** Not hidden: React hides the page it leaves behind a loading one with an inline `display: none`. */
function shown(element: HTMLElement) {
  for (let node: HTMLElement | null = element; node; node = node.parentElement) {
    if (node.hidden || node.style.display === 'none') return false;
  }
  return true;
}

/** The content of the page on the screen: a hidden page left by the change is not it. */
function pageContent() {
  return [...document.querySelectorAll<HTMLElement>('[data-page-content]')].find(shown) ?? null;
}

/** The heading of the page on the screen: the one shell keeps its content, and a page left by the change may stay hidden in it. */
function pageHeading(content: HTMLElement) {
  return [...content.querySelectorAll<HTMLElement>('[data-page-heading]')].find(shown) ?? null;
}

function focusInto(content: HTMLElement) {
  (pageHeading(content) ?? content).focus({ preventScroll: true });
}

/** «К содержимому»: the heading of the page, or its content without one; false while the page is not there. */
export function focusPageContent() {
  const content = pageContent();
  if (!content) return false;
  focusInto(content);
  return true;
}

/** A dialog or a drawer above the page keeps the focus: the page under it is inert. */
function overlayOpen() {
  return Boolean(document.querySelector('[aria-modal="true"]'));
}

/**
 * After a change of the path (SH2.2) — or of the object the page opens, a chapter of «Письмо» opened from its list by the
 * parameter `draft` (specs/2026-11-11-ux9-writing-a.md WA1.4) — the focus stands on the heading of the new page, or on its
 * content. Not on the first load, not when only the other parameters of the address change (a tab, a filter), not while a
 * dialog or a drawer stays open, and not when the
 * focus is already inside the new page. A menu that is closing is waited for: the focus comes after it (SH2.4). The content
 * of the one shell stays from page to page: while the new page loads the focus waits on the content, and goes on to the
 * heading once the page shows it, unless the author has moved it.
 */
export function usePageFocus(path: string, object?: string | null) {
  const page = object ? `${path}\u0000${object}` : path;
  const shown = useRef(page);
  useEffect(() => {
    if (shown.current === page) return undefined;
    shown.current = page;
    const started = performance.now();
    let frame = 0;
    /** The focus stands on the content and waits there for the heading of the new page. */
    let parked = false;
    const later = () => {
      if (performance.now() - started < settleMs) frame = requestAnimationFrame(settle);
    };
    const settle = () => {
      const content = pageContent();
      const active = document.activeElement;
      if (parked) {
        // The author who moves the focus himself ends the wait: it is not taken back.
        if (!content || active !== content) return;
        const heading = pageHeading(content);
        if (heading) heading.focus({ preventScroll: true });
        else later();
        return;
      }
      if (!content || overlayOpen()) {
        later();
        return;
      }
      if (active !== content && content.contains(active)) return;
      const heading = pageHeading(content);
      if (heading) {
        heading.focus({ preventScroll: true });
        return;
      }
      content.focus({ preventScroll: true });
      parked = true;
      later();
    };
    frame = requestAnimationFrame(settle);
    return () => cancelAnimationFrame(frame);
  }, [page]);
}
