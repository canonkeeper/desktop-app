package jobs

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"testing"
	"time"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/httpapi"
	"canonkeeper/backend/internal/projects"
)

func TestServiceCancelQueuedJobMarksCanceled(t *testing.T) {
	store := newJobStoreForTest()
	store.roles[projectActorKey("user-1", "project-1")] = projects.RoleViewer
	service := NewService(store, fixedJobClock{now: jobTestNow()})
	store.jobs["job-1"] = Job{
		ID:            "job-1",
		ProjectID:     "project-1",
		Kind:          KindImport,
		Status:        StatusQueued,
		Subject:       ImportSubject{Type: KindImport, ID: "project-1", ProjectID: "project-1"},
		CanCancel:     true,
		CreatorUserID: "user-1",
		CreatedAt:     jobTestNow(),
		UpdatedAt:     jobTestNow(),
	}

	job, err := service.CancelJob(context.Background(), Actor{UserID: "user-1"}, "job-1")
	if err != nil {
		t.Fatalf("CancelJob() error = %v", err)
	}
	if job.Status != StatusCanceled {
		t.Fatalf("CancelJob() status = %q, want %q", job.Status, StatusCanceled)
	}
	if job.CanCancel {
		t.Fatal("CancelJob() CanCancel = true, want false for canceled job")
	}
}

// Лента агента авторизуется через GetJob: тот же резолвер цели, что и у задания.
func TestTimelineAccessReusesJobsTargetResolver(t *testing.T) {
	store := newJobStoreForTest()
	store.roles[projectActorKey("user-1", "project-1")] = projects.RoleViewer
	store.jobs["job-1"] = researchChatJobForCancel(StatusRunning, "")
	resolver := &targetCancelResolverForTest{readable: true}
	service := NewService(store, fixedJobClock{now: jobTestNow()}, resolver)

	job, err := service.GetJob(context.Background(), Actor{UserID: "user-1"}, "job-1")
	if err != nil || job.ID != "job-1" {
		t.Fatalf("GetJob() = %#v, %v", job, err)
	}
	if resolver.readCalls != 1 || resolver.calls != 0 {
		t.Fatalf("resolver use read/cancel = %d/%d", resolver.readCalls, resolver.calls)
	}

	resolver.readable = false
	if _, err := service.GetJob(context.Background(), Actor{UserID: "user-1"}, "job-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unauthorized GetJob() error = %v, want ErrNotFound", err)
	}
}

func TestServiceCancelQueuedJobAllowsProjectAdmin(t *testing.T) {
	store := newJobStoreForTest()
	store.roles[projectActorKey("admin-1", "project-1")] = projects.RoleAdmin
	service := NewService(store, fixedJobClock{now: jobTestNow()})
	store.jobs["job-1"] = Job{
		ID:            "job-1",
		ProjectID:     "project-1",
		Kind:          KindImport,
		Status:        StatusQueued,
		Subject:       ImportSubject{Type: KindImport, ID: "project-1", ProjectID: "project-1"},
		CanCancel:     true,
		CreatorUserID: "user-1",
		CreatedAt:     jobTestNow(),
		UpdatedAt:     jobTestNow(),
	}

	job, err := service.CancelJob(context.Background(), Actor{UserID: "admin-1"}, "job-1")
	if err != nil {
		t.Fatalf("CancelJob(admin) error = %v", err)
	}
	if job.Status != StatusCanceled {
		t.Fatalf("CancelJob(admin) status = %q, want %q", job.Status, StatusCanceled)
	}
}

func TestServiceCancelQueuedJobRejectsNonCreatorProjectMember(t *testing.T) {
	store := newJobStoreForTest()
	store.roles[projectActorKey("user-2", "project-1")] = projects.RoleViewer
	service := NewService(store, fixedJobClock{now: jobTestNow()})
	store.jobs["job-1"] = Job{
		ID:            "job-1",
		ProjectID:     "project-1",
		Kind:          KindImport,
		Status:        StatusQueued,
		Subject:       ImportSubject{Type: KindImport, ID: "project-1", ProjectID: "project-1"},
		CanCancel:     true,
		CreatorUserID: "user-1",
		CreatedAt:     jobTestNow(),
		UpdatedAt:     jobTestNow(),
	}

	_, err := service.CancelJob(context.Background(), Actor{UserID: "user-2"}, "job-1")
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("CancelJob(non-creator viewer) error = %v, want ErrForbidden", err)
	}
	if store.jobs["job-1"].Status != StatusQueued {
		t.Fatalf("CancelJob(non-creator viewer) mutated status = %q, want queued", store.jobs["job-1"].Status)
	}
}

func TestServiceCancelTerminalJobRejectsNonCreatorProjectMember(t *testing.T) {
	store := newJobStoreForTest()
	store.roles[projectActorKey("user-2", "project-1")] = projects.RoleViewer
	service := NewService(store, fixedJobClock{now: jobTestNow()})
	store.jobs["job-1"] = Job{
		ID:            "job-1",
		ProjectID:     "project-1",
		Kind:          KindImport,
		Status:        StatusSucceeded,
		Subject:       ImportSubject{Type: KindImport, ID: "project-1", ProjectID: "project-1"},
		Result:        importResultForTest(),
		CanCancel:     true,
		CreatorUserID: "user-1",
		CreatedAt:     jobTestNow(),
		UpdatedAt:     jobTestNow(),
	}

	_, err := service.CancelJob(context.Background(), Actor{UserID: "user-2"}, "job-1")
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("CancelJob(non-creator viewer terminal) error = %v, want ErrForbidden", err)
	}
	if store.jobs["job-1"].Status != StatusSucceeded {
		t.Fatalf("CancelJob(non-creator viewer terminal) mutated status = %q, want succeeded", store.jobs["job-1"].Status)
	}
}

func TestServiceCancelTerminalJobReturnsCurrentState(t *testing.T) {
	store := newJobStoreForTest()
	store.roles[projectActorKey("user-1", "project-1")] = projects.RoleAdmin
	service := NewService(store, fixedJobClock{now: jobTestNow()})
	store.jobs["job-1"] = Job{
		ID:        "job-1",
		ProjectID: "project-1",
		Kind:      KindImport,
		Status:    StatusSucceeded,
		Subject:   ImportSubject{Type: KindImport, ID: "project-1", ProjectID: "project-1"},
		CreatedAt: jobTestNow(),
		UpdatedAt: jobTestNow(),
	}

	job, err := service.CancelJob(context.Background(), Actor{UserID: "user-1"}, "job-1")
	if err != nil {
		t.Fatalf("CancelJob() error = %v", err)
	}
	if job.Status != StatusSucceeded {
		t.Fatalf("CancelJob() terminal status = %q, want %q", job.Status, StatusSucceeded)
	}
}

func TestServiceCancelReturnsTerminalStateWhenTransitionRaces(t *testing.T) {
	store := newJobStoreForTest()
	store.roles[projectActorKey("user-1", "project-1")] = projects.RoleViewer
	service := NewService(store, fixedJobClock{now: jobTestNow()})
	store.jobs["job-1"] = Job{
		ID:            "job-1",
		ProjectID:     "project-1",
		Kind:          KindImport,
		Status:        StatusRunning,
		Progress:      jobProgressForTest(0.75),
		Subject:       ImportSubject{Type: KindImport, ID: "project-1", ProjectID: "project-1"},
		CanCancel:     true,
		CreatorUserID: "user-1",
		CreatedAt:     jobTestNow(),
		UpdatedAt:     jobTestNow(),
	}
	store.beforeCancelJobStatus = func(input CancelJobStatusStoreInput) {
		job := store.jobs[input.JobID]
		job.Status = StatusSucceeded
		job.Progress = jobProgressForTest(1)
		job.Result = importResultForTest()
		job.CanCancel = false
		store.jobs[job.ID] = job
	}

	job, err := service.CancelJob(context.Background(), Actor{UserID: "user-1"}, "job-1")
	if err != nil {
		t.Fatalf("CancelJob(racing terminal transition) error = %v, want nil", err)
	}
	if job.Status != StatusSucceeded {
		t.Fatalf("CancelJob(racing terminal transition) status = %q, want %q", job.Status, StatusSucceeded)
	}
	assertJobImportResultVisibility(t, "CancelJob(racing terminal transition)", job.Job, true)
}

func TestServiceCancelRunningJobReturnsCancelingWithoutFurtherCancel(t *testing.T) {
	store := newJobStoreForTest()
	store.roles[projectActorKey("user-1", "project-1")] = projects.RoleViewer
	service := NewService(store, fixedJobClock{now: jobTestNow()})
	store.jobs["job-1"] = Job{
		ID:            "job-1",
		ProjectID:     "project-1",
		Kind:          KindImport,
		Status:        StatusRunning,
		Progress:      jobProgressForTest(0.5),
		Subject:       ImportSubject{Type: KindImport, ID: "project-1", ProjectID: "project-1"},
		CanCancel:     true,
		CreatorUserID: "user-1",
		CreatedAt:     jobTestNow(),
		UpdatedAt:     jobTestNow(),
	}

	job, err := service.CancelJob(context.Background(), Actor{UserID: "user-1"}, "job-1")
	if err != nil {
		t.Fatalf("CancelJob(running creator) error = %v, want nil", err)
	}
	if job.Status != StatusCanceling {
		t.Fatalf("CancelJob(running creator) status = %q, want %q", job.Status, StatusCanceling)
	}
	if job.CanCancel {
		t.Fatal("CancelJob(running creator) CanCancel = true, want false for canceling job")
	}
}

func TestServiceCancelAlreadyCancelingJobReturnsCurrentState(t *testing.T) {
	for _, tc := range []struct {
		name    string
		actorID string
		role    projects.Role
	}{
		{name: "creator_viewer", actorID: "creator-1", role: projects.RoleViewer},
		{name: "admin_non_creator", actorID: "admin-1", role: projects.RoleAdmin},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newJobStoreForTest()
			store.roles[projectActorKey(tc.actorID, "project-1")] = tc.role
			service := NewService(store, fixedJobClock{now: jobTestNow()})
			store.jobs["job-1"] = Job{
				ID:            "job-1",
				ProjectID:     "project-1",
				Kind:          KindImport,
				Status:        StatusCanceling,
				Progress:      jobProgressForTest(0.5),
				Subject:       ImportSubject{Type: KindImport, ID: "project-1", ProjectID: "project-1"},
				CanCancel:     false,
				CreatorUserID: "creator-1",
				CreatedAt:     jobTestNow(),
				UpdatedAt:     jobTestNow(),
			}

			job, err := service.CancelJob(context.Background(), Actor{UserID: tc.actorID}, "job-1")
			if err != nil {
				t.Fatalf("CancelJob(canceling %s) error = %v, want nil", tc.name, err)
			}
			if job.Status != StatusCanceling {
				t.Fatalf("CancelJob(canceling %s) status = %q, want %q", tc.name, job.Status, StatusCanceling)
			}
			if job.CanCancel {
				t.Fatalf("CancelJob(canceling %s) CanCancel = true, want false", tc.name)
			}
		})
	}
}

func TestServiceDirectLookupUsesLeakSafeNotFoundForNonMembers(t *testing.T) {
	store := newJobStoreForTest()
	service := NewService(store, fixedJobClock{now: jobTestNow()})
	store.jobs["job-1"] = Job{
		ID:        "job-1",
		ProjectID: "project-1",
		Kind:      KindImport,
		Status:    StatusQueued,
		Subject:   ImportSubject{Type: KindImport, ID: "project-1", ProjectID: "project-1"},
		CreatedAt: jobTestNow(),
		UpdatedAt: jobTestNow(),
	}

	_, err := service.GetJob(context.Background(), Actor{UserID: "outsider"}, "job-1")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetJob() error = %v, want ErrNotFound", err)
	}
}

func TestServiceDirectLookupResolvesThenLoadsExactOwningProject(t *testing.T) {
	store := newJobStoreForTest()
	store.roles[projectActorKey("reader-1", "project-a")] = projects.RoleViewer
	store.jobs["job-1"] = Job{
		ID: "job-1", ProjectID: "project-a", Kind: KindImport, Status: StatusQueued,
		Subject:   ImportSubject{Type: KindImport, ID: "project-a", ProjectID: "project-a"},
		CreatedAt: jobTestNow(), UpdatedAt: jobTestNow(),
	}
	service := NewService(store, fixedJobClock{now: jobTestNow()})

	job, err := service.GetJob(context.Background(), Actor{UserID: "reader-1"}, "job-1")
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	if job.ProjectID != "project-a" {
		t.Fatalf("GetJob() project = %q, want resolver-owned project-a", job.ProjectID)
	}
	if len(store.resolveJobAccessInputs) != 1 || store.resolveJobAccessInputs[0] != (ResolveJobAccessForActorStoreInput{
		ActorUserID: "reader-1", JobID: "job-1",
	}) {
		t.Fatalf("resolver inputs = %#v, want authenticated actor plus resource ID", store.resolveJobAccessInputs)
	}
	if len(store.getJobInputs) != 1 || store.getJobInputs[0] != (GetJobStoreInput{
		ActorUserID: "reader-1", ProjectID: "project-a", JobID: "job-1",
	}) {
		t.Fatalf("downstream inputs = %#v, want exact resolved project plus job", store.getJobInputs)
	}
}

func TestServiceDirectLookupConcealsJobWhenRoleIsRemovedAfterResolution(t *testing.T) {
	store := newJobStoreForTest()
	store.roles[projectActorKey("reader-1", "project-a")] = projects.RoleViewer
	store.jobs["job-1"] = Job{
		ID: "job-1", ProjectID: "project-a", Kind: KindImport, Status: StatusQueued,
		Subject: ImportSubject{Type: KindImport, ID: "project-a", ProjectID: "project-a"},
	}
	store.afterResolveJobAccess = func() {
		delete(store.roles, projectActorKey("reader-1", "project-a"))
	}
	service := NewService(store, fixedJobClock{now: jobTestNow()})

	_, err := service.GetJob(context.Background(), Actor{UserID: "reader-1"}, "job-1")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetJob() error = %v, want ErrNotFound after role removal race", err)
	}
}

func TestServiceReadImportResultVisibilityByActor(t *testing.T) {
	for _, tc := range []struct {
		name       string
		actorID    string
		role       projects.Role
		result     Result
		wantResult bool
	}{
		{
			name:       "viewer_non_creator",
			actorID:    "viewer-1",
			role:       projects.RoleViewer,
			result:     importResultForTest(),
			wantResult: false,
		},
		{
			name:       "creator_viewer",
			actorID:    "creator-1",
			role:       projects.RoleViewer,
			result:     importResultForTest(),
			wantResult: true,
		},
		{
			name:       "editor_non_creator",
			actorID:    "editor-1",
			role:       projects.RoleEditor,
			result:     importResultForTest(),
			wantResult: true,
		},
		{
			name:       "creator_editor",
			actorID:    "creator-1",
			role:       projects.RoleEditor,
			result:     importResultForTest(),
			wantResult: true,
		},
		{
			name:       "admin_non_creator",
			actorID:    "admin-1",
			role:       projects.RoleAdmin,
			result:     importResultForTest(),
			wantResult: true,
		},
		{
			name:       "owner_non_creator",
			actorID:    "owner-1",
			role:       projects.RoleOwner,
			result:     importResultForTest(),
			wantResult: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newJobStoreForTest()
			store.roles[projectActorKey(tc.actorID, "project-1")] = tc.role
			service := NewService(store, fixedJobClock{now: jobTestNow()})
			store.jobs["job-1"] = Job{
				ID:            "job-1",
				ProjectID:     "project-1",
				Kind:          KindImport,
				Status:        StatusSucceeded,
				Progress:      jobProgressForTest(1),
				Subject:       ImportSubject{Type: KindImport, ID: "project-1", ProjectID: "project-1"},
				Result:        tc.result,
				CreatorUserID: "creator-1",
				CreatedAt:     jobTestNow(),
				UpdatedAt:     jobTestNow(),
			}

			page, err := service.ListProjectJobs(context.Background(), Actor{UserID: tc.actorID}, "project-1", ListProjectJobsInput{Kind: KindImport, Limit: 10})
			if err != nil {
				t.Fatalf("ListProjectJobs() error = %v", err)
			}
			if len(page.Jobs) != 1 {
				t.Fatalf("ListProjectJobs() returned %d jobs, want 1", len(page.Jobs))
			}
			assertJobImportResultVisibility(t, "ListProjectJobs()", page.Jobs[0], tc.wantResult)

			got, err := service.GetJob(context.Background(), Actor{UserID: tc.actorID}, "job-1")
			if err != nil {
				t.Fatalf("GetJob() error = %v", err)
			}
			assertJobImportResultVisibility(t, "GetJob()", got, tc.wantResult)
		})
	}
}

func TestServiceReadMakesCanCancelActorSpecific(t *testing.T) {
	for _, tc := range []struct {
		name          string
		actorID       string
		role          projects.Role
		wantCanCancel bool
	}{
		{name: "viewer_non_creator", actorID: "viewer-1", role: projects.RoleViewer, wantCanCancel: false},
		{name: "editor_non_creator", actorID: "editor-1", role: projects.RoleEditor, wantCanCancel: false},
		{name: "creator_viewer", actorID: "creator-1", role: projects.RoleViewer, wantCanCancel: true},
		{name: "creator_editor", actorID: "creator-1", role: projects.RoleEditor, wantCanCancel: true},
		{name: "admin_non_creator", actorID: "admin-1", role: projects.RoleAdmin, wantCanCancel: true},
		{name: "owner_non_creator", actorID: "owner-1", role: projects.RoleOwner, wantCanCancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newJobStoreForTest()
			store.roles[projectActorKey(tc.actorID, "project-1")] = tc.role
			service := NewService(store, fixedJobClock{now: jobTestNow()})
			store.jobs["job-1"] = Job{
				ID:            "job-1",
				ProjectID:     "project-1",
				Kind:          KindImport,
				Status:        StatusQueued,
				Progress:      jobProgressForTest(0.25),
				Subject:       ImportSubject{Type: KindImport, ID: "project-1", ProjectID: "project-1"},
				CanCancel:     true,
				CreatorUserID: "creator-1",
				CreatedAt:     jobTestNow(),
				UpdatedAt:     jobTestNow(),
			}

			page, err := service.ListProjectJobs(context.Background(), Actor{UserID: tc.actorID}, "project-1", ListProjectJobsInput{Kind: KindImport, Limit: 10})
			if err != nil {
				t.Fatalf("ListProjectJobs() error = %v", err)
			}
			if len(page.Jobs) != 1 {
				t.Fatalf("ListProjectJobs() returned %d jobs, want 1", len(page.Jobs))
			}
			assertJobCanCancelForActor(t, "ListProjectJobs()", page.Jobs[0], tc.wantCanCancel)

			got, err := service.GetJob(context.Background(), Actor{UserID: tc.actorID}, "job-1")
			if err != nil {
				t.Fatalf("GetJob() error = %v", err)
			}
			assertJobCanCancelForActor(t, "GetJob()", got, tc.wantCanCancel)
		})
	}
}

func TestServiceListProjectJobsReturnsCursorPage(t *testing.T) {
	store := newJobStoreForTest()
	store.roles[projectActorKey("user-1", "project-1")] = projects.RoleAdmin
	service := NewService(store, fixedJobClock{now: jobTestNow()})
	for _, job := range []Job{
		{ID: "job-4", ProjectID: "project-1", Kind: KindImport, Status: StatusQueued, CreatedAt: jobTestNow().Add(4 * time.Minute), UpdatedAt: jobTestNow()},
		{ID: "job-3", ProjectID: "project-1", Kind: KindImport, Status: StatusQueued, CreatedAt: jobTestNow().Add(3 * time.Minute), UpdatedAt: jobTestNow()},
		{ID: "job-2", ProjectID: "project-1", Kind: KindImport, Status: StatusQueued, CreatedAt: jobTestNow().Add(2 * time.Minute), UpdatedAt: jobTestNow()},
		{ID: "job-1", ProjectID: "project-1", Kind: KindImport, Status: StatusQueued, CreatedAt: jobTestNow().Add(time.Minute), UpdatedAt: jobTestNow()},
	} {
		store.jobs[job.ID] = job
	}

	firstPage, firstErr := service.ListProjectJobs(context.Background(), Actor{UserID: "user-1"}, "project-1", ListProjectJobsInput{Kind: KindImport, Limit: 2})
	if firstErr != nil {
		t.Fatalf("ListProjectJobs(first page) error = %v", firstErr)
	}
	if gotIDs := jobIDsForTest(firstPage.Jobs); !sameJobIDs(gotIDs, []string{"job-4", "job-3"}) {
		t.Fatalf("ListProjectJobs(first page) IDs = %v, want [job-4 job-3]", gotIDs)
	}
	if !firstPage.Meta.HasMore {
		t.Fatal("ListProjectJobs(first page) hasMore = false, want true")
	}
	cursor := firstPage.Meta.NextCursor
	if cursor == "" {
		t.Fatal("ListProjectJobs(first page) nextCursor is empty, want opaque cursor")
	}

	secondPage, secondErr := service.ListProjectJobs(context.Background(), Actor{UserID: "user-1"}, "project-1", ListProjectJobsInput{Kind: KindImport, Limit: 2, Cursor: cursor})
	if secondErr != nil {
		t.Fatalf("ListProjectJobs(second page) error = %v", secondErr)
	}
	if gotIDs := jobIDsForTest(secondPage.Jobs); !sameJobIDs(gotIDs, []string{"job-2", "job-1"}) {
		t.Fatalf("ListProjectJobs(second page) IDs = %v, want [job-2 job-1]", gotIDs)
	}
	if secondPage.Meta.HasMore {
		t.Fatal("ListProjectJobs(second page) hasMore = true, want false")
	}
	if next := secondPage.Meta.NextCursor; next != "" {
		t.Fatalf("ListProjectJobs(second page) nextCursor = %q, want empty", next)
	}
}

func TestServiceListProjectJobsRejectsInvalidOrMismatchedCursor(t *testing.T) {
	store := newJobStoreForTest()
	store.roles[projectActorKey("user-1", "project-1")] = projects.RoleAdmin
	store.roles[projectActorKey("user-1", "project-2")] = projects.RoleAdmin
	service := NewService(store, fixedJobClock{now: jobTestNow()})
	for _, job := range []Job{
		{ID: "job-3", ProjectID: "project-1", Kind: KindImport, Status: StatusQueued, CreatedAt: jobTestNow().Add(3 * time.Minute), UpdatedAt: jobTestNow()},
		{ID: "job-2", ProjectID: "project-1", Kind: KindImport, Status: StatusQueued, CreatedAt: jobTestNow().Add(2 * time.Minute), UpdatedAt: jobTestNow()},
		{ID: "job-1", ProjectID: "project-1", Kind: KindImport, Status: StatusQueued, CreatedAt: jobTestNow().Add(time.Minute), UpdatedAt: jobTestNow()},
	} {
		store.jobs[job.ID] = job
	}

	firstPage, err := service.ListProjectJobs(context.Background(), Actor{UserID: "user-1"}, "project-1", ListProjectJobsInput{Kind: KindImport, Limit: 1})
	if err != nil {
		t.Fatalf("ListProjectJobs(first page) error = %v", err)
	}
	cursor := firstPage.Meta.NextCursor
	if cursor == "" {
		t.Fatal("ListProjectJobs(first page) nextCursor is empty, want cursor for mismatch checks")
	}

	_, err = service.ListProjectJobs(context.Background(), Actor{UserID: "user-1"}, "project-1", ListProjectJobsInput{Kind: KindImport, Limit: 1, Cursor: "not-a-valid-cursor"})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("ListProjectJobs(invalid cursor) error = %v, want ErrValidation", err)
	}
	_, err = service.ListProjectJobs(context.Background(), Actor{UserID: "user-1"}, "project-2", ListProjectJobsInput{Kind: KindImport, Limit: 1, Cursor: cursor})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("ListProjectJobs(project-mismatched cursor) error = %v, want ErrValidation", err)
	}
	_, err = service.ListProjectJobs(context.Background(), Actor{UserID: "user-1"}, "project-1", ListProjectJobsInput{Kind: KindCanonTextIndex, Limit: 1, Cursor: cursor})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("ListProjectJobs(kind-mismatched cursor) error = %v, want ErrValidation", err)
	}
}

// UC2: SQL checks the read right only for the candidates of one page; the service skips the
// unreadable ones, reads on after a full page and still returns the first readable Jobs.
func TestServiceListProjectJobsSkipsUnreadableCandidatesAndReadsOn(t *testing.T) {
	store := newJobStoreForTest()
	store.roles[projectActorKey("user-1", "project-1")] = projects.RoleAdmin
	service := NewService(store, fixedJobClock{now: jobTestNow()})
	for minute := 1; minute <= 6; minute++ {
		job := Job{
			ID: fmt.Sprintf("job-%d", minute), ProjectID: "project-1", Kind: KindImport, Status: StatusSucceeded,
			CreatedAt: jobTestNow().Add(time.Duration(minute) * time.Minute), UpdatedAt: jobTestNow(),
		}
		store.jobs[job.ID] = job
	}
	store.unreadable["job-6"], store.unreadable["job-5"], store.unreadable["job-3"] = true, true, true

	first, err := service.ListProjectJobs(context.Background(), Actor{UserID: "user-1"}, "project-1", ListProjectJobsInput{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if got := jobIDsForTest(first.Jobs); !sameJobIDs(got, []string{"job-4", "job-2"}) || !first.Meta.HasMore {
		t.Fatalf("first page = %v hasMore=%v, want [job-4 job-2] and more", got, first.Meta.HasMore)
	}
	if len(store.listInputs) != 2 || store.listInputs[1].CursorID != "job-4" {
		t.Fatalf("store pages = %+v, want a second page after the last candidate job-4", store.listInputs)
	}
	second, err := service.ListProjectJobs(context.Background(), Actor{UserID: "user-1"}, "project-1", ListProjectJobsInput{Limit: 2, Cursor: first.Meta.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if got := jobIDsForTest(second.Jobs); !sameJobIDs(got, []string{"job-1"}) || second.Meta.HasMore {
		t.Fatalf("second page = %v hasMore=%v, want [job-1] and the end", got, second.Meta.HasMore)
	}
}

// UC1: the active filter reaches the store, and its cursor serves only the active list.
func TestServiceListProjectJobsActiveOnly(t *testing.T) {
	store := newJobStoreForTest()
	store.roles[projectActorKey("user-1", "project-1")] = projects.RoleAdmin
	service := NewService(store, fixedJobClock{now: jobTestNow()})
	for minute, status := range []Status{StatusSucceeded, StatusRunning, StatusFailed, StatusQueued, StatusCanceling} {
		job := Job{
			ID: fmt.Sprintf("job-%d", minute+1), ProjectID: "project-1", Kind: KindImport, Status: status,
			CreatedAt: jobTestNow().Add(time.Duration(minute+1) * time.Minute), UpdatedAt: jobTestNow(),
		}
		store.jobs[job.ID] = job
	}
	page, err := service.ListProjectJobs(context.Background(), Actor{UserID: "user-1"}, "project-1", ListProjectJobsInput{ActiveOnly: true, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if got := jobIDsForTest(page.Jobs); !sameJobIDs(got, []string{"job-5", "job-4"}) || !page.Meta.HasMore || !store.listInputs[0].ActiveOnly {
		t.Fatalf("active page = %v hasMore=%v inputs=%+v", got, page.Meta.HasMore, store.listInputs)
	}
	if _, err := service.ListProjectJobs(context.Background(), Actor{UserID: "user-1"}, "project-1", ListProjectJobsInput{Limit: 2, Cursor: page.Meta.NextCursor}); !errors.Is(err, ErrValidation) {
		t.Fatalf("active cursor on the full list error = %v, want ErrValidation", err)
	}
	rest, err := service.ListProjectJobs(context.Background(), Actor{UserID: "user-1"}, "project-1", ListProjectJobsInput{ActiveOnly: true, Limit: 2, Cursor: page.Meta.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if got := jobIDsForTest(rest.Jobs); !sameJobIDs(got, []string{"job-2"}) || rest.Meta.HasMore {
		t.Fatalf("active rest = %v hasMore=%v, want [job-2]", got, rest.Meta.HasMore)
	}
}

func TestServiceCancelQueuedJobContinuesWhenWorkerRacesToRunning(t *testing.T) {
	store := newJobStoreForTest()
	store.roles[projectActorKey("creator-1", "project-1")] = projects.RoleViewer
	service := NewService(store, fixedJobClock{now: jobTestNow()})
	store.jobs["job-1"] = Job{
		ID:            "job-1",
		ProjectID:     "project-1",
		Kind:          KindImport,
		Status:        StatusQueued,
		Progress:      jobProgressForTest(0),
		Subject:       ImportSubject{Type: KindImport, ID: "project-1", ProjectID: "project-1"},
		CanCancel:     true,
		CreatorUserID: "creator-1",
		CreatedAt:     jobTestNow(),
		UpdatedAt:     jobTestNow(),
	}
	store.beforeCancelJobStatus = func(input CancelJobStatusStoreInput) {
		if input.JobID != "job-1" || input.From != StatusQueued || input.To != StatusCanceled {
			return
		}
		job := store.jobs[input.JobID]
		job.Status = StatusRunning
		job.Progress = jobProgressForTest(0.4)
		store.jobs[job.ID] = job
	}

	job, err := service.CancelJob(context.Background(), Actor{UserID: "creator-1"}, "job-1")
	if err != nil {
		t.Fatalf("CancelJob(queued racing to running) error = %v, want nil", err)
	}
	if job.Status != StatusCanceling {
		t.Fatalf("CancelJob(queued racing to running) status = %q, want %q", job.Status, StatusCanceling)
	}
	if progressValue(job.Progress) != 0.4 {
		t.Fatalf("CancelJob(queued racing to running) progress = %v, want 0.4", job.Progress)
	}
	if job.CanCancel {
		t.Fatal("CancelJob(queued racing to running) CanCancel = true, want false")
	}
}

func TestServiceCancelQueuedSystemIndexingContinuesWhenWorkerRacesToRunning(t *testing.T) {
	store := newJobStoreForTest()
	store.roles[projectActorKey("owner-1", "project-1")] = projects.RoleOwner
	service := NewService(store, fixedJobClock{now: jobTestNow()}, &targetCancelResolverForTest{readable: true})
	store.jobs["job-1"] = Job{
		ID: "job-1", ProjectID: "project-1", Kind: KindChapterCards,
		Status: StatusQueued, Progress: jobProgressForTest(0), CanCancel: false,
		Subject:   ChapterCardsSubject{Type: KindChapterCards, ProjectID: "project-1", SourceView: governance.SourceViewDraft},
		CreatedAt: jobTestNow(), UpdatedAt: jobTestNow(),
	}
	store.beforeCancelJobStatus = func(input CancelJobStatusStoreInput) {
		if input.JobID != "job-1" || input.From != StatusQueued || input.To != StatusCanceled {
			return
		}
		job := store.jobs[input.JobID]
		job.Status = StatusRunning
		job.Progress = jobProgressForTest(0.4)
		store.jobs[job.ID] = job
	}

	job, err := service.CancelJob(context.Background(), Actor{UserID: "owner-1"}, "job-1")
	if err != nil {
		t.Fatalf("CancelJob(system queued racing to running) error = %v", err)
	}
	if job.Status != StatusCanceling || progressValue(job.Progress) != 0.4 || job.CanCancel {
		t.Fatalf("CancelJob(system queued racing to running) = %+v", job)
	}
}

func TestJobProjectionRetainsNullableExpiryAndSafeProblem(t *testing.T) {
	expiresAt := jobTestNow().Add(time.Hour)
	response := mustJobResponseForTest(t, Job{
		ID: "job-1", ProjectID: "project-1", Kind: KindImport, Status: StatusFailed,
		Subject:   ImportSubject{Type: KindImport, ID: "project-1", ProjectID: "project-1"},
		Error:     &Error{Code: "worker.handler_failed", Message: "safe failure"},
		ExpiresAt: &expiresAt, CreatedAt: jobTestNow(), UpdatedAt: jobTestNow(),
	})
	if response.ExpiresAt == nil || response.Error == nil {
		t.Fatalf("response = %#v", response)
	}
	problem, ok := response.Error.(httpapi.Problem)
	if !ok || problem.RequestID != jobProjectionRequestIDForTest {
		t.Fatalf("error = %#v, want requestId %q", response.Error, jobProjectionRequestIDForTest)
	}
}

type fixedJobClock struct {
	now time.Time
}

func (c fixedJobClock) Now() time.Time {
	return c.now
}

func jobTestNow() time.Time {
	return time.Date(2026, 6, 24, 12, 30, 0, 0, time.UTC)
}

func jobIDsForTest(records []Job) []string {
	ids := make([]string, len(records))
	for i, job := range records {
		ids[i] = job.ID
	}
	return ids
}

func sameJobIDs(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

type jobStoreForTest struct {
	jobs                   map[string]Job
	unreadable             map[string]bool
	listInputs             []ListProjectJobsStoreInput
	roles                  map[string]projects.Role
	beforeCancelJobStatus  func(CancelJobStatusStoreInput)
	afterResolveJobAccess  func()
	resolveJobAccessInputs []ResolveJobAccessForActorStoreInput
	getJobInputs           []GetJobStoreInput
}

func newJobStoreForTest() *jobStoreForTest {
	return &jobStoreForTest{
		jobs:       make(map[string]Job),
		unreadable: make(map[string]bool),
		roles:      make(map[string]projects.Role),
	}
}

func (s *jobStoreForTest) ActiveProjectRole(_ context.Context, actorUserID, projectID string) (projects.Role, error) {
	role, ok := s.roles[projectActorKey(actorUserID, projectID)]
	if !ok {
		return "", ErrNotFound
	}
	return role, nil
}

func (s *jobStoreForTest) ListProjectJobs(_ context.Context, input ListProjectJobsStoreInput) (ProjectJobCandidates, error) {
	s.listInputs = append(s.listInputs, input)
	var result []Job
	for _, job := range s.jobs {
		if job.ProjectID == input.ProjectID && (input.Kind == "" || job.Kind == input.Kind) &&
			(!input.ActiveOnly || job.Status == StatusQueued || job.Status == StatusRunning || job.Status == StatusCanceling) {
			result = append(result, job)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].CreatedAt.Equal(result[j].CreatedAt) {
			return result[i].ID > result[j].ID
		}
		return result[i].CreatedAt.After(result[j].CreatedAt)
	})
	if input.HasCursor {
		filtered := result[:0]
		for _, job := range result {
			if job.CreatedAt.Before(input.CursorAt) || (job.CreatedAt.Equal(input.CursorAt) && job.ID < input.CursorID) {
				filtered = append(filtered, job)
			}
		}
		result = filtered
	}
	if input.Limit > 0 && len(result) > input.Limit {
		result = result[:input.Limit]
	}
	page := ProjectJobCandidates{Count: len(result)}
	for _, job := range result {
		if !s.unreadable[job.ID] {
			page.Readable = append(page.Readable, job)
		}
	}
	if len(result) > 0 {
		page.LastCreatedAt, page.LastID = result[len(result)-1].CreatedAt, result[len(result)-1].ID
	}
	return page, nil
}

func (s *jobStoreForTest) ResolveJobAccessForActor(_ context.Context, input ResolveJobAccessForActorStoreInput) (ResolvedJobAccess, error) {
	s.resolveJobAccessInputs = append(s.resolveJobAccessInputs, input)
	job, ok := s.jobs[input.JobID]
	if !ok {
		return ResolvedJobAccess{}, ErrNotFound
	}
	role, ok := s.roles[projectActorKey(input.ActorUserID, job.ProjectID)]
	if !ok {
		return ResolvedJobAccess{}, ErrNotFound
	}
	if s.afterResolveJobAccess != nil {
		s.afterResolveJobAccess()
	}
	return ResolvedJobAccess{
		ProjectID: job.ProjectID, JobID: job.ID, CurrentRole: role, Visibility: JobAccessVisibilityActive,
	}, nil
}

func (s *jobStoreForTest) GetJob(_ context.Context, input GetJobStoreInput) (Job, error) {
	s.getJobInputs = append(s.getJobInputs, input)
	job, ok := s.jobs[input.JobID]
	if !ok || job.ProjectID != input.ProjectID {
		return Job{}, ErrNotFound
	}
	if _, ok := s.roles[projectActorKey(input.ActorUserID, input.ProjectID)]; !ok {
		return Job{}, ErrNotFound
	}
	return job, nil
}

func (s *jobStoreForTest) CancelJobStatus(_ context.Context, input CancelJobStatusStoreInput) (Job, error) {
	job, ok := s.jobs[input.JobID]
	if !ok || job.ProjectID != input.ProjectID {
		return Job{}, ErrNotFound
	}
	if s.beforeCancelJobStatus != nil {
		s.beforeCancelJobStatus(input)
		job = s.jobs[input.JobID]
	}
	role, ok := s.roles[projectActorKey(input.ActorUserID, job.ProjectID)]
	if !ok {
		return Job{}, ErrNotFound
	}
	if input.ActorUserID != job.CreatorUserID && !projects.CanManageMembers(role) {
		return Job{}, ErrForbidden
	}
	if job.Status != input.From {
		return Job{}, ErrConflict
	}
	job.Status = input.To
	job.Progress = jobProgressForTest(input.Progress)
	job.Result = input.Result
	job.Error = input.Error
	job.UpdatedAt = input.UpdatedAt
	s.jobs[job.ID] = job
	return job, nil
}

func projectActorKey(actorUserID, projectID string) string {
	return actorUserID + "|" + projectID
}

func assertJobImportResultVisibility(t *testing.T, operation string, job Job, wantResult bool) {
	t.Helper()
	if wantResult {
		result, ok := job.Result.(ImportJobResult)
		if !ok || result.Type != KindImport || result.BookID != "book-1" {
			t.Fatalf("%s result = %#v, want import result", operation, job.Result)
		}
		return
	}
	if job.Result != nil {
		t.Fatalf("%s result = %#v, want nil", operation, job.Result)
	}
}

func assertJobCanCancelForActor(t *testing.T, operation string, job Job, wantCanCancel bool) {
	t.Helper()
	if job.CanCancel != wantCanCancel {
		t.Fatalf("%s CanCancel = %v, want %v", operation, job.CanCancel, wantCanCancel)
	}
	response := mustJobResponseForTest(t, job)
	if wantCanCancel {
		if response.Links.Cancel == nil || *response.Links.Cancel != "/api/v1/jobs/"+job.ID+"/cancel" {
			t.Fatalf("%s cancel link = %v, want API cancel link", operation, response.Links.Cancel)
		}
		return
	}
	if response.Links.Cancel != nil {
		t.Fatalf("%s cancel link = %q, want nil", operation, *response.Links.Cancel)
	}
}

func importResultForTest() ImportJobResult {
	return ImportJobResult{Type: KindImport, BookID: "book-1", ChapterCount: 2, ParagraphCount: 3, ImportMode: "new_book", SourceFileName: "book.epub"}
}

func jobProgressForTest(value float64) *float64 {
	return &value
}

const jobProjectionRequestIDForTest = "00000000-0000-4000-8000-000000000904"

func mustJobResponseForTest(t *testing.T, job Job) jobResponse {
	t.Helper()
	response, err := jobResponseFromJob(job, jobProjectionRequestIDForTest)
	if err != nil {
		t.Fatal(err)
	}
	return response
}
