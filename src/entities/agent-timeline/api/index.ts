import {
  useInfiniteQuery,
  useQueryClient,
  type InfiniteData,
} from '@tanstack/react-query';
import { useCallback, useEffect, useMemo, useReducer, useRef, useState } from 'react';
import type {
  AgentTimelineEvent,
  AgentTimelineContext,
  AgentTimelineDelta,
  CanonKeeperApiClient,
} from '../../../shared/api';
import {
  openAgentTimelineStream,
  parseAgentTimelineStreamEnvelope,
  timelinePageIsEof,
  timelinePageNextCursor,
  type TimelineApiClient,
  type TimelineCursor,
  type TimelinePage,
} from '../../../shared/api';
import { isTransientApiFailure } from '../../api-errors';
import { mergeEvents, mergeLiveEvents } from './merge-events';

export type { AgentTimelineEvent, TimelineApiClient, TimelineCursor, TimelinePage };

export type AgentTimelineConnection = 'connecting' | 'connected' | 'reconnecting' | 'closed';
export type AgentTimelineTerminalEvent = Pick<
  AgentTimelineEvent,
  'id' | 'cursor' | 'jobId' | 'kind' | 'outcome' | 'role'
>;

export interface UseCanonicalAgentTimelineOptions {
  api: TimelineApiClient;
  baseUrl?: string;
  createSocket?: (url: string) => WebSocket;
  jobId: string;
  locationHref?: string;
  onTerminalEvent?: (event: AgentTimelineTerminalEvent) => void;
  projectId: string;
  reconnect?: boolean;
}

export interface CanonicalAgentTimelineState {
  connection: AgentTimelineConnection;
  error: string;
  events: readonly AgentTimelineEvent[];
  contextByRole: Partial<Record<AgentTimelineEvent['role'], AgentTimelineContext>>;
  isError: boolean;
  isLoading: boolean;
  retry: () => void;
}

const queryRoot = ['canonkeeper', 'agent-timeline'] as const;

/**
 * A failed read or a dropped stream is a hiccup until the next attempt fails too: the stream
 * counts as broken from the second failure in a row, so the first read of a turn that fails once
 * and then passes shows no break (specs/2026-10-13-agent-feed.md, Decision log 30.10). Network and
 * 5xx failures keep retrying; a refusal (4xx: the Job is gone or this account may not read it)
 * stops the retries when it is that second failure.
 */
const failuresToBreak = 2;

/** The wait before the next attempt after `failures` failures in a row: 1 s, 2 s, 4 s … 30 s. */
function retryDelay(failures: number) {
  return Math.min(30_000, 1_000 * 2 ** Math.min(failures, 5));
}

function retriesRead(failures: number, error: unknown) {
  return isTransientApiFailure(error) || failures + 1 < failuresToBreak;
}

function queryKey(projectId: string, jobId: string) {
  return [...queryRoot, projectId, jobId] as const;
}

function loadPage(
  api: TimelineApiClient,
  projectId: string,
  jobId: string,
  cursor: TimelineCursor | null,
  signal: AbortSignal,
) {
  return api.getAgentTimeline(projectId, jobId, {
    cursor: cursor === null ? undefined : cursor,
    signal,
  }) as Promise<TimelinePage>;
}

function timelineApiFromMeta(meta: unknown): TimelineApiClient {
  const api = (meta as { timelineApi?: TimelineApiClient } | undefined)?.timelineApi;
  if (!api) throw new Error('Canonical timeline query is missing its API client metadata.');
  return api;
}

function eventsFromData(data: InfiniteData<TimelinePage, unknown> | undefined) {
  return mergeEvents([], data?.pages.flatMap((page) => page.events) ?? []);
}

function lastEventCursor(events: readonly AgentTimelineEvent[], fallback: TimelineCursor | null) {
  return events.at(-1)?.cursor ?? fallback;
}

function setEvents(
  queryClient: ReturnType<typeof useQueryClient>,
  key: ReturnType<typeof queryKey>,
  incoming: readonly AgentTimelineEvent[],
) {
  queryClient.setQueryData<InfiniteData<TimelinePage, unknown>>(key, (current) => {
    if (!current || current.pages.length === 0) return current;
    const events = mergeEvents(eventsFromData(current), incoming);
    const lastPageIndex = current.pages.length - 1;
    return {
      ...current,
      pages: current.pages.map((page, index) => index === lastPageIndex
        ? { ...page, eof: true, events, nextCursor: null }
        : { ...page, events: [] }),
    };
  });
}

function errorMessage(error: unknown) {
  return error instanceof Error ? error.message : 'Не удалось загрузить timeline.';
}

function terminalEventMetadata(event: AgentTimelineEvent): AgentTimelineTerminalEvent {
  return {
    cursor: event.cursor,
    id: event.id,
    jobId: event.jobId,
    kind: event.kind,
    outcome: event.outcome,
    role: event.role,
  };
}

function triggersAuthoritativeJobRefresh(event: AgentTimelineEvent) {
  return event.kind === 'turn_outcome';
}

export function useCanonicalAgentTimeline({
  api,
  baseUrl,
  createSocket,
  jobId,
  locationHref,
  onTerminalEvent,
  projectId,
  reconnect: reconnectEnabled = true,
}: UseCanonicalAgentTimelineOptions): CanonicalAgentTimelineState {
  const queryClient = useQueryClient();
  const key = useMemo(() => queryKey(projectId, jobId), [jobId, projectId]);
  const [stream, setStream] = useState<Pick<CanonicalAgentTimelineState, 'connection' | 'error'>>({
    connection: 'connecting',
    error: '',
  });
  const [liveEvents, setLiveEvents] = useState<readonly AgentTimelineEvent[]>([]);
  const [contextByRole, setContextByRole] = useState<CanonicalAgentTimelineState['contextByRole']>({});
  const terminalEvents = useRef(new Set<string>());
  const onTerminalEventRef = useRef(onTerminalEvent);
  onTerminalEventRef.current = onTerminalEvent;
  const apiRef = useRef(api);
  apiRef.current = api;
  const createSocketRef = useRef(createSocket);
  createSocketRef.current = createSocket;
  const reconnectEnabledRef = useRef(reconnectEnabled);
  reconnectEnabledRef.current = reconnectEnabled;
  const [streamGeneration, restartStream] = useReducer((generation: number) => generation + 1, 0);

  const timelineQuery = useInfiniteQuery({
    queryKey: key,
    queryFn: ({ meta, pageParam, signal }) => loadPage(timelineApiFromMeta(meta), projectId, jobId, pageParam, signal),
    enabled: projectId.length > 0 && jobId.length > 0,
    getNextPageParam: (
      lastPage: TimelinePage,
      _allPages: TimelinePage[],
      lastPageParam: TimelineCursor | null,
      allPageParams: (TimelineCursor | null)[],
    ) => {
      if (timelinePageIsEof(lastPage)) return undefined;
      const cursor = timelinePageNextCursor(lastPage);
      if (cursor === null) throw new Error('Timeline page is missing nextCursor before EOF.');
      if (String(cursor) === String(lastPageParam)
        || allPageParams.some((pageParam) => pageParam !== null && String(pageParam) === String(cursor))) {
        throw new Error('Timeline page cursor repeated before EOF.');
      }
      return cursor;
    },
    initialPageParam: null as TimelineCursor | null,
    meta: { outageScope: 'local', timelineApi: api },
    retry: retriesRead,
    retryDelay,
    staleTime: Infinity,
  });

  useEffect(() => {
    terminalEvents.current.clear();
    setLiveEvents([]);
    setContextByRole({});
  }, [jobId, projectId]);

  // Each page read asks for the next one: a page that comes before a render shows the fetch leaves the flags as they
  // were, so the count of pages is what moves.
  const pagesRead = timelineQuery.data?.pages.length ?? 0;
  useEffect(() => {
    if (!timelineQuery.isSuccess || timelineQuery.isFetchingNextPage || !timelineQuery.hasNextPage) return;
    void timelineQuery.fetchNextPage();
  }, [
    pagesRead,
    timelineQuery.fetchNextPage,
    timelineQuery.hasNextPage,
    timelineQuery.isFetchingNextPage,
    timelineQuery.isSuccess,
  ]);

  const ready = timelineQuery.isSuccess && !timelineQuery.isFetchingNextPage && !timelineQuery.hasNextPage;

  useEffect(() => {
    if (!ready || !projectId || !jobId) return undefined;
    let disposed = false;
    let socket: WebSocket | undefined;
    let reconnectTimer: ReturnType<typeof setTimeout> | undefined;
    let failures = 0;
    let replayPromise: Promise<void> | null = null;
    const lastPage = timelineQuery.data?.pages.at(-1);
    const initialEvents = eventsFromData(timelineQuery.data);
    let lastCursor = lastEventCursor(
      initialEvents,
      lastPage ? timelinePageNextCursor(lastPage) : null,
    );

    setStream({ connection: 'connecting', error: '' });

    function rememberCursor(cursor: TimelineCursor | undefined) {
      if (cursor !== undefined) lastCursor = cursor;
    }

    function append(incoming: readonly AgentTimelineEvent[], authoritativeOrder = false) {
      setEvents(queryClient, key, incoming);
      setLiveEvents((current) => {
        const ordered = current.length === 0 ? initialEvents : current;
        return authoritativeOrder ? mergeEvents(ordered, incoming) : mergeLiveEvents(ordered, incoming);
      });
      for (const event of incoming) {
        rememberCursor(event.cursor);
        if (triggersAuthoritativeJobRefresh(event) && !terminalEvents.current.has(event.id)) {
          terminalEvents.current.add(event.id);
          onTerminalEventRef.current?.(terminalEventMetadata(event));
        }
      }
    }

    function appendDelta(cursor: TimelineCursor, delta: AgentTimelineDelta) {
      const projected = { ...delta, cursor };
      setLiveEvents((current) => {
        const merged = current.length === 0 ? [...initialEvents] : [...current];
        const index = merged.findIndex((event) => event.id === delta.id);
        if (index === -1) return [...merged, projected];
        const existing = merged[index];
        if (existing) merged[index] = { ...existing, text: `${existing.text ?? ''}${delta.text}` };
        return merged;
      });
    }

    async function replayFromCursor() {
      if (replayPromise) return replayPromise;
      replayPromise = (async () => {
        let cursor = lastCursor;
        const seenCursors = new Set<string>();
        if (cursor !== null) seenCursors.add(cursor);
        for (;;) {
          const page = await apiRef.current.getAgentTimeline(projectId, jobId, {
            cursor: cursor === null ? undefined : cursor,
          }) as unknown as TimelinePage;
          append(page.events, true);
          const nextCursor = timelinePageNextCursor(page);
          if (timelinePageIsEof(page)) {
            lastCursor = lastEventCursor(page.events, nextCursor ?? cursor);
            return;
          }
          if (nextCursor === null || seenCursors.has(nextCursor)) {
            throw new Error('Timeline page is missing a progressing cursor before EOF.');
          }
          seenCursors.add(nextCursor);
          cursor = nextCursor;
        }
      })().finally(() => {
        replayPromise = null;
      });
      return replayPromise;
    }

    /** One more failure in a row: a drop of the socket or a failed replay (`error`). */
    function scheduleReconnect(error?: unknown) {
      if (disposed) return;
      failures += 1;
      const refused = error !== undefined && !isTransientApiFailure(error);
      if (!reconnectEnabledRef.current || (refused && failures >= failuresToBreak)) {
        setStream((current) => ({ ...current, connection: 'closed' }));
        return;
      }
      if (failures >= failuresToBreak) setStream((current) => ({ ...current, connection: 'reconnecting' }));
      if (reconnectTimer) return;
      reconnectTimer = setTimeout(() => {
        reconnectTimer = undefined;
        void connect(true);
      }, retryDelay(failures - 1));
    }

    function protocolFailure() {
      setStream({ connection: 'reconnecting', error: 'Не удалось прочитать timeline. Переподключаемся…' });
      socket?.close(4000, 'invalid public timeline envelope');
    }

    function connect(replay: boolean) {
      if (disposed) return;
      setContextByRole({});
      void (async () => {
        if (replay) {
          try {
            await replayFromCursor();
          } catch (error) {
            if (disposed) return;
            setStream((current) => ({ ...current, error: errorMessage(error) }));
            scheduleReconnect(error);
            return;
          }
        }
        if (disposed) return;
        try {
          socket = openAgentTimelineStream(projectId, jobId, {
            baseUrl,
            createSocket: createSocketRef.current,
            locationHref,
          });
        } catch (error) {
          setStream((current) => ({ ...current, error: errorMessage(error) }));
          scheduleReconnect(error);
          return;
        }
        socket.addEventListener('open', () => {
          if (disposed) return;
          failures = 0;
          setStream({ connection: 'connected', error: '' });
        });
        socket.addEventListener('message', (message) => {
          if (disposed || typeof message.data !== 'string') {
            if (!disposed) protocolFailure();
            return;
          }
          const parsed = parseAgentTimelineStreamEnvelope(message.data);
          if (!parsed.success) {
            protocolFailure();
            return;
          }
          const envelope = parsed.data;
          if (envelope.type === 'snapshot') {
            append(envelope.snapshot.events as unknown as AgentTimelineEvent[], false);
            rememberCursor(envelope.cursor);
            setStream({ connection: 'connected', error: '' });
            if (envelope.snapshot.needsReconcile) void replayFromCursor();
            return;
          }
          if (envelope.type === 'event') {
            append([envelope.event as unknown as AgentTimelineEvent]);
            rememberCursor(envelope.cursor);
            setStream({ connection: 'connected', error: '' });
            return;
          }
          if (envelope.type === 'delta') {
            appendDelta(envelope.cursor, envelope.delta);
            setStream({ connection: 'connected', error: '' });
            return;
          }
          if (envelope.type === 'context') {
            setContextByRole((current) => ({ ...current, [envelope.context.role]: envelope.context }));
            setStream({ connection: 'connected', error: '' });
            return;
          }
          rememberCursor(envelope.cursor);
          setStream({
            connection: 'reconnecting',
            error: envelope.message ?? 'Timeline требует повторной синхронизации.',
          });
          void replayFromCursor().catch((error) => {
            if (!disposed) setStream({ connection: 'reconnecting', error: errorMessage(error) });
          });
        });
        socket.addEventListener('close', () => {
          if (disposed) return;
          scheduleReconnect();
        });
      })();
    }

    connect(false);
    return () => {
      disposed = true;
      if (reconnectTimer) clearTimeout(reconnectTimer);
      socket?.close(1000, 'timeline resource changed');
    };
  }, [
    baseUrl,
    jobId,
    key,
    locationHref,
    projectId,
    queryClient,
    ready,
    streamGeneration,
  ]);

  useEffect(() => {
    if (!ready) return;
    for (const event of eventsFromData(timelineQuery.data)) {
      if (!triggersAuthoritativeJobRefresh(event) || terminalEvents.current.has(event.id)) continue;
      terminalEvents.current.add(event.id);
      onTerminalEventRef.current?.(terminalEventMetadata(event));
    }
  }, [ready, timelineQuery.data]);

  // «Повторить» reads the timeline again and reopens a stream that stopped on a refusal.
  const retry = useCallback(() => {
    restartStream();
    void timelineQuery.refetch();
  }, [timelineQuery.refetch]);
  const queryError = timelineQuery.error ? errorMessage(timelineQuery.error) : '';
  return {
    connection: timelineQuery.isError
      ? 'closed'
      : timelineQuery.failureCount >= failuresToBreak ? 'reconnecting' : stream.connection,
    contextByRole,
    error: stream.error || queryError,
    events: mergeLiveEvents(liveEvents, eventsFromData(timelineQuery.data)),
    isError: timelineQuery.isError,
    isLoading: timelineQuery.isPending || timelineQuery.isFetchingNextPage,
    retry,
  };
}

/**
 * How many questions the timeline of a thread holds. A timeline shows the whole thread and each of its events carries
 * the Job asked for, so a question just sent is told by this count and not by its Job (the owner 04.11: the message left
 * the feed until its turn reached the timeline).
 */
export function timelineQuestions(events: readonly { kind: string }[]) {
  return events.filter((event) => event.kind === 'user').length;
}

/** The turns of a thread while the timeline of its next turn is read: they do not blink away with each message. */
export function useThreadEvents<Event>(threadId: string, events: readonly Event[]): readonly Event[] {
  const kept = useRef({ threadId, events });
  if (events.length > 0 || kept.current.threadId !== threadId) kept.current = { threadId, events };
  return kept.current.events;
}

export type CanonKeeperTimelineClient = Pick<CanonKeeperApiClient, 'getAgentTimeline'>;
