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
  /**
   * The helper of the build is not connected — the desktop app until the author connects one
   * (specs/2026-11-12-desk-window.md WN3.5): no cards Job is queued, and the reading of a saga waits for the helper.
   */
  helperMissing?: boolean;
  /**
   * The site of the link does not answer (specs/2026-11-13-desk-account-ai.md AI1.1): the choice of the helper of the account
   * is refused ai.account_unreachable.
   */
  accountSilent?: boolean;
  /**
   * The app is linked with an account of the site (specs/2026-11-13-desk-account-ai.md, checklist step 7): the mock has no
   * browser, so a scenario links it — getAccount answers linked, and the helper of the account may be chosen.
   */
  accountLinked?: boolean;
  /**
   * How the link that waits ends at the next read of getAccount (specs/2026-11-15-desk-account-link-web.md, checklist step 2):
   * the author confirmed it on the site, the site refused the code, the shell did not keep the pass, or its term ended. The
   * mock has no browser and no site: the scenario is the return; without it a link waits until its term or «Отменить».
   */
  accountLinkOutcome?: 'linked' | 'exchange' | 'keyring' | 'expired';
  /** The site refused the pass of the linked app (AW2.4): getAccount says broken, once — a new link links again. */
  accountBroken?: boolean;
  /** The site did not answer the last read of the account (AW2.3): what it told stays with «нет связи». */
  accountUnreachable?: boolean;
  /** The app is linked and knows nothing of the account yet — a restart without the site, or the moment after a link (AW2.3). */
  accountInfoMissing?: boolean;
  /** Where the shell keeps the pass (AW2.2, AW1.3): the store of the system by default, or the file of the data directory. */
  accountPassStore?: 'system' | 'file';
  /** The site said nothing of the questions of the day (AW2.2): no line «Вопросы сегодня». */
  accountQuestionsUnknown?: boolean;
  /** The end of the tier of the account; without it the tier has no term (AW2.2). */
  accountTierUntil?: string;
  /** The site did not hear «Отвязать» (AW1.5): unlinking is only on this computer. */
  unlinkSiteSilent?: boolean;
  /**
   * The site has no open version of the app in its mirror (specs/2026-11-16-desk-app-page.md AP1): the page «Приложение»
   * stays hidden whatever the switch, and the administrator reads «Зеркала нет».
   */
  desktopAppNoMirror?: boolean;
  /**
   * The start of a question of the helper of the account is refused by its counter (specs/2026-11-13-desk-account-ai.md AI2.1,
   * AI2.6): the site did not answer twice (ai.account_unreachable, mayReturn says whether its unit comes back by itself), the
   * fence of the day (ai.daily_fence, with Retry-After), an app the site does not take (ai.app_unfit), a thread too large
   * (app.request_too_large) or a refusal of the tier (quota.chat_turns_daily, quota.active_turns).
   */
  accountTurnRefusal?: { code: string; mayReturn?: boolean };
  /**
   * Who holds the places of «разом» as the app counts them from what its site told (specs/2026-11-15-desk-turns-here.md TH2.1):
   * getAccountQuota of the helper of the account names them; without it the answer has none, as against an older site.
   */
  accountPlaces?: Schema<'ActiveTurnPlaces'>;
  /** Whether the unit of the question of a failed turn of the helper of the account came back (AI2.9): getJob says it. */
  questionReturned?: NonNullable<Schema<'Job'>['questionReturned']>;
  /**
   * The helper is connected and the author has not allowed it to read the books of the saga yet
   * (specs/2026-11-13-desk-gateway.md GW4.3): the reading of a saga waits for his consent, the chat takes questions.
   */
  consentMissing?: boolean;
  /**
   * The reading of the books by «Свой ИИ» stands at this reason (specs/2026-11-13-desk-consent.md CN3.4, CN3.8): the mock
   * has no provider, so a refusal of one, no connection or the sum is set here; a consent the saga has reads paused with it.
   */
  readingPause?: Schema<'ReadingPause'>;
  /**
   * The reading stops at this reason now, as the server stops it (specs/2026-11-15-desk-account-words-web.md, checklist step 2):
   * a read of the status of a saga under a consent that acts puts that consent on the pause — it stays there once the scenario
   * is gone, and «Продолжить» or «Повторить» lets the reading go on.
   */
  readingStops?: Schema<'ReadingPause'>;
  /**
   * The limits of the tier of the account for the reading over 30 days the site told (specs/2026-11-13-desk-account-ai.md
   * AI3.4, AI3.7); by default room in both; null — the site has not told them yet.
   */
  readingLimits?: { characters: Schema<'ReadingLimit'>; work: Schema<'ReadingLimit'> } | null;
  /**
   * What is left to read of a saga the estimate of the helper of the account counts (specs/2026-11-15-desk-account-words-web.md,
   * checklist step 2): its chapters and whether the names and the threads wait; by default 612 chapters and nothing waits.
   */
  readingEstimateLeft?: { chapters: number; namesWait?: boolean; threadsWait?: boolean };
  /**
   * The marks of the reading a scenario sows into a saga (specs/2026-11-15-desk-reading-rest.md RR2.5): they are the state of
   * the saga from then on — «Повторить» forgets them and a step of the worker of the mock brings what waits together or marks
   * it again; a scenario with other marks sows them anew. Chapters of the saga that did not read after the paid answers of
   * the model (CN3.6).
   */
  readingUnread?: number;
  /** Why most of those chapters did not read: the answer broke off (the default) or could not be read. */
  readingUnreadReason?: NonNullable<Schema<'IndexingReading'>['unread']>['reason'];
  /** Every chapter of the saga is read and the names of the heroes and the threads wait for the model (CN3.8). */
  readingNamesWait?: boolean;
  /** Every chapter is read and the names of the heroes did not come together, for this reason (RR2.1). */
  readingNamesUnread?: NonNullable<Schema<'ReadingUnreadReason'>>;
  /** Every chapter is read and the threads of the plot did not come together, for this reason (RR2.1). */
  readingThreadsUnread?: NonNullable<Schema<'ReadingUnreadReason'>>;
  /** The worker of the mock marks the names and the threads again on the next attempt, for the same reasons (RR2.5). */
  readingRestAgain?: boolean;
  /**
   * The journal of what went out of the app (specs/2026-11-14-desk-journal.md JR3.1, JR3.3): nothing went yet; cleared and
   * empty; cleared with rows since; or a full journal — two sagas, a reading that goes on, a question that went to another
   * helper for the first time, and more rows than one page («Показать ещё»). Without it, the journal of the computer of the
   * mock: three rows of one saga.
   */
  sendLog?: 'nothing' | 'cleared' | 'cleared-rows' | 'full';
  /** The sample of three chapters does not read, for this reason (CN2.4). */
  sampleFailure?: NonNullable<Schema<'ReadingSample'>['failure']>;
  /**
   * Where the shell of the app keeps its secrets (specs/2026-11-13-desk-secrets.md SE1): the store of the system, or — a
   * Linux without one — a file of the data directory.
   */
  aiSecretsStore?: 'system' | 'file';
  /**
   * An answer of the helper of the chat runs (specs/2026-11-13-desk-gateway.md GW2.3): a new window of the chat waits for
   * its end — «Применится после ответа помощника».
   */
  aiAnswerRunning?: boolean;
  /**
   * The bridge with an Obsidian vault (specs/2026-11-13-desk-obsidian.md): what the shell answers a choice of a folder —
   * the folder it picked, or the reason the backend refuses it —, a window without its shell, and how the vault of a linked
   * saga answers now: not at all, a vault gone by its reason, another operation reading it, a vault too slow, a choice of
   * too many notes or of too many entries, a tree cut at the limits of a look, a choice of more records of notes than a load or
   * a saga keeps (specs/2026-11-15-desk-obsidian-notes.md ON1.7). obsidianLinked makes the folders and the
   * notes of the example linked to books and records of the saga as part A2 keeps them: a book changed since its load, a
   * book in «Недавно удалённые», a note unchanged and a note changed since their load, a record edited in the app.
   */
  obsidianShell?: { picked: string } | { refused: 'not_found' | 'not_folder' | 'unreadable' } | 'absent' | 'waits';
  obsidianVault?: 'unknown' | { missing: 'not_found' | 'not_folder' | 'unreadable' } | 'busy' | 'slow' | 'too_large' | 'too_many_entries' | 'truncated'
    | 'too_many_records';
  obsidianLinked?: boolean;
  /**
   * The author edited the notes of the example in Obsidian since he loaded them (specs/2026-11-15-desk-obsidian-book-web.md): every
   * folder glues another text — one chapter more —, so a book loaded from it has changes in its notes.
   */
  obsidianEdited?: boolean;
  /**
   * How a write of the books of the folders answers (specs/2026-11-14-desk-obsidian-books.md, Data): the saga has a live
   * queue of the volumes, the key of a load that fell, a note of the folder only in the cloud, an update that changes
   * nothing, the folder of a link gone from the vault. Another glue than the fingerprint of the request is folder_changed.
   * A load of the records of notes meets a name the author took during it (specs/2026-11-15-desk-obsidian-notes.md): conflict.
   */
  obsidianWrite?: 'queue_busy' | 'load_interrupted' | 'notes_unread' | 'nothing_to_apply' | 'folder_missing' | 'conflict';
  /**
   * How the export of the map and of the notes of the chapters answers (specs/2026-11-15-desk-obsidian-export.md, Data;
   * specs/2026-11-16-desk-obsidian-export-web.md XW4): the map is being updated (`building` in getSagaMap too) or the helper has
   * read no chapter (the map without cards), a link or a file on the way to the folder of the export, its manifest that does not
   * read, a file the author changed — not updated without his word, moved into _Удалено/ with it —, a run that did not reach its
   * end, and one that did not read four files of its folder — one by each reason — and did not end either. The export reads the
   * map of the saga as getSagaMap does: a saga without cards is `no_cards` without the scenario. By default the first export
   * creates every reference, the next ones change none.
   */
  obsidianExport?: 'updating' | 'no_cards' | 'unsafe' | 'manifest_unreadable' | 'changed' | 'partial' | 'skipped';
}

/** What a refusal carries besides its code: fields of the problem and the Retry-After of a 429. */
export interface MockProblemExtras {
  fields?: Pick<Schema<'Problem'>, 'attemptsLeft' | 'lockMinutes' | 'materialId' | 'mayReturn' | 'errors' | 'vaultReason' | 'notes' | 'bytes' | 'limitNotes' | 'limitBytes' | 'entries' | 'limitEntries' | 'folders' | 'queue' | 'unread' | 'bookId'
    | 'records' | 'sagaRecords' | 'limitRecords' | 'reason'>;
  retryAfter?: number;
}

export class MockApiError extends ApiStatusError {
  constructor(
    readonly status: number,
    message: string,
    readonly operationId: OperationId,
    readonly code?: string,
    readonly extras: MockProblemExtras = {},
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
        ...extras.fields,
      },
      new Response(null, { status, headers: extras.retryAfter === undefined ? undefined : { 'Retry-After': String(extras.retryAfter) } }),
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
  'listRecentlyDeleted',
  'listAdminWriters', 'getAdminWriter', 'setAdminWriterQuota', 'blockAdminWriter', 'unblockAdminWriter', 'unlinkAdminWriterApp', 'listAdminDays', 'getAdminServiceStatus',
  'getAdminQuotaDefaults', 'setAdminQuotaDefaults',
  // The switch of the page «Приложение» (specs/2026-11-16-desk-app-page.md AP1.2); the page itself is read by anyone.
  'getAdminDesktopApp', 'setAdminDesktopApp',
  // «Связаться с нами» (specs/2026-11-07-contacts.md): the reads of an author and the admin part; a guest writes.
  'getAdminContacts', 'setAdminContacts', 'listFeedbackThreads', 'getFeedbackThread', 'addFeedbackMessage', 'getFeedbackImage',
  'listAdminFeedback', 'getAdminFeedbackThread', 'replyAdminFeedback', 'setAdminFeedbackStatus',
  'getAdminPromoSagas', 'createAdminPromoPermission', 'listAdminPromoPermissions', 'takeAdminPromoFragment', 'getAdminPromoIllustration',
  'listPromoPermissions', 'answerPromoPermission', 'revokePromoPermission',
  'sendEmailVerification',
  'changeUnverifiedEmail',
  'changePassword',
  'logoutEverywhere',
  'setAccountName',
  'getAccountSessions',
  'logoutOtherSessions',
  'acceptLegalDocuments',
  'logoutUser',
  'listProjects',
  'getProject',
  'createProject',
  'openDemoSaga',
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
  'skipImportQueueItem',
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
  'createAuthorMaterial',
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
  'takeChapterDraftOriginalLook',
  'applyChapterDraftNotes',
  'getChapterDraftExport',
  'getProjectVoice',
  'setAccountVoice',
  'getAccountLetters',
  'setAccountLetters',
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
