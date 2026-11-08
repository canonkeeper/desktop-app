package deletion

import (
	"testing"
	"time"

	"canonkeeper/backend/internal/governance"
)

const (
	testTombstoneID = "10000000-0000-4000-8000-000000000001"
	testActorID     = "20000000-0000-4000-8000-000000000001"
	testProjectID   = "30000000-0000-4000-8000-000000000001"
	testBookID      = "40000000-0000-4000-8000-000000000001"
	testChapterID   = "50000000-0000-4000-8000-000000000001"
)

func TestSourceDeleteRequestValidate(t *testing.T) {
	fencedAt := time.Date(2026, 7, 12, 12, 30, 0, 123456000, time.UTC)
	governed := SourceDeleteRequest{
		TombstoneID: testTombstoneID,
		ActorUserID: testActorID,
		ProjectID:   testProjectID,
		EntityKind:  EntityProject,
		EntityID:    testProjectID,
		FencedAt:    fencedAt,
		GovernedWindow: &GovernedSourceDeleteWindow{
			PurgeAfter:           fencedAt.Add(SourceRestoreWindow),
			DerivedPurgeDeadline: fencedAt.Add(DerivedPurgeDeadline),
			SourcePurgeDeadline:  fencedAt.Add(SourceRestoreWindow + SourceHardPurgeGrace),
		},
	}
	if err := governed.Validate(); err != nil {
		t.Fatalf("valid governed request: %v", err)
	}
	book := governed
	book.EntityKind = EntityBook
	book.EntityID = testBookID
	if err := book.Validate(); err != nil {
		t.Fatalf("valid book request: %v", err)
	}
	chapter := governed
	chapter.EntityKind = EntityChapter
	chapter.EntityID = testChapterID
	if err := chapter.Validate(); err != nil {
		t.Fatalf("valid chapter request: %v", err)
	}

	tests := map[string]func(*SourceDeleteRequest){
		"missing tombstone":         func(value *SourceDeleteRequest) { value.TombstoneID = "" },
		"noncanonical actor":        func(value *SourceDeleteRequest) { value.ActorUserID = "20000000-0000-4000-8000-00000000000A" },
		"missing project":           func(value *SourceDeleteRequest) { value.ProjectID = "" },
		"unknown entity":            func(value *SourceDeleteRequest) { value.EntityKind = "paragraph" },
		"project identity mismatch": func(value *SourceDeleteRequest) { value.EntityID = testBookID },
		"governed missing window":   func(value *SourceDeleteRequest) { value.GovernedWindow = nil },
		"wrong restore duration": func(value *SourceDeleteRequest) {
			value.GovernedWindow.PurgeAfter = value.GovernedWindow.PurgeAfter.Add(time.Microsecond)
		},
		"wrong derived deadline": func(value *SourceDeleteRequest) {
			value.GovernedWindow.DerivedPurgeDeadline = value.GovernedWindow.DerivedPurgeDeadline.Add(time.Microsecond)
		},
		"wrong source deadline": func(value *SourceDeleteRequest) {
			value.GovernedWindow.SourcePurgeDeadline = value.GovernedWindow.SourcePurgeDeadline.Add(time.Microsecond)
		},
		"non-UTC fence": func(value *SourceDeleteRequest) {
			value.FencedAt = value.FencedAt.In(time.FixedZone("UTC-alias", 0))
		},
		"sub-microsecond fence": func(value *SourceDeleteRequest) {
			value.FencedAt = value.FencedAt.Add(time.Nanosecond)
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			candidate := governed
			window := *governed.GovernedWindow
			candidate.GovernedWindow = &window
			mutate(&candidate)
			if err := candidate.Validate(); err == nil {
				t.Fatal("invalid source delete request accepted")
			}
		})
	}
}

func TestSourceFenceResultValidate(t *testing.T) {
	projectDeleted := governance.SourceGenerationVector{
		ProjectID: testProjectID, ProjectGeneration: 7,
	}
	bookDeleted := governance.SourceGenerationVector{
		ProjectID: testProjectID, ProjectGeneration: 7,
		BookID: testBookID, BookGeneration: 4,
	}
	chapterDeleted := governance.SourceGenerationVector{
		ProjectID: testProjectID, ProjectGeneration: 7,
		BookID: testBookID, BookGeneration: 4,
		ChapterID: testChapterID, ChapterGeneration: 11,
	}
	for name, deleted := range map[string]governance.SourceGenerationVector{
		"project": projectDeleted,
		"book":    bookDeleted,
		"chapter": chapterDeleted,
	} {
		t.Run(name, func(t *testing.T) {
			result := SourceFenceResult{
				TombstoneID: testTombstoneID,
				Deleted:     deleted,
				Current:     incrementSelectedGeneration(deleted),
			}
			if err := result.Validate(); err != nil {
				t.Fatalf("valid source fence result: %v", err)
			}
			result.Current.ProjectGeneration++
			if err := result.Validate(); err == nil {
				t.Fatal("source fence changed an ancestor or incremented the wrong counter")
			}
		})
	}

	invalid := SourceFenceResult{
		TombstoneID: "not-a-uuid",
		Deleted:     projectDeleted,
		Current:     incrementSelectedGeneration(projectDeleted),
	}
	if err := invalid.Validate(); err == nil {
		t.Fatal("invalid tombstone identity accepted")
	}
}

func TestSourceRestoreRequestValidate(t *testing.T) {
	request := SourceRestoreRequest{
		ActorUserID: testActorID,
		ProjectID:   testProjectID,
		EntityKind:  EntityChapter,
		EntityID:    testChapterID,
	}
	if err := request.Validate(); err != nil {
		t.Fatalf("valid restore request: %v", err)
	}
	request.EntityID = ""
	if err := request.Validate(); err == nil {
		t.Fatal("restore request without entity accepted")
	}
}

func TestSourceDeletionTargetOrderIsClosed(t *testing.T) {
	wantTargets := []governance.DeletionTarget{governance.TargetRuntime, governance.TargetFilesystem, governance.TargetPostgreSQL}
	gotTargets := SourceDeletionTargets()
	if len(gotTargets) != len(wantTargets) {
		t.Fatalf("target count = %d, want %d", len(gotTargets), len(wantTargets))
	}
	for index := range wantTargets {
		if gotTargets[index] != wantTargets[index] {
			t.Fatalf("target[%d] = %q, want %q", index, gotTargets[index], wantTargets[index])
		}
	}
	gotTargets[0] = governance.TargetPostgreSQL
	if SourceDeletionTargets()[0] != governance.TargetRuntime {
		t.Fatal("target registry was mutable")
	}
}

func incrementSelectedGeneration(vector governance.SourceGenerationVector) governance.SourceGenerationVector {
	if vector.ChapterID != "" {
		vector.ChapterGeneration++
	} else if vector.BookID != "" {
		vector.BookGeneration++
	} else {
		vector.ProjectGeneration++
	}
	return vector
}
