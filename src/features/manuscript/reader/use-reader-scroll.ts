import { useEffect, useState, type RefObject } from 'react';

/** The place of reading in a chapter: the browser keeps it for the tab, so a reload opens the chapter where it was read. */
const placeKey = (chapterId: string) => `canonkeeper.reader.place.${chapterId}`;

function remember(chapterId: string, top: number) {
  try {
    window.sessionStorage.setItem(placeKey(chapterId), String(Math.round(top)));
  } catch {
    // A private window may refuse the storage: the place is then only on the screen.
  }
}

function recall(chapterId: string) {
  try {
    return Number(window.sessionStorage.getItem(placeKey(chapterId)) ?? 0) || 0;
  } catch {
    return 0;
  }
}

/**
 * The line of the reader follows the scroll of the text (specs/2026-11-10-ux7-reader.md RD1.1): it goes away while the
 * text scrolls down and comes back when it scrolls up and at the start of the chapter; its thin line shows how much of
 * the chapter is behind. A chapter opened without a place of a link starts where it was read in this tab, else at its top.
 */
export function useReaderScroll(scroller: RefObject<HTMLElement | null>, chapterId: string, linkedParagraph: string | null) {
  const [state, setState] = useState({ hidden: false, progress: 0 });
  useEffect(() => {
    const box = scroller.current;
    if (!box || !chapterId) return undefined;
    if (!linkedParagraph) box.scrollTop = recall(chapterId);
    let last = box.scrollTop;
    let frame = 0;
    const measure = () => {
      frame = 0;
      const top = box.scrollTop;
      const room = box.scrollHeight - box.clientHeight;
      const delta = top - last;
      last = top;
      remember(chapterId, top);
      setState((previous) => {
        const hidden = top < 64 ? false : delta > 6 ? true : delta < -6 ? false : previous.hidden;
        const progress = room > 0 ? Math.min(1, top / room) : 0;
        return hidden === previous.hidden && Math.abs(progress - previous.progress) < 0.005 ? previous : { hidden, progress };
      });
    };
    const onScroll = () => {
      if (!frame) frame = requestAnimationFrame(measure);
    };
    measure();
    box.addEventListener('scroll', onScroll, { passive: true });
    return () => {
      box.removeEventListener('scroll', onScroll);
      if (frame) cancelAnimationFrame(frame);
    };
  }, [chapterId, linkedParagraph, scroller]);
  return state;
}
