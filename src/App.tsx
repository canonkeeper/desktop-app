import { Suspense, useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore } from 'react';
import { onlineManager, useQueryClient, type QueryClient } from '@tanstack/react-query';
import { edition } from '@edition';
import type { CanonKeeperApiClient } from './shared/api';
import { type AuthShellStatus, useAuthShellStore } from './app/auth-store';
import { openPages } from './app/pages';
import { RouteFallback } from './app/RouteFallback';
import {
  classifyAuthFailure,
  useCurrentUserQuery,
} from './entities/session/api';
import { canonicalProductRoutes, createAppRoute, toHref, type AppRoute, type RouteNavigate } from './shared/navigation/app-route';
import { Button } from './ui/controls/Button';
import { Alert } from './ui/feedback/Alert';
import { NoticeSlot, useSteadyPageUnderNotices } from './ui/feedback/notice-slot';
import { ToastRegion } from './ui/feedback/Toast';
import { useToastQueue } from './ui/feedback/useToastQueue';
import styles from './App.module.css';
import { NotesProvider } from './features/notes/NotesProvider';
import { ServiceUnavailable, requestIdFrom } from './app/ServiceUnavailable';
import { AnswerReadyWatcher } from './features/chat/AnswerReadyWatcher';
import { TopBarStrip } from './ui/navigation/TopBarStrip';
import { SagaSectionsProvider } from './ui/saga-sections';

function findActiveSessionFailure(queryClient: QueryClient) {
  for (const query of queryClient.getQueryCache().getAll()) {
    if (query.getObserversCount() === 0 || !query.state.error) continue;
    const failure = classifyAuthFailure(query.state.error, '');
    if (failure.kind === 'unauthorized'
      || query.meta?.outageScope !== 'local' && (failure.kind === 'network' || failure.kind === 'server')) {
      return query.state.error;
    }
  }
  return null;
}

function useActiveSessionFailure(queryClient: QueryClient) {
  const subscribe = useCallback(
    (notify: () => void) => queryClient.getQueryCache().subscribe(notify),
    [queryClient],
  );
  const getSnapshot = useCallback(() => findActiveSessionFailure(queryClient), [queryClient]);
  return useSyncExternalStore(subscribe, getSnapshot, () => null);
}

interface AppProps {
  api: CanonKeeperApiClient;
  navigate: RouteNavigate;
  route: AppRoute;
}

/**
 * The shell of every edition: who the author is, an outage of the service, the page of the address and the notices.
 * What the account of the edition adds — sign-in, documents, public pages — comes from `@edition`.
 */
export function App({ api, navigate, route }: AppProps) {
  const { clearAuthMessage, errorMessage, setFailure } = useAuthShellStore();
  const queryClient = useQueryClient();
  const currentUserQuery = useCurrentUserQuery(api);
  const activeFailureError = useActiveSessionFailure(queryClient);
  const outagePausedRef = useRef(false);
  const probedFailureRef = useRef<unknown>(null);
  const [retryingOutage, setRetryingOutage] = useState(false);
  const [outageAttempt, setOutageAttempt] = useState(1);
  const connectionToasts = useToastQueue();
  // The place of notices above the page: every toast stands there and covers nothing (the owner 04.11).
  const [noticeSlot, setNoticeSlot] = useState<HTMLElement | null>(null);
  useSteadyPageUnderNotices(noticeSlot);
  const requestedHref = route.surface === 'manuscript' && route.invalidQuery
    ? '/manuscript/books'
    : toHref(route.path, Object.fromEntries(route.query));
  const sessionFailure = useMemo(
    () =>
      currentUserQuery.error
        ? classifyAuthFailure(currentUserQuery.error, 'Не удалось проверить вход.')
        : null,
    [currentUserQuery.error],
  );
  const activeFailure = useMemo(
    () => (activeFailureError ? classifyAuthFailure(activeFailureError, 'Сервис временно недоступен.') : null),
    [activeFailureError],
  );
  const unauthorizedFailure = sessionFailure?.kind === 'unauthorized'
    ? sessionFailure
    : activeFailure?.kind === 'unauthorized'
      ? activeFailure
      : null;
  const serviceOutage = !unauthorizedFailure
    && (sessionFailure?.kind === 'network' || sessionFailure?.kind === 'server');
  const user = currentUserQuery.data ?? null;
  const status: AuthShellStatus = currentUserQuery.isPending
    ? 'checking'
    : unauthorizedFailure
        ? 'anonymous'
        : user
          ? 'authenticated'
          : sessionFailure
            ? 'error'
            : 'anonymous';
  const account = edition.useAccount({
    api, navigate, route, requestedHref, status, user, unauthorizedFailure, serviceOutage,
    toasts: {
      push: connectionToasts.push,
      dismissAll: () => connectionToasts.queue.forEach((toast) => connectionToasts.dismiss(toast.id)),
    },
  });
  const page = edition.pages[route.surface];
  // Admin panel R1.4: a page for some authors only — anyone else lands on the shelf without a message.
  const pageDenied = Boolean(page?.allows && !(user && page.allows(user)));

  useEffect(() => {
    if (sessionFailure && sessionFailure.kind !== 'unauthorized') {
      setFailure(sessionFailure.kind, sessionFailure.message);
      return;
    }
    clearAuthMessage();
  }, [clearAuthMessage, sessionFailure, setFailure]);

  useEffect(() => {
    const needsProbe = Boolean(user)
      && !sessionFailure
      && (activeFailure?.kind === 'network' || activeFailure?.kind === 'server');
    if (!needsProbe) {
      if (!activeFailureError) probedFailureRef.current = null;
      return;
    }
    if (probedFailureRef.current === activeFailureError) return;
    probedFailureRef.current = activeFailureError;
    void currentUserQuery.refetch();
  }, [activeFailure, activeFailureError, currentUserQuery, sessionFailure, user]);

  useEffect(() => {
    if (serviceOutage) {
      outagePausedRef.current = true;
      onlineManager.setOnline(false);
      return;
    }
    if (outagePausedRef.current) {
      outagePausedRef.current = false;
      onlineManager.setOnline(true);
    }
  }, [serviceOutage]);

  useEffect(() => () => {
    if (outagePausedRef.current) onlineManager.setOnline(true);
  }, []);

  useEffect(() => {
    // R1.1: a signed-in writer goes from the home page straight to the shelf.
    if ((route.surface === 'home' || pageDenied) && status === 'authenticated' && user) {
      navigate('/manuscript/books', undefined, { replace: true });
    }
  }, [navigate, pageDenied, route.surface, status, user]);

  const hasPublicPage = Boolean(account.publicPage);
  useEffect(() => {
    if (status === 'authenticated' && !hasPublicPage) {
      void (page && !pageDenied && page.preload ? page.preload() : openPages.manuscript.preload());
    }
  }, [hasPublicPage, page, pageDenied, status]);

  async function retryService() {
    setOutageAttempt((attempt) => attempt + 1);
    setRetryingOutage(true);
    try {
      const sessionResult = await currentUserQuery.refetch();
      if (!sessionResult.isSuccess) return;
      onlineManager.setOnline(true);
      await queryClient.refetchQueries(
        {
          predicate: (query) => query.getObserversCount() > 0 && query.queryKey[1] !== 'session',
          type: 'active',
        },
        { cancelRefetch: false },
      );
      connectionToasts.push({ message: 'Соединение восстановлено', tone: 'success' });
      setOutageAttempt(1);
    } finally {
      setRetryingOutage(false);
    }
  }

  if (account.publicPage) return account.publicPage;

  if (route.surface === 'home') return <RouteFallback />;

  if (serviceOutage && !user) {
    return (
      <ServiceUnavailable
        attempt={outageAttempt}
        fullPage
        onRetry={() => void retryService()}
        requestId={requestIdFrom(currentUserQuery.error)}
        retrying={retryingOutage}
      />
    );
  }

  if (account.signInPage) return account.signInPage;
  // The edition has no sign-in of its own: the page waits until the author is known.
  if (status !== 'authenticated' || !user) return <RouteFallback />;

  const { onLogout } = account;
  const pageContent = pageDenied ? <RouteFallback /> : page?.render({ api, navigate, onLogout, route, user });
  const surfaceContent = pageContent ?? (
    <main className={styles.appShell}>
      <header className={styles.header}>
        <div>
          <p className={styles.eyebrow}>Canon Keeper</p>
          <h1 id="route-not-found-title">Страница не найдена</h1>
        </div>
        <nav className={styles.nav} aria-label="Основная навигация">
          {canonicalProductRoutes().filter((item) => edition.pages[createAppRoute(item.path).surface]).map((item) => (
            <button
              className={route.path === item.path ? styles.activeNav : styles.navButton}
              key={item.path}
              onClick={() => navigate(item.path)}
              type="button"
            >
              {item.label}
            </button>
          ))}
        </nav>
        {onLogout ? (
          <button
            className={styles.navButton}
            onClick={onLogout}
            type="button"
          >
            Выйти
          </button>
        ) : null}
      </header>
      <TopBarStrip />

      <section className={styles.panel} aria-labelledby="route-not-found-title">
        <p className={styles.copy}>Такой страницы нет.</p>
        <div className={styles.nav}>
          <Button onClick={() => navigate('/manuscript/books')} variant="primary">К моим книгам</Button>
          <Button onClick={() => navigate('/chat')} variant="outline">Открыть чат</Button>
        </div>
      </section>
    </main>
  );

  return (
    <NoticeSlot.Provider value={noticeSlot}>
      {serviceOutage
        ? (
          <ServiceUnavailable
            attempt={outageAttempt}
            onRetry={() => void retryService()}
            requestId={requestIdFrom(currentUserQuery.error)}
            retrying={retryingOutage}
          />
        )
        : errorMessage
          ? <Alert className={styles.sessionError} tone="error">{errorMessage}</Alert>
          : null}
      {account.wrapAccount(
        <div
          className={styles.productSurface}
          key={user.id}
        >
          <div className={styles.pageNotices} data-page-notices="" ref={setNoticeSlot} />
          <AnswerReadyWatcher api={api} dismiss={connectionToasts.dismiss} navigate={navigate} notify={connectionToasts.push} route={route} />
          {account.wrapPages(
            <SagaSectionsProvider value={edition.sections}>
              <NotesProvider api={api} accountId={user.id} navigate={navigate} route={route}>
                <Suspense fallback={<RouteFallback />}>{surfaceContent}</Suspense>
              </NotesProvider>
            </SagaSectionsProvider>,
          )}
          {account.notices}
        </div>,
      )}
      {account.overlay}
      <ToastRegion onDismiss={connectionToasts.dismiss} queue={connectionToasts.queue} />
    </NoticeSlot.Provider>
  );
}
