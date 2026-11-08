import { Feather } from 'lucide-react';
import type { ReactNode } from 'react';
import { followRoute, type RouteNavigate } from '../../shared/navigation/app-route';
import styles from './PublicHeader.module.css';

interface PublicHeaderProps {
  /** The page's own links beside the brand. */
  children: ReactNode;
  /** Where «Canon Keeper» leads. */
  homePath: string;
  navigate: RouteNavigate;
}

/** The header of the pages open without signing in: the home page and the documents. */
export function PublicHeader({ children, homePath, navigate }: PublicHeaderProps) {
  return (
    <header className={styles.header}>
      <a className={styles.logo} href={homePath} onClick={followRoute(navigate, homePath)}>
        <Feather aria-hidden="true" size={20} />
        <span>Canon Keeper</span>
      </a>
      {children}
    </header>
  );
}
