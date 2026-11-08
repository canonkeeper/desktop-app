import {
  ApiStatusError,
  type OperationId,
  type OperationRequest,
  type Schema,
} from '../../shared/api';

export type MockScenarioPreset =
  | 'normal'
  | 'auth-401'
  | 'forbidden-403'
  | 'empty-lists'
  | 'request-error'
  | 'failed-create';
export type MockActorRole = 'owner' | 'admin' | 'editor' | 'viewer' | 'non_member';

export type MockOperationFailure = {
  status: number;
  message: string;
  code?: string;
};

export interface MockScenarioControl {
  actorRole?: MockActorRole;
  emailVerified?: boolean;
  preset: MockScenarioPreset;
  operationFailures?: Partial<Record<OperationId, MockOperationFailure>>;
  /** The Job code a running chat turn fails with instead of answering (UC4.2 «Отказы хода»). */
  turnFailure?: string;
  /**
   * The provider's image limit a drawing or a layer still drawn runs into instead of drawing
   * (specs/2026-10-29-saga-illustrations.md R13.5): it ends draw_limited with this reset.
   */
  drawingLimit?: { retryAt: string | null };
  verifiedEmail?: string;
}

export class MockApiError extends ApiStatusError {
  constructor(
    readonly status: number,
    message: string,
    readonly operationId: OperationId,
    readonly code?: string,
  ) {
    super(
      status,
      {
        code: code ?? 'mock_error',
        detail: message,
        requestId: `mock-${operationId}`,
        status,
        title: message,
        type: 'about:blank',
      },
      new Response(null, { status }),
    );
    this.name = 'MockApiError';
  }
}

export const agentWorkflowOperationIds = [
  'listChatSessions',
  'createChatSession',
  'getChatSession',
  'resolveChatChapterKey',
  'deleteChatSession',
  'createChatTurn',
  'createChatTurnWithChapter',
  'getChatAttachment',
  'steerChatTurn',
] as const satisfies readonly OperationId[];

const protectedOperations = new Set<OperationId>([
  'listNotes', 'createPersonalNote', 'getPersonalNote', 'updatePersonalNote', 'deletePersonalNote', 'resolvePersonalNoteSource',
  'getCurrentUser',
  'getAccountQuota',
  'listAdminWriters', 'getAdminWriter', 'setAdminWriterQuota', 'blockAdminWriter', 'unblockAdminWriter', 'listAdminDays', 'getAdminServiceStatus',
  'getAdminQuotaDefaults', 'setAdminQuotaDefaults',
  // «Связаться с нами» (specs/2026-11-07-contacts.md): the reads of an author and the admin part; a guest writes.
  'getAdminContacts', 'setAdminContacts', 'listFeedbackThreads', 'getFeedbackThread', 'addFeedbackMessage', 'getFeedbackImage',
  'listAdminFeedback', 'getAdminFeedbackThread', 'replyAdminFeedback', 'setAdminFeedbackStatus',
  'getAdminPromoSagas', 'createAdminPromoPermission', 'listAdminPromoPermissions', 'takeAdminPromoFragment', 'getAdminPromoIllustration',
  'listPromoPermissions', 'answerPromoPermission', 'revokePromoPermission',
  'sendEmailVerification',
  'changePassword',
  'logoutEverywhere',
  'acceptLegalDocuments',
  'logoutUser',
  'listProjects',
  'getProject',
  'createProject',
  'updateProject',
  'deleteProject',
  'listBooks',
  'createBook',
  'getBook',
  'updateBook',
  'deleteBook',
  'getImportConstraints',
  'importBookFile',
  'createImportQueue',
  'listImportQueues',
  'getImportQueue',
  'uploadImportQueueItem',
  'updateImportQueue',
  'startImportQueue',
  'cancelImportQueue',
  'listProjectJobs',
  'getIndexingStatus',
  'startProjectIndexing',
  'getJob',
  'cancelJob',
  'listChapters',
  'getChapter',
  'deleteChapter',
  'searchProject',
  'searchChapters',
  'listProjectMembers',
  'updateProjectMemberRole',
  'removeProjectMember',
  'listProjectInvitations',
  'listMyProjectInvitations',
  'createProjectInvitation',
  'acceptProjectInvitation',
  'cancelProjectInvitation',
  'listAuthorMaterials',
  'updateAuthorMaterial',
  'deleteAuthorMaterial',
  'startAuthorMaterialsImport',
  'getAuthorMaterialsImport',
  'confirmAuthorMaterialsImport',
  'discardAuthorMaterialsImport',
  'listAuthorMemory',
  'createAuthorMemory',
  'updateAuthorMemory',
  'deleteAuthorMemory',
  'listChapterDrafts',
  'createChapterDraft',
  'getChapterDraft',
  'updateChapterDraft',
  'deleteChapterDraft',
  'startChapterDraftJob',
  'addChapterDraftToBook',
  'openChapterEdit',
  'replaceChapterFromDraft',
  'revertChapterFromDraft',
  'applyChapterDraftNote',
  'updateChapterDraftNote',
  'startChapterProofread',
  'loadChapterDraftFile',
  'applyChapterDraftNotes',
  'getChapterDraftExport',
  'getProjectVoice',
  'setAccountVoice',
  'listProjectImages',
  'getProjectImage',
  'getProjectImageFile',
  'setProjectImageReference',
  'updateProjectImageDetails',
  'removeProjectImageReference',
  'deleteProjectImage',
  'deleteProjectImages',
  'getProjectIllustrationStyle',
  'updateProjectIllustrationStyle',
  ...agentWorkflowOperationIds,
]);

export function applyScenario(operationId: OperationId, scenario: MockScenarioControl) {
  const injected = scenario.operationFailures?.[operationId];
  if (injected) {
    throw new MockApiError(injected.status, injected.message, operationId, injected.code);
  }

  if (scenario.preset === 'auth-401' && protectedOperations.has(operationId)) {
    throw new MockApiError(401, 'Сессия истекла. Войдите снова.', operationId);
  }

  if (scenario.preset === 'forbidden-403' && operationId !== 'getCurrentUser' && protectedOperations.has(operationId)) {
    throw new MockApiError(403, 'Нет доступа к этому проекту.', operationId);
  }

  if (scenario.preset === 'request-error') {
    throw new MockApiError(500, 'Mock request failed.', operationId);
  }
}

export function isProtectedOperation(operationId: OperationId) {
  return protectedOperations.has(operationId);
}

export const normalScenario: MockScenarioControl = { actorRole: 'owner', preset: 'normal' };

type CanonicalSourceLocator = Schema<'CanonicalChapterSourceLocatorV1'>;

export type AgentWorkflowScenarioFixture = {
  actorRole: Extract<MockActorRole, 'editor'>;
  projectId: string;
  jobTransition: { start: 'running'; terminal: 'succeeded' };
  publishedSource: CanonicalSourceLocator;
  draftSource: CanonicalSourceLocator;
  chat: {
    session: OperationRequest<'createChatSession'>;
    turn: OperationRequest<'createChatTurn'>;
  };
  expected: {
    jobs: { chat: { start: 'running'; terminal: 'succeeded' } };
    deletion: {
      restoredBookId: string;
      deletedSource: { chat: { status: 409; code: 'conflict' } };
      replayConflicts: { chat: { status: 409; code: 'conflict' } };
      scrubbed: { chatJob: { status: 404 } };
    };
  };
};

const sourceIdentity = {
  projectId: 'project-white-port',
  bookId: 'book-02',
  sourceGeneration: '1111111111111111111111111111111111111111111111111111111111111111',
  contentHash: '2222222222222222222222222222222222222222222222222222222222222222',
};

export const agentWorkflowScenario = {
  actorRole: 'editor',
  projectId: sourceIdentity.projectId,
  jobTransition: { start: 'running', terminal: 'succeeded' },
  publishedSource: {
    ...sourceIdentity,
    chapterId: 'chapter-12',
    sourceRef: 'chapter-12',
    sourceView: 'published',
    sourceRevision: 7,
  },
  draftSource: {
    ...sourceIdentity,
    chapterId: 'chapter-16',
    sourceRef: 'chapter-16',
    sourceView: 'draft',
    sourceRevision: 9,
  },
  chat: {
    session: { scope: 'project', sourceView: 'published', title: 'Исследование порта' },
    turn: { body: 'Кто первым заметил дым?' },
  },
  expected: {
    jobs: { chat: { start: 'running', terminal: 'succeeded' } },
    deletion: {
      restoredBookId: sourceIdentity.bookId,
      deletedSource: { chat: { status: 409, code: 'conflict' } },
      replayConflicts: { chat: { status: 409, code: 'conflict' } },
      scrubbed: { chatJob: { status: 404 } },
    },
  },
} satisfies AgentWorkflowScenarioFixture;
