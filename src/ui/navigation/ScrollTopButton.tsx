import { ArrowUp } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import { IconButton } from '../controls/IconButton';
import styles from './navigation.module.css';

/** The nearest box that scrolls the page the button stands in: the column of the page, else the document. */
function scrollBox(start: HTMLElement | null): HTMLElement {
  for (let node = start?.parentElement ?? null; node; node = node.parentElement) {
    const { overflowY } = getComputedStyle(node);
    if (overflowY === 'auto' || overflowY === 'scroll') return node;
  }
  return (document.scrollingElement as HTMLElement | null) ?? document.documentElement;
}

/**
 * «Наверх» (an author's review 03.11): a round button at the bottom right of a long page. It appears once the page is
 * scrolled a screen down and takes it back to the top. It keeps to the end of its own column, so a panel beside the
 * column stays clear; any long page may put it last in its column. The box is the one that scrolls when it is read:
 * a layout that changes under the page — a panel opened beside it, a phone turned — never leaves it watching another
 * (owner 04.11).
 */
export function ScrollTopButton() {
  const anchor = useRef<HTMLDivElement>(null);
  const [shown, setShown] = useState(false);

  useEffect(() => {
    const update = () => {
      const box = scrollBox(anchor.current);
      setShown(box.scrollTop > box.clientHeight);
    };
    update();
    // A scroll does not bubble; the document hears every one of them while it captures.
    document.addEventListener('scroll', update, { capture: true, passive: true });
    return () => document.removeEventListener('scroll', update, { capture: true });
  }, []);

  return (
    <div className={styles.scrollTop} ref={anchor}>
      {shown ? (
        <IconButton aria-label="Наверх" className={styles.scrollTopButton} onClick={() => scrollBox(anchor.current).scrollTo({ top: 0, behavior: 'smooth' })} title="Наверх">
          <ArrowUp aria-hidden="true" size={18} />
        </IconButton>
      ) : null}
    </div>
  );
}
