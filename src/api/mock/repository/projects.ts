import type {
  ActiveSourceListOptions,
  DeletedSourceListOptions,
  OperationRequest,
  OperationResponse,
  Project,
  Schema,
} from '../../../shared/api';
import {
  clone,
  deletedSource,
  deletedSourceRow,
  normalizedEmail,
  now,
  paginateList,
  rejectUnknownKeys,
  requestedSourceListState,
  type CursorListOptions,
  type SourceListOptions,
} from './common';
import type { MockRepositoryContext } from './context';
import {
  createProjectInvitationFixture,
  createProjectMembershipForUser,
  draftBookId,
} from './fixtures';
import { scrubAgentWorkflows } from './workflow-scrub';
import { MockApiError } from '../scenarios';

const assignableProjectMemberRoles = ['admin', 'editor', 'viewer'] as const;

// The demo saga of the site as the backend makes it (specs/2026-11-09-demo-saga.md DS2.2).
const demoSagaTitle = 'Пример: «Игрок» Достоевского';
const demoSagaDescription = 'Роман Ф. М. Достоевского «Игрок» — сага-пример: попробуйте сервис без своей рукописи. Текст: Викитека, CC BY-SA 4.0.';

function validateUpdateProjectRequest(body: OperationRequest<'updateProject'>) {
  rejectUnknownKeys('updateProject', body, ['description', 'title']);
  if (body.title !== undefined && (typeof body.title !== 'string' || body.title.trim().length === 0 || body.title.length > 160)) {
    throw new MockApiError(400, 'Project title is invalid.', 'updateProject', 'validation_failed');
  }
  if (body.description !== undefined && body.description !== null && (typeof body.description !== 'string' || body.description.length > 1000)) {
    throw new MockApiError(400, 'Project description is invalid.', 'updateProject', 'validation_failed');
  }
}
function validateUpdateProjectMemberRoleRequest(body: OperationRequest<'updateProjectMemberRole'>) {
  rejectUnknownKeys('updateProjectMemberRole', body, ['role']);
  if (!assignableProjectMemberRoles.includes((body as { role?: unknown }).role as (typeof assignableProjectMemberRoles)[number])) {
    throw new MockApiError(400, 'Project member role is invalid.', 'updateProjectMemberRole', 'validation_failed');
  }
}

export function createProjectOperations(context: MockRepositoryContext) {
  function listProjects(options: DeletedSourceListOptions<'listProjects'>): Promise<Schema<'DeletedProjectList'>>;
  function listProjects(options?: ActiveSourceListOptions<'listProjects'>): Promise<Schema<'ProjectList'>>;
  async function listProjects(options: SourceListOptions = {}): Promise<OperationResponse<'listProjects', '200'>> {
    context.applyOperation('listProjects');
    const state = requestedSourceListState('listProjects', options.state);
    const { state: _state, ...pagination } = options;
    const rawRole = context.rawActorRole();
    if (state === 'deleted') {
      const role = context.actorRole();
      if (role === 'non_member') {
        return paginateList('listProjects', [], pagination, { actorRole: role, state });
      }
      if (role !== 'admin' && role !== 'owner') {
        throw new MockApiError(403, 'Нет доступа к удаленным проектам.', 'listProjects');
      }
      const data = context.scenario.preset === 'empty-lists'
        ? []
        : context.state.deletedProjects.map((source) => deletedSourceRow(source));
      return paginateList('listProjects', data, pagination, { actorRole: role, state });
    }
    const projects = rawRole === 'non_member'
      ? context.state.projects.filter((project) => context.nonMemberAccessibleMembership(project.id))
      : context.state.projects;
    const data = projects.length === 0 || context.scenario.preset === 'empty-lists'
      ? []
      : projects.map((project) => context.visibleProject(project));
    return paginateList('listProjects', data, pagination, { actorRole: context.actorRole(), state });
  }

  async function createProject(body: OperationRequest<'createProject'>): Promise<OperationResponse<'createProject', '201'>> {
    context.applyOperation('createProject');
    if (context.scenario.preset === 'failed-create') {
      throw new MockApiError(500, 'Не удалось создать проект.', 'createProject');
    }
    const projectBase = {
      id: `project-${context.state.projects.length + 1}`,
      title: body.title,
      description: body.description ?? null,
      bookCount: 0,
      chapterCount: 0,
      wordCount: 0,
      createdAt: now,
      updatedAt: now,
    };
    const membershipId = `member-created-${projectBase.id}`;
    const project: Project = {
      ...projectBase,
      currentMembership: createProjectMembershipForUser(projectBase, context.state.user, membershipId, 'owner'),
    };
    context.state.projects.push(project);
    context.state.memberships.push(createProjectMembershipForUser(project, context.state.user, membershipId, 'owner'));
    return context.visibleProject(project);
  }

  /**
   * «Попробовать на примере» (specs/2026-11-09-demo-saga.md DS1): the demo saga of the signed-in author, with its book
   * ready, made when he has none. A second call answers the same saga.
   */
  async function openDemoSaga(): Promise<OperationResponse<'openDemoSaga', '200'>> {
    context.applyOperation('openDemoSaga');
    const session = context.state.session;
    if (!session) throw new MockApiError(401, 'Сессия истекла. Войдите снова.', 'openDemoSaga', 'auth.session_required');
    if (!session.user.emailVerified) throw new MockApiError(403, 'email address is not verified', 'openDemoSaga', 'auth.email_unverified');
    if (context.scenario.preset === 'failed-create') {
      throw new MockApiError(500, 'Не удалось открыть пример.', 'openDemoSaga');
    }
    const existing = context.state.projects.find((project) => project.demo && project.currentMembership.role === 'owner');
    if (existing) return context.visibleProject(existing);
    const projectBase = {
      id: `project-${context.state.projects.length + 1}`,
      title: demoSagaTitle,
      description: demoSagaDescription,
      demo: true as const,
      bookCount: 0,
      chapterCount: 0,
      wordCount: 0,
      createdAt: now,
      updatedAt: now,
    };
    const membershipId = `member-created-${projectBase.id}`;
    const project: Project = {
      ...projectBase,
      currentMembership: createProjectMembershipForUser(projectBase, context.state.user, membershipId, 'owner'),
    };
    context.state.projects.push(project);
    context.state.memberships.push(createProjectMembershipForUser(project, context.state.user, membershipId, 'owner'));
    // The book of the example: one ready book, as the import of the product leaves it.
    const bookId = `book-demo-${projectBase.id}`;
    const templateBook = context.state.books.find((book) => book.id === draftBookId);
    const templateChapter = context.state.chapters.find((chapter) => chapter.bookId === draftBookId);
    if (templateBook && templateChapter) {
      context.state.books.push({
        ...clone(templateBook),
        id: bookId,
        projectId: project.id,
        title: 'Игрок',
        subtitle: null,
        order: 1,
        displayNumber: '1',
        status: 'ready',
        chapterCount: 1,
        wordCount: templateChapter.wordCount,
        createdAt: now,
        updatedAt: now,
      });
      context.state.chapters.push({
        ...clone(templateChapter),
        id: `${bookId}-chapter-1`,
        bookId,
        title: 'Глава I',
        status: 'published',
        publishedAt: now,
        paragraphs: templateChapter.paragraphs.map((paragraph, index) => ({ ...clone(paragraph), id: `${bookId}-paragraph-${index + 1}` })),
        navigation: { ...clone(templateChapter.navigation), total: 1 },
      });
      // Its text is found and its cards are built: the example is ready at once (DS2).
      for (const prepared of [context.state.indexedChapterIds, context.state.cardedChapterIds]) {
        prepared.draft.push(`${bookId}-chapter-1`);
        prepared.published.push(`${bookId}-chapter-1`);
      }
      Object.assign(project, { bookCount: 1, chapterCount: 1, wordCount: templateChapter.wordCount });
    }
    return context.visibleProject(project);
  }

  async function getProject(id: string): Promise<OperationResponse<'getProject', '200'>> {
    context.applyOperation('getProject');
    return context.visibleProject(context.projectFor(id, 'getProject'));
  }

  async function getProjectSupportSnapshot(id: string): Promise<OperationResponse<'getProjectSupportSnapshot', '200'>> {
    context.applyOperation('getProjectSupportSnapshot');
    context.projectFor(id, 'getProjectSupportSnapshot', ['admin', 'owner']);
    return {
      schemaVersion: 'CKProjectSupportSnapshotV1',
      deletionTasks: [],
      artifactOwners: [],
      deletionTaskAges: [],
    };
  }

  async function updateProject(id: string, body: OperationRequest<'updateProject'>): Promise<OperationResponse<'updateProject', '200'>> {
    context.applyOperation('updateProject');
    validateUpdateProjectRequest(body);
    const project = context.projectFor(id, 'updateProject', ['editor', 'admin', 'owner']);
    if (Object.prototype.hasOwnProperty.call(body, 'description')) {
      project.description = body.description ?? '';
    }
    if (Object.prototype.hasOwnProperty.call(body, 'title')) {
      project.title = body.title!;
    }
    project.updatedAt = now;
    return context.visibleProject(project);
  }

  async function deleteProject(id: string): Promise<void> {
    context.applyOperation('deleteProject');
    const project = context.projectFor(id, 'deleteProject', ['admin', 'owner']);
    scrubAgentWorkflows(context, { projectId: id });
    const removedJobIds = new Set(
      context.state.jobs.filter((job) => job.subject.projectId === id).map((job) => job.id),
    );
    context.state.deletedProjects.push(deletedSource(project));
    context.state.projects = context.state.projects.filter((project) => project.id !== id);
    context.state.jobs = context.state.jobs.filter((job) => !removedJobIds.has(job.id));
    for (const jobId of removedJobIds) {
      context.jobCreators.delete(jobId);
    }
  }

  async function restoreProject(id: string): Promise<OperationResponse<'restoreProject', '200'>> {
    context.applyOperation('restoreProject');
    const sourceIndex = context.state.deletedProjects.findIndex((source) => source.resource.id === id);
    if (sourceIndex < 0) {
      throw new MockApiError(404, 'Resource not found.', 'restoreProject');
    }
    const source = context.state.deletedProjects[sourceIndex]!;
    context.requireDeletedSourceRole('restoreProject', id, ['admin', 'owner']);
    context.state.deletedProjects.splice(sourceIndex, 1);
    context.state.projects.push(source.resource);
    return context.visibleProject(source.resource);
  }

  async function listProjectMembers(id: string, options: CursorListOptions = {}): Promise<OperationResponse<'listProjectMembers', '200'>> {
    context.applyOperation('listProjectMembers');
    const project = context.projectFor(id, 'listProjectMembers');
    context.requireProjectAccess('listProjectMembers', project.id, ['admin', 'owner']);
    return paginateList('listProjectMembers', context.state.memberships.filter((membership) => membership.projectId === project.id), options, { projectId: project.id });
  }

  async function updateProjectMemberRole(projectIdInput: string, memberId: string, body: OperationRequest<'updateProjectMemberRole'>): Promise<OperationResponse<'updateProjectMemberRole', '200'>> {
    context.applyOperation('updateProjectMemberRole');
    validateUpdateProjectMemberRoleRequest(body);
    const project = context.projectFor(projectIdInput, 'updateProjectMemberRole');
    const membership = context.state.memberships.find((candidate) => candidate.projectId === project.id && candidate.id === memberId);
    if (!membership) {
      throw new MockApiError(404, 'Resource not found.', 'updateProjectMemberRole');
    }
    context.requireProjectAccess('updateProjectMemberRole', project.id, ['admin', 'owner']);
    if (membership.role === 'owner') {
      throw new MockApiError(409, 'Owner membership cannot be changed while ownership transfer is out of scope.', 'updateProjectMemberRole');
    }
    Object.assign(membership, { role: body.role, updatedAt: now });
    return clone(membership);
  }

  async function removeProjectMember(projectIdInput: string, memberId: string): Promise<void> {
    context.applyOperation('removeProjectMember');
    const project = context.projectFor(projectIdInput, 'removeProjectMember');
    const membership = context.state.memberships.find((candidate) => candidate.projectId === project.id && candidate.id === memberId);
    if (!membership) {
      throw new MockApiError(404, 'Resource not found.', 'removeProjectMember');
    }
    context.requireProjectAccess('removeProjectMember', project.id, ['admin', 'owner']);
    if (membership.role === 'owner') {
      throw new MockApiError(409, 'Owner membership cannot be removed while last-owner transfer is out of scope.', 'removeProjectMember');
    }
    context.state.memberships = context.state.memberships.filter((candidate) => !(candidate.projectId === project.id && candidate.id === memberId));
  }

  async function listProjectInvitations(id: string, options: CursorListOptions = {}): Promise<OperationResponse<'listProjectInvitations', '200'>> {
    context.applyOperation('listProjectInvitations');
    const project = context.projectFor(id, 'listProjectInvitations');
    context.requireProjectAccess('listProjectInvitations', project.id, ['admin', 'owner']);
    return paginateList('listProjectInvitations', context.state.invitations.filter((invitation) => invitation.projectId === project.id), options, { projectId: project.id });
  }

  async function listMyProjectInvitations(options: CursorListOptions = {}): Promise<OperationResponse<'listMyProjectInvitations', '200'>> {
    context.applyOperation('listMyProjectInvitations');
    if (!context.state.session) {
      throw new MockApiError(401, 'Сессия отсутствует.', 'listMyProjectInvitations');
    }
    const providerVerified = context.state.session.user.emailVerified && context.scenario.emailVerified !== false;
    const verifiedEmail = providerVerified ? (context.scenario.verifiedEmail ?? context.state.session.user.email) : null;
    if (!verifiedEmail || normalizedEmail(verifiedEmail) !== normalizedEmail(context.state.session.user.email)) {
      throw new MockApiError(403, 'Email is not verified for invitation discovery.', 'listMyProjectInvitations');
    }
    const email = normalizedEmail(verifiedEmail);
    const invitations = context.state.invitations.filter((invitation) => invitation.status === 'pending' && normalizedEmail(invitation.email) === email);
    return paginateList('listMyProjectInvitations', invitations, options, { verifiedEmail: email });
  }

  async function createProjectInvitation(id: string, body: OperationRequest<'createProjectInvitation'>): Promise<OperationResponse<'createProjectInvitation', '201'>> {
    context.applyOperation('createProjectInvitation');
    const project = context.projectFor(id, 'createProjectInvitation');
    context.requireProjectAccess('createProjectInvitation', project.id, ['admin', 'owner']);
    if ((body as { role?: string }).role === 'owner') {
      throw new MockApiError(400, 'Owner invitations are out of scope.', 'createProjectInvitation');
    }
    const inviter = context.state.session?.user ?? context.state.user;
    const invitation = createProjectInvitationFixture(
      project.id,
      `invitation-created-${context.state.invitations.length + 1}`,
      'pending',
      body.email,
      body.role,
      project.title,
      inviter,
    );
    context.state.invitations.push(invitation);
    return clone(invitation);
  }

  async function cancelProjectInvitation(id: string): Promise<OperationResponse<'cancelProjectInvitation', '200'>> {
    context.applyOperation('cancelProjectInvitation');
    const invitation = context.invitationFor(id, 'cancelProjectInvitation');
    const project = context.projectFor(invitation.projectId, 'cancelProjectInvitation');
    context.requireProjectAccess('cancelProjectInvitation', project.id, ['admin', 'owner']);
    if (invitation.status !== 'pending') {
      throw new MockApiError(409, 'Invitation is not pending.', 'cancelProjectInvitation');
    }
    const canceled = { ...invitation, status: 'canceled' as const, acceptedAt: null, updatedAt: now };
    context.state.invitations = context.state.invitations.map((candidate) => (candidate.id === id ? canceled : candidate));
    return clone(canceled);
  }

  async function acceptProjectInvitation(id: string): Promise<OperationResponse<'acceptProjectInvitation', '200'>> {
    context.applyOperation('acceptProjectInvitation');
    if (context.rawActorRole() !== 'non_member') {
      throw new MockApiError(403, 'Нет доступа к этому проекту.', 'acceptProjectInvitation');
    }
    const invitation = context.invitationFor(id, 'acceptProjectInvitation');
    const authenticatedUser = context.state.session?.user ?? context.state.user;
    const providerVerified = authenticatedUser.emailVerified && context.scenario.emailVerified !== false;
    const verifiedEmail = providerVerified ? (context.scenario.verifiedEmail ?? authenticatedUser.email) : null;
    if (!verifiedEmail || normalizedEmail(verifiedEmail) !== normalizedEmail(invitation.email)) {
      throw new MockApiError(404, 'Resource not found.', 'acceptProjectInvitation');
    }
    if (normalizedEmail(authenticatedUser.email) !== normalizedEmail(invitation.email)) {
      throw new MockApiError(404, 'Resource not found.', 'acceptProjectInvitation');
    }
    if (invitation.status !== 'pending') {
      throw new MockApiError(409, 'Invitation is not pending.', 'acceptProjectInvitation');
    }
    const project = context.state.projects.find((candidate) => candidate.id === invitation.projectId);
    if (!project) {
      throw new MockApiError(404, 'Resource not found.', 'acceptProjectInvitation');
    }
    const accepted = { ...invitation, status: 'accepted' as const, acceptedAt: now, updatedAt: now };
    context.state.invitations = context.state.invitations.map((candidate) => (candidate.id === id ? accepted : candidate));
    const membership = createProjectMembershipForUser(project, authenticatedUser, `member-${id}`, invitation.role);
    context.state.memberships = [
      ...context.state.memberships.filter((candidate) => !(candidate.projectId === project.id && candidate.id === membership.id)),
      membership,
    ];
    return clone(membership);
  }

  return {
    listProjects,
    createProject,
    openDemoSaga,
    getProject,
    getProjectSupportSnapshot,
    updateProject,
    deleteProject,
    restoreProject,
    listProjectMembers,
    updateProjectMemberRole,
    removeProjectMember,
    listProjectInvitations,
    listMyProjectInvitations,
    createProjectInvitation,
    cancelProjectInvitation,
    acceptProjectInvitation,
  };
}
