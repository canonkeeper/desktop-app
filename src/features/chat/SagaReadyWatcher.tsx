import { CircleCheck, X } from 'lucide-react';
import { useEffect, useRef, useState, type ReactNode } from 'react';
import { useIndexingStatusQuery, useShownSaga, type IndexingStatus, type ManuscriptApiClient } from '../../entities/manuscript/api';
import { readyWords } from '../../entities/manuscript/preparation-copy';
import type { AppRoute, RouteNavigate } from '../../shared/navigation/app-route';
import { markHiddenTab } from '../../shared/navigation/page-title';
import { Button } from '../../ui/controls/Button';
import { IconButton } from '../../ui/controls/IconButton';
import { TopBarNoticeProvider } from '../../ui/navigation/TopBarStrip';
import { isReaderRoute } from '../../ui/shell/shell-routes';
import { browserNotice } from './answer-ready';
import { firstTelling } from './saga-ready';
import styles from './SagaReadyWatcher.module.css';

interface SagaReadyWatcherProps {
  api: Pick<ManuscriptApiClient, 'getIndexingStatus'>;
  navigate: RouteNavigate;
  route: AppRoute;
  /** The pages under the top bar the strip stands in. */
  children: ReactNode;
}

type SagaReady = NonNullable<IndexingStatus['sagaReady']>;

/** While the saga is prepared the status is read again this often on the pages that do not follow it themselves. */
const followEveryMs = 15_000;

/**
 * Tells once that the saga of the page became ready to be asked (FS4.2 of the spec «Сага готова», specs/2026-11-09): a
 * strip in the place of the notices of the shell, under the top bar where the line of the address stands — not over the
 * whole page (specs/2026-11-10-ux3-saga-state.md SS3.5) — in the words of the line of the shelf: «Книга прочитана — можно
 * спрашивать · 1 книга, 3 главы». The fact is the `sagaReady` of the status of the server; this browser only remembers
 * which readiness it has told of. In the chat of that saga the strip has no «Открыть чат»; on the shelf the line of the
 * shelf says it, so the strip stands aside. A hidden tab gets its mark and, if allowed, a notification of the browser.
 */
export function SagaReadyWatcher({ api, children, navigate, route }: SagaReadyWatcherProps) {
  // The shelf and the chat open the first saga for an address without one; other pages name their saga in the address.
  const shown = useShownSaga(route.query.get('projectId'));
  const defaulted = route.surface === 'chat' || route.surface === 'manuscript';
  const projectId = route.query.get('projectId') ?? (defaulted ? shown.projectId : null) ?? '';
  const status = useIndexingStatusQuery(api, projectId, { enabled: projectId !== '', follow: followEveryMs });
  const ready = status.data?.sagaReady;
  // The shelf sees the work on the saga begin and end at once: the status is read again at both moments, so the
  // preparation is followed from its start and its end is told without waiting for the next reading.
  const { refetch } = status;
  const preparing = shown.preparing && projectId !== '';
  const wasPreparing = useRef(preparing);
  useEffect(() => {
    if (wasPreparing.current === preparing) return;
    wasPreparing.current = preparing;
    void refetch();
  }, [preparing, refetch]);
  const [told, setTold] = useState<{ projectId: string; ready: SagaReady } | null>(null);

  // The readiness is told by its time: the same one read again says nothing new. On the shelf its own line says it
  // (W-74): it is told there, and the next page does not tell it again.
  const shelf = route.surface === 'manuscript' && !isReaderRoute(route);
  const at = ready?.at;
  const books = ready?.books ?? 0;
  const chapters = ready?.chapters ?? 0;
  useEffect(() => {
    if (!at || !projectId || !firstTelling(projectId, at)) return;
    const next = { projectId, ready: { at, books, chapters } };
    if (!shelf) setTold(next);
    signalHiddenTab(next.ready, () => {
      setTold(null);
      navigate('/chat', { projectId: next.projectId });
    }, projectId);
  }, [at, books, chapters, navigate, projectId, shelf]);

  const strip = told && told.projectId === projectId && !shelf ? (
    <ReadyStrip inChat={route.surface === 'chat'} onClose={() => setTold(null)} onOpenChat={() => {
      setTold(null);
      navigate('/chat', { projectId: told.projectId });
    }} ready={told.ready} />
  ) : null;
  return <TopBarNoticeProvider value={strip}>{children}</TopBarNoticeProvider>;
}

function ReadyStrip({ inChat, onClose, onOpenChat, ready }: { inChat: boolean; onClose: () => void; onOpenChat: () => void; ready: SagaReady }) {
  const words = readyWords(ready);
  return (
    <div aria-label={words.title} className={styles.strip} role="status">
      <CircleCheck aria-hidden="true" className={styles.icon} size={18} />
      <p className={styles.message}>{words.title} · {words.facts}</p>
      {/* In the chat of this saga the question field is already at hand. */}
      {inChat ? null : <Button onClick={onOpenChat} size="sm" variant="outline">Открыть чат</Button>}
      <IconButton aria-label="Закрыть уведомление" onClick={onClose} size="sm"><X aria-hidden="true" size={16} /></IconButton>
    </div>
  );
}

/** A hidden tab gets its mark and, if the author allowed them, a notification of the browser that opens the chat. */
function signalHiddenTab(ready: SagaReady, openChat: () => void, projectId: string) {
  if (document.visibilityState === 'visible') return;
  markHiddenTab('Сага готова');
  if (browserNotice() !== 'on') return;
  const words = readyWords(ready);
  try {
    const notification = new window.Notification(words.title, { body: words.facts, tag: `saga-ready-${projectId}` });
    notification.onclick = () => {
      window.focus();
      openChat();
      notification.close();
    };
  } catch {
    // Some mobile browsers send notifications only from a service worker; the tab mark stays.
  }
}
