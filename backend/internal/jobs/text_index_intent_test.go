package jobs

import (
	"testing"
	"time"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/projects"
)

func TestCanonTextIndexIntentInputRejectsForeignSourceBeforeDatabase(t *testing.T) {
	const (
		projectID = "10000000-0000-4000-8000-000000000011"
		foreignID = "10000000-0000-4000-8000-000000000012"
	)
	view, err := governance.PresentSourceView(governance.SourceViewDraft)
	if err != nil {
		t.Fatal(err)
	}
	foreignRef, err := governance.NewViewBoundProjectRef(governance.SourceGenerationVector{
		ProjectID: foreignID, ProjectGeneration: 1,
	}, view, governance.AbsentChatArm())
	if err != nil {
		t.Fatal(err)
	}
	input := CanonTextIndexIntentInput{
		JobID: "10000000-0000-4000-8000-000000000013",
		Subject: CanonTextIndexSubject{
			Type: KindCanonTextIndex, ProjectID: projectID,
			SourceView: governance.SourceViewDraft, CatalogRevision: 1,
		},
		Source:    JobSourceManifest{SourceView: view, Refs: []governance.ArtifactSourceRef{foreignRef}},
		CreatedAt: time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC),
	}
	if err := input.Validate(); err != ErrValidation {
		t.Fatalf("Validate() error = %v, want ErrValidation", err)
	}
}

func TestCanonTextIndexIntentCancelProjectionRemainsActorScoped(t *testing.T) {
	const (
		projectID = "10000000-0000-4000-8000-000000000001"
		creatorID = "10000000-0000-4000-8000-000000000002"
	)
	job := Job{
		ID: "10000000-0000-4000-8000-000000000003", ProjectID: projectID,
		Kind: KindCanonTextIndex, Status: StatusQueued, CanCancel: true,
		Subject: CanonTextIndexSubject{
			Type: KindCanonTextIndex, ProjectID: projectID,
			SourceView: governance.SourceViewDraft, CatalogRevision: 1,
		},
	}

	if got := jobForActor(Actor{UserID: creatorID}, projects.RoleEditor, job, true); got.CanCancel {
		t.Fatal("service-created text intent exposed creator cancel shortcut")
	}
	if got := jobForActor(Actor{UserID: creatorID}, projects.RoleAdmin, job, true); !got.CanCancel {
		t.Fatal("admin lost text intent cancel capability")
	}

	job.CreatorUserID = creatorID
	if got := jobForActor(Actor{UserID: creatorID}, projects.RoleViewer, job, true); !got.CanCancel {
		t.Fatal("adopted creator lost readable-target cancel shortcut after downgrade")
	}
	if got := jobForActor(Actor{UserID: "10000000-0000-4000-8000-000000000004"}, projects.RoleEditor, job, true); got.CanCancel {
		t.Fatal("unrelated editor received creator cancel shortcut")
	}
}

func TestSystemIndexingCancelProjectionAllowsOnlyProjectManagers(t *testing.T) {
	const (
		projectID = "10000000-0000-4000-8000-000000000021"
		actorID   = "10000000-0000-4000-8000-000000000022"
	)
	for _, kind := range []Kind{KindCanonTextIndex, KindChapterCards, KindSagaLayer} {
		t.Run(string(kind), func(t *testing.T) {
			job := Job{
				ID: "10000000-0000-4000-8000-000000000023", ProjectID: projectID,
				Kind: kind, Status: StatusQueued, CanCancel: false,
				Subject: CanonTextIndexSubject{
					Type: KindCanonTextIndex, ProjectID: projectID,
					SourceView: governance.SourceViewDraft, CatalogRevision: 1,
				},
			}
			if kind == KindChapterCards {
				job.Subject = ChapterCardsSubject{Type: kind, ProjectID: projectID, SourceView: governance.SourceViewDraft}
			}
			if kind == KindSagaLayer {
				job.Subject = SagaLayerSubject{Type: kind, ProjectID: projectID, SourceView: governance.SourceViewDraft}
			}

			for _, role := range []projects.Role{projects.RoleOwner, projects.RoleAdmin} {
				if got := jobForActor(Actor{UserID: actorID}, role, job, true); !got.CanCancel {
					t.Fatalf("%s projection CanCancel = false for %s", kind, role)
				}
			}
			if got := jobForActor(Actor{UserID: actorID}, projects.RoleEditor, job, true); got.CanCancel {
				t.Fatalf("%s projection exposed editor cancellation", kind)
			}
		})
	}
	for _, kind := range CurrentKinds() {
		if kind == KindCanonTextIndex || kind == KindChapterCards || kind == KindSagaLayer {
			continue
		}
		job := Job{
			ID: "10000000-0000-4000-8000-000000000024", ProjectID: projectID,
			Kind: kind, Status: StatusQueued, CanCancel: false,
		}
		if got := jobForActor(Actor{UserID: actorID}, projects.RoleOwner, job, true); got.CanCancel {
			t.Fatalf("%s projection exposed system cancellation", kind)
		}
	}
}
