import createClient, { type Client } from 'openapi-fetch';
import type { paths } from './generated/openapi';
import { operationManifest, type OperationId } from './generated/operation-manifest';
import { noteServerDate } from '../clock/server-clock';
import type {
  ApiClient,
  ApiProblem,
  ApiRequestOptions,
  DownloadedFile,
  OperationRequest,
  OperationResponses,
  RequiredIdempotencyOptions,
  UploadProgress,
} from './contracts';

// Транспорт отвечает только за HTTP, защитные заголовки и разбор ошибок.
type ApiFetch = (request: Request) => Promise<Response>;
type OpenApiFetchResult = {
  data?: unknown;
  error?: unknown;
  response: Response;
};
type OpenApiFetchRequest = (
  method: Lowercase<(typeof operationManifest)[OperationId]['method']>,
  path: keyof paths,
  init?: { params?: unknown; body?: unknown; parseAs?: 'blob'; signal?: AbortSignal },
) => Promise<OpenApiFetchResult>;
type OperationInit<TOperation extends OperationId> = {
  params?: unknown;
  body?: OperationRequest<TOperation>;
  signal?: AbortSignal;
  headers?: Record<string, string>;
};
type MultipartRequestOptions = ApiRequestOptions & {
  onUploadProgress?: (progress: UploadProgress) => void;
};
export class ApiStatusError extends Error {
  constructor(
    readonly status: number,
    readonly problem: ApiProblem,
    readonly response: Response,
  ) {
    super(problem.title || `Request failed with ${status}`);
    this.name = 'ApiStatusError';
    Object.setPrototypeOf(this, new.target.prototype);
  }
}

export class NetworkApiError extends Error {
  constructor(readonly cause: unknown) {
    super('Network request failed.');
    this.name = 'NetworkApiError';
    Object.setPrototypeOf(this, new.target.prototype);
  }
}

export interface CanonKeeperApiClientOptions {
  baseUrl?: string;
  fetch?: ApiFetch;
}

function defaultBaseUrl() {
  return '/api/v1';
}

function problemFromError(error: unknown, status: number): ApiProblem {
  if (error && typeof error === 'object') {
    return error as ApiProblem;
  }

  return {
    type: 'about:blank',
    title: 'Request failed',
    status,
    detail: typeof error === 'string' ? error : undefined,
    code: 'request_failed',
    requestId: 'req_unavailable',
  };
}

function joinUrl(baseUrl: string, apiPath: string) {
  return `${baseUrl.replace(/\/$/, '')}${apiPath}`;
}

function applyPathParams(apiPath: string, pathParams: Record<string, string | number | boolean>) {
  return apiPath.replace(/\{([^}]+)\}/g, (_match, key: string) => {
    const value = pathParams[key];
    if (value === undefined) {
      throw new Error(`Missing path parameter: ${key}`);
    }
    return encodeURIComponent(String(value));
  });
}

function responseFromXhr(request: XMLHttpRequest) {
  const headers = new Headers();
  for (const line of request.getAllResponseHeaders().trim().split(/[\r\n]+/)) {
    const separator = line.indexOf(':');
    if (separator > 0) {
      headers.append(line.slice(0, separator).trim(), line.slice(separator + 1).trim());
    }
  }
  return new Response(request.responseText || null, {
    status: request.status,
    statusText: request.statusText,
    headers,
  });
}

/** The name of an attachment as RFC 6266 gives it: filename*=utf-8''… for a name that is not ASCII. */
function attachmentName(disposition: string | null) {
  const encoded = /filename\*=utf-8''([^;]+)/i.exec(disposition ?? '')?.[1];
  return encoded ? decodeURIComponent(encoded) : /filename="?([^";]+)"?/i.exec(disposition ?? '')?.[1];
}

function abortCause(signal: AbortSignal | undefined) {
  return signal?.reason ?? new DOMException('Request aborted.', 'AbortError');
}

const csrfHeaderName = 'X-CSRF-Token';
const idempotencyHeaderName = 'Idempotency-Key';

export function idempotencyHeaders(options: RequiredIdempotencyOptions | undefined, operationId: OperationId): Record<string, string> {
  const key = options?.idempotencyKey;
  if (!key) {
    throw new Error(`${operationId} requires an Idempotency-Key.`);
  }
  return { [idempotencyHeaderName]: key };
}

export function createIdempotencyKey(operationId: OperationId) {
  const randomPart = globalThis.crypto?.randomUUID?.() ?? `${Date.now()}_${Math.random().toString(36).slice(2, 10)}`;
  return `${operationId}:${randomPart}`;
}

function needsCsrf(method: string, headers: Record<string, string>) {
  return ['POST', 'PUT', 'PATCH', 'DELETE'].includes(method) && !headers[csrfHeaderName];
}

function withContractHeaders(method: string, params: unknown, headers: Record<string, string> = {}, csrfToken?: string) {
  const nextParams = params && typeof params === 'object' ? { ...(params as Record<string, unknown>) } : {};
  const nextHeaders = { ...((nextParams.header as Record<string, string> | undefined) ?? {}), ...headers };
  if (needsCsrf(method, nextHeaders) && csrfToken) {
    nextHeaders[csrfHeaderName] = csrfToken;
  }
  if (Object.keys(nextHeaders).length > 0) {
    nextParams.header = nextHeaders;
  }
  return Object.keys(nextParams).length > 0 ? nextParams : undefined;
}

export function splitListOptions<TOptions extends { signal?: AbortSignal }>(options: TOptions = {} as TOptions) {
  const { signal, ...queryOptions } = options;
  const query = Object.fromEntries(Object.entries(queryOptions).filter(([, value]) => value !== undefined && value !== null));
  return {
    query: Object.keys(query).length > 0 ? query : undefined,
    signal,
  };
}

export class OpenApiClientTransport implements ApiClient {
  private readonly baseUrl: string;
  private readonly client: Client<paths>;
  private csrfToken: string | null = null;
  private csrfRequest: Promise<string> | null = null;

  constructor(options: CanonKeeperApiClientOptions = {}) {
    this.baseUrl = options.baseUrl ?? defaultBaseUrl();
    const send = options.fetch ?? ((request: Request) => globalThis.fetch(request));
    this.client = createClient<paths>({
      baseUrl: this.baseUrl,
      credentials: 'include',
      // The Date of every answer keeps the clock of the server this tab counts by (specs/2026-11-11-ux9-shelf-chat.md SC3.8).
      fetch: async (request) => {
        const sentAt = Date.now();
        const response = await send(request);
        noteServerDate(response.headers.get('Date'), sentAt, Date.now());
        return response;
      },
    });
  }

  async request<TOperation extends OperationId, TStatus extends keyof OperationResponses<TOperation>>(
    operationId: TOperation,
    init: OperationInit<TOperation> = {},
  ): Promise<OperationResponses<TOperation>[TStatus]> {
    const route = operationManifest[operationId];
    const explicitHeaders = init.headers ?? {};
    return this.withCsrfRecovery(
      needsCsrf(route.method, explicitHeaders),
      () => this.requestOnce<TOperation, TStatus>(operationId, init),
    );
  }

  private async requestOnce<TOperation extends OperationId, TStatus extends keyof OperationResponses<TOperation>>(
    operationId: TOperation,
    init: OperationInit<TOperation>,
  ): Promise<OperationResponses<TOperation>[TStatus]> {
    const route = operationManifest[operationId];
    const requestInit: { params?: unknown; body?: unknown; signal?: AbortSignal } = {};
    const explicitHeaders = init.headers ?? {};
    const csrfToken = needsCsrf(route.method, explicitHeaders) ? await this.ensureCsrfToken() : undefined;
    const params = withContractHeaders(route.method, init.params, explicitHeaders, csrfToken);
    if (params) {
      requestInit.params = params;
    }
    if (init.body !== undefined) {
      requestInit.body = init.body;
    }
    if (init.signal) {
      requestInit.signal = init.signal;
    }

    try {
      const openApiRequest = this.client.request as unknown as OpenApiFetchRequest;
      const result = await openApiRequest(
        route.method.toLowerCase() as Lowercase<typeof route.method>,
        route.path,
        requestInit,
      );
      if (result.error !== undefined) {
        throw new ApiStatusError(result.response.status, problemFromError(result.error, result.response.status), result.response);
      }
      if (['registerUser', 'loginUser', 'logoutUser', 'rotateSession'].includes(operationId)) {
        this.csrfToken = null;
      }
      return result.data as OperationResponses<TOperation>[TStatus];
    } catch (error) {
      if (error instanceof ApiStatusError) {
        throw error;
      }
      throw new NetworkApiError(error);
    } finally {
      if (operationId === 'startImpersonation' || operationId === 'stopImpersonation') {
        this.csrfToken = null;
      }
    }
  }

  /**
   * An operation that answers with a file: the bytes and the name the server gave it. A write goes with its body and the
   * same request-forgery protection as request (the batch of keys of the tiers).
   */
  protected async requestFile<TOperation extends OperationId>(operationId: TOperation, params: unknown, fallbackName: string, body?: unknown): Promise<DownloadedFile> {
    const route = operationManifest[operationId];
    return this.withCsrfRecovery(needsCsrf(route.method, {}), () => this.requestFileOnce(operationId, params, fallbackName, body));
  }

  private async requestFileOnce<TOperation extends OperationId>(operationId: TOperation, params: unknown, fallbackName: string, body?: unknown): Promise<DownloadedFile> {
    const route = operationManifest[operationId];
    const csrfToken = needsCsrf(route.method, {}) ? await this.ensureCsrfToken() : undefined;
    const openApiRequest = this.client.request as unknown as OpenApiFetchRequest;
    let result: OpenApiFetchResult;
    try {
      result = await openApiRequest(route.method.toLowerCase() as Lowercase<typeof route.method>, route.path, {
        params: withContractHeaders(route.method, params, {}, csrfToken), parseAs: 'blob', ...(body === undefined ? {} : { body }),
      });
    } catch (error) {
      throw new NetworkApiError(error);
    }
    if (result.error !== undefined) {
      throw new ApiStatusError(result.response.status, problemFromError(result.error, result.response.status), result.response);
    }
    return { blob: result.data as Blob, fileName: attachmentName(result.response.headers.get('Content-Disposition')) ?? fallbackName };
  }

  protected async ensureCsrfToken() {
    if (this.csrfToken) {
      return this.csrfToken;
    }
    // Each answer voids the one before it, so concurrent writes share one request: a second request
    // would leave the first write with a value the server no longer takes.
    this.csrfRequest ??= this.request<'getCsrfToken', '200'>('getCsrfToken')
      .then((response) => {
        this.csrfToken = response.csrfToken;
        return response.csrfToken;
      })
      .finally(() => {
        this.csrfRequest = null;
      });
    return this.csrfRequest;
  }

  private async withCsrfRecovery<TResult>(
    csrfRequired: boolean,
    send: () => Promise<TResult>,
  ) {
    try {
      return await send();
    } catch (error) {
      if (!csrfRequired || !(error instanceof ApiStatusError) || error.status !== 403 || error.problem.code !== 'csrf.invalid') {
        throw error;
      }
      this.csrfToken = null;
      return send();
    }
  }

  protected async postMultipart<TOperation extends OperationId, TResult>(
    operationId: TOperation,
    pathParams: Record<string, string | number | boolean>,
    form: FormData,
    options: MultipartRequestOptions = {},
  ): Promise<TResult> {
    const route = operationManifest[operationId];
    const explicitHeaders: Record<string, string> = options.idempotencyKey ? { [idempotencyHeaderName]: options.idempotencyKey } : {};
    return this.withCsrfRecovery(
      needsCsrf(route.method, explicitHeaders),
      () => this.postMultipartOnce<TOperation, TResult>(operationId, pathParams, form, options),
    );
  }

  private async postMultipartOnce<TOperation extends OperationId, TResult>(
    operationId: TOperation,
    pathParams: Record<string, string | number | boolean>,
    form: FormData,
    options: MultipartRequestOptions,
  ): Promise<TResult> {
    const route = operationManifest[operationId];
    const url = joinUrl(this.baseUrl, applyPathParams(route.path, pathParams));
    const explicitHeaders: Record<string, string> = options.idempotencyKey ? { [idempotencyHeaderName]: options.idempotencyKey } : {};
    const csrfToken = needsCsrf(route.method, explicitHeaders) ? await this.ensureCsrfToken() : undefined;
    const params = withContractHeaders(route.method, { path: pathParams }, explicitHeaders, csrfToken);
    const headers = (params as { header?: Record<string, string> } | undefined)?.header ?? {};

    return new Promise<TResult>((resolve, reject) => {
      const request = new XMLHttpRequest();
      const abortRequest = () => request.abort();
      const cleanup = () => options.signal?.removeEventListener('abort', abortRequest);

      request.open(route.method, url);
      request.withCredentials = true;
      for (const [name, value] of Object.entries(headers)) {
        request.setRequestHeader(name, value);
      }
      request.upload.onprogress = (event) => {
        options.onUploadProgress?.({
          loadedBytes: event.loaded,
          totalBytes: event.lengthComputable && event.total > 0 ? event.total : null,
        });
      };
      request.onload = () => {
        cleanup();
        const response = responseFromXhr(request);
        if (!response.ok) {
          void this.readProblem(response).then(
            (problem) => reject(new ApiStatusError(response.status, problem, response)),
            reject,
          );
          return;
        }
        void response.json().then(
          (body) => resolve(body as TResult),
          (error) => reject(error instanceof NetworkApiError ? error : new NetworkApiError(error)),
        );
      };
      request.onerror = () => {
        cleanup();
        reject(new NetworkApiError(new TypeError('XMLHttpRequest network failure.')));
      };
      request.onabort = () => {
        cleanup();
        reject(new NetworkApiError(abortCause(options.signal)));
      };

      if (options.signal?.aborted) {
        reject(new NetworkApiError(abortCause(options.signal)));
        return;
      }
      options.signal?.addEventListener('abort', abortRequest, { once: true });
      if (options.signal?.aborted) {
        request.abort();
        return;
      }
      request.send(form);
    });
  }

  private async readProblem(response: Response): Promise<ApiProblem> {
    const fallback = {
      type: 'about:blank',
      title: response.statusText || 'Request failed',
      status: response.status,
      code: 'request_failed',
      requestId: 'req_unavailable',
    };
    if (!response.headers.get('content-type')?.toLowerCase().includes('json')) {
      return fallback;
    }
    try {
      return problemFromError(await response.clone().json(), response.status);
    } catch {
      return fallback;
    }
  }
}
