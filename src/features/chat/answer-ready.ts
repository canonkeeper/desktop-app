import { useQuery } from '@tanstack/react-query';
import { useEffect, useSyncExternalStore } from 'react';
import { chatSessionsQueryOptions, type ChatApiClient } from '../../entities/chat/api';
import { textSourceView } from '../../entities/manuscript/api';
import type { AppRoute } from '../../shared/navigation/app-route';

/**
 * A chat turn the author waits for. Only which Jobs are awaited lives here, an ephemeral
 * fact of the interface; their status comes from the getJob queries of the watcher.
 */
export interface WatchedTurn {
  chatId: string;
  jobId: string;
  /** The question or the chat title, for the browser notification. */
  label?: string;
  projectId: string;
}

let watched: readonly WatchedTurn[] = [];
const turnListeners = new Set<() => void>();

function publishTurns(next: readonly WatchedTurn[]) {
  watched = next;
  turnListeners.forEach((listener) => listener());
}

function subscribeTurns(listener: () => void) {
  turnListeners.add(listener);
  return () => { turnListeners.delete(listener); };
}

export function watchTurn(turn: WatchedTurn) {
  if (watched.some((item) => item.jobId === turn.jobId)) return;
  publishTurns([...watched, turn]);
}

export function forgetTurn(jobId: string) {
  if (watched.some((item) => item.jobId === jobId)) publishTurns(watched.filter((item) => item.jobId !== jobId));
}

/** An identity change or logout drops the turns of the previous account. */
export function forgetAllTurns() {
  if (watched.length) publishTurns([]);
  if (unseen.length) publishUnseen([]);
}

export function useWatchedTurns() {
  return useSyncExternalStore(subscribeTurns, () => watched, () => watched);
}

/**
 * The answers that came while the author was not in their chat (specs/2026-11-10-ux2-shell.md S2.5,
 * specs/2026-11-11-ux4-after-answer.md AA2.2): the watcher of the turns notes each when it says «Ответ готов», opening its
 * chat forgets it. An ephemeral fact of the interface, as the turns watched are.
 */
let unseen: readonly WatchedTurn[] = [];
const unseenListeners = new Set<() => void>();

function publishUnseen(next: readonly WatchedTurn[]) {
  if (next === unseen) return;
  unseen = next;
  unseenListeners.forEach((listener) => listener());
}

export function markAnswerUnseen(turn: WatchedTurn) {
  if (!unseen.some((item) => item.jobId === turn.jobId)) publishUnseen([...unseen, turn]);
}

/** The author opened a chat: its ready answers are seen. */
export function seeAnswers(chatId: string) {
  if (unseen.some((item) => item.chatId === chatId)) publishUnseen(unseen.filter((item) => item.chatId !== chatId));
}

function subscribeUnseen(listener: () => void) {
  unseenListeners.add(listener);
  return () => { unseenListeners.delete(listener); };
}

/** The ready answers the author has not opened, newest last. */
export function useUnseenAnswers() {
  return useSyncExternalStore(subscribeUnseen, () => unseen, () => unseen);
}

const workingChatsIntervalMs = 4_000;
const working = new Set(['queued', 'running', 'canceling']);

/** What the mark of «Чат» says (AA2.2): an answer is on its way, and how many are ready and not opened. */
export interface ChatMarkState {
  preparing: boolean;
  ready: number;
}

/**
 * The mark of «Чат» in the menu of a saga (S2.5, AA2.2), from the same data the watcher of the turns and the list of
 * chats read — no poll and no counter of its own: «готовится» while a question of the saga is answered — a turn the
 * browser awaits or a chat the list shows working — and the number of the ready answers the author has not opened. A
 * working chat of the list is awaited too, so its answer is told when it comes. The list is read off the chat page
 * only: there the page reads it and the item of the open section has no mark.
 */
export function useChatMark(route: Pick<AppRoute, 'surface' | 'query'>, projectId?: string | null, api?: Pick<ChatApiClient, 'listChatSessions'>): ChatMarkState | null {
  const turns = useWatchedTurns();
  const answers = useUnseenAnswers();
  const inChat = route.surface === 'chat';
  const openChatId = inChat ? route.query.get('chatId') : null;
  const chats = useQuery({
    ...chatSessionsQueryOptions(api ?? { listChatSessions: () => Promise.reject(new Error('no chats')) }, { projectId: projectId ?? '', sourceView: textSourceView }),
    enabled: Boolean(api && projectId) && !inChat,
    refetchInterval: (query) => query.state.data?.data.some((chat) => working.has(chat.latestTurnStatus ?? '')) ? workingChatsIntervalMs : false,
  });
  useEffect(() => {
    if (openChatId) seeAnswers(openChatId);
  }, [openChatId]);
  const workingChats = projectId ? (chats.data?.data ?? []).filter((chat) => working.has(chat.latestTurnStatus ?? '') && chat.latestTurnJobId) : [];
  useEffect(() => {
    for (const chat of workingChats) watchTurn({ chatId: chat.id, jobId: chat.latestTurnJobId!, label: chat.title, projectId: projectId! });
  }, [projectId, workingChats.map((chat) => chat.latestTurnJobId).join()]);
  if (inChat) return null;
  const preparing = workingChats.length > 0 || turns.some((turn) => !projectId || turn.projectId === projectId);
  const ready = answers.filter((answer) => !projectId || answer.projectId === projectId).length;
  return preparing || ready ? { preparing, ready } : null;
}

const preferenceKey = 'canonkeeper.answer-ready.browser';
const preferenceListeners = new Set<() => void>();

export type BrowserNotice = 'unsupported' | 'denied' | 'off' | 'on';

function browserPermission() {
  return typeof window !== 'undefined' && 'Notification' in window ? window.Notification.permission : null;
}

function storedPreference() {
  try {
    return window.localStorage.getItem(preferenceKey) === '1';
  } catch {
    return false;
  }
}

function storePreference(on: boolean) {
  try {
    if (on) window.localStorage.setItem(preferenceKey, '1');
    else window.localStorage.removeItem(preferenceKey);
  } catch {
    // Without storage the choice lasts until the page reloads.
  }
  preferenceListeners.forEach((listener) => listener());
}

/** Whether a browser notification is sent when an awaited answer is ready (R6.4). */
export function browserNotice(): BrowserNotice {
  const permission = browserPermission();
  if (permission === null) return 'unsupported';
  if (permission === 'denied') return 'denied';
  return permission === 'granted' && storedPreference() ? 'on' : 'off';
}

function subscribePreference(listener: () => void) {
  preferenceListeners.add(listener);
  return () => { preferenceListeners.delete(listener); };
}

export function useBrowserNotice() {
  return useSyncExternalStore(subscribePreference, browserNotice, () => 'unsupported' as const);
}

/** Turns the browser notification on (asking the browser once) or off. */
export async function toggleBrowserNotice() {
  if (browserNotice() === 'on') {
    storePreference(false);
    return;
  }
  const permission = browserPermission();
  if (permission === null || permission === 'denied') return;
  const granted = permission === 'granted' || await window.Notification.requestPermission() === 'granted';
  storePreference(granted);
}
