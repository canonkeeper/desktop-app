import type { CanonKeeperApiClient, OperationRequest } from '../shared/api';
import { createAppRoute } from '../shared/navigation/app-route';
import { browserClass } from '../shared/support/browser-class';

// Отчёты браузера об ошибках (specs/2026-10-28-reliability.md, UC5): только вид, класс ошибки,
// раздел приложения, файл сборки, позиции стека в файлах сборки и класс браузера. Текст ошибки,
// адрес страницы с параметрами и текст пользователя сюда не попадают.

type ClientErrorReport = OperationRequest<'reportClientError'>;
export type ClientErrorKind = ClientErrorReport['kind'];

export interface ClientErrorEnvironment {
  origin: string;
  pathname: string;
  entryScript: string;
  userAgent: string;
}

export interface ClientErrorReporter {
  report(kind: ClientErrorKind, error: unknown): void;
}

const maxReportsPerPage = 5;
const maxFrames = 5;
const namePattern = /^[A-Za-z][A-Za-z0-9_.]{0,59}$/;
const filePattern = /^[A-Za-z0-9._-]{1,80}$/;
const frameLocation = /(\S+?):(\d{1,7}):(\d{1,7})\)?$/;
const chunkLoadMessage = /Failed to fetch dynamically imported module|error loading dynamically imported module|Importing a module script failed|Unable to preload CSS/i;
const chunkReloadKey = 'canonkeeper.chunk-reload-at';
const chunkReloadInterval = 60_000;

export function createClientErrorReporter(
  api: Pick<CanonKeeperApiClient, 'reportClientError'>,
  environment: () => ClientErrorEnvironment = browserEnvironment,
): ClientErrorReporter {
  const sent = new Set<string>();
  return {
    report(kind, error) {
      if (sent.size >= maxReportsPerPage) return;
      const report = buildReport(kind, error, environment());
      const key = JSON.stringify([report.kind, report.name, report.frames]);
      if (sent.has(key)) return;
      sent.add(key);
      // Отказ отправки не порождает новый отчёт: страница без сети не зацикливается.
      void api.reportClientError(report).catch(() => undefined);
    },
  };
}

/** Слушает необработанные ошибки и отказы промисов; возвращает отмену подписки. */
export function installClientErrorReporting(reporter: ClientErrorReporter, target: EventTarget = window) {
  const onError = (event: Event) => {
    const { error } = event as ErrorEvent;
    if (error === undefined) return;
    reporter.report(isChunkLoadError(error) ? 'chunk' : 'error', error);
  };
  const onRejection = (event: Event) => {
    const { reason } = event as PromiseRejectionEvent;
    reporter.report(isChunkLoadError(reason) ? 'chunk' : 'rejection', reason);
  };
  target.addEventListener('error', onError);
  target.addEventListener('unhandledrejection', onRejection);
  return () => {
    target.removeEventListener('error', onError);
    target.removeEventListener('unhandledrejection', onRejection);
  };
}

let installedReporter: ClientErrorReporter | null = null;

/** Отчёт приложения: main.tsx устанавливает отправителя, экран ошибки сообщает об ошибке рендера. */
export function setClientErrorReporter(reporter: ClientErrorReporter | null) {
  installedReporter = reporter;
}

export function reportClientError(kind: ClientErrorKind, error: unknown) {
  installedReporter?.report(kind, error);
}

/** Код сборки, которой больше нет на сервере (старая вкладка после выпуска). */
export function isChunkLoadError(error: unknown) {
  return error instanceof Error && (error.name === 'ChunkLoadError' || chunkLoadMessage.test(error.message));
}

interface SessionStore {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
}

/** Разрешает перезагрузку после отказа чанка не чаще раза в минуту, чтобы не зациклиться. */
export function shouldReloadAfterChunkFailure(storage: SessionStore | undefined, now = Date.now()) {
  try {
    if (!storage) return false;
    const last = Number(storage.getItem(chunkReloadKey));
    if (Number.isFinite(last) && last > 0 && now - last < chunkReloadInterval) return false;
    storage.setItem(chunkReloadKey, String(now));
    return true;
  } catch {
    return false;
  }
}

function buildReport(kind: ClientErrorKind, error: unknown, environment: ClientErrorEnvironment): ClientErrorReport {
  const name = error instanceof Error && namePattern.test(error.name) ? error.name : error instanceof Error ? 'Error' : 'NonError';
  const surface = createAppRoute(environment.pathname, new URLSearchParams()).surface;
  const report: ClientErrorReport = {
    kind,
    name,
    surface,
    build: fileName(environment.entryScript, environment.origin) ?? 'unknown',
    browser: browserClass(environment.userAgent),
    mobile: /Mobi|Android|iPhone|iPad/.test(environment.userAgent),
  };
  const frames = error instanceof Error ? bundleFrames(error.stack ?? '', environment.origin) : [];
  if (frames.length > 0) report.frames = frames;
  return report;
}

function bundleFrames(stack: string, origin: string) {
  const frames: string[] = [];
  for (const line of stack.split('\n')) {
    const match = frameLocation.exec(line.trim());
    if (!match) continue;
    const [, location, row, column] = match;
    const file = fileName(location!.replace(/^.*\(/, ''), origin);
    if (file) frames.push(`${file}:${row}:${column}`);
    if (frames.length === maxFrames) break;
  }
  return frames;
}

/** Имя файла сборки этого сайта; чужие и встроенные скрипты не называются. */
function fileName(location: string, origin: string) {
  try {
    const url = new URL(location);
    if (url.origin !== origin) return null;
    const file = url.pathname.split('/').at(-1) ?? '';
    return filePattern.test(file) ? file : null;
  } catch {
    return null;
  }
}

function browserEnvironment(): ClientErrorEnvironment {
  const entry = document.querySelector<HTMLScriptElement>('script[type="module"][src]');
  return {
    origin: window.location.origin,
    pathname: window.location.pathname,
    entryScript: entry?.src ?? '',
    userAgent: navigator.userAgent,
  };
}
