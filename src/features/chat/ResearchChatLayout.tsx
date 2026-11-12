import { MessageCircle, X } from 'lucide-react';
import { useLayoutEffect, useRef, useState, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import { Button } from '../../ui/controls/Button';
import { IconButton } from '../../ui/controls/IconButton';
import { formatCount } from '../../ui/copy';
import { Drawer } from '../../ui/overlays/Drawer';
import styles from './ResearchChatLayout.module.css';

interface ResearchChatLayoutProps {
  /**
   * The actions of the open chat beside «Чаты» while the chat list is hidden (specs/2026-11-10-ux1-chat.md CH2.2);
   * `showSessions` opens the list, where a name is changed.
   */
  actions?: (showSessions: () => void) => ReactNode;
  children: ReactNode;
  /**
   * The list of chats: in the column, where its «Новый чат» goes to `newChatIn` — the row of the panel — when the page gives
   * one, or in the drawer of «Чаты», where it keeps «Новый чат» at its top.
   */
  sessions: (onNavigate: () => void, newChatIn: HTMLElement | null) => ReactNode;
  sourceOpen: boolean;
  /**
   * The row of the panel the page keeps with its heading in it (specs/2026-11-11-ux2-polish.md PL2.1): the head of the column
   * of chats, where «Новый чат» stands, or — the column folded — the row of «Чаты» and the actions of the open chat. The
   * layout leaves the first row of its grid to it. Without it the layout keeps its own row of «Чаты».
   */
  row?: HTMLElement | null;
  /** The chats whose turn works: the «Чаты» button shows it while the list is hidden (R2.4). */
  working?: number;
}

export function ResearchChatLayout({ actions, children, row, sessions, sourceOpen, working = 0 }: ResearchChatLayoutProps) {
  const rootRef = useRef<HTMLDivElement>(null);
  const [collapsed, setCollapsed] = useState(false);
  const [sessionsOpen, setSessionsOpen] = useState(false);
  useLayoutEffect(() => {
    const measure = () => {
      // While a source is open beside the chat, the list of chats folds into «Чаты» at any width (AA1.7, Ч-43).
      const compact = window.innerWidth < 1440 || window.innerHeight <= 760 || sourceOpen;
      setCollapsed(compact);
      if (!compact) setSessionsOpen(false);
    };
    measure();
    const observer = typeof ResizeObserver === 'function' ? new ResizeObserver(measure) : null;
    if (rootRef.current) observer?.observe(rootRef.current);
    window.addEventListener('resize', measure);
    return () => { observer?.disconnect(); window.removeEventListener('resize', measure); };
  }, [sourceOpen]);

  const folded = collapsed ? <>
    <Button aria-label={working ? `Чаты: помощник отвечает в ${formatCount(working, 'чате', 'чатах', 'чатах')}` : undefined} onClick={() => setSessionsOpen(true)} size="touch" variant="ghost">
      <MessageCircle aria-hidden="true" size={16} />Чаты{working ? <span aria-hidden="true" className={styles.working} /> : null}
    </Button>
    {actions ? <div className={styles.actions}>{actions(() => setSessionsOpen(true))}</div> : null}
  </> : null;
  const inRow = row !== undefined;
  return <div className={styles.layout} data-chat-layout="" data-collapsed={collapsed || undefined} data-row={inRow || undefined} ref={rootRef}>
    {inRow ? (row && folded ? createPortal(folded, row) : null) : folded ? <div className={styles.toolbar}>{folded}</div> : null}
    <div className={styles.body}>
      {!collapsed ? <div className={styles.sessions}>{sessions(() => setSessionsOpen(false), row ?? null)}</div> : null}
      <div className={styles.conversation}>{children}</div>
    </div>
    {collapsed && sessionsOpen ? <Drawer canDismiss label="Чаты" onDismiss={() => setSessionsOpen(false)}>
      <div className={styles.drawerHeader}><strong>Чаты</strong><IconButton aria-label="Закрыть список чатов" onClick={() => setSessionsOpen(false)} size="touch"><X aria-hidden="true" size={16} /></IconButton></div>
      {sessions(() => setSessionsOpen(false), null)}
    </Drawer> : null}
  </div>;
}
