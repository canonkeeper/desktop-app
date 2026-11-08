import { MessageCircle, X } from 'lucide-react';
import { useLayoutEffect, useRef, useState, type ReactNode } from 'react';
import { Button } from '../../ui/controls/Button';
import { IconButton } from '../../ui/controls/IconButton';
import { Drawer } from '../../ui/overlays/Drawer';
import styles from './ResearchChatLayout.module.css';

/** `working` counts the chats whose turn works; the «Чаты» button shows it while the list is hidden (R2.4). */
export function ResearchChatLayout({ children, sessions, sourceOpen, toolbar, working = 0 }: { children: ReactNode; sessions: (onNavigate: () => void) => ReactNode; sourceOpen: boolean; toolbar?: ReactNode; working?: number }) {
  const rootRef = useRef<HTMLDivElement>(null);
  const [collapsed, setCollapsed] = useState(false);
  const [sessionsOpen, setSessionsOpen] = useState(false);
  useLayoutEffect(() => {
    const measure = () => {
      const compact = window.innerWidth < 1440 || window.innerHeight <= 760
        || (sourceOpen && (rootRef.current?.clientWidth ?? 0) < 1080);
      setCollapsed(compact);
      if (!compact) setSessionsOpen(false);
    };
    measure();
    const observer = typeof ResizeObserver === 'function' ? new ResizeObserver(measure) : null;
    if (rootRef.current) observer?.observe(rootRef.current);
    window.addEventListener('resize', measure);
    return () => { observer?.disconnect(); window.removeEventListener('resize', measure); };
  }, [sourceOpen]);

  return <div className={styles.layout} data-collapsed={collapsed || undefined} data-toolbar={Boolean(toolbar) || undefined} ref={rootRef}>
    {toolbar || collapsed ? <div className={styles.toolbar}>
      {collapsed ? <Button aria-label={working ? `Чаты, работают: ${working}` : undefined} onClick={() => setSessionsOpen(true)} size="touch" variant="ghost">
        <MessageCircle aria-hidden="true" size={16} />Чаты{working ? <span aria-hidden="true" className={styles.working} /> : null}
      </Button> : null}
      {toolbar}
    </div> : null}
    <div className={styles.body}>
      {!collapsed ? <div className={styles.sessions}>{sessions(() => setSessionsOpen(false))}</div> : null}
      <div className={styles.conversation}>{children}</div>
    </div>
    {collapsed && sessionsOpen ? <Drawer canDismiss label="Чаты" onDismiss={() => setSessionsOpen(false)}>
      <div className={styles.drawerHeader}><strong>Чаты</strong><IconButton aria-label="Закрыть список чатов" onClick={() => setSessionsOpen(false)} size="touch"><X aria-hidden="true" size={16} /></IconButton></div>
      {sessions(() => setSessionsOpen(false))}
    </Drawer> : null}
  </div>;
}
