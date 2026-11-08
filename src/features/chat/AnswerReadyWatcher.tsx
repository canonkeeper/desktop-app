import { useQueries, type UseQueryResult } from '@tanstack/react-query';
import { useEffect, useRef } from 'react';
import { isApiStatusFailure } from '../../entities/api-errors';
import type { ChatApiClient, ChatJob } from '../../entities/chat/api';
import type { AppRoute, RouteNavigate } from '../../shared/navigation/app-route';
import { Button } from '../../ui/controls/Button';
import type { ToastInput } from '../../ui/feedback/useToastQueue';
import { browserNotice, forgetAllTurns, forgetTurn, useWatchedTurns, type WatchedTurn } from './answer-ready';
import { turnJobQueryOptions } from './api';

interface AnswerReadyWatcherProps {
  api: Pick<ChatApiClient, 'getJob'>;
  dismiss: (id: number) => void;
  navigate: RouteNavigate;
  notify: (toast: ToastInput) => number;
  route: AppRoute;
}

const titleMark = /^● [^·]+ · /u;

/**
 * Waits for the awaited chat turns on every page (UC6): when a turn ends and the author is not
 * looking at its chat, the app says so, marks a hidden tab and, if allowed, notifies the browser.
 */
export function AnswerReadyWatcher({ api, dismiss, navigate, notify, route }: AnswerReadyWatcherProps) {
  const turns = useWatchedTurns();
  const jobs = useQueries({ combine: jobStates, queries: turns.map((turn) => turnJobQueryOptions(api, turn.jobId)) });
  const latest = useRef({ dismiss, navigate, notify, route });
  useEffect(() => { latest.current = { dismiss, navigate, notify, route }; });
  useEffect(() => forgetAllTurns, []);

  useEffect(() => {
    turns.forEach((turn, index) => {
      const query = jobs[index];
      // A deleted chat takes its Job with it: nothing is awaited any more.
      if (isApiStatusFailure(query?.error) && query.error.status === 404) forgetTurn(turn.jobId);
      const job = query?.data;
      if (job?.id !== turn.jobId || !finished(job)) return;
      forgetTurn(turn.jobId);
      if (job.status !== 'canceled') signal(turn, job.status === 'succeeded', latest.current);
    });
  }, [jobs, turns]);

  return null;
}

// A stable combine keeps the result stable between renders while no query changes.
function jobStates(results: UseQueryResult<ChatJob>[]) {
  return results.map((result) => ({ data: result.data, error: result.error }));
}

function finished(job: ChatJob) {
  return job.status === 'succeeded' || job.status === 'failed' || job.status === 'canceled';
}

function signal(turn: WatchedTurn, ready: boolean, app: Omit<AnswerReadyWatcherProps, 'api'>) {
  const visible = document.visibilityState === 'visible';
  // The author sees the answer arrive in the open chat (R6.5).
  if (visible && app.route.surface === 'chat' && app.route.query.get('chatId') === turn.chatId) return;
  const message = ready ? 'Ответ готов' : 'Ответ не получен';
  const openChat = () => app.navigate('/chat', { chatId: turn.chatId, projectId: turn.projectId });
  let id = 0;
  id = app.notify({
    action: <Button onClick={() => { app.dismiss(id); openChat(); }} size="sm" variant="outline">Открыть чат</Button>,
    message,
    persistent: true,
    tone: ready ? 'success' : 'warning',
  });
  if (visible) return;
  markHiddenTab(message);
  if (browserNotice() !== 'on') return;
  try {
    const notification = new window.Notification(message, { body: turn.label ? `«${clip(turn.label)}»` : undefined, tag: turn.jobId });
    notification.onclick = () => {
      window.focus();
      app.dismiss(id);
      openChat();
      notification.close();
    };
  } catch {
    // Some mobile browsers send notifications only from a service worker; the tab mark stays.
  }
}

function clip(text: string) {
  const characters = Array.from(text.replace(/\s+/gu, ' ').trim());
  return characters.length > 80 ? `${characters.slice(0, 79).join('')}…` : characters.join('');
}

/** «● Ответ готов · » stays in the tab title until the author returns to the tab (R6.3). */
function markHiddenTab(message: string) {
  document.title = `● ${message} · ${document.title.replace(titleMark, '')}`;
  const restore = () => {
    if (document.visibilityState !== 'visible') return;
    document.title = document.title.replace(titleMark, '');
    document.removeEventListener('visibilitychange', restore);
  };
  document.addEventListener('visibilitychange', restore);
}
