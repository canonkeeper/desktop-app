import { createContext, useEffect } from 'react';

/**
 * The place of notices above the page: the toasts of every page stand there, in the flow of the product's surface,
 * so they cover neither a field nor a button nor a panel (the owner 04.11). Without it a page keeps them at the top.
 */
export const NoticeSlot = createContext<HTMLElement | null>(null);

/** The outermost scrollers of a page: an inner one moves with them and does not shrink on its own. */
function pageScrollers(node: Element): Element[] {
  return [...node.children].flatMap((child) => /auto|scroll/u.test(getComputedStyle(child).overflowY) && child.scrollHeight > child.clientHeight
    ? [child]
    : pageScrollers(child));
}

/**
 * A notice takes its height from the page: the scrollers take it up, so the text and the buttons under the reader stay
 * where they are when a notice comes and goes (specs/2026-11-03-narrow-windows-navigation.md N8.4). The observer runs
 * after layout and before paint, so no frame shows the page moved. When a notice goes, layout has already cut a scroller
 * at the bottom to its new end: that scroller stays at the end, so a feed held at its last line keeps it.
 */
export function useSteadyPageUnderNotices(slot: HTMLElement | null) {
  useEffect(() => {
    const surface = slot?.parentElement;
    if (!slot || !surface) return;
    let height = slot.offsetHeight;
    const observer = new ResizeObserver(() => {
      const change = slot.offsetHeight - height;
      height += change;
      if (!change) return;
      for (const scroller of pageScrollers(surface)) {
        if (change > 0 || scroller.scrollTop + scroller.clientHeight < scroller.scrollHeight - 1) scroller.scrollTop += change;
      }
    });
    observer.observe(slot);
    return () => observer.disconnect();
  }, [slot]);
}
