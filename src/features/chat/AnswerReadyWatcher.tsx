import { useQueries, type UseQueryResult } from '@tanstack/react-query';
import { useEffect, useRef } from 'react';
import { isApiStatusFailure } from '../../entities/api-errors';
import type { ChatApiClient, ChatJob } from '../../entities/chat/api';
import type { AppRoute, RouteNavigate } from '../../shared/navigation/app-route';
import { Button } from '../../ui/controls/Button';
import type { ToastInput } from '../../ui/feedback/useToastQueue';
import { formatCount } from '../../ui/copy';
import { browserNotice, forgetAllTurns, forgetTurn, markAnswerUnseen, useUnseenAnswers, useWatchedTurns, type WatchedTurn } from './answer-ready';
import { turnJobQueryOptions } from './api';
import { markHiddenTab } from '../../shared/navigation/page-title';

interface AnswerReadyWatcherProps {
  api: Pick<ChatApiClient, 'getJob'>;
  dismiss: (id: number) => void;
  navigate: RouteNavigate;
  notify: (toast: ToastInput) => number;
  route: AppRoute;
}

/**
 * Waits for the awaited chat turns on every page (UC6): when a turn ends and the author is not
 * looking at its chat, the app says so, marks a hidden tab and, if allowed, notifies the browser.
 */
export function AnswerReadyWatcher({ api, dismiss, navigate, notify, route }: AnswerReadyWatcherProps) {
  const turns = useWatchedTurns();
  const unseen = useUnseenAnswers();
  const jobs = useQueries({ combine: jobStates, queries: turns.map((turn) => turnJobQueryOptions(api, turn.jobId)) });
  // The one line of the ready answers (AA2.3): a second answer takes the place of the first line instead of a second one.
  const readyLine = useRef<{ id: number; turns: readonly WatchedTurn[] } | null>(null);
  const latest = useRef({ dismiss, navigate, notify, route, unseen, readyLine });
  useEffect(() => { latest.current = { dismiss, navigate, notify, route, unseen, readyLine }; });
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

  // The author opened the chat the line names: the line has said what it had to.
  const openChatId = route.surface === 'chat' ? route.query.get('chatId') : null;
  useEffect(() => {
    const line = readyLine.current;
    if (!openChatId || !line?.turns.some((turn) => turn.chatId === openChatId)) return;
    dismiss(line.id);
    readyLine.current = null;
  }, [dismiss, openChatId]);

  return null;
}

// A stable combine keeps the result stable between renders while no query changes.
function jobStates(results: UseQueryResult<ChatJob>[]) {
  return results.map((result) => ({ data: result.data, error: result.error }));
}

function finished(job: ChatJob) {
  return job.status === 'succeeded' || job.status === 'failed' || job.status === 'canceled';
}

type WatcherApp = Omit<AnswerReadyWatcherProps, 'api'> & {
  unseen: readonly WatchedTurn[];
  readyLine: { current: { id: number; turns: readonly WatchedTurn[] } | null };
};

function signal(turn: WatchedTurn, ready: boolean, app: WatcherApp) {
  const visible = document.visibilityState === 'visible';
  const inItsChat = app.route.surface === 'chat' && app.route.query.get('chatId') === turn.chatId;
  // The author sees the answer arrive in the open chat (R6.5).
  if (visible && inItsChat) return;
  // The number at «Чат» in the menu counts it until the author opens its chat (S2.5, AA2.2).
  if (ready && !inItsChat) markAnswerUnseen(turn);
  const openChat = (opened: WatchedTurn) => app.navigate('/chat', { chatId: opened.chatId, projectId: opened.projectId });
  let id = 0;
  if (ready) {
    // One line of every ready answer not opened yet; it opens the freshest (AA2.3).
    const answers = [...app.unseen.filter((answer) => answer.jobId !== turn.jobId), turn];
    const previous = app.readyLine.current;
    if (previous) app.dismiss(previous.id);
    const one = answers.length === 1;
    id = app.notify({
      action: <Button onClick={() => { app.dismiss(id); app.readyLine.current = null; openChat(turn); }} size="sm" variant="outline">{one ? 'Открыть' : 'Открыть чаты'}</Button>,
      message: one ? (turn.label ? `Ответ готов: «${clip(turn.label, 48)}»` : 'Ответ готов') : `Готовы ${formatCount(answers.length, 'ответ', 'ответа', 'ответов')}`,
      persistent: true,
      tone: 'success',
    });
    app.readyLine.current = { id, turns: answers };
  } else {
    id = app.notify({
      action: <Button onClick={() => { app.dismiss(id); openChat(turn); }} size="sm" variant="outline">Открыть чат</Button>,
      message: 'Ответ не получен',
      ...(turn.label ? { detail: `«${clip(turn.label, 80)}»` } : {}),
      persistent: true,
      tone: 'warning',
    });
  }
  const message = ready ? 'Ответ готов' : 'Ответ не получен';
  if (visible) return;
  markHiddenTab(message);
  if (browserNotice() !== 'on') return;
  try {
    const notification = new window.Notification(message, { body: turn.label ? `«${clip(turn.label, 80)}»` : undefined, tag: turn.jobId });
    notification.onclick = () => {
      window.focus();
      app.dismiss(id);
      openChat(turn);
      notification.close();
    };
  } catch {
    // Some mobile browsers send notifications only from a service worker; the tab mark stays.
  }
}

function clip(text: string, length: number) {
  const characters = Array.from(text.replace(/\s+/gu, ' ').trim());
  return characters.length > length ? `${characters.slice(0, length - 1).join('').trimEnd()}…` : characters.join('');
}
