package governance

type ProjectRole string

const (
	ProjectRoleViewer ProjectRole = "viewer"
	ProjectRoleEditor ProjectRole = "editor"
	ProjectRoleAdmin  ProjectRole = "admin"
	ProjectRoleOwner  ProjectRole = "owner"
)

type AccessPolicy string

const (
	AccessNone            AccessPolicy = "none"
	AccessViewer          AccessPolicy = "viewer_plus"
	AccessEditor          AccessPolicy = "editor_plus"
	AccessAdmin           AccessPolicy = "admin_plus"
	AccessAdminOrOwner    AccessPolicy = "admin_or_owner"
	AccessBackendBinding  AccessPolicy = "backend_thread_turn_binding"
	AccessBackendWorker   AccessPolicy = "backend_worker"
	AccessNoPublicSurface AccessPolicy = "no_public_surface"
)

type AccessException string

const (
	ExceptionNone          AccessException = "none"
	ExceptionCreatorCancel AccessException = "creator_cancel_only"
)

type Action string

const (
	ActionRead   Action = "read"
	ActionMutate Action = "mutate"
)

type Surface string

const (
	SurfaceProjectSupportSnapshot   Surface = "project_support_snapshot"
	SurfaceWorkflowRunStatus        Surface = "workflow_run_status"
	SurfaceWorkflowControl          Surface = "workflow_control"
	SurfaceWorkflowAudit            Surface = "workflow_audit"
	SurfaceSourceChunkUI            Surface = "source_chunk_ui"
	SurfaceSourceChunkSDK           Surface = "source_chunk_sdk"
	SurfaceSDKDynamicTools          Surface = "sdk_dynamic_tools"
	SurfaceRuntimeWorkflowStart     Surface = "runtime_workflow_start"
	SurfaceRetrievalQuery           Surface = "retrieval_query"
	SurfaceChatRead                 Surface = "chat_read"
	SurfaceChatMutation             Surface = "chat_mutation"
	SurfaceCancelOwnRunningChatTurn Surface = "cancel_own_running_chat_turn"
	SurfaceProjectDelete            Surface = "project_delete"
	SurfaceBookChapterDelete        Surface = "book_chapter_delete"
	SurfaceDeletedProjectList       Surface = "deleted_project_list"
	SurfaceDeletedBookChapterList   Surface = "deleted_book_chapter_list"
	SurfaceProjectRestore           Surface = "project_restore"
	SurfaceBookChapterRestore       Surface = "book_chapter_restore"
	SurfaceRetrievedContextSnapshot Surface = "retrieved_context_snapshot"
)

type SurfacePolicy struct {
	Read      AccessPolicy
	Mutate    AccessPolicy
	Exception AccessException
}

type SurfaceRegistration struct {
	Surface Surface
	Policy  SurfacePolicy
}

var authorizationSurfacePolicy = []SurfaceRegistration{
	{Surface: SurfaceProjectSupportSnapshot, Policy: SurfacePolicy{Read: AccessAdmin, Mutate: AccessNone, Exception: ExceptionNone}},
	{Surface: SurfaceWorkflowRunStatus, Policy: SurfacePolicy{Read: AccessViewer, Mutate: AccessNone, Exception: ExceptionNone}},
	{Surface: SurfaceWorkflowControl, Policy: SurfacePolicy{Read: AccessNone, Mutate: AccessEditor, Exception: ExceptionNone}},
	{Surface: SurfaceWorkflowAudit, Policy: SurfacePolicy{Read: AccessAdmin, Mutate: AccessNone, Exception: ExceptionNone}},
	{Surface: SurfaceSourceChunkUI, Policy: SurfacePolicy{Read: AccessViewer, Mutate: AccessNone, Exception: ExceptionNone}},
	{Surface: SurfaceSourceChunkSDK, Policy: SurfacePolicy{Read: AccessBackendBinding, Mutate: AccessNone, Exception: ExceptionNone}},
	{Surface: SurfaceSDKDynamicTools, Policy: SurfacePolicy{Read: AccessBackendBinding, Mutate: AccessBackendBinding, Exception: ExceptionNone}},
	{Surface: SurfaceRuntimeWorkflowStart, Policy: SurfacePolicy{Read: AccessNone, Mutate: AccessBackendWorker, Exception: ExceptionNone}},
	{Surface: SurfaceRetrievalQuery, Policy: SurfacePolicy{Read: AccessViewer, Mutate: AccessNone, Exception: ExceptionNone}},
	{Surface: SurfaceChatRead, Policy: SurfacePolicy{Read: AccessViewer, Mutate: AccessNone, Exception: ExceptionNone}},
	{Surface: SurfaceChatMutation, Policy: SurfacePolicy{Read: AccessViewer, Mutate: AccessEditor, Exception: ExceptionNone}},
	{Surface: SurfaceCancelOwnRunningChatTurn, Policy: SurfacePolicy{Read: AccessNone, Mutate: AccessViewer, Exception: ExceptionCreatorCancel}},
	{Surface: SurfaceProjectDelete, Policy: SurfacePolicy{Read: AccessNone, Mutate: AccessAdminOrOwner, Exception: ExceptionNone}},
	{Surface: SurfaceBookChapterDelete, Policy: SurfacePolicy{Read: AccessNone, Mutate: AccessEditor, Exception: ExceptionNone}},
	{Surface: SurfaceDeletedProjectList, Policy: SurfacePolicy{Read: AccessAdminOrOwner, Mutate: AccessNone, Exception: ExceptionNone}},
	{Surface: SurfaceDeletedBookChapterList, Policy: SurfacePolicy{Read: AccessEditor, Mutate: AccessNone, Exception: ExceptionNone}},
	{Surface: SurfaceProjectRestore, Policy: SurfacePolicy{Read: AccessNone, Mutate: AccessAdminOrOwner, Exception: ExceptionNone}},
	{Surface: SurfaceBookChapterRestore, Policy: SurfacePolicy{Read: AccessNone, Mutate: AccessEditor, Exception: ExceptionNone}},
	{Surface: SurfaceRetrievedContextSnapshot, Policy: SurfacePolicy{Read: AccessNoPublicSurface, Mutate: AccessNoPublicSurface, Exception: ExceptionNone}},
}

func (p AccessPolicy) IsUserSessionPolicy() bool {
	switch p {
	case AccessViewer, AccessEditor, AccessAdmin, AccessAdminOrOwner:
		return true
	default:
		return false
	}
}

func AuthorizationSurfacePolicy() []SurfaceRegistration {
	return append([]SurfaceRegistration(nil), authorizationSurfacePolicy...)
}
