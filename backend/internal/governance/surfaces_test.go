package governance

import (
	"reflect"
	"testing"
)

func TestAuthorizationSurfaceRegistryIsClosedAndComplete(t *testing.T) {
	want := []SurfaceRegistration{
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

	got := AuthorizationSurfacePolicy()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("AuthorizationSurfacePolicy() mismatch\n got: %#v\nwant: %#v", got, want)
	}

	got[0].Policy.Read = AccessNone
	if fresh := AuthorizationSurfacePolicy(); !reflect.DeepEqual(fresh, want) {
		t.Fatal("AuthorizationSurfacePolicy returned mutable shared state")
	}
}

func TestAuthorizationSurfaceUserSessionPoliciesAreClosed(t *testing.T) {
	allowed := map[AccessPolicy]bool{
		AccessViewer:       true,
		AccessEditor:       true,
		AccessAdmin:        true,
		AccessAdminOrOwner: true,
	}
	for _, policy := range []AccessPolicy{
		AccessNone, AccessViewer, AccessEditor, AccessAdmin, AccessAdminOrOwner,
		AccessBackendBinding, AccessBackendWorker, AccessNoPublicSurface,
	} {
		if got := policy.IsUserSessionPolicy(); got != allowed[policy] {
			t.Fatalf("AccessPolicy(%q).IsUserSessionPolicy() = %v, want %v", policy, got, allowed[policy])
		}
	}
	if AccessPolicy("future_policy").IsUserSessionPolicy() {
		t.Fatal("unknown policy entered the user-session policy set")
	}
}

func TestSupportAndLifecycleAuthorizationPolicies(t *testing.T) {
	got := map[Surface]SurfacePolicy{}
	for _, registration := range AuthorizationSurfacePolicy() {
		got[registration.Surface] = registration.Policy
	}
	want := map[Surface]SurfacePolicy{
		SurfaceProjectSupportSnapshot: {Read: AccessAdmin, Mutate: AccessNone, Exception: ExceptionNone},
		SurfaceProjectDelete:          {Read: AccessNone, Mutate: AccessAdminOrOwner, Exception: ExceptionNone},
		SurfaceBookChapterDelete:      {Read: AccessNone, Mutate: AccessEditor, Exception: ExceptionNone},
		SurfaceDeletedProjectList:     {Read: AccessAdminOrOwner, Mutate: AccessNone, Exception: ExceptionNone},
		SurfaceDeletedBookChapterList: {Read: AccessEditor, Mutate: AccessNone, Exception: ExceptionNone},
		SurfaceProjectRestore:         {Read: AccessNone, Mutate: AccessAdminOrOwner, Exception: ExceptionNone},
		SurfaceBookChapterRestore:     {Read: AccessNone, Mutate: AccessEditor, Exception: ExceptionNone},
	}
	for surface, policy := range want {
		if got[surface] != policy {
			t.Fatalf("%s policy = %#v, want %#v", surface, got[surface], policy)
		}
	}
}
