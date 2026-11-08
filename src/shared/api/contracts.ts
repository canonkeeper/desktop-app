import type { components, operations } from './generated/openapi';
import { operationManifest, type OperationId } from './generated/operation-manifest';

// Единая точка публичных типов API: здесь нет сетевой логики и состояния клиента.
export type { OperationId };
export type OperationManifestRoute = (typeof operationManifest)[OperationId];
export type BookExportFormat = NonNullable<OperationParameters<'getBookExport'>['query']>['format'];
/** A file the browser is about to save, with the name the server gave it. */
export type DownloadedFile = { blob: Blob; fileName: string };

export function getOperationManifestEntries(): ReadonlyArray<readonly [OperationId, OperationManifestRoute]> {
  return Object.entries(operationManifest) as Array<[OperationId, OperationManifestRoute]>;
}

type ResponseMap<TOperation extends OperationId> = operations[TOperation]['responses'];
type NumericStatus<TStatus> = TStatus extends `${infer TNumber extends number}` ? TNumber : TStatus;
type ResponseStatus<TOperation extends OperationId, TStatus> =
  NumericStatus<TStatus> extends keyof ResponseMap<TOperation> ? NumericStatus<TStatus> : never;
type ResponseContent<TResponse> = TResponse extends { content: infer TContent }
  ? TContent extends { 'application/json': infer TJson }
    ? TJson
    : undefined
  : undefined;
type RequestContent<TRequestBody> = TRequestBody extends { content: infer TContent }
  ? TContent extends { 'application/json': infer TJson }
    ? TJson
    : TContent extends { 'multipart/form-data': infer TForm }
      ? TForm
      : undefined
  : undefined;
type RequestBodyContent<TRequestBody> = [TRequestBody] extends [never]
  ? undefined
  : RequestContent<NonNullable<TRequestBody>>;

export type OperationResponses<TOperation extends OperationId> = {
  [TStatus in keyof ResponseMap<TOperation> as TStatus extends string | number ? TStatus : never]: ResponseContent<
    ResponseMap<TOperation>[TStatus]
  >;
} & {
  [TStatus in keyof ResponseMap<TOperation> as TStatus extends string | number ? `${TStatus}` : never]: ResponseContent<
    ResponseMap<TOperation>[TStatus]
  >;
};
export type OperationResponse<TOperation extends OperationId, TStatus extends string | number> = ResponseContent<
  ResponseMap<TOperation>[ResponseStatus<TOperation, TStatus>]
>;
export type OperationRequest<TOperation extends OperationId> = operations[TOperation] extends {
  requestBody?: infer TRequestBody;
}
  ? RequestBodyContent<TRequestBody>
  : undefined;
export type OperationParameters<TOperation extends OperationId> = operations[TOperation] extends {
  parameters: infer TParameters;
}
  ? TParameters
  : never;

export type SchemaName = keyof components['schemas'];
export type Schema<TName extends SchemaName> = components['schemas'][TName];
/**
 * Where the author is (VoiceCallPlace, specs/2026-11-03-voice-assistant.md V0.8, V2.4): the page of the saga and the ids
 * of what is open on it; the server reads the rest. A call follows him from page to page.
 */
export type VoicePlace = Schema<'VoiceCallPlace'>;
export type Project = Schema<'Project'>;
export type ProjectSupportSnapshot = Schema<'ProjectSupportSnapshot'>;
export type ProjectMembership = Schema<'ProjectMembership'>;
export type Book = Schema<'Book'>;
export type Chapter = Schema<'Chapter'>;
export type ReaderLocator = Schema<'ReaderLocator'>;
export type ReaderReferenceLocator = Schema<'ReaderReferenceLocator'>;
export type PersonalNote = Schema<'PersonalNote'>;
export type PersonalNoteSource = Schema<'PersonalNoteSource'>;
export type NotesListEntry = Schema<'NotesListEntry'>;
export type NotesListOptions = OperationRequest<'listNotes'> & Pick<ApiRequestOptions, 'signal'>;
export type ApiProblem = Schema<'Problem'>;
export type SearchScope = Schema<'SearchScope'>;
export type SourceNavigationAnchor = Schema<'SourceNavigationAnchor'>;
export type SourceView = Schema<'SourceView'>;
export type ChatChapterLocation = Schema<'ChatChapterLocation'>;
export type ChatAttachment = Schema<'ChatAttachment'>;
export type ChatAttachmentSummary = Schema<'ChatAttachmentSummary'>;
export type Job = Schema<'Job'>;
export type JobStartResponse = Schema<'JobStartResponse'>;
export type ImportQueue = Schema<'ImportQueue'>;
export type ImportQueueItem = Schema<'ImportQueueItem'>;
export type ChatSession = Schema<'ChatSession'>;
export type AgentTimelineEvent = Schema<'AgentTimelineEvent'>;
export type AgentTimelineContext = Schema<'AgentTimelineContext'>;
export type AgentTimelineDelta = Schema<'AgentTimelineDelta'>;
export type AgentTimelineSnapshot = Schema<'AgentTimelineSnapshot'>;
export type AgentTimelineStreamEnvelope = Schema<'AgentTimelineStreamEnvelope'>;

type OperationInit<TOperation extends OperationId> = {
  params?: unknown;
  body?: OperationRequest<TOperation>;
  signal?: AbortSignal;
  headers?: Record<string, string>;
};

export interface ApiRequestOptions {
  signal?: AbortSignal;
  idempotencyKey?: string;
}

export type RequiredIdempotencyOptions = ApiRequestOptions & {
  idempotencyKey: string;
};

export type ImportBookRequestOptions = RequiredIdempotencyOptions & {
  onUploadProgress?: (progress: UploadProgress) => void;
};

/** The author's own image (illustrations R15.1): the caption and the edition of the confirmation given with it. */
export interface ImageUploadInput {
  file: File;
  title?: string;
  consentVersion?: string;
}

/** An image of a message of feedback into its draft (specs/2026-11-07-contacts.md F1.3). */
export interface FeedbackImageUploadInput {
  file: File;
  draftKey: string;
}

export type FeedbackImageUploadOptions = Pick<ApiRequestOptions, 'signal'> & {
  onUploadProgress?: (progress: UploadProgress) => void;
};

export type BookCoverUploadOptions = Pick<ApiRequestOptions, 'signal'> & {
  onUploadProgress?: (progress: UploadProgress) => void;
};

export interface UploadProgress {
  loadedBytes: number;
  totalBytes: number | null;
}

/** Входные данные загрузки файла для `importBookFile`; по контракту сериализуются как multipart/form-data. */
export interface ImportBookInput {
  file: File;
  metadata?: NonNullable<OperationRequest<'importBookFile'>>['metadata'];
  options?: NonNullable<OperationRequest<'importBookFile'>>['options'];
}

/** Multipart payload for one placeholder item in a durable import queue. */
export interface ImportQueueItemUploadInput {
  itemId: string;
  file: File;
}

export type SearchProjectOptions = Partial<Pick<NonNullable<OperationRequest<'searchProject'>>, 'cursor' | 'filters' | 'limit'>> &
  Pick<ApiRequestOptions, 'signal'>;
type OperationQueryParameters<TOperation extends OperationId> = OperationParameters<TOperation> extends { query?: infer TQuery }
  ? NonNullable<TQuery>
  : never;
export type CursorListOptions<TOperation extends OperationId> = Omit<OperationQueryParameters<TOperation>, 'cursor'> & {
  cursor?: string | null;
} & Pick<ApiRequestOptions, 'signal'>;
type SourceListOperation = 'listProjects' | 'listBooks' | 'listChapters';
export type ActiveSourceListOptions<TOperation extends SourceListOperation> = Omit<CursorListOptions<TOperation>, 'state'> & {
  state?: 'active';
};
export type DeletedSourceListOptions<TOperation extends SourceListOperation> = Omit<CursorListOptions<TOperation>, 'state'> & {
  state: 'deleted';
};
export type ListOptions = {
  cursor?: string | null;
  limit?: number;
} & Pick<ApiRequestOptions, 'signal'>;
export type ListProjectJobsOptions = OperationQueryParameters<'listProjectJobs'> & Pick<ApiRequestOptions, 'signal'>;
export type GetIndexingStatusOptions = OperationQueryParameters<'getIndexingStatus'> & Pick<ApiRequestOptions, 'signal'>;
type SagaReadOptions<TOperation extends OperationId> = OperationQueryParameters<TOperation> & Pick<ApiRequestOptions, 'signal'>;
export type ListSagaGlossaryOptions = SagaReadOptions<'listSagaGlossary'>;
export type GetSagaMapOptions = SagaReadOptions<'getSagaMap'>;
export type GetSagaMapNodeOptions = SagaReadOptions<'getSagaMapNode'>;
export type SearchSagaMapOptions = SagaReadOptions<'searchSagaMap'>;
export type ListSagaMapHiddenOptions = SagaReadOptions<'listSagaMapHidden'>;
export type GetSagaContentsOptions = SagaReadOptions<'getSagaContents'>;
export type ListAuthorMemoryOptions = CursorListOptions<'listAuthorMemory'>;
export type ListAuthorMaterialsOptions = CursorListOptions<'listAuthorMaterials'>;
/** An import of the author's materials: a file or a pasted text (specs/2026-11-04-saga-map.md, R9.2). */
export type AuthorMaterialsImportInput = { file: File; text?: undefined } | { file?: undefined; text: string };
/** A chapter of the author for the proofreading (specs/2026-11-07-writing-proofread.md, PR2): one file or one text. */
export type ChapterFileInput = { file: File; text?: undefined } | { file?: undefined; text: string };
export type ListChapterDraftsOptions = CursorListOptions<'listChapterDrafts'>;
export type GetAgentTimelineOptions = OperationQueryParameters<'getAgentTimeline'> & Pick<ApiRequestOptions, 'signal'>;
export type ListChatSessionsOptions = CursorListOptions<'listChatSessions'>;
export type ListProjectImagesOptions = CursorListOptions<'listProjectImages'>;
export type ListDrawingsOptions = CursorListOptions<'listDrawings'>;
export type RemoveProjectImageReferenceQuery = OperationQueryParameters<'removeProjectImageReference'>;
export type ListAdminDaysOptions = OperationQueryParameters<'listAdminDays'> & Pick<ApiRequestOptions, 'signal'>;

export interface ApiClient {
  request<TOperation extends OperationId, TStatus extends keyof OperationResponses<TOperation>>(
    operationId: TOperation,
    init?: OperationInit<TOperation>,
  ): Promise<OperationResponses<TOperation>[TStatus]>;
}

export interface CanonKeeperApiClient {
  register(body: OperationRequest<'registerUser'>): Promise<OperationResponse<'registerUser', '201'>>;
  sendEmailVerification(): Promise<OperationResponse<'sendEmailVerification', '202'>>;
  confirmEmailVerification(body: OperationRequest<'confirmEmailVerification'>): Promise<OperationResponse<'confirmEmailVerification', '200'>>;
  requestPasswordReset(body: OperationRequest<'requestPasswordReset'>): Promise<OperationResponse<'requestPasswordReset', '202'>>;
  resetPassword(body: OperationRequest<'resetPassword'>): Promise<OperationResponse<'resetPassword', '200'>>;
  changePassword(body: OperationRequest<'changePassword'>): Promise<void>;
  logoutEverywhere(): Promise<void>;
  acceptLegalDocuments(body: OperationRequest<'acceptLegalDocuments'>): Promise<void>;
  getAccountQuota(options?: ApiRequestOptions): Promise<OperationResponse<'getAccountQuota', '200'>>;
  listAdminWriters(options?: ApiRequestOptions): Promise<OperationResponse<'listAdminWriters', '200'>>;
  getAdminWriter(userId: string, options?: ApiRequestOptions): Promise<OperationResponse<'getAdminWriter', '200'>>;
  setAdminWriterQuota(userId: string, body: OperationRequest<'setAdminWriterQuota'>): Promise<OperationResponse<'setAdminWriterQuota', '200'>>;
  blockAdminWriter(userId: string): Promise<OperationResponse<'blockAdminWriter', '200'>>;
  unblockAdminWriter(userId: string): Promise<OperationResponse<'unblockAdminWriter', '200'>>;
  listAdminDays(options?: ListAdminDaysOptions): Promise<OperationResponse<'listAdminDays', '200'>>;
  getAdminServiceStatus(options?: ApiRequestOptions): Promise<OperationResponse<'getAdminServiceStatus', '200'>>;
  setAdminWriterIllustrations(userId: string, body: OperationRequest<'setAdminWriterIllustrations'>): Promise<OperationResponse<'setAdminWriterIllustrations', '200'>>;
  listAdminIllustrationRequests(options?: ApiRequestOptions): Promise<OperationResponse<'listAdminIllustrationRequests', '200'>>;
  declineAdminIllustrationRequest(requestId: string, body: OperationRequest<'declineAdminIllustrationRequest'>): Promise<OperationResponse<'declineAdminIllustrationRequest', '200'>>;
  // «Связаться с нами» (specs/2026-11-07-contacts.md).
  getContacts(options?: ApiRequestOptions): Promise<OperationResponse<'getContacts', '200'>>;
  getAdminContacts(options?: ApiRequestOptions): Promise<OperationResponse<'getAdminContacts', '200'>>;
  setAdminContacts(body: OperationRequest<'setAdminContacts'>): Promise<OperationResponse<'setAdminContacts', '200'>>;
  uploadFeedbackImage(input: FeedbackImageUploadInput, options?: FeedbackImageUploadOptions): Promise<OperationResponse<'uploadFeedbackImage', '201'>>;
  createFeedbackThread(body: OperationRequest<'createFeedbackThread'>): Promise<OperationResponse<'createFeedbackThread', '201'>>;
  listFeedbackThreads(options?: ApiRequestOptions): Promise<OperationResponse<'listFeedbackThreads', '200'>>;
  getFeedbackThread(threadId: string, options?: ApiRequestOptions): Promise<OperationResponse<'getFeedbackThread', '200'>>;
  addFeedbackMessage(threadId: string, body: OperationRequest<'addFeedbackMessage'>): Promise<OperationResponse<'addFeedbackMessage', '201'>>;
  listAdminFeedback(options?: ApiRequestOptions): Promise<OperationResponse<'listAdminFeedback', '200'>>;
  getAdminFeedbackThread(threadId: string, options?: ApiRequestOptions): Promise<OperationResponse<'getAdminFeedbackThread', '200'>>;
  replyAdminFeedback(threadId: string, body: OperationRequest<'replyAdminFeedback'>): Promise<OperationResponse<'replyAdminFeedback', '201'>>;
  setAdminFeedbackStatus(threadId: string, body: OperationRequest<'setAdminFeedbackStatus'>): Promise<OperationResponse<'setAdminFeedbackStatus', '200'>>;
  getAdminQuotaDefaults(options?: ApiRequestOptions): Promise<OperationResponse<'getAdminQuotaDefaults', '200'>>;
  setAdminQuotaDefaults(body: OperationRequest<'setAdminQuotaDefaults'>): Promise<OperationResponse<'setAdminQuotaDefaults', '200'>>;
  getAdminPromoSagas(userId: string, options?: ApiRequestOptions): Promise<OperationResponse<'getAdminPromoSagas', '200'>>;
  createAdminPromoPermission(userId: string, body: OperationRequest<'createAdminPromoPermission'>, options: RequiredIdempotencyOptions): Promise<OperationResponse<'createAdminPromoPermission', '201'>>;
  listAdminPromoPermissions(options?: ApiRequestOptions): Promise<OperationResponse<'listAdminPromoPermissions', '200'>>;
  takeAdminPromoFragment(permissionId: string, body: OperationRequest<'takeAdminPromoFragment'>): Promise<OperationResponse<'takeAdminPromoFragment', '200'>>;
  listPromoPermissions(options?: ApiRequestOptions): Promise<OperationResponse<'listPromoPermissions', '200'>>;
  answerPromoPermission(permissionId: string, body: OperationRequest<'answerPromoPermission'>): Promise<OperationResponse<'answerPromoPermission', '200'>>;
  revokePromoPermission(permissionId: string): Promise<OperationResponse<'revokePromoPermission', '200'>>;
  takeReadingTestPair(slug: string, body: OperationRequest<'takeReadingTestPair'>): Promise<OperationResponse<'takeReadingTestPair', '200'>>;
  voteReadingTestPair(slug: string, body: OperationRequest<'voteReadingTestPair'>): Promise<OperationResponse<'voteReadingTestPair', '200'>>;
  readPromoLink(body: OperationRequest<'readPromoLink'>): Promise<OperationResponse<'readPromoLink', '200'>>;
  answerPromoLink(body: OperationRequest<'answerPromoLink'>): Promise<OperationResponse<'answerPromoLink', '200'>>;
  revokePromoLink(body: OperationRequest<'revokePromoLink'>): Promise<OperationResponse<'revokePromoLink', '200'>>;
  getIllustrationAccess(options?: ApiRequestOptions): Promise<OperationResponse<'getIllustrationAccess', '200'>>;
  createIllustrationAccessRequest(body: OperationRequest<'createIllustrationAccessRequest'>): Promise<OperationResponse<'createIllustrationAccessRequest', '201'>>;
  login(body: OperationRequest<'loginUser'>): Promise<OperationResponse<'loginUser', '200'>>;
  logout(): Promise<void>;
  reportClientError(body: OperationRequest<'reportClientError'>): Promise<void>;
  rotateSession(): Promise<OperationResponse<'rotateSession', '200'>>;
  startImpersonation(body: OperationRequest<'startImpersonation'>): Promise<OperationResponse<'startImpersonation', '200'>>;
  stopImpersonation(): Promise<OperationResponse<'stopImpersonation', '200'>>;
  getCurrentUser(options?: ApiRequestOptions): Promise<OperationResponse<'getCurrentUser', '200'>>;
  listNotes(options?: NotesListOptions): Promise<OperationResponse<'listNotes', '200'>>;
  createPersonalNote(body: OperationRequest<'createPersonalNote'>, options: RequiredIdempotencyOptions): Promise<PersonalNote>;
  getPersonalNote(noteId: string, options?: ApiRequestOptions): Promise<PersonalNote>;
  updatePersonalNote(noteId: string, body: OperationRequest<'updatePersonalNote'>): Promise<PersonalNote>;
  deletePersonalNote(noteId: string, expectedRevision: number): Promise<void>;
  resolvePersonalNoteSource(noteId: string, options?: ApiRequestOptions): Promise<OperationResponse<'resolvePersonalNoteSource', '200'>>;
  listProjects(options: DeletedSourceListOptions<'listProjects'>): Promise<Schema<'DeletedProjectList'>>;
  listProjects(options?: ActiveSourceListOptions<'listProjects'>): Promise<Schema<'ProjectList'>>;
  createProject(body: OperationRequest<'createProject'>): Promise<OperationResponse<'createProject', '201'>>;
  getProject(projectId: string): Promise<OperationResponse<'getProject', '200'>>;
  getProjectSupportSnapshot(projectId: string): Promise<OperationResponse<'getProjectSupportSnapshot', '200'>>;
  updateProject(projectId: string, body: OperationRequest<'updateProject'>): Promise<OperationResponse<'updateProject', '200'>>;
  deleteProject(projectId: string): Promise<void>;
  restoreProject(projectId: string): Promise<OperationResponse<'restoreProject', '200'>>;
  listProjectMembers(projectId: string, options?: CursorListOptions<'listProjectMembers'>): Promise<OperationResponse<'listProjectMembers', '200'>>;
  updateProjectMemberRole(projectId: string, memberId: string, body: OperationRequest<'updateProjectMemberRole'>): Promise<OperationResponse<'updateProjectMemberRole', '200'>>;
  removeProjectMember(projectId: string, memberId: string): Promise<void>;
  listProjectInvitations(projectId: string, options?: CursorListOptions<'listProjectInvitations'>): Promise<OperationResponse<'listProjectInvitations', '200'>>;
  listMyProjectInvitations(options?: CursorListOptions<'listMyProjectInvitations'>): Promise<OperationResponse<'listMyProjectInvitations', '200'>>;
  createProjectInvitation(projectId: string, body: OperationRequest<'createProjectInvitation'>): Promise<OperationResponse<'createProjectInvitation', '201'>>;
  cancelProjectInvitation(invitationId: string): Promise<OperationResponse<'cancelProjectInvitation', '200'>>;
  acceptProjectInvitation(invitationId: string): Promise<OperationResponse<'acceptProjectInvitation', '200'>>;
  listBooks(projectId: string, options: DeletedSourceListOptions<'listBooks'>): Promise<Schema<'DeletedBookList'>>;
  listBooks(projectId: string, options?: ActiveSourceListOptions<'listBooks'>): Promise<Schema<'BookList'>>;
  createBook(projectId: string, body: OperationRequest<'createBook'>): Promise<OperationResponse<'createBook', '201'>>;
  getBook(bookId: string): Promise<OperationResponse<'getBook', '200'>>;
  updateBook(bookId: string, body: OperationRequest<'updateBook'>): Promise<OperationResponse<'updateBook', '200'>>;
  deleteBook(bookId: string): Promise<void>;
  restoreBook(bookId: string): Promise<OperationResponse<'restoreBook', '200'>>;
  uploadBookCover(bookId: string, file: File, options?: BookCoverUploadOptions): Promise<OperationResponse<'uploadBookCover', '200'>>;
  deleteBookCover(bookId: string): Promise<OperationResponse<'deleteBookCover', '200'>>;
  getBookExport(bookId: string, format: BookExportFormat): Promise<DownloadedFile>;
  setBookCoverFromImage(bookId: string, imageNumber: number): Promise<OperationResponse<'setBookCoverFromImage', '200'>>;
  getImportConstraints(projectId: string): Promise<OperationResponse<'getImportConstraints', '200'>>;
  createImportQueue(projectId: string, body: OperationRequest<'createImportQueue'>, options: RequiredIdempotencyOptions): Promise<OperationResponse<'createImportQueue', '201'>>;
  listImportQueues(projectId: string, options?: Pick<ApiRequestOptions, 'signal'>): Promise<OperationResponse<'listImportQueues', '200'>>;
  getImportQueue(projectId: string, queueId: string, options?: Pick<ApiRequestOptions, 'signal'>): Promise<OperationResponse<'getImportQueue', '200'>>;
  uploadImportQueueItem(projectId: string, queueId: string, input: ImportQueueItemUploadInput, options: ImportBookRequestOptions): Promise<OperationResponse<'uploadImportQueueItem', '200'>>;
  updateImportQueue(projectId: string, queueId: string, body: OperationRequest<'updateImportQueue'>): Promise<OperationResponse<'updateImportQueue', '200'>>;
  startImportQueue(projectId: string, queueId: string, body: OperationRequest<'startImportQueue'>, options: RequiredIdempotencyOptions): Promise<OperationResponse<'startImportQueue', '202'>>;
  cancelImportQueue(projectId: string, queueId: string): Promise<OperationResponse<'cancelImportQueue', '200'>>;
  importBookFile(projectId: string, input: ImportBookInput, options: ImportBookRequestOptions): Promise<OperationResponse<'importBookFile', '202'>>;
  listProjectJobs(projectId: string, options?: ListProjectJobsOptions): Promise<OperationResponse<'listProjectJobs', '200'>>;
  getIndexingStatus(projectId: string, options?: GetIndexingStatusOptions): Promise<OperationResponse<'getIndexingStatus', '200'>>;
  listSagaGlossary(projectId: string, options: ListSagaGlossaryOptions): Promise<OperationResponse<'listSagaGlossary', '200'>>;
  getSagaMap(projectId: string, options: GetSagaMapOptions): Promise<OperationResponse<'getSagaMap', '200'>>;
  getSagaMapNode(projectId: string, options: GetSagaMapNodeOptions): Promise<OperationResponse<'getSagaMapNode', '200'>>;
  searchSagaMap(projectId: string, options: SearchSagaMapOptions): Promise<OperationResponse<'searchSagaMap', '200'>>;
  listSagaMapHidden(projectId: string, options: ListSagaMapHiddenOptions): Promise<OperationResponse<'listSagaMapHidden', '200'>>;
  hideSagaMapItem(projectId: string, sourceView: SourceView, body: OperationRequest<'hideSagaMapItem'>): Promise<OperationResponse<'hideSagaMapItem', '201'>>;
  restoreSagaMapItem(projectId: string, hiddenId: string): Promise<void>;
  getSagaContents(projectId: string, options: GetSagaContentsOptions): Promise<OperationResponse<'getSagaContents', '200'>>;
  listAuthorMaterials(projectId: string, options?: ListAuthorMaterialsOptions): Promise<OperationResponse<'listAuthorMaterials', '200'>>;
  updateAuthorMaterial(projectId: string, materialId: string, body: OperationRequest<'updateAuthorMaterial'>): Promise<OperationResponse<'updateAuthorMaterial', '200'>>;
  deleteAuthorMaterial(projectId: string, materialId: string, expectedRevision: number): Promise<void>;
  startAuthorMaterialsImport(projectId: string, input: AuthorMaterialsImportInput, options: RequiredIdempotencyOptions): Promise<OperationResponse<'startAuthorMaterialsImport', '202'>>;
  getAuthorMaterialsImport(projectId: string, options?: { signal?: AbortSignal }): Promise<OperationResponse<'getAuthorMaterialsImport', '200'>>;
  confirmAuthorMaterialsImport(projectId: string, importId: string, body: OperationRequest<'confirmAuthorMaterialsImport'>, options: RequiredIdempotencyOptions): Promise<OperationResponse<'confirmAuthorMaterialsImport', '200'>>;
  discardAuthorMaterialsImport(projectId: string, importId: string): Promise<void>;
  listAuthorMemory(projectId: string, options?: ListAuthorMemoryOptions): Promise<OperationResponse<'listAuthorMemory', '200'>>;
  createAuthorMemory(projectId: string, body: OperationRequest<'createAuthorMemory'>, options: RequiredIdempotencyOptions): Promise<OperationResponse<'createAuthorMemory', '201'>>;
  updateAuthorMemory(projectId: string, recordId: string, body: OperationRequest<'updateAuthorMemory'>): Promise<OperationResponse<'updateAuthorMemory', '200'>>;
  deleteAuthorMemory(projectId: string, recordId: string, expectedRevision: number): Promise<void>;
  listChapterDrafts(projectId: string, options?: ListChapterDraftsOptions): Promise<OperationResponse<'listChapterDrafts', '200'>>;
  createChapterDraft(projectId: string, body: OperationRequest<'createChapterDraft'>, options: RequiredIdempotencyOptions): Promise<OperationResponse<'createChapterDraft', '201'>>;
  getChapterDraft(projectId: string, draftId: string, options?: Pick<ApiRequestOptions, 'signal'>): Promise<OperationResponse<'getChapterDraft', '200'>>;
  getChapterDraftPlan(projectId: string, draftId: string, options?: { number?: number } & Pick<ApiRequestOptions, 'signal'>): Promise<OperationResponse<'getChapterDraftPlan', '200'>>;
  getChapterDraftMoment(projectId: string, draftId: string, options?: Pick<ApiRequestOptions, 'signal'>): Promise<OperationResponse<'getChapterDraftMoment', '200'>>;
  updateChapterDraftPlan(projectId: string, draftId: string, body: OperationRequest<'updateChapterDraftPlan'>): Promise<OperationResponse<'updateChapterDraftPlan', '200'>>;
  updateChapterDraftMoment(projectId: string, draftId: string, body: OperationRequest<'updateChapterDraftMoment'>): Promise<OperationResponse<'updateChapterDraftMoment', '200'>>;
  listChapterDraftVersions(projectId: string, draftId: string, options?: Pick<ApiRequestOptions, 'signal'>): Promise<OperationResponse<'listChapterDraftVersions', '200'>>;
  getChapterDraftVersion(projectId: string, draftId: string, number: number, options?: Pick<ApiRequestOptions, 'signal'>): Promise<OperationResponse<'getChapterDraftVersion', '200'>>;
  takeChapterDraftVersion(projectId: string, draftId: string, number: number, body: OperationRequest<'takeChapterDraftVersion'>): Promise<OperationResponse<'takeChapterDraftVersion', '200'>>;
  updateChapterDraft(projectId: string, draftId: string, body: OperationRequest<'updateChapterDraft'>): Promise<OperationResponse<'updateChapterDraft', '200'>>;
  deleteChapterDraft(projectId: string, draftId: string, expectedRevision: number): Promise<void>;
  startChapterDraftJob(projectId: string, draftId: string, body: OperationRequest<'startChapterDraftJob'>, options: RequiredIdempotencyOptions): Promise<OperationResponse<'startChapterDraftJob', '201'>>;
  addChapterDraftToBook(projectId: string, draftId: string, body: OperationRequest<'addChapterDraftToBook'>): Promise<OperationResponse<'addChapterDraftToBook', '200'>>;
  /** «Править главу», «Заменить главу в книге», «Вернуть прежний текст» (specs/2026-11-05-voice-full-access.md A8.7). */
  openChapterEdit(projectId: string, chapterId: string): Promise<OperationResponse<'openChapterEdit', '200'>>;
  replaceChapterFromDraft(projectId: string, draftId: string, body: OperationRequest<'replaceChapterFromDraft'>): Promise<OperationResponse<'replaceChapterFromDraft', '200'>>;
  revertChapterFromDraft(projectId: string, draftId: string, body: OperationRequest<'revertChapterFromDraft'>): Promise<OperationResponse<'revertChapterFromDraft', '200'>>;
  reopenChapterFromBook(projectId: string, draftId: string, body: OperationRequest<'reopenChapterFromBook'>): Promise<OperationResponse<'reopenChapterFromBook', '200'>>;
  applyChapterDraftNote(projectId: string, draftId: string, noteId: string, body: OperationRequest<'applyChapterDraftNote'>): Promise<OperationResponse<'applyChapterDraftNote', '200'>>;
  updateChapterDraftNote(projectId: string, draftId: string, noteId: string, body: OperationRequest<'updateChapterDraftNote'>): Promise<OperationResponse<'updateChapterDraftNote', '200'>>;
  submitDraftTurn(projectId: string, draftId: string, body: OperationRequest<'submitDraftTurn'>, options: RequiredIdempotencyOptions): Promise<OperationResponse<'submitDraftTurn', '202'>>;
  /** The proofreading of a chapter of the author (specs/2026-11-07-writing-proofread.md, PR2, PR4.4, PR6). */
  startChapterProofread(projectId: string, input: ChapterFileInput & { title?: string; bookId?: string; afterChapterId?: string }, options: RequiredIdempotencyOptions): Promise<OperationResponse<'startChapterProofread', '201'>>;
  loadChapterDraftFile(projectId: string, draftId: string, input: ChapterFileInput & { expectedRevision: number }, options: RequiredIdempotencyOptions): Promise<OperationResponse<'loadChapterDraftFile', '200'>>;
  applyChapterDraftNotes(projectId: string, draftId: string, body: OperationRequest<'applyChapterDraftNotes'>): Promise<OperationResponse<'applyChapterDraftNotes', '200'>>;
  getChapterDraftExport(projectId: string, draftId: string): Promise<DownloadedFile>;
  getProjectVoice(projectId: string, options?: Pick<ApiRequestOptions, 'signal'>): Promise<OperationResponse<'getProjectVoice', '200'>>;
  /** The voice of the co-author for the user's next conversations (specs/2026-11-03-voice-assistant.md V1.9). */
  setAccountVoice(body: OperationRequest<'setAccountVoice'>): Promise<OperationResponse<'setAccountVoice', '200'>>;
  /** The proof of this client's writes for the start of a voice call (specs/2026-11-03-voice-assistant.md V0.2). */
  voiceCallToken(): Promise<string>;
  listProjectImages(projectId: string, options?: ListProjectImagesOptions): Promise<OperationResponse<'listProjectImages', '200'>>;
  getProjectImage(projectId: string, number: number, options?: Pick<ApiRequestOptions, 'signal'>): Promise<OperationResponse<'getProjectImage', '200'>>;
  setProjectImageReference(projectId: string, number: number, body: OperationRequest<'setProjectImageReference'>): Promise<OperationResponse<'setProjectImageReference', '200'>>;
  updateProjectImageDetails(projectId: string, number: number, body: OperationRequest<'updateProjectImageDetails'>): Promise<OperationResponse<'updateProjectImageDetails', '200'>>;
  removeProjectImageReference(projectId: string, number: number, query: RemoveProjectImageReferenceQuery): Promise<OperationResponse<'removeProjectImageReference', '200'>>;
  deleteProjectImage(projectId: string, number: number): Promise<void>;
  deleteProjectImages(projectId: string, body: OperationRequest<'deleteProjectImages'>, options: RequiredIdempotencyOptions): Promise<OperationResponse<'deleteProjectImages', '200'>>;
  getProjectIllustrationStyle(projectId: string, options?: Pick<ApiRequestOptions, 'signal'>): Promise<OperationResponse<'getProjectIllustrationStyle', '200'>>;
  updateProjectIllustrationStyle(projectId: string, body: OperationRequest<'updateProjectIllustrationStyle'>): Promise<OperationResponse<'updateProjectIllustrationStyle', '200'>>;
  cutoutProjectImage(projectId: string, number: number, options: RequiredIdempotencyOptions): Promise<OperationResponse<'cutoutProjectImage', '201'>>;
  createProjectImageVariant(projectId: string, number: number, body: OperationRequest<'createProjectImageVariant'>, options: RequiredIdempotencyOptions): Promise<OperationResponse<'createProjectImageVariant', '201'>>;
  chooseProjectImageVariant(projectId: string, number: number, options: RequiredIdempotencyOptions): Promise<OperationResponse<'chooseProjectImageVariant', '200'>>;
  chooseProjectImageLayerVariant(projectId: string, number: number, layerId: string, options: RequiredIdempotencyOptions): Promise<OperationResponse<'chooseProjectImageLayerVariant', '200'>>;
  listDrawings(projectId: string, options?: ListDrawingsOptions): Promise<OperationResponse<'listDrawings', '200'>>;
  createDrawing(projectId: string, body: OperationRequest<'createDrawing'>, options: RequiredIdempotencyOptions): Promise<OperationResponse<'createDrawing', '201'>>;
  getDrawing(projectId: string, drawingId: string, options?: Pick<ApiRequestOptions, 'signal'>): Promise<OperationResponse<'getDrawing', '200'>>;
  updateDrawing(projectId: string, drawingId: string, body: OperationRequest<'updateDrawing'>): Promise<OperationResponse<'updateDrawing', '200'>>;
  deleteDrawing(projectId: string, drawingId: string): Promise<void>;
  drawDrawing(projectId: string, drawingId: string, body: OperationRequest<'drawDrawing'>, options: RequiredIdempotencyOptions): Promise<OperationResponse<'drawDrawing', '201'>>;
  submitDrawingTurn(projectId: string, drawingId: string, body: OperationRequest<'submitDrawingTurn'>, options: RequiredIdempotencyOptions): Promise<OperationResponse<'submitDrawingTurn', '202'>>;
  enlargeProjectImage(projectId: string, number: number, options: RequiredIdempotencyOptions): Promise<OperationResponse<'enlargeProjectImage', '201'>>;
  createProjectImageLayer(projectId: string, number: number, body: OperationRequest<'createProjectImageLayer'>, options: RequiredIdempotencyOptions): Promise<OperationResponse<'createProjectImageLayer', '201'>>;
  updateProjectImageLayer(projectId: string, number: number, layerId: string, body: OperationRequest<'updateProjectImageLayer'>): Promise<OperationResponse<'updateProjectImageLayer', '200'>>;
  deleteProjectImageLayer(projectId: string, number: number, layerId: string): Promise<OperationResponse<'deleteProjectImageLayer', '200'>>;
  createProjectCharacterSheet(projectId: string, body: OperationRequest<'createProjectCharacterSheet'>, options: RequiredIdempotencyOptions): Promise<OperationResponse<'createProjectCharacterSheet', '201'>>;
  createIllustrationStyleSketches(projectId: string, body: OperationRequest<'createIllustrationStyleSketches'>, options: RequiredIdempotencyOptions): Promise<OperationResponse<'createIllustrationStyleSketches', '201'>>;
  uploadProjectImage(projectId: string, input: ImageUploadInput, options: ImportBookRequestOptions): Promise<OperationResponse<'uploadProjectImage', '201'>>;
  startProjectIndexing(projectId: string, body: OperationRequest<'startProjectIndexing'>, options: RequiredIdempotencyOptions): Promise<OperationResponse<'startProjectIndexing', '202'>>;
  getJob(jobId: string): Promise<OperationResponse<'getJob', '200'>>;
  getAgentTimeline(projectId: string, jobId: string, options?: GetAgentTimelineOptions): Promise<OperationResponse<'getAgentTimeline', '200'>>;
  cancelJob(jobId: string): Promise<OperationResponse<'cancelJob', '200' | '204'>>;
  listChapters(bookId: string, options: DeletedSourceListOptions<'listChapters'>): Promise<Schema<'DeletedChapterList'>>;
  listChapters(bookId: string, options?: ActiveSourceListOptions<'listChapters'>): Promise<Schema<'ChapterList'>>;
  getChapter(chapterId: string, contentVariant: SourceView): Promise<OperationResponse<'getChapter', '200'>>;
  deleteChapter(chapterId: string): Promise<void>;
  restoreChapter(chapterId: string): Promise<OperationResponse<'restoreChapter', '200'>>;
  searchProject(projectId: string, query: string, sourceView: SourceView, scope?: SearchScope, options?: SearchProjectOptions): Promise<OperationResponse<'searchProject', '200'>>;
  searchChapters(body: OperationRequest<'searchChapters'>, options?: Pick<ApiRequestOptions, 'signal'>): Promise<OperationResponse<'searchChapters', '200'>>;
  listChatSessions(projectId: string, options: ListChatSessionsOptions): Promise<OperationResponse<'listChatSessions', '200'>>;
  createChatSession(projectId: string, body: OperationRequest<'createChatSession'>, options: RequiredIdempotencyOptions): Promise<OperationResponse<'createChatSession', '201'>>;
  getChatSession(chatId: string): Promise<OperationResponse<'getChatSession', '200'>>;
  resolveChatChapterKey(chatId: string, chapterKey: string, options?: Pick<ApiRequestOptions, 'signal'>): Promise<ChatChapterLocation>;
  renameChatSession(chatId: string, body: OperationRequest<'updateChatSession'>): Promise<OperationResponse<'updateChatSession', '200'>>;
  deleteChatSession(chatId: string): Promise<void>;
  createChatTurn(chatId: string, body: OperationRequest<'createChatTurn'>, options: RequiredIdempotencyOptions): Promise<OperationResponse<'createChatTurn', '202'>>;
  steerChatTurn(chatId: string, turnId: string, body: OperationRequest<'steerChatTurn'>, options: RequiredIdempotencyOptions): Promise<OperationResponse<'steerChatTurn', '202'>>;
  /** A question with the author's chapter attached (specs/2026-11-07-chat-attach-chapter.md CA2). */
  createChatTurnWithChapter(chatId: string, input: ChapterFileInput & { body: string }, options: RequiredIdempotencyOptions): Promise<OperationResponse<'createChatTurnWithChapter', '202'>>;
  /** The chapter attached to a question of the chat, as parsed (CA4.3). */
  getChatAttachment(chatId: string, number: number, options?: Pick<ApiRequestOptions, 'signal'>): Promise<ChatAttachment>;
}
