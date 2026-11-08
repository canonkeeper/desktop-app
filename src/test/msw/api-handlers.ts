import { http } from 'msw';
import { createNotesHandlers } from './notes-handlers';
import { createPromoHandlers } from './promo-handlers';
import { createContactHandlers } from './contact-handlers';
import { createMockApiClient, type MockRepository } from '../../api/mock/repository';
import type { ProjectImageFileQuery } from '../../api/mock/repository/images';
import { MockApiError, type MockScenarioControl, type MockScenarioPreset } from '../../api/mock/scenarios';
import type {
  GetSagaContentsOptions,
  GetSagaMapNodeOptions,
  SearchSagaMapOptions,
  GetSagaMapOptions,
  ListSagaMapHiddenOptions,
  ImageUploadInput,
  ImportBookInput,
  AuthorMaterialsImportInput,
  BookExportFormat,
  ListAuthorMaterialsOptions,
  ListAuthorMemoryOptions,
  ListProjectImagesOptions,
  ListSagaGlossaryOptions,
  OperationId,
  OperationRequest,
  SourceView,
} from '../../shared/api';
import {
  cursorListQuery,
  listBooksForState,
  listChaptersForState,
  listProjectsForState,
  projectJobsQuery,
  readBookCoverForm,
  readChapterForm, readImageUploadForm, readMaterialsImportForm,
  readImportForm,
  readImportQueueItemForm,
  readJson,
} from './handler-input';
import {
  binaryResponse,
  clearSessionHeaders,
  createMswCsrfState,
  csrfHandler,
  emptyResponse,
  idempotentJsonResponse,
  jsonOrEmptyResponse,
  jsonResponse,
  mswPath,
  oneParam,
  repositoryCsrfStates,
  sessionHeaders,
  type MswCsrfState,
  type MswIdempotencyRecord,
  type MswSecurityContext,
} from './handler-runtime';

function createAdminHandlers(repository: MockRepository, csrfState: MswCsrfState) {
  const security = (operationId: OperationId, request: Request): MswSecurityContext => ({ csrfState, operationId, request, repository });
  return [
    http.get(mswPath('listAdminWriters'), ({ request }) =>
      jsonResponse(() => repository.listAdminWriters(), undefined, security('listAdminWriters', request))),
    http.get(mswPath('getAdminWriter'), ({ params, request }) =>
      jsonResponse(() => repository.getAdminWriter(oneParam(params.userId)), undefined, security('getAdminWriter', request))),
    http.put(mswPath('setAdminWriterQuota'), ({ params, request }) =>
      jsonResponse(
        async () => repository.setAdminWriterQuota(oneParam(params.userId), (await readJson(request)) as OperationRequest<'setAdminWriterQuota'>),
        undefined,
        security('setAdminWriterQuota', request),
      )),
    http.post(mswPath('blockAdminWriter'), ({ params, request }) =>
      jsonResponse(() => repository.blockAdminWriter(oneParam(params.userId)), undefined, security('blockAdminWriter', request))),
    http.post(mswPath('unblockAdminWriter'), ({ params, request }) =>
      jsonResponse(() => repository.unblockAdminWriter(oneParam(params.userId)), undefined, security('unblockAdminWriter', request))),
    http.get(mswPath('listAdminDays'), ({ request }) => {
      const days = new URL(request.url).searchParams.get('days');
      return jsonResponse(() => repository.listAdminDays(days === null ? {} : { days: Number(days) }), undefined, security('listAdminDays', request));
    }),
    http.get(mswPath('getAdminServiceStatus'), ({ request }) =>
      jsonResponse(() => repository.getAdminServiceStatus(), undefined, security('getAdminServiceStatus', request))),
    http.put(mswPath('setAdminWriterIllustrations'), ({ params, request }) =>
      jsonResponse(
        async () => repository.setAdminWriterIllustrations(oneParam(params.userId), (await readJson(request)) as OperationRequest<'setAdminWriterIllustrations'>),
        undefined,
        security('setAdminWriterIllustrations', request),
      )),
    http.get(mswPath('listAdminIllustrationRequests'), ({ request }) =>
      jsonResponse(() => repository.listAdminIllustrationRequests(), undefined, security('listAdminIllustrationRequests', request))),
    http.post(mswPath('declineAdminIllustrationRequest'), ({ params, request }) =>
      jsonResponse(
        async () => repository.declineAdminIllustrationRequest(oneParam(params.requestId), (await readJson(request)) as OperationRequest<'declineAdminIllustrationRequest'>),
        undefined,
        security('declineAdminIllustrationRequest', request),
      )),
    http.get(mswPath('getAdminQuotaDefaults'), ({ request }) =>
      jsonResponse(() => repository.getAdminQuotaDefaults(), undefined, security('getAdminQuotaDefaults', request))),
    http.put(mswPath('setAdminQuotaDefaults'), ({ request }) =>
      jsonResponse(
        async () => repository.setAdminQuotaDefaults((await readJson(request)) as OperationRequest<'setAdminQuotaDefaults'>),
        undefined,
        security('setAdminQuotaDefaults', request),
      )),
  ];
}

interface ApiHandlersOptions {
  preset?: MockScenarioPreset;
  repository?: MockRepository;
  scenario?: MockScenarioControl;
}

function createChatWriterCorrectionHandlers(
  repository: MockRepository,
  csrfState: MswCsrfState,
  idempotencyStore: Map<string, MswIdempotencyRecord>,
) {
  const security = (operationId: OperationId, request: Request): MswSecurityContext => ({
    csrfState,
    operationId,
    request,
    repository,
  });
  return [
    http.get(mswPath('listChatSessions'), ({ params, request }) => {
      return jsonResponse(() => repository.listChatSessions(oneParam(params.projectId), cursorListQuery(request)),
        undefined, security('listChatSessions', request));
    }),
    http.post(mswPath('createChatSession'), async ({ params, request }) => idempotentJsonResponse(
      (options, body) => repository.createChatSession(oneParam(params.projectId), body as OperationRequest<'createChatSession'>, options),
      { status: 201 },
      security('createChatSession', request),
      () => readJson(request),
      idempotencyStore,
    )),
    http.get(mswPath('getChatSession'), ({ params, request }) =>
      jsonResponse(() => repository.getChatSession(oneParam(params.chatId)), undefined, security('getChatSession', request))),
    http.get(mswPath('resolveChatChapterKey'), ({ params, request }) =>
      jsonResponse(() => repository.resolveChatChapterKey(oneParam(params.chatId), oneParam(params.chapterKey)), undefined, security('resolveChatChapterKey', request))),
    http.patch(mswPath('updateChatSession'), async ({ params, request }) =>
      jsonResponse(
        async () => repository.renameChatSession(oneParam(params.chatId), (await readJson(request)) as OperationRequest<'updateChatSession'>),
        undefined,
        security('updateChatSession', request),
      )),
    http.delete(mswPath('deleteChatSession'), ({ params, request }) =>
      emptyResponse(() => repository.deleteChatSession(oneParam(params.chatId)), security('deleteChatSession', request))),
    http.post(mswPath('steerChatTurn'), async ({ params, request }) => idempotentJsonResponse(
      (options, body) => repository.steerChatTurn(oneParam(params.chatId), oneParam(params.turnId), body as OperationRequest<'steerChatTurn'>, options),
      { status: 202 },
      security('steerChatTurn', request),
      () => readJson(request),
      idempotencyStore,
    )),
    // A question with the author's chapter attached (specs/2026-11-07-chat-attach-chapter.md CA2, CA4.3).
    http.post(mswPath('createChatTurnWithChapter'), async ({ params, request }) => idempotentJsonResponse(
      (options, body) => repository.createChatTurnWithChapter(oneParam(params.chatId), body as Parameters<typeof repository.createChatTurnWithChapter>[1], options),
      { status: 202 },
      security('createChatTurnWithChapter', request),
      () => readChapterForm(request),
      idempotencyStore,
    )),
    http.get(mswPath('getChatAttachment'), ({ params, request }) =>
      jsonResponse(() => repository.getChatAttachment(oneParam(params.chatId), Number(oneParam(params.number))), undefined, security('getChatAttachment', request))),
    http.post(mswPath('createChatTurn'), async ({ params, request }) => idempotentJsonResponse(
      (options, body) => repository.createChatTurn(oneParam(params.chatId), body as OperationRequest<'createChatTurn'>, options),
      { status: 202 },
      security('createChatTurn', request),
      () => readJson(request),
      idempotencyStore,
    )),
    http.get(mswPath('getAgentTimeline'), ({ params, request }) => {
      const cursor = new URL(request.url).searchParams.get('cursor') ?? undefined;
      return jsonResponse(() => repository.getAgentTimeline(
        oneParam(params.projectId),
        oneParam(params.jobId),
        { cursor },
      ), undefined, security('getAgentTimeline', request));
    }),
  ];
}

export function createApiHandlers(options: ApiHandlersOptions = {}) {
  const csrfState = createMswCsrfState();
  const repository = options.repository ?? createMockApiClient();
  const idempotencyStore = new Map<string, MswIdempotencyRecord>();
  repository.setScenario(options.scenario ?? { preset: options.preset ?? 'normal' });
  repositoryCsrfStates.set(repository, csrfState);

  return [
    csrfHandler(csrfState),
    ...createChatWriterCorrectionHandlers(repository, csrfState, idempotencyStore),
    ...createNotesHandlers(repository, csrfState, idempotencyStore),
    ...createPromoHandlers(repository, csrfState, idempotencyStore),
    ...createContactHandlers(repository, csrfState),
    http.get(mswPath('getCurrentUser'), ({ request }) =>
      jsonResponse(() => repository.getCurrentUser(), undefined, { csrfState, operationId: 'getCurrentUser', request, repository }),
    ),
    http.get(mswPath('getAccountQuota'), ({ request }) =>
      jsonResponse(() => repository.getAccountQuota(), undefined, { csrfState, operationId: 'getAccountQuota', request, repository }),
    ),
    http.get(mswPath('getIllustrationAccess'), ({ request }) =>
      jsonResponse(() => repository.getIllustrationAccess(), undefined, { csrfState, operationId: 'getIllustrationAccess', request, repository }),
    ),
    http.post(mswPath('createIllustrationAccessRequest'), ({ request }) =>
      jsonResponse(
        async () => repository.createIllustrationAccessRequest((await readJson(request)) as OperationRequest<'createIllustrationAccessRequest'>),
        { status: 201 },
        { csrfState, operationId: 'createIllustrationAccessRequest', request, repository },
      ),
    ),
    ...createAdminHandlers(repository, csrfState),
    http.post(mswPath('rotateSession'), ({ request }) =>
      jsonResponse(() => repository.rotateSession(), { headers: sessionHeaders() }, { csrfState, operationId: 'rotateSession', request, repository }),
    ),
    http.post(mswPath('reportClientError'), async ({ request }) => {
      const body = (await readJson(request)) as OperationRequest<'reportClientError'>;
      return emptyResponse(() => repository.reportClientError(body), { csrfState, operationId: 'reportClientError', request, repository });
    }),
    http.post(mswPath('logoutUser'), ({ request }) =>
      emptyResponse(() => repository.logout(), { csrfState, operationId: 'logoutUser', request, repository }, { headers: clearSessionHeaders() }),
    ),
    http.get(mswPath('listProjects'), ({ request }) =>
      jsonResponse(() => listProjectsForState(repository, request), undefined, { operationId: 'listProjects', request, repository }),
    ),
    http.post(mswPath('createProject'), async ({ request }) =>
      jsonResponse(async () => repository.createProject((await readJson(request)) as OperationRequest<'createProject'>), { status: 201 }, {
        operationId: 'createProject',
        request,
        repository,
      }),
    ),
    http.get(mswPath('getProject'), ({ params, request }) =>
      jsonResponse(() => repository.getProject(oneParam(params.projectId)), undefined, { operationId: 'getProject', request, repository }),
    ),
    http.get(mswPath('getProjectSupportSnapshot'), ({ params, request }) =>
      jsonResponse(() => repository.getProjectSupportSnapshot(oneParam(params.projectId)), undefined, {
        operationId: 'getProjectSupportSnapshot',
        request,
        repository,
      }),
    ),
    http.patch(mswPath('updateProject'), async ({ params, request }) =>
      jsonResponse(
        async () => repository.updateProject(oneParam(params.projectId), (await readJson(request)) as OperationRequest<'updateProject'>),
        undefined,
        { operationId: 'updateProject', request, repository },
      ),
    ),
    http.delete(mswPath('deleteProject'), ({ params, request }) =>
      emptyResponse(() => repository.deleteProject(oneParam(params.projectId)), { operationId: 'deleteProject', request, repository }),
    ),
    http.post(mswPath('restoreProject'), ({ params, request }) =>
      jsonResponse(() => repository.restoreProject(oneParam(params.projectId)), undefined, { operationId: 'restoreProject', request, repository }),
    ),
    http.get(mswPath('listBooks'), ({ params, request }) =>
      jsonResponse(() => listBooksForState(repository, oneParam(params.projectId), request), undefined, { operationId: 'listBooks', request, repository }),
    ),
    http.post(mswPath('createBook'), async ({ params, request }) =>
      jsonResponse(async () => repository.createBook(oneParam(params.projectId), (await readJson(request)) as OperationRequest<'createBook'>), {
        status: 201,
      }, { operationId: 'createBook', request, repository }),
    ),
    http.get(mswPath('getImportConstraints'), ({ params, request }) =>
      jsonResponse(() => repository.getImportConstraints(oneParam(params.projectId)), undefined, {
        operationId: 'getImportConstraints',
        request,
        repository,
      }),
    ),
    http.post(mswPath('importBookFile'), async ({ params, request }) => {
      return idempotentJsonResponse((options, importBody) => repository.importBookFile(oneParam(params.projectId), importBody as ImportBookInput, options), { status: 202 }, {
        operationId: 'importBookFile',
        request,
        repository,
      }, () => readImportForm(request), idempotencyStore);
    }),
    http.post(mswPath('createImportQueue'), async ({ params, request }) =>
      idempotentJsonResponse((options, body) => repository.createImportQueue(oneParam(params.projectId), body as OperationRequest<'createImportQueue'>, options), {
        status: 201,
      }, { operationId: 'createImportQueue', request, repository }, () => readJson(request), idempotencyStore)),
    http.get(mswPath('listImportQueues'), ({ params, request }) =>
      jsonResponse(() => repository.listImportQueues(oneParam(params.projectId)), undefined, {
        operationId: 'listImportQueues',
        request,
        repository,
      })),
    http.get(mswPath('getImportQueue'), ({ params, request }) =>
      jsonResponse(() => repository.getImportQueue(oneParam(params.projectId), oneParam(params.queueId)), undefined, {
        operationId: 'getImportQueue',
        request,
        repository,
      })),
    http.post(mswPath('uploadImportQueueItem'), async ({ params, request }) =>
      idempotentJsonResponse((options, body) => repository.uploadImportQueueItem(
        oneParam(params.projectId),
        oneParam(params.queueId),
        body as Parameters<MockRepository['uploadImportQueueItem']>[2],
        options,
      ), { status: 200 }, { operationId: 'uploadImportQueueItem', request, repository }, () => readImportQueueItemForm(request), idempotencyStore)),
    http.patch(mswPath('updateImportQueue'), async ({ params, request }) =>
      jsonResponse(async () => repository.updateImportQueue(
        oneParam(params.projectId),
        oneParam(params.queueId),
        (await readJson(request)) as OperationRequest<'updateImportQueue'>,
      ), undefined, { operationId: 'updateImportQueue', request, repository })),
    http.post(mswPath('startImportQueue'), async ({ params, request }) =>
      idempotentJsonResponse((options, body) => repository.startImportQueue(
        oneParam(params.projectId),
        oneParam(params.queueId),
        body as OperationRequest<'startImportQueue'>,
        options,
      ), { status: 202 }, { operationId: 'startImportQueue', request, repository }, () => readJson(request), idempotencyStore)),
    http.delete(mswPath('cancelImportQueue'), ({ params, request }) =>
      jsonResponse(() => repository.cancelImportQueue(oneParam(params.projectId), oneParam(params.queueId)), undefined, {
        operationId: 'cancelImportQueue',
        request,
        repository,
      })),
    http.get(mswPath('listProjectJobs'), ({ params, request }) =>
      jsonResponse(() => repository.listProjectJobs(oneParam(params.projectId), projectJobsQuery(request)), undefined, {
        operationId: 'listProjectJobs',
        request,
        repository,
      }),
    ),
    http.get(mswPath('getIndexingStatus'), ({ params, request }) => {
      const query = new URL(request.url).searchParams;
      const bookId = query.get('bookId') ?? undefined;
      const sourceView = query.get('sourceView') as SourceView | null;
      return jsonResponse(() => repository.getIndexingStatus(oneParam(params.projectId), {
        ...(bookId && sourceView ? { bookId, sourceView } : {}),
      }), undefined, {
        operationId: 'getIndexingStatus',
        request,
        repository,
      });
    }),
    http.get(mswPath('listSagaGlossary'), ({ params, request }) => jsonResponse(
      () => repository.listSagaGlossary(oneParam(params.projectId), sagaQuery(request) as ListSagaGlossaryOptions), undefined, { operationId: 'listSagaGlossary', request, repository },
    )),
    http.get(mswPath('getSagaMap'), ({ params, request }) => jsonResponse(
      () => repository.getSagaMap(oneParam(params.projectId), sagaQuery(request) as GetSagaMapOptions), undefined, { operationId: 'getSagaMap', request, repository },
    )),
    http.get(mswPath('getSagaMapNode'), ({ params, request }) => jsonResponse(
      () => repository.getSagaMapNode(oneParam(params.projectId), sagaQuery(request) as GetSagaMapNodeOptions), undefined, { operationId: 'getSagaMapNode', request, repository },
    )),
    http.get(mswPath('searchSagaMap'), ({ params, request }) => jsonResponse(
      () => repository.searchSagaMap(oneParam(params.projectId), sagaQuery(request) as SearchSagaMapOptions), undefined, { operationId: 'searchSagaMap', request, repository },
    )),
    http.get(mswPath('listSagaMapHidden'), ({ params, request }) => jsonResponse(
      () => repository.listSagaMapHidden(oneParam(params.projectId), sagaQuery(request) as ListSagaMapHiddenOptions), undefined, { operationId: 'listSagaMapHidden', request, repository },
    )),
    http.post(mswPath('hideSagaMapItem'), async ({ params, request }) => {
      const body = (await readJson(request)) as OperationRequest<'hideSagaMapItem'>;
      return jsonResponse(
        () => repository.hideSagaMapItem(oneParam(params.projectId), sagaQuery(request).sourceView as SourceView, body), { status: 201 }, { operationId: 'hideSagaMapItem', request, repository },
      );
    }),
    http.delete(mswPath('restoreSagaMapItem'), ({ params, request }) => emptyResponse(
      () => repository.restoreSagaMapItem(oneParam(params.projectId), oneParam(params.hiddenId)), { operationId: 'restoreSagaMapItem', request, repository },
    )),
    http.get(mswPath('getSagaContents'), ({ params, request }) => jsonResponse(
      () => repository.getSagaContents(oneParam(params.projectId), sagaQuery(request) as GetSagaContentsOptions), undefined, { operationId: 'getSagaContents', request, repository },
    )),
    http.get(mswPath('listChapterDrafts'), ({ params, request }) => jsonResponse(
      () => repository.listChapterDrafts(oneParam(params.projectId), cursorListQuery(request)),
      undefined, { operationId: 'listChapterDrafts', request, repository },
    )),
    http.post(mswPath('createChapterDraft'), async ({ params, request }) => idempotentJsonResponse(
      (options, body) => repository.createChapterDraft(oneParam(params.projectId), body as OperationRequest<'createChapterDraft'>, options),
      { status: 201 }, { operationId: 'createChapterDraft', request, repository }, () => readJson(request), idempotencyStore,
    )),
    http.get(mswPath('getChapterDraftPlan'), ({ params, request }) => jsonResponse(
      () => repository.getChapterDraftPlan(oneParam(params.projectId), oneParam(params.draftId)),
      undefined, { operationId: 'getChapterDraftPlan', request, repository },
    )),
    http.get(mswPath('getChapterDraftMoment'), ({ params, request }) => jsonResponse(
      () => repository.getChapterDraftMoment(oneParam(params.projectId), oneParam(params.draftId)),
      undefined, { operationId: 'getChapterDraftMoment', request, repository },
    )),
    http.put(mswPath('updateChapterDraftPlan'), async ({ params, request }) => jsonResponse(
      async () => repository.updateChapterDraftPlan(oneParam(params.projectId), oneParam(params.draftId), (await readJson(request)) as OperationRequest<'updateChapterDraftPlan'>),
      undefined, { operationId: 'updateChapterDraftPlan', request, repository },
    )),
    http.patch(mswPath('updateChapterDraftMoment'), async ({ params, request }) => jsonResponse(
      async () => repository.updateChapterDraftMoment(oneParam(params.projectId), oneParam(params.draftId), (await readJson(request)) as OperationRequest<'updateChapterDraftMoment'>),
      undefined, { operationId: 'updateChapterDraftMoment', request, repository },
    )),
    http.get(mswPath('listChapterDraftVersions'), ({ params, request }) => jsonResponse(
      () => repository.listChapterDraftVersions(oneParam(params.projectId), oneParam(params.draftId)),
      undefined, { operationId: 'listChapterDraftVersions', request, repository },
    )),
    http.get(mswPath('getChapterDraftVersion'), ({ params, request }) => jsonResponse(
      () => repository.getChapterDraftVersion(oneParam(params.projectId), oneParam(params.draftId), Number(oneParam(params.number))),
      undefined, { operationId: 'getChapterDraftVersion', request, repository },
    )),
    http.post(mswPath('takeChapterDraftVersion'), async ({ params, request }) => jsonResponse(
      async () => repository.takeChapterDraftVersion(oneParam(params.projectId), oneParam(params.draftId), Number(oneParam(params.number)), (await readJson(request)) as OperationRequest<'takeChapterDraftVersion'>),
      undefined, { operationId: 'takeChapterDraftVersion', request, repository },
    )),
    http.get(mswPath('getChapterDraft'), ({ params, request }) => jsonResponse(
      () => repository.getChapterDraft(oneParam(params.projectId), oneParam(params.draftId)),
      undefined, { operationId: 'getChapterDraft', request, repository },
    )),
    http.get(mswPath('getProjectVoice'), ({ params, request }) => jsonResponse(
      () => repository.getProjectVoice(oneParam(params.projectId)),
      undefined, { operationId: 'getProjectVoice', request, repository },
    )),
    http.put(mswPath('setAccountVoice'), async ({ request }) => jsonResponse(
      async () => repository.setAccountVoice((await readJson(request)) as OperationRequest<'setAccountVoice'>),
      undefined, { operationId: 'setAccountVoice', request, repository },
    )),
    http.patch(mswPath('updateChapterDraft'), async ({ params, request }) => jsonResponse(
      async () => repository.updateChapterDraft(oneParam(params.projectId), oneParam(params.draftId), (await readJson(request)) as OperationRequest<'updateChapterDraft'>),
      undefined, { operationId: 'updateChapterDraft', request, repository },
    )),
    http.delete(mswPath('deleteChapterDraft'), ({ params, request }) => emptyResponse(
      () => repository.deleteChapterDraft(oneParam(params.projectId), oneParam(params.draftId), Number(new URL(request.url).searchParams.get('expectedRevision'))),
      { operationId: 'deleteChapterDraft', request, repository },
    )),
    http.post(mswPath('startChapterDraftJob'), async ({ params, request }) => idempotentJsonResponse(
      (options, body) => repository.startChapterDraftJob(oneParam(params.projectId), oneParam(params.draftId), body as OperationRequest<'startChapterDraftJob'>, options),
      { status: 201 }, { operationId: 'startChapterDraftJob', request, repository }, () => readJson(request), idempotencyStore,
    )),
    http.post(mswPath('openChapterEdit'), ({ params, request }) => jsonResponse(
      async () => repository.openChapterEdit(oneParam(params.projectId), oneParam(params.chapterId)),
      undefined, { operationId: 'openChapterEdit', request, repository },
    )),
    http.post(mswPath('replaceChapterFromDraft'), async ({ params, request }) => jsonResponse(
      async () => repository.replaceChapterFromDraft(oneParam(params.projectId), oneParam(params.draftId), (await readJson(request)) as OperationRequest<'replaceChapterFromDraft'>),
      undefined, { operationId: 'replaceChapterFromDraft', request, repository },
    )),
    http.post(mswPath('reopenChapterFromBook'), async ({ params, request }) => jsonResponse(
      async () => repository.reopenChapterFromBook(oneParam(params.projectId), oneParam(params.draftId), (await readJson(request)) as OperationRequest<'reopenChapterFromBook'>),
      undefined, { operationId: 'reopenChapterFromBook', request, repository },
    )),
    http.post(mswPath('revertChapterFromDraft'), async ({ params, request }) => jsonResponse(
      async () => repository.revertChapterFromDraft(oneParam(params.projectId), oneParam(params.draftId), (await readJson(request)) as OperationRequest<'revertChapterFromDraft'>),
      undefined, { operationId: 'revertChapterFromDraft', request, repository },
    )),
    http.post(mswPath('addChapterDraftToBook'), async ({ params, request }) => jsonResponse(
      async () => repository.addChapterDraftToBook(oneParam(params.projectId), oneParam(params.draftId), (await readJson(request)) as OperationRequest<'addChapterDraftToBook'>),
      undefined, { operationId: 'addChapterDraftToBook', request, repository },
    )),
    // The proofreading of a chapter of the author (specs/2026-11-07-writing-proofread.md, PR2, PR4.4, PR6).
    http.post(mswPath('startChapterProofread'), async ({ params, request }) => idempotentJsonResponse(
      (options, body) => repository.startChapterProofread(oneParam(params.projectId), body as Parameters<typeof repository.startChapterProofread>[1], options),
      { status: 201 }, { operationId: 'startChapterProofread', request, repository }, () => readChapterForm(request), idempotencyStore,
    )),
    http.post(mswPath('loadChapterDraftFile'), async ({ params, request }) => idempotentJsonResponse(
      (options, body) => repository.loadChapterDraftFile(oneParam(params.projectId), oneParam(params.draftId), body as Parameters<typeof repository.loadChapterDraftFile>[2], options),
      { status: 200 }, { operationId: 'loadChapterDraftFile', request, repository }, () => readChapterForm(request), idempotencyStore,
    )),
    http.post(mswPath('applyChapterDraftNotes'), async ({ params, request }) => jsonResponse(
      async () => repository.applyChapterDraftNotes(oneParam(params.projectId), oneParam(params.draftId), (await readJson(request)) as OperationRequest<'applyChapterDraftNotes'>),
      undefined, { operationId: 'applyChapterDraftNotes', request, repository },
    )),
    http.get(mswPath('getChapterDraftExport'), ({ params, request }) => binaryResponse(async () => {
      const file = await repository.getChapterDraftExport(oneParam(params.projectId), oneParam(params.draftId));
      return {
        body: `Файл ${file.fileName}`,
        contentType: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
        headers: { 'Content-Disposition': `attachment; filename*=utf-8''${encodeURIComponent(file.fileName)}` },
      };
    }, { operationId: 'getChapterDraftExport', request, repository })),
    http.post(mswPath('applyChapterDraftNote'), async ({ params, request }) => jsonResponse(
      async () => repository.applyChapterDraftNote(oneParam(params.projectId), oneParam(params.draftId), oneParam(params.noteId), (await readJson(request)) as OperationRequest<'applyChapterDraftNote'>),
      undefined, { operationId: 'applyChapterDraftNote', request, repository },
    )),
    http.patch(mswPath('updateChapterDraftNote'), async ({ params, request }) => jsonResponse(
      async () => repository.updateChapterDraftNote(oneParam(params.projectId), oneParam(params.draftId), oneParam(params.noteId), (await readJson(request)) as OperationRequest<'updateChapterDraftNote'>),
      undefined, { operationId: 'updateChapterDraftNote', request, repository },
    )),
    http.post(mswPath('submitDraftTurn'), async ({ params, request }) => idempotentJsonResponse(
      (options, body) => repository.submitDraftTurn(oneParam(params.projectId), oneParam(params.draftId), body as OperationRequest<'submitDraftTurn'>, options),
      { status: 202 }, { operationId: 'submitDraftTurn', request, repository }, () => readJson(request), idempotencyStore,
    )),
    http.get(mswPath('listAuthorMaterials'), ({ params, request }) => jsonResponse(() => {
      const query = new URL(request.url).searchParams;
      return repository.listAuthorMaterials(oneParam(params.projectId), {
        ...(query.get('kind') ? { kind: query.get('kind') as NonNullable<ListAuthorMaterialsOptions['kind']> } : {}),
        ...(query.get('q') ? { q: query.get('q') as string } : {}),
      });
    }, undefined, { operationId: 'listAuthorMaterials', request, repository })),
    http.put(mswPath('updateAuthorMaterial'), async ({ params, request }) => jsonResponse(
      async () => repository.updateAuthorMaterial(
        oneParam(params.projectId), oneParam(params.materialId), (await readJson(request)) as OperationRequest<'updateAuthorMaterial'>,
      ), undefined, { operationId: 'updateAuthorMaterial', request, repository },
    )),
    http.delete(mswPath('deleteAuthorMaterial'), ({ params, request }) => emptyResponse(
      () => repository.deleteAuthorMaterial(
        oneParam(params.projectId), oneParam(params.materialId), Number(new URL(request.url).searchParams.get('expectedRevision')),
      ), { operationId: 'deleteAuthorMaterial', request, repository },
    )),
    http.post(mswPath('startAuthorMaterialsImport'), async ({ params, request }) => idempotentJsonResponse(
      (options, body) => repository.startAuthorMaterialsImport(oneParam(params.projectId), body as AuthorMaterialsImportInput, options),
      { status: 202 }, { operationId: 'startAuthorMaterialsImport', request, repository }, () => readMaterialsImportForm(request), idempotencyStore,
    )),
    http.get(mswPath('getAuthorMaterialsImport'), ({ params, request }) => jsonResponse(
      () => repository.getAuthorMaterialsImport(oneParam(params.projectId)), undefined, { operationId: 'getAuthorMaterialsImport', request, repository },
    )),
    http.post(mswPath('confirmAuthorMaterialsImport'), async ({ params, request }) => idempotentJsonResponse(
      (options, body) => repository.confirmAuthorMaterialsImport(oneParam(params.projectId), oneParam(params.importId), body as OperationRequest<'confirmAuthorMaterialsImport'>, options),
      { status: 200 }, { operationId: 'confirmAuthorMaterialsImport', request, repository }, () => readJson(request), idempotencyStore,
    )),
    http.delete(mswPath('discardAuthorMaterialsImport'), ({ params, request }) => emptyResponse(
      () => repository.discardAuthorMaterialsImport(oneParam(params.projectId), oneParam(params.importId)),
      { operationId: 'discardAuthorMaterialsImport', request, repository },
    )),
    http.get(mswPath('listAuthorMemory'), ({ params, request }) => jsonResponse(() => {
      const query = new URL(request.url).searchParams;
      return repository.listAuthorMemory(oneParam(params.projectId), {
        ...(query.get('kind') ? { kind: query.get('kind') as NonNullable<ListAuthorMemoryOptions['kind']> } : {}),
        ...(query.get('q') ? { q: query.get('q')! } : {}),
        ...cursorListQuery(request),
      });
    }, undefined, { operationId: 'listAuthorMemory', request, repository })),
    http.post(mswPath('createAuthorMemory'), async ({ params, request }) => idempotentJsonResponse(
      (options, body) => repository.createAuthorMemory(oneParam(params.projectId), body as OperationRequest<'createAuthorMemory'>, options),
      { status: 201 }, { operationId: 'createAuthorMemory', request, repository }, () => readJson(request), idempotencyStore,
    )),
    http.put(mswPath('updateAuthorMemory'), async ({ params, request }) => jsonResponse(
      async () => repository.updateAuthorMemory(
        oneParam(params.projectId), oneParam(params.recordId), (await readJson(request)) as OperationRequest<'updateAuthorMemory'>,
      ), undefined, { operationId: 'updateAuthorMemory', request, repository },
    )),
    http.delete(mswPath('deleteAuthorMemory'), ({ params, request }) => emptyResponse(
      () => repository.deleteAuthorMemory(
        oneParam(params.projectId), oneParam(params.recordId), Number(new URL(request.url).searchParams.get('expectedRevision')),
      ), { operationId: 'deleteAuthorMemory', request, repository },
    )),
    http.get(mswPath('listProjectImages'), ({ params, request }) => jsonResponse(() => {
      const query = new URL(request.url).searchParams;
      return repository.listProjectImages(oneParam(params.projectId), {
        ...(query.get('subject') ? { subject: query.get('subject')! } : {}),
        ...(query.get('chapter') ? { chapter: query.get('chapter')! } : {}),
        ...(query.get('kind') ? { kind: query.get('kind') as ListProjectImagesOptions['kind'] } : {}),
        ...(query.get('turn') ? { turn: query.get('turn')! } : {}),
        ...(query.get('branch') ? { branch: Number(query.get('branch')) } : {}),
        ...(query.get('references') === 'true' ? { references: true } : {}),
        ...(query.get('group') ? { group: query.get('group') as ListProjectImagesOptions['group'] } : {}),
        ...cursorListQuery(request),
      });
    }, undefined, { operationId: 'listProjectImages', request, repository })),
    http.get(mswPath('getProjectImage'), ({ params, request }) => jsonResponse(
      () => repository.getProjectImage(oneParam(params.projectId), Number(oneParam(params.number))), undefined, { operationId: 'getProjectImage', request, repository },
    )),
    http.get(mswPath('getProjectImageFile'), ({ params, request }) => binaryResponse(async () => {
      const query = new URL(request.url).searchParams;
      const size = query.get('size');
      const file = await repository.getProjectImageFile(oneParam(params.projectId), Number(oneParam(params.number)), {
        ...(size ? { size: size as ProjectImageFileQuery['size'] } : {}),
        ...(query.get('layer') ? { layer: query.get('layer')! } : {}),
        ...(query.get('v') ? { v: Number(query.get('v')) } : {}),
      });
      return { body: file.content, contentType: file.mimeType, status: 200, cacheControl: file.cacheControl };
    }, { operationId: 'getProjectImageFile', request, repository })),
    http.put(mswPath('updateProjectImageDetails'), async ({ params, request }) => jsonResponse(
      async () => repository.updateProjectImageDetails(
        oneParam(params.projectId), Number(oneParam(params.number)), (await readJson(request)) as OperationRequest<'updateProjectImageDetails'>,
      ), undefined, { operationId: 'updateProjectImageDetails', request, repository },
    )),
    http.put(mswPath('setProjectImageReference'), async ({ params, request }) => jsonResponse(
      async () => repository.setProjectImageReference(
        oneParam(params.projectId), Number(oneParam(params.number)), (await readJson(request)) as OperationRequest<'setProjectImageReference'>,
      ), undefined, { operationId: 'setProjectImageReference', request, repository },
    )),
    http.delete(mswPath('removeProjectImageReference'), ({ params, request }) => jsonResponse(() => {
      const query = new URL(request.url).searchParams;
      return repository.removeProjectImageReference(oneParam(params.projectId), Number(oneParam(params.number)), {
        subject: query.get('subject') ?? '',
        ...(query.get('fromChapter') ? { fromChapter: query.get('fromChapter')! } : {}),
      });
    }, undefined, { operationId: 'removeProjectImageReference', request, repository })),
    http.delete(mswPath('deleteProjectImage'), ({ params, request }) => emptyResponse(
      () => repository.deleteProjectImage(oneParam(params.projectId), Number(oneParam(params.number))), { operationId: 'deleteProjectImage', request, repository },
    )),
    http.post(mswPath('deleteProjectImages'), async ({ params, request }) => idempotentJsonResponse(
      (options, body) => repository.deleteProjectImages(oneParam(params.projectId), body as OperationRequest<'deleteProjectImages'>, options),
      { status: 200 }, { operationId: 'deleteProjectImages', request, repository }, () => readJson(request), idempotencyStore,
    )),
    http.post(mswPath('cutoutProjectImage'), async ({ params, request }) => idempotentJsonResponse(
      (options) => repository.cutoutProjectImage(oneParam(params.projectId), Number(oneParam(params.number)), options),
      { status: 201 }, { operationId: 'cutoutProjectImage', request, repository }, {}, idempotencyStore,
    )),
    http.post(mswPath('chooseProjectImageVariant'), async ({ params, request }) => idempotentJsonResponse(
      (options) => repository.chooseProjectImageVariant(oneParam(params.projectId), Number(oneParam(params.number)), options),
      { status: 200 }, { operationId: 'chooseProjectImageVariant', request, repository }, {}, idempotencyStore,
    )),
    http.post(mswPath('chooseProjectImageLayerVariant'), async ({ params, request }) => idempotentJsonResponse(
      (options) => repository.chooseProjectImageLayerVariant(oneParam(params.projectId), Number(oneParam(params.number)), oneParam(params.layerId), options),
      { status: 200 }, { operationId: 'chooseProjectImageLayerVariant', request, repository }, {}, idempotencyStore,
    )),
    http.get(mswPath('listDrawings'), ({ params, request }) => jsonResponse(() => {
      const query = new URL(request.url).searchParams;
      const limit = query.get('limit');
      return repository.listDrawings(oneParam(params.projectId), limit === null ? {} : { limit: Number(limit) });
    }, undefined, { operationId: 'listDrawings', request, repository })),
    http.post(mswPath('createDrawing'), async ({ params, request }) => idempotentJsonResponse(
      (options, body) => repository.createDrawing(oneParam(params.projectId), body as OperationRequest<'createDrawing'>, options),
      { status: 201 }, { operationId: 'createDrawing', request, repository }, () => readJson(request), idempotencyStore,
    )),
    http.get(mswPath('getDrawing'), ({ params, request }) => jsonResponse(
      () => repository.getDrawing(oneParam(params.projectId), oneParam(params.drawingId)),
      undefined, { operationId: 'getDrawing', request, repository },
    )),
    http.patch(mswPath('updateDrawing'), async ({ params, request }) => jsonResponse(
      async () => repository.updateDrawing(oneParam(params.projectId), oneParam(params.drawingId), (await readJson(request)) as OperationRequest<'updateDrawing'>),
      undefined, { operationId: 'updateDrawing', request, repository },
    )),
    http.delete(mswPath('deleteDrawing'), ({ params, request }) => emptyResponse(
      () => repository.deleteDrawing(oneParam(params.projectId), oneParam(params.drawingId)),
      { operationId: 'deleteDrawing', request, repository },
    )),
    http.post(mswPath('submitDrawingTurn'), async ({ params, request }) => idempotentJsonResponse(
      (options, body) => repository.submitDrawingTurn(oneParam(params.projectId), oneParam(params.drawingId), body as OperationRequest<'submitDrawingTurn'>, options),
      { status: 202 }, { operationId: 'submitDrawingTurn', request, repository }, () => readJson(request), idempotencyStore,
    )),
    http.post(mswPath('drawDrawing'), async ({ params, request }) => idempotentJsonResponse(
      (options, body) => repository.drawDrawing(oneParam(params.projectId), oneParam(params.drawingId), body as OperationRequest<'drawDrawing'>, options),
      { status: 201 }, { operationId: 'drawDrawing', request, repository }, () => readJson(request), idempotencyStore,
    )),
    http.post(mswPath('createProjectImageVariant'), async ({ params, request }) => idempotentJsonResponse(
      (options, body) => repository.createProjectImageVariant(oneParam(params.projectId), Number(oneParam(params.number)), body as OperationRequest<'createProjectImageVariant'>, options),
      { status: 201 }, { operationId: 'createProjectImageVariant', request, repository }, async () => { const text = await request.clone().text(); return text ? JSON.parse(text) as unknown : {}; }, idempotencyStore,
    )),    http.post(mswPath('enlargeProjectImage'), async ({ params, request }) => idempotentJsonResponse(
      (options) => repository.enlargeProjectImage(oneParam(params.projectId), Number(oneParam(params.number)), options),
      { status: 201 }, { operationId: 'enlargeProjectImage', request, repository }, {}, idempotencyStore,
    )),
    http.post(mswPath('createProjectImageLayer'), async ({ params, request }) => idempotentJsonResponse(
      (options, body) => repository.createProjectImageLayer(
        oneParam(params.projectId), Number(oneParam(params.number)), body as OperationRequest<'createProjectImageLayer'>, options,
      ),
      { status: 201 }, { operationId: 'createProjectImageLayer', request, repository }, () => readJson(request), idempotencyStore,
    )),
    http.patch(mswPath('updateProjectImageLayer'), async ({ params, request }) => jsonResponse(
      async () => repository.updateProjectImageLayer(
        oneParam(params.projectId), Number(oneParam(params.number)), oneParam(params.layerId),
        (await readJson(request)) as OperationRequest<'updateProjectImageLayer'>,
      ), undefined, { operationId: 'updateProjectImageLayer', request, repository },
    )),
    http.delete(mswPath('deleteProjectImageLayer'), ({ params, request }) => jsonResponse(
      () => repository.deleteProjectImageLayer(oneParam(params.projectId), Number(oneParam(params.number)), oneParam(params.layerId)),
      undefined, { operationId: 'deleteProjectImageLayer', request, repository },
    )),
    http.post(mswPath('createProjectCharacterSheet'), async ({ params, request }) => idempotentJsonResponse(
      (options, body) => repository.createProjectCharacterSheet(oneParam(params.projectId), body as OperationRequest<'createProjectCharacterSheet'>, options),
      { status: 201 }, { operationId: 'createProjectCharacterSheet', request, repository }, () => readJson(request), idempotencyStore,
    )),
    http.post(mswPath('createIllustrationStyleSketches'), async ({ params, request }) => idempotentJsonResponse(
      (options, body) => repository.createIllustrationStyleSketches(oneParam(params.projectId), body as OperationRequest<'createIllustrationStyleSketches'>, options),
      { status: 201 }, { operationId: 'createIllustrationStyleSketches', request, repository }, () => readJson(request), idempotencyStore,
    )),
    http.post(mswPath('uploadProjectImage'), async ({ params, request }) => idempotentJsonResponse(
      (options, body) => repository.uploadProjectImage(oneParam(params.projectId), body as ImageUploadInput, options),
      { status: 201 }, { operationId: 'uploadProjectImage', request, repository }, () => readImageUploadForm(request), idempotencyStore,
    )),
    http.get(mswPath('getProjectIllustrationStyle'), ({ params, request }) => jsonResponse(
      () => repository.getProjectIllustrationStyle(oneParam(params.projectId)), undefined, { operationId: 'getProjectIllustrationStyle', request, repository },
    )),
    http.put(mswPath('updateProjectIllustrationStyle'), async ({ params, request }) => jsonResponse(
      async () => repository.updateProjectIllustrationStyle(
        oneParam(params.projectId), (await readJson(request)) as OperationRequest<'updateProjectIllustrationStyle'>,
      ), undefined, { operationId: 'updateProjectIllustrationStyle', request, repository },
    )),
    http.post(mswPath('startProjectIndexing'), async ({ params, request }) => {
      return idempotentJsonResponse((options, body) => repository.startProjectIndexing(oneParam(params.projectId), body as OperationRequest<'startProjectIndexing'>, options), {
        status: 202,
      }, { operationId: 'startProjectIndexing', request, repository }, () => readJson(request), idempotencyStore);
    }),
    http.get(mswPath('getJob'), ({ params, request }) =>
      jsonResponse(() => repository.getJob(oneParam(params.jobId)), undefined, { operationId: 'getJob', request, repository }),
    ),
    http.post(mswPath('cancelJob'), ({ params, request }) =>
      jsonOrEmptyResponse(() => repository.cancelJob(oneParam(params.jobId)), { operationId: 'cancelJob', request, repository }),
    ),
    http.get(mswPath('getBook'), ({ params, request }) =>
      jsonResponse(() => repository.getBook(oneParam(params.bookId)), undefined, { operationId: 'getBook', request, repository }),
    ),
    http.post(mswPath('uploadBookCover'), async ({ params, request }) =>
      jsonResponse(
        async () => repository.uploadBookCover(oneParam(params.bookId), await readBookCoverForm(request)),
        undefined,
        { operationId: 'uploadBookCover', request, repository },
      )),
    http.post(mswPath('setBookCoverFromImage'), async ({ params, request }) =>
      jsonResponse(
        async () => repository.setBookCoverFromImage(oneParam(params.bookId), ((await readJson(request)) as OperationRequest<'setBookCoverFromImage'>).imageNumber),
        undefined,
        { operationId: 'setBookCoverFromImage', request, repository },
      )),
    http.get(mswPath('getBookExport'), ({ params, request }) => binaryResponse(async () => {
      const format = new URL(request.url).searchParams.get('format') as BookExportFormat;
      const file = await repository.getBookExport(oneParam(params.bookId), format);
      return {
        body: `Файл ${file.fileName}`,
        contentType: 'application/octet-stream',
        headers: { 'Content-Disposition': `attachment; filename*=utf-8''${encodeURIComponent(file.fileName)}` },
      };
    }, { operationId: 'getBookExport', request, repository })),
    http.get(mswPath('getBookCover'), ({ params, request }) => binaryResponse(async () => {
      const bookId = oneParam(params.bookId);
      const version = oneParam(params.version);
      const book = await repository.getBook(bookId);
      if (book.coverUrl !== `/api/v1/books/${bookId}/cover/${version}`) {
        throw new MockApiError(404, 'Cover not found.', 'getBookCover');
      }
      const etag = `"mock-cover-${version}"`;
      const notModified = request.headers.get('If-None-Match') === etag;
      return {
        body: notModified ? null : mockCoverPNG,
        contentType: 'image/png',
        status: notModified ? 304 : 200,
        cacheControl: 'private, max-age=31536000, immutable',
        headers: { ETag: etag },
      };
    }, { operationId: 'getBookCover', request, repository })),
    http.delete(mswPath('deleteBookCover'), ({ params, request }) =>
      jsonResponse(() => repository.deleteBookCover(oneParam(params.bookId)), undefined, { operationId: 'deleteBookCover', request, repository })),
    http.patch(mswPath('updateBook'), async ({ params, request }) =>
      jsonResponse(async () => repository.updateBook(oneParam(params.bookId), (await readJson(request)) as OperationRequest<'updateBook'>), undefined, {
        operationId: 'updateBook',
        request,
        repository,
      }),
    ),
    http.delete(mswPath('deleteBook'), ({ params, request }) =>
      emptyResponse(() => repository.deleteBook(oneParam(params.bookId)), { operationId: 'deleteBook', request, repository }),
    ),
    http.post(mswPath('restoreBook'), ({ params, request }) =>
      jsonResponse(() => repository.restoreBook(oneParam(params.bookId)), undefined, { operationId: 'restoreBook', request, repository }),
    ),
    http.get(mswPath('listChapters'), ({ params, request }) =>
      jsonResponse(() => listChaptersForState(repository, oneParam(params.bookId), request), undefined, { operationId: 'listChapters', request, repository }),
    ),
    http.get(mswPath('getChapter'), ({ params, request }) =>
      jsonResponse(() => repository.getChapter(
        oneParam(params.chapterId),
        (new URL(request.url).searchParams.get('contentVariant') ?? 'draft') as SourceView,
      ), undefined, { operationId: 'getChapter', request, repository }),
    ),
    http.delete(mswPath('deleteChapter'), ({ params, request }) =>
      emptyResponse(() => repository.deleteChapter(oneParam(params.chapterId)), { operationId: 'deleteChapter', request, repository }),
    ),
    http.post(mswPath('restoreChapter'), ({ params, request }) =>
      jsonResponse(() => repository.restoreChapter(oneParam(params.chapterId)), undefined, { operationId: 'restoreChapter', request, repository }),
    ),
    http.get(mswPath('listProjectMembers'), ({ params, request }) =>
      jsonResponse(() => repository.listProjectMembers(oneParam(params.projectId), cursorListQuery(request)), undefined, {
        operationId: 'listProjectMembers',
        request,
        repository,
      }),
    ),
    http.patch(mswPath('updateProjectMemberRole'), async ({ params, request }) => {
      const body = (await readJson(request)) as OperationRequest<'updateProjectMemberRole'>;
      return jsonResponse(() => repository.updateProjectMemberRole(oneParam(params.projectId), oneParam(params.memberId), body), undefined, {
        operationId: 'updateProjectMemberRole',
        request,
        repository,
      });
    }),
    http.delete(mswPath('removeProjectMember'), ({ params, request }) =>
      emptyResponse(async () => {
        await repository.removeProjectMember(oneParam(params.projectId), oneParam(params.memberId));
      }, { operationId: 'removeProjectMember', request, repository }),
    ),
    http.get(mswPath('listProjectInvitations'), ({ params, request }) =>
      jsonResponse(() => repository.listProjectInvitations(oneParam(params.projectId), cursorListQuery(request)), undefined, {
        operationId: 'listProjectInvitations',
        request,
        repository,
      }),
    ),
    http.get(mswPath('listMyProjectInvitations'), ({ request }) =>
      jsonResponse(() => repository.listMyProjectInvitations(cursorListQuery(request)), undefined, {
        operationId: 'listMyProjectInvitations',
        request,
        repository,
      }),
    ),
    http.post(mswPath('createProjectInvitation'), async ({ params, request }) => {
      const body = (await readJson(request)) as OperationRequest<'createProjectInvitation'>;
      return jsonResponse(() => repository.createProjectInvitation(oneParam(params.projectId), body), { status: 201 }, {
        operationId: 'createProjectInvitation',
        request,
        repository,
      });
    }),
    http.post(mswPath('cancelProjectInvitation'), ({ params, request }) =>
      jsonResponse(() => repository.cancelProjectInvitation(oneParam(params.invitationId)), undefined, {
        operationId: 'cancelProjectInvitation',
        request,
        repository,
      }),
    ),
    http.post(mswPath('acceptProjectInvitation'), ({ params, request }) =>
      jsonResponse(() => repository.acceptProjectInvitation(oneParam(params.invitationId)), undefined, {
        operationId: 'acceptProjectInvitation',
        request,
        repository,
      }),
    ),
    http.post(mswPath('searchChapters'), async ({ request }) => jsonResponse(
      async () => repository.searchChapters(await readJson(request) as OperationRequest<'searchChapters'>),
      undefined, { operationId: 'searchChapters', request, repository },
    )),
    http.post(mswPath('searchProject'), async ({ params, request }) => {
      return jsonResponse(async () => {
        const body = (await readJson(request)) as OperationRequest<'searchProject'>;
        const { query, scope, sourceView, ...options } = body as OperationRequest<'searchProject'> & Record<string, unknown>;
        return repository.searchProject(oneParam(params.projectId), query, sourceView, scope, options, { allowSignal: false });
      }, undefined, {
        operationId: 'searchProject',
        request,
        repository,
      });
    }),
  ];
}

const mockCoverPNG = Uint8Array.from([
  137, 80, 78, 71, 13, 10, 26, 10, 0, 0, 0, 13, 73, 72, 68, 82,
  0, 0, 0, 1, 0, 0, 0, 1, 8, 6, 0, 0, 0, 31, 21, 196, 137,
  0, 0, 0, 13, 73, 68, 65, 84, 8, 215, 99, 248, 207, 192, 240, 31,
  0, 5, 0, 1, 255, 137, 153, 61, 29, 0, 0, 0, 0, 73, 69, 78, 68,
  174, 66, 96, 130,
]);

/** Saga reads take single query values; offset and limit are numbers. */
function sagaQuery(request: Request) {
  const options: Record<string, unknown> = {};
  for (const [key, value] of new URL(request.url).searchParams) {
    options[key] = key === 'offset' || key === 'limit' ? Number(value) : value;
  }
  return options;
}
