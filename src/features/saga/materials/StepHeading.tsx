import { useEffect, useRef, type ReactNode } from 'react';
import styles from './Materials.module.css';

/**
 * A step the author led to takes the focus: the button that led to it is gone. A step he finds when he opens the part leaves
 * the focus where it is — on the tab of the part or the heading of the page (specs/2026-11-10-ux2-memory-page.md MP1.1).
 */
export function StepHeading({ children, led }: { children: ReactNode; led: boolean }) {
  const heading = useRef<HTMLHeadingElement>(null);
  useEffect(() => {
    if (led) heading.current?.focus({ preventScroll: true });
  }, [children, led]);
  return <h2 className={styles.heading} ref={heading} tabIndex={-1}>{children}</h2>;
}
