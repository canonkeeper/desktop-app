import { SkeletonArea } from '../ui/feedback/Skeleton';
import styles from '../App.module.css';

/** The place of a page whose code is still on its way. */
export function RouteFallback() {
  return <div aria-hidden="true" className={styles.routeFallback} />;
}

/**
 * The place of a page of the shell whose code is still on its way (specs/2026-11-10-ux2-patterns-states.md PS3.1): the top
 * bar and the sections already stand, the content shows the form of a page — a heading, a line, cards — after 200 ms.
 */
export function PageFallback() {
  return <div className={styles.pageFallback}><SkeletonArea shape="page" /></div>;
}
