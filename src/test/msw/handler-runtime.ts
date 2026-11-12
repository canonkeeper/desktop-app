import { Buffer } from 'node:buffer';
import { http, HttpResponse } from 'msw';
import type { MockRepository } from '../../api/mock/repository';
import {
  agentWorkflowOperationIds,
  MockApiError,
  type MockActorRole,
  type MockScenarioControl,
} from '../../api/mock/scenarios';
import { getOperationManifestEntries, type OperationId, type OperationManifestRoute } from '../../shared/api';

export const mswApiBaseUrl = 'http://localhost/api/v1';

export type MswSecurityContext = {
  csrfState?: MswCsrfState;
  hasActiveSession?: () => boolean;
  operationId: OperationId;
  request: Request;
  repository?: MockRepository;
};
export type MswCsrfState = {
  tokenVersion: number;
};
export type MswIdempotencyRecord = {
  body: unknown;
  fingerprint: string;
  headers?: Record<string, string>;
  status: number;
};
type FileWithMockContent = File & { mockContent?: string };
const sessionCookieName = 'ck_session';
const sessionCookieValue = 'mock-session';
const sessionCookieMaxAgeSeconds = 43_200;
const csrfTokenPrefix = 'ck_msw_csrf_token_';
const minIdempotencyKeyLength = 8;
const maxIdempotencyKeyLength = 128;
export const repositoryCsrfStates = new WeakMap<MockRepository, MswCsrfState>();
const allowedCorsOrigins = new Set([
  'http://localhost:3000',
  'http://127.0.0.1:3000',
  'http://[::1]:3000',
  'http://localhost:5173',
  'http://127.0.0.1:5173',
  'http://[::1]:5173',
]);
/**
 * The drawings the web starts and the author's own images keep their idempotency in the mock gallery
 * (specs/2026-10-29-saga-illustrations.md UC8, UC15).
 */
const imageDrawingOperationIds: OperationId[] = [
  'cutoutProjectImage', 'changeProjectImageAngle', 'createProjectImageVariant', 'chooseProjectImageVariant', 'redrawProjectImageVariant', 'enlargeProjectImage', 'createProjectImageLayer', 'createProjectCharacterSheet',
  'createIllustrationStyleSketches', 'deleteProjectImages', 'uploadProjectImage',
  // The workshop (specs/2026-11-02-image-workshop.md).
  'createDrawing', 'drawDrawing', 'chooseProjectImageLayerVariant', 'submitDrawingTurn',
  // The helper of the chapter workshop (specs/2026-11-02-writer-workshop.md W1.2).
  'submitDraftTurn',
  'startAuthorMaterialsImport', 'confirmAuthorMaterialsImport',
];
const idempotentOperations = new Set<OperationId>([
  'createAuthorMemory',
  'createAuthorMaterial',
  'createChapterDraft',
  'startChapterDraftJob',
  'startChapterProofread',
  'loadChapterDraftFile',
  'createPersonalNote',
  'importBookFile',
  'startProjectIndexing',
  'createChatSession',
  'createChatTurn',
  'createChatTurnWithChapter',
  'steerChatTurn',
  'createImportQueue',
  'uploadImportQueueItem',
  'startImportQueue',
  ...imageDrawingOperationIds,
  'createAdminPromoPermission',
]);
const repositoryManagedIdempotencyOperations = new Set<OperationId>([
  'createAuthorMemory',
  'createAuthorMaterial',
  'createChapterDraft',
  'startChapterDraftJob',
  'createPersonalNote',
  ...agentWorkflowOperationIds,
  ...imageDrawingOperationIds,
  'createImportQueue',
  'uploadImportQueueItem',
  'startImportQueue',
  'createAdminPromoPermission',
]);
const operationManifestById = new Map<OperationId, OperationManifestRoute>(getOperationManifestEntries());

export function mswPath(operationId: OperationId) {
  const route = operationManifestById.get(operationId);
  if (!route) {
    throw new Error(`Missing generated operation route for ${operationId}`);
  }
  return `${mswApiBaseUrl}${route.path.replace(/\{([^}]+)\}/g, ':$1')}`;
}

export function createMswCsrfState(): MswCsrfState {
  return { tokenVersion: 0 };
}

function currentMswCsrfTokenValue(state: MswCsrfState) {
  return `${csrfTokenPrefix}${String(state.tokenVersion).padStart(6, '0')}`;
}

function rotateMswCsrfToken(state: MswCsrfState) {
  state.tokenVersion += 1;
}

export function csrfHandler(csrfState: MswCsrfState) {
  return http.get(mswPath('getCsrfToken'), () =>
    HttpResponse.json({
      csrfToken: currentMswCsrfTokenValue(csrfState),
      expiresAt: '2026-06-21T18:00:00.000Z',
      rotation: 'refreshed',
    }, { headers: noStoreHeaders() }),
  );
}

export function oneParam(value: string | readonly string[] | undefined) {
  return Array.isArray(value) ? (value[0] ?? '') : (value ?? '');
}

export async function jsonResponse<T>(load: () => Promise<T> | T, init?: ResponseInit, security?: MswSecurityContext) {
  try {
    const headers = noStoreHeaders(init?.headers);
    headers.set('content-type', 'application/json');
    return new Response(JSON.stringify(await withMswSecurity(load, security)), { ...init, headers });
  } catch (error) {
    return problemResponse(error);
  }
}

export async function jsonOrEmptyResponse<T>(load: () => Promise<T | undefined> | T | undefined, security?: MswSecurityContext) {
  try {
    const body = await withMswSecurity(load, security);
    if (body === undefined) {
      return new Response(null, { status: 204, headers: noStoreHeaders() });
    }
    const headers = noStoreHeaders({ 'content-type': 'application/json' });
    return new Response(JSON.stringify(body), { headers });
  } catch (error) {
    return problemResponse(error);
  }
}

export async function idempotentJsonResponse<T>(
  load: (options: { idempotencyKey: string }, requestBody: unknown) => Promise<T> | T,
  init: ResponseInit,
  security: MswSecurityContext,
  requestBody: unknown | (() => Promise<unknown> | unknown),
  store: Map<string, MswIdempotencyRecord>,
) {
  try {
    const cleanup = enterMswSecurity(security);
    try {
      const resolvedRequestBody = typeof requestBody === 'function' ? await requestBody() : requestBody;
      const idempotency = await mswIdempotencyContext(security.request, security.operationId, resolvedRequestBody);
      const repositoryManaged = repositoryManagedIdempotencyOperations.has(security.operationId);
      const existing = repositoryManaged ? undefined : store.get(idempotency.key);
      if (existing && existing.fingerprint !== idempotency.fingerprint) {
        throw new MockApiError(409, 'Idempotency-Key was reused with a different request payload.', security.operationId);
      }
      if (existing) {
        const headers = noStoreHeaders(existing.headers);
        headers.set('content-type', 'application/json');
        return new Response(JSON.stringify(existing.body), { status: existing.status, headers });
      }

      const body = await load({ idempotencyKey: idempotency.idempotencyKey }, resolvedRequestBody);
      const status = init.status ?? 200;
      const headers = asyncStartHeaders(body, init.headers, security.operationId);
      if (!repositoryManaged) {
        store.set(idempotency.key, {
          body,
          fingerprint: idempotency.fingerprint,
          headers: Object.fromEntries(headers.entries()),
          status,
        });
      }
      headers.set('content-type', 'application/json');
      return new Response(JSON.stringify(body), { ...init, headers });
    } finally {
      cleanup();
    }
  } catch (error) {
    return problemResponse(error);
  }
}

export async function emptyResponse(load: () => Promise<void> | void, security?: MswSecurityContext, init?: ResponseInit) {
  try {
    await withMswSecurity(load, security);
    return new Response(null, { ...init, status: init?.status ?? 204, headers: noStoreHeaders(init?.headers) });
  } catch (error) {
    return problemResponse(error);
  }
}

type BinaryResponseValue = {
  body: BodyInit | null;
  contentType: string;
  status?: number;
  headers?: HeadersInit;
  cacheControl?: string;
};

export async function binaryResponse(load: () => Promise<BinaryResponseValue> | BinaryResponseValue, security?: MswSecurityContext) {
  try {
    const result = await withMswSecurity(load, security);
    const headers = noStoreHeaders(result.headers);
    headers.set('cache-control', result.cacheControl ?? 'no-store');
    headers.set('content-type', result.contentType);
    return new Response(result.body, { headers, status: result.status ?? 200 });
  } catch (error) {
    return problemResponse(error);
  }
}

async function withMswSecurity<T>(load: () => Promise<T> | T, security?: MswSecurityContext) {
  if (!security) {
    return await load();
  }
  const csrfState = csrfStateForSecurity(security);
  const cleanup = enterMswSecurity(security);
  try {
    const result = await load();
    if (rotatesCsrfAfterSuccess(security.operationId) && csrfState) {
      rotateMswCsrfToken(csrfState);
    }
    return result;
  } finally {
    cleanup();
  }
}

function enterMswSecurity(security: MswSecurityContext) {
  const repository = security.repository;
  const previousScenario = repository?.getScenario();
  const sessionActive = security.hasActiveSession?.() ?? repository?.hasActiveSession() ?? true;
  const actorRole = enforceMswRequest(security.operationId, security.request, csrfStateForSecurity(security), sessionActive, previousScenario?.actorRole ?? 'owner');
  if (!security.repository) {
    return () => undefined;
  }
  if (!repository || !previousScenario) {
    return () => undefined;
  }
  repository.setScenario({
    ...previousScenario,
    actorRole,
    ...verifiedEmailScenario(security.request),
  });
  return () => {
    repository.setScenario(previousScenario);
  };
}

function csrfStateForSecurity(security: MswSecurityContext) {
  return security.csrfState ?? (security.repository ? repositoryCsrfStates.get(security.repository) : undefined);
}

function enforceMswRequest(
  operationId: OperationId,
  request: Request,
  csrfState: MswCsrfState | undefined,
  sessionActive: boolean,
  fallbackActorRole: MockActorRole = 'owner',
): MockActorRole {
  if (requiresStrictOrigin(request.method, operationId) && !hasAllowedRequestOrigin(request)) {
    throw new MockApiError(403, 'Strict local Origin is required.', operationId);
  }
  const logoutSessionResolved = operationId === 'logoutUser' && hasMswSessionCookie(request);
  if (operationId !== 'logoutUser' && !isPublicOperation(operationId) && (!hasMswSessionCookie(request) || !sessionActive)) {
    throw new MockApiError(401, 'Сессия истекла. Войдите снова.', operationId);
  }
  if (requiresCsrf(request.method) && (operationId !== 'logoutUser' || logoutSessionResolved)) {
    if (!csrfState || request.headers.get('x-csrf-token') !== currentMswCsrfTokenValue(csrfState)) {
      throw new MockApiError(403, 'CSRF token is missing or invalid.', operationId);
    }
  }
  return readActorRole(request) ?? fallbackActorRole;
}

const mswPublicOperations = new Set<OperationId>([
  'getCsrfToken', 'registerUser', 'loginUser', 'confirmEmailVerification', 'requestPasswordReset', 'resetPassword',
  // The page «Новый пароль» checks its link on opening (specs/2026-11-10-ux3-auth.md AU4.1).
  'checkPasswordResetLink',
  'readPromoLink', 'answerPromoLink', 'revokePromoLink',
  'takeReadingTestPair', 'voteReadingTestPair',
  // «Связаться с нами» of a guest (specs/2026-11-07-contacts.md F1).
  'getContacts', 'uploadFeedbackImage', 'createFeedbackThread',
  // «Тарифы» of a guest (specs/2026-11-08-subscription-tiers.md TR1.6).
  'listSubscriptionTiers',
  // The window of the desktop opens its session with a one-time code (specs/2026-11-07-desktop-app.md DA3.3).
  'openDesktopSession',
]);

function isPublicOperation(operationId: OperationId) {
  return mswPublicOperations.has(operationId);
}

function requiresCsrf(method: string) {
  return ['POST', 'PUT', 'PATCH', 'DELETE'].includes(method.toUpperCase());
}

function requiresStrictOrigin(method: string, operationId: OperationId) {
  return ['POST', 'PUT', 'PATCH', 'DELETE'].includes(method.toUpperCase()) || operationId === 'searchProject';
}

function rotatesCsrfAfterSuccess(operationId: OperationId) {
  return operationId === 'loginUser' || operationId === 'rotateSession' || operationId === 'startImpersonation' || operationId === 'stopImpersonation';
}

export function hasMswSessionCookie(request: Request) {
  return parseCookies(request.headers.get('cookie'))[sessionCookieName] === sessionCookieValue;
}

function parseCookies(header: string | null) {
  const cookies: Record<string, string> = {};
  for (const segment of header?.split(';') ?? []) {
    const [rawKey, ...rawValue] = segment.trim().split('=');
    if (rawKey && rawValue.length > 0) {
      cookies[rawKey] = rawValue.join('=');
    }
  }
  return cookies;
}

function readActorRole(request: Request): MockActorRole | undefined {
  const raw = request.headers.get('mock_actor_role') ?? request.headers.get('mock-actor-role');
  if (raw === null) {
    return undefined;
  }
  if (raw === 'owner' || raw === 'admin' || raw === 'editor' || raw === 'viewer' || raw === 'non_member') {
    return raw;
  }
  throw new MockApiError(400, 'Invalid mock actor role.', 'listProjects');
}

function verifiedEmailScenario(request: Request): Partial<Pick<MockScenarioControl, 'emailVerified' | 'verifiedEmail'>> {
  const verifiedEmail = request.headers.get('mock-verified-email');
  const emailVerified = request.headers.get('mock-email-verified');
  if (!verifiedEmail && emailVerified === null) {
    return {};
  }
  return {
    ...(emailVerified !== null ? { emailVerified: emailVerified !== 'false' } : {}),
    ...(verifiedEmail ? { verifiedEmail } : {}),
  };
}

function hasAllowedRequestOrigin(request: Request) {
  const fetchSite = request.headers.get('sec-fetch-site');
  if (fetchSite === 'cross-site') {
    return false;
  }
  const origin = request.headers.get('origin');
  if (origin) {
    return isAllowedLocalOrigin(origin);
  }
  const referer = request.headers.get('referer');
  if (referer) {
    return isAllowedLocalOrigin(referer);
  }
  if (fetchSite === 'same-origin') {
    return true;
  }
  return false;
}

function isAllowedLocalOrigin(origin: string) {
  try {
    return allowedCorsOrigins.has(new URL(origin).origin);
  } catch {
    return false;
  }
}

function noStoreHeaders(init?: HeadersInit) {
  const headers = new Headers(init);
  headers.set('cache-control', 'no-store');
  return headers;
}

export function sessionHeaders(init?: HeadersInit) {
  const headers = noStoreHeaders(init);
  headers.set('set-cookie', `${sessionCookieName}=${sessionCookieValue}; Path=/; HttpOnly; SameSite=Lax; Max-Age=${sessionCookieMaxAgeSeconds}`);
  return headers;
}

export function clearSessionHeaders(init?: HeadersInit) {
  const headers = noStoreHeaders(init);
  headers.set('set-cookie', `${sessionCookieName}=; Path=/; HttpOnly; SameSite=Lax; Max-Age=0`);
  return headers;
}

function asyncStartHeaders(body: unknown, init?: HeadersInit, operationId?: OperationId) {
  const headers = noStoreHeaders(init);
  if (operationId === 'createImportQueue' && body && typeof body === 'object' && 'id' in body) {
    headers.set('Location', String((body as { links?: { self?: unknown } }).links?.self ?? (body as { id: string }).id));
    return headers;
  }
  if (operationId === 'uploadImportQueueItem' || operationId === 'startImportQueue') {
    return headers;
  }
  const jobId = body && typeof body === 'object' && 'jobId' in body ? (body as { jobId?: unknown }).jobId : undefined;
  headers.set('Location', typeof jobId === 'string' ? `/api/v1/jobs/${jobId}` : '/api/v1/jobs');
  headers.set('Retry-After', '1');
  return headers;
}

async function mswIdempotencyContext(request: Request, operationId: OperationId, body: unknown) {
  if (!idempotentOperations.has(operationId)) {
    throw new MockApiError(500, 'MSW idempotency guard used for a non-idempotent operation.', operationId);
  }
  const header = request.headers.get('idempotency-key');
  if (!header) {
    throw new MockApiError(400, 'Idempotency-Key header is required.', operationId);
  }
  validateIdempotencyKey(operationId, header);
  const principal = parseCookies(request.headers.get('cookie'))[sessionCookieName] ?? 'anonymous';
  const pathname = new URL(request.url).pathname;
  return {
    idempotencyKey: header,
    key: `${principal}:${request.method}:${pathname}:${operationId}:${header}`,
    fingerprint: await stableStringify(body),
  };
}

function validateIdempotencyKey(operationId: OperationId, idempotencyKey: string) {
  if (idempotencyKey.length < minIdempotencyKeyLength || idempotencyKey.length > maxIdempotencyKeyLength) {
    throw new MockApiError(400, 'Idempotency-Key must be between 8 and 128 characters.', operationId);
  }
}

async function stableStringify(value: unknown): Promise<string> {
  if (typeof File !== 'undefined' && value instanceof File) {
    return JSON.stringify({ content: await fileContentForFingerprint(value), name: value.name, size: value.size, type: value.type });
  }
  if (Array.isArray(value)) {
    return `[${(await Promise.all(value.map(stableStringify))).join(',')}]`;
  }
  if (value && typeof value === 'object') {
    const entries = await Promise.all(Object.entries(value)
      .sort(([left], [right]) => left.localeCompare(right))
      .map(async ([key, entry]) => `${JSON.stringify(key)}:${await stableStringify(entry)}`));
    return `{${entries.join(',')}}`;
  }
  return JSON.stringify(value) ?? 'undefined';
}

async function fileContentForFingerprint(file: File): Promise<string> {
  const mockContent = (file as FileWithMockContent).mockContent;
  if (typeof mockContent === 'string') {
    return mockContent;
  }
  if ('text' in file && typeof file.text === 'function') {
    return await file.text();
  }
  if ('arrayBuffer' in file && typeof file.arrayBuffer === 'function') {
    return Buffer.from(await file.arrayBuffer()).toString('latin1');
  }
  return '';
}

function problemResponse(error: unknown) {
  const headers = noStoreHeaders({ 'content-type': 'application/problem+json' });
  if (error instanceof MockApiError) {
    if (error.status === 429) {
      headers.set('retry-after', '60');
    }
    const title = titleForStatus(error.status);
    return HttpResponse.json(
      {
        code: error.code ?? problemCode(title),
        detail: error.status === 404 ? 'Resource not found.' : error.message,
        requestId: 'req_msw',
        status: error.status,
        title,
        type: 'about:blank',
      },
      { status: error.status, headers },
    );
  }

  return HttpResponse.json(
    {
      code: 'mock_error',
      detail: error instanceof Error ? error.message : 'Request failed',
      requestId: 'req_msw',
      status: 500,
      title: 'Request failed',
      type: 'about:blank',
    },
    { status: 500, headers },
  );
}

function problemCode(title: string) {
  return title.toLowerCase().replace(/[^a-z0-9]+/g, '_').replace(/^_|_$/g, '') || 'mock_error';
}

function titleForStatus(status: number) {
  switch (status) {
    case 400:
      return 'Bad request';
    case 401:
      return 'Unauthorized';
    case 403:
      return 'Forbidden';
    case 404:
      return 'Not found';
    case 409:
      return 'Conflict';
    case 410:
      return 'Gone';
    case 429:
      return 'Rate limited';
    case 413:
      return 'Payload too large';
    case 415:
      return 'Unsupported media type';
    case 422:
      return 'Unprocessable entity';
    default:
      return status >= 500 ? 'Internal server error' : 'Request failed';
  }
}
