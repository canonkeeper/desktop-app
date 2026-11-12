import type {
  Book,
  Chapter,
  Job,
  OperationId,
  Project,
  RequiredIdempotencyOptions,
  SourceView,
} from '../../../shared/api';
import {
  chapterInView,
  clone,
  findById,
  idempotencyFingerprintValue,
  isTerminalJobStatus,
  jobUrl,
  stableStringify,
  validateIdempotencyKey,
  validateSourceView,
  type DirectIdempotencyRecord,
  type JobCreatorRecord,
  type ProjectInvitation,
  type ProjectMembership,
} from './common';
import { createProjectMembershipFixture } from './fixtures';
import { createInitialState, type MockState } from './state';
import {
  applyScenario,
  isProtectedOperation,
  MockApiError,
  normalScenario,
  type MockActorRole,
  type MockScenarioControl,
} from '../scenarios';

type ScenarioOperationId = Parameters<typeof applyScenario>[0];

export type MockRepositoryContext = {
  state: MockState;
  scenario: MockScenarioControl;
  directIdempotency: Map<string, DirectIdempotencyRecord>;
  jobCreators: Map<string, JobCreatorRecord>;
  setScenario(scenario: MockScenarioControl): void;
  getScenario(): MockScenarioControl;
  hasActiveSession(): boolean;
  applyOperation(operationId: OperationId): void;
  reset(): void;
  projectMembership(
    project: Project,
    memberId: string,
    role: ProjectMembership['role'],
    email?: string,
    displayName?: string,
  ): ProjectMembership;
  rawActorRole(): MockActorRole;
  acceptedInvitationMembership(projectIdForMembership?: string): ProjectMembership | undefined;
  createdProjectMembership(projectIdForMembership?: string): ProjectMembership | undefined;
  nonMemberAccessibleMembership(projectIdForMembership?: string): ProjectMembership | undefined;
  actorRole(projectIdForMembership?: string): MockActorRole;
  bookHasSourceView(id: string, sourceView: SourceView): boolean;
  projectHasSourceView(id: string, sourceView: SourceView): boolean;
  requireReadableSourceView(operationId: ScenarioOperationId, resourceProjectId: string, sourceView: unknown): SourceView;
  jobIsVisible(job: Job): boolean;
  requireProjectAccess(operationId: ScenarioOperationId, resourceProjectId: string, allowed?: MockActorRole[]): void;
  requireDeletedSourceRole(operationId: ScenarioOperationId, resourceProjectId: string, allowed: MockActorRole[]): void;
  visibleJob(job: Job): Job;
  visibleProject(project: Project): Project;
  projectFor(id: string, operationId: ScenarioOperationId, allowed?: MockActorRole[]): Project;
  bookFor(id: string, operationId: ScenarioOperationId, allowed?: MockActorRole[]): Book;
  chapterFor(id: string, operationId: ScenarioOperationId, allowed?: MockActorRole[]): Chapter;
  locatorHasLiveParent(locator: { bookId: string; chapterId: string }): boolean;
  invitationFor(id: string, operationId: ScenarioOperationId): ProjectInvitation;
  jobFor(id: string, operationId: ScenarioOperationId, allowed?: MockActorRole[]): Job;
  currentPrincipalId(): string;
  markJobCreator(job: Job): void;
  requireJobCancelAccess(job: Job): void;
  idempotentCommand<TResponse>(
    operationId: OperationId,
    resourceId: string,
    options: RequiredIdempotencyOptions | undefined,
    fingerprintInput: unknown,
    execute: () => TResponse | Promise<TResponse>,
  ): Promise<TResponse>;
};

const modelWorkOperations = new Set<OperationId>(['importBookFile', 'startImportQueue', 'createChatTurn', 'steerChatTurn', 'startAuthorMaterialsImport', 'startProjectIndexing']);

export function createMockRepositoryContext(): MockRepositoryContext {
  const context: MockRepositoryContext = {
    state: createInitialState(),
    scenario: normalScenario,
    directIdempotency: new Map<string, DirectIdempotencyRecord>(),
    jobCreators: new Map<string, JobCreatorRecord>(),
    setScenario,
    getScenario,
    hasActiveSession,
    applyOperation,
    reset,
    projectMembership,
    rawActorRole,
    acceptedInvitationMembership,
    createdProjectMembership,
    nonMemberAccessibleMembership,
    actorRole,
    bookHasSourceView,
    projectHasSourceView,
    requireReadableSourceView,
    jobIsVisible,
    requireProjectAccess,
    requireDeletedSourceRole,
    visibleJob,
    visibleProject,
    projectFor,
    bookFor,
    chapterFor,
    locatorHasLiveParent,
    invitationFor,
    jobFor,
    currentPrincipalId,
    markJobCreator,
    requireJobCancelAccess,
    idempotentCommand,
  };

  function setScenario(scenario: MockScenarioControl) {
    context.scenario = scenario;
  }

  function getScenario() {
    return context.scenario;
  }

  function hasActiveSession() {
    return context.state.session !== null;
  }

  function applyOperation(operationId: OperationId) {
    if (operationId !== 'logoutUser' && isProtectedOperation(operationId) && !context.state.session) {
      throw new MockApiError(401, 'Сессия истекла. Войдите снова.', operationId);
    }
    // Model work needs a verified address (UC1.4).
    if (modelWorkOperations.has(operationId) && context.state.session && !context.state.session.user.emailVerified) {
      throw new MockApiError(403, 'email address is not verified', operationId, 'auth.email_unverified');
    }
    applyScenario(operationId, context.scenario);
  }

  function reset() {
    context.state = createInitialState();
    context.scenario = normalScenario;
    context.directIdempotency.clear();
    context.jobCreators.clear();
  }

  function projectMembership(
    project: Project,
    memberId: string,
    role: ProjectMembership['role'],
    email?: string,
    displayName?: string,
  ): ProjectMembership {
    return createProjectMembershipFixture(project, context.state.user, memberId, role, email, displayName);
  }

  function rawActorRole(): MockActorRole {
    return context.scenario.actorRole ?? 'owner';
  }

  function acceptedInvitationMembership(projectIdForMembership?: string): ProjectMembership | undefined {
    const principalId = context.currentPrincipalId();
    return context.state.memberships.find((membership) => (
      membership.id.startsWith('member-invitation-') &&
      membership.status === 'active' &&
      membership.userId === principalId &&
      (projectIdForMembership === undefined || membership.projectId === projectIdForMembership)
    ));
  }

  function createdProjectMembership(projectIdForMembership?: string): ProjectMembership | undefined {
    const principalId = context.currentPrincipalId();
    return context.state.memberships.find((membership) => (
      membership.id.startsWith('member-created-') &&
      membership.status === 'active' &&
      membership.userId === principalId &&
      (projectIdForMembership === undefined || membership.projectId === projectIdForMembership)
    ));
  }

  function nonMemberAccessibleMembership(projectIdForMembership?: string): ProjectMembership | undefined {
    return context.acceptedInvitationMembership(projectIdForMembership) ?? context.createdProjectMembership(projectIdForMembership);
  }

  function actorRole(projectIdForMembership?: string): MockActorRole {
    const role = context.rawActorRole();
    if (role !== 'non_member') {
      return role;
    }
    // Принятое приглашение открывает только тот проект, к которому относится членство.
    return context.nonMemberAccessibleMembership(projectIdForMembership)?.role ?? 'non_member';
  }

  function bookHasSourceView(id: string, sourceView: SourceView) {
    return context.state.chapters.some((chapter) => chapter.bookId === id && chapterInView(chapter, sourceView));
  }

  function projectHasSourceView(id: string, sourceView: SourceView) {
    return context.state.books.some((book) => book.projectId === id && context.bookHasSourceView(book.id, sourceView));
  }

  function requireReadableSourceView(
    operationId: Parameters<typeof applyScenario>[0],
    resourceProjectId: string,
    sourceView: unknown,
  ): SourceView {
    context.requireProjectAccess(operationId, resourceProjectId);
    const readableView = validateSourceView(operationId, sourceView);
    // Недоступный вид текста маскируется как отсутствующий ресурс, чтобы не раскрывать черновик.
    if (
      (readableView === 'draft' && context.actorRole(resourceProjectId) === 'viewer') ||
      !context.projectHasSourceView(resourceProjectId, readableView)
    ) {
      throw new MockApiError(404, 'Resource not found.', operationId);
    }
    return readableView;
  }

  function jobIsVisible(job: Job) {
    if (!context.state.projects.some((project) => project.id === job.subject.projectId)) {
      return false;
    }
    if (!('sourceView' in job.subject)) {
      return true;
    }
    if (job.subject.sourceView === 'draft' && context.actorRole(job.subject.projectId) === 'viewer') {
      return false;
    }
    if (!context.projectHasSourceView(job.subject.projectId, job.subject.sourceView)) {
      return false;
    }
    return !('bookId' in job.subject) || context.bookHasSourceView(job.subject.bookId, job.subject.sourceView);
  }

  function requireProjectAccess(operationId: Parameters<typeof applyScenario>[0], resourceProjectId: string, allowed?: MockActorRole[]) {
    const knownProject = context.state.projects.some((project) => project.id === resourceProjectId);
    const role = context.actorRole(resourceProjectId);
    if (role === 'non_member' || !knownProject) {
      throw new MockApiError(404, 'Resource not found.', operationId);
    }
    if (allowed && !allowed.includes(role)) {
      throw new MockApiError(403, 'Нет доступа к этому проекту.', operationId);
    }
  }

  function requireDeletedSourceRole(
    operationId: Parameters<typeof applyScenario>[0],
    resourceProjectId: string,
    allowed: MockActorRole[],
  ) {
    const role = context.actorRole(resourceProjectId);
    if (role === 'non_member') {
      throw new MockApiError(404, 'Resource not found.', operationId);
    }
    if (!allowed.includes(role)) {
      throw new MockApiError(403, 'Нет доступа к этому проекту.', operationId);
    }
  }

  function visibleJob(job: Job): Job {
    const visible = clone(job);
    const role = context.actorRole(job.subject.projectId);
    const creator = context.jobCreators.get(job.id);
    const terminal = isTerminalJobStatus(visible.status);
    const canManage = role === 'admin' || role === 'owner' || creator?.principalId === context.currentPrincipalId();
    visible.canCancel = visible.canCancel && !terminal && canManage;
    visible.links = {
      ...visible.links,
      cancel: visible.canCancel ? `${jobUrl(visible.id)}/cancel` : null,
    };
    if (!terminal || visible.status === 'canceled') {
      visible.result = null;
    }
    // Only the creator of a book update preview reads its places (specs/2026-10-27-update-book-from-file.md, R6.3).
    if (visible.result?.type === 'import' && visible.result.preview && creator?.principalId !== context.currentPrincipalId()) {
      visible.result.preview.chapters = visible.result.preview.chapters.map(({ places: _places, ...chapter }) => chapter);
    }
    return visible;
  }

  function visibleProject(project: Project): Project {
    const role = context.actorRole(project.id);
    if (role === 'non_member') {
      return clone(project);
    }
    return {
      ...clone(project),
      currentMembership: context.projectMembership(project, role === 'owner' ? 'member-owner' : 'member-current', role),
    };
  }

  function projectFor(id: string, operationId: Parameters<typeof applyScenario>[0], allowed?: MockActorRole[]) {
    const project = findById(context.state.projects, id, operationId);
    context.requireProjectAccess(operationId, project.id, allowed);
    return project;
  }

  function bookFor(id: string, operationId: Parameters<typeof applyScenario>[0], allowed?: MockActorRole[]) {
    const book = findById(context.state.books, id, operationId);
    context.requireProjectAccess(operationId, book.projectId, allowed);
    return book;
  }

  function chapterFor(id: string, operationId: Parameters<typeof applyScenario>[0], allowed?: MockActorRole[]) {
    const chapter = findById(context.state.chapters, id, operationId);
    context.bookFor(chapter.bookId, operationId, allowed);
    return chapter;
  }

  function locatorHasLiveParent(locator: { bookId: string; chapterId: string }) {
    return context.state.books.some((book) => book.id === locator.bookId)
      && context.state.chapters.some((chapter) => chapter.id === locator.chapterId && chapter.bookId === locator.bookId);
  }

  function invitationFor(id: string, operationId: Parameters<typeof applyScenario>[0]) {
    return findById(context.state.invitations, id, operationId);
  }

  function jobFor(id: string, operationId: Parameters<typeof applyScenario>[0], allowed?: MockActorRole[]) {
    const job = findById(context.state.jobs, id, operationId);
    if (!context.jobIsVisible(job)) {
      throw new MockApiError(404, 'Resource not found.', operationId);
    }
    context.requireProjectAccess(operationId, job.subject.projectId, allowed);
    return job;
  }

  function currentPrincipalId() {
    return context.state.session?.user.id ?? context.state.user.id;
  }

  function markJobCreator(job: Job) {
    context.jobCreators.set(job.id, { principalId: context.currentPrincipalId() });
  }

  function requireJobCancelAccess(job: Job) {
    const role = context.actorRole(job.subject.projectId);
    const creator = context.jobCreators.get(job.id);
    if (
      role === 'admin' ||
      role === 'owner' ||
      creator?.principalId === context.currentPrincipalId()
    ) {
      return;
    }
    throw new MockApiError(403, 'Нет доступа к этому проекту.', 'cancelJob');
  }

  async function idempotentCommand<TResponse>(
    operationId: OperationId,
    resourceId: string,
    options: RequiredIdempotencyOptions | undefined,
    fingerprintInput: unknown,
    execute: () => TResponse | Promise<TResponse>,
  ): Promise<TResponse> {
    if (!options?.idempotencyKey) {
      throw new MockApiError(400, 'Idempotency-Key is required.', operationId, 'idempotency_key_required');
    }
    validateIdempotencyKey(operationId, options.idempotencyKey);
    const principalId = context.currentPrincipalId();
    // Один ключ можно повторять только для той же операции, ресурса и пользователя.
    const storeKey = `${principalId}:${operationId}:${resourceId}:${options.idempotencyKey}`;
    const fingerprint = stableStringify(await idempotencyFingerprintValue({ operationId, resourceId, body: fingerprintInput }));
    const existing = context.directIdempotency.get(storeKey);
    if (existing) {
      if (existing.fingerprint !== fingerprint) {
        throw new MockApiError(409, 'Idempotency-Key payload mismatch.', operationId, 'idempotency_key_mismatch');
      }
      const response = existing.response;
      const jobId = response && typeof response === 'object'
        ? (response as { jobId?: unknown }).jobId
        : undefined;
      // Запись ключа переживает удаление источника: исчезнувший Job нельзя восстановить или создать заново.
      if (typeof jobId === 'string') {
        const job = context.state.jobs.find((candidate) => candidate.id === jobId);
        if (!job) {
          throw new MockApiError(409, 'Idempotent Job is no longer available.', operationId, 'conflict');
        }
        return {
          ...(clone(response) as object),
          job: context.visibleJob(job),
        } as TResponse;
      }
      return clone(existing.response) as TResponse;
    }
    const response = await execute();
    context.directIdempotency.set(storeKey, { fingerprint, response: clone(response) });
    return response;
  }


  return context;
}
