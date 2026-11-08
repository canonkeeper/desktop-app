import { useEffect, useRef, type ReactNode } from 'react';
import styles from './Materials.module.css';

/** A step takes the focus: the button that led to it is gone, and Esc must still close the panel. */
export function StepHeading({ children }: { children: ReactNode }) {
  const heading = useRef<HTMLHeadingElement>(null);
  useEffect(() => heading.current?.focus({ preventScroll: true }), [children]);
  return <h2 className={styles.heading} ref={heading} tabIndex={-1}>{children}</h2>;
}
