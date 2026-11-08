import type { OperationId, OperationRequest, OperationResponse, Schema } from '../../../shared/api';
import { MockApiError } from '../scenarios';
import { clone, now } from './common';
import { mockNewAuthorWriterId, mockPlatformAdminWriterId } from './admin-writers';
import type { MockRepositoryContext } from './context';
import { createIllustrationAccess } from './illustration-access';
import { mockCodeDefaults, mockDefault } from './quota';

type AdminWriter = Schema<'AdminWriter'>;
type AdminQuotaKey = Schema<'AdminQuotaKey'>;
type AdminQuotaDefaultKey = Schema<'AdminQuotaDefaultKey'>;
type AdminWriterStage = Schema<'AdminWriterStage'>;

const stages: AdminWriterStage[] = ['unverified', 'no_documents', 'no_book', 'no_question', 'asking', 'quiet', 'blocked'];

/** The limits by default in the order of the tab (backend domain/quota.DefaultKeys); the writer card has all but the trial. */
const defaultKeys = Object.keys(mockCodeDefaults) as AdminQuotaDefaultKey[];
const quotaKeys = defaultKeys.filter((key): key is AdminQuotaKey => key !== 'trial_drawings');
/** The largest value a limit keeps (backend domain/quota.Key.Max): bigint for the characters, integer otherwise. */
const maxInteger = 2_147_483_647;

function validLimit(key: AdminQuotaDefaultKey, value: number | 'default') {
  if (value === 'default') return true;
  const max = key === 'project_characters' || key === 'account_characters' ? Number.MAX_SAFE_INTEGER : maxInteger;
  return Number.isInteger(value) && value >= 0 && value <= max;
}

function writerFixture(id: string, email: string, fields: Partial<AdminWriter>): AdminWriter {
  return {
    id, email, registeredAt: now, emailVerified: true, documentsAccepted: true, blocked: false, quotaChanged: false,
    projects: 0, books: 0, chapters: 0, imports: 0, questions: 0, questionsWeek: 0, unanswered: 0,
    lastQuestionDay: null, lastLoginAt: null, stage: 'no_book', platformAdmin: false, ...fields,
  };
}

/** The stage rule of services/owner.ListWriters. */
function stageOf(writer: AdminWriter): AdminWriterStage {
  if (writer.blocked) return 'blocked';
  if (!writer.emailVerified) return 'unverified';
  if (!writer.documentsAccepted) return 'no_documents';
  if (writer.questionsWeek > 0) return 'asking';
  if (writer.questions > 0) return 'quiet';
  if (writer.books === 0 && writer.imports === 0) return 'no_book';
  return 'no_question';
}

/** The names of the synthetic writers: the list of writers has no names, the requests do. */
const writerNames: Record<string, string> = {
  [mockNewAuthorWriterId]: 'Новый Автор',
  [mockPlatformAdminWriterId]: 'Мира Волкова',
};

/** The admin pages of the platform administrator (specs/2026-10-29-admin-panel.md) over synthetic writers. */
export function createAdminOperations(context: MockRepositoryContext) {
  const writers: AdminWriter[] = [
    writerFixture(mockNewAuthorWriterId, 'new-author@example.test', { emailVerified: false }),
    writerFixture(mockPlatformAdminWriterId, 'mira@example.com', {
      platformAdmin: true, projects: 1, books: 4, chapters: 186, imports: 4, questions: 12, questionsWeek: 3,
      lastQuestionDay: now.slice(0, 10), lastLoginAt: now,
    }),
  ];
  const overrides = new Map<string, Partial<Record<AdminQuotaKey, number>>>();
  const access = createIllustrationAccess(context);

  /** A writer's limit in force: his own, else the platform's, else the code's (admin-panel R7.2). */
  function limitOf(userId: string, key: AdminQuotaKey) {
    return overrides.get(userId)?.[key] ?? mockDefault(context.state.quotaDefaults, key);
  }

  /** The block «Иллюстрации» of a writer card with the writer's images_per_day in force. */
  function illustrationsOf(userId: string) {
    return access.viewOf(userId, limitOf(userId, 'images_per_day'), false);
  }

  function adminOperation(operationId: OperationId) {
    context.applyOperation(operationId);
    if (!context.state.session || context.state.session.user.impersonation) {
      throw new MockApiError(403, 'Forbidden', operationId, 'admin.forbidden');
    }
  }

  function staged(writer: AdminWriter): AdminWriter {
    return { ...writer, quotaChanged: Object.keys(overrides.get(writer.id) ?? {}).length > 0, stage: stageOf(writer) };
  }

  function findWriter(userId: string, operationId: OperationId) {
    const writer = writers.find((candidate) => candidate.id === userId);
    if (!writer) throw new MockApiError(404, 'Resource not found', operationId, 'not_found');
    return writer;
  }

  async function listAdminWriters(): Promise<OperationResponse<'listAdminWriters', '200'>> {
    adminOperation('listAdminWriters');
    const listed = writers.map(staged);
    return clone({
      writers: listed,
      hidden: { service: 0, deleted: 0 },
      stages: stages.map((stage) => ({ stage, writers: listed.filter((writer) => writer.stage === stage).length })),
    });
  }

  async function getAdminWriter(userId: string): Promise<OperationResponse<'getAdminWriter', '200'>> {
    adminOperation('getAdminWriter');
    const writer = staged(findWriter(userId, 'getAdminWriter'));
    const own = overrides.get(userId) ?? {};
    const used: Partial<Record<AdminQuotaKey, number>> = {
      chat_turns_per_day: 0, active_turns: 0, imports_per_day: 0, projects: writer.projects,
      account_books: writer.books, account_chapters: writer.chapters, account_characters: 0, images_per_day: 0, image_uploads_per_day: 0, chapter_drafts_per_day: 0,
      drawing_turns_per_day: 0, draft_turns_per_day: 0, draft_rewrites_per_day: 0,
    };
    return clone({
      writer,
      quota: {
        day: now.slice(0, 10),
        resetsAt: new Date(Date.parse(`${now.slice(0, 10)}T00:00:00+03:00`) + 24 * 60 * 60 * 1000).toISOString(),
        limits: quotaKeys.map((key) => ({
          key, limit: limitOf(userId, key), overridden: own[key] !== undefined, used: used[key] ?? null,
        })),
        ownedProjects: [],
      },
      impersonations: [],
      illustrations: illustrationsOf(userId),
    });
  }

  async function setAdminWriterQuota(
    userId: string,
    body: OperationRequest<'setAdminWriterQuota'>,
  ): Promise<OperationResponse<'setAdminWriterQuota', '200'>> {
    adminOperation('setAdminWriterQuota');
    findWriter(userId, 'setAdminWriterQuota');
    const entries = Object.entries(body ?? {}) as [string, number | 'default'][];
    if (entries.length === 0 || entries.some(([key, value]) => !quotaKeys.includes(key as AdminQuotaKey) || !validLimit(key as AdminQuotaKey, value))) {
      throw new MockApiError(400, 'validation failed', 'setAdminWriterQuota', 'request.validation');
    }
    const own = { ...(overrides.get(userId) ?? {}) };
    const changes = entries.map(([name, value]) => {
      const key = name as AdminQuotaKey;
      const before = own[key] ?? null;
      if (value === 'default') delete own[key];
      else own[key] = value;
      return { key, before, after: value === 'default' ? null : value };
    });
    overrides.set(userId, own);
    return { changes };
  }

  async function blockAdminWriter(userId: string): Promise<OperationResponse<'blockAdminWriter', '200'>> {
    adminOperation('blockAdminWriter');
    const writer = findWriter(userId, 'blockAdminWriter');
    if (writer.platformAdmin) throw new MockApiError(409, 'Conflict', 'blockAdminWriter', 'admin.protected');
    const changed = !writer.blocked;
    writer.blocked = true;
    return { changed, sessionsRevoked: 0, tokensDeleted: 0 };
  }

  async function unblockAdminWriter(userId: string): Promise<OperationResponse<'unblockAdminWriter', '200'>> {
    adminOperation('unblockAdminWriter');
    const writer = findWriter(userId, 'unblockAdminWriter');
    const changed = writer.blocked;
    writer.blocked = false;
    return { changed };
  }

  async function listAdminDays(options: { days?: number } = {}): Promise<OperationResponse<'listAdminDays', '200'>> {
    adminOperation('listAdminDays');
    const days = options.days ?? 14;
    if (!Number.isInteger(days) || days < 1 || days > 366) {
      throw new MockApiError(400, 'validation failed', 'listAdminDays', 'request.validation');
    }
    const counts = {
      registered: 0, verified: 0, firstImport: 0, firstQuestion: 0, active: 0, questions: 0, answered: 0, failed: 0,
      interrupted: 0, imports: 0, importsSucceeded: 0, importsFailed: 0, importsInterrupted: 0, modelCalls: 0,
      inputTokens: 0, cachedInputTokens: 0, outputTokens: 0,
    };
    const last = Date.parse(`${now.slice(0, 10)}T00:00:00Z`);
    const list = Array.from({ length: days }, (_, index) => ({
      day: new Date(last - (days - 1 - index) * 24 * 60 * 60 * 1000).toISOString().slice(0, 10),
      counts: { ...counts },
    }));
    return { first: list[0]!.day, last: list[list.length - 1]!.day, days: list, total: { ...counts } };
  }

  async function getAdminServiceStatus(): Promise<OperationResponse<'getAdminServiceStatus', '200'>> {
    adminOperation('getAdminServiceStatus');
    return {
      checkedAt: now,
      failedSince: new Date(Date.parse(now) - 24 * 60 * 60 * 1000).toISOString(),
      activeJobs: [],
      failedJobs: [],
      chapterCards: { version: 3, projects: [] },
      codex: { version: null, replacement: null },
    };
  }

  /** «Включить», «Выключить», «Вернуть пробные» (specs/2026-10-29-saga-illustrations.md R11.6). */
  async function setAdminWriterIllustrations(
    userId: string,
    body: OperationRequest<'setAdminWriterIllustrations'>,
  ): Promise<OperationResponse<'setAdminWriterIllustrations', '200'>> {
    adminOperation('setAdminWriterIllustrations');
    findWriter(userId, 'setAdminWriterIllustrations');
    const images = body.imagesPerDay;
    if (images !== undefined && images !== 'default' && (!Number.isInteger(images) || images < 0)) {
      throw new MockApiError(400, 'validation failed', 'setAdminWriterIllustrations', 'request.validation');
    }
    access.setRight('setAdminWriterIllustrations', userId, body);
    if (images !== undefined) {
      const own = { ...(overrides.get(userId) ?? {}) };
      if (images === 'default') delete own.images_per_day;
      else own.images_per_day = images;
      overrides.set(userId, own);
    }
    return clone(illustrationsOf(userId));
  }

  /** The tab «Запросы»: pending first, newest first, with the counter. */
  async function listAdminIllustrationRequests(): Promise<OperationResponse<'listAdminIllustrationRequests', '200'>> {
    adminOperation('listAdminIllustrationRequests');
    const requests = access.requests().map((request) => {
      const writer = writers.find((candidate) => candidate.id === request.userId);
      return {
        ...access.publicRequest(request), userId: request.userId,
        email: writer?.email ?? context.state.session?.user.email ?? '',
        accessState: access.stateOf(access.rightOf(request.userId)),
        // Who the writer is, as his card tells it (specs/2026-11-07-contacts.md R1.3).
        writer: {
          name: writer ? writerNames[writer.id] ?? writer.email : context.state.session?.user.displayName ?? '',
          registeredAt: writer?.registeredAt ?? now, projects: writer?.projects ?? 0, books: writer?.books ?? 0,
          lastLoginAt: writer?.lastLoginAt ?? null, lastQuestionDay: writer?.lastQuestionDay ?? null,
        },
      };
    });
    return clone({ requests, pending: requests.filter((request) => request.status === 'pending').length });
  }

  async function declineAdminIllustrationRequest(
    requestId: string,
    body: OperationRequest<'declineAdminIllustrationRequest'>,
  ): Promise<OperationResponse<'declineAdminIllustrationRequest', '200'>> {
    adminOperation('declineAdminIllustrationRequest');
    if (body?.message && [...body.message].length > 1000) {
      throw new MockApiError(400, 'validation failed', 'declineAdminIllustrationRequest', 'request.validation');
    }
    return access.publicRequest(access.declineRequest('declineAdminIllustrationRequest', requestId));
  }

  /** The tab «Лимиты» (specs/2026-10-29-admin-panel.md R7.4). */
  async function getAdminQuotaDefaults(): Promise<OperationResponse<'getAdminQuotaDefaults', '200'>> {
    adminOperation('getAdminQuotaDefaults');
    const platform = context.state.quotaDefaults;
    return clone({
      defaults: defaultKeys.map((key) => ({
        key, code: mockCodeDefaults[key], platform: platform[key] ?? null, effective: mockDefault(platform, key),
      })),
      updatedAt: context.state.quotaDefaultsUpdatedAt,
    });
  }

  /** «Изменить…» and «Вернуть исходное» (R7.5): only a key whose value changes is written and returned. */
  async function setAdminQuotaDefaults(
    body: OperationRequest<'setAdminQuotaDefaults'>,
  ): Promise<OperationResponse<'setAdminQuotaDefaults', '200'>> {
    adminOperation('setAdminQuotaDefaults');
    const entries = Object.entries(body ?? {}) as [string, number | 'default'][];
    if (entries.length === 0 || entries.some(([key, value]) => !defaultKeys.includes(key as AdminQuotaDefaultKey) || !validLimit(key as AdminQuotaDefaultKey, value))) {
      throw new MockApiError(400, 'validation failed', 'setAdminQuotaDefaults', 'request.validation');
    }
    const platform = context.state.quotaDefaults;
    const changes = defaultKeys.flatMap((key) => {
      const entry = entries.find(([name]) => name === key);
      if (!entry) return [];
      const before = platform[key] ?? null;
      const after = entry[1] === 'default' ? null : entry[1];
      if (before === after) return [];
      if (after === null) delete platform[key];
      else platform[key] = after;
      return [{ key, before, after }];
    });
    if (changes.length > 0) context.state.quotaDefaultsUpdatedAt = new Date().toISOString();
    return { changes };
  }

  return {
    listAdminWriters, getAdminWriter, setAdminWriterQuota, blockAdminWriter, unblockAdminWriter, listAdminDays, getAdminServiceStatus,
    setAdminWriterIllustrations, listAdminIllustrationRequests, declineAdminIllustrationRequest, getAdminQuotaDefaults, setAdminQuotaDefaults,
  };
}
