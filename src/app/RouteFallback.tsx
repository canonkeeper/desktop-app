import styles from '../App.module.css';

/** The place of a page whose code is still on its way. */
export function RouteFallback() {
  return <div aria-hidden="true" className={styles.routeFallback} />;
}
