import { Suspense, useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore } from 'react';
import { onlineManager, useQueryClient, type QueryClient } from '@tanstack/react-query';
import { edition } from '@edition';
import type { CanonKeeperApiClient } from './shared/api';
import { type AuthShellStatus, useAuthShellStore } from './app/auth-store';
import { focusPageContent, usePageFocus } from './app/page-focus';
import { openPages } from './app/pages';
import { RouteFallback } from './app/RouteFallback';
import {
  classifyAuthFailure,
  useCurrentUserQuery,
} from './entities/session/api';
import { toHref, type AppRoute, type RouteNavigate } from './shared/navigation/app-route';
import { sectionTitle, usePageTitlePart } from './shared/navigation/page-title';
import { Alert } from './ui/feedback/Alert';
import { NoticeSlot, useSteadyPageUnderNotices } from './ui/feedback/notice-slot';
import { ToastsProvider } from './ui/feedback/Toast';
import { useToastQueue } from './ui/feedback/useToastQueue';
import styles from './App.module.css';
import { NotesProvider } from './features/notes/NotesProvider';
import { ServiceUnavailable, requestIdFrom } from './app/ServiceUnavailable';
import { AnswerReadyWatcher } from './features/chat/AnswerReadyWatcher';
import { SagaReadyWatcher } from './features/chat/SagaReadyWatcher';
import { SagaSectionsProvider } from './ui/saga-sections';
import { shellOfSurface } from './ui/shell/shell-routes';
import { AppShell } from './app/shell/AppShell';
import { NotFound } from './app/shell/NotFound';
import { PageErrorBoundary } from './app/PageErrorBoundary';

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
  // A query is added to the cache while the page that asks for it renders: that is no failure yet, and telling the root of
  // it then would update the root while another component renders (specs/2026-11-11-ux9-shelf-chat.md SC2.5, W-35). The
  // failures come with the updates of a query and of its observers, after the render.
  const subscribe = useCallback(
    (notify: () => void) => queryClient.getQueryCache().subscribe((event) => {
      if (event.type !== 'added') notify();
    }),
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
  // The tab names the section of the address on every page, before and after signing in (specs/2026-11-10-ux1-shell.md SH1.2).
  usePageTitlePart('section', sectionTitle(route));
  // A chapter of «Письмо» opened from its list is a new page too (specs/2026-11-11-ux9-writing-a.md WA1.4).
  usePageFocus(route.path, route.path === '/writing' ? route.query.get('draft') : null);
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

  // «Соединение восстановлено» is said by the end of the outage itself, whoever checked the connection — «Повторить»,
  // the check of the strip, the network coming back or the page: a retry that another check overtook left without the
  // line (specs/2026-11-11-ux9-account.md AC3.3, the flake of the full check of 12.11).
  const outageSeenRef = useRef(false);
  const pushConnectionToast = connectionToasts.push;
  useEffect(() => {
    if (serviceOutage) {
      outageSeenRef.current = true;
      return;
    }
    if (!outageSeenRef.current || status === 'checking') return;
    outageSeenRef.current = false;
    // An outage that ended at the way in says nothing: the author signs in, he did not get a page back.
    if (status !== 'authenticated') return;
    pushConnectionToast({ message: 'Соединение восстановлено', tone: 'success' });
    setOutageAttempt(1);
  }, [pushConnectionToast, serviceOutage, status]);

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
  // An error of the page stays in its place: the shell and «Написать нам» of the account stay with it (specs/2026-11-11-ux9-account.md AC3.2).
  const pageContent = (
    <PageErrorBoundary key={route.path}>
      {pageDenied ? <RouteFallback /> : page?.render({ api, navigate, onLogout, route, user }) ?? <NotFound navigate={navigate} />}
    </PageErrorBoundary>
  );
  // One shell of the author around the page (specs/2026-11-10-ux2-shell-core.md SC1.1).
  const surfaceContent = shellOfSurface[route.surface]
    ? <AppShell api={api} navigate={navigate} onLogout={onLogout} route={route} user={user}>{pageContent}</AppShell>
    : <Suspense fallback={<RouteFallback />}>{pageContent}</Suspense>;

  return (
    <NoticeSlot.Provider value={noticeSlot}>
      {/* The one queue and area of the toasts of the tab (specs/2026-11-10-ux2-patterns-actions.md PA1.1). */}
      <ToastsProvider queue={connectionToasts}>
        {/* The first stop of Tab on every page of the author (specs/2026-11-10-ux1-shell.md SH2.3): seen only in focus. */}
        <a className={styles.skipLink} href="#page-content" onClick={(event) => {
          event.preventDefault();
          focusPageContent();
        }}>К содержимому</a>
        {serviceOutage
          ? (
            // A strip above the open page, in the place of the notices: the page stays readable (specs/2026-11-11-ux9-account.md AC3.3).
            <ServiceUnavailable
              attempt={outageAttempt}
              onRetry={() => void retryService()}
              requestId={requestIdFrom(currentUserQuery.error)}
              retrying={retryingOutage}
              slot={noticeSlot}
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
            {/* «Книга прочитана» stands under the top bar, in the place of the notices of the shell (specs/2026-11-10-ux3-saga-state.md SS3.5). */}
            <SagaReadyWatcher api={api} navigate={navigate} route={route}>
              {account.wrapPages(
                <SagaSectionsProvider value={edition.sections}>
                  <NotesProvider api={api} accountId={user.id} navigate={navigate} route={route}>
                    {surfaceContent}
                  </NotesProvider>
                </SagaSectionsProvider>,
              )}
            </SagaReadyWatcher>
            {account.notices}
          </div>,
        )}
        {account.overlay}
      </ToastsProvider>
    </NoticeSlot.Provider>
  );
}
