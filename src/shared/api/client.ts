import type {
  ActiveSourceListOptions,
  ApiClient,
  ApiRequestOptions,
  BookCoverUploadOptions,
  BookExportFormat,
  BookExportOptions,
  CanonKeeperApiClient,
  CursorListOptions,
  DeletedSourceListOptions,
  FeedbackImageUploadInput,
  FeedbackImageUploadOptions,
  GetIndexingStatusOptions,
  GetAgentTimelineOptions,
  GetSagaContentsOptions,
  GetSagaMapNodeOptions,
  SearchSagaMapOptions,
  GetSagaFirstQuestionsOptions,
  GetSagaMapOptions,
  ListSagaMapHiddenOptions,
  ImportBookInput,
  ImageUploadInput,
  ImportBookRequestOptions,
  ImportQueueItemUploadInput,
  ListAdminDaysOptions,
  ListChatSessionsOptions,
  AuthorMaterialsImportInput,
  ChapterFileInput,
  ProofreadChapterInput,
  ListAuthorMaterialsOptions,
  ListAuthorMemoryOptions,
  ListChapterDraftsOptions,
  ListDrawingsOptions,
  GetDrawingsFacetsOptions,
  ListProjectImagesOptions,
  ListProjectJobsOptions,
  ListSagaGlossaryOptions,
  NotesListOptions,
  OperationRequest,
  OperationResponse,
  RemoveProjectImageReferenceQuery,
  RequiredIdempotencyOptions,
  Schema,
  SearchProjectOptions,
  SearchScope,
  SourceView,
} from './contracts';
import {
  NetworkApiError,
  OpenApiClientTransport,
  idempotencyHeaders,
  splitListOptions,
  type CanonKeeperApiClientOptions,
} from './transport';

// The media type of each kind of a book file (specs/2026-11-10-book-file-kinds.md K1.2): browsers name a text, an RTF
// or a packed FB2 differently, so a file goes with the type of the end of its name, the longest first.
const canonicalImportMimeTypes: Record<string, string> = {
  '.epub': 'application/epub+zip',
  '.fb2': 'application/x-fictionbook+xml',
  '.docx': 'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
  '.txt': 'text/plain',
  '.md': 'text/markdown',
  '.rtf': 'application/rtf',
  '.odt': 'application/vnd.oasis.opendocument.text',
  '.fb2.zip': 'application/zip',
};

function canonicalImportFile(file: File) {
  const name = file.name.trim().toLowerCase();
  const kind = Object.keys(canonicalImportMimeTypes)
    .filter((suffix) => name.endsWith(suffix))
    .sort((left, right) => right.length - left.length)[0];
  return kind ? new File([file], file.name, { type: canonicalImportMimeTypes[kind] }) : file;
}

// Методы клиента отражают операции контракта и делегируют всю сетевую работу транспорту.
export class OpenApiCanonKeeperClient extends OpenApiClientTransport implements CanonKeeperApiClient, ApiClient {
  register(body: OperationRequest<'registerUser'>) {
    return this.request<'registerUser', '201'>('registerUser', { body });
  }

  sendEmailVerification() {
    return this.request<'sendEmailVerification', '202'>('sendEmailVerification');
  }

  confirmEmailVerification(body: OperationRequest<'confirmEmailVerification'>) {
    return this.request<'confirmEmailVerification', '200'>('confirmEmailVerification', { body });
  }

  requestPasswordReset(body: OperationRequest<'requestPasswordReset'>) {
    return this.request<'requestPasswordReset', '202'>('requestPasswordReset', { body });
  }

  resetPassword(body: OperationRequest<'resetPassword'>) {
    return this.request<'resetPassword', '200'>('resetPassword', { body });
  }

  changeUnverifiedEmail(body: OperationRequest<'changeUnverifiedEmail'>) {
    return this.request<'changeUnverifiedEmail', '200'>('changeUnverifiedEmail', { body });
  }

  checkPasswordResetLink(body: OperationRequest<'checkPasswordResetLink'>) {
    return this.request<'checkPasswordResetLink', '200'>('checkPasswordResetLink', { body });
  }

  async changePassword(body: OperationRequest<'changePassword'>) {
    await this.request<'changePassword', '204'>('changePassword', { body });
  }

  async logoutEverywhere() {
    await this.request<'logoutEverywhere', '204'>('logoutEverywhere');
  }

  setAccountName(body: OperationRequest<'setAccountName'>) {
    return this.request<'setAccountName', '200'>('setAccountName', { body });
  }

  getAccountSessions(options: ApiRequestOptions = {}) {
    return this.request<'getAccountSessions', '200'>('getAccountSessions', options);
  }

  logoutOtherSessions() {
    return this.request<'logoutOtherSessions', '200'>('logoutOtherSessions');
  }

  async acceptLegalDocuments(body: OperationRequest<'acceptLegalDocuments'>) {
    await this.request<'acceptLegalDocuments', '204'>('acceptLegalDocuments', { body });
  }

  getAccountQuota(options: ApiRequestOptions = {}) {
    return this.request<'getAccountQuota', '200'>('getAccountQuota', options);
  }

  listRecentlyDeleted() {
    return this.request<'listRecentlyDeleted', '200'>('listRecentlyDeleted');
  }

  listAdminWriters(options: ApiRequestOptions = {}) {
    return this.request<'listAdminWriters', '200'>('listAdminWriters', { signal: options.signal });
  }

  getAdminWriter(userId: string, options: ApiRequestOptions = {}) {
    return this.request<'getAdminWriter', '200'>('getAdminWriter', { params: { path: { userId } }, signal: options.signal });
  }

  setAdminWriterQuota(userId: string, body: OperationRequest<'setAdminWriterQuota'>) {
    return this.request<'setAdminWriterQuota', '200'>('setAdminWriterQuota', { params: { path: { userId } }, body });
  }

  blockAdminWriter(userId: string) {
    return this.request<'blockAdminWriter', '200'>('blockAdminWriter', { params: { path: { userId } } });
  }

  unblockAdminWriter(userId: string) {
    return this.request<'unblockAdminWriter', '200'>('unblockAdminWriter', { params: { path: { userId } } });
  }

  listAdminDays(options: ListAdminDaysOptions = {}) {
    const { signal, ...query } = options;
    return this.request<'listAdminDays', '200'>('listAdminDays', { params: { query }, signal });
  }

  getAdminServiceStatus(options: ApiRequestOptions = {}) {
    return this.request<'getAdminServiceStatus', '200'>('getAdminServiceStatus', { signal: options.signal });
  }
  setAdminWriterIllustrations(userId: string, body: OperationRequest<'setAdminWriterIllustrations'>) {
    return this.request<'setAdminWriterIllustrations', '200'>('setAdminWriterIllustrations', { params: { path: { userId } }, body });
  }

  listSubscriptionTiers(options: ApiRequestOptions = {}) {
    return this.request<'listSubscriptionTiers', '200'>('listSubscriptionTiers', { signal: options.signal });
  }

  getAccountPlan(options: ApiRequestOptions = {}) {
    return this.request<'getAccountPlan', '200'>('getAccountPlan', { signal: options.signal });
  }

  getAccountLetters(options: ApiRequestOptions = {}) {
    return this.request<'getAccountLetters', '200'>('getAccountLetters', { signal: options.signal });
  }

  setAccountLetters(body: OperationRequest<'setAccountLetters'>) {
    return this.request<'setAccountLetters', '200'>('setAccountLetters', { body });
  }

  listAdminSubscriptionTiers(options: ApiRequestOptions = {}) {
    return this.request<'listAdminSubscriptionTiers', '200'>('listAdminSubscriptionTiers', { signal: options.signal });
  }

  setAdminSubscriptionTier(tier: Schema<'SubscriptionTierCode'>, body: OperationRequest<'setAdminSubscriptionTier'>) {
    return this.request<'setAdminSubscriptionTier', '200'>('setAdminSubscriptionTier', { params: { path: { tier } }, body });
  }

  grantAdminWriterTier(userId: string, body: OperationRequest<'grantAdminWriterTier'>) {
    return this.request<'grantAdminWriterTier', '201'>('grantAdminWriterTier', { params: { path: { userId } }, body });
  }

  endAdminWriterPeriod(userId: string, periodId: string) {
    return this.request<'endAdminWriterPeriod', '200'>('endAdminWriterPeriod', { params: { path: { userId, periodId } } });
  }

  checkSubscriptionKey(body: OperationRequest<'checkSubscriptionKey'>) {
    return this.request<'checkSubscriptionKey', '200'>('checkSubscriptionKey', { body });
  }

  redeemSubscriptionKey(body: OperationRequest<'redeemSubscriptionKey'>) {
    return this.request<'redeemSubscriptionKey', '200'>('redeemSubscriptionKey', { body });
  }

  listAdminKeyBatches(options: ApiRequestOptions = {}) {
    return this.request<'listAdminKeyBatches', '200'>('listAdminKeyBatches', { signal: options.signal });
  }

  createAdminKeyBatch(body: OperationRequest<'createAdminKeyBatch'>) {
    return this.requestFile('createAdminKeyBatch', undefined, `canonkeeper-${body.tier}-${body.count}.txt`, body);
  }

  revokeAdminKeyBatch(batchId: string) {
    return this.request<'revokeAdminKeyBatch', '200'>('revokeAdminKeyBatch', { params: { path: { batchId } } });
  }

  resetAdminWriterTrials(userId: string, body: OperationRequest<'resetAdminWriterTrials'>) {
    return this.request<'resetAdminWriterTrials', '200'>('resetAdminWriterTrials', { params: { path: { userId } }, body });
  }

  getContacts(options: ApiRequestOptions = {}) {
    return this.request<'getContacts', '200'>('getContacts', { signal: options.signal });
  }

  getAdminContacts(options: ApiRequestOptions = {}) {
    return this.request<'getAdminContacts', '200'>('getAdminContacts', { signal: options.signal });
  }

  setAdminContacts(body: OperationRequest<'setAdminContacts'>) {
    return this.request<'setAdminContacts', '200'>('setAdminContacts', { body });
  }

  uploadFeedbackImage(input: FeedbackImageUploadInput, options: FeedbackImageUploadOptions = {}) {
    const form = new FormData();
    form.append('draftKey', input.draftKey);
    form.append('file', input.file, input.file.name || 'image');
    return this.postMultipart<'uploadFeedbackImage', OperationResponse<'uploadFeedbackImage', '201'>>('uploadFeedbackImage', {}, form, options);
  }

  createFeedbackThread(body: OperationRequest<'createFeedbackThread'>) {
    return this.request<'createFeedbackThread', '201'>('createFeedbackThread', { body });
  }

  listFeedbackThreads(options: ApiRequestOptions = {}) {
    return this.request<'listFeedbackThreads', '200'>('listFeedbackThreads', { signal: options.signal });
  }

  getFeedbackThread(threadId: string, options: ApiRequestOptions = {}) {
    return this.request<'getFeedbackThread', '200'>('getFeedbackThread', { params: { path: { threadId } }, signal: options.signal });
  }

  addFeedbackMessage(threadId: string, body: OperationRequest<'addFeedbackMessage'>) {
    return this.request<'addFeedbackMessage', '201'>('addFeedbackMessage', { params: { path: { threadId } }, body });
  }

  listAdminFeedback(options: ApiRequestOptions = {}) {
    return this.request<'listAdminFeedback', '200'>('listAdminFeedback', { signal: options.signal });
  }

  getAdminFeedbackThread(threadId: string, options: ApiRequestOptions = {}) {
    return this.request<'getAdminFeedbackThread', '200'>('getAdminFeedbackThread', { params: { path: { threadId } }, signal: options.signal });
  }

  replyAdminFeedback(threadId: string, body: OperationRequest<'replyAdminFeedback'>) {
    return this.request<'replyAdminFeedback', '201'>('replyAdminFeedback', { params: { path: { threadId } }, body });
  }

  setAdminFeedbackStatus(threadId: string, body: OperationRequest<'setAdminFeedbackStatus'>) {
    return this.request<'setAdminFeedbackStatus', '200'>('setAdminFeedbackStatus', { params: { path: { threadId } }, body });
  }

  getAdminQuotaDefaults(options: ApiRequestOptions = {}) {
    return this.request<'getAdminQuotaDefaults', '200'>('getAdminQuotaDefaults', { signal: options.signal });
  }

  setAdminQuotaDefaults(body: OperationRequest<'setAdminQuotaDefaults'>) {
    return this.request<'setAdminQuotaDefaults', '200'>('setAdminQuotaDefaults', { body });
  }

  getAdminPromoSagas(userId: string, options: ApiRequestOptions = {}) {
    return this.request<'getAdminPromoSagas', '200'>('getAdminPromoSagas', { params: { path: { userId } }, signal: options.signal });
  }

  createAdminPromoPermission(userId: string, body: OperationRequest<'createAdminPromoPermission'>, options: RequiredIdempotencyOptions) {
    return this.request<'createAdminPromoPermission', '201'>('createAdminPromoPermission', {
      params: { path: { userId } }, body, headers: idempotencyHeaders(options, 'createAdminPromoPermission'), signal: options.signal,
    });
  }

  listAdminPromoPermissions(options: ApiRequestOptions = {}) {
    return this.request<'listAdminPromoPermissions', '200'>('listAdminPromoPermissions', { signal: options.signal });
  }

  takeAdminPromoFragment(permissionId: string, body: OperationRequest<'takeAdminPromoFragment'>) {
    return this.request<'takeAdminPromoFragment', '200'>('takeAdminPromoFragment', { params: { path: { permissionId } }, body });
  }

  listPromoPermissions(options: ApiRequestOptions = {}) {
    return this.request<'listPromoPermissions', '200'>('listPromoPermissions', { signal: options.signal });
  }

  answerPromoPermission(permissionId: string, body: OperationRequest<'answerPromoPermission'>) {
    return this.request<'answerPromoPermission', '200'>('answerPromoPermission', { params: { path: { permissionId } }, body });
  }

  revokePromoPermission(permissionId: string) {
    return this.request<'revokePromoPermission', '200'>('revokePromoPermission', { params: { path: { permissionId } } });
  }

  takeReadingTestPair(slug: string, body: OperationRequest<'takeReadingTestPair'>) {
    return this.request<'takeReadingTestPair', '200'>('takeReadingTestPair', { params: { path: { slug } }, body });
  }

  voteReadingTestPair(slug: string, body: OperationRequest<'voteReadingTestPair'>) {
    return this.request<'voteReadingTestPair', '200'>('voteReadingTestPair', { params: { path: { slug } }, body });
  }

  readPromoLink(body: OperationRequest<'readPromoLink'>) {
    return this.request<'readPromoLink', '200'>('readPromoLink', { body });
  }

  answerPromoLink(body: OperationRequest<'answerPromoLink'>) {
    return this.request<'answerPromoLink', '200'>('answerPromoLink', { body });
  }

  revokePromoLink(body: OperationRequest<'revokePromoLink'>) {
    return this.request<'revokePromoLink', '200'>('revokePromoLink', { body });
  }

  getIllustrationAccess(options: ApiRequestOptions = {}) {
    return this.request<'getIllustrationAccess', '200'>('getIllustrationAccess', { signal: options.signal });
  }

  login(body: OperationRequest<'loginUser'>) {
    return this.request<'loginUser', '200'>('loginUser', { body });
  }

  async logout() {
    await this.request<'logoutUser', '204'>('logoutUser');
  }

  async reportClientError(body: OperationRequest<'reportClientError'>) {
    await this.request<'reportClientError', '204'>('reportClientError', { body });
  }

  rotateSession() {
    return this.request<'rotateSession', '200'>('rotateSession');
  }

  startImpersonation(body: OperationRequest<'startImpersonation'>) {
    return this.request<'startImpersonation', '200'>('startImpersonation', { body });
  }

  stopImpersonation() {
    return this.request<'stopImpersonation', '200'>('stopImpersonation');
  }

  getCurrentUser(options: ApiRequestOptions = {}) {
    return this.request<'getCurrentUser', '200'>('getCurrentUser', options);
  }

  listNotes(options: NotesListOptions = {}) {
    const { signal, ...body } = options;
    return this.request<'listNotes', '200'>('listNotes', { body, signal });
  }

  createPersonalNote(body: OperationRequest<'createPersonalNote'>, options: RequiredIdempotencyOptions) {
    return this.request<'createPersonalNote', '201'>('createPersonalNote', {
      body, headers: idempotencyHeaders(options, 'createPersonalNote'), signal: options.signal,
    });
  }

  getPersonalNote(noteId: string, options: ApiRequestOptions = {}) {
    return this.request<'getPersonalNote', '200'>('getPersonalNote', { params: { path: { noteId } }, signal: options.signal });
  }

  updatePersonalNote(noteId: string, body: OperationRequest<'updatePersonalNote'>) {
    return this.request<'updatePersonalNote', '200'>('updatePersonalNote', { params: { path: { noteId } }, body });
  }

  async deletePersonalNote(noteId: string, expectedRevision: number) {
    await this.request<'deletePersonalNote', '204'>('deletePersonalNote', { params: { path: { noteId }, query: { expectedRevision } } });
  }

  resolvePersonalNoteSource(noteId: string, options: ApiRequestOptions = {}) {
    return this.request<'resolvePersonalNoteSource', '200'>('resolvePersonalNoteSource', { params: { path: { noteId } }, signal: options.signal });
  }

  listProjects(options: DeletedSourceListOptions<'listProjects'>): Promise<Schema<'DeletedProjectList'>>;
  listProjects(options?: ActiveSourceListOptions<'listProjects'>): Promise<Schema<'ProjectList'>>;
  listProjects(options: CursorListOptions<'listProjects'> = {}) {
    const { query, signal } = splitListOptions(options);
    return this.request<'listProjects', '200'>('listProjects', { params: query ? { query } : undefined, signal });
  }

  createProject(body: OperationRequest<'createProject'>) {
    return this.request<'createProject', '201'>('createProject', { body });
  }

  openDemoSaga() {
    return this.request<'openDemoSaga', '200'>('openDemoSaga');
  }

  getProject(projectId: string) {
    return this.request<'getProject', '200'>('getProject', { params: { path: { projectId } } });
  }

  getProjectSupportSnapshot(projectId: string) {
    return this.request<'getProjectSupportSnapshot', '200'>('getProjectSupportSnapshot', { params: { path: { projectId } } });
  }

  updateProject(projectId: string, body: OperationRequest<'updateProject'>) {
    return this.request<'updateProject', '200'>('updateProject', { params: { path: { projectId } }, body });
  }

  async deleteProject(projectId: string) {
    await this.request<'deleteProject', '204'>('deleteProject', { params: { path: { projectId } } });
  }

  restoreProject(projectId: string) {
    return this.request<'restoreProject', '200'>('restoreProject', { params: { path: { projectId } } });
  }

  listProjectMembers(projectId: string, options: CursorListOptions<'listProjectMembers'> = {}) {
    const { query, signal } = splitListOptions(options);
    return this.request<'listProjectMembers', '200'>('listProjectMembers', {
      params: query ? { path: { projectId }, query } : { path: { projectId } },
      signal,
    });
  }

  updateProjectMemberRole(projectId: string, memberId: string, body: OperationRequest<'updateProjectMemberRole'>) {
    return this.request<'updateProjectMemberRole', '200'>('updateProjectMemberRole', { params: { path: { projectId, memberId } }, body });
  }

  async removeProjectMember(projectId: string, memberId: string) {
    await this.request<'removeProjectMember', '204'>('removeProjectMember', { params: { path: { projectId, memberId } } });
  }

  listProjectInvitations(projectId: string, options: CursorListOptions<'listProjectInvitations'> = {}) {
    const { query, signal } = splitListOptions(options);
    return this.request<'listProjectInvitations', '200'>('listProjectInvitations', {
      params: query ? { path: { projectId }, query } : { path: { projectId } },
      signal,
    });
  }

  listMyProjectInvitations(options: CursorListOptions<'listMyProjectInvitations'> = {}) {
    const { query, signal } = splitListOptions(options);
    return this.request<'listMyProjectInvitations', '200'>('listMyProjectInvitations', {
      params: query ? { query } : undefined,
      signal,
    });
  }

  createProjectInvitation(projectId: string, body: OperationRequest<'createProjectInvitation'>) {
    return this.request<'createProjectInvitation', '201'>('createProjectInvitation', { params: { path: { projectId } }, body });
  }

  cancelProjectInvitation(invitationId: string) {
    return this.request<'cancelProjectInvitation', '200'>('cancelProjectInvitation', { params: { path: { invitationId } } });
  }

  acceptProjectInvitation(invitationId: string) {
    return this.request<'acceptProjectInvitation', '200'>('acceptProjectInvitation', { params: { path: { invitationId } } });
  }

  listBooks(projectId: string, options: DeletedSourceListOptions<'listBooks'>): Promise<Schema<'DeletedBookList'>>;
  listBooks(projectId: string, options?: ActiveSourceListOptions<'listBooks'>): Promise<Schema<'BookList'>>;
  listBooks(projectId: string, options: CursorListOptions<'listBooks'> = {}) {
    const { query, signal } = splitListOptions(options);
    return this.request<'listBooks', '200'>('listBooks', {
      params: query ? { path: { projectId }, query } : { path: { projectId } },
      signal,
    });
  }

  createBook(projectId: string, body: OperationRequest<'createBook'>) {
    return this.request<'createBook', '201'>('createBook', { params: { path: { projectId } }, body });
  }

  getBook(bookId: string) {
    return this.request<'getBook', '200'>('getBook', { params: { path: { bookId } } });
  }

  updateBook(bookId: string, body: OperationRequest<'updateBook'>) {
    return this.request<'updateBook', '200'>('updateBook', { params: { path: { bookId } }, body });
  }

  async deleteBook(bookId: string) {
    await this.request<'deleteBook', '204'>('deleteBook', { params: { path: { bookId } } });
  }

  restoreBook(bookId: string) {
    return this.request<'restoreBook', '200'>('restoreBook', { params: { path: { bookId } } });
  }

  previewBookUpdateRevert(bookId: string) {
    return this.request<'previewBookUpdateRevert', '200'>('previewBookUpdateRevert', { params: { path: { bookId } } });
  }

  revertBookUpdate(bookId: string, body: OperationRequest<'revertBookUpdate'>) {
    return this.request<'revertBookUpdate', '200'>('revertBookUpdate', { params: { path: { bookId } }, body });
  }

  uploadBookCover(bookId: string, file: File, options: BookCoverUploadOptions = {}) {
    const form = new FormData();
    form.append('file', file);
    return this.postMultipart<'uploadBookCover', OperationResponse<'uploadBookCover', '200'>>(
      'uploadBookCover', { bookId }, form, options,
    );
  }

  deleteBookCover(bookId: string) {
    return this.request<'deleteBookCover', '200'>('deleteBookCover', { params: { path: { bookId } } });
  }

  setBookCoverFromImage(bookId: string, imageNumber: number) {
    return this.request<'setBookCoverFromImage', '200'>('setBookCoverFromImage', { params: { path: { bookId } }, body: { imageNumber } });
  }

  getBookExport(bookId: string, format: BookExportFormat, options: BookExportOptions = {}) {
    // Without the drawings nothing is sent: the file is the one it was before them (BP2.1).
    const query = options.drawings ? { format, drawings: true } : { format };
    return this.requestFile('getBookExport', { path: { bookId }, query }, `Книга.${format}`);
  }

  getBookDrawingCounts(projectId: string, options: Pick<ApiRequestOptions, 'signal'> = {}) {
    return this.request<'getBookDrawingCounts', '200'>('getBookDrawingCounts', { params: { path: { projectId } }, signal: options.signal });
  }

  getImportConstraints(projectId: string) {
    return this.request<'getImportConstraints', '200'>('getImportConstraints', { params: { path: { projectId } } });
  }

  createImportQueue(projectId: string, body: OperationRequest<'createImportQueue'>, options: RequiredIdempotencyOptions) {
    return this.request<'createImportQueue', '201'>('createImportQueue', {
      params: { path: { projectId } },
      body,
      headers: idempotencyHeaders(options, 'createImportQueue'),
      signal: options?.signal,
    });
  }

  listImportQueues(projectId: string, options: Pick<ApiRequestOptions, 'signal'> = {}) {
    return this.request<'listImportQueues', '200'>('listImportQueues', {
      params: { path: { projectId } },
      signal: options.signal,
    });
  }

  getImportQueue(projectId: string, queueId: string, options: Pick<ApiRequestOptions, 'signal'> = {}) {
    return this.request<'getImportQueue', '200'>('getImportQueue', {
      params: { path: { projectId, queueId } },
      signal: options.signal,
    });
  }

  async uploadImportQueueItem(
    projectId: string,
    queueId: string,
    input: ImportQueueItemUploadInput,
    options: ImportBookRequestOptions,
  ) {
    idempotencyHeaders(options, 'uploadImportQueueItem');
    const form = new FormData();
    form.append('itemId', input.itemId);
    form.append('file', canonicalImportFile(input.file), input.file.name);
    return this.postMultipart<'uploadImportQueueItem', OperationResponse<'uploadImportQueueItem', '200'>>(
      'uploadImportQueueItem', { projectId, queueId }, form, options,
    );
  }

  /** «Пропустить» of a volume that did not read (specs/2026-11-11-ux7-upload.md UP3.2). */
  skipImportQueueItem(projectId: string, queueId: string, itemId: string) {
    return this.request<'skipImportQueueItem', '200'>('skipImportQueueItem', {
      params: { path: { projectId, queueId, itemId } },
    });
  }

  startImportQueue(
    projectId: string,
    queueId: string,
    body: OperationRequest<'startImportQueue'>,
    options: RequiredIdempotencyOptions,
  ) {
    return this.request<'startImportQueue', '202'>('startImportQueue', {
      params: { path: { projectId, queueId } },
      body,
      headers: idempotencyHeaders(options, 'startImportQueue'),
      signal: options?.signal,
    });
  }

  cancelImportQueue(projectId: string, queueId: string) {
    return this.request<'cancelImportQueue', '200'>('cancelImportQueue', {
      params: { path: { projectId, queueId } },
    });
  }

  async importBookFile(projectId: string, input: ImportBookInput, options: ImportBookRequestOptions) {
    idempotencyHeaders(options, 'importBookFile');
    const form = new FormData();
    form.append('file', canonicalImportFile(input.file), input.file.name);
    if (input.metadata) {
      form.append('metadata', JSON.stringify(input.metadata));
    }
    if (input.options) {
      form.append('options', JSON.stringify(input.options));
    }
    const response = await this.postMultipart<'importBookFile', OperationResponse<'importBookFile', '202'>>(
      'importBookFile', { projectId }, form, options,
    );
    const jobId = response?.job?.id;
    if (typeof jobId !== 'string' || jobId.trim() === '') {
      throw new NetworkApiError(new TypeError('importBookFile response has no usable job identity.'));
    }
    return response;
  }

  listProjectJobs(projectId: string, options: ListProjectJobsOptions = {}) {
    const { signal, ...query } = options;
    return this.request<'listProjectJobs', '200'>('listProjectJobs', { params: { path: { projectId }, query }, signal });
  }

  getIndexingStatus(projectId: string, options: GetIndexingStatusOptions = {}) {
    const { signal, ...query } = options;
    return this.request<'getIndexingStatus', '200'>('getIndexingStatus', {
      params: { path: { projectId }, query },
      signal,
    });
  }

  listSagaGlossary(projectId: string, options: ListSagaGlossaryOptions) {
    const { signal, ...query } = options;
    return this.request<'listSagaGlossary', '200'>('listSagaGlossary', { params: { path: { projectId }, query }, signal });
  }

  /** The graph of the saga; the browser revalidates it by its ETag (specs/2026-11-04-saga-map.md, R1.5). */
  getSagaMap(projectId: string, options: GetSagaMapOptions) {
    const { signal, ...query } = options;
    return this.request<'getSagaMap', '200'>('getSagaMap', { params: { path: { projectId }, query }, signal });
  }

  /** A search of the map beyond its nodes (specs/2026-11-04-saga-map.md, R10.6). */
  searchSagaMap(projectId: string, options: SearchSagaMapOptions) {
    const { signal, ...query } = options;
    return this.request<'searchSagaMap', '200'>('searchSagaMap', { params: { path: { projectId }, query }, signal });
  }

  /** The questions an empty chat offers, with the names of the book (specs/2026-11-10-first-questions.md FQ1.4). */
  getSagaFirstQuestions(projectId: string, options: GetSagaFirstQuestionsOptions) {
    const { signal, ...query } = options;
    return this.request<'getSagaFirstQuestions', '200'>('getSagaFirstQuestions', { params: { path: { projectId }, query }, signal });
  }

  /** What the author took off the map (specs/2026-11-04-saga-map.md, R11.3). */
  listSagaMapHidden(projectId: string, options: ListSagaMapHiddenOptions) {
    const { signal, ...query } = options;
    return this.request<'listSagaMapHidden', '200'>('listSagaMapHidden', { params: { path: { projectId }, query }, signal });
  }

  hideSagaMapItem(projectId: string, sourceView: SourceView, body: OperationRequest<'hideSagaMapItem'>) {
    return this.request<'hideSagaMapItem', '201'>('hideSagaMapItem', { params: { path: { projectId }, query: { sourceView } }, body });
  }

  async restoreSagaMapItem(projectId: string, hiddenId: string) {
    await this.request<'restoreSagaMapItem', '204'>('restoreSagaMapItem', { params: { path: { projectId, hiddenId } } });
  }

  getSagaMapNode(projectId: string, options: GetSagaMapNodeOptions) {
    const { signal, ...query } = options;
    return this.request<'getSagaMapNode', '200'>('getSagaMapNode', { params: { path: { projectId }, query }, signal });
  }

  getSagaContents(projectId: string, options: GetSagaContentsOptions) {
    const { signal, ...query } = options;
    return this.request<'getSagaContents', '200'>('getSagaContents', { params: { path: { projectId }, query }, signal });
  }

  listAuthorMaterials(projectId: string, options: ListAuthorMaterialsOptions = {}) {
    const { signal, ...query } = options;
    return this.request<'listAuthorMaterials', '200'>('listAuthorMaterials', { params: { path: { projectId }, query }, signal });
  }

  createAuthorMaterial(projectId: string, body: OperationRequest<'createAuthorMaterial'>, options: RequiredIdempotencyOptions) {
    return this.request<'createAuthorMaterial', '201'>('createAuthorMaterial', {
      params: { path: { projectId } }, body, headers: idempotencyHeaders(options, 'createAuthorMaterial'), signal: options.signal,
    });
  }

  updateAuthorMaterial(projectId: string, materialId: string, body: OperationRequest<'updateAuthorMaterial'>) {
    return this.request<'updateAuthorMaterial', '200'>('updateAuthorMaterial', { params: { path: { projectId, materialId } }, body });
  }

  async deleteAuthorMaterial(projectId: string, materialId: string, expectedRevision: number) {
    await this.request<'deleteAuthorMaterial', '204'>('deleteAuthorMaterial', { params: { path: { projectId, materialId }, query: { expectedRevision } } });
  }

  startAuthorMaterialsImport(projectId: string, input: AuthorMaterialsImportInput, options: RequiredIdempotencyOptions) {
    idempotencyHeaders(options, 'startAuthorMaterialsImport');
    const form = new FormData();
    if (input.file) form.append('file', input.file, input.file.name);
    else form.append('text', input.text);
    return this.postMultipart<'startAuthorMaterialsImport', OperationResponse<'startAuthorMaterialsImport', '202'>>(
      'startAuthorMaterialsImport', { projectId }, form, options,
    );
  }

  getAuthorMaterialsImport(projectId: string, options: { signal?: AbortSignal } = {}) {
    return this.request<'getAuthorMaterialsImport', '200'>('getAuthorMaterialsImport', { params: { path: { projectId } }, signal: options.signal });
  }

  confirmAuthorMaterialsImport(projectId: string, importId: string, body: OperationRequest<'confirmAuthorMaterialsImport'>, options: RequiredIdempotencyOptions) {
    return this.request<'confirmAuthorMaterialsImport', '200'>('confirmAuthorMaterialsImport', {
      params: { path: { projectId, importId } }, body, headers: idempotencyHeaders(options, 'confirmAuthorMaterialsImport'),
    });
  }

  async discardAuthorMaterialsImport(projectId: string, importId: string) {
    await this.request<'discardAuthorMaterialsImport', '204'>('discardAuthorMaterialsImport', { params: { path: { projectId, importId } } });
  }

  listAuthorMemory(projectId: string, options: ListAuthorMemoryOptions = {}) {
    const { query, signal } = splitListOptions(options);
    return this.request<'listAuthorMemory', '200'>('listAuthorMemory', {
      params: query ? { path: { projectId }, query } : { path: { projectId } },
      signal,
    });
  }

  createAuthorMemory(projectId: string, body: OperationRequest<'createAuthorMemory'>, options: RequiredIdempotencyOptions) {
    return this.request<'createAuthorMemory', '201'>('createAuthorMemory', {
      params: { path: { projectId } }, body, headers: idempotencyHeaders(options, 'createAuthorMemory'), signal: options.signal,
    });
  }

  updateAuthorMemory(projectId: string, recordId: string, body: OperationRequest<'updateAuthorMemory'>) {
    return this.request<'updateAuthorMemory', '200'>('updateAuthorMemory', { params: { path: { projectId, recordId } }, body });
  }

  async deleteAuthorMemory(projectId: string, recordId: string, expectedRevision: number) {
    await this.request<'deleteAuthorMemory', '204'>('deleteAuthorMemory', { params: { path: { projectId, recordId }, query: { expectedRevision } } });
  }

  listChapterDrafts(projectId: string, options: ListChapterDraftsOptions = {}) {
    const { query, signal } = splitListOptions(options);
    return this.request<'listChapterDrafts', '200'>('listChapterDrafts', {
      params: query ? { path: { projectId }, query } : { path: { projectId } },
      signal,
    });
  }

  createChapterDraft(projectId: string, body: OperationRequest<'createChapterDraft'>, options: RequiredIdempotencyOptions) {
    return this.request<'createChapterDraft', '201'>('createChapterDraft', {
      params: { path: { projectId } }, body, headers: idempotencyHeaders(options, 'createChapterDraft'), signal: options.signal,
    });
  }

  getChapterDraft(projectId: string, draftId: string, options: Pick<ApiRequestOptions, 'signal'> = {}) {
    return this.request<'getChapterDraft', '200'>('getChapterDraft', { params: { path: { projectId, draftId } }, signal: options.signal });
  }

  /** A version of the plan of the writing agent (specs/2026-11-06-writing-agent.md, WA4). */
  getChapterDraftPlan(projectId: string, draftId: string, options: { number?: number } & Pick<ApiRequestOptions, 'signal'> = {}) {
    return this.request<'getChapterDraftPlan', '200'>('getChapterDraftPlan', {
      params: { path: { projectId, draftId }, ...(options.number ? { query: { number: options.number } } : {}) }, signal: options.signal,
    });
  }

  /** The pack of the chapter on its moment (WA2). */
  getChapterDraftMoment(projectId: string, draftId: string, options: Pick<ApiRequestOptions, 'signal'> = {}) {
    return this.request<'getChapterDraftMoment', '200'>('getChapterDraftMoment', { params: { path: { projectId, draftId } }, signal: options.signal });
  }

  /** The author's version of the plan (specs/2026-11-06-writing-agent.md, WA4.2). */
  updateChapterDraftPlan(projectId: string, draftId: string, body: OperationRequest<'updateChapterDraftPlan'>) {
    return this.request<'updateChapterDraftPlan', '200'>('updateChapterDraftPlan', { params: { path: { projectId, draftId } }, body });
  }

  /** «Оставить как было» of a plan «Переписать отсюда до конца» that came (specs/2026-11-11-ux9-writing-b.md WB1.2). */
  withdrawChapterDraftPlan(projectId: string, draftId: string, body: OperationRequest<'withdrawChapterDraftPlan'>) {
    return this.request<'withdrawChapterDraftPlan', '200'>('withdrawChapterDraftPlan', { params: { path: { projectId, draftId } }, body });
  }

  /** The author's choices about the pack (WA2.5). */
  updateChapterDraftMoment(projectId: string, draftId: string, body: OperationRequest<'updateChapterDraftMoment'>) {
    return this.request<'updateChapterDraftMoment', '200'>('updateChapterDraftMoment', { params: { path: { projectId, draftId } }, body });
  }

  /** The versions of the text of a draft (specs/2026-11-06-writing-agent.md, WA7.5). */
  listChapterDraftVersions(projectId: string, draftId: string, options: Pick<ApiRequestOptions, 'signal'> = {}) {
    return this.request<'listChapterDraftVersions', '200'>('listChapterDraftVersions', { params: { path: { projectId, draftId } }, signal: options.signal });
  }

  getChapterDraftVersion(projectId: string, draftId: string, number: number, options: Pick<ApiRequestOptions, 'signal'> = {}) {
    return this.request<'getChapterDraftVersion', '200'>('getChapterDraftVersion', { params: { path: { projectId, draftId, number } }, signal: options.signal });
  }

  /** «Вернуть эту версию» or «Взять в главу» a piece of it. */
  takeChapterDraftVersion(projectId: string, draftId: string, number: number, body: OperationRequest<'takeChapterDraftVersion'>) {
    return this.request<'takeChapterDraftVersion', '200'>('takeChapterDraftVersion', { params: { path: { projectId, draftId, number } }, body });
  }

  updateChapterDraft(projectId: string, draftId: string, body: OperationRequest<'updateChapterDraft'>) {
    return this.request<'updateChapterDraft', '200'>('updateChapterDraft', { params: { path: { projectId, draftId } }, body });
  }

  async deleteChapterDraft(projectId: string, draftId: string, expectedRevision: number) {
    await this.request<'deleteChapterDraft', '204'>('deleteChapterDraft', { params: { path: { projectId, draftId }, query: { expectedRevision } } });
  }

  startChapterDraftJob(projectId: string, draftId: string, body: OperationRequest<'startChapterDraftJob'>, options: RequiredIdempotencyOptions) {
    return this.request<'startChapterDraftJob', '201'>('startChapterDraftJob', {
      params: { path: { projectId, draftId } }, body, headers: idempotencyHeaders(options, 'startChapterDraftJob'), signal: options.signal,
    });
  }

  addChapterDraftToBook(projectId: string, draftId: string, body: OperationRequest<'addChapterDraftToBook'>) {
    return this.request<'addChapterDraftToBook', '200'>('addChapterDraftToBook', { params: { path: { projectId, draftId } }, body });
  }

  openChapterEdit(projectId: string, chapterId: string) {
    return this.request<'openChapterEdit', '200'>('openChapterEdit', { params: { path: { projectId, chapterId } } });
  }

  replaceChapterFromDraft(projectId: string, draftId: string, body: OperationRequest<'replaceChapterFromDraft'>) {
    return this.request<'replaceChapterFromDraft', '200'>('replaceChapterFromDraft', { params: { path: { projectId, draftId } }, body });
  }

  reopenChapterFromBook(projectId: string, draftId: string, body: OperationRequest<'reopenChapterFromBook'>) {
    return this.request<'reopenChapterFromBook', '200'>('reopenChapterFromBook', { params: { path: { projectId, draftId } }, body });
  }

  revertChapterFromDraft(projectId: string, draftId: string, body: OperationRequest<'revertChapterFromDraft'>) {
    return this.request<'revertChapterFromDraft', '200'>('revertChapterFromDraft', { params: { path: { projectId, draftId } }, body });
  }

  startChapterProofread(projectId: string, input: ProofreadChapterInput & { title?: string; bookId?: string; afterChapterId?: string }, options: RequiredIdempotencyOptions) {
    idempotencyHeaders(options, 'startChapterProofread');
    const form = 'chatId' in input && input.chatId ? chatChapterForm(input.chatId, input.attachment) : chapterForm(input as ChapterFileInput);
    if (input.title) form.append('title', input.title);
    if (input.bookId) form.append('bookId', input.bookId);
    if (input.afterChapterId) form.append('afterChapterId', input.afterChapterId);
    return this.postMultipart<'startChapterProofread', OperationResponse<'startChapterProofread', '201'>>('startChapterProofread', { projectId }, form, options);
  }

  loadChapterDraftFile(projectId: string, draftId: string, input: ChapterFileInput & { expectedRevision: number }, options: RequiredIdempotencyOptions) {
    idempotencyHeaders(options, 'loadChapterDraftFile');
    const form = new FormData();
    form.append('expectedRevision', String(input.expectedRevision));
    const chapter = chapterForm(input);
    for (const [name, value] of chapter.entries()) form.append(name, value);
    return this.postMultipart<'loadChapterDraftFile', OperationResponse<'loadChapterDraftFile', '200'>>('loadChapterDraftFile', { projectId, draftId }, form, options);
  }

  // The look of the author's Word file for the original of a draft (specs/2026-11-07-writing-proofread.md, PR9): the same
  // file gives the same original, so it carries no key.
  takeChapterDraftOriginalLook(projectId: string, draftId: string, input: { file: File }) {
    const form = new FormData();
    form.append('file', input.file);
    return this.postMultipart<'takeChapterDraftOriginalLook', OperationResponse<'takeChapterDraftOriginalLook', '200'>>(
      'takeChapterDraftOriginalLook', { projectId, draftId }, form, {},
    );
  }

  applyChapterDraftNotes(projectId: string, draftId: string, body: OperationRequest<'applyChapterDraftNotes'>) {
    return this.request<'applyChapterDraftNotes', '200'>('applyChapterDraftNotes', { params: { path: { projectId, draftId } }, body });
  }

  getChapterDraftExport(projectId: string, draftId: string) {
    return this.requestFile('getChapterDraftExport', { path: { projectId, draftId } }, 'Глава — правки.docx');
  }

  applyChapterDraftNote(projectId: string, draftId: string, noteId: string, body: OperationRequest<'applyChapterDraftNote'>) {
    return this.request<'applyChapterDraftNote', '200'>('applyChapterDraftNote', { params: { path: { projectId, draftId, noteId } }, body });
  }

  updateChapterDraftNote(projectId: string, draftId: string, noteId: string, body: OperationRequest<'updateChapterDraftNote'>) {
    return this.request<'updateChapterDraftNote', '200'>('updateChapterDraftNote', { params: { path: { projectId, draftId, noteId } }, body });
  }

  submitDraftTurn(projectId: string, draftId: string, body: OperationRequest<'submitDraftTurn'>, options: RequiredIdempotencyOptions) {
    return this.request<'submitDraftTurn', '202'>('submitDraftTurn', {
      params: { path: { projectId, draftId } }, body, headers: idempotencyHeaders(options, 'submitDraftTurn'), signal: options.signal,
    });
  }

  getProjectVoice(projectId: string, options: Pick<ApiRequestOptions, 'signal'> = {}) {
    return this.request<'getProjectVoice', '200'>('getProjectVoice', { params: { path: { projectId } }, signal: options.signal });
  }

  setAccountVoice(body: OperationRequest<'setAccountVoice'>) {
    return this.request<'setAccountVoice', '200'>('setAccountVoice', { body });
  }

  /** What this client sends with its writes: asking for a fresh one would void it. */
  voiceCallToken() {
    return this.ensureCsrfToken();
  }

  listProjectImages(projectId: string, options: ListProjectImagesOptions = {}) {
    const { query, signal } = splitListOptions(options);
    return this.request<'listProjectImages', '200'>('listProjectImages', {
      params: query ? { path: { projectId }, query } : { path: { projectId } },
      signal,
    });
  }

  getProjectImage(projectId: string, number: number, options: Pick<ApiRequestOptions, 'signal'> = {}) {
    return this.request<'getProjectImage', '200'>('getProjectImage', { params: { path: { projectId, number } }, signal: options.signal });
  }

  updateProjectImageDetails(projectId: string, number: number, body: OperationRequest<'updateProjectImageDetails'>) {
    return this.request<'updateProjectImageDetails', '200'>('updateProjectImageDetails', { params: { path: { projectId, number } }, body });
  }

  setProjectImageReference(projectId: string, number: number, body: OperationRequest<'setProjectImageReference'>) {
    return this.request<'setProjectImageReference', '200'>('setProjectImageReference', { params: { path: { projectId, number } }, body });
  }

  removeProjectImageReference(projectId: string, number: number, query: RemoveProjectImageReferenceQuery) {
    return this.request<'removeProjectImageReference', '200'>('removeProjectImageReference', { params: { path: { projectId, number }, query } });
  }

  async deleteProjectImage(projectId: string, number: number) {
    await this.request<'deleteProjectImage', '204'>('deleteProjectImage', { params: { path: { projectId, number } } });
  }

  getProjectIllustrationStyle(projectId: string, options: Pick<ApiRequestOptions, 'signal'> = {}) {
    return this.request<'getProjectIllustrationStyle', '200'>('getProjectIllustrationStyle', { params: { path: { projectId } }, signal: options.signal });
  }

  updateProjectIllustrationStyle(projectId: string, body: OperationRequest<'updateProjectIllustrationStyle'>) {
    return this.request<'updateProjectIllustrationStyle', '200'>('updateProjectIllustrationStyle', { params: { path: { projectId } }, body });
  }

  cutoutProjectImage(projectId: string, number: number, options: RequiredIdempotencyOptions) {
    return this.request<'cutoutProjectImage', '201'>('cutoutProjectImage', {
      params: { path: { projectId, number } }, headers: idempotencyHeaders(options, 'cutoutProjectImage'), signal: options?.signal,
    });
  }

  changeProjectImageAngle(projectId: string, number: number, body: OperationRequest<'changeProjectImageAngle'>, options: RequiredIdempotencyOptions) {
    return this.request<'changeProjectImageAngle', '201'>('changeProjectImageAngle', {
      params: { path: { projectId, number } }, body, headers: idempotencyHeaders(options, 'changeProjectImageAngle'), signal: options?.signal,
    });
  }


  deleteProjectImages(projectId: string, body: OperationRequest<'deleteProjectImages'>, options: RequiredIdempotencyOptions) {
    return this.request<'deleteProjectImages', '200'>('deleteProjectImages', {
      params: { path: { projectId } }, body, headers: idempotencyHeaders(options, 'deleteProjectImages'), signal: options?.signal,
    });
  }

  createProjectImageVariant(projectId: string, number: number, body: OperationRequest<'createProjectImageVariant'>, options: RequiredIdempotencyOptions) {
    return this.request<'createProjectImageVariant', '201'>('createProjectImageVariant', {
      params: { path: { projectId, number } }, body, headers: idempotencyHeaders(options, 'createProjectImageVariant'), signal: options?.signal,
    });
  }

  chooseProjectImageVariant(projectId: string, number: number, options: RequiredIdempotencyOptions) {
    return this.request<'chooseProjectImageVariant', '200'>('chooseProjectImageVariant', {
      params: { path: { projectId, number } }, headers: idempotencyHeaders(options, 'chooseProjectImageVariant'), signal: options?.signal,
    });
  }

  /**
   * The main picture of drawing K in its full size, as a file for the archive of «Скачать (N)»
   * (specs/2026-11-12-ux10-drawing-ops.md DO3.1): the file of getProjectImageFile, read here and not shown.
   */
  downloadProjectImage(projectId: string, number: number) {
    return this.requestFile('getProjectImageFile', { path: { projectId, number }, query: { size: 'full' } }, `${number}.png`);
  }

  /** «Нарисовать этот ещё раз» of a variant that did not come out (specs/2026-11-12-ux10-drawing-ops.md DO1). */
  redrawProjectImageVariant(projectId: string, number: number, body: OperationRequest<'redrawProjectImageVariant'>, options: RequiredIdempotencyOptions) {
    return this.request<'redrawProjectImageVariant', '200'>('redrawProjectImageVariant', {
      params: { path: { projectId, number } }, body, headers: idempotencyHeaders(options, 'redrawProjectImageVariant'), signal: options?.signal,
    });
  }

  chooseProjectImageLayerVariant(projectId: string, number: number, layerId: string, options: RequiredIdempotencyOptions) {
    return this.request<'chooseProjectImageLayerVariant', '200'>('chooseProjectImageLayerVariant', {
      params: { path: { projectId, number, layerId } }, headers: idempotencyHeaders(options, 'chooseProjectImageLayerVariant'), signal: options?.signal,
    });
  }

  listDrawings(projectId: string, options: ListDrawingsOptions = {}) {
    const { query, signal } = splitListOptions(options);
    return this.request<'listDrawings', '200'>('listDrawings', {
      params: query ? { path: { projectId }, query } : { path: { projectId } },
      signal,
    });
  }

  getDrawingsFacets(projectId: string, options: GetDrawingsFacetsOptions = {}) {
    const { signal, ...query } = options;
    return this.request<'getDrawingsFacets', '200'>('getDrawingsFacets', { params: { path: { projectId }, query }, signal });
  }

  createDrawing(projectId: string, body: OperationRequest<'createDrawing'>, options: RequiredIdempotencyOptions) {
    return this.request<'createDrawing', '201'>('createDrawing', {
      params: { path: { projectId } }, body, headers: idempotencyHeaders(options, 'createDrawing'), signal: options.signal,
    });
  }

  getDrawing(projectId: string, drawingId: string, options: Pick<ApiRequestOptions, 'signal'> = {}) {
    return this.request<'getDrawing', '200'>('getDrawing', { params: { path: { projectId, drawingId } }, signal: options.signal });
  }

  updateDrawing(projectId: string, drawingId: string, body: OperationRequest<'updateDrawing'>) {
    return this.request<'updateDrawing', '200'>('updateDrawing', { params: { path: { projectId, drawingId } }, body });
  }

  async deleteDrawing(projectId: string, drawingId: string) {
    await this.request<'deleteDrawing', '204'>('deleteDrawing', { params: { path: { projectId, drawingId } } });
  }

  restoreDrawing(projectId: string, drawingId: string) {
    return this.request<'restoreDrawing', '200'>('restoreDrawing', { params: { path: { projectId, drawingId } } });
  }

  /** «Отменить» of a drawing put off until the service of drawings is free (DO2.2). */
  cancelDeferredDrawing(projectId: string, drawingId: string) {
    return this.request<'cancelDeferredDrawing', '200'>('cancelDeferredDrawing', { params: { path: { projectId, drawingId } } });
  }

  drawDrawing(projectId: string, drawingId: string, body: OperationRequest<'drawDrawing'>, options: RequiredIdempotencyOptions) {
    return this.request<'drawDrawing', '201'>('drawDrawing', {
      params: { path: { projectId, drawingId } }, body, headers: idempotencyHeaders(options, 'drawDrawing'), signal: options.signal,
    });
  }

  submitDrawingTurn(projectId: string, drawingId: string, body: OperationRequest<'submitDrawingTurn'>, options: RequiredIdempotencyOptions) {
    return this.request<'submitDrawingTurn', '202'>('submitDrawingTurn', {
      params: { path: { projectId, drawingId } }, body, headers: idempotencyHeaders(options, 'submitDrawingTurn'), signal: options.signal,
    });
  }

  enlargeProjectImage(projectId: string, number: number, options: RequiredIdempotencyOptions) {
    return this.request<'enlargeProjectImage', '201'>('enlargeProjectImage', {
      params: { path: { projectId, number } }, headers: idempotencyHeaders(options, 'enlargeProjectImage'), signal: options?.signal,
    });
  }

  createProjectImageLayer(projectId: string, number: number, body: OperationRequest<'createProjectImageLayer'>, options: RequiredIdempotencyOptions) {
    return this.request<'createProjectImageLayer', '201'>('createProjectImageLayer', {
      params: { path: { projectId, number } }, body, headers: idempotencyHeaders(options, 'createProjectImageLayer'), signal: options?.signal,
    });
  }

  updateProjectImageLayer(projectId: string, number: number, layerId: string, body: OperationRequest<'updateProjectImageLayer'>) {
    return this.request<'updateProjectImageLayer', '200'>('updateProjectImageLayer', { params: { path: { projectId, number, layerId } }, body });
  }

  deleteProjectImageLayer(projectId: string, number: number, layerId: string) {
    return this.request<'deleteProjectImageLayer', '200'>('deleteProjectImageLayer', { params: { path: { projectId, number, layerId } } });
  }

  createProjectCharacterSheet(projectId: string, body: OperationRequest<'createProjectCharacterSheet'>, options: RequiredIdempotencyOptions) {
    return this.request<'createProjectCharacterSheet', '201'>('createProjectCharacterSheet', {
      params: { path: { projectId } }, body, headers: idempotencyHeaders(options, 'createProjectCharacterSheet'), signal: options?.signal,
    });
  }

  createIllustrationStyleSketches(projectId: string, body: OperationRequest<'createIllustrationStyleSketches'>, options: RequiredIdempotencyOptions) {
    return this.request<'createIllustrationStyleSketches', '201'>('createIllustrationStyleSketches', {
      params: { path: { projectId } }, body, headers: idempotencyHeaders(options, 'createIllustrationStyleSketches'), signal: options?.signal,
    });
  }

  async uploadProjectImage(projectId: string, input: ImageUploadInput, options: ImportBookRequestOptions) {
    idempotencyHeaders(options, 'uploadProjectImage');
    const form = new FormData();
    form.append('file', input.file, input.file.name);
    if (input.title !== undefined) {
      form.append('title', input.title);
    }
    if (input.consentVersion !== undefined) {
      form.append('consentVersion', input.consentVersion);
    }
    return this.postMultipart<'uploadProjectImage', OperationResponse<'uploadProjectImage', '201'>>(
      'uploadProjectImage', { projectId }, form, options,
    );
  }


  startProjectIndexing(projectId: string, body: OperationRequest<'startProjectIndexing'>, options: RequiredIdempotencyOptions) {
    return this.request<'startProjectIndexing', '202'>('startProjectIndexing', {
      params: { path: { projectId } },
      body,
      headers: idempotencyHeaders(options, 'startProjectIndexing'),
      signal: options?.signal,
    });
  }

  getJob(jobId: string) {
    return this.request<'getJob', '200'>('getJob', { params: { path: { jobId } } });
  }

  getAgentTimeline(projectId: string, jobId: string, options: GetAgentTimelineOptions = {}) {
    const { cursor, signal } = options;
    return this.request<'getAgentTimeline', '200'>('getAgentTimeline', {
      params: cursor === undefined || cursor === null
        ? { path: { projectId, jobId } }
        : { path: { projectId, jobId }, query: { cursor: String(cursor) } },
      signal,
    });
  }

  cancelJob(jobId: string) {
    return this.request<'cancelJob', '200' | '204'>('cancelJob', { params: { path: { jobId } } });
  }

  listChapters(bookId: string, options: DeletedSourceListOptions<'listChapters'>): Promise<Schema<'DeletedChapterList'>>;
  listChapters(bookId: string, options?: ActiveSourceListOptions<'listChapters'>): Promise<Schema<'ChapterList'>>;
  listChapters(bookId: string, options: CursorListOptions<'listChapters'> = {}) {
    const { query, signal } = splitListOptions(options);
    return this.request<'listChapters', '200'>('listChapters', {
      params: query ? { path: { bookId }, query } : { path: { bookId } },
      signal,
    });
  }

  getChapter(chapterId: string, contentVariant: SourceView) {
    return this.request<'getChapter', '200'>('getChapter', { params: { path: { chapterId }, query: { contentVariant } } });
  }

  async deleteChapter(chapterId: string) {
    await this.request<'deleteChapter', '204'>('deleteChapter', { params: { path: { chapterId } } });
  }

  restoreChapter(chapterId: string) {
    return this.request<'restoreChapter', '200'>('restoreChapter', { params: { path: { chapterId } } });
  }

  searchProject(projectId: string, query: string, sourceView: SourceView, scope?: SearchScope, options: SearchProjectOptions = {}) {
    return this.request<'searchProject', '200'>('searchProject', {
      params: { path: { projectId } },
      body: { query, scope: scope ?? 'all', sourceView, filters: options.filters, limit: options.limit ?? 10, cursor: options.cursor },
      signal: options.signal,
    });
  }

  searchChapters(body: OperationRequest<'searchChapters'>, options: Pick<ApiRequestOptions, 'signal'> = {}) {
    return this.request<'searchChapters', '200'>('searchChapters', { body, signal: options.signal });
  }

  listChatSessions(projectId: string, options: ListChatSessionsOptions) {
    const { query, signal } = splitListOptions(options);
    return this.request<'listChatSessions', '200'>('listChatSessions', {
      params: { path: { projectId }, query },
      signal,
    });
  }

  createChatSession(projectId: string, body: OperationRequest<'createChatSession'>, options: RequiredIdempotencyOptions) {
    return this.request<'createChatSession', '201'>('createChatSession', {
      params: { path: { projectId } },
      body,
      headers: idempotencyHeaders(options, 'createChatSession'),
      signal: options.signal,
    });
  }

  getChatSession(chatId: string) {
    return this.request<'getChatSession', '200'>('getChatSession', { params: { path: { chatId } } });
  }

  resolveChatChapterKey(chatId: string, chapterKey: string, options: Pick<ApiRequestOptions, 'signal'> = {}) {
    return this.request<'resolveChatChapterKey', '200'>('resolveChatChapterKey', {
      params: { path: { chatId, chapterKey } }, signal: options.signal,
    });
  }

  renameChatSession(chatId: string, body: OperationRequest<'updateChatSession'>) {
    return this.request<'updateChatSession', '200'>('updateChatSession', { params: { path: { chatId } }, body });
  }

  async deleteChatSession(chatId: string) {
    await this.request<'deleteChatSession', '204'>('deleteChatSession', { params: { path: { chatId } } });
  }

  createChatTurn(chatId: string, body: OperationRequest<'createChatTurn'>, options: RequiredIdempotencyOptions) {
    return this.request<'createChatTurn', '202'>('createChatTurn', {
      params: { path: { chatId } },
      body,
      headers: idempotencyHeaders(options, 'createChatTurn'),
      signal: options.signal,
    });
  }

  steerChatTurn(chatId: string, turnId: string, body: OperationRequest<'steerChatTurn'>, options: RequiredIdempotencyOptions) {
    return this.request<'steerChatTurn', '202'>('steerChatTurn', {
      params: { path: { chatId, turnId } },
      body,
      headers: idempotencyHeaders(options, 'steerChatTurn'),
      signal: options.signal,
    });
  }

  /** A question with the author's chapter attached: the question and the file or the pasted text in one request. */
  createChatTurnWithChapter(chatId: string, input: ChapterFileInput & { body: string }, options: RequiredIdempotencyOptions) {
    idempotencyHeaders(options, 'createChatTurnWithChapter');
    const form = new FormData();
    form.append('body', input.body);
    const chapter = chapterForm(input);
    for (const [name, value] of chapter.entries()) form.append(name, value);
    return this.postMultipart<'createChatTurnWithChapter', OperationResponse<'createChatTurnWithChapter', '202'>>(
      'createChatTurnWithChapter', { chatId }, form, options,
    );
  }

  getChatAttachment(chatId: string, number: number, options: Pick<ApiRequestOptions, 'signal'> = {}) {
    return this.request<'getChatAttachment', '200'>('getChatAttachment', {
      params: { path: { chatId, number } }, signal: options.signal,
    });
  }

}

export function createCanonKeeperApiClient(options?: CanonKeeperApiClientOptions) {
  return new OpenApiCanonKeeperClient(options);
}

/** One file or one text of a chapter as the form of its request (specs/2026-11-07-writing-proofread.md, PR2). */
/** A chapter attached in a chat, named by its chat and number (CA5.5). */
function chatChapterForm(chatId: string, attachment: number) {
  const form = new FormData();
  form.append('chatId', chatId);
  form.append('attachment', String(attachment));
  return form;
}

function chapterForm(input: ChapterFileInput) {
  const form = new FormData();
  if (input.file) form.append('file', input.file, input.file.name);
  else form.append('text', input.text);
  return form;
}
